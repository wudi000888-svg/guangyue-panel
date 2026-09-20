package controlplane

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

type FleetPeer struct {
	InstanceID string `json:"instance_id,omitempty"`
	ID         string `json:"id"`
	Scope      string `json:"scope"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	SiteID     string `json:"site_id"`
	Token      string `json:"token,omitempty"`
	Created    int64  `json:"created"`
}

func (s *Store) fleetPeer(id string) (FleetPeer, error) {
	var b []byte
	var p FleetPeer
	err := s.db.QueryRow("SELECT doc FROM fleet_peers WHERE id=?", id).Scan(&b)
	if err == nil {
		err = s.vault.open(b, &p)
	}
	return p, err
}
func (s *Store) fleetPeers() ([]FleetPeer, error) {
	peers, err := s.fleetPeersWithToken()
	if err != nil {
		return nil, err
	}
	for i := range peers {
		// The management API must never return the encrypted credential.
		peers[i].Token = ""
	}
	return peers, nil
}

func (s *Store) fleetPeersWithToken() ([]FleetPeer, error) {
	rows, err := s.db.Query("SELECT doc FROM fleet_peers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []FleetPeer{}
	for rows.Next() {
		var b []byte
		var p FleetPeer
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = s.vault.open(b, &p); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (a *App) fleetGateway(w http.ResponseWriter, r *http.Request) {
	if a.gatewaySlots != nil {
		select {
		case a.gatewaySlots <- struct{}{}:
			defer func() { <-a.gatewaySlots }()
		default:
			failure(w, 429, "站点请求繁忙")
			return
		}
	}

	// Pairing tokens are served by both editions.  Lite cannot act as a fleet
	// controller, but a Pro master may use its token to manage this site.
	if r.Method != "POST" {
		failure(w, 404, "接口不存在")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(token, "gyp_") || len(token) != 47 {
		failure(w, 401, "站点凭据无效")
		return
	}
	var actorID, expires int64
	var scope, id string
	err := a.store.db.QueryRow("SELECT id,actor_id,scope,expires FROM fleet_tokens WHERE token_hash=?", digest(token)).Scan(&id, &actorID, &scope, &expires)
	if err != nil || expires != 0 && expires <= time.Now().Unix() {
		failure(w, 401, "站点凭据已失效")
		return
	}
	actor, err := a.store.record(actorID)
	if err != nil || !actor.Enabled || actor.Role != "owner" {
		failure(w, 403, "站点凭据无管理权限")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpapi.MaxGatewayBytes)
	var in httpapi.GatewayRequest
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !httpapi.AllowedGateway(in.Method, in.Path, scope) {
		failure(w, 403, "站点接口不在授权范围内")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), in.Method, in.Path, bytes.NewReader(in.Body))
	if err != nil {
		failure(w, 400, "站点请求无效")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Requested-With", "guangyue")
	buffer := &httpapi.BufferResponse{}
	if request.URL.Path == "/api/node-pool" || request.URL.Path == "/api/node-pool/egress" || request.URL.Path == "/api/node-mounts/sync" {
		a.mountGateway(buffer, request, actor, id)
	} else {
		a.dispatchAuthenticated(buffer, request, actor)
	}
	if buffer.Overflow || !json.Valid(buffer.Body.Bytes()) {
		failure(w, 502, "站点响应无效或超出限制")
		return
	}
	a.store.runtimeMu.RLock()
	if a.store.logsEnabledLocked() {
		_, _ = a.store.db.Exec("UPDATE fleet_tokens SET last_used=? WHERE id=?", time.Now().Unix(), id)
	}
	a.store.runtimeMu.RUnlock()
	instanceID, identityErr := a.store.siteInstanceID()
	if identityErr != nil {
		failure(w, 500, "读取本站身份失败")
		return
	}
	jsonResponse(w, 200, httpapi.GatewayResponse{InstanceID: instanceID, SiteID: a.cfg.siteID(), Scope: scope, Status: buffer.Code, Body: json.RawMessage(buffer.Body.Bytes())})
}

// adoptBusiness stages a role conversion for a legacy independently managed
// site. The panel process is intentionally unable to rewrite the root-owned
// config file, so a state marker is consumed on the next supervised restart.
func (a *App) adoptBusiness(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.Method != "POST" || actor.Role != "owner" || a.cfg.businessAgent() {
		failure(w, 403, "只有独立站管理员可以转换为子站")
		return
	}
	var in businessAdoption
	if !decode(w, r, &in) {
		return
	}
	in.SiteID = strings.TrimSpace(in.SiteID)
	in.ControllerURL = strings.TrimSpace(in.ControllerURL)
	if in.SiteID != a.cfg.siteID() || !validBusinessBootstrap(in.ConnectToken) || validateControllerURL(in.ControllerURL, a.cfg.Dev) != nil {
		failure(w, 400, "子站转换信息无效")
		return
	}
	if sites, err := a.store.businessSites(); err != nil {
		failure(w, 500, "读取现有子站失败")
		return
	} else if len(sites) > 0 {
		failure(w, 409, "已有主站业务配置，不能把主站转换为子站")
		return
	}
	markerPath := filepath.Join(a.cfg.StateDir, businessAdoptionFile)
	if raw, err := os.ReadFile(markerPath); err == nil {
		var previous businessAdoption
		if json.Unmarshal(raw, &previous) == nil && previous == in {
			jsonResponse(w, 200, object{"ok": true, "role": "business", "restart_required": true})
			return
		}
		failure(w, 409, "本站已有待处理的子站转换")
		return
	}
	if err := writeJSON(markerPath, in); err != nil {
		failure(w, 500, "保存子站转换信息失败")
		return
	}
	a.store.audit(actor.Username, "business-adopt", in.SiteID)
	jsonResponse(w, 200, object{"ok": true, "role": "business", "restart_required": true})
	if a.restart != nil {
		go func() {
			time.Sleep(250 * time.Millisecond)
			a.restart()
		}()
	}
}

func (a *App) relaySiteAPI(w http.ResponseWriter, r *http.Request, actor Record, id string) {
	if !a.cfg.controller() || actor.Role != "owner" {
		failure(w, 403, "群站管理需要 Pro 管理员权限")
		return
	}
	if !httpapi.AllowedGateway(r.Method, r.URL.RequestURI(), "manage") {
		failure(w, 403, "此操作需要在目标站点直接执行")
		return
	}
	peer, err := a.remoteSubsite(id)
	if err != nil {
		failure(w, 404, "站点不存在")
		return
	}
	if httpapi.ValidateEndpoint(peer.URL, a.cfg.Dev) != nil {
		failure(w, 400, "站点地址无效")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(b) > 1<<20 {
		failure(w, 413, "请求过大")
		return
	}
	in := httpapi.GatewayRequest{Method: r.Method, Path: r.URL.RequestURI()}
	if len(b) > 0 {
		in.Body = b
	}
	transport := httpapi.NewGatewayTransport(a.cfg.Dev)
	defer transport.CloseIdleConnections()
	out, err := httpapi.CallGateway(r.Context(), transport, peer.URL, peer.Token, in)
	if err != nil {
		failure(w, 502, "站点连接失败，请检查地址、证书和接入令牌")
		return
	}
	if out.SiteID != peer.SiteID || peer.InstanceID != "" && out.InstanceID != peer.InstanceID {
		failure(w, 502, "站点身份发生变化，请重新接入")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(out.Status)
	_, _ = w.Write(out.Body)
}
func (a *App) fleetAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	path := strings.TrimPrefix(r.URL.Path, "/api/fleet")
	// Lite exposes only its local pairing-token lifecycle.  Site takeover and
	// peer administration remain Pro-master capabilities.
	if a.cfg.edition() != "pro" {
		allowed := strings.HasPrefix(path, "/tokens/") && strings.HasSuffix(path, "/sharing") && (r.Method == "GET" || r.Method == "PUT") || path == "" && r.Method == "GET" || path == "/tokens" && r.Method == "POST" || strings.HasPrefix(path, "/tokens/") && r.Method == "DELETE"
		if !allowed {
			failure(w, 403, "Lite 仅支持生成和撤销配对令牌，站点接管需要 Pro 主站")
			return
		}
	}
	if path == "" && r.Method == "GET" {
		peers := []FleetPeer{}
		if a.cfg.edition() == "pro" {
			var err error
			peers, err = a.store.fleetPeers()
			if err != nil {
				failure(w, 500, "读取站点失败")
				return
			}
		}
		tokens := []object{}
		rows, err := a.store.db.Query("SELECT id,name,scope,created,expires,last_used FROM fleet_tokens ORDER BY created DESC")
		if err != nil {
			failure(w, 500, "读取接入令牌失败")
			return
		}
		for rows.Next() {
			var id, name, scope string
			var created, expires, used int64
			if err = rows.Scan(&id, &name, &scope, &created, &expires, &used); err != nil {
				rows.Close()
				failure(w, 500, "读取接入令牌失败")
				return
			}
			tokens = append(tokens, object{"id": id, "name": name, "scope": scope, "created": created, "expires": expires, "last_used": used})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			failure(w, 500, "读取接入令牌失败")
			return
		}
		jsonResponse(w, 200, object{"site_id": a.cfg.siteID(), "peers": peers, "tokens": tokens})
		return
	}
	if path == "/tokens" && r.Method == "POST" {
		var in struct {
			Name    string `json:"name"`
			Scope   string `json:"scope"`
			Days    int    `json:"days"`
			Forever bool   `json:"forever"`
		}
		if !decode(w, r, &in) {
			return
		}
		if a.cfg.edition() == "lite" {
			in.Scope = "manage"
		}
		if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 64 || (in.Scope != "read" && in.Scope != "manage") || (!in.Forever && (in.Days < 1 || in.Days > 365)) {
			failure(w, 400, "请填写令牌名称、权限和 1 至 365 天有效期，或选择永久有效")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		var count int
		if a.store.db.QueryRow("SELECT COUNT(*) FROM fleet_tokens").Scan(&count) != nil || count >= 32 {
			failure(w, 409, "最多保留 32 个接入令牌")
			return
		}
		id, token := randomToken(12), "gyp_"+randomToken(32)
		now := time.Now().Unix()
		expires := int64(0)
		if !in.Forever {
			expires = now + int64(in.Days)*86400
		}
		_, err := a.store.db.Exec("INSERT INTO fleet_tokens(id,token_hash,name,actor_id,scope,created,expires) VALUES(?,?,?,?,?,?,?)", id, digest(token), in.Name, actor.ID, in.Scope, now, expires)
		if err != nil {
			failure(w, 500, "创建接入令牌失败")
			return
		}
		a.store.audit(actor.Username, "fleet_token_create", id)
		instanceID, identityErr := a.store.siteInstanceID()
		if identityErr != nil {
			failure(w, 500, "读取本站身份失败")
			return
		}
		connection := encodeSiteToken(SiteToken{InstanceID: instanceID, Version: 1, URL: a.cfg.PublicURL, SiteID: a.cfg.siteID(), Token: token, Name: a.store.siteSettings().PanelName})
		jsonResponse(w, 201, object{"id": id, "token": token, "connection_token": connection, "site_id": a.cfg.siteID(), "expires": expires})
		return
	}
	if strings.HasPrefix(path, "/tokens/") && strings.HasSuffix(path, "/sharing") {
		a.mountSharingAPI(w, r, actor, strings.TrimSuffix(strings.TrimPrefix(path, "/tokens/"), "/sharing"))
		return
	}
	if strings.HasPrefix(path, "/tokens/") && r.Method == "DELETE" {
		a.mu.Lock()
		defer a.mu.Unlock()
		id := strings.TrimPrefix(path, "/tokens/")
		if _, err := a.store.db.Exec("DELETE FROM fleet_tokens WHERE id=?", id); err != nil {
			failure(w, 500, "撤销令牌失败")
			return
		}
		a.status = "pending"
		err := a.reconcile()
		if err == nil {
			err = a.settleHYRevocations()
		}
		a.store.audit(actor.Username, "fleet_token_revoke", id)
		jsonResponse(w, 200, object{"ok": true, "pending": err != nil})
		return
	}
	if path == "/peers" && r.Method == "POST" {
		var in struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 64 || httpapi.ValidateEndpoint(in.URL, a.cfg.Dev) != nil || len(in.Token) != 47 || !strings.HasPrefix(in.Token, "gyp_") {
			failure(w, 400, "请填写有效站点名称、HTTPS 地址和接入令牌")
			return
		}
		transport := httpapi.NewGatewayTransport(a.cfg.Dev)
		defer transport.CloseIdleConnections()
		out, err := httpapi.CallGateway(r.Context(), transport, in.URL, in.Token, httpapi.GatewayRequest{Method: "GET", Path: "/api/operations"})
		if err != nil || out.Status != 200 {
			failure(w, 400, "站点验证失败，请检查地址、证书和接入令牌")
			return
		}
		if out.SiteID == a.cfg.siteID() {
			failure(w, 400, "站点标识重复，请为每个站点配置不同 site_id")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		peers, err := a.store.fleetPeers()
		if err != nil || len(peers) >= 64 {
			failure(w, 409, "最多接入 64 个站点")
			return
		}
		for _, p := range peers {
			if p.SiteID == out.SiteID {
				failure(w, 409, "该站点已接入")
				return
			}
		}
		u, _ := url.Parse(in.URL)
		u.Path = ""
		p := FleetPeer{Scope: out.Scope, ID: randomToken(12), Name: strings.TrimSpace(in.Name), URL: u.String(), Token: in.Token, SiteID: out.SiteID, Created: time.Now().Unix()}
		b, err := a.store.vault.seal(p)
		if err == nil {
			_, err = a.store.db.Exec("INSERT INTO fleet_peers(id,doc) VALUES(?,?)", p.ID, b)
		}
		if err != nil {
			failure(w, 500, "保存站点失败")
			return
		}
		a.store.audit(actor.Username, "fleet_peer_add", p.ID)
		p.Token = ""
		jsonResponse(w, 201, p)
		return
	}
	if strings.HasPrefix(path, "/peers/") && r.Method == "DELETE" {
		id := strings.TrimPrefix(path, "/peers/")
		if _, err := a.store.db.Exec("DELETE FROM fleet_peers WHERE id=?", id); err != nil {
			failure(w, 500, "移除站点失败")
			return
		}
		a.store.audit(actor.Username, "fleet_peer_remove", id)
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	failure(w, 404, "接口不存在")
}
