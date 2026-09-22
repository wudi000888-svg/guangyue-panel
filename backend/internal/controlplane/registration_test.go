package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setRegistrationSettings(t *testing.T, a *App, enabled, captcha bool) {
	t.Helper()
	b, _ := json.Marshal(SiteSettings{PanelName: "Test", Organization: "Test", DefaultLocale: "zh-CN", RegistrationEnabled: enabled, RegistrationCaptcha: captcha})
	if err := a.store.setMeta("site_settings", string(b)); err != nil {
		t.Fatal(err)
	}
}

func TestPublicRegistrationUsesDemoPlanAndOptionalSlider(t *testing.T) {
	a := testApp(t)
	setRegistrationSettings(t, a, true, true)

	challenge := req(t, a, Record{}, http.MethodGet, "/api/register/challenge", nil)
	if challenge.Code != 200 {
		t.Fatalf("challenge: %d %s", challenge.Code, challenge.Body.String())
	}
	var c struct {
		Token  string `json:"token"`
		Target int    `json:"target"`
	}
	if err := json.Unmarshal(challenge.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	// A wrong slider position must be rejected.
	bad := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(`{"username":"new-member","password":"strong-pass-123","confirm_password":"strong-pass-123","captcha_token":"`+c.Token+`","captcha_position":0}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set("X-Requested-With", "guangyue")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, bad)
	if w.Code != 400 {
		t.Fatalf("wrong slider accepted: %d %s", w.Code, w.Body.String())
	}
	challenge = req(t, a, Record{}, http.MethodGet, "/api/register/challenge", nil)
	if err := json.Unmarshal(challenge.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	w = req(t, a, Record{}, http.MethodPost, "/api/register", object{"username": "new-member", "password": "strong-pass-123", "confirm_password": "strong-pass-123", "captcha_token": c.Token, "captcha_position": c.Target})
	if w.Code != 201 {
		t.Fatalf("registration: %d %s", w.Code, w.Body.String())
	}
	var record Record
	var id int64
	if err := a.store.db.QueryRow("SELECT id FROM users WHERE username=?", "new-member").Scan(&id); err != nil {
		t.Fatal(err)
	}
	record, _ = a.store.record(id)
	if record.Entitlement == nil || record.Entitlement.PlanID != defaultDemoPlan {
		t.Fatalf("registration did not assign demo plan: %#v", record.Entitlement)
	}
}

func TestPublicRegistrationCanSkipSliderWhenDisabled(t *testing.T) {
	a := testApp(t)
	setRegistrationSettings(t, a, true, false)
	w := req(t, a, Record{}, http.MethodPost, "/api/register", object{"username": "plain-member", "password": "strong-pass-123", "confirm_password": "strong-pass-123"})
	if w.Code != 201 {
		t.Fatalf("registration without slider: %d %s", w.Code, w.Body.String())
	}
}
