package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fleetToken(t *testing.T, a *App, owner Record, scope string) string {
	t.Helper()
	w := req(t, a, owner, "POST", "/api/fleet/tokens", object{"name": "fixture federation", "scope": scope, "days": 1})
	if w.Code != 201 {
		t.Fatal("token creation", w.Code)
	}
	var data struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Token == "" {
		t.Fatal("missing credential")
	}
	return data.Token
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
