package controlplane

import (
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"net/http"
	"strings"
	"time"
)

func assignPlan(r *Record, p Plan, now int64) {
	r.InitMeter("period-"+randomToken(12), now)
	r.Entitlement = &domain.Entitlement{PlanID: p.ID, Version: p.Version, Name: p.Name, GroupIDs: append([]string{}, p.GroupIDs...), Cycle: p.Cycle, Timezone: p.Timezone, AssignedAt: now, Revision: randomToken(12)}
	r.Quota, r.VLESS, r.HY2 = p.Quota, p.VLESS, p.HY2
	r.Expires = 0
	if p.ValidDays > 0 {
		r.Expires = now + int64(p.ValidDays)*86400
	}
	r.Meter.End, _ = domain.NextPeriod(now, p.Cycle, p.Timezone)
}

func (a *App) nodePolicyAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.Method != "POST" {
		failure(w, 405, "方法不支持")
		return
	}
	var input struct {
		IDs       []string          `json:"ids"`
		RateMilli *int64            `json:"rate_milli"`
		GroupIDs  *[]string         `json:"group_ids"`
		SameExit  bool              `json:"same_exit"`
		Expected  map[string]string `json:"expected"`
		Preview   bool              `json:"preview"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.IDs) == 0 || len(input.IDs) > 16 || input.RateMilli == nil && input.GroupIDs == nil {
		failure(w, 400, "请选择节点和要修改的倍率或分组")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	selected := map[string]bool{}
	for _, id := range input.IDs {
		if selected[id] {
			failure(w, 400, "节点选择重复")
			return
		}
		selected[id] = true
	}
	if input.SameExit {
		for _, n := range nodes {
			for _, target := range nodes {
				if selected[target.ID] && sameExit(n, target) {
					selected[n.ID] = true
				}
			}
		}
	}
	p := resourceChange{}
	for _, old := range nodes {
		if !selected[old.ID] {
			continue
		}
		delete(selected, old.ID)
		if old.ManagedBy == publicManager {
			failure(w, 409, "自动节点倍率和分组由公共采集策略设置")
			return
		}
		if input.Expected != nil && input.Expected[old.ID] != nodePolicyRevision(old) {
			failure(w, 409, "节点权限已变化，请刷新后重试")
			return
		}
		n := old
		n.GroupIDs = append([]string{}, old.GroupIDs...)
		if input.RateMilli != nil {
			n.RateMilli = *input.RateMilli
		}
		if input.GroupIDs != nil {
			n.GroupIDs = append([]string{}, (*input.GroupIDs)...)
		}
		if err = a.store.validateNodePolicy(&n, &old); err != nil {
			failure(w, 400, err.Error())
			return
		}
		p.oldNodes = append(p.oldNodes, old)
		p.newNodes = append(p.newNodes, n)
	}
	if len(selected) > 0 {
		failure(w, 404, "部分节点已不存在")
		return
	}
	if input.Preview {
		expected := map[string]string{}
		changes := []object{}
		for i, old := range p.oldNodes {
			n := p.newNodes[i]
			expected[old.ID] = nodePolicyRevision(old)
			changes = append(changes, object{"id": old.ID, "name": old.Name, "before_rate": old.RateMilli, "after_rate": n.RateMilli, "before_groups": old.GroupIDs, "after_groups": n.GroupIDs})
		}
		jsonResponse(w, 200, object{"expected": expected, "changes": changes, "nodes_affected": len(changes)})
		return
	}
	if err = a.collect(); err != nil {
		failure(w, 502, "流量同步未完成，倍率未改变")
		return
	}
	if err = a.applyResourceChange(p, a.reconcile); err != nil {
		failure(w, 502, err.Error())
		return
	}
	a.store.audit(actor.Username, "node_policy", time.Now().Format(time.RFC3339))
	jsonResponse(w, 200, object{"ok": true, "nodes_affected": len(p.newNodes)})
}
func nodePolicyRevision(n Node) string {
	return n.RateRevision + ":" + digest(strings.Join(n.GroupIDs, "\n"))
}
