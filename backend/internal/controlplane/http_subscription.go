package controlplane

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) subscriptionInfo(w http.ResponseWriter, r *http.Request, actor Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := actor.ID
	if x := r.URL.Query().Get("user_id"); x != "" {
		id, _ = strconv.ParseInt(x, 10, 64)
	}
	if id != actor.ID && actor.Role != "owner" {
		failure(w, 403, "无权访问其他用户订阅")
		return
	}
	record, err := a.store.record(id)
	if err != nil {
		failure(w, 404, "用户不存在")
		return
	}
	_, err = a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	pool := r.URL.Query().Get("pool")
	if pool != "" && pool != "private" && pool != "public" && pool != "all" && pool != "local" && pool != "mounted" && pool != "subsite" && pool != "mixed" {
		failure(w, 400, "订阅池不存在")
		return
	}
	if pool == "" {
		pool = "mixed"
	}
	// Members have one canonical subscription. Source-specific pools remain an
	// administrator diagnostic view and cannot be selected through a member
	// session or by changing the query string.
	if actor.Role != "owner" {
		pool = "mixed"
	}
	public := pool == "public"
	protocol := r.URL.Query().Get("protocol")
	if protocol != "" && protocol != "vless" && protocol != "hy2" {
		failure(w, 400, "节点协议无效")
		return
	}
	views := []SubscriptionNode{}
	lines := []string{}
	if record.Active() && record.Mount == nil {
		entries, err := a.subscriptionCatalogSource(record, pool, protocol)
		if err != nil {
			failure(w, 500, "读取订阅节点失败")
			return
		}
		for _, entry := range entries {
			n := entry.node
			views = append(views, SubscriptionNode{Mounted: entry.mounted, Source: func() string {
				if entry.mounted {
					return "mounted"
				}
				if n.ManagedBy == publicManager {
					return "public"
				}
				return "local"
			}(), GroupIDs: authorizedNodeGroups(record, n), MemberNodeQuality: MemberNodeQuality{RateMilli: nodeRate(n), ID: n.ID, Name: entry.name, Protocol: n.Protocol, ProbeIP: n.ProbeIP, Country: n.Country, CountryCode: n.CountryCode, CheckedAt: n.CheckedAt, Quality: memberQualityReport(n)}, URI: entry.uri, DNS: n.DNS, SiteID: entry.siteID, SiteName: entry.siteName})
			lines = append(lines, entry.uri)
		}
	}
	if !record.Active() {
		views = []SubscriptionNode{}
		lines = []string{}
	}
	raw := ""
	if len(lines) > 0 {
		raw = strings.Join(lines, "\n") + "\n"
	}
	address := a.cfg.PublicURL + "/sub/" + record.Credentials.Token
	if public {
		address = a.cfg.PublicURL + "/public-sub/" + strconv.FormatInt(record.ID, 10) + "/" + record.Credentials.PublicToken
	}
	if !public && pool != "mixed" {
		source := pool
		if source == "private" {
			source = "local"
		}
		address += "?source=" + source
	}
	jsonResponse(w, 200, object{"url": address, "pool": pool, "raw": raw, "nodes": views, "protocol": protocol, "active": record.Active() && (len(views) > 0 || public), "user": record.User})
}
func (a *App) serveSubscription(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) != 43 {
		failure(w, 404, "订阅不存在")
		return
	}
	// Rendering only reads committed rows, so it runs without the control
	// lock; clients must not stall behind core reconciliation. Only the
	// access-time write below needs the lock that guards user documents.
	var id int64
	public := r.PathValue("user") != ""
	var err error
	if public {
		id, err = strconv.ParseInt(r.PathValue("user"), 10, 64)
	} else {
		err = a.store.db.QueryRow("SELECT id FROM users WHERE token_hash=?", digest(token)).Scan(&id)
	}
	record, e := a.store.record(id)
	if err != nil || e != nil || public && subtle.ConstantTimeCompare([]byte(token), []byte(record.Credentials.PublicToken)) != 1 {
		failure(w, 404, "订阅不存在")
		return
	}
	if !record.Active() || record.Mount != nil {
		failure(w, 403, "账号已停用、到期或额度已用尽")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	source := r.URL.Query().Get("source")
	if public {
		source = "public"
	} else if source == "" {
		source = "mixed"
	}
	if !public && record.Role != "owner" {
		source = "mixed"
	}
	if source != "private" && source != "public" && source != "all" && source != "local" && source != "mounted" && source != "subsite" && source != "mixed" {
		failure(w, 400, "订阅来源无效")
		return
	}
	entries, err := a.subscriptionCatalogSource(record, source, r.URL.Query().Get("protocol"))
	if err != nil {
		failure(w, 500, "读取订阅节点失败")
		return
	}
	b, typ, err := renderSubscriptionEntries(a.cfg, nodes, entries, r.URL.Query().Get("format"), public)
	if err != nil {
		if errors.Is(err, errNoNodes) {
			failure(w, 403, "没有获授权的可用节点")
			return
		}
		failure(w, 400, "订阅格式不支持")
		return
	}
	if err = a.markSubscriptionAccess(id); err != nil {
		failure(w, 500, "保存访问时间失败")
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", record.QuotaUpload(), record.QuotaDownload(), record.Quota, record.Expires))
	updateHours := "6"
	if public {
		updateHours = "1"
	}
	w.Header().Set("Profile-Update-Interval", updateHours)
	w.Header().Set("Content-Disposition", "attachment; filename="+map[bool]string{true: "guangyue-public.", false: "guangyue."}[public]+map[bool]string{true: "yaml", false: "txt"}[r.URL.Query().Get("format") == "mihomo"])
	w.Write(b)
}

// markSubscriptionAccess re-reads the user under the control lock so the
// access time never overwrites a newer document written meanwhile. The time
// is informational: while the control loop is busy it is skipped rather than
// making the client wait.
func (a *App) markSubscriptionAccess(id int64) error {
	if !a.mu.TryLock() {
		return nil
	}
	defer a.mu.Unlock()
	current, err := a.store.record(id)
	if err != nil {
		return err
	}
	return a.store.markSubscriptionAccess(&current, time.Now().Unix())
}
func (a *App) hyAuth(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Addr string `json:"addr"`
		Auth string `json:"auth"`
		TX   int64  `json:"tx"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		jsonResponse(w, 200, object{"ok": false})
		return
	}
	if identity, ok := a.hyAuthenticate(input.Auth); ok {
		jsonResponse(w, 200, object{"ok": true, "id": identity})
		return
	}
	jsonResponse(w, 200, object{"ok": false})
}
