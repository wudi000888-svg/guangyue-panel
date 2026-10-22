package controlplane

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	mux.HandleFunc("GET /api/register/challenge", a.registrationChallenge)
	mux.HandleFunc("POST /api/register/verify", a.verifyRegistrationChallenge)
	mux.HandleFunc("POST /api/register", a.register)
	mux.HandleFunc("GET /api/email/status", a.emailPublicAPI)
	mux.HandleFunc("POST /api/email/registration", a.emailPublicAPI)
	mux.HandleFunc("POST /api/email/verify", a.emailPublicAPI)
	mux.HandleFunc("POST /api/email/reset/request", a.emailPublicAPI)
	mux.HandleFunc("POST /api/email/reset/confirm", a.emailPublicAPI)
	mux.HandleFunc("POST /api/business/enroll", a.businessEnroll)
	mux.HandleFunc("POST /api/business/connect", a.businessEnroll)
	mux.HandleFunc("POST /api/business/sync", a.businessSync)
	mux.HandleFunc("POST /api/fleet-gateway", a.fleetGateway)
	// Provider callbacks are authenticated by the provider signature, not by
	// the browser session/origin headers used by the panel API.
	mux.HandleFunc("POST /api/payments/webhook/{provider}", a.paymentWebhook)
	mux.HandleFunc("GET /api/payments/webhook/{provider}", a.paymentWebhook)
	mux.HandleFunc("GET /api/site", a.siteInfo)
	mux.HandleFunc("GET /api/access-status", a.accessStatus)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, object{"ok": true, "version": version, "edition": a.cfg.edition(), "product": a.cfg.productName(), "site_id": a.cfg.siteID(), "role": a.cfg.deploymentRole(), "maintenance": a.maintenancePending(), "background_frozen": a.maintenanceFrozen()})
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
		// A managed child keeps its normal local panel.  The controller supplies
		// policy when reachable, while the child remains usable during an outage.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/sub/") || strings.HasPrefix(r.URL.Path, "/public-sub/") {
			w.Header().Set("Cache-Control", "no-store, private")
		}
		webhook := strings.HasPrefix(r.URL.Path, "/api/payments/webhook/")
		if !a.enforceSiteAccess(w, r) {
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && !webhook {
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
		} else if webhook && a.controlReady() != nil {
			failure(w, 503, "站点控制器暂不可用")
			return
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
	ip := requestIP(r)
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
	if bcrypt.CompareHashAndPassword(hash, []byte(input.Password)) != nil || err != nil || e != nil || !record.Enabled || record.Mount != nil {
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
		if strings.HasPrefix(r.URL.Path, "/api/commerce/") || strings.HasPrefix(r.URL.Path, "/api/support/") || strings.HasPrefix(r.URL.Path, "/api/email/") || strings.HasPrefix(r.URL.Path, "/api/account/email") {
			failure(w, 403, "账户服务必须在主控访问")
			return
		}
		a.relaySiteAPI(w, r, actor, peer)
		return
	}
	a.dispatchAuthenticated(w, r, actor)
}
func (a *App) dispatchAuthenticated(w http.ResponseWriter, r *http.Request, actor Record) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/email/"):
		a.emailAdminAPI(w, r, actor)
	case r.URL.Path == "/api/account/email":
		a.accountEmailAPI(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/access-check":
		a.accessCheck(w, r, actor)
	case (r.Method == "GET" || r.Method == "HEAD") && strings.HasPrefix(r.URL.Path, "/api/clients/download/"):
		a.clientDownload(w, r, actor)
	case strings.HasPrefix(r.URL.Path, "/api/support/"):
		a.supportAPI(w, r, actor)
	case strings.HasPrefix(r.URL.Path, "/api/commerce/"):
		a.commerceAPI(w, r, actor)
	case r.URL.Path == "/api/messages" || strings.HasPrefix(r.URL.Path, "/api/messages/"):
		a.messages(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/operations":
		a.operations(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/state":
		a.state(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/dashboard":
		a.dashboard(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/monitor":
		a.liveAPI(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/subscription":
		a.subscriptionInfo(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/usage":
		a.userUsage(w, r, actor)
	case r.Method == "GET" && r.URL.Path == "/api/node-quality":
		a.memberNodeQuality(w, r, actor)
	case (r.Method == "GET" || r.Method == "PUT") && r.URL.Path == "/api/node-square":
		a.nodeSquare(w, r, actor)
	case r.URL.Path == "/api/site-status" || r.URL.Path == "/api/site-control":
		a.siteControlAPI(w, r, actor)
	case r.Method == "POST" && r.URL.Path == "/api/adopt":
		a.adoptBusiness(w, r, actor)
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
	system := object{"version": version, "edition": a.cfg.edition(), "role": a.cfg.deploymentRole(), "site_id": a.cfg.siteID(), "database": a.store.db.Driver(), "cache": a.cache.Mode(), "cache_degraded": a.cache.Degraded(), "uptime": int64(time.Since(a.started).Seconds()), "heap": mem.HeapAlloc, "status": a.status, "error": a.syncError, "traffic_error": a.trafficError, "applied_at": a.appliedAt, "panel_host": a.cfg.PublicURL, "vless_host": a.cfg.VLESSHost, "hy2_host": a.cfg.HY2Host, "cert_expires": certExpiry(a.cfg.Cert)}
	system["payment_module_enabled"], _ = a.paymentModuleEnabled()
	if actor.Role != "owner" {
		system["error"] = ""
		system["traffic_error"] = ""
	}
	if actor.Role == "owner" {
		system["node_save_receipts"] = true
		system["reality_sni"] = a.cfg.RealitySNI
		system["hy2_old_connections"] = a.oldHYConnections
		system["services"] = []object{{"name": "Xray", "active": a.cfg.Dev || corePID("guangyue-xray.service") > 0}, {"name": "Hysteria2", "active": a.cfg.Dev || corePID("guangyue-hy2.service") > 0}, a.gatewayServiceStatus()}
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
	if r.URL.Path == "/api/plans" || strings.HasPrefix(r.URL.Path, "/api/plans/") || r.URL.Path == "/api/node-groups" || strings.HasPrefix(r.URL.Path, "/api/node-groups/") || r.URL.Path == "/api/entitlements/batch" || r.URL.Path == "/api/nodes/policy" {
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
	case r.URL.Path == "/api/settings/navigation" && (r.Method == "GET" || r.Method == "PUT"):
		a.panelNavigation(w, r, actor)
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
