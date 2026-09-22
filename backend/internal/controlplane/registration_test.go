package controlplane

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setRegistrationSettings(t *testing.T, a *App, enabled, captcha bool) {
	t.Helper()
	b, _ := json.Marshal(SiteSettings{PanelName: "Test", Organization: "Test", DefaultLocale: "zh-CN", RegistrationEnabled: enabled, RegistrationCaptcha: captcha})
	if err := a.store.setMeta("site_settings", string(b)); err != nil {
		t.Fatal(err)
	}
}
func puzzle(t *testing.T, a *App) (string, int) {
	t.Helper()
	w := req(t, a, Record{}, "GET", "/api/register/challenge", nil)
	var c struct {
		Token, Background, Piece string
		Width, Height            int
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatal("challenge failed", w.Code)
	}
	var fields map[string]any
	json.Unmarshal(w.Body.Bytes(), &fields)
	if _, ok := fields["target"]; ok {
		t.Fatal("answer exposed")
	}
	if c.Width != 320 || c.Height != 160 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid puzzle response")
	}
	for _, v := range []string{c.Background, c.Piece} {
		b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "data:image/png;base64,"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = png.Decode(bytes.NewReader(b)); e != nil {
			t.Fatal(e)
		}
	}
	a.registrationMu.Lock()
	stored := a.registrationChallenges[c.Token]
	stored.created = time.Now().Add(-time.Second)
	a.registrationChallenges[c.Token] = stored
	a.registrationMu.Unlock()
	return c.Token, stored.target
}
func solvePuzzle(t *testing.T, a *App, token string, target int) string {
	t.Helper()
	w := req(t, a, Record{}, "POST", "/api/register/verify", object{"token": token, "position": target, "trace": []captchaPoint{{0, 0}, {float64(target) / 3, 200}, {float64(target) * 2 / 3, 400}, {float64(target), 700}}})
	var out struct{ Token string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Token == token {
		t.Fatal("verify failed", w.Code, w.Body.String())
	}
	return out.Token
}
func signupBody(name, token string) object {
	return object{"username": name, "password": "strong-pass-123", "confirm_password": "strong-pass-123", "captcha_token": token}
}
func TestPublicRegistrationUsesDemoPlanAndOptionalSlider(t *testing.T) {
	a := testApp(t)
	setRegistrationSettings(t, a, true, true)
	token, target := puzzle(t, a)
	if w := req(t, a, Record{}, "POST", "/api/register", signupBody("new-member", token)); w.Code != 400 {
		t.Fatal("unverified challenge accepted")
	}
	token, target = puzzle(t, a)
	proof := solvePuzzle(t, a, token, target)
	w := req(t, a, Record{}, "POST", "/api/register", signupBody("new-member", proof))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var id int64
	if err := a.store.db.QueryRow("SELECT id FROM users WHERE username=?", "new-member").Scan(&id); err != nil {
		t.Fatal(err)
	}
	record, _ := a.store.record(id)
	if record.Entitlement == nil || record.Entitlement.PlanID != defaultDemoPlan {
		t.Fatal("registration did not assign demo plan")
	}
	if w = req(t, a, Record{}, "POST", "/api/register", signupBody("replay-member", proof)); w.Code != 400 {
		t.Fatal("proof reused")
	}
}
func TestRegistrationPuzzleRejectsIncorrectExpiredAndCrossClientAttempts(t *testing.T) {
	for _, mode := range []string{"wrong", "expired", "client", "trace"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			setRegistrationSettings(t, a, true, true)
			token, target := puzzle(t, a)
			position := target
			trace := []captchaPoint{{0, 0}, {20, 200}, {60, 400}, {float64(target), 700}}
			if mode == "wrong" {
				position = 0
			}
			if mode == "expired" {
				c := a.registrationChallenges[token]
				c.expires = time.Now().Add(-time.Second)
				a.registrationChallenges[token] = c
			}
			if mode == "trace" {
				trace = nil
			}
			r := httptest.NewRequest("POST", "/api/register/verify", bytes.NewReader(jsonBytes(object{"token": token, "position": position, "trace": trace})))
			r.Header.Set("X-Requested-With", "guangyue")
			r.Header.Set("Content-Type", "application/json")
			if mode == "client" {
				r.Header.Set("User-Agent", "different-browser")
			}
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatal("invalid attempt accepted", w.Code)
			}
			if _, ok := a.registrationChallenges[token]; ok {
				t.Fatal("failed puzzle reusable")
			}
		})
	}
}
func TestRegistrationPuzzleRefreshAndRateLimit(t *testing.T) {
	a := testApp(t)
	setRegistrationSettings(t, a, true, true)
	old, _ := puzzle(t, a)
	puzzle(t, a)
	if _, ok := a.registrationChallenges[old]; ok {
		t.Fatal("refresh retained old puzzle")
	}
	for i := 0; i < 10; i++ {
		puzzle(t, a)
	}
	if w := req(t, a, Record{}, "GET", "/api/register/challenge", nil); w.Code != 429 {
		t.Fatal("missing challenge rate limit")
	}
}
func TestPublicRegistrationCanSkipSliderWhenDisabled(t *testing.T) {
	a := testApp(t)
	setRegistrationSettings(t, a, true, false)
	w := req(t, a, Record{}, "POST", "/api/register", signupBody("plain-member", ""))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = req(t, a, Record{}, "GET", "/api/register/challenge", nil); w.Code != 404 {
		t.Fatal("disabled captcha generated")
	}
}
