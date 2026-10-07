package controlplane

import (
	"net/http"
	"slices"
	"strings"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/geoip"
)

type SiteAccessStatus struct {
	Enabled         bool   `json:"enabled"`
	Allowed         bool   `json:"allowed"`
	IP              string `json:"ip"`
	CountryCode     string `json:"country_code"`
	CountryName     string `json:"country_name"`
	Reason          string `json:"reason"`
	DatabaseVersion string `json:"database_version"`
}

func (a *App) siteAccessStatus(r *http.Request, inspect bool) (SiteAccessStatus, error) {
	settings, err := a.store.readSiteSettings()
	if err != nil {
		return SiteAccessStatus{}, err
	}
	out := SiteAccessStatus{Enabled: settings.CountryAccessEnabled, Allowed: true, DatabaseVersion: geoip.Version}
	if !settings.CountryAccessEnabled && !inspect {
		return out, nil
	}
	out.IP = requestIP(r)
	country, err := geoip.Lookup(out.IP)
	if err != nil {
		return out, err
	}
	out.CountryCode, out.CountryName = country.Code, country.Name
	if settings.DefaultLocale == "en" && country.EnglishName != "" {
		out.CountryName = country.EnglishName
	}
	// Unallocated/private/unknown addresses do not acquire a guessed country.
	if settings.CountryAccessEnabled && country.Code != "" && slices.Contains(settings.CountryAccessBlocked, country.Code) {
		out.Allowed = false
		out.Reason = settings.CountryAccessReason
		if out.Reason == "" {
			out.Reason = "管理员已限制此国家或地区访问本站"
		}
	}
	return out, nil
}

func (a *App) accessStatus(w http.ResponseWriter, r *http.Request) {
	status, err := a.siteAccessStatus(r, false)
	if err != nil {
		failure(w, 503, "访问规则暂不可用，请稍后重试")
		return
	}
	jsonResponse(w, 200, status)
}

func (a *App) accessCheck(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.Role != "owner" {
		failure(w, 403, "需要管理员权限")
		return
	}
	status, err := a.siteAccessStatus(r, true)
	if err != nil {
		failure(w, 503, "读取国家访问检测失败")
		return
	}
	jsonResponse(w, 200, status)
}

// Restrict the browser panel, including login and authenticated API calls.
// Core traffic, subscriptions and authenticated machine integrations continue.
func browserAccessPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	if path == "/api/health" || path == "/api/access-status" || path == "/api/fleet-gateway" {
		return false
	}
	return !strings.HasPrefix(path, "/api/business/") && !strings.HasPrefix(path, "/api/payments/webhook/")
}

func (a *App) enforceSiteAccess(w http.ResponseWriter, r *http.Request) bool {
	if !browserAccessPath(r.URL.Path) {
		return true
	}
	status, err := a.siteAccessStatus(r, false)
	if err != nil {
		failure(w, 503, "访问规则暂不可用，请稍后重试")
		return false
	}
	if status.Allowed {
		return true
	}
	jsonResponse(w, http.StatusForbidden, object{"error": status.Reason, "access_denied": true, "enabled": status.Enabled, "allowed": false, "ip": status.IP, "country_code": status.CountryCode, "country_name": status.CountryName, "reason": status.Reason, "database_version": status.DatabaseVersion})
	return false
}
