package controlplane

import (
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, object{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		failure(w, 400, "请求格式无效")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		failure(w, 400, "请求格式无效")
		return false
	}
	return true
}
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/business/enroll", a.businessEnroll)
	mux.HandleFunc("POST /api/business/sync", a.businessSync)
	mux.HandleFunc("POST /api/fleet-gateway", a.fleetGateway)
	mux.HandleFunc("GET /api/site", a.siteInfo)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, object{"ok": true, "version": version, "edition": a.cfg.edition(), "product": a.cfg.productName(), "site_id": a.cfg.siteID(), "role": a.cfg.deploymentRole()})
	})
	mux.HandleFunc("GET /sub/{token}", a.serveSubscription)
	mux.HandleFunc("GET /public-sub/{user}/{token}", a.serveSubscription)
	mux.HandleFunc("/api/", a.authenticated)
	files := http.FileServer(http.Dir(a.cfg.WebDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			failure(w, 405, "方法不支持")
			return
		}
		path := filepath.Join(a.cfg.WebDir, filepath.Clean("/"+r.URL.Path))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			http.ServeFile(w, r, filepath.Join(a.cfg.WebDir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.businessAgent() && r.URL.Path != "/api/health" {
			failure(w, 404, "此业务站由主控管理")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/sub/") || strings.HasPrefix(r.URL.Path, "/public-sub/") {
			w.Header().Set("Cache-Control", "no-store, private")
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if a.controlReady() != nil {
				failure(w, 503, "站点控制器暂不可用")
				return
			}
			if r.Header.Get("X-Requested-With") != "guangyue" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				failure(w, 403, "请求来源校验失败")
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" && origin != a.cfg.PublicURL {
				failure(w, 403, "请求来源校验失败")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) rateAllowed(ip string) bool {
	a.limitMu.Lock()
	defer a.limitMu.Unlock()
	now := time.Now()
	for k, v := range a.limits {
		if len(v) == 0 || now.Sub(v[len(v)-1]) > 15*time.Minute {
			delete(a.limits, k)
		}
	}
	v := a.limits[ip]
	kept := v[:0]
	for _, t := range v {
		if now.Sub(t) < 15*time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 12 || len(a.limits) >= 4096 {
		return false
	}
	a.limits[ip] = append(kept, now)
	return true
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "127.0.0.1" || ip == "::1" {
		if x := r.Header.Get("X-Real-IP"); net.ParseIP(x) != nil {
			ip = x
		}
	}
	if !a.rateAllowed(ip) {
		failure(w, 429, "登录尝试过多，请稍后重试")
		return
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		failure(w, 429, "登录繁忙，请稍后重试")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Password) > 72 {
		failure(w, 401, "账号或密码不正确")
		return
	}
	var id int64
	err := a.store.db.QueryRow("SELECT id FROM users WHERE username=?", input.Username).Scan(&id)
	record, e := a.store.record(id)
	hash := record.Password
	if err != nil || e != nil {
		hash = []byte("$2a$12$5MJHI2X5oyMlWCDFi.QcEeYZkyCvZcjBrghueRf.T28NiMvIaGH0C")
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(input.Password)) != nil || err != nil || e != nil || !record.Enabled {
		failure(w, 401, "账号或密码不正确")
		return
	}
	token := randomToken(32)
	expires := time.Now().Add(8 * time.Hour)
	_, err = a.store.db.Exec("INSERT INTO sessions(token_hash,user_id,expires) VALUES(?,?,?)", digest(token), id, expires.Unix())
	if err != nil {
		failure(w, 500, "无法创建会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "gy_session", Value: token, Path: "/", HttpOnly: true, Secure: !a.cfg.Dev, SameSite: http.SameSiteStrictMode, Expires: expires})
	jsonResponse(w, 200, object{"user": record.User})
}
func (a *App) session(r *http.Request) (Record, error) {
	cookie, err := r.Cookie("gy_session")
	if err != nil {
		return Record{}, err
	}
	var id int64
	err = a.store.db.QueryRow("SELECT user_id FROM sessions WHERE token_hash=? AND expires>?", digest(cookie.Value), time.Now().Unix()).Scan(&id)
	if err != nil {
		return Record{}, err
	}
	record, err := a.store.record(id)
	if err == nil && !record.Enabled {
		err = errors.New("disabled")
	}
	return record, err
}
func (a *App) authenticated(w http.ResponseWriter, r *http.Request) {
	actor, err := a.session(r)
	if err != nil {
		failure(w, 401, "请登录")
		return
	}
	if peer := r.Header.Get("X-Guangyue-Site"); peer != "" {
		a.relaySiteAPI(w, r, actor, peer)
		return
	}
	a.dispatchAuthenticated(w, r, actor)
}
func (a *App) dispatchAuthenticated(w http.ResponseWriter, r *http.Request, actor Record) {
	switch {
	case r.URL.Path == "/api/messages" || strings.HasPrefix(r.URL.Path, "/api/messages/"):
		a.messages(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/operations":
		a.operations(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/state":
		a.state(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/dashboard":
		a.dashboard(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/subscription":
		a.subscriptionInfo(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/usage":
		a.userUsage(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/node-quality":
		a.memberNodeQuality(w, r, actor)
	case r.Method == "POST" && r.URL.Path == "/api/logout":
		cookie, _ := r.Cookie("gy_session")
		_, _ = a.store.db.Exec("DELETE FROM sessions WHERE token_hash=?", digest(cookie.Value))
		http.SetCookie(w, &http.Cookie{Name: "gy_session", Value: "", Path: "/", HttpOnly: true, Secure: !a.cfg.Dev, MaxAge: -1, SameSite: http.SameSiteStrictMode})
		jsonResponse(w, 200, object{"ok": true})
	case r.Method == "POST" && r.URL.Path == "/api/password":
		a.password(w, r, actor)
	default:
		if actor.Role != "owner" {
			failure(w, 403, "需要管理员权限")
			return
		}
		a.admin(w, r, actor)
	}
}
func (a *App) state(w http.ResponseWriter, r *http.Request, actor Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.store.record(actor.ID)
	if err != nil || !current.Enabled {
		failure(w, 401, "请登录")
		return
	}
	actor = current
	resolved := []Record{actor}
	if err := a.store.resolveAccess(resolved); err != nil {
		failure(w, 500, "读取授权失败")
		return
	}
	actor = resolved[0]
	records, err := a.store.records()
	if err != nil {
		failure(w, 500, "读取用户失败")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	users := []User{}
	var up, down int64
	active := 0
	for _, u := range records {
		if actor.Role != "owner" && u.ID != actor.ID {
			continue
		}
		users = append(users, u.User)
		up += u.Upload
		down += u.Download
		if u.Active() {
			active++
		}
	}
	publicNodes := []Node{}
	for _, n := range nodes {
		if actor.Role != "owner" {
			if !memberMayUseNode(actor, n) {
				continue
			}
			n = memberNodeState(n)
		} else {
			n = n.public()
		}
		publicNodes = append(publicNodes, n)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	system := object{"version": version, "edition": a.cfg.edition(), "site_id": a.cfg.siteID(), "database": a.store.db.Driver(), "cache": a.cache.Mode(), "cache_degraded": a.cache.Degraded(), "uptime": int64(time.Since(a.started).Seconds()), "heap": mem.HeapAlloc, "status": a.status, "error": a.syncError, "traffic_error": a.trafficError, "applied_at": a.appliedAt, "panel_host": a.cfg.PublicURL, "vless_host": a.cfg.VLESSHost, "hy2_host": a.cfg.HY2Host, "cert_expires": certExpiry(a.cfg.Cert)}
	if actor.Role != "owner" {
		system["error"] = ""
		system["traffic_error"] = ""
	}
	if actor.Role == "owner" {
		system["reality_sni"] = a.cfg.RealitySNI
		system["hy2_old_connections"] = a.oldHYConnections
		system["services"] = []object{{"name": "Xray", "active": a.cfg.Dev || corePID("guangyue-xray.service") > 0}, {"name": "Hysteria2", "active": a.cfg.Dev || corePID("guangyue-hy2.service") > 0}, {"name": "Nginx", "active": a.cfg.Dev || corePID("nginx.service") > 0}}
	}
	history := []object{}
	query := "SELECT hour,SUM(upload),SUM(download) FROM traffic WHERE hour>=?"
	args := []any{time.Now().Add(-24 * time.Hour).Unix()}
	if actor.Role != "owner" {
		query += " AND user_id=?"
		args = append(args, actor.ID)
	}
	query += " GROUP BY hour ORDER BY hour"
	rows, err := a.store.db.Query(query, args...)
	if err == nil {
		for rows.Next() {
			var hour, u, d int64
			if rows.Scan(&hour, &u, &d) == nil {
				history = append(history, object{"hour": hour, "upload": u, "download": d})
			}
		}
		rows.Close()
	}
	audit := []object{}
	if actor.Role == "owner" {
		rows, err = a.store.db.Query("SELECT at,actor,action,target FROM audit ORDER BY id DESC LIMIT 30")
		if err == nil {
			for rows.Next() {
				var at int64
				var by, action, target string
				if rows.Scan(&at, &by, &action, &target) == nil {
					audit = append(audit, object{"at": at, "actor": by, "action": action, "target": target})
				}
			}
			rows.Close()
		}
	}
	ipPool := []IPResource{}
	if actor.Role == "owner" {
		pools, e := a.store.pools()
		if e != nil {
			failure(w, 500, "读取 IP 池失败")
			return
		}
		for _, p := range pools {
			ipPool = append(ipPool, p.public(nodes))
		}
	}
	if actor.Role != "owner" && !actor.Active() {
		publicNodes = []Node{}
		active = 0
	}
	jsonResponse(w, 200, object{"me": actor.User, "users": users, "nodes": publicNodes, "ip_pool": ipPool, "system": system, "site": a.store.siteSettings(), "runtime": a.store.publicRuntimeState(), "unread_messages": a.store.unreadMessages(actor.ID), "totals": object{"upload": up, "download": down, "active": active}, "history": history, "audit": audit})
}
func certExpiry(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return 0
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0
	}
	return cert.NotAfter.Unix()
}
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
	if pool != "" && pool != "private" && pool != "public" {
		failure(w, 400, "订阅池不存在")
		return
	}
	public := pool == "public"
	protocol := r.URL.Query().Get("protocol")
	if protocol != "" && protocol != "vless" && protocol != "hy2" {
		failure(w, 400, "节点协议无效")
		return
	}
	views := []SubscriptionNode{}
	lines := []string{}
	if record.Active() {
		entries, err := a.subscriptionCatalog(record, public, protocol)
		if err != nil {
			failure(w, 500, "读取订阅节点失败")
			return
		}
		for _, entry := range entries {
			n := entry.node
			views = append(views, SubscriptionNode{MemberNodeQuality: MemberNodeQuality{RateMilli: nodeRate(n), ID: n.ID, Name: entry.name, Protocol: n.Protocol, ProbeIP: n.ProbeIP, Country: n.Country, CountryCode: n.CountryCode, CheckedAt: n.CheckedAt, Quality: memberQualityReport(n)}, URI: entry.uri, DNS: n.DNS, SiteID: entry.siteID, SiteName: entry.siteName})
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
	jsonResponse(w, 200, object{"url": address, "pool": map[bool]string{true: "public", false: "private"}[public], "raw": raw, "nodes": views, "protocol": protocol, "active": record.Active() && (len(views) > 0 || public), "user": record.User})
}
func (a *App) serveSubscription(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) != 43 {
		failure(w, 404, "订阅不存在")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
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
	if !record.Active() {
		failure(w, 403, "账号已停用、到期或额度已用尽")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	entries, err := a.subscriptionCatalog(record, public, r.URL.Query().Get("protocol"))
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
	if err = a.store.markSubscriptionAccess(&record, time.Now().Unix()); err != nil {
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
	records, err := a.coreRecords()
	nodes, e := a.store.nodes()
	if err == nil && e == nil {
		for _, record := range records {
			if !record.Active() || !record.HY2 {
				continue
			}
			for _, node := range nodes {
				if node.Protocol != "hy2" || !memberMayUseNode(record, node) {
					continue
				}
				if subtle.ConstantTimeCompare([]byte(input.Auth), []byte(hyNodePassword(record, node))) == 1 {
					jsonResponse(w, 200, object{"ok": true, "id": hyNodeIdentity(record, node)})
					return
				}
			}
		}
	}
	jsonResponse(w, 200, object{"ok": false})
}

func (a *App) password(w http.ResponseWriter, r *http.Request, actor Record) {
	var input struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Password) == 0 || len(input.Password) > 72 {
		failure(w, 400, "密码不能为空，且不能超过 72 个字节")
		return
	}
	if bcrypt.CompareHashAndPassword(actor.Password, []byte(input.Current)) != nil {
		failure(w, 400, "当前密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		failure(w, 500, "密码更新失败")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	actor, err = a.store.record(actor.ID)
	if err != nil {
		failure(w, 404, "用户不存在")
		return
	}
	actor.Password = hash
	if err = a.store.save(&actor); err != nil {
		failure(w, 500, "密码保存失败")
		return
	}
	_, _ = a.store.db.Exec("DELETE FROM sessions WHERE user_id=?", actor.ID)
	a.store.audit(actor.Username, "change_password", actor.Username)
	_ = os.Remove(filepath.Join(a.cfg.StateDir, "initial-owner.json"))
	jsonResponse(w, 200, object{"ok": true})
}

func (a *App) admin(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.URL.Path == "/api/plans" || r.URL.Path == "/api/node-groups" || r.URL.Path == "/api/entitlements/batch" || r.URL.Path == "/api/nodes/policy" {
		a.entitlementAPI(w, r, actor)
		return
	}
	if r.URL.Path == "/api/network-settings" || r.URL.Path == "/api/updates" || strings.HasPrefix(r.URL.Path, "/api/updates/") {
		a.updatesAPI(w, r, actor)
		return
	}
	if r.URL.Path == "/api/business-sites" || strings.HasPrefix(r.URL.Path, "/api/business-sites/") {
		a.businessAPI(w, r, actor)
		return
	}
	switch {
	case r.URL.Path == "/api/fleet" || strings.HasPrefix(r.URL.Path, "/api/fleet/"):
		a.fleetAPI(w, r, actor)
	case r.URL.Path == "/api/tasks" || strings.HasPrefix(r.URL.Path, "/api/tasks/"):
		a.tasksAPI(w, r, actor)
	case r.URL.Path == "/api/import-sources" || strings.HasPrefix(r.URL.Path, "/api/import-sources/"):
		a.importSources(w, r, actor)
	case r.URL.Path == "/api/settings" && (r.Method == "GET" || r.Method == "PUT"):
		a.panelSettings(w, r, actor)
	case r.URL.Path == "/api/runtime-settings" && (r.Method == "GET" || r.Method == "PUT"):
		a.runtimeSettings(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/public-pool":
		a.publicPoolInfo(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/public-pool/sources/fetch":
		a.queuePublicSources(w, r, actor)
	case r.URL.Path == "/api/public-pool/sources" || strings.HasPrefix(r.URL.Path, "/api/public-pool/sources/"):
		a.managePublicSources(w, r, actor)
	case r.Method == "PUT" && r.URL.Path == "/api/public-pool":
		a.savePublicSettings(w, r, actor)
	case r.Method == "POST" && r.URL.Path == "/api/public-pool/run":
		a.runPublicPool(w, r)
	case r.Method == "POST" && (strings.HasPrefix(r.URL.Path, "/api/ips/") || strings.HasPrefix(r.URL.Path, "/api/nodes/")) && strings.HasSuffix(r.URL.Path, "/quality"):
		a.qualityTest(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/ips/batch":
		a.batchResources(w, r, actor, "ips")
	case r.Method == "POST" && r.URL.Path == "/api/nodes/batch":
		a.batchResources(w, r, actor, "nodes")
	case r.Method == "POST" && r.URL.Path == "/api/ips/import":
		a.importSubscription(w, r, actor)
	case r.Method == "POST" && (strings.HasPrefix(r.URL.Path, "/api/ips/") || strings.HasPrefix(r.URL.Path, "/api/nodes/")) && strings.HasSuffix(r.URL.Path, "/speed"):
		a.speedTest(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/ips":
		a.listIPs(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/ips":
		a.saveIP(w, r, actor)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/ips/") && strings.HasSuffix(r.URL.Path, "/detect"):
		a.detectIP(w, r)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/ips/"):
		a.deleteIP(w, r, actor)
	case r.Method == "POST" && r.URL.Path == "/api/users":
		a.createUser(w, r, actor)
	case strings.HasPrefix(r.URL.Path, "/api/users/"):
		a.changeUser(w, r, actor)
	case r.Method == "POST" && r.URL.Path == "/api/nodes/probe":
		var n Node
		if !decode(w, r, &n) {
			return
		}
		a.mu.Lock()
		nodes, _ := a.store.nodes()
		for _, old := range nodes {
			if old.ID == n.ID && n.Password == "" && n.HasPassword {
				n.Password = old.Password
			}
		}
		if n.ExitID != "" {
			p, err := a.store.pool(n.ExitID)
			if err != nil || !p.Enabled {
				a.mu.Unlock()
				failure(w, 400, "请选择已启用的 IP 池资源")
				return
			}
			n = bindPool(n, p)
		}
		a.mu.Unlock()
		if n.Exit == "" && n.ExitID == "" {
			n.Exit = "direct"
		}
		result, err := probeNode(r.Context(), n)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		jsonResponse(w, 200, result)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/nodes/") && strings.HasSuffix(r.URL.Path, "/detect"):
		a.detectNode(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/nodes":
		a.saveNode(w, r, actor)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/nodes/"):
		a.deleteNode(w, r, actor)
	case r.Method == "POST" && r.URL.Path == "/api/apply":
		a.mu.Lock()
		a.status = "pending"
		a.mu.Unlock()
		jsonResponse(w, 202, object{"ok": true})
	case r.Method == "GET" && r.URL.Path == "/api/backup":
		a.backup(w, r, actor)
	default:
		failure(w, 404, "接口不存在")
	}
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,31}$`)

type userInput struct {
	PlanID   string `json:"plan_id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
	VLESS    bool   `json:"vless"`
	HY2      bool   `json:"hy2"`
	Expires  int64  `json:"expires"`
	Quota    int64  `json:"quota"`
}

func validUser(x userInput) bool {
	return usernamePattern.MatchString(x.Username) && x.Quota >= 0 && x.Quota <= 1<<60 && x.Expires >= 0 && x.Expires <= 253402300799
}
func (a *App) createUser(w http.ResponseWriter, r *http.Request, actor Record) {
	var input userInput
	if !decode(w, r, &input) {
		return
	}
	if !validUser(input) || len(input.Password) == 0 || len(input.Password) > 72 {
		failure(w, 400, "账号需为 3–32 位字母、数字或 ._-，密码需非空且不超过 72 个字节，额度与到期时间需有效")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		failure(w, 500, "创建密码失败")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var count int
	_ = a.store.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count >= 100 {
		failure(w, 400, "个人版最多 100 位用户")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	record := Record{User: User{Username: input.Username, Role: "user", Enabled: input.Enabled, VLESS: input.VLESS, HY2: input.HY2, Expires: input.Expires, Quota: input.Quota, Created: time.Now().Unix()}, Credentials: Credentials{HY2: randomToken(24), Token: randomToken(32), VLESS: map[string]string{}}, Password: hash}
	record.InitMeter("period-"+randomToken(12), record.Created)
	if input.PlanID != "" {
		p, e := a.store.plan(input.PlanID)
		if e != nil || p.Archived {
			failure(w, 400, "套餐不存在或已归档")
			return
		}
		assignPlan(&record, p, time.Now().Unix())
	}
	for _, n := range nodes {
		if n.Protocol == "vless" {
			record.Credentials.VLESS[n.ID] = uuid()
		}
	}
	if err = a.store.save(&record); err != nil {
		failure(w, 409, "账号已存在或保存失败")
		return
	}
	a.status = "pending"
	a.store.audit(actor.Username, "create_user", record.Username)
	jsonResponse(w, 201, record.User)
}
func (a *App) changeUser(w http.ResponseWriter, r *http.Request, actor Record) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		failure(w, 400, "用户编号无效")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.store.record(id)
	if err != nil {
		failure(w, 404, "用户不存在")
		return
	}
	if r.Method == "DELETE" && action == "" {
		if record.Role == "owner" {
			failure(w, 400, "不能删除管理员")
			return
		}
		if err = a.store.queueKick(id); err != nil {
			failure(w, 500, "撤销队列写入失败")
			return
		}
		_, err = a.store.db.Exec("DELETE FROM users WHERE id=?", id)
		if err != nil {
			failure(w, 500, "删除失败")
			return
		}
		a.status = "pending"
		a.store.audit(actor.Username, "delete_user", record.Username)
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	if r.Method == "PUT" && action == "" {
		var input userInput
		if !decode(w, r, &input) {
			return
		}
		if !validUser(input) {
			failure(w, 400, "账号、额度或到期时间无效")
			return
		}
		if record.Role == "owner" && !input.Enabled {
			failure(w, 400, "不能停用管理员登录")
			return
		}
		if record.Entitlement != nil && (input.Quota != record.Quota || input.VLESS != record.VLESS || input.HY2 != record.HY2 || input.Expires != record.Expires) {
			failure(w, 409, "套餐权益请通过套餐与权益操作变更")
			return
		}
		if input.Password != "" {
			if len(input.Password) > 72 {
				failure(w, 400, "密码不能为空，且不能超过 72 个字节")
				return
			}
			record.Password, err = bcrypt.GenerateFromPassword([]byte(input.Password), 12)
			if err != nil {
				failure(w, 500, "密码更新失败")
				return
			}
			_, _ = a.store.db.Exec("DELETE FROM sessions WHERE user_id=?", id)
		}
		record.Username = input.Username
		record.Enabled = input.Enabled
		record.VLESS = input.VLESS
		record.HY2 = input.HY2
		record.Expires = input.Expires
		record.Quota = input.Quota
		record.Credentials.HYGeneration++
	} else if r.Method == "POST" {
		switch action {
		case "rotate-public-sub":
			record.Credentials.PublicToken = randomToken(32)
		case "rotate-sub":
			record.Credentials.Token = randomToken(32)
		case "revoke":
			record.Credentials.PublicToken = randomToken(32)
			record.Credentials.Token = randomToken(32)
			record.Credentials.HY2 = randomToken(24)
			for n := range record.Credentials.VLESS {
				record.Credentials.VLESS[n] = uuid()
			}
			if err = a.store.queueKick(id); err != nil {
				failure(w, 500, "撤销队列写入失败")
				return
			}
		case "reset-traffic":
			failure(w, 409, "请使用套餐权益中的开始新周期，生命周期用量不能清零")
			return
		default:
			failure(w, 404, "操作不存在")
			return
		}
	} else {
		failure(w, 405, "方法不支持")
		return
	}
	if err = a.validateBusinessUserQuota(record); err != nil {
		failure(w, 409, err.Error())
		return
	}
	if err = a.store.save(&record); err != nil {
		failure(w, 409, "账号重复或保存失败")
		return
	}
	a.status = "pending"
	a.store.audit(actor.Username, "update_user "+action, record.Username)
	jsonResponse(w, 200, record.User)
}
func (a *App) saveNode(w http.ResponseWriter, r *http.Request, actor Record) {
	var n Node
	if !decode(w, r, &n) {
		return
	}
	// Protection is server-owned. Reject forbidden changes before DNS/TLS work,
	// then check again against the current row after acquiring the write lock.
	n.DefaultDirect = false
	a.mu.Lock()
	snapshot, snapshotErr := a.store.nodes()
	a.mu.Unlock()
	if snapshotErr != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	for _, previous := range snapshot {
		if previous.ID == n.ID {
			if err := validateDefaultDirectEdit(previous, n); err != nil {
				failure(w, 409, err.Error())
				return
			}
		}
	}
	if n.Protocol != "vless" && strings.TrimSpace(n.RealitySNI) != "" {
		failure(w, 400, "自定义 SNI 仅适用于 VLESS Reality 节点")
		return
	}
	var err error
	n.RealitySNI, err = normalizeRealitySNI(n.RealitySNI, a.cfg)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	// This address is always derived by the server, never accepted from JSON.
	n.RealityIP = ""
	if n.RealitySNI != "" {
		a.mu.Lock()
		snapshot, readErr := a.store.nodes()
		a.mu.Unlock()
		if readErr != nil {
			failure(w, 500, "读取节点失败")
			return
		}
		n.RealityIP, err = resolveRealityTarget(r.Context(), a.cfg, snapshot, n.RealitySNI, net.DefaultResolver.LookupIPAddr, checkRealityTLS)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		if !a.cfg.Dev {
			if !a.realityProbeMu.TryLock() {
				failure(w, 409, "已有 SNI 验证正在运行，请稍后重试")
				return
			}
			err = probeRealityCore(r.Context(), a.cfg, n.RealitySNI, n.RealityIP)
			a.realityProbeMu.Unlock()
			if err != nil {
				failure(w, 400, err.Error())
				return
			}
		}
	}
	n.ManagedBy, n.Quality = "", nil
	a.mu.Lock()
	defer a.mu.Unlock()
	currentActor, actorErr := a.store.record(actor.ID)
	if actorErr != nil || !currentActor.Enabled || currentActor.Role != "owner" {
		failure(w, 403, "管理员权限已变更，请重新登录")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	var old *Node
	for _, x := range nodes {
		if x.ID == n.ID {
			if x.ManagedBy == publicManager {
				failure(w, 409, "自动节点由公共代理池管理，请调整采集策略")
				return
			}
			v := x
			old = &v
			if err := validateDefaultDirectEdit(x, n); err != nil {
				failure(w, 409, err.Error())
				return
			}
			n.DefaultDirect = x.DefaultDirect
			if n.Protocol != x.Protocol {
				failure(w, 400, "节点协议不能变更")
				return
			}
			if n.Password == "" && n.HasPassword {
				n.Password = x.Password
			}
		}
	}
	if err = a.store.validateNodePolicy(&n, old); err != nil {
		failure(w, 400, err.Error())
		return
	}
	if old == nil {
		if n.ID != "" || (n.Protocol != "vless" && n.Protocol != "hy2") || len(nodes) >= 16 {
			failure(w, 400, "支持新增 VLESS / HY2，最多共 16 个节点")
			return
		}
		n.ID = n.Protocol + "-" + digest(randomToken(8))[:8]
	}
	if n.ExitID == "" {
		n.Upstream, n.UpstreamType, n.BridgePort, n.BridgePassword = nil, "", 0, ""
	}
	if n.ExitID != "" {
		if err = a.checkExclusiveBusinessExit(n.ExitID); err != nil {
			failure(w, 409, err.Error())
			return
		}
		p, e := a.store.pool(n.ExitID)
		if e != nil || !p.Enabled {
			failure(w, 400, "请选择已启用的 IP 池资源")
			return
		}
		if p.PoolGroup == "public" {
			failure(w, 400, "公共出口请使用自动生成的节点")
			return
		}
		n = bindPool(n, p)
	}
	if n.Exit == "" && n.ExitID == "" {
		n.Exit = "direct"
	}
	if n.Exit == "subscription" && n.ExitID == "" {
		failure(w, 400, "机场出口必须从 IP 池中选择")
		return
	}
	if n.Exit == "direct" {
		n.Upstream, n.UpstreamType, n.BridgePort, n.BridgePassword = nil, "", 0, ""
		n.Host = ""
		n.Port = 0
		n.Username = ""
		n.Password = ""
	}
	if err = validateNode(n); err != nil {
		failure(w, 400, err.Error())
		return
	}
	if n.DNS == nil {
		if old != nil {
			n.DNS = old.DNS
		} else {
			n.DNS = defaultNodeDNS()
		}
	}
	if err = validateNodeDNS(n.DNS); err != nil {
		failure(w, 400, err.Error())
		return
	}
	n.Speed = nil
	if old != nil && sameExit(*old, n) {
		n.Speed = old.Speed
		n.Quality = old.Quality
	}
	// Detection metadata is server-owned, including in local development mode.
	n.Name = "待检测 · " + strings.ToUpper(n.Protocol)
	n.ProbeIP, n.Country, n.CountryCode, n.ProbeError = "", "", "", ""
	n.ProbedAt, n.CheckedAt = 0, 0
	if old != nil && sameExit(*old, n) {
		n.Name, n.ProbeIP, n.Country, n.CountryCode = old.Name, old.ProbeIP, old.Country, old.CountryCode
		n.ProbedAt, n.CheckedAt, n.ProbeError = old.ProbedAt, old.CheckedAt, old.ProbeError
	}
	if n.ExitID != "" {
		p, _ := a.store.pool(n.ExitID)
		n = bindPool(n, p)
	}
	if !a.cfg.Dev {
		result, e := probeNode(r.Context(), n)
		if e != nil {
			failure(w, 400, e.Error())
			return
		}
		applyEgress(&n, result, nil)
	}
	if n.Exit == "direct" && n.Quality == nil {
		if q := a.reusableQuality(n); q != nil {
			n.Quality = q
			applyQualityIdentity(&n, *q)
		}
	}
	// Direct is a built-in choice. Only proxy exits are adopted into the pool.
	shared, savePools := []Node{n}, []IPResource{}
	if n.Exit != "direct" {
		pools, e := a.store.pools()
		if e != nil {
			failure(w, 500, "读取 IP 池失败")
			return
		}
		var selected *IPResource
		for _, p := range pools {
			if n.ExitID == p.ID || n.ExitID == "" && sameExit(n, p.Node) {
				copy := p
				selected = &copy
				break
			}
		}
		if selected == nil {
			if len(pools) >= 256 {
				failure(w, 400, "IP 池最多 256 项")
				return
			}
			p := resourceFromNode(n)
			selected = &p
		}
		if err = a.checkExclusiveBusinessExit(selected.ID); err != nil {
			failure(w, 409, err.Error())
			return
		}
		if !selected.Enabled {
			failure(w, 400, "该 IP 已停用，请在 IP 管理中启用")
			return
		}
		selected.Name, selected.ProbeIP, selected.Country, selected.CountryCode = n.Name, n.ProbeIP, n.Country, n.CountryCode
		if selected.ProbeIP == "" {
			selected.Name = "待检测出口"
		}
		selected.ProbedAt, selected.CheckedAt, selected.ProbeError = n.ProbedAt, n.CheckedAt, n.ProbeError
		if !a.cfg.Dev {
			selected.Reachable = true
		}
		n = bindPool(n, *selected)
		shared = poolBindings(nodes, selected.ID)
		for i := range shared {
			shared[i] = bindPool(shared[i], *selected)
		}
		shared = append(shared, n)
		savePools = append(savePools, *selected)
	}
	if !a.cfg.Dev {
		if err = a.collect(); err != nil {
			failure(w, 502, "流量同步未完成，节点设置未改变")
			return
		}
	}
	// A hostname has one Nginx destination and one Reality handshake target.
	// Keep its pinned address consistent across nodes in the same transaction.
	realityBefore := []Node{}
	if n.RealitySNI != "" {
		for _, other := range nodes {
			if other.ID == n.ID || other.RealitySNI != n.RealitySNI || other.RealityIP == n.RealityIP {
				continue
			}
			realityBefore = append(realityBefore, other)
			found := false
			for i := range shared {
				if shared[i].ID == other.ID {
					shared[i].RealityIP = n.RealityIP
					found = true
				}
			}
			if !found {
				other.RealityIP = n.RealityIP
				shared = append(shared, other)
			}
		}
	}
	if err = a.store.savePoolNodes(savePools, shared); err != nil {
		failure(w, 500, "保存节点失败")
		return
	}
	if old == nil && n.Protocol == "vless" {
		records, e := a.store.records()
		if e != nil {
			failure(w, 500, "读取用户失败")
			return
		}
		for _, record := range records {
			record.Credentials.VLESS[n.ID] = uuid()
			if err = a.store.save(&record); err != nil {
				failure(w, 500, "分配凭据失败")
				return
			}
		}
	}
	a.status = "pending"
	if err = a.reconcile(); err != nil {
		for _, previous := range realityBefore {
			_ = a.store.saveNode(previous)
		}
		if old != nil {
			_ = a.store.saveNode(*old)
		} else {
			_, _ = a.store.db.Exec("DELETE FROM nodes WHERE id=?", n.ID)
		}
		_ = a.store.setMeta("x_nodes", "")
		_ = a.store.setMeta("hy_nodes", "")
		rollback := a.reconcile()
		a.syncError = err.Error()
		a.status = "error"
		if rollback != nil {
			a.syncError += "; rollback pending"
		}
		a.store.audit(actor.Username, "node_apply_failed", n.Name)
		failure(w, 502, "核心应用失败，已恢复原节点设置；请检查运行状态")
		return
	}
	a.status = "applied"
	a.syncError = ""
	a.appliedAt = time.Now().Unix()
	a.store.audit(actor.Username, "save_node", n.Name)
	jsonResponse(w, 200, n.public())
}
func (a *App) deleteNode(w http.ResponseWriter, r *http.Request, actor Record) {
	id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	a.mu.Lock()
	defer a.mu.Unlock()
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取失败")
		return
	}
	var old *Node
	counts := map[string]int{}
	for _, n := range nodes {
		if n.ManagedBy != publicManager {
			counts[n.Protocol]++
		}
		if n.ID == id {
			v := n
			old = &v
		}
	}
	if old == nil {
		failure(w, 404, "节点不存在")
		return
	}
	if old.ManagedBy == publicManager {
		failure(w, 409, "自动节点由公共代理池管理，请调整采集策略")
		return
	}
	if old.DefaultDirect {
		failure(w, 409, defaultDirectMessage)
		return
	}
	if counts[old.Protocol] <= 1 {
		failure(w, 400, "HY2 和 VLESS 均需至少保留一个节点")
		return
	}
	if !a.cfg.Dev {
		if err = a.collect(); err != nil {
			failure(w, 502, "流量同步暂未完成，请稍后重试删除")
			return
		}
	}
	if _, err = a.store.db.Exec("DELETE FROM nodes WHERE id=?", id); err != nil {
		failure(w, 500, "删除失败")
		return
	}
	if err = a.reconcile(); err != nil {
		_ = a.store.saveNode(*old)
		_ = a.reconcile()
		failure(w, 502, "删除应用失败，已恢复节点")
		return
	}
	a.store.audit(actor.Username, "delete_node", old.Name)
	jsonResponse(w, 200, object{"ok": true})
}
