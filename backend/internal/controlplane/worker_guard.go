package controlplane

import (
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// safeCall runs fn and turns a panic into an error, so one malformed RPC
// answer or record cannot take the whole panel down (a panic in a background
// goroutine otherwise kills the process, and repeats on every restart).
func safeCall(name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("%s panicked: %v\n%s", name, r, debug.Stack())
			err = fmt.Errorf("%s 内部错误，已记录日志", name)
		}
	}()
	return fn()
}

// workerReport keeps the latest error of each background worker. Errors were
// previously discarded, so a stuck receipt or sweep left no trace.
type workerReport struct {
	mu     sync.Mutex
	status map[string]workerStatus
}

type workerStatus struct {
	Error  string `json:"error"`
	Since  int64  `json:"since"`
	logged time.Time
}

// workerLogEvery bounds how often an unchanged error is logged again.
const workerLogEvery = 10 * time.Minute

// runWorker executes one worker iteration with panic protection and records
// its outcome.
func (a *App) runWorker(name string, fn func() error) {
	a.workers.record(name, safeCall(name, fn))
}

func (r *workerReport) record(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == nil {
		r.status = map[string]workerStatus{}
	}
	if err == nil {
		if previous, ok := r.status[name]; ok {
			log.Printf("%s recovered after: %s", name, previous.Error)
			delete(r.status, name)
		}
		return
	}
	now := time.Now()
	s, ok := r.status[name]
	if !ok || s.Error != err.Error() {
		s = workerStatus{Error: err.Error(), Since: now.Unix()}
	}
	if s.logged.IsZero() || now.Sub(s.logged) >= workerLogEvery {
		log.Printf("%s failed: %s", name, s.Error)
		s.logged = now
	}
	r.status[name] = s
}

// snapshot lists workers that are currently failing, for the owner's view.
func (r *workerReport) snapshot() map[string]workerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]workerStatus, len(r.status))
	for name, s := range r.status {
		out[name] = s
	}
	return out
}
