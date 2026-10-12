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
	var response struct {
		Nodes []NodeSquareNode `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Nodes) != 1 || response.Nodes[0].ID != "vless-main" {
		t.Fatalf("individual node selection widened: %+v", response.Nodes)
	}
}

func TestNodeSquareOwnerControlsLocalVisibilityAndMemberOnlySeesPublished(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	nodes, _ := a.store.nodes()
	for i := range nodes {
		nodes[i].SquareVisible = nodes[i].ID == "vless-main"
		nodes[i].Password = "keep-this-secret"
		if err := a.store.saveNode(nodes[i]); err != nil {
			t.Fatal(err)
		}
	}
	p := Plan{ID: "admin-square-plan", Version: 1, Name: "公开套餐", ValidDays: 30, GroupIDs: []string{legacyPrivateGroup}, VLESS: true}
	o := Offer{ID: "admin-square-offer", Version: 1, Enabled: true, Price: 100, Plan: p}
	if _, err := a.store.db.Exec("INSERT INTO plans(id,doc) VALUES(?,?)", p.ID, jsonBytes(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec("INSERT INTO commerce_offers(id,version,enabled,doc) VALUES(?,?,?,?)", o.ID, 1, 1, jsonBytes(o)); err != nil {
		t.Fatal(err)
	}
	var admin struct{ Nodes []NodeSquareNode `json:"nodes"` }
	if w := req(t, a, owner, "GET", "/api/node-square", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &admin) != nil {
		t.Fatalf("admin listing failed: %d %s", w.Code, w.Body.String())
	}
	if len(admin.Nodes) != 2 {
		t.Fatalf("admin listing must include hidden/disabled/no-offer nodes: %+v", admin.Nodes)
	}
	for _, n := range admin.Nodes {
		if n.SiteID != a.cfg.siteID() || n.SiteName == "" {
			t.Fatalf("local site metadata missing: %+v", n)
		}
		if n.ID == "vless-main" && (!n.Published || !n.SquareVisible || len(n.Plans) != 1) {
			t.Fatalf("published local node malformed: %+v", n)
		}
		if n.ID == "hy2-main" && n.Published {
			t.Fatalf("disabled node reported published: %+v", n)
		}
	}
	if w := req(t, a, member, "GET", "/api/node-square", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "vless-main") || strings.Contains(w.Body.String(), "hy2-main") {
		t.Fatalf("member listing leaked unpublished node: %d %s", w.Code, w.Body.String())
	}
	if w := req(t, a, member, "PUT", "/api/node-square", object{"node_id": "vless-main", "site_id": a.cfg.siteID(), "square_visible": false}); w.Code != 403 {
		t.Fatalf("member changed node visibility: %d %s", w.Code, w.Body.String())
	}
	if w := req(t, a, owner, "PUT", "/api/node-square", object{"node_id": "vless-main", "site_id": a.cfg.siteID(), "square_visible": false}); w.Code != 200 {
		t.Fatalf("owner visibility update failed: %d %s", w.Code, w.Body.String())
	}
	updated, _ := a.store.nodes()
	for _, n := range updated {
		if n.ID == "vless-main" && n.Password != "keep-this-secret" {
			t.Fatal("visibility update rewrote node fields")
		}
	}
	if w := req(t, a, member, "GET", "/api/node-square", nil); w.Code != 200 || strings.Contains(w.Body.String(), "vless-main") {
		t.Fatalf("hidden node remained visible: %s", w.Body.String())
	}
}

func TestNodeSquareMountedNodesAreSiteScopedAndOwnerControllable(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	local, _ := a.store.nodes()
	for i := range local {
		local[i].SquareVisible = local[i].ID == "vless-main"
		if err := a.store.saveNode(local[i]); err != nil {
			t.Fatal(err)
		}
	}
	child := Node{ID: "vless-main", Name: "Child VLESS", Protocol: "vless", Enabled: true, Exit: "direct", SquareVisible: true, GroupIDs: []string{defaultSubsiteGroup}}
	site := BusinessSite{ID: "child-site", Name: "Tokyo Child", Enabled: true, Revision: "site-rev", Connection: &SiteConnection{SiteID: "child-site", Scope: "manage"}, Mount: &SiteMount{Catalog: MountCatalog{Revision: "catalog-rev", Nodes: []Node{child}}, Nodes: []MountedNode{{NodeID: child.ID, Name: child.Name, Enabled: true, SquareVisible: true}}}
	site.defaults()
	b, err := a.store.vault.seal(site)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("INSERT INTO business_sites(id,doc) VALUES(?,?)", site.ID, b); err != nil {
		t.Fatal(err)
	}
	p := Plan{ID: "mounted-square-plan", Version: 1, Name: "挂载套餐", GroupIDs: []string{defaultSubsiteGroup}, VLESS: true}
	o := Offer{ID: "mounted-square-offer", Version: 1, Enabled: true, Price: 200, Plan: p}
	if _, err = a.store.db.Exec("INSERT INTO plans(id,doc) VALUES(?,?)", p.ID, jsonBytes(p)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("INSERT INTO commerce_offers(id,version,enabled,doc) VALUES(?,?,?,?)", o.ID, 1, 1, jsonBytes(o)); err != nil {
		t.Fatal(err)
	}
	var admin struct{ Nodes []NodeSquareNode `json:"nodes"` }
	w := req(t, a, owner, "GET", "/api/node-square", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &admin) != nil {
		t.Fatalf("mounted admin listing failed: %d %s", w.Code, w.Body.String())
	}
	seenLocal, seenChild := false, false
	for _, n := range admin.Nodes {
		if n.ID != "vless-main" {
			continue
		}
		if n.SiteID == a.cfg.siteID() { seenLocal = true }
		if n.SiteID == site.ID { seenChild = true }
	}
	if !seenLocal || !seenChild {
		t.Fatalf("colliding node IDs were not site scoped: %+v", admin.Nodes)
	}
	if w = req(t, a, member, "GET", "/api/node-square", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Tokyo Child") {
		t.Fatalf("published mounted node missing: %d %s", w.Code, w.Body.String())
	}
	if w = req(t, a, owner, "PUT", "/api/node-square", object{"node_id": "vless-main", "site_id": site.ID, "square_visible": false}); w.Code != 200 {
		t.Fatalf("mounted visibility update failed: %d %s", w.Code, w.Body.String())
	}
	if w = req(t, a, member, "GET", "/api/node-square", nil); w.Code != 200 || strings.Contains(w.Body.String(), "Tokyo Child") {
		t.Fatalf("hidden mounted node remained visible: %s", w.Body.String())
	}
	stored, _ := a.store.businessSite(site.ID)
	if stored.Mount.Nodes[0].SquareVisible {
		t.Fatal("mounted visibility was not persisted")
	}
}
