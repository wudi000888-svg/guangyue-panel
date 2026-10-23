package controlplane

import "testing"

// Core counters are cumulative and come back on every 5-second tick; an
// unchanged reading must not rewrite checkpoints or node policies.
func TestAccountSkipsUnchangedCounters(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	counters := []Counter{
		{Key: "x:up", Generation: "g1", Protocol: "vless", Direction: "up", UserID: u.ID, NodeID: "vless-main", Value: 1000},
		{Key: "x:down", Generation: "g1", Protocol: "vless", Direction: "down", UserID: u.ID, NodeID: "vless-main", Value: 2000},
	}
	if err := a.store.account(counters); err != nil {
		t.Fatal(err)
	}
	changes := func() int64 {
		var n int64
		if err := a.store.db.QueryRow("SELECT total_changes()").Scan(&n); err != nil {
			t.Skip("total_changes() unavailable on this database")
		}
		return n
	}
	before := changes()
	if err := a.store.account(counters); err != nil {
		t.Fatal(err)
	}
	if written := changes() - before; written != 0 {
		t.Fatalf("unchanged tick wrote %d rows", written)
	}
	counters[0].Value = 1500
	if err := a.store.account(counters); err != nil {
		t.Fatal(err)
	}
	got, _ := a.store.record(u.ID)
	if got.Upload != 1500 || got.Download != 2000 {
		t.Fatalf("usage %d/%d after skipped tick", got.Upload, got.Download)
	}
}
