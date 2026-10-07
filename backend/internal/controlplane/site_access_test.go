package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func countryRequest(a *App, path, ip, claimed string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = ip
	r.Header.Set("X-Real-IP", claimed)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func setCountryPolicy(t *testing.T, a *App, enabled bool) {
	t.Helper()
	v := a.store.siteSettings()
	v.CountryAccessEnabled = enabled
	v.CountryAccessBlocked = []string{"US", "CA"}
	v.CountryAccessReason = "此地区暂不开放服务 <script>"
	b, _ := json.Marshal(v)
	if err := a.store.setMeta("site_settings", string(b)); err != nil {
		t.Fatal(err)
	}
}

func TestSiteCountryAccessDefaultOffAndHeaderTrust(t *testing.T) {
	a := testApp(t)
	status := decoded[SiteAccessStatus](t, countryRequest(a, "/api/access-status", "8.8.8.8:4000", ""), 200)
	if status.Enabled || !status.Allowed || status.IP != "" || status.CountryCode != "" {
		t.Fatal("detection must be off by default")
	}
	setCountryPolicy(t, a, true)
	for _, test := range []struct{ remote, claimed, expected, country string }{{"8.8.8.8:4000", "223.5.5.5", "8.8.8.8", "US"}, {"127.0.0.1:4000", "8.8.8.8", "8.8.8.8", "US"}, {"[::ffff:127.0.0.1]:4000", "::ffff:8.8.8.8", "8.8.8.8", "US"}, {"[::1]:4000", "2001:4860:4860::8888", "2001:4860:4860::8888", "CA"}} {
		status := decoded[SiteAccessStatus](t, countryRequest(a, "/api/access-status", test.remote, test.claimed), 200)
		if status.Allowed || status.CountryCode != test.country || status.IP != test.expected || status.Reason == "" {
			t.Fatalf("header trust failed: %+v", status)
		}
	}
	status = decoded[SiteAccessStatus](t, countryRequest(a, "/api/access-status?ip=8.8.8.8", "223.5.5.5:4000", "8.8.8.8"), 200)
	if !status.Allowed || status.CountryCode != "CN" || status.IP != "223.5.5.5" {
		t.Fatal("request address must not be supplied by query/header")
	}
}

func TestSiteCountryAccessProtectsPanelAndRetainsIntegrationPaths(t *testing.T) {
	a := testApp(t)
	setCountryPolicy(t, a, true)
	for _, path := range []string{"/api/site", "/api/state", "/api/login", "/api/settings", "/api/clients/download/v2rayn/0"} {
		w := countryRequest(a, path, "8.8.8.8:4000", "")
		v := decoded[struct {
			Denied bool   `json:"access_denied"`
			IP     string `json:"ip"`
			Reason string `json:"reason"`
		}](t, w, http.StatusForbidden)
		if !v.Denied || v.IP != "8.8.8.8" || !strings.Contains(v.Reason, "地区") {
			t.Fatalf("panel restriction missing: %s", path)
		}
	}
	for _, path := range []string{"/api/health", "/sub/invalid-token", "/api/fleet-gateway", "/api/business/sync", "/api/payments/webhook/unknown"} {
		w := countryRequest(a, path, "8.8.8.8:4000", "")
		if strings.Contains(w.Body.String(), "access_denied") {
			t.Fatalf("machine integration restricted: %s", path)
		}
	}
	status := decoded[SiteAccessStatus](t, countryRequest(a, "/api/access-status", "127.0.0.1:4000", ""), 200)
	if !status.Allowed || status.CountryCode != "" {
		t.Fatal("private/unknown addresses must not be assigned a guessed country")
	}
	setCountryPolicy(t, a, false)
	decoded[SiteSettings](t, countryRequest(a, "/api/site", "8.8.8.8:4000", ""), 200)
}

func TestSiteAccessSettingsPersistenceDefaultsAndValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	v := a.store.siteSettings()
	if len(v.SidebarAdmin) > 6 || len(v.SidebarMember) > 4 || v.ClientDownloadRelay || v.CountryAccessEnabled {
		t.Fatal("default navigation and feature switches")
	}
	v.SidebarAdmin = []string{}
	v.SidebarMember = []string{"clients", "clients"}
	v.ClientDownloadRelay = true
	v.CountryAccessBlocked = []string{" cn ", "CN", "us"}
	saved := decoded[SiteSettings](t, req(t, a, owner, "PUT", "/api/settings", v), 200)
	if len(saved.SidebarAdmin) != 1 || saved.SidebarAdmin[0] != "settings" || len(saved.SidebarMember) != 2 || len(saved.CountryAccessBlocked) != 2 || !saved.ClientDownloadRelay {
		t.Fatal("settings normalization or persistence")
	}
	for _, change := range []func(*SiteSettings){func(v *SiteSettings) { v.SidebarMember = []string{"users"} }, func(v *SiteSettings) { v.SidebarAdmin = []string{"unknown"} }, func(v *SiteSettings) { v.CountryAccessBlocked = []string{"XX"} }, func(v *SiteSettings) { v.CountryAccessReason = strings.Repeat("界", 501) }} {
		bad := a.store.siteSettings()
		change(&bad)
		if req(t, a, owner, "PUT", "/api/settings", bad).Code != 400 {
			t.Fatal("invalid security/navigation settings accepted")
		}
	}
	if req(t, a, member, "GET", "/api/access-check", nil).Code != 403 {
		t.Fatal("member may not inspect admin security settings")
	}
	decoded[SiteAccessStatus](t, req(t, a, owner, "GET", "/api/access-check", nil), 200)
}

func TestSiteCountryPolicyReadFailureDoesNotSilentlyAllow(t *testing.T) {
	a := testApp(t)
	if err := a.store.setMeta("site_settings", "{broken"); err != nil {
		t.Fatal(err)
	}
	if countryRequest(a, "/api/site", "8.8.8.8:4000", "").Code != 503 {
		t.Fatal("unreadable policy allowed access")
	}
}
