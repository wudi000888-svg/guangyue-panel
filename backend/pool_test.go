package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodePool(t *testing.T, w *httptest.ResponseRecorder) IPResource {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("IP operation failed: %d %s", w.Code, w.Body.String())
	}
	var p IPResource
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func testSharedProxy(t *testing.T, a *App) IPResource {
	t.Helper()
	nodes, _ := a.store.nodes()
	p := resourceFromNode(Node{Protocol: "vless", Exit: "http", Host: "203.0.113.40", Port: 8080, Enabled: true})
	for i := range nodes {
		nodes[i] = bindPool(nodes[i], p)
	}
	if err := a.store.savePoolNodes([]IPResource{p}, nodes); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPoolMigrationPreservesRoutesAndCredentials(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "owner", "owner")
	nodes, _ := a.store.nodes()
	for i := range nodes {
		nodes[i].ExitID = ""
		nodes[i].Exit, nodes[i].Host, nodes[i].Port = "http", "203.0.113.40", 8080
		if err := a.store.saveNode(nodes[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.db.Exec("DELETE FROM ip_pool"); err != nil {
		t.Fatal(err)
	}
	before, _, _ := subscription(a.cfg, u, nodes, "raw", "")
	xhash, hhash := nodeHash(nodes, "vless"), nodeHash(nodes, "hy2")
	for i := 0; i < 2; i++ {
		if err := a.store.migratePools(); err != nil {
			t.Fatal(err)
		}
	}
	pools, _ := a.store.pools()
	updated, _ := a.store.nodes()
	if len(pools) != 1 || updated[0].ExitID == "" || updated[0].ExitID != updated[1].ExitID {
		t.Fatal("migration did not deduplicate the shared exit")
	}
	after, _, _ := subscription(a.cfg, u, updated, "raw", "")
	if !bytes.Equal(before, after) || nodeHash(updated, "vless") != xhash || nodeHash(updated, "hy2") != hhash {
		t.Fatal("migration changed credentials or rendered route hashes")
	}
}

func TestPoolAuthorizationEncryptionAndDuplicateProtection(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	input := IPResource{Node: Node{Exit: "http", Host: "203.0.113.10", Port: 8080, Username: "pool-user", Password: "private-pool-password", Enabled: true, Name: "伪造国家", ProbeIP: "8.8.8.8"}, Label: "主出口"}
	for _, path := range []string{"/api/ips", "/api/ips/missing/detect"} {
		if w := req(t, a, user, "POST", path, input); w.Code != 403 {
			t.Fatal("ordinary user can manage IP pool")
		}
	}
	if req(t, a, user, "GET", "/api/ips", nil).Code != 403 {
		t.Fatal("ordinary user can list IP pool")
	}
	w := req(t, a, owner, "POST", "/api/ips", input)
	p := decodePool(t, w)
	if strings.Contains(w.Body.String(), input.Password) || p.Password != "" || !p.HasPassword || p.ProbeIP != "" || p.Name == "伪造国家" {
		t.Fatal("IP response exposes credentials or trusts supplied metadata")
	}
	var encrypted []byte
	if err := a.store.db.QueryRow("SELECT doc FROM ip_pool WHERE id=?", p.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(input.Password)) || bytes.Contains(encrypted, []byte(input.Host)) {
		t.Fatal("pool credentials were persisted in plaintext")
	}
	if req(t, a, owner, "POST", "/api/ips", input).Code != 409 {
		t.Fatal("duplicate transport accepted")
	}
	w = req(t, a, user, "GET", "/api/state", nil)
	if strings.Contains(w.Body.String(), p.ID) || strings.Contains(w.Body.String(), input.Host) {
		t.Fatal("IP pool leaked to ordinary member")
	}
}

func TestSharedPoolSelectionUpdatesBothCoresAndProtectsBindings(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	originalUser, _ := a.store.record(owner.ID)
	p := decodePool(t, req(t, a, owner, "POST", "/api/ips", IPResource{Node: Node{Exit: "socks5", Host: "203.0.113.11", Port: 1080, Username: "proxy-user", Password: "proxy-pass", Enabled: true}, Label: "共享出口"}))
	nodes, _ := a.store.nodes()
	for _, n := range nodes {
		// The selected pool must override transport fields supplied by the client.
		w := req(t, a, owner, "POST", "/api/nodes", Node{ID: n.ID, Protocol: n.Protocol, Enabled: n.Enabled, ExitID: p.ID, Exit: "direct", Host: "untrusted.invalid"})
		if w.Code != 200 {
			t.Fatalf("selection failed: %d %s", w.Code, w.Body.String())
		}
	}
	for _, file := range []string{"xray.json", "hy2.json"} {
		b, err := os.ReadFile(filepath.Join(a.cfg.StateDir, file))
		if err != nil || !bytes.Contains(b, []byte("203.0.113.11")) || bytes.Contains(b, []byte("untrusted.invalid")) {
			t.Fatalf("%s did not render selected exit", file)
		}
	}
	p.Host = "203.0.113.12"
	p = decodePool(t, req(t, a, owner, "POST", "/api/ips", p))
	if len(p.NodeIDs) != 2 {
		t.Fatal("shared resource did not report both bindings")
	}
	updated, _ := a.store.nodes()
	for _, n := range updated {
		if n.Host != p.Host || n.Password != "proxy-pass" || n.ExitID != p.ID {
			t.Fatal("shared edit was not propagated or lost the saved password")
		}
	}
	for _, file := range []string{"xray.json", "hy2.json"} {
		b, _ := os.ReadFile(filepath.Join(a.cfg.StateDir, file))
		if !bytes.Contains(b, []byte("203.0.113.12")) {
			t.Fatal("updated exit did not reach rendered core config")
		}
	}
	p.Enabled = false
	if req(t, a, owner, "POST", "/api/ips", p).Code != 409 {
		t.Fatal("in-use resource can be disabled via the single edit API")
	}
	for _, n := range nodes {
		if req(t, a, owner, "POST", "/api/nodes", Node{ID: n.ID, Protocol: n.Protocol, Enabled: n.Enabled, Exit: "direct"}).Code != 200 {
			t.Fatal("switching back failed")
		}
	}
	if req(t, a, owner, "DELETE", "/api/ips/"+p.ID, nil).Code != 200 {
		t.Fatal("unassigned pool cannot be deleted")
	}
	u, _ := a.store.record(owner.ID)
	b1, _ := json.Marshal(originalUser.Credentials)
	b2, _ := json.Marshal(u.Credentials)
	if !bytes.Equal(b1, b2) {
		t.Fatal("exit selection rotated member credentials")
	}
}

func TestPoolMetadataRefreshAndStaleResults(t *testing.T) {
	a := testApp(t)
	p := testSharedProxy(t, a)
	nodes, _ := a.store.nodes()
	before := nodeHash(nodes, "vless") + nodeHash(nodes, "hy2")
	a.mu.Lock()
	_, err := a.storePoolEgressLocked(p, Egress{IP: "203.0.113.10", Country: "新加坡", CountryCode: "SG"}, nil)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ = a.store.nodes()
	if before != nodeHash(nodes, "vless")+nodeHash(nodes, "hy2") {
		t.Fatal("pool metadata refresh restarts cores")
	}
	for _, n := range nodes {
		if n.Name != "新加坡 · 203.0.113.10" {
			t.Fatal("metadata was not synchronized across bindings")
		}
	}
	p, _ = a.store.pool(p.ID)
	newer := p
	newer.Revision += "-new"
	if err = a.store.savePoolNodes([]IPResource{newer}, nil); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	_, err = a.storePoolEgressLocked(p, Egress{IP: "8.8.8.8", Country: "美国", CountryCode: "US"}, nil)
	a.mu.Unlock()
	if err == nil {
		t.Fatal("stale check overwrote edited resource")
	}
	current, _ := a.store.pool(p.ID)
	if current.ProbeIP != "203.0.113.10" {
		t.Fatal("stale probe changed current IP")
	}
}

func TestPoolTransactionRollsBackOnNodeWriteFailure(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	old := testSharedProxy(t, a)
	if _, err := a.store.db.Exec("CREATE TRIGGER reject_node_update BEFORE UPDATE ON nodes BEGIN SELECT RAISE(ABORT,'injected node write failure'); END"); err != nil {
		t.Fatal(err)
	}
	p := old
	p.Label = "must not persist"
	if req(t, a, owner, "POST", "/api/ips", p).Code != 500 {
		t.Fatal("injected write failure not reported")
	}
	current, _ := a.store.pool(old.ID)
	if current.Label != old.Label || current.Revision != old.Revision {
		t.Fatal("pool updated despite rolled-back node writes")
	}
}

func TestUnavailablePoolCannotReplaceLiveExit(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	_ = testSharedProxy(t, a)
	a.cfg.Dev = false
	// A loopback closed port deterministically fails without external network access.
	p := decodePool(t, req(t, a, owner, "POST", "/api/ips", IPResource{Node: Node{Enabled: true, Exit: "http", Host: "127.0.0.1", Port: 1}, Label: "offline fixture"}))
	if p.Reachable || p.ProbeError == "" {
		t.Fatal("unreachable resource was marked ready")
	}
	nodes, _ := a.store.nodes()
	old := nodes[0]
	if req(t, a, owner, "POST", "/api/nodes", Node{ID: old.ID, Protocol: old.Protocol, Enabled: true, ExitID: p.ID}).Code != 400 {
		t.Fatal("unavailable exit was applied")
	}
	updated, _ := a.store.nodes()
	if updated[0].ExitID != old.ExitID || updated[0].Exit != old.Exit {
		t.Fatal("failed selection changed live exit")
	}
	bound, _ := a.store.pool(old.ExitID)
	bound.Exit, bound.Host, bound.Port = "http", "127.0.0.1", 1
	if req(t, a, owner, "POST", "/api/ips", bound).Code != 409 { // duplicate offline transport is rejected first
		t.Fatal("duplicate shared edit not rejected")
	}
	bound.Port = 2
	if req(t, a, owner, "POST", "/api/ips", bound).Code != 400 {
		t.Fatal("unreachable shared edit was applied")
	}
	current, _ := a.store.pool(old.ExitID)
	if current.Exit != "http" || current.Host != "203.0.113.40" {
		t.Fatal("failed shared edit changed existing pool")
	}
}
