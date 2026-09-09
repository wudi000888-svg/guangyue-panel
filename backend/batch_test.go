package main

import (
	"errors"
	"testing"
)

func TestIndependentExitCascadeAndBulkRollback(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	if err := a.store.ensureDefaultDirectNodes(); err != nil {
		t.Fatal(err)
	}
	p := decodePool(t, req(t, a, owner, "POST", "/api/ips", IPResource{Node: Node{Exit: "http", Host: "203.0.113.61", Port: 8080, Enabled: true}}))
	vl := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", Node{Protocol: "vless", Enabled: true, ExitID: p.ID}))
	hy := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", Node{Protocol: "hy2", Enabled: true, ExitID: p.ID}))
	before, _ := a.store.record(member.ID)
	plan, err := a.planResourceChange("ips", batchRequest{IDs: []string{p.ID}, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = a.applyResourceChange(plan, func() error {
		calls++
		if calls == 1 {
			return errors.New("injected")
		}
		return nil
	})
	restored, _ := a.store.record(member.ID)
	if err == nil || calls != 2 || restored.Credentials.VLESS[vl.ID] != before.Credentials.VLESS[vl.ID] {
		t.Fatal("rollback did not restore credentials")
	}
	if _, err := a.store.pool(p.ID); err != nil {
		t.Fatal("rollback did not restore exit")
	}
	response := decoded[map[string]any](t, req(t, a, owner, "DELETE", "/api/ips/"+p.ID, nil), 200)
	if response["nodes_deleted"] != float64(2) {
		t.Fatal("missing cascade")
	}
	nodes, _ := a.store.nodes()
	if len(nodes) != 2 {
		t.Fatal("wrong node retention")
	}
	for _, n := range nodes {
		if !n.DefaultDirect || !n.Enabled || n.ID == vl.ID || n.ID == hy.ID {
			t.Fatal("default protection failed")
		}
	}
	after, _ := a.store.record(member.ID)
	if after.Credentials.VLESS[vl.ID] != "" || after.Credentials.HY2 != before.Credentials.HY2 || after.Credentials.Token != before.Credentials.Token {
		t.Fatal("credential scope")
	}
}
func TestBatchValidatesWholeSelectionAndOwnership(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	a.store.ensureDefaultDirectNodes()
	n := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", Node{Protocol: "vless", Enabled: true}))
	defaults, _ := a.store.nodes()
	id := ""
	for _, d := range defaults {
		if d.DefaultDirect {
			id = d.ID
			break
		}
	}
	for _, action := range []string{"delete", "disable"} {
		if req(t, a, owner, "POST", "/api/nodes/batch", batchRequest{IDs: []string{n.ID, id}, Action: action}).Code != 409 {
			t.Fatal("default allowed in destructive selection")
		}
	}
	if req(t, a, member, "POST", "/api/nodes/batch", batchRequest{IDs: []string{n.ID}, Action: "delete"}).Code != 403 {
		t.Fatal("member batch mutation")
	}
	for _, ids := range [][]string{{n.ID, "missing"}, {n.ID, n.ID}, nil} {
		if req(t, a, owner, "POST", "/api/nodes/batch", batchRequest{IDs: ids, Action: "delete"}).Code != 409 {
			t.Fatal("invalid selection accepted")
		}
	}
	nodes, _ := a.store.nodes()
	if len(nodes) != 3 {
		t.Fatal("partial delete")
	}
	source := newTestSource(t, a, "https://one.example/sub")
	if err := a.syncSourceLocked(source, sourceItems(t, "one"), nil); err != nil {
		t.Fatal(err)
	}
	source, _ = a.store.importSource(source.ID)
	if req(t, a, owner, "POST", "/api/ips/batch", batchRequest{IDs: source.ResourceIDs, Action: "delete"}).Code != 409 {
		t.Fatal("source lifecycle bypassed")
	}
	a.heavyMu.Lock()
	code := req(t, a, owner, "POST", "/api/nodes/batch", batchRequest{IDs: []string{n.ID}, Action: "disable"}).Code
	a.heavyMu.Unlock()
	if code != 409 {
		t.Fatal("overlapping resource mutation")
	}
	decoded[map[string]any](t, req(t, a, owner, "POST", "/api/nodes/batch", batchRequest{IDs: []string{n.ID}, Action: "disable"}), 200)
	nodes, _ = a.store.nodes()
	for _, row := range nodes {
		if row.ID == n.ID && row.Enabled {
			t.Fatal("not disabled")
		}
	}
	decoded[map[string]any](t, req(t, a, owner, "POST", "/api/nodes/batch", batchRequest{IDs: []string{n.ID}, Action: "enable"}), 200)
}
