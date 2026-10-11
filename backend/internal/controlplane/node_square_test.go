package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNodeSquareOnlyListsExplicitlyPublishedNodesAndOnSalePlans(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	nodes, err := a.store.nodes()
	if err != nil {
		t.Fatal(err)
	}
	for i := range nodes {
		if nodes[i].ID == "vless-main" {
			nodes[i].SquareVisible = true
			nodes[i].Password = "private-node-password"
		}
		if nodes[i].ID == "hy2-main" {
			nodes[i].SquareVisible = true
			nodes[i].Enabled = false
		}
		if err := a.store.saveNode(nodes[i]); err != nil {
			t.Fatal(err)
		}
	}
	p := Plan{ID: "square-plan", Version: 1, Name: "线路体验", Price: 1299, ValidDays: 30, Cycle: "30d", GroupIDs: []string{legacyPrivateGroup}, VLESS: true}
	o := Offer{ID: "square-offer", Version: 1, Enabled: true, Price: p.Price, Plan: p}
	if _, err = a.store.db.Exec("INSERT INTO plans(id,doc) VALUES(?,?)", p.ID, jsonBytes(p)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("INSERT INTO commerce_offers(id,version,enabled,doc) VALUES(?,?,?,?)", o.ID, o.Version, boolInt(o.Enabled), jsonBytes(o)); err != nil {
		t.Fatal(err)
	}

	w := req(t, a, member, "GET", "/api/node-square", nil)
	if w.Code != 200 {
		t.Fatalf("node square: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Nodes []NodeSquareNode `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Nodes) != 1 || response.Nodes[0].ID != "vless-main" {
		t.Fatalf("unexpected public nodes: %+v", response.Nodes)
	}
	if len(response.Nodes[0].Plans) != 1 || response.Nodes[0].Plans[0].ID != p.ID || response.Nodes[0].Plans[0].Price != p.Price {
		t.Fatalf("unexpected package summary: %+v", response.Nodes[0].Plans)
	}
	body := w.Body.String()
	for _, secret := range []string{"private-node-password", "reality_sni", "exit_id", "host", "port", "username"} {
		if strings.Contains(body, secret) {
			t.Fatalf("node square leaked private field %q: %s", secret, body)
		}
	}
	if req(t, a, Record{}, "GET", "/api/node-square", nil).Code != 401 {
		t.Fatal("node square is accessible without a session")
	}
	if req(t, a, owner, "GET", "/api/node-square", nil).Code != 200 {
		t.Fatal("owner cannot view node square")
	}
}

func TestNodeSquareHonorsIndividualPlanNodeSelection(t *testing.T) {
	a := testApp(t)
	member := testUser(t, a, "member", "user")
	nodes, _ := a.store.nodes()
	for i := range nodes {
		nodes[i].SquareVisible = true
		if err := a.store.saveNode(nodes[i]); err != nil {
			t.Fatal(err)
		}
	}
	p := Plan{ID: "single-square-plan", Version: 1, Name: "单线路", ValidDays: 7, Cycle: "7d", GroupIDs: []string{legacyPrivateGroup}, NodeIDs: []string{"vless-main"}, VLESS: true, HY2: true}
	o := Offer{ID: "single-square-offer", Version: 1, Enabled: true, Price: 0, Plan: p}
	if _, err := a.store.db.Exec("INSERT INTO plans(id,doc) VALUES(?,?)", p.ID, jsonBytes(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec("INSERT INTO commerce_offers(id,version,enabled,doc) VALUES(?,?,?,?)", o.ID, o.Version, boolInt(o.Enabled), jsonBytes(o)); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, member, "GET", "/api/node-square", nil)
	if w.Code != 200 {
		t.Fatalf("node square: %d %s", w.Code, w.Body.String())
	}
	var response struct{ Nodes []NodeSquareNode `json:"nodes"` }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Nodes) != 1 || response.Nodes[0].ID != "vless-main" {
		t.Fatalf("individual node selection widened: %+v", response.Nodes)
	}
}
