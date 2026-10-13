package controlplane

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const deploymentMaintenanceFlag = "/var/lib/guangyue-maintenance/pending"

func maintenancePendingAt(path string, stat func(string) (os.FileInfo, error)) bool {
	_, err := stat(path)
	// Any flag, including an unreadable file, symlink or directory, fences the
	// application. Only confirmed absence allows data or external operations.
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func (a *App) maintenancePending() bool {
	path := deploymentMaintenanceFlag
	if a.maintenanceFlagPath != "" {
		path = a.maintenanceFlagPath
	}
	return maintenancePendingAt(path, os.Lstat)
}

func (a *App) maintenanceFrozen() bool {
	if !a.maintenancePending() || a.maintenanceActive.Load() != 0 || a.maintenanceRequests.Load() != 0 {
		return false
	}
	// These API-started jobs use the application lifetime, rather than a data
	// worker's lifetime. Do not claim they stopped just because cancellation was
	// requested; the installer also waits for the old process to exit.
	a.publicMu.Lock()
	public := a.publicCancel != nil
	a.publicMu.Unlock()
	a.mu.Lock()
	importing := a.importRunning != ""
	a.mu.Unlock()
	a.publicSourceMu.Lock()
	source := a.publicSourceID != ""
	a.publicSourceMu.Unlock()
	return !public && !importing && !source
}

func (a *App) maintenanceHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		health := r.Method == http.MethodGet && r.URL.Path == "/api/health"
		if !health {
			// Count before checking the flag so a request racing maintenance
			// cannot be omitted from the frozen acknowledgement.
			a.maintenanceRequests.Add(1)
			defer a.maintenanceRequests.Add(-1)
		}
		if a.maintenancePending() && !health {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "5")
			failure(w, http.StatusServiceUnavailable, "站点维护中，暂不接受新的操作，请稍后重试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// A maintenance generation owns all data/external workers. A flag cancels their
// contexts, but the next generation can start only after EVERY worker returns.
// Controller lease heartbeats and cache liveness remain outside this gate.
func maintenanceWorkerGroup(ctx context.Context, pending func() bool, work []func(context.Context), active *atomic.Int32) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for pending() {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		child, cancel := context.WithCancel(ctx)
		var workers sync.WaitGroup
		for _, run := range work {
			workers.Add(1)
			active.Add(1)
			go func(run func(context.Context)) {
				defer workers.Done()
				defer active.Add(-1)
				if child.Err() == nil && !pending() {
					run(child)
				}
			}(run)
		}
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		paused := false
	watch:
		for {
			select {
			case <-ctx.Done():
				break watch
			case <-done:
				break watch
			case <-ticker.C:
				if pending() {
					paused = true
					break watch
				}
			}
		}
		cancel()
		<-done
		if !paused {
			return
		}
	}
}
