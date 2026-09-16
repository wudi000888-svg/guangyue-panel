package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func liveFixture(at time.Time, value int64) liveInput {
	return liveInput{at: at, traffic: [2]bool{true, true}, connections: [2]bool{true, true}, generation: [2]string{"x1", "hy1"}, counts: [2]map[int64]int64{{1: 3}, {1: 2, 2: 1}}, counters: []Counter{
		{Key: "x:a", Generation: "x1", UserID: 1, Protocol: "vless", Direction: "up", Value: value},
		{Key: "hy:a", Generation: "hy1", UserID: 1, Protocol: "hy2", Direction: "down", Value: value * 2},
	}}
}
func TestLiveRatesResetFailureAndRecovery(t *testing.T) {
	m := &liveMonitor{}
	at := time.Now()
	m.sample(liveFixture(at, 100000))
	if m.frames[0].Total.Upload != nil {
		t.Fatal("first sample must warm up")
	}
	m.sample(liveFixture(at.Add(5*time.Second), 100500))
	f := m.frames[1]
	if *f.Total.Upload != 100 || *f.Total.Download != 200 || *f.Total.VLESS != 3 || *f.Total.HY2 != 3 {
		t.Fatalf("wrong rates/counts: %+v", f)
	}
	if *frameUser(f, 2).Upload != 0 || *frameUser(f, 2).HY2 != 1 {
		t.Fatal("user isolation")
	}
	reset := liveFixture(at.Add(10*time.Second), 3)
	m.sample(reset)
	if m.frames[2].values[1].Upload != nil {
		t.Fatal("counter reset produced a rate")
	}
	restart := liveFixture(at.Add(15*time.Second), 99999)
	restart.generation[0] = "x2"
	m.sample(restart)
	if m.frames[3].Total.Upload != nil {
		t.Fatal("restart produced a spike")
	}
	failed := liveFixture(at.Add(20*time.Second), 100000)
	failed.traffic[1], failed.connections[1] = false, false
	m.sample(failed)
	if m.frames[4].Total.Upload != nil || m.frames[4].Total.HY2 != nil || *m.frames[4].Total.VLESS != 3 {
		t.Fatal("unknown must not be zero or hide healthy protocol")
	}
	m.sample(liveFixture(at.Add(25*time.Second), 101000))
	if m.frames[5].Total.Upload != nil {
		t.Fatal("recovery must warm up")
	}
	m.sample(liveFixture(at.Add(30*time.Second), 101500))
	if *m.frames[6].Total.Upload != 100 {
		t.Fatal("rates did not recover")
	}
	m.sample(liveFixture(at.Add(60*time.Second), 999999))
	if m.frames[7].Total.Upload != nil {
		t.Fatal("long gap reported as live")
	}
	m.sample(liveFixture(at.Add(10*time.Minute), 999999))
	if len(m.frames) != 1 {
		t.Fatal("history exceeded five minutes")
	}
}
func TestLiveAPIAccessFreshnessAndBoundedHistory(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	for _, actor := range []Record{{}, member} {
		w := req(t, a, actor, "GET", "/api/monitor", nil)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("monitor exposed to non-admin")
		}
	}
	now := time.Now()
	for i := 0; i < 100; i++ {
		a.monitor.sample(liveFixture(now.Add(time.Duration(i-99)*5*time.Second), int64(i)*500))
	}
	if len(a.monitor.frames) != liveFrames {
		t.Fatal("history not bounded")
	}
	w := req(t, a, owner, "GET", "/api/monitor?user_id=2&q=member", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), member.Credentials.HY2) || strings.Contains(w.Body.String(), member.Credentials.Token) {
		t.Fatal("bad monitor response or credentials exposed")
	}
	var response struct {
		Users    []struct{ ID int64 }
		Stale    bool
		Selected liveValue
		History  []object
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Stale || len(response.Users) != 1 || response.Users[0].ID != member.ID || *response.Selected.HY2 != 1 || len(response.History) != liveFrames {
		t.Fatalf("wrong response %s", w.Body.String())
	}
	a.monitor.sample(liveFixture(now.Add(-time.Minute), 0))
	w = req(t, a, owner, "GET", "/api/monitor", nil)
	json.Unmarshal(w.Body.Bytes(), &response)
	if !response.Stale || response.Selected.Upload != nil || response.Selected.VLESS != nil {
		t.Fatal("stale snapshot looked current")
	}
	for _, query := range []string{"?user_id=-1", "?user_id=bad", "?page=0", "?user_id=99999"} {
		if req(t, a, owner, "GET", "/api/monitor"+query, nil).Code < 400 {
			t.Fatal("invalid query accepted")
		}
	}
}
func TestLiveConcurrentReaders(t *testing.T) {
	m := &liveMonitor{}
	var wg sync.WaitGroup
	for n := 0; n < 3; n++ {
		wg.Go(func() {
			for i := 0; i < 100; i++ {
				m.RLock()
				if len(m.frames) > 0 {
					_ = frameUser(m.frames[len(m.frames)-1], 1)
				}
				m.RUnlock()
			}
		})
	}
	for i := 0; i < 100; i++ {
		m.sample(liveFixture(time.Now(), int64(i)))
	}
	wg.Wait()
}

type liveSlowWriter struct {
	entered, release chan struct{}
	header           http.Header
}

func (w *liveSlowWriter) Header() http.Header { return w.header }
func (w *liveSlowWriter) WriteHeader(int)     {}
func (w *liveSlowWriter) Write(b []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(b), nil
}

func TestLiveSlowBrowserDoesNotBlockSampler(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	w := &liveSlowWriter{entered: make(chan struct{}), release: make(chan struct{}), header: make(http.Header)}
	defer close(w.release)
	go a.liveAPI(w, httptest.NewRequest("GET", "/api/monitor", nil), owner)
	select {
	case <-w.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("response did not start")
	}
	done := make(chan struct{})
	go func() { a.monitor.sample(liveFixture(time.Now(), 100)); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("slow HTTP response blocked sampling")
	}
}
