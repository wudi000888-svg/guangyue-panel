package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

func testManager(t *testing.T, capacity int) *Manager {
	t.Helper()
	opts := persistence.Options{}
	if strings.HasPrefix(t.Name(), "TestPostgresQueue/") {
		opts = persistence.Options{Driver: "postgres", DSN: os.Getenv("GY_TEST_POSTGRES_DSN"), SiteID: fmt.Sprintf("queue_%x", time.Now().UnixNano())}
	}
	d, err := persistence.Open(context.Background(), t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if opts.Driver == "postgres" {
			_, _ = d.Exec("DROP SCHEMA " + d.Schema() + " CASCADE")
		}
		d.Close()
	})
	return New(d, Options{Workers: 1, Capacity: capacity, Timeout: time.Second})
}
func runManager(t *testing.T, m *Manager) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	})
	return cancel
}
func awaitState(t *testing.T, m *Manager, id, state string) Task {
	t.Helper()
	limit := time.Now().Add(15 * time.Second)
	for time.Now().Before(limit) {
		v, err := m.Get(context.Background(), id)
		if err == nil && v.State == state {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := m.Get(context.Background(), id)
	t.Fatalf("expected %s, got %+v", state, v)
	return v
}
func TestQueueDedupCapacityAndCancellation(t *testing.T) {
	m := testManager(t, 1)
	m.Register("hold", func(ctx context.Context, _ Task) (json.RawMessage, error) { <-ctx.Done(); return nil, ctx.Err() })
	ctx := context.Background()
	one, err := m.Submit(ctx, "hold", "first", "v1", 1)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Submit(ctx, "hold", "first", "v1", 1)
	if err != nil || again.ID != one.ID {
		t.Fatal("duplicate work", err)
	}
	if _, err = m.Submit(ctx, "hold", "second", "v1", 1); !errors.Is(err, ErrFull) {
		t.Fatal("unbounded queue", err)
	}
	runManager(t, m)
	awaitState(t, m, one.ID, "running")
	if err = m.Cancel(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	awaitState(t, m, one.ID, "cancelled")
}
func TestQueueRecoveryAndDelivery(t *testing.T) {
	m := testManager(t, 4)
	m.Register("work", func(_ context.Context, t Task) (json.RawMessage, error) {
		if t.Guard != "v1" {
			return nil, errors.New("guard lost")
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	task, err := m.Submit(context.Background(), "work", "node", "v1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec("UPDATE tasks SET state='running' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	runManager(t, m)
	v := awaitState(t, m, task.ID, "succeeded")
	if string(v.Result) != `{"ok":true}` {
		t.Fatal("result not persisted")
	}
	next, err := m.Submit(context.Background(), "work", "node", "v2", 2)
	if err != nil || next.ID == task.ID {
		t.Fatal("dedup slot not released", err)
	}
	awaitState(t, m, next.ID, "failed")
}
func TestQueueShutdownRequeuesWork(t *testing.T) {
	m := testManager(t, 4)
	m.Register("hold", func(ctx context.Context, _ Task) (json.RawMessage, error) { <-ctx.Done(); return nil, ctx.Err() })
	task, err := m.Submit(context.Background(), "hold", "node", "v1", 2)
	if err != nil {
		t.Fatal(err)
	}
	cancel := runManager(t, m)
	awaitState(t, m, task.ID, "running")
	cancel()
	awaitState(t, m, task.ID, "queued")
}

func TestNoLogsPrunesIdleResults(t *testing.T) {
	m := testManager(t, 4)
	m.opts.RetainHistory = func() bool { return false }
	m.Register("work", func(context.Context, Task) (json.RawMessage, error) { return nil, nil })
	task, err := m.Submit(context.Background(), "work", "old", "v1", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec("UPDATE tasks SET state='succeeded',finished=?,dedup_key=NULL,result=? WHERE id=?", time.Now().Unix()-61, []byte(`{"private":true}`), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	runManager(t, m)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = m.Get(context.Background(), task.ID); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idle no_logs result was retained")
}

func TestPostgresQueue(t *testing.T) {
	if os.Getenv("GY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL integration not configured")
	}
	t.Run("dedup_cancel", TestQueueDedupCapacityAndCancellation)
	t.Run("recovery_delivery", TestQueueRecoveryAndDelivery)
	t.Run("shutdown", TestQueueShutdownRequeuesWork)
}
