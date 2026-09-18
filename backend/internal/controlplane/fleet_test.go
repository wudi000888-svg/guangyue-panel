package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fleetToken(t *testing.T, a *App, owner Record, scope string) string {
	_, token, _ := fleetTokenWithOptions(t, a, owner, scope, false)
	return token
}
func fleetTokenWithOptions(t *testing.T, a *App, owner Record, scope string, forever bool) (string, string, int64) {
	t.Helper()
	days := 1
	if forever {
		days = 0
	}
	w := req(t, a, owner, "POST", "/api/fleet/tokens", object{"name": "fixture federation", "scope": scope, "days": days, "forever": forever})
	if w.Code != 201 {
		t.Fatal("token creation", w.Code)
	}
	var data struct {
		ID      string `json:"id"`
		Token   string `json:"token"`
		Expires int64  `json:"expires"`
	}
	if json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Token == "" {
		t.Fatal("missing credential")
	}
	return data.ID, data.Token, data.Expires
}

func TestFleetPermanentToken(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro"
	owner := testUser(t, a, "permanent-owner", "owner")
	id, token, expires := fleetTokenWithOptions(t, a, owner, "manage", true)
	if id == "" || expires != 0 {
		t.Fatalf("permanent token expiry: id=%q expires=%d", id, expires)
	}

	var listing struct {
		Tokens []struct {
			ID      string `json:"id"`
			Expires int64  `json:"expires"`
		} `json:"tokens"`
	}
	w := req(t, a, owner, "GET", "/api/fleet", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil {
		t.Fatalf("permanent token listing: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, item := range listing.Tokens {
		if item.ID == id {
			found = true
			if item.Expires != 0 {
				t.Fatalf("permanent token changed in listing: %d", item.Expires)
			}
		}
	}
	if !found {
		t.Fatal("permanent token missing from listing")
	}

	b, _ := json.Marshal(object{"method": "GET", "path": "/api/operations", "body": object{}})
	r := httptest.NewRequest("POST", "/api/fleet-gateway", strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "guangyue")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("permanent token gateway: %d %s", w.Code, w.Body.String())
	}

	if w = req(t, a, owner, "DELETE", "/api/fleet/tokens/"+id, nil); w.Code != 200 {
		t.Fatalf("permanent token revoke: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/api/fleet-gateway", strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "guangyue")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("revoked permanent token accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestFleetRegistrationForwardingIsolationAndRevocation(t *testing.T) {
	local, remote := testApp(t), testApp(t)
	local.cfg.Edition, remote.cfg.Edition = "pro", "pro"
	local.cfg.SiteID, remote.cfg.SiteID = "central", "remote"
	owner := testUser(t, local, "central-owner", "owner")
	member := testUser(t, local, "central-member", "user")
	remoteOwner := testUser(t, remote, "remote-owner", "owner")
	server := httptest.NewServer(remote.routes())
	defer server.Close()
	token := fleetToken(t, remote, remoteOwner, "manage")
	w := req(t, local, owner, "POST", "/api/fleet/peers", object{"name": "Remote fixture", "url": server.URL, "token": token})
	if w.Code != 201 {
		t.Fatal("peer registration", w.Code)
	}
	var peer FleetPeer
	if json.Unmarshal(w.Body.Bytes(), &peer) != nil || peer.Token != "" {
		t.Fatal("peer secret returned")
	}
	var encrypted []byte
	if err := local.store.db.QueryRow("SELECT doc FROM fleet_peers WHERE id=?", peer.ID).Scan(&encrypted); err != nil || strings.Contains(string(encrypted), token) {
		t.Fatal("peer secret not encrypted")
	}
	forward := func(actor Record) *httptest.ResponseRecorder {
		session := randomToken(32)
		_, err := local.store.db.Exec("INSERT INTO sessions VALUES(?,?,?)", digest(session), actor.ID, time.Now().Add(time.Hour).Unix())
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/api/state", nil)
		r.AddCookie(&http.Cookie{Name: "gy_session", Value: session})
		r.Header.Set("X-Guangyue-Site", peer.ID)
		w := httptest.NewRecorder()
		local.routes().ServeHTTP(w, r)
		return w
	}
	w = forward(owner)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote-owner") || strings.Contains(w.Body.String(), "central-member") {
		t.Fatal("site forwarding failed", w.Code)
	}
	if w = forward(member); w.Code != 403 {
		t.Fatal("member accessed remote administration")
	}
	remote.cfg.SiteID = "changed"
	if w = forward(owner); w.Code != 502 {
		t.Fatal("site identity substitution accepted")
	}
	remote.cfg.SiteID = "remote"
	if _, err := remote.store.db.Exec("DELETE FROM fleet_tokens"); err != nil {
		t.Fatal(err)
	}
	if w = forward(owner); w.Code != 502 {
		t.Fatal("revoked remote token accepted")
	}
}
func TestFleetReadonlyGatewayAndLocalAuthorization(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro"
	owner := testUser(t, a, "owner", "owner")
	token := fleetToken(t, a, owner, "read")
	invoke := func(method, path string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(object{"method": method, "path": path, "body": object{}})
		r := httptest.NewRequest("POST", "/api/fleet-gateway", strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Requested-With", "guangyue")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		return w
	}
	if w := invoke("DELETE", "/api/nodes/vless-main"); w.Code != 403 {
		t.Fatal("readonly mutation")
	}
	if w := invoke("GET", "/api/backup"); w.Code != 403 {
		t.Fatal("recovery material exposed")
	}
	if w := invoke("GET", "/api/operations"); w.Code != 200 {
		t.Fatal("readonly state failed")
	}
	owner.Enabled = false
	if err := a.store.save(&owner); err != nil {
		t.Fatal(err)
	}
	if w := invoke("GET", "/api/operations"); w.Code != 403 {
		t.Fatal("disabled owner token accepted")
	}
}

func TestLitePairingTokenAndProTakeover(t *testing.T) {
	lite := testApp(t)
	lite.cfg.Edition, lite.cfg.SiteID = "lite", "child"
	liteOwner := testUser(t, lite, "lite-owner", "owner")
	server := httptest.NewServer(lite.routes())
	defer server.Close()

	// Lite can issue a pairing token, but the scope is always management so a
	// Pro master can take over the site after the token is pasted.
	token := fleetToken(t, lite, liteOwner, "read")
	pro := testApp(t)
	pro.cfg.Edition, pro.cfg.Role, pro.cfg.SiteID = "pro", "controller", "master"
	proOwner := testUser(t, pro, "pro-owner", "owner")
	w := req(t, pro, proOwner, "POST", "/api/fleet/peers", object{"name": "Lite child", "url": server.URL, "token": token})
	if w.Code != 201 {
		t.Fatalf("Pro could not take over Lite with pairing token: %d %s", w.Code, w.Body.String())
	}
	var peer FleetPeer
	if err := json.Unmarshal(w.Body.Bytes(), &peer); err != nil || peer.Scope != "manage" || peer.SiteID != "child" {
		t.Fatalf("unexpected paired site: %+v", peer)
	}
	if w = req(t, lite, liteOwner, "POST", "/api/fleet/peers", object{"name": "blocked", "url": server.URL, "token": token}); w.Code != 403 {
		t.Fatalf("Lite created a peer: %d", w.Code)
	}
	if w = req(t, lite, liteOwner, "GET", "/api/fleet", nil); w.Code != 200 || strings.Contains(w.Body.String(), peer.ID) {
		t.Fatalf("Lite exposed controller peer directory: %d %s", w.Code, w.Body.String())
	}
}

func TestLegacyPeerBecomesBusinessSiteOnUnifiedPage(t *testing.T) {
	lite := testApp(t)
	lite.cfg.Edition, lite.cfg.SiteID = "lite", "legacy_child"
	liteOwner := testUser(t, lite, "legacy-owner", "owner")
	server := httptest.NewServer(lite.routes())
	defer server.Close()

	pro := testApp(t)
	pro.cfg.Edition, pro.cfg.Role, pro.cfg.SiteID = "pro", "controller", "master"
	proOwner := testUser(t, pro, "master-owner", "owner")
	token := fleetToken(t, lite, liteOwner, "manage")
	if w := req(t, pro, proOwner, "POST", "/api/fleet/peers", object{"name": "Legacy child", "url": server.URL, "token": token}); w.Code != 201 {
		t.Fatalf("legacy peer registration: %d %s", w.Code, w.Body.String())
	}

	var listing struct {
		Sites []BusinessSite `json:"sites"`
	}
	if w := req(t, pro, proOwner, "GET", "/api/business-sites", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil {
		t.Fatalf("unified business listing: %d %s", w.Code, w.Body.String())
	}
	if len(listing.Sites) != 1 || listing.Sites[0].ID != "legacy_child" {
		t.Fatalf("legacy peer was not converted: %+v", listing.Sites)
	}
	var peers int
	if err := pro.store.db.QueryRow("SELECT COUNT(*) FROM fleet_peers").Scan(&peers); err != nil || peers != 0 {
		t.Fatalf("legacy peer remains after conversion: %d %v", peers, err)
	}
	marker, err := os.ReadFile(filepath.Join(lite.cfg.StateDir, businessAdoptionFile))
	if err != nil || !strings.Contains(string(marker), "https://panel.test") {
		t.Fatalf("adoption marker missing: %v %s", err, marker)
	}
}

func TestIndependentSiteCanStageBusinessAdoption(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition, a.cfg.Role, a.cfg.SiteID = "lite", "standalone", "standalone_site"
	owner := testUser(t, a, "site-owner", "owner")
	token := "gye_" + strings.Repeat("A", 43)
	w := req(t, a, owner, "POST", "/api/adopt", businessAdoption{SiteID: a.cfg.SiteID, ControllerURL: "https://master.example.com", ConnectToken: token})
	if w.Code != 200 {
		t.Fatalf("stage adoption: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.cfg.StateDir, businessAdoptionFile)); err != nil {
		t.Fatalf("adoption marker not written: %v", err)
	}
}
