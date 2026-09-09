package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

type App struct {
	dnsGateway       *nodeGateway
	cfg              Config
	store            *Store
	mu               sync.Mutex
	realityProbeMu   sync.Mutex
	heavyMu          sync.Mutex
	importRunning    string
	importWG         sync.WaitGroup
	publicSourceMu   sync.Mutex
	publicSourceID   string
	probeMu          sync.Mutex
	speedMu          sync.Mutex
	qualityMu        sync.Mutex
	publicMu         sync.Mutex
	publicCancel     context.CancelFunc
	publicStatus     PublicStatus
	publicContext    context.Context
	publicWG         sync.WaitGroup
	publicStopping   bool
	bridgeMu         sync.Mutex
	bridge           *bridgeProcess
	status           string
	syncError        string
	trafficError     string
	appliedAt        int64
	lastUsers        string
	oldHYConnections int
	loginSlots       chan struct{}
	limits           map[string][]time.Time
	limitMu          sync.Mutex
	started          time.Time
}

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
func main() {
	// CLI setup and early configuration errors also honor the persisted mode.
	log.SetOutput(&runtimeLogWriter{dir: "/var/lib/guangyue", out: os.Stderr})
	cfgPath := flag.String("config", "/etc/guangyue-personal.json", "configuration file")
	managedService := flag.String("run-core", "", "run a fixed managed service with runtime log control")
	init := flag.Bool("init", false, "initialize configuration")
	prepare := flag.Bool("prepare", false, "initialize state and render core configurations")
	restore := flag.String("restore", "", "restore a backup while services are stopped")
	flag.Parse()
	if *managedService != "" {
		if *cfgPath != "/etc/guangyue-personal.json" || *init || *prepare || *restore != "" || flag.NArg() != 0 {
			os.Exit(2)
		}
		os.Exit(runRuntimeService(*managedService))
	}
	if *init {
		if err := initConfig(*cfgPath); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	log.SetOutput(&runtimeLogWriter{dir: filepath.Clean(cfg.StateDir), out: os.Stderr})
	if *restore != "" {
		if err = restoreBackup(cfg, *restore); err != nil {
			log.Fatal(err)
		}
		return
	}
	debug.SetMemoryLimit(48 << 20)
	store, err := openStore(cfg.StateDir)
	if err != nil {
		log.Fatal(err)
	}
	defer store.db.Close()
	if err = store.bootstrap(cfg.StateDir); err != nil {
		log.Fatal(err)
	}
	if err = store.migratePublicSubscriptions(); err != nil {
		log.Fatal(err)
	}
	if err = store.migratePools(); err != nil {
		log.Fatal(err)
	}
	if err = store.ensureDefaultDirectNodes(); err != nil {
		log.Fatal(err)
	}
	a := &App{cfg: cfg, store: store, status: "pending", started: time.Now(), loginSlots: make(chan struct{}, 2), limits: map[string][]time.Time{}}
	defer a.stopBridge()
	defer a.stopNodeGateway()
	if *prepare {
		if err = a.prepare(); err != nil {
			log.Fatal(err)
		}
		if err = store.markPreparedNodes(); err != nil {
			log.Fatal(err)
		}
		return
	}
	public := &http.Server{Addr: cfg.Listen, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	internalMux := http.NewServeMux()
	internalMux.HandleFunc("POST /auth", a.hyAuth)
	internal := &http.Server{Addr: cfg.InternalListen, Handler: internalMux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 4 * time.Second, WriteTimeout: 8 * time.Second, IdleTimeout: 20 * time.Second, MaxHeaderBytes: 4 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.publicContext = ctx
	defer a.stopPublic()
	go func() {
		if err := internal.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Print("internal listener failed")
			stop()
		}
	}()
	go func() {
		if err := public.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Print("public listener failed")
			stop()
		}
	}()
	var workers sync.WaitGroup
	startWorker := func(work func(context.Context)) {
		workers.Add(1)
		go func() { defer workers.Done(); work(ctx) }()
	}
	defer workers.Wait()
	startWorker(a.loop)
	a.importWG.Add(1)
	go func() { defer a.importWG.Done(); a.importSourceLoop(ctx) }()
	a.importWG.Add(1)
	go func() { defer a.importWG.Done(); a.publicSourceLoop(ctx) }()
	defer a.importWG.Wait()
	if !cfg.Dev {
		startWorker(a.egressLoop)
		startWorker(a.publicLoop)
	}
	log.Printf("Guangyue Panel %s listening on %s", version, cfg.Listen)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := public.Shutdown(shutdown); err != nil {
		_ = public.Close()
	}
	if err := internal.Shutdown(shutdown); err != nil {
		_ = internal.Close()
	}
}
func (a *App) loop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		if err := a.collect(); err != nil {
			a.trafficError = err.Error()
		} else {
			a.trafficError = ""
		}
		err := a.reconcile()
		if err != nil {
			a.status = "error"
			a.syncError = err.Error()
		} else {
			a.status = "applied"
			a.syncError = ""
			a.appliedAt = time.Now().Unix()
		}
		_, _ = a.store.db.Exec("DELETE FROM sessions WHERE expires<?", time.Now().Unix())
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
