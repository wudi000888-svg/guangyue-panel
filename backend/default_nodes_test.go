package main

import (
	"reflect"
	"testing"
)

func TestDefaultDirectMigrationPreservesExistingRoutesAndCredentials(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	// A legacy main node may already have been assigned to an external exit.
	legacy := Node{ID: "hy2-main", Protocol: "hy2", Enabled: true, Exit: "http", Host: "8.8.8.8", Port: 8080}
	if err := a.store.saveNode(legacy); err != nil {
		t.Fatal(err)
	}
	direct := Node{ID: "hy2-existing", Protocol: "hy2", Enabled: true, Exit: "direct", ProbeIP: "1.1.1.1"}
	if err := a.store.saveNode(direct); err != nil {
		t.Fatal(err)
	}
	if err := a.store.ensureDefaultDirectNodes(); err != nil {
		t.Fatal(err)
	}
	nodes, _ := a.store.nodes()
	defaults := map[string]string{}
	for _, n := range nodes {
		if n.ID == legacy.ID && !reflect.DeepEqual(n, legacy) {
			t.Fatal("legacy proxy route changed")
		}
		if n.DefaultDirect {
			if !n.Enabled || n.Exit != "direct" || n.ExitID != "" {
				t.Fatal("invalid default")
			}
			defaults[n.Protocol] = n.ID
		}
	}
	if !reflect.DeepEqual(defaults, map[string]string{"vless": "vless-main", "hy2": "hy2-existing"}) {
		t.Fatal("existing direct nodes not adopted")
	}
	current, _ := a.store.record(owner.ID)
	if !reflect.DeepEqual(current.Credentials, owner.Credentials) {
		t.Fatal("migration changed credentials")
	}
	if err := a.store.ensureDefaultDirectNodes(); err != nil {
		t.Fatal(err)
	}
	after, _ := a.store.nodes()
	if !reflect.DeepEqual(after, nodes) {
		t.Fatal("migration not idempotent")
	}
}

func TestDefaultDirectMigrationCreatesMissingProtocolsAtomically(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	a.store.db.Exec("DELETE FROM nodes")
	if err := a.store.ensureDefaultDirectNodes(); err != nil {
		t.Fatal(err)
	}
	nodes, _ := a.store.nodes()
	current, _ := a.store.record(owner.ID)
	if len(nodes) != 2 {
		t.Fatal("missing direct defaults")
	}
	for _, n := range nodes {
		if !n.Enabled || !n.DefaultDirect || n.Exit != "direct" || n.ExitID != "" {
			t.Fatal("invalid default")
		}
		if n.Protocol == "vless" && current.Credentials.VLESS[n.ID] == "" {
			t.Fatal("default missing user credential")
		}
	}
	if current.Credentials.Token != owner.Credentials.Token || current.Credentials.HY2 != owner.Credentials.HY2 {
		t.Fatal("account identity changed")
	}
}

func TestDefaultDirectAPIRejectsDeletionDisableAndRebinding(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	if err := a.store.ensureDefaultDirectNodes(); err != nil {
		t.Fatal(err)
	}
	nodes, _ := a.store.nodes()
	for _, n := range nodes {
		if got := req(t, a, owner, "DELETE", "/api/nodes/"+n.ID, nil).Code; got != 409 {
			t.Fatal("default deletion allowed", got)
		}
		for _, change := range []func(*Node){
			func(n *Node) { n.Enabled = false },
			func(n *Node) { n.ExitID = "arbitrary-pool" },
			func(n *Node) { n.Exit = "http"; n.Host = "8.8.8.8"; n.Port = 8080 },
			func(n *Node) { n.Protocol = "other" },
		} {
			changed := n
			changed.DefaultDirect = false // The client cannot remove protection.
			change(&changed)
			if got := req(t, a, owner, "POST", "/api/nodes", changed).Code; got != 409 {
				t.Fatal("default mutation allowed", got)
			}
		}
		if got := req(t, a, owner, "POST", "/api/nodes", n).Code; got != 200 {
			t.Fatal("valid default save rejected", got)
		}
	}
	after, _ := a.store.nodes()
	if len(after) != 2 || !after[0].DefaultDirect || !after[1].DefaultDirect {
		t.Fatal("default protection lost")
	}
	newNode := decoded[Node](t, req(t, a, owner, "POST", "/api/nodes", Node{Protocol: "vless", Enabled: true, DefaultDirect: true}), 200)
	if newNode.DefaultDirect || newNode.Exit != "direct" {
		t.Fatal("client forged default marker or default exit incorrect")
	}
	if req(t, a, owner, "DELETE", "/api/nodes/"+newNode.ID, nil).Code != 200 {
		t.Fatal("ordinary node cannot be deleted")
	}
}

func TestSourceCascadePreservesDefaultDirectPair(t *testing.T) {
	a, owner, source, _ := sourceDeletionFixture(t)
	if err := a.store.ensureDefaultDirectNodes(); err != nil {
		t.Fatal(err)
	}
	if req(t, a, owner, "DELETE", "/api/import-sources/"+source.ID, nil).Code != 200 {
		t.Fatal("source deletion failed")
	}
	nodes, _ := a.store.nodes()
	if len(nodes) != 2 {
		t.Fatal("cascade left bound nodes or removed defaults")
	}
	for _, n := range nodes {
		if !n.DefaultDirect || !n.Enabled || n.Exit != "direct" || n.ExitID != "" {
			t.Fatal("default lost")
		}
	}
	current, _ := a.store.record(owner.ID)
	if len(current.Credentials.VLESS) != 1 {
		t.Fatal("bound credential retained or default credential lost")
	}
}
