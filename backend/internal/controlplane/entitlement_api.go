package controlplane

import (
	"encoding/json"
	"errors"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (a *App) entitlementAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.store.record(actor.ID)
	if err != nil || !current.Enabled || current.Role != "owner" {
		failure(w, 403, "管理员权限已变更")
		return
	}
	path := r.URL.Path
	if path == "/api/nodes/policy" {
		a.nodePolicyAPI(w, r, actor)
		return
	}
	if path == "/api/entitlements/batch" {
		a.entitlementBatch(w, r, actor)
		return
	}
	if r.Method == "GET" && path == "/api/plans" {
		plans, e := a.store.plans()
		if e != nil {
			failure(w, 500, "读取套餐失败")
			return
		}
		jsonResponse(w, 200, object{"plans": plans})
		return
	}
	if r.Method == "GET" && path == "/api/node-groups" {
		groups, e := a.store.nodeGroups()
		if e != nil {
			failure(w, 500, "读取节点组失败")
			return
		}
		nodes, e := a.store.nodes()
		if e != nil {
			failure(w, 500, "读取节点失败")
			return
		}
		members := map[string][]object{}
		for _, g := range groups {
			members[g.ID] = []object{}
		}
		add := func(site string, n Node) {
			for _, id := range normalizeNodePolicy(n).GroupIDs {
				members[id] = append(members[id], object{"site_id": site, "node_id": n.ID, "name": n.Name, "protocol": n.Protocol})
			}
		}
		for _, n := range nodes {
			add(a.cfg.siteID(), n)
		}
		sites, e := a.store.businessSites()
		if e != nil {
			failure(w, 500, "读取业务站失败")
			return
		}
		for _, s := range sites {
			ns, e := a.materializeBusinessNodes(s)
			if e != nil {
				failure(w, 500, "读取业务节点失败")
				return
			}
			for _, n := range ns {
				add(s.ID, n)
			}
		}
		jsonResponse(w, 200, object{"groups": groups, "members": members})
		return
	}
	if r.Method == "POST" && path == "/api/node-groups" {
		var g NodeGroup
		if !decode(w, r, &g) {
			return
		}
		g.Name = strings.TrimSpace(g.Name)
		if g.Sort < 0 || g.Sort > 9999 || g.Name == "" || len(g.Name) > 100 || len(g.Description) > 1000 || g.Scope != "private" && g.Scope != "public" {
			failure(w, 400, "节点组名称或订阅范围无效")
			return
		}
		groups, e := a.store.nodeGroups()
		if e != nil {
			failure(w, 500, "读取节点组失败")
			return
		}
		found := false
		for _, old := range groups {
			if old.ID == g.ID {
				found = true
				if old.Enabled != g.Enabled {
					if e := a.validateGroupCapability(old.ID); e != nil {
						failure(w, 409, e.Error())
						return
					}
				}
				if old.Revision != g.Revision {
					failure(w, 409, "节点组已变化，请刷新后重试")
					return
				}
				if old.Scope != g.Scope {
					failure(w, 409, "已创建节点组不能更改订阅范围")
					return
				}
			}
		}
		if g.ID != "" && !found {
			failure(w, 404, "节点组不存在")
			return
		}
		if g.ID == "" {
			if len(groups) >= 128 {
				failure(w, 400, "节点组最多 128 个")
				return
			}
			g.ID = "group-" + randomToken(9)
		}
		g.Revision = randomToken(12)
		b, _ := json.Marshal(g)
		if _, e = a.store.db.Exec("INSERT INTO node_groups(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", g.ID, b); e != nil {
			failure(w, 500, "保存节点组失败")
			return
		}
		a.status = "pending"
		a.store.audit(actor.Username, "save_node_group", g.ID)
		jsonResponse(w, 200, g)
		return
	}
	if r.Method == "POST" && path == "/api/plans" {
		var p Plan
		if !decode(w, r, &p) {
			return
		}
		if err = a.validatePlan(&p); err != nil {
			failure(w, 400, err.Error())
			return
		}
		if p.ID == "" {
			plans, e := a.store.plans()
			if e != nil {
				failure(w, 500, "读取套餐失败")
				return
			}
			if len(plans) >= 256 {
				failure(w, 400, "套餐最多 256 个")
				return
			}
			p.ID = "plan-" + randomToken(9)
			p.Version = 1
		} else {
			old, e := a.store.plan(p.ID)
			if e != nil {
				failure(w, 404, "套餐不存在")
				return
			}
			if old.Version != p.Version {
				failure(w, 409, "套餐已变化，请刷新后重试")
				return
			}
			p.Version++
		}
		b, _ := json.Marshal(p)
		tx, e := a.store.db.Begin()
		if e != nil {
			failure(w, 500, "保存套餐失败")
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("INSERT INTO plans(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", p.ID, b); e == nil {
			_, e = tx.Exec("INSERT INTO plan_versions(plan_id,version,doc) VALUES(?,?,?)", p.ID, p.Version, b)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			failure(w, 500, "保存套餐失败")
			return
		}
		a.store.audit(actor.Username, "save_plan", p.ID)
		jsonResponse(w, 200, p)
		return
	}
	failure(w, 405, "方法不支持；套餐和节点组通过归档或停用保留历史引用")
}

func (a *App) validatePlan(p *Plan) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Timezone == "" {
		p.Timezone = "Asia/Shanghai"
	}
	if p.Cycle == "" {
		p.Cycle = "none"
	}
	if p.Sort < 0 || p.Sort > 9999 || p.Name == "" || len(p.Name) > 100 || len(p.Description) > 2000 || len(p.Category) > 100 || len(p.Notes) > 2000 || p.Quota < 0 || p.Quota > 1<<60 || p.Price < 0 || p.Price > moneyLimit || p.ValidDays < 0 || p.ValidDays > 36500 || !p.VLESS && !p.HY2 || len(p.GroupIDs) == 0 {
		return errors.New("请填写有效的套餐名称、额度、有效期、协议和节点组")
	}
	if _, err := domain.NextPeriod(time.Now().Unix(), p.Cycle, p.Timezone); err != nil {
		return errors.New("配额周期或时区无效")
	}
	groups, err := a.store.nodeGroups()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, g := range groups {
		known[g.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range p.GroupIDs {
		if !known[id] || seen[id] {
			return errors.New("套餐节点组不存在或重复")
		}
		seen[id] = true
	}
	sort.Strings(p.GroupIDs)
	return nil
}

type entitlementRequest struct {
	IDs         []int64 `json:"ids"`
	Action      string  `json:"action"`
	PlanID      string  `json:"plan_id"`
	Days        int     `json:"days"`
	Preview     bool    `json:"preview"`
	Expected    string  `json:"expected"`
	OperationID string  `json:"operation_id"`
}

func (a *App) entitlementBatch(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.Method != "POST" {
		failure(w, 405, "方法不支持")
		return
	}
	var in entitlementRequest
	if !decode(w, r, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 100 || in.Action != "assign" && in.Action != "renew" && in.Action != "reset" && in.Action != "independent" && in.Action != "disable" {
		failure(w, 400, "请选择用户和有效的权益操作")
		return
	}
	if in.Action == "renew" && (in.Days < 1 || in.Days > 36500) {
		failure(w, 400, "续期天数需为 1–36500")
		return
	}
	if !in.Preview && (len(in.OperationID) < 16 || len(in.OperationID) > 80) {
		failure(w, 400, "缺少操作标识，请重新预览")
		return
	}
	identity := in
	identity.Preview = false
	identity.Expected = ""
	identity.OperationID = ""
	encoded, _ := json.Marshal(identity)
	requestHash := digest(string(encoded))
	if !in.Preview {
		var priorHash string
		var prior []byte
		if e := a.store.db.QueryRow("SELECT fingerprint,doc FROM entitlement_operations WHERE id=?", in.OperationID).Scan(&priorHash, &prior); e == nil {
			if priorHash != requestHash {
				failure(w, 409, "操作标识已被其他请求使用")
				return
			}
			jsonResponse(w, 200, json.RawMessage(prior))
			return
		}
	}
	if err := a.collect(); err != nil {
		failure(w, 502, "流量同步未完成，请稍后重试")
		return
	}
	var plan Plan
	var err error
	if in.Action == "assign" {
		plan, err = a.store.plan(in.PlanID)
		if err != nil || plan.Archived {
			failure(w, 409, "套餐不存在或已归档")
			return
		}
	}
	records := []Record{}
	effects := []object{}
	versions := []object{}
	seen := map[int64]bool{}
	for _, id := range in.IDs {
		if seen[id] {
			failure(w, 400, "用户选择重复")
			return
		}
		seen[id] = true
		if pending, e := a.store.commercePending(id); e != nil || pending {
			failure(w, 409, "请先处理用户的未完成订单")
			return
		}
		u, e := a.store.record(id)
		if e != nil {
			failure(w, 404, "部分用户已不存在")
			return
		}
		if u.Archived {
			failure(w, 409, "账号已归档，历史记录只读")
			return
		}
		if u.Meter != nil && u.Meter.PendingReset {
			failure(w, 409, "用户正在等待旧周期业务站授权撤回")
			return
		}
		if in.Action == "disable" && u.Role == "owner" {
			failure(w, 409, "不能停用管理员登录")
			return
		}
		u.InitMeter("legacy-"+businessUsageKey(u.ID), time.Now().Unix())
		versions = append(versions, object{"id": id, "entitlement": u.Entitlement, "quota": u.Quota, "expires": u.Expires, "enabled": u.Enabled, "vless": u.VLESS, "hy2": u.HY2, "period": u.Meter.PeriodID})
		before := u.User
		meterCopy := *u.Meter
		u.Meter = &meterCopy
		switch in.Action {
		case "assign":
			u.Entitlement = &domain.Entitlement{PlanID: plan.ID, Version: plan.Version, Name: plan.Name, GroupIDs: append([]string{}, plan.GroupIDs...), Cycle: plan.Cycle, Timezone: plan.Timezone, AssignedAt: time.Now().Unix(), Revision: randomToken(12)}
			u.Quota, u.VLESS, u.HY2 = plan.Quota, plan.VLESS, plan.HY2
			u.Expires = 0
			if plan.ValidDays > 0 {
				u.Expires = time.Now().Unix() + int64(plan.ValidDays)*86400
			}
			u.Meter.End, e = domain.NextPeriod(time.Now().Unix(), plan.Cycle, plan.Timezone)
			if e != nil {
				failure(w, 400, "套餐周期无效")
				return
			}
		case "renew":
			u.Expires = max(time.Now().Unix(), u.Expires) + int64(in.Days)*86400
		case "reset":
			u.Meter.PendingReset = true
		case "independent":
			u.Entitlement = nil
			u.Meter.End = 0
		case "disable":
			u.Enabled = false
		}
		if in.Action != "reset" {
			if e = a.validateBusinessUserQuota(u); e != nil {
				failure(w, 409, e.Error())
				return
			}
		}
		if e = a.validateEntitlementSites(u, in.Action); e != nil {
			failure(w, 409, e.Error())
			return
		}
		effects = append(effects, object{"id": id, "username": u.Username, "before": before, "after": u.User, "quota_used": u.QuotaUsed(), "exhausted": u.Quota > 0 && u.QuotaUsed() >= u.Quota, "sync": "pending"})
		records = append(records, u)
	}
	fingerprintData, _ := json.Marshal(object{"request": requestHash, "versions": versions, "plan": plan})
	fingerprint := digest(string(fingerprintData))
	if in.Preview {
		jsonResponse(w, 200, object{"effects": effects, "expected": fingerprint})
		return
	}
	if in.Expected != fingerprint {
		failure(w, 409, "用户或套餐已变化，请重新预览")
		return
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		failure(w, 500, "保存权益失败")
		return
	}
	defer tx.Rollback()
	for _, u := range records {
		b, _ := json.Marshal(u.User)
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, u.ID); err != nil {
			failure(w, 500, "保存权益失败")
			return
		}
	}
	result := object{"ok": true, "effects": effects, "status": "pending"}
	body, _ := json.Marshal(result)
	if _, err = tx.Exec("INSERT INTO entitlement_operations(id,fingerprint,doc) VALUES(?,?,?)", in.OperationID, requestHash, body); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failure(w, 409, "权益操作未保存，请使用相同操作标识重试")
		return
	}
	a.status = "pending"
	a.store.audit(actor.Username, "entitlement_"+in.Action, in.OperationID)
	jsonResponse(w, 200, result)
}
