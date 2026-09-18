package controlplane

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

// The shareable envelope is a credential, not a signed identity assertion.
// Always verify its site ID against the authenticated gateway before storing it.
type SiteToken struct {
	InstanceID string `json:"instance_id,omitempty"`
	Version    int    `json:"version"`
	URL        string `json:"url"`
	SiteID     string `json:"site_id"`
	Token      string `json:"token"`
	Name       string `json:"name"`
}
type SiteConnection struct {
	InstanceID string      `json:"instance_id,omitempty"`
	URL        string      `json:"url"`
	SiteID     string      `json:"site_id"`
	Token      string      `json:"token,omitempty"`
	Scope      string      `json:"scope"`
	LegacyID   string      `json:"legacy_id,omitempty"`
	Status     *SiteStatus `json:"status,omitempty"`
}
type SiteStatus struct {
	ServiceError string    `json:"service_error,omitempty"`
	Version      string    `json:"version"`
	Edition      string    `json:"edition"`
	Role         string    `json:"role"`
	Paused       bool      `json:"paused"`
	Pending      bool      `json:"pending"`
	Control      bool      `json:"control"`
	Users        int       `json:"users"`
	Nodes        int       `json:"nodes"`
	OnlineUsers  *int      `json:"online_users"`
	Upload       int64     `json:"upload"`
	Download     int64     `json:"download"`
	SampledAt    int64     `json:"sampled_at"`
	Live         liveValue `json:"live"`
}

func encodeSiteToken(v SiteToken) string {
	b, _ := json.Marshal(v)
	return "gys_" + base64.RawURLEncoding.EncodeToString(b)
}
func parseSiteToken(token, address string, dev bool) (SiteToken, error) {
	v := SiteToken{Version: 1, Token: strings.TrimSpace(token), URL: strings.TrimSpace(address)}
	if len(v.Token) > 8192 {
		return v, errors.New("子站令牌过长")
	}
	if strings.HasPrefix(v.Token, "gys_") {
		b, err := base64.RawURLEncoding.DecodeString(v.Token[4:])
		if err != nil {
			return v, errors.New("子站令牌格式无效")
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&v) != nil || d.Decode(&struct{}{}) != io.EOF || v.Version != 1 || v.SiteID == "" {
			return v, errors.New("子站令牌格式无效")
		}
	}
	if !validBusinessBootstrap(strings.Replace(v.Token, "gyp_", "gye_", 1)) || !strings.HasPrefix(v.Token, "gyp_") {
		return v, errors.New("请使用子站生成的管理令牌")
	}
	if httpapi.ValidateEndpoint(v.URL, dev) != nil {
		return v, errors.New("旧格式令牌需要填写子站 HTTPS 地址")
	}
	u, _ := url.Parse(v.URL)
	u.Path = ""
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	v.URL = u.String()
	return v, nil
}
func directSiteID(address string) string {
	return "site_" + digest(strings.TrimRight(address, "/"))[:24]
}

// A transactional directory-only migration works for offline children, retains
// read-only scopes, and never writes a role marker on a remote server.
func (a *App) migrateSiteDirectory(actor Record) error {
	a.fleetMigrationMu.Lock()
	defer a.fleetMigrationMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	peers, err := a.store.fleetPeersWithToken()
	if err != nil {
		return err
	}
	for _, p := range peers {
		v := BusinessSite{ID: directSiteID(p.URL), Name: p.Name, Enabled: true, OwnerID: actor.ID, Created: p.Created, Revision: randomToken(12), Connection: &SiteConnection{URL: p.URL, SiteID: p.SiteID, Token: p.Token, Scope: p.Scope, LegacyID: p.ID}}
		v.defaults()
		b, e := a.store.vault.seal(v)
		if e != nil {
			return e
		}
		tx, e := a.store.db.Begin()
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO business_sites(id,doc) VALUES(?,?) ON CONFLICT(id) DO NOTHING", v.ID, b); e == nil {
			_, e = tx.Exec("DELETE FROM fleet_peers WHERE id=?", p.ID)
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) remoteSubsite(id string) (FleetPeer, error) {
	v, err := a.store.businessSite(id)
	if err == nil && v.Connection != nil {
		if v.Removed || !v.Enabled {
			return FleetPeer{}, errors.New("sub-site unavailable")
		}
		c := v.Connection
		return FleetPeer{ID: v.ID, Name: v.Name, URL: c.URL, SiteID: c.SiteID, Token: c.Token, Scope: c.Scope, InstanceID: c.InstanceID}, nil
	}
	// Retain already-open old site selections across directory migration.
	sites, err := a.store.businessSites()
	if err != nil {
		return FleetPeer{}, err
	}
	for _, v := range sites {
		if v.Connection != nil && v.Connection.LegacyID == id && !v.Removed && v.Enabled {
			c := v.Connection
			return FleetPeer{ID: v.ID, Name: v.Name, URL: c.URL, SiteID: c.SiteID, Token: c.Token, Scope: c.Scope, InstanceID: c.InstanceID}, nil
		}
	}
	return a.store.fleetPeer(id)
}
func (a *App) callSubsite(ctx context.Context, c SiteConnection, in httpapi.GatewayRequest) (httpapi.GatewayResponse, error) {
	if httpapi.ValidateEndpoint(c.URL, a.cfg.Dev) != nil {
		return httpapi.GatewayResponse{}, errors.New("子站地址无效")
	}
	transport := httpapi.NewGatewayTransport(a.cfg.Dev)
	defer transport.CloseIdleConnections()
	out, err := httpapi.CallGateway(ctx, transport, c.URL, c.Token, in)
	if err != nil {
		return out, errors.New("子站连接失败，请检查地址、证书和令牌")
	}
	if c.SiteID != "" && out.SiteID != c.SiteID || c.InstanceID != "" && out.InstanceID != c.InstanceID {
		return out, errors.New("子站身份发生变化，请重新导入令牌")
	}
	if out.Status < 200 || out.Status >= 300 {
		return out, errors.New("子站拒绝操作，请检查令牌权限或升级子站")
	}
	return out, nil
}
func (a *App) importSubsite(w http.ResponseWriter, r *http.Request, actor Record) {
	var in struct {
		Token string `json:"token"`
		URL   string `json:"url"`
		Name  string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := parseSiteToken(in.Token, in.URL, a.cfg.Dev)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	if strings.TrimRight(c.URL, "/") == strings.TrimRight(a.cfg.PublicURL, "/") {
		failure(w, 400, "不能把主站自身导入为子站")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(in.Name) > 64 {
		failure(w, 400, "站点名称过长")
		return
	}
	if err := a.migrateSiteDirectory(actor); err != nil {
		failure(w, 500, "迁移子站目录失败")
		return
	}
	a.fleetMigrationMu.Lock()
	defer a.fleetMigrationMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	connection := SiteConnection{URL: c.URL, SiteID: c.SiteID, Token: c.Token, Scope: "manage", InstanceID: c.InstanceID}
	out, err := a.callSubsite(ctx, connection, httpapi.GatewayRequest{Method: "GET", Path: "/api/operations"})
	if err != nil {
		failure(w, 502, err.Error())
		return
	}
	if out.Scope != "manage" {
		failure(w, 403, "请使用子站的管理令牌，只读令牌不能调度")
		return
	}
	localInstance, identityErr := a.store.siteInstanceID()
	if identityErr != nil {
		failure(w, 500, "读取本站身份失败")
		return
	}
	if out.InstanceID != "" && out.InstanceID == localInstance {
		failure(w, 400, "不能把主站自身导入为子站")
		return
	}
	if out.SiteID == "" {
		failure(w, 502, "子站未返回有效身份")
		return
	}
	connection.SiteID = out.SiteID
	connection.InstanceID = out.InstanceID
	var detail struct {
		System struct {
			Role    string `json:"role"`
			Version string `json:"version"`
			Edition string `json:"edition"`
		} `json:"system"`
		Site SiteSettings `json:"site"`
	}
	if json.Unmarshal(out.Body, &detail) != nil {
		failure(w, 502, "子站返回无效状态")
		return
	}
	if in.Name == "" {
		in.Name = detail.Site.PanelName
	}
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 {
		in.Name = out.SiteID
	}
	id := directSiteID(c.URL)
	a.mu.Lock()
	sites, err := a.store.businessSites()
	a.mu.Unlock()
	if err != nil {
		failure(w, 500, "读取子站目录失败")
		return
	}
	// A changed address or newly issued token still refers to the same site.
	for _, v := range sites {
		if v.Connection != nil && connection.InstanceID != "" && v.Connection.InstanceID == connection.InstanceID {
			id = v.ID
			break
		}
	}
	count := 0
	for _, v := range sites {
		if !v.Removed && v.ID != id {
			count++
		}
	}
	if count >= businessMaxSites {
		failure(w, 409, "最多接入 64 个子站")
		return
	}
	// Explicit token import, unlike merely opening the directory, can transition
	// a former pull agent to local management. The child retains all stored data.
	if detail.System.Role == "business" {
		_, err = a.callSubsite(ctx, connection, httpapi.GatewayRequest{Method: "POST", Path: "/api/site-control", Body: json.RawMessage(`{"action":"token-management"}`)})
		if err != nil {
			failure(w, 409, "请先将子站升级到 0.24.0，再导入令牌")
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v, err := a.store.businessSite(id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failure(w, 500, "读取子站目录失败")
		return
	}
	if err != nil {
		v = BusinessSite{ID: id, OwnerID: actor.ID, Created: time.Now().Unix()}
	}
	v.Name, v.Enabled, v.Removed, v.Revision = in.Name, true, false, randomToken(12)
	v.Connection = &connection
	v.LastSeen = time.Now().Unix()
	v.Error = ""
	v.Connection.Status = &SiteStatus{Version: detail.System.Version, Edition: detail.System.Edition, Role: detail.System.Role}
	v.defaults()
	b, err := a.store.vault.seal(v)
	if err == nil {
		_, err = a.store.db.Exec("INSERT INTO business_sites(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", id, b)
	}
	if err != nil {
		failure(w, 500, "保存子站连接失败")
		return
	}
	a.store.audit(actor.Username, "import-subsite", id)
	jsonResponse(w, 201, v.public())
}
func (a *App) directSiteAPI(w http.ResponseWriter, r *http.Request, actor Record, path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		return false
	}
	a.mu.Lock()
	v, err := a.store.businessSite(parts[0])
	a.mu.Unlock()
	if err != nil || v.Connection == nil {
		return false
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, err = a.store.db.Exec("DELETE FROM business_sites WHERE id=?", v.ID); err != nil {
			failure(w, 500, "移除子站失败")
			return true
		}
		a.store.audit(actor.Username, "remove-subsite", v.ID)
		jsonResponse(w, 200, object{"ok": true})
		return true
	}
	if len(parts) != 2 || r.Method != "POST" || (parts[1] != "probe" && parts[1] != "control") {
		failure(w, 400, "令牌子站请通过管理入口调度")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	request := httpapi.GatewayRequest{Method: "GET", Path: "/api/site-status"}
	if parts[1] == "control" {
		var in struct {
			Paused *bool `json:"paused"`
		}
		if !decode(w, r, &in) {
			return true
		}
		if in.Paused == nil {
			failure(w, 400, "请选择启用或暂停服务")
			return true
		}
		if v.Connection.Scope != "manage" {
			failure(w, 403, "只读令牌不能调度")
			return true
		}
		request.Method, request.Path = "PUT", "/api/site-control"
		request.Body, _ = json.Marshal(in)
	}
	out, err := a.callSubsite(ctx, *v.Connection, request)
	status := SiteStatus{}
	// Old gateways have no site-status route; preserve manageable existing sites.
	if err != nil && parts[1] == "probe" {
		out, err = a.callSubsite(ctx, *v.Connection, httpapi.GatewayRequest{Method: "GET", Path: "/api/operations"})
		if err == nil {
			var old struct {
				System SiteStatus `json:"system"`
			}
			err = json.Unmarshal(out.Body, &old)
			status = old.System
		}
	} else if err == nil {
		err = json.Unmarshal(out.Body, &status)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, e := a.store.businessSite(v.ID)
	if e != nil || current.Connection == nil || current.Connection.Token != v.Connection.Token {
		failure(w, 409, "子站连接已更改，请刷新")
		return true
	}
	if err != nil {
		current.Error = "子站连接或操作失败，请检查网络、版本和令牌"
	} else {
		current.Error = ""
		current.LastSeen = time.Now().Unix()
		current.Connection.Status = &status
	}
	if e = a.store.saveBusinessSite(current); e != nil {
		failure(w, 500, "保存子站状态失败")
		return true
	}
	if err != nil {
		failure(w, 502, current.Error)
		return true
	}
	if parts[1] == "control" {
		a.store.audit(actor.Username, "control-subsite", v.ID)
	}
	jsonResponse(w, 200, current.public())
	return true
}

func (a *App) siteControlAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.Role != "owner" {
		failure(w, 403, "需要管理员权限")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.URL.Path == "/api/site-control" && r.Method == "POST" {
		var in struct {
			Action string `json:"action"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Action != "token-management" {
			failure(w, 400, "未知站点操作")
			return
		}
		if a.cfg.businessAgent() {
			if err := a.store.materializeLocalPolicies(); err != nil {
				failure(w, 500, "保存本地权限失败")
				return
			}
			if err := writeJSON(filepath.Join(a.cfg.StateDir, "site-local.json"), object{"site_id": a.cfg.siteID()}); err != nil {
				failure(w, 500, "保存连接模式失败")
				return
			}
			a.store.audit(actor.Username, "token-management", a.cfg.siteID())
			if a.restart != nil {
				go func() { time.Sleep(time.Second); a.restart() }()
			}
		}
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	if r.URL.Path == "/api/site-control" && r.Method == "PUT" {
		var in struct {
			Paused *bool `json:"paused"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Paused == nil {
			failure(w, 400, "请选择启用或暂停服务")
			return
		}
		value := "false"
		if *in.Paused {
			value = "true"
		}
		tx, err := a.store.db.Begin()
		if err == nil {
			defer tx.Rollback()
			_, err = tx.Exec("INSERT INTO meta(key,value) VALUES('site_paused',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value)
			if err == nil && *in.Paused {
				_, err = tx.Exec("INSERT INTO revocations(user_id) SELECT id FROM users WHERE 1=1 ON CONFLICT(user_id) DO NOTHING")
			}
			if err == nil {
				err = tx.Commit()
			}
		}
		if err != nil {
			failure(w, 500, "保存服务状态失败")
			return
		}
		a.status = "pending"
		a.lastCoreHash = ""
		a.store.audit(actor.Username, "site-service", value)
	} else if r.URL.Path != "/api/site-status" || r.Method != "GET" {
		failure(w, 404, "接口不存在")
		return
	}
	users, err := a.store.records()
	if err != nil {
		failure(w, 500, "读取用户失败")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	status := SiteStatus{Version: version, Edition: a.cfg.edition(), Role: a.cfg.deploymentRole(), Paused: a.store.meta("site_paused") == "true", Control: true, Pending: a.status == "pending", Users: len(users), Nodes: len(nodes)}
	if a.status == "error" {
		status.ServiceError = "服务配置应用失败，请查看子站运维状态"
	}
	for _, u := range users {
		status.Upload += u.Upload
		status.Download += u.Download
	}
	a.monitor.RLock()
	if n := len(a.monitor.frames); n > 0 {
		f := a.monitor.frames[n-1]
		status.SampledAt = f.At
		if time.Now().UnixMilli()-f.At <= 15000 {
			status.Live = f.Total
			if f.Connections[0] && f.Connections[1] {
				n := 0
				for _, v := range f.values {
					if v.VLESS != nil && *v.VLESS > 0 || v.HY2 != nil && *v.HY2 > 0 {
						n++
					}
				}
				status.OnlineUsers = &n
			}
		}
	}
	a.monitor.RUnlock()
	jsonResponse(w, 200, status)
}

// Installation identity is independent of the database schema name, which may
// legitimately be "default" on multiple standalone servers.
func (s *Store) siteInstanceID() (string, error) {
	id, err := s.readMeta("site_instance_id")
	if err != nil || id != "" {
		return id, err
	}
	if _, err = s.db.Exec("INSERT INTO meta(key,value) VALUES('site_instance_id',?) ON CONFLICT(key) DO NOTHING", uuid()); err != nil {
		return "", err
	}
	return s.readMeta("site_instance_id")
}

// Retain the last compiled authorization when a pull-managed child becomes
// independently managed. Credentials, quotas, counters and node IDs stay intact.
func (s *Store) materializeLocalPolicies() error {
	users, err := s.records()
	if err != nil {
		return err
	}
	nodes, err := s.nodes()
	if err != nil {
		return err
	}
	groups, err := s.nodeGroups()
	if err != nil {
		return err
	}
	known := map[string]NodeGroup{}
	for _, g := range groups {
		known[g.ID] = g
	}
	for _, n := range nodes {
		for _, id := range normalizeNodePolicy(n).GroupIDs {
			if _, ok := known[id]; !ok {
				scope := "private"
				if n.ManagedBy == publicManager {
					scope = "public"
				}
				known[id] = NodeGroup{ID: id, Name: id, Scope: scope, Enabled: true, Revision: randomToken(12)}
			}
		}
	}
	for _, u := range users {
		if u.CompiledGroups == nil {
			continue
		}
		for _, id := range *u.CompiledGroups {
			g, ok := known[id]
			if !ok {
				g = NodeGroup{ID: id, Name: id, Scope: "private", Revision: randomToken(12)}
			}
			g.Enabled = true
			known[id] = g
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, g := range known {
		b, e := json.Marshal(g)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO node_groups(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", g.ID, b); e != nil {
			return e
		}
	}
	for _, u := range users {
		if u.CompiledGroups == nil {
			continue
		}
		e := domain.Entitlement{Name: "迁移权益", Cycle: "none", Timezone: "Asia/Shanghai", AssignedAt: time.Now().Unix(), Revision: randomToken(12)}
		if u.Entitlement != nil {
			e = *u.Entitlement
		}
		e.PlanID = ""
		e.GroupIDs = append([]string{}, (*u.CompiledGroups)...)
		u.Entitlement = &e
		b, marshalErr := json.Marshal(u.User)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, u.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
