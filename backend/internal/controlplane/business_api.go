package controlplane

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

func decodeBusiness(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		failure(w, 400, "业务站请求格式无效或超出限制")
		return false
	}
	return true
}
func (a *App) businessAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	if !a.cfg.controller() || actor.Role != "owner" {
		failure(w, 403, "需要 Pro 主控站管理员权限")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/business-sites")
	if path == "" && r.Method == "GET" {
		sites, err := a.store.businessSites()
		if err != nil {
			failure(w, 500, "读取业务站失败")
			return
		}
		for i := range sites {
			sites[i] = sites[i].public()
		}
		jsonResponse(w, 200, object{"role": a.cfg.deploymentRole(), "site_id": a.cfg.siteID(), "sites": sites, "lease_seconds": businessLeaseSeconds})
		return
	}
	if path == "" && r.Method == "POST" {
		var in struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Group string `json:"group"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		in.Group = strings.TrimSpace(in.Group)
		if !persistence.ValidSite(in.ID) || in.ID == a.cfg.siteID() || in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || utf8.RuneCountInString(in.Group) > 40 {
			failure(w, 400, "请填写唯一站点标识和有效名称")
			return
		}
		sites, err := a.store.businessSites()
		if err != nil || len(sites) >= businessMaxSites {
			failure(w, 400, "业务站数量已达上限")
			return
		}
		token := "gye_" + randomToken(32)
		v := BusinessSite{ID: in.ID, Name: in.Name, Group: in.Group, Enabled: true, OwnerID: actor.ID, Created: time.Now().Unix(), EnrollmentExpires: time.Now().Add(24 * time.Hour).Unix(), Revision: randomToken(12), Nodes: []Node{}, Grants: []BusinessGrant{}}
		v.defaults()
		b, err := a.store.vault.seal(v)
		if err == nil {
			_, err = a.store.db.Exec("INSERT INTO business_sites(id,enroll_hash,doc) VALUES(?,?,?)", v.ID, digest(token), b)
		}
		if err != nil {
			failure(w, 409, "站点标识已存在或保存失败")
			return
		}
		a.store.audit(actor.Username, "create-business-site", v.ID)
		jsonResponse(w, 201, object{"site": v.public(), "enrollment": object{"site_id": v.ID, "controller_url": a.cfg.PublicURL, "enrollment_token": token}, "expires": v.EnrollmentExpires})
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		failure(w, 404, "业务站不存在")
		return
	}
	v, err := a.store.businessSite(parts[0])
	if err != nil {
		failure(w, 404, "业务站不存在")
		return
	}
	if len(parts) == 2 && parts[1] == "tasks" && r.Method == "POST" {
		var in struct {
			Kind   string `json:"kind"`
			NodeID string `json:"node_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !v.Enabled || v.Info == nil || (in.Kind != "speed" && in.Kind != "quality") {
			failure(w, 400, "业务站或检测类型无效")
			return
		}
		found := false
		for _, n := range v.SentNodes {
			if n.ID == in.NodeID && n.Enabled {
				found = true
			}
		}
		if !found {
			failure(w, 404, "节点不存在或尚未同步")
			return
		}
		pending := []BusinessCommand{}
		for _, c := range v.Commands {
			if c.State == "queued" || c.State == "running" {
				if c.Kind == in.Kind && c.NodeID == in.NodeID {
					jsonResponse(w, 202, c)
					return
				}
				pending = append(pending, c)
			}
		}
		if len(pending) >= 16 {
			failure(w, 429, "业务站检测队列已满")
			return
		}
		cmd := BusinessCommand{ID: randomToken(16), Kind: in.Kind, NodeID: in.NodeID, Created: time.Now().Unix(), State: "queued"}
		v.Commands = append(pending, cmd)
		if err = a.store.saveBusinessSite(v); err != nil {
			failure(w, 500, "保存检测任务失败")
			return
		}
		jsonResponse(w, 202, cmd)
		return
	}
	if len(parts) == 1 && r.Method == "PUT" {
		var in struct {
			Name         string          `json:"name"`
			Group        string          `json:"group"`
			Enabled      bool            `json:"enabled"`
			Exclusive    bool            `json:"exclusive"`
			Revision     string          `json:"revision"`
			Nodes        []Node          `json:"nodes"`
			DefaultNodes []Node          `json:"default_nodes"`
			Grants       []BusinessGrant `json:"grants"`
		}
		if !decodeBusiness(w, r, &in) {
			return
		}
		if in.Revision != v.Revision {
			failure(w, 409, "站点配置已更新，请刷新后重试")
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		in.Group = strings.TrimSpace(in.Group)
		if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || utf8.RuneCountInString(in.Group) > 40 {
			failure(w, 400, "站点名称或分组无效")
			return
		}
		v.Name = in.Name
		v.Group = in.Group
		v.Enabled = in.Enabled
		v.Exclusive = in.Exclusive
		for i := range in.Grants {
			g := &in.Grants[i]
			u, e := a.store.record(g.UserID)
			if e != nil {
				failure(w, 400, "用户不存在")
				return
			}
			if u.Meter != nil {
				if u.Meter.PendingReset {
					failure(w, 409, "用户正在切换配额周期")
					return
				}
				g.PeriodID = u.Meter.PeriodID
			}
			g.Budget = max(int64(0), g.Quota-v.Usage[g.UserID].total())
		}
		v.Grants = in.Grants
		if in.DefaultNodes != nil {
			for i := range in.DefaultNodes {
				n := &in.DefaultNodes[i]
				if n.ID != "vless-main" && n.ID != "hy2-main" {
					failure(w, 400, "默认节点标识无效")
					return
				}
				old := normalizeNodePolicy(Node{ID: n.ID})
				for _, p := range v.DefaultNodes {
					if p.ID == n.ID {
						old = p
					}
				}
				n.ManagedBy = ""
				if err = a.store.validateNodePolicy(n, &old); err != nil {
					failure(w, 400, err.Error())
					return
				}
			}
			v.DefaultNodes = in.DefaultNodes
		}
		v.Nodes = nil
		for _, n := range in.Nodes {
			if p, e := a.store.pool(n.ExitID); e == nil && p.PoolGroup == "public" {
				n.ManagedBy = publicManager
			}
			var old *Node
			for _, prior := range v.SentNodes {
				if prior.ID == n.ID {
					copy := normalizeNodePolicy(prior)
					old = &copy
				}
			}
			if err = a.store.validateNodePolicy(&n, old); err != nil {
				failure(w, 400, err.Error())
				return
			}
			v.Nodes = append(v.Nodes, businessNodeTemplate(n))
		}
		if err = a.validateBusinessPolicy(v); err != nil {
			failure(w, 409, err.Error())
			return
		}
		v.Revision = randomToken(12)
		if err = a.store.saveBusinessSite(v); err != nil {
			failure(w, 500, "保存业务站失败")
			return
		}
		a.status = "pending"
		a.store.audit(actor.Username, "update-business-site", v.ID)
		jsonResponse(w, 200, v.public())
		return
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		if v.Enabled || len(v.Grants) > 0 || len(v.SentGrants) > 0 || v.Applied != v.Desired && v.Info != nil {
			failure(w, 409, "请先停用站点并清空成员，等待业务站确认撤销后再删除")
			return
		}
		if _, err = a.store.db.Exec("DELETE FROM business_sites WHERE id=?", v.ID); err != nil {
			failure(w, 500, "删除业务站失败")
			return
		}
		a.store.audit(actor.Username, "delete-business-site", v.ID)
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	failure(w, 404, "接口不存在")
}
func (a *App) businessEnroll(w http.ResponseWriter, r *http.Request) {
	if a.gatewaySlots != nil {
		select {
		case a.gatewaySlots <- struct{}{}:
			defer func() { <-a.gatewaySlots }()
		default:
			failure(w, 429, "业务站同步繁忙，请稍后重试")
			return
		}
	}
	if !a.cfg.controller() {
		failure(w, 404, "接口不存在")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) != 47 || !strings.HasPrefix(token, "gye_") {
		failure(w, 401, "注册凭据无效")
		return
	}
	var info BusinessInfo
	if !decodeBusiness(w, r, &info) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var id string
	if err := a.store.db.QueryRow("SELECT id FROM business_sites WHERE enroll_hash=?", digest(token)).Scan(&id); err != nil {
		failure(w, 401, "注册凭据已失效")
		return
	}
	v, err := a.store.businessSite(id)
	if err != nil || !v.Enabled || v.EnrollmentExpires <= time.Now().Unix() || !a.businessOwnerEnabled(v) {
		failure(w, 401, "注册凭据已失效")
		return
	}
	if id != info.SiteID || !validBusinessInfo(info) || v.Info != nil && !equalBusinessInfo(*v.Info, info) {
		failure(w, 409, "业务站身份与注册信息不符")
		return
	}
	if v.PendingToken == "" {
		v.PendingToken = "gyb_" + randomToken(32)
	}
	v.Info = &info
	v.LastSeen = time.Now().Unix()
	b, err := a.store.vault.seal(v)
	if err == nil {
		_, err = a.store.db.Exec("UPDATE business_sites SET doc=?,token_hash=? WHERE id=?", b, digest(v.PendingToken), v.ID)
	}
	if err != nil {
		failure(w, 500, "注册业务站失败")
		return
	}
	jsonResponse(w, 200, object{"site_id": v.ID, "token": v.PendingToken})
}
func (a *App) businessSync(w http.ResponseWriter, r *http.Request) {
	if a.gatewaySlots != nil {
		select {
		case a.gatewaySlots <- struct{}{}:
			defer func() { <-a.gatewaySlots }()
		default:
			failure(w, 429, "业务站同步繁忙，请稍后重试")
			return
		}
	}
	if !a.cfg.controller() {
		failure(w, 404, "接口不存在")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) != 47 || !strings.HasPrefix(token, "gyb_") {
		failure(w, 401, "业务站凭据无效")
		return
	}
	var in BusinessHeartbeat
	if !decodeBusiness(w, r, &in) {
		return
	}
	if len(in.Usage) > 4096 || len(in.Reports) > 16 || len(in.Commands) > 32 || len(in.Version) > 32 {
		failure(w, 400, "业务站报告超出限制")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var id string
	if err := a.store.db.QueryRow("SELECT id FROM business_sites WHERE token_hash=?", digest(token)).Scan(&id); err != nil {
		failure(w, 401, "业务站凭据已失效")
		return
	}
	if subtle.ConstantTimeCompare([]byte(id), []byte(in.SiteID)) != 1 {
		failure(w, 403, "业务站身份不符")
		return
	}
	v, err := a.store.businessSite(id)
	if err != nil || v.Info == nil {
		failure(w, 401, "业务站未注册")
		return
	}
	if err = a.acceptBusinessUsage(&v, in); err != nil {
		failure(w, 409, "流量报告无效，未更新计量")
		return
	}
	for i := range v.Commands {
		status := in.Commands[v.Commands[i].ID]
		if status == "queued" || status == "running" || status == "succeeded" || status == "failed" || status == "cancelled" {
			v.Commands[i].State = status
		}
		if v.Commands[i].Created < time.Now().Add(-24*time.Hour).Unix() && (v.Commands[i].State == "queued" || v.Commands[i].State == "running") {
			v.Commands[i].State = "failed"
		}
	}
	if in.Version != "" {
		v.Info.Version = in.Version
		if in.Protocol == 2 {
			v.Info.Protocol = 2
		}
	}
	v.PendingToken = ""
	v.LastSeen = time.Now().Unix()
	v.Error = ""
	if in.Error != "" {
		v.Error = "业务站配置尚未生效，请检查业务站运行状态"
	}
	if in.Applied == v.Desired {
		v.Reports = nil
		for _, report := range in.Reports {
			for _, sent := range v.SentNodes {
				if sent.ID != report.ID {
					continue
				}
				b, marshalErr := json.Marshal(report.Quality)
				if marshalErr != nil || len(b) > 48<<10 {
					report.Quality = nil
				}
				if len(report.Country) > 128 || len(report.CountryCode) > 2 {
					report.Country = ""
					report.CountryCode = ""
				}
				if report.Speed != nil && (report.Speed.Mbps < 0 || report.Speed.Mbps > 1e7 || report.Speed.Bytes < 0 || report.Speed.LatencyMS < 0 || len(report.Speed.Error) > 256) {
					report.Speed = nil
				}
				n := memberNodeState(report)
				n.ID = sent.ID
				n.Protocol = sent.Protocol
				n.Enabled = sent.Enabled
				n.DefaultDirect = sent.DefaultDirect
				n.Exit = sent.Exit
				n.ExitID = sent.ExitID
				n.ManagedBy = sent.ManagedBy
				n.DNS = sent.DNS
				n.Speed = report.Speed
				if len(n.Name) > 256 {
					n.Name = ""
				}
				if !publicIP(n.ProbeIP) {
					n.ProbeIP = ""
					n.Quality = nil
				}
				v.Reports = append(v.Reports, n)
				break
			}
		}
	}
	out, err := a.businessSnapshot(&v)
	out.UsageAck = append([]NodeUsage{}, in.NodeUsage...)
	if err == nil {
		_, err = a.store.db.Exec("UPDATE business_sites SET enroll_hash=NULL WHERE id=?", v.ID)
	}
	if err != nil {
		failure(w, 500, "生成业务站配置失败")
		return
	}
	a.status = "pending"
	jsonResponse(w, 200, out)
}
