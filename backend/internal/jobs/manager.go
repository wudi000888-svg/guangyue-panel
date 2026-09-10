// Package jobs implements a bounded durable queue owned by one site controller.
// PostgreSQL/SQLite persist work; Redis is deliberately not the queue of record.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

var ErrBusy = errors.New("task capacity is busy")
var ErrFull = errors.New("task queue is full")

type Task struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Target   string          `json:"target"`
	Guard    string          `json:"-"`
	State    string          `json:"state"`
	ActorID  int64           `json:"-"`
	Created  int64           `json:"created"`
	Started  int64           `json:"started"`
	Finished int64           `json:"finished"`
	NextAt   int64           `json:"next_at"`
	Attempts int             `json:"attempts"`
	Error    string          `json:"error,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
}
type Handler func(context.Context, Task) (json.RawMessage, error)
type Options struct {
	Workers, Capacity int
	Timeout           time.Duration
	RetainHistory     func() bool
}
type Manager struct {
	db       *persistence.DB
	opts     Options
	handlers map[string]Handler
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	wake     chan struct{}
	running  bool
	failures chan error
}

func New(db *persistence.DB, opts Options) *Manager {
	if opts.Workers < 1 {
		opts.Workers = 1
	}
	if opts.Workers > 8 {
		opts.Workers = 8
	}
	if opts.Capacity < 1 {
		opts.Capacity = 64
	}
	if opts.Capacity > 1024 {
		opts.Capacity = 1024
	}
	if opts.Timeout <= 0 || opts.Timeout > 5*time.Minute {
		opts.Timeout = 2 * time.Minute
	}
	return &Manager{db: db, opts: opts, handlers: map[string]Handler{}, cancels: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), failures: make(chan error, 1)}
}

// Register is startup-only and must precede Run/Submit.
func (m *Manager) Register(kind string, h Handler) { m.handlers[kind] = h }
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) Submit(ctx context.Context, kind, target, guard string, actor int64) (Task, error) {
	if m.handlers[kind] == nil {
		return Task{}, errors.New("unsupported task type")
	}
	if len(target) > 200 || len(guard) > 128 {
		return Task{}, errors.New("invalid task target")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := kind + ":" + target
	var existing string
	rows, err := m.db.QueryContext(ctx, "SELECT id FROM tasks WHERE dedup_key=?", key)
	if err != nil {
		return Task{}, err
	}
	if rows.Next() {
		err = rows.Scan(&existing)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return Task{}, err
	}
	if existing != "" {
		return m.Get(ctx, existing)
	}
	var count int
	if err = m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE state IN ('queued','running')").Scan(&count); err != nil {
		return Task{}, err
	}
	if count >= m.opts.Capacity {
		return Task{}, ErrFull
	}
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return Task{}, err
	}
	id := hex.EncodeToString(b)
	now := time.Now().Unix()
	_, err = m.db.ExecContext(ctx, "INSERT INTO tasks(id,kind,target,guard,dedup_key,state,actor_id,created,next_at) VALUES(?,?,?,?,?,'queued',?,?,?)", id, kind, target, guard, key, actor, now, now)
	if err != nil {
		return Task{}, err
	}
	m.signal()
	return m.Get(ctx, id)
}

const columns = "id,kind,target,guard,state,actor_id,created,started,finished,next_at,attempts,error,result"

type scanner interface{ Scan(...any) error }

func scan(s scanner) (t Task, err error) {
	var b []byte
	err = s.Scan(&t.ID, &t.Kind, &t.Target, &t.Guard, &t.State, &t.ActorID, &t.Created, &t.Started, &t.Finished, &t.NextAt, &t.Attempts, &t.Error, &b)
	t.Result = b
	return
}
func (m *Manager) Get(ctx context.Context, id string) (Task, error) {
	return scan(m.db.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE id=?", id))
}
func (m *Manager) List(ctx context.Context) ([]Task, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT "+columns+" FROM tasks ORDER BY created DESC,id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		v.Result = nil
		out = append(out, v)
	}
	return out, rows.Err()
}
func (m *Manager) Cancel(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.db.ExecContext(ctx, "UPDATE tasks SET state='cancelled',finished=?,dedup_key=NULL,result=NULL WHERE id=? AND state IN ('queued','running')", time.Now().Unix(), id); err != nil {
		return err
	}
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
	}
	return nil
}
func (m *Manager) Recover(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, "UPDATE tasks SET state='queued',started=0,next_at=?,error='' WHERE state='running'", time.Now().Unix())
	return err
}
func (m *Manager) Run(ctx context.Context) (err error) {
	parent := ctx
	defer func() {
		// Cancellation can race a database claim/prune. A requested shutdown
		// must complete normally after workers persist their final state.
		if parent.Err() != nil && errors.Is(err, parent.Err()) {
			err = nil
		}
	}()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("task runner already running")
	}
	m.running = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.running = false; m.mu.Unlock() }()
	if err := m.Recover(ctx); err != nil {
		return err
	}
	slots := make(chan struct{}, m.opts.Workers)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		for len(slots) < cap(slots) && ctx.Err() == nil {
			task, found, err := m.claim(ctx)
			if err != nil {
				return err
			}
			if !found {
				break
			}
			slots <- struct{}{}
			wg.Add(1)
			go func(t Task) { defer wg.Done(); defer func() { <-slots; m.signal() }(); m.execute(ctx, t) }(task)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.Prune(ctx); err != nil {
				return err
			}
		case err := <-m.failures:
			return err
		case <-m.wake:
		}
	}
}
func (m *Manager) claim(ctx context.Context) (Task, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.db.QueryContext(ctx, "SELECT "+columns+" FROM tasks WHERE state='queued' AND next_at<=? ORDER BY created,id LIMIT 1", time.Now().Unix())
	if err != nil {
		return Task{}, false, err
	}
	if !rows.Next() {
		err = errors.Join(rows.Err(), rows.Close())
		return Task{}, false, err
	}
	t, err := scan(rows)
	err = errors.Join(err, rows.Close())
	if err != nil {
		return t, false, err
	}
	res, err := m.db.ExecContext(ctx, "UPDATE tasks SET state='running',started=?,attempts=attempts+1 WHERE id=? AND state='queued'", time.Now().Unix(), t.ID)
	if err != nil {
		return t, false, err
	}
	n, err := res.RowsAffected()
	t.Attempts++
	return t, n == 1, err
}
func (m *Manager) execute(parent context.Context, t Task) {
	ctx, cancel := context.WithTimeout(parent, m.opts.Timeout)
	m.mu.Lock()
	m.cancels[t.ID] = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); delete(m.cancels, t.ID); m.mu.Unlock() }()
	current, err := m.Get(ctx, t.ID)
	if err == nil && current.State != "running" {
		return
	}
	var result json.RawMessage
	// A claimed task must always reach finalization, even when shutdown cancels
	// the context before its initial read. Otherwise it stays running forever.
	if err == nil {
		if h := m.handlers[t.Kind]; h != nil {
			result, err = invoke(ctx, h, t)
		} else {
			err = errors.New("task type is no longer supported")
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if parent.Err() != nil {
		_, persistErr := m.db.ExecContext(finishCtx, "UPDATE tasks SET state='queued',next_at=?,error='' WHERE id=? AND state='running'", time.Now().Unix(), t.ID)
		m.report(persistErr)
		return
	}
	if errors.Is(err, ErrBusy) && t.Attempts < 6 {
		_, persistErr := m.db.ExecContext(finishCtx, "UPDATE tasks SET state='queued',next_at=?,error='等待资源空闲' WHERE id=? AND state='running'", time.Now().Add(time.Duration(1<<t.Attempts)*time.Second).Unix(), t.ID)
		m.report(persistErr)
		return
	}
	state, message := "succeeded", ""
	if err != nil {
		state = "failed"
		message = err.Error()
		if len(message) > 300 {
			message = "任务执行失败"
		}
	}
	if ctx.Err() != nil {
		state = "failed"
		message = "任务超时或已取消"
	}
	if len(result) > 1<<20 || len(result) > 0 && !json.Valid(result) {
		result = nil
		state = "failed"
		message = "任务结果超出限制"
	}
	_, persistErr := m.db.ExecContext(finishCtx, "UPDATE tasks SET state=?,finished=?,dedup_key=NULL,error=?,result=? WHERE id=? AND state='running'", state, time.Now().Unix(), message, []byte(result), t.ID)
	m.report(persistErr)
	m.report(m.Prune(finishCtx))
}
func (m *Manager) report(err error) {
	if err != nil {
		select {
		case m.failures <- errors.New("task persistence failed"):
		default:
		}
	}
}

// Prune also runs while idle so no_logs delivery results cannot linger indefinitely.
func (m *Manager) Prune(ctx context.Context) error {
	// no_logs retains a short result delivery window, without long-term history.
	retention := int64(24 * 3600)
	if m.opts.RetainHistory != nil && !m.opts.RetainHistory() {
		retention = 60
	}
	if _, err := m.db.ExecContext(ctx, "DELETE FROM tasks WHERE finished>0 AND finished<?", time.Now().Unix()-retention); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx, "DELETE FROM tasks WHERE id IN (SELECT id FROM tasks WHERE finished>0 ORDER BY finished DESC LIMIT 2147483647 OFFSET 100)")
	return err
}
func invoke(ctx context.Context, h Handler, t Task) (b json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			b = nil
			err = fmt.Errorf("任务执行异常")
		}
	}()
	return h(ctx, t)
}
