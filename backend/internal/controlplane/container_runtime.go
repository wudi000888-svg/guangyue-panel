package controlplane

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/coreprocess"
)

var containerManagedServices = []string{"panel", "xray", "hy2"}

func containerWrapperCommand(name string) (*exec.Cmd, error) {
	if _, _, err := runtimeServiceCommand(name); err != nil {
		return nil, err
	}
	cmd := exec.Command("/opt/guangyue-personal/bin/guangyue", "-run-core", name)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GUANGYUE_CONTAINER=") && !strings.HasPrefix(v, "GUANGYUE_LOG_WRAPPER=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GUANGYUE_CONTAINER=1")
	return cmd, nil
}

type containerSupervisor struct {
	command    func(string) (*exec.Cmd, error)
	out        io.Writer
	errOut     io.Writer
	retry      time.Duration
	stopGrace  time.Duration
	started    func(string, int)
	stopSignal func(int, syscall.Signal) error
}

func (s containerSupervisor) run(ctx context.Context) error {
	if s.command == nil {
		return errors.New("missing fixed container command factory")
	}
	var workers sync.WaitGroup
	for _, name := range containerManagedServices {
		workers.Add(1)
		go func(name string) { defer workers.Done(); s.runService(ctx, name) }(name)
	}
	workers.Wait()
	return nil
}

func (s containerSupervisor) runService(ctx context.Context, name string) {
	retry := s.retry
	if retry <= 0 {
		retry = time.Second
	}
	for ctx.Err() == nil {
		cmd, err := s.command(name)
		if err == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Stdout, cmd.Stderr = s.out, s.errOut
			cmd.WaitDelay = 2 * time.Second
			err = cmd.Start()
			if err == nil {
				if s.started != nil {
					s.started(name, cmd.Process.Pid)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case err = <-done:
				case <-ctx.Done():
					s.stop(cmd.Process.Pid, done)
					return
				}
				// The wrapper may have crashed before reaping its child. All of
				// its descendants share this dedicated PGID in container mode.
				s.signalGroup(cmd.Process.Pid, syscall.SIGKILL)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if s.errOut != nil {
			// No command, config, secret or child output is interpolated here.
			_, _ = io.WriteString(s.errOut, "managed "+name+" stopped; restarting\n")
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s containerSupervisor) signalGroup(pid int, sig syscall.Signal) {
	if s.stopSignal != nil {
		_ = s.stopSignal(pid, sig)
	} else {
		_ = syscall.Kill(-pid, sig)
	}
}

func (s containerSupervisor) stop(pid int, done <-chan error) {
	s.signalGroup(pid, syscall.SIGTERM)
	grace := s.stopGrace
	if grace <= 0 {
		grace = 14 * time.Second
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		// Reap through Cmd.Wait, then terminate any lingering descendants.
		s.signalGroup(pid, syscall.SIGKILL)
	case <-timer.C:
		s.signalGroup(pid, syscall.SIGKILL)
		<-done
	}
}

func containerRuntimeLock(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("container runtime directory must be private")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("container runtime directory owner mismatch")
	}
	f, err := os.OpenFile(filepath.Join(dir, "supervisor.lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	lockInfo, err := f.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm()&0077 != 0 {
		_ = f.Close()
		return nil, errors.New("container supervisor lock must be private")
	}
	owner, ok := lockInfo.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return nil, errors.New("container supervisor lock owner mismatch")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("container runtime already has a supervisor")
	}
	return f, nil
}

func runContainerRuntime() int {
	if !coreprocess.ContainerMode() || os.Geteuid() == 0 {
		log.Print("container runtime requires GUANGYUE_CONTAINER=1 and a non-root service user")
		return 2
	}
	cfg, err := loadConfig("/etc/guangyue-personal.json")
	if err != nil || cfg.StateDir != "/var/lib/guangyue" {
		log.Print("container runtime requires the fixed application configuration and state directory")
		return 2
	}
	lock, err := containerRuntimeLock(coreprocess.ContainerRuntimeDir)
	if err != nil {
		log.Print("container runtime could not acquire its private supervisor lock")
		return 1
	}
	defer lock.Close()
	// Children inherit their own persisted resource limits; this limit applies
	// only to the small supervisor and certificate watcher.
	debug.SetMemoryLimit(24 << 20)
	runtime.GOMAXPROCS(1)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := &runtimeLogWriter{dir: cfg.StateDir, out: os.Stdout}
	errOut := &runtimeLogWriter{dir: cfg.StateDir, out: os.Stderr}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchContainerCertificate(ctx, cfg.Cert, cfg.CertKey, 5*time.Second, func() error {
			return coreprocess.Restart("guangyue-hy2.service")
		})
	}()
	err = (containerSupervisor{command: containerWrapperCommand, out: out, errOut: errOut}).run(ctx)
	stop()
	<-watchDone
	if err != nil {
		return 1
	}
	return 0
}

func containerCertificateFingerprint(cert, key string) ([32]byte, error) {
	var empty [32]byte
	read := func(path string) ([]byte, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err != nil || len(b) > 1<<20 {
			return nil, errors.New("certificate file is too large or unreadable")
		}
		return b, nil
	}
	certPEM, err := read(cert)
	if err != nil {
		return empty, err
	}
	keyPEM, err := read(key)
	if err != nil {
		return empty, err
	}
	if _, err = tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return empty, errors.New("certificate and key do not match")
	}
	h := sha256.New()
	_, _ = h.Write(certPEM)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(keyPEM)
	return [32]byte(h.Sum(nil)), nil
}

func watchContainerCertificate(ctx context.Context, cert, key string, interval time.Duration, restart func() error) {
	previous, err := containerCertificateFingerprint(cert, key)
	valid := err == nil
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, e := containerCertificateFingerprint(cert, key)
			if e != nil || valid && current == previous {
				continue
			}
			if restart() == nil {
				previous = current
				valid = true
			}
		}
	}
}
