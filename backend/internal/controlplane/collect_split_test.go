package controlplane

import (
	"os"
	"path/filepath"
	"testing"
)

// A traffic read that spans a core restart would book the old process's
// totals against the new generation and double-bill users, so it must be
// discarded; counters are cumulative and the next read recovers them.
func TestGatherCountersDiscardsReadAcrossRestart(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	a.cfg.Dev = false
	script := "#!/bin/sh\necho '{\"stat\":[{\"name\":\"user>>>" + email(u.ID, "vless-main") + ">>>traffic>>>uplink\",\"value\":\"4096\"}]}'\n"
	a.cfg.Xray = filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(a.cfg.Xray, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	original := corePID
	t.Cleanup(func() { corePID = original })
	pids := []int{}
	corePID = func(string) int {
		if len(pids) == 0 {
			return 100
		}
		next := pids[0]
		pids = pids[1:]
		return next
	}

	pids = []int{100, 100, 0, 0}
	if sample := a.gatherCounters(); len(sample.counters) != 1 || !sample.live.traffic[0] {
		t.Fatalf("stable read lost counters: %+v", sample.counters)
	}
	pids = []int{100, 200, 0, 0}
	sample := a.gatherCounters()
	if len(sample.counters) != 0 || sample.live.traffic[0] || len(sample.errs) == 0 {
		t.Fatalf("read across an Xray restart was kept: %+v", sample.counters)
	}
}

func TestLoopDropsSampleWhenAnotherCollectWasBooked(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	seq := a.collectSeq
	if err := a.applyCounters(counterSample{}); err != nil {
		t.Fatal(err)
	}
	if a.collectSeq == seq {
		t.Fatal("applied sample did not advance the sequence")
	}
	a.mu.Unlock()
}
