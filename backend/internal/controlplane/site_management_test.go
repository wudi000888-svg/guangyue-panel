package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func siteRequest(t *testing.T, a *App, actor Record, id, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "guangyue")
	r.Header.Set("X-Guangyue-Site", id)
	token := randomToken(32)
	if _, err := a.store.db.Exec("INSERT INTO sessions VALUES(?,?,?)", digest(token), actor.ID, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	r.AddCookie(&http.Cookie{Name: "gy_session", Value: token})
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func TestDirectSubsiteLifecycle(t *testing.T) {
	master, child := testApp(t), testApp(t)
	master.cfg.Edition = "pro" // Both installations intentionally keep site_id=default.
	owner := testUser(t, master, "master-owner", "owner")
	member := testUser(t, master, "master-member", "user")
	childOwner := testUser(t, child, "child-owner", "owner")
	childUser := testUser(t, child, "child-member", "user")
	nodesBefore, _ := child.store.nodes()
	recordsBefore, _ := child.store.records()
	server := httptest.NewServer(child.routes())
	defer server.Close()
	child.cfg.PublicURL = server.URL
	w := req(t, child, childOwner, "POST", "/api/fleet/tokens", object{"name": "master", "scope": "manage", "forever": true})
	var token struct {
		ID         string `json:"id"`
		Token      string `json:"token"`
		Connection string `json:"connection_token"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &token) != nil || !strings.HasPrefix(token.Connection, "gys_") {
		t.Fatal("cannot create portable token", w.Code)
	}
	importSite := func(actor Record, secret, address string) *httptest.ResponseRecorder {
		return req(t, master, actor, "POST", "/api/business-sites/import", object{"token": secret, "url": address})
	}
	if w = importSite(member, token.Connection, ""); w.Code != 403 {
		t.Fatal("member imported site", w.Code)
	}
	if w = req(t, child, childOwner, "POST", "/api/business-sites/import", object{"token": token.Connection}); w.Code != 403 {
		t.Fatal("Lite imported site", w.Code)
	}
	w = importSite(owner, token.Connection, "")
	var site BusinessSite
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &site) != nil || site.Connection == nil {
		t.Fatal("direct import", w.Code, w.Body.String())
	}
	if site.Connection.Token != "" || strings.Contains(w.Body.String(), token.Token) {
		t.Fatal("credential exposed")
	}
	stored, err := master.store.businessSite(site.ID)
	if err != nil || stored.Connection.Token != token.Token {
		t.Fatal("credential not stored")
	}
	var encrypted []byte
	if err = master.store.db.QueryRow("SELECT doc FROM business_sites WHERE id=?", site.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte(token.Token)) {
		t.Fatal("credential stored in plaintext")
	}
	nodesAfter, _ := child.store.nodes()
	recordsAfter, _ := child.store.records()
	if !reflect.DeepEqual(nodesBefore, nodesAfter) || !reflect.DeepEqual(recordsBefore, recordsAfter) {
		t.Fatal("import changed child data")
	}
	if _, err = os.Stat(filepath.Join(child.cfg.StateDir, businessAdoptionFile)); !os.IsNotExist(err) {
		t.Fatal("import changed child role")
	}
	if w = importSite(owner, token.Token, server.URL+"/"); w.Code != 201 {
		t.Fatal("legacy token import", w.Code)
	}
	sites, _ := master.store.businessSites()
	if len(sites) != 1 {
		t.Fatal("reimport duplicated site")
	}
	second := httptest.NewServer(child.routes())
	defer second.Close()
	changed := encodeSiteToken(SiteToken{Version: 1, SiteID: child.cfg.siteID(), URL: second.URL, Token: token.Token})
	w = importSite(owner, changed, "")
	if w.Code != 201 {
		t.Fatal("updated address", w.Code, w.Body.String())
	}
	sites, _ = master.store.businessSites()
	if len(sites) != 1 || sites[0].ID != site.ID || sites[0].Connection.URL != second.URL {
		t.Fatal("address update duplicated identity")
	}
	bad := encodeSiteToken(SiteToken{Version: 1, SiteID: "other", URL: second.URL, Token: token.Token})
	if w = importSite(owner, bad, ""); w.Code != 502 {
		t.Fatal("forged identity accepted", w.Code)
	}
	for _, path := range []string{"/api/state", "/api/plans", "/api/node-groups", "/api/monitor"} {
		if w = siteRequest(t, master, owner, site.ID, "GET", path, nil); w.Code != 200 {
			t.Fatal("remote management", path, w.Code, w.Body.String())
		}
	}
	if w = siteRequest(t, master, member, site.ID, "GET", "/api/state", nil); w.Code != 403 {
		t.Fatal("member reached child")
	}
	if w = siteRequest(t, master, owner, site.ID, "PUT", fmt.Sprintf("/api/users/%d", childUser.ID), userInput{Username: childUser.Username, Enabled: false, VLESS: true, HY2: true}); w.Code != 200 {
		t.Fatal("remote user edit", w.Code, w.Body.String())
	}
	childUser, _ = child.store.record(childUser.ID)
	localMember, _ := master.store.record(member.ID)
	if childUser.Enabled || !localMember.Enabled {
		t.Fatal("remote mutation crossed site boundary")
	}
	childUser.Enabled = true
	if err = child.store.save(&childUser); err != nil {
		t.Fatal(err)
	}
	if w = req(t, master, owner, "POST", "/api/business-sites/"+site.ID+"/control", object{"paused": true}); w.Code != 200 {
		t.Fatal("pause", w.Code, w.Body.String())
	}
	for _, u := range mustCoreRecords(t, child) {
		if u.Enabled {
			t.Fatal("pause left proxy authorization enabled")
		}
	}
	childUser, _ = child.store.record(childUser.ID)
	if !childUser.Enabled {
		t.Fatal("pause destroyed stored permission")
	}
	if w = req(t, child, childOwner, "GET", "/api/state", nil); w.Code != 200 {
		t.Fatal("pause blocked local panel")
	}
	var kicks int
	if err = child.store.db.QueryRow("SELECT COUNT(*) FROM revocations").Scan(&kicks); err != nil || kicks < 2 {
		t.Fatal("pause did not queue disconnects")
	}
	if w = req(t, child, childOwner, "PUT", "/api/site-control", object{"paused": false}); w.Code != 200 {
		t.Fatal("local resume", w.Code)
	}
	for _, u := range mustCoreRecords(t, child) {
		if !u.Enabled {
			t.Fatal("resume did not restore proxy access")
		}
	}
	w = req(t, master, owner, "POST", "/api/business-sites/"+site.ID+"/probe", object{})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &site) != nil || site.Connection.Status.OnlineUsers != nil || site.Connection.Status.Live.Upload != nil {
		t.Fatal("unknown monitor data", w.Code)
	}
	child.monitor.sample(liveInput{at: time.Now(), connections: [2]bool{true, true}, counts: [2]map[int64]int64{{childUser.ID: 2}, {childUser.ID: 1}}})
	w = req(t, master, owner, "POST", "/api/business-sites/"+site.ID+"/probe", object{})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &site) != nil || site.Connection.Status.OnlineUsers == nil || *site.Connection.Status.OnlineUsers != 1 {
		t.Fatal("online count", w.Code)
	}
	if w = req(t, child, childOwner, "DELETE", "/api/fleet/tokens/"+token.ID, nil); w.Code != 200 {
		t.Fatal("revoke", w.Code)
	}
	if w = siteRequest(t, master, owner, site.ID, "GET", "/api/state", nil); w.Code != 502 {
		t.Fatal("revoked token worked", w.Code)
	}
	server.Close()
	second.Close()
	if w = req(t, master, owner, "DELETE", "/api/business-sites/"+site.ID, nil); w.Code != 200 {
		t.Fatal("offline removal", w.Code)
	}
	if w = siteRequest(t, master, owner, site.ID, "GET", "/api/state", nil); w.Code != 404 {
		t.Fatal("removed connection accessible", w.Code)
	}
	if w = req(t, child, childOwner, "GET", "/api/state", nil); w.Code != 200 {
		t.Fatal("removal destroyed child")
	}
}

func TestSiteTokenValidation(t *testing.T) {
	token := "gyp_" + randomToken(32)
	for _, address := range []string{"http://example.com", "https://u:p@example.com", "https://example.com/path", "https://example.com?token=secret"} {
		if _, err := parseSiteToken(token, address, false); err == nil {
			t.Fatal("invalid endpoint accepted", address)
		}
	}
	for _, secret := range []string{"gys_invalid", "gys_", strings.Repeat("x", 8193), "gyb_" + randomToken(32), encodeSiteToken(SiteToken{Version: 2, URL: "https://example.com", SiteID: "a", Token: token})} {
		if _, err := parseSiteToken(secret, "https://example.com", false); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}

func TestLegacyOfflineDirectoryMigration(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro"
	owner := testUser(t, a, "owner", "owner")
	peer := FleetPeer{ID: "old", Name: "offline", URL: "https://offline.invalid", SiteID: "child", Scope: "read", Token: "gyp_" + randomToken(32)}
	b, _ := a.store.vault.seal(peer)
	if _, err := a.store.db.Exec("INSERT INTO fleet_peers(id,doc) VALUES(?,?)", peer.ID, b); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, owner, "GET", "/api/business-sites", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	sites, _ := a.store.businessSites()
	if len(sites) != 1 || sites[0].Connection.Scope != "read" {
		t.Fatal("offline/read-only peer not preserved")
	}
	p, err := a.remoteSubsite(peer.ID)
	if err != nil || p.SiteID != "child" {
		t.Fatal("old selection was broken")
	}
	if w = req(t, a, owner, "POST", "/api/business-sites/"+sites[0].ID+"/control", object{"paused": true}); w.Code != 403 {
		t.Fatal("read scope gained control")
	}
	if w = req(t, a, owner, "DELETE", "/api/business-sites/"+sites[0].ID, nil); w.Code != 200 {
		t.Fatal("cannot remove offline peer")
	}
	if _, err = a.remoteSubsite(peer.ID); err == nil {
		t.Fatal("old alias bypassed deletion")
	}
}

func TestTokenManagementSurvivesRestart(t *testing.T) {
	a := testApp(t)
	a.cfg.Role = "business"
	a.cfg.SiteID = "child"
	a.cfg.ControllerURL = "https://master.example.com"
	a.cfg.ConnectToken = "gye_" + randomToken(32)
	owner := testUser(t, a, "owner", "owner")
	testUser(t, a, "member", "user")
	cfgPath := filepath.Join(a.cfg.StateDir, "config.json")
	if err := writeJSON(cfgPath, a.cfg); err != nil {
		t.Fatal(err)
	}
	records, _ := a.store.records()
	nodes, _ := a.store.nodes()
	if w := req(t, a, owner, "POST", "/api/site-control", object{"action": "token-management"}); w.Code != 200 {
		t.Fatal("conversion", w.Code)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil || cfg.businessAgent() || cfg.controller() || cfg.DatabaseOptions().Driver != "sqlite" {
		t.Fatal("local management config", err)
	}
	if err = a.syncBusinessAgent(context.Background(), http.DefaultClient); err != nil {
		t.Fatal("converted child still contacted master", err)
	}
	if err = writeJSON(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(cfg.StateDir, "site-local.json")); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(cfgPath)
	if err != nil || !cfg.LocalManagement {
		t.Fatal("portable config lost local management", err)
	}
	if err = writeJSON(filepath.Join(cfg.StateDir, "site-local.json"), object{"site_id": cfg.siteID()}); err != nil {
		t.Fatal(err)
	}
	a.cfg = cfg
	after, _ := a.store.records()
	afterNodes, _ := a.store.nodes()
	if !reflect.DeepEqual(records, after) || !reflect.DeepEqual(nodes, afterNodes) {
		t.Fatal("conversion modified child data")
	}
	for _, u := range mustCoreRecords(t, a) {
		if !u.Enabled {
			t.Fatal("converted child still requires master lease")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !awaitBusinessLease(ctx, a.cfg.StateDir) {
		t.Fatal("converted core wrapper still waits for lease")
	}
}

func TestRemovedLegacySiteRetainsUnsettledAccounting(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro"
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	site, _ := createBusinessTest(t, a, owner, "old_child")
	site.Info = &BusinessInfo{SiteID: site.ID}
	site.Grants = []BusinessGrant{{UserID: u.ID, Quota: 1000}}
	site.Issued = map[int64]int64{u.ID: 1000}
	site.Usage = map[int64]BusinessUsage{u.ID: {Upload: 20}}
	if err := a.store.saveBusinessSite(site); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if w := req(t, a, owner, "DELETE", "/api/business-sites/"+site.ID, nil); w.Code != 200 {
			t.Fatal("immediate/idempotent removal", w.Code)
		}
	}
	after, err := a.store.businessSite(site.ID)
	if err != nil || !after.Removed || after.Enabled || len(after.Grants) != 0 || siteReservation(after, u.ID) != 980 {
		t.Fatal("removed site lost unsettled reservations", err)
	}
	w := req(t, a, owner, "GET", "/api/business-sites", nil)
	var listing struct {
		Sites []BusinessSite `json:"sites"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil || len(listing.Sites) != 0 {
		t.Fatal("removed site still visible")
	}
}

func TestConvertedPoliciesPreserveNodeAccess(t *testing.T) {
	a := testApp(t)
	a.cfg.Role = "business"
	a.cfg.SiteID = "child"
	owner := testUser(t, a, "owner", "owner")
	allowed := testUser(t, a, "allowed", "user")
	denied := testUser(t, a, "denied", "user")
	allowed.CompiledGroups = pointer([]string{"remote-group"})
	denied.CompiledGroups = pointer([]string{})
	allowed.NodeGroupIDs = pointer([]string{})
	denied.NodeGroupIDs = pointer([]string{"remote-group"})
	for _, u := range []*Record{&allowed, &denied} {
		if err := a.store.save(u); err != nil {
			t.Fatal(err)
		}
	}
	n := Node{ID: "remote-node", Protocol: "vless", Enabled: true, PolicyVersion: 1, RateMilli: 2000, GroupIDs: []string{"remote-group"}}
	if err := a.store.saveNode(n); err != nil {
		t.Fatal(err)
	}
	if w := req(t, a, owner, "POST", "/api/site-control", object{"action": "token-management"}); w.Code != 200 {
		t.Fatal("conversion", w.Code)
	}
	a.cfg.LocalManagement = true
	for _, u := range mustCoreRecords(t, a) {
		if u.ID == allowed.ID && (!nodeGroupAllowed(u, n) || u.Credentials.Token != allowed.Credentials.Token) {
			t.Fatal("conversion lost access or credentials")
		}
		if u.ID == denied.ID && nodeGroupAllowed(u, n) {
			t.Fatal("conversion granted previously denied node")
		}
	}
}

func TestMultipleDefaultSitesAndSelfImport(t *testing.T) {
	master := testApp(t)
	master.cfg.Edition = "pro"
	owner := testUser(t, master, "owner", "owner")
	for range 2 {
		child := testApp(t)
		actor := testUser(t, child, "owner", "owner")
		server := httptest.NewServer(child.routes())
		defer server.Close()
		token := fleetToken(t, child, actor, "manage")
		w := req(t, master, owner, "POST", "/api/business-sites/import", object{"token": token, "url": server.URL})
		if w.Code != 201 {
			t.Fatal("default site failed", w.Code, w.Body.String())
		}
	}
	sites, _ := master.store.businessSites()
	if len(sites) != 2 {
		t.Fatal("default IDs merged distinct installations")
	}
	self := httptest.NewServer(master.routes())
	defer self.Close()
	token := fleetToken(t, master, owner, "manage")
	if w := req(t, master, owner, "POST", "/api/business-sites/import", object{"token": token, "url": self.URL}); w.Code != 400 {
		t.Fatal("self-import allowed", w.Code)
	}
}
