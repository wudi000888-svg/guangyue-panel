package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newTestSource(t *testing.T, a *App, url string) ImportSource {
	t.Helper()
	s, _, e := a.createSource(sourceInput{Name: "Provider", URL: url, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func sourceItems(t *testing.T, labels ...string) []importItem {
	t.Helper()
	items := []importItem{}
	for _, label := range labels {
		v, e := normalizeProxy(object{"name": label, "udp": true, "type": "hysteria2", "server": label + ".example", "port": 443, "password": "fixture-secret"})
		if e != nil {
			t.Fatal(e)
		}
		items = append(items, v)
	}
	return items
}
func TestSourceEncryptionPermissionsAndDailySlots(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	address := "https://provider.example/sub/private-url-token"
	s := newTestSource(t, a, address)
	var raw []byte
	a.store.db.QueryRow("SELECT doc FROM import_sources WHERE id=?", s.ID).Scan(&raw)
	if bytes.Contains(raw, []byte("private-url-token")) {
		t.Fatal("URL stored in plaintext")
	}
	response := req(t, a, owner, "GET", "/api/import-sources", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-url-token") {
		t.Fatal("source URL leaked")
	}
	if req(t, a, member, "GET", "/api/import-sources", nil).Code != 403 || req(t, a, member, "POST", "/api/import-sources", object{}).Code != 403 {
		t.Fatal("source permissions")
	}
	again, duplicate, e := a.createSource(sourceInput{URL: address, Enabled: true})
	if e != nil || !duplicate || again.ID != s.ID {
		t.Fatal("duplicate source")
	}
	now := time.Now().Unix()
	slots := map[int64]bool{}
	for i := 0; i < 32; i++ {
		at := sourceNextAt(fmt.Sprint(i), now)
		if at <= now || at > now+86400 || sourceNextAt(fmt.Sprint(i), at) != at+86400 {
			t.Fatal("daily schedule")
		}
		slots[at] = true
	}
	if len(slots) < 28 {
		t.Fatal("sources clustered")
	}
	second, e := openStore(a.cfg.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer second.db.Close()
	persisted, e := second.importSource(s.ID)
	if e != nil || persisted.URL != address || persisted.NextAt != s.NextAt {
		t.Fatal("source persistence")
	}
}
func TestSourceRefreshPreservesBindingsAndPrunesOnlyOwnedUnused(t *testing.T) {
	a := testApp(t)
	s := newTestSource(t, a, "https://provider.example/sub")
	if e := a.syncSourceLocked(s, sourceItems(t, "alpha", "beta"), nil); e != nil {
		t.Fatal(e)
	}
	s, _ = a.store.importSource(s.ID)
	pools, _ := a.store.pools()
	var boundID string
	for _, p := range pools {
		if p.Label == "alpha" {
			boundID = p.ID
			n := bindPool(Node{ID: "bound", Protocol: "vless", Enabled: true}, p)
			a.store.saveNode(n)
		}
	}
	beforeNodes, _ := a.store.nodes()
	beforeHash := nodeHash(beforeNodes, "vless")
	if e := a.syncSourceLocked(s, sourceItems(t, "gamma"), nil); e != nil {
		t.Fatal(e)
	}
	pools, _ = a.store.pools()
	if len(pools) != 2 {
		t.Fatal("expected bound old plus new")
	}
	p, e := a.store.pool(boundID)
	if e != nil || !p.SourceStale {
		t.Fatal("bound old resource not retained and marked")
	}
	afterNodes, _ := a.store.nodes()
	if nodeHash(afterNodes, "vless") != beforeHash {
		t.Fatal("changed an existing node binding")
	}
	s, _ = a.store.importSource(s.ID)
	if s.Added != 1 || s.Removed != 1 || s.Retained != 1 || len(s.ResourceIDs) != 1 {
		t.Fatal("refresh counters")
	}
	id := s.ResourceIDs[0]
	if e = a.syncSourceLocked(s, sourceItems(t, "gamma"), nil); e != nil {
		t.Fatal(e)
	}
	s, _ = a.store.importSource(s.ID)
	if s.Added != 0 || s.ResourceIDs[0] != id {
		t.Fatal("unchanged resource identity lost")
	}
}
func TestSourceRefreshDeduplicatesManualAndSharedResources(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	if req(t, a, owner, "POST", "/api/ips/import", object{"content": "hy2://fixture-secret@manual.example:443#manual"}).Code != 200 {
		t.Fatal("manual fixture")
	}
	one := newTestSource(t, a, "https://one.example/sub")
	if e := a.syncSourceLocked(one, sourceItems(t, "manual", "shared"), nil); e != nil {
		t.Fatal(e)
	}
	two := newTestSource(t, a, "https://two.example/sub")
	if e := a.syncSourceLocked(two, sourceItems(t, "shared"), nil); e != nil {
		t.Fatal(e)
	}
	pools, _ := a.store.pools()
	if len(pools) != 2 {
		t.Fatal("cross-source duplication")
	}
	one, _ = a.store.importSource(one.ID)
	if e := a.syncSourceLocked(one, sourceItems(t, "new"), nil); e != nil {
		t.Fatal(e)
	}
	pools, _ = a.store.pools()
	if len(pools) != 3 {
		t.Fatal("manual or shared resource deleted")
	}
}
func TestSourceDeleteAndConflictingEdit(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	s := newTestSource(t, a, "https://one.example/sub")
	if e := a.syncSourceLocked(s, sourceItems(t, "one"), nil); e != nil {
		t.Fatal(e)
	}
	in := sourceInput{Name: "Renamed", Enabled: false, Revision: "stale"}
	if req(t, a, owner, "PUT", "/api/import-sources/"+s.ID, in).Code != 409 {
		t.Fatal("stale edit accepted")
	}
	in.Revision = s.Revision
	if req(t, a, owner, "PUT", "/api/import-sources/"+s.ID, in).Code != 200 {
		t.Fatal("edit failed")
	}
	updated, _ := a.store.importSource(s.ID)
	if updated.Enabled || updated.Queued || updated.Name != "Renamed" {
		t.Fatal("pause failed")
	}
	if req(t, a, owner, "DELETE", "/api/import-sources/"+s.ID, object{}).Code != 200 {
		t.Fatal("delete failed")
	}
	pools, _ := a.store.pools()
	if len(pools) != 0 {
		t.Fatal("deleting source must remove owned resources")
	}
}
func TestSourceDispatchSingleFlightBackoffAndInvalidation(t *testing.T) {
	a := testApp(t)
	s := newTestSource(t, a, "https://one.example/sub")
	now := time.Now().Unix()
	calls := 0
	fetch := func(context.Context, string) ([]byte, error) { calls++; return nil, errors.New("download unavailable") }
	a.heavyMu.Lock()
	if a.runImportSource(context.Background(), fetch, now) {
		t.Fatal("overlapped heavy task")
	}
	a.heavyMu.Unlock()
	if !a.runImportSource(context.Background(), fetch, now) || calls != 1 {
		t.Fatal("dispatch failed")
	}
	failed, _ := a.store.importSource(s.ID)
	if failed.Failures != 1 || failed.NextAt < now+3600 {
		t.Fatal("missing backoff")
	}
	other := newTestSource(t, a, "https://two.example/sub")
	if a.runImportSource(context.Background(), fetch, now+30) {
		t.Fatal("missing dispatch gap")
	}
	mutate := func(context.Context, string) ([]byte, error) {
		current, _ := a.store.importSource(other.ID)
		current.Revision = randomToken(12)
		current.Enabled = false
		a.store.saveImportSource(current)
		return []byte("hy2://fixture-secret@late.example:443#late"), nil
	}
	if !a.runImportSource(context.Background(), mutate, now+61) {
		t.Fatal("second source not scheduled")
	}
	pools, _ := a.store.pools()
	if len(pools) != 0 {
		t.Fatal("stale background result applied")
	}
}

func TestSourceAutomaticDueDispatchWithoutManualQueue(t *testing.T) {
	a := testApp(t)
	s := newTestSource(t, a, "https://daily.example/sub")
	now := time.Now().Unix()
	s.Queued = false
	s.NextAt = now + 3600
	if err := a.store.saveImportSource(s); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fetch := func(context.Context, string) ([]byte, error) {
		calls++
		return []byte("hy2://fixture-secret@daily.example:443#daily"), nil
	}
	if a.runImportSource(context.Background(), fetch, now) || calls != 0 {
		t.Fatal("automatic source dispatched before its daily time")
	}
	s.NextAt, s.Enabled = now, false
	if err := a.store.saveImportSource(s); err != nil {
		t.Fatal(err)
	}
	if a.runImportSource(context.Background(), fetch, now) || calls != 0 {
		t.Fatal("paused source dispatched automatically")
	}
	s.Enabled = true
	if err := a.store.saveImportSource(s); err != nil {
		t.Fatal(err)
	}
	if !a.runImportSource(context.Background(), fetch, now) || calls != 1 {
		t.Fatal("due automatic source needs a manual queue")
	}
	updated, err := a.store.importSource(s.ID)
	if err != nil || updated.LastSuccess == 0 || updated.Queued || updated.NextAt <= now || len(updated.ResourceIDs) != 1 {
		t.Fatal("automatic update did not persist resources and its next daily schedule")
	}
	if a.runImportSource(context.Background(), fetch, updated.NextAt-1) || calls != 1 {
		t.Fatal("automatic source ran twice before its next daily schedule")
	}
}

func TestPartialAndInvalidSourceResponseDoesNotPrune(t *testing.T) {
	a := testApp(t)
	s := newTestSource(t, a, "https://one.example/sub")
	if e := a.syncSourceLocked(s, sourceItems(t, "one", "two"), nil); e != nil {
		t.Fatal(e)
	}
	s, _ = a.store.importSource(s.ID)
	if e := a.syncSourceLocked(s, sourceItems(t, "one"), []importWarning{{2, "unsupported"}}); e != nil {
		t.Fatal(e)
	}
	pools, _ := a.store.pools()
	if len(pools) != 2 {
		t.Fatal("partial response pruned resources")
	}
	s, _ = a.store.importSource(s.ID)
	s.Queued = true
	a.store.saveImportSource(s)
	a.runImportSource(context.Background(), func(context.Context, string) ([]byte, error) { return []byte("<html>expired</html>"), nil }, time.Now().Unix())
	pools, _ = a.store.pools()
	if len(pools) != 2 {
		t.Fatal("invalid response pruned resources")
	}
}
func TestHTTPSImportCreatesManagedSourceWithoutCredentialLeak(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	w := req(t, a, owner, "POST", "/api/ips/import", object{"url": "https://provider.example/sub/private-token"})
	var v object
	json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || v["queued"] != true || strings.Contains(w.Body.String(), "private-token") {
		t.Fatal("managed import failed")
	}
	sources, _ := a.store.importSources()
	if len(sources) != 1 || !sources[0].Enabled || !sources[0].Queued {
		t.Fatal("daily default not saved")
	}
}
