package controlplane

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaintenanceFlagIsPersistentAndFailsClosed(t *testing.T) {
	if deploymentMaintenanceFlag != "/var/lib/guangyue-maintenance/pending" {
		t.Fatal("maintenance fence must survive a host reboot")
	}
	for _, row := range []struct {
		name    string
		err     error
		pending bool
	}{
		{"absent", os.ErrNotExist, false},
		{"wrapped-absent", &os.PathError{Op: "lstat", Path: "pending", Err: os.ErrNotExist}, false},
		{"exists", nil, true},
		{"permission", os.ErrPermission, true},
		{"io-failure", errors.New("filesystem unavailable"), true},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := maintenancePendingAt("pending", func(string) (os.FileInfo, error) { return nil, row.err }); got != row.pending {
				t.Fatalf("pending = %v, want %v", got, row.pending)
			}
		})
	}
	flag := filepath.Join(t.TempDir(), "pending")
	a := &App{maintenanceFlagPath: flag}
	if a.maintenancePending() {
		t.Fatal("an absent fence changed legacy behavior")
	}
	// Lstat refuses to mistake a dangling symlink for confirmed absence.
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), flag); err != nil {
		t.Fatal(err)
	}
	if !a.maintenancePending() {
		t.Fatal("dangling maintenance symlink did not fail closed")
	}
}

func TestMaintenanceBlocksAllPublicAndInternalOperationsExceptHealth(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "pending")
	if err := os.WriteFile(flag, []byte("transaction"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{maintenanceFlagPath: flag}
	var called atomic.Int32
	handler := a.maintenanceHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(204)
	}))
	for _, row := range []struct{ method, path string }{
		{"POST", "/auth"}, {"POST", "/api/auth/login"}, {"POST", "/api/payments/callback"},
		{"GET", "/sub/token"}, {"GET", "/index.html"}, {"GET", "/api/users"},
		{"HEAD", "/api/health"}, {"POST", "/api/health"}, {"GET", "/api/health/"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(row.method, row.path, nil))
		if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Retry-After") != "5" {
			t.Fatalf("%s %s bypassed maintenance: %d", row.method, row.path, response.Code)
		}
	}
	if called.Load() != 0 || a.maintenanceRequests.Load() != 0 {
		t.Fatal("fenced requests reached application code or leaked an in-flight count")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/health", nil))
	if response.Code != 204 || called.Load() != 1 {
		t.Fatal("GET health cannot probe a provisional deployment")
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/auth", nil))
	if response.Code != 204 || called.Load() != 2 {
		t.Fatal("clearing maintenance did not restore the original handler")
	}
}

func TestMaintenanceFrozenWaitsForInFlightRequestsAndAsyncJobs(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "pending")
	a := &App{maintenanceFlagPath: flag}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := a.maintenanceHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(204)
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/payments", nil))
	}()
	<-entered
	if err := os.WriteFile(flag, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if a.maintenanceFrozen() {
		t.Fatal("maintenance acknowledged before an in-flight operation completed")
	}
	close(release)
	<-done
	if !a.maintenanceFrozen() {
		t.Fatal("finished request was still counted as an active operation")
	}
	a.maintenanceActive.Store(1)
	if a.maintenanceFrozen() {
		t.Fatal("active data worker was omitted from the frozen acknowledgement")
	}
	a.maintenanceActive.Store(0)
	a.publicCancel = func() {}
	if a.maintenanceFrozen() {
		t.Fatal("an API-started public collection job was omitted")
	}
	a.publicCancel = nil
	a.importRunning = "subscription-import"
	if a.maintenanceFrozen() {
		t.Fatal("an in-flight import was omitted")
	}
	a.importRunning = ""
	a.publicSourceID = "public-source"
	if a.maintenanceFrozen() {
		t.Fatal("an in-flight source fetch was omitted")
	}
}

func TestMaintenanceWorkersStartOnlyAfterCommitAndNeverOverlapGenerations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pending atomic.Bool
	pending.Store(true)
	var active, started, heartbeat atomic.Int32
	firstCancelled, releaseFirst, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	worker := func(ctx context.Context) {
		generation := started.Add(1)
		<-ctx.Done()
		if generation == 1 {
			close(firstCancelled)
			<-releaseFirst // Models a signer/provider persisting its last operation.
		}
	}
	go func() {
		defer close(done)
		maintenanceWorkerGroup(ctx, pending.Load, []func(context.Context){worker}, &active)
	}()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				heartbeat.Add(1)
			}
		}
	}()
	maintenanceWaitFor(t, func() bool { return heartbeat.Load() > 2 })
	if started.Load() != 0 || active.Load() != 0 {
		t.Fatal("provisional application started data/financial workers before commit")
	}
	pending.Store(false)
	maintenanceWaitFor(t, func() bool { return started.Load() == 1 })
	pending.Store(true)
	select {
	case <-firstCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not cancel a running worker")
	}
	if active.Load() != 1 {
		t.Fatal("cancellation was incorrectly treated as actual completion")
	}
	pending.Store(false)
	time.Sleep(220 * time.Millisecond)
	if started.Load() != 1 {
		t.Fatal("new worker generation overlapped an unfinished signer/provider")
	}
	close(releaseFirst)
	maintenanceWaitFor(t, func() bool { return started.Load() == 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker group did not wait for bounded shutdown")
	}
	<-heartbeatDone
	if active.Load() != 0 {
		t.Fatal("finished generation leaked an active worker count")
	}
}

func maintenanceWaitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("maintenance state did not converge")
}
