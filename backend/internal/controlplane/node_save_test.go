package controlplane

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNodeSaveReceiptReplay(t *testing.T) {
	for _, protocol := range []string{"vless", "hy2"} {
		t.Run(protocol, func(t *testing.T) {
			a := testApp(t)
			owner := testUser(t, a, "receipt-owner", "owner")
			input := Node{Protocol: protocol, Enabled: true, SaveRequestID: "node-save-request-0001"}
			first := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", input))
			if first.SaveRequestID != input.SaveRequestID || first.CreateRequestID != input.SaveRequestID || first.CreateRequestHash != "" {
				t.Fatal("missing public receipt or exposed fingerprint")
			}
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					got := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", input))
					if got.ID != first.ID {
						t.Error("retry created another node")
					}
				})
			}
			wg.Wait()
			nodes, _ := a.store.nodes()
			if len(nodes) != 3 {
				t.Fatal("duplicate nodes", len(nodes))
			}
			if protocol == "vless" {
				u, _ := a.store.record(owner.ID)
				if len(u.Credentials.VLESS) != 2 || u.Credentials.VLESS[first.ID] == "" {
					t.Fatal("missing or duplicate credentials")
				}
			}
			changed := first
			changed.SaveRequestID = "node-save-request-0002"
			changed.Enabled = false
			decodeNode(t, req(t, a, owner, "POST", "/api/nodes", changed))
			// Recreate the application to prove the receipt is persisted, not cached.
			restarted := &App{store: a.store, cfg: a.cfg}
			replay := decodeNode(t, req(t, restarted, owner, "POST", "/api/nodes", input))
			if replay.ID != first.ID || replay.Enabled || replay.CreateRequestID != input.SaveRequestID {
				t.Fatal("replay overwrote a later edit or lost the creation receipt")
			}
			input.Enabled = false
			if req(t, a, owner, "POST", "/api/nodes", input).Code != 409 {
				t.Fatal("same key accepted different input")
			}
			input.Enabled = true
			other := testUser(t, a, "receipt-other", "owner")
			if req(t, a, other, "POST", "/api/nodes", input).Code != 409 {
				t.Fatal("receipt not bound to actor")
			}
			if view := memberNodeState(first); view.SaveRequestID != "" || view.CreateRequestID != "" || view.CreateRequestHash != "" {
				t.Fatal("receipt leaked to member")
			}
		})
	}
}

func TestNodeSaveReceiptRollbackAndValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "receipt-rollback", "owner")
	input := Node{Protocol: "hy2", Enabled: true, SaveRequestID: "node-save-request-rollback"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.publicContext = ctx
	w := httptest.NewRecorder()
	// Simulate losing the controller after the middleware has admitted a write.
	a.saveNode(w, httptest.NewRequest("POST", "/api/nodes", strings.NewReader(string(jsonBytes(input)))), owner)
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	nodes, _ := a.store.nodes()
	if len(nodes) != 2 {
		t.Fatal("failed apply retained a creation receipt")
	}
	a.publicContext = nil
	decodeNode(t, req(t, a, owner, "POST", "/api/nodes", input))
	for _, invalid := range []string{"short", strings.Repeat("x", 81), "node-save-?/request"} {
		input.SaveRequestID = invalid
		if req(t, a, owner, "POST", "/api/nodes", input).Code != 400 {
			t.Fatal("invalid receipt accepted")
		}
	}
}

func TestConcurrentNodeCreationUsesOneReceipt(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "receipt-concurrent", "owner")
	input := Node{Protocol: "vless", Enabled: true, SaveRequestID: "node-save-request-concurrent"}
	start, ids := make(chan struct{}), make(chan string, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			<-start
			ids <- decodeNode(t, req(t, a, owner, "POST", "/api/nodes", input)).ID
		})
	}
	close(start)
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	nodes, _ := a.store.nodes()
	if len(unique) != 1 || len(nodes) != 3 {
		t.Fatal("concurrent retries created duplicates")
	}
}

func TestNodeSaveCredentialsCommitAtomically(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "receipt-atomic", "owner")
	_, err := a.store.db.Exec("CREATE TRIGGER fail_node_credentials BEFORE UPDATE OF credentials ON users BEGIN SELECT RAISE(ABORT, 'fixture'); END")
	if err != nil {
		t.Fatal(err)
	}
	w := req(t, a, owner, "POST", "/api/nodes", Node{Protocol: "vless", Enabled: true, SaveRequestID: "node-save-request-atomic"})
	if w.Code != 500 {
		t.Fatal(w.Code, w.Body.String())
	}
	nodes, _ := a.store.nodes()
	if len(nodes) != 2 {
		t.Fatal("failed credential assignment retained a node/receipt")
	}
}

func TestNodeSaveReceiptDoesNotRestartCore(t *testing.T) {
	n := Node{ID: "vless-fixture", Protocol: "vless", Enabled: true, Exit: "direct"}
	before := nodeHash([]Node{n}, "vless")
	n.SaveRequestID, n.CreateRequestID, n.CreateRequestHash = "request", "creation", "hash"
	if nodeHash([]Node{n}, "vless") != before {
		t.Fatal("receipt changed core configuration hash")
	}
}
