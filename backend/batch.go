package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

type batchRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"`
}

type resourceChange struct {
	oldPools, newPools       []IPResource
	oldNodes, newNodes       []Node
	oldRecords, newRecords   []Record
	removePools, removeNodes []string
}

// Validate the entire selection before changing anything. Source-owned resources
// remain under their source's lifecycle, including legacy membership references.
func (a *App) planResourceChange(kind string, input batchRequest) (resourceChange, error) {
	p := resourceChange{}
	if len(input.IDs) == 0 || len(input.IDs) > 256 || input.Action != "delete" && input.Action != "enable" && input.Action != "disable" {
		return p, errors.New("请选择 1–256 项和有效的批量操作")
	}
	selected := map[string]bool{}
	for _, id := range input.IDs {
		if id == "" || selected[id] {
			return p, errors.New("选择项为空或重复，请刷新后重试")
		}
		selected[id] = true
	}
	pools, err := a.store.pools()
	if err != nil {
		return p, err
	}
	nodes, err := a.store.nodes()
	if err != nil {
		return p, err
	}
	found, affected := map[string]bool{}, map[string]bool{}
	if kind == "ips" {
		sources, err := a.store.importSources()
		if err != nil {
			return p, err
		}
		for _, old := range pools {
			if !selected[old.ID] {
				continue
			}
			if old.PoolGroup == "public" {
				return p, errors.New("公共资源由公共代理池管理")
			}
			if input.Action == "delete" {
				managed := old.SubscriptionID != ""
				for _, s := range sources {
					managed = managed || containsSourceID(s.ResourceIDs, old.ID)
				}
				if managed {
					return p, errors.New("订阅出口请通过订阅来源管理删除")
				}
			}
			found[old.ID] = true
			p.oldPools = append(p.oldPools, old)
			if input.Action == "delete" {
				p.removePools = append(p.removePools, old.ID)
			} else {
				next := old
				next.Enabled = input.Action == "enable"
				next.Revision = randomToken(12)
				p.newPools = append(p.newPools, next)
			}
			for _, n := range nodes {
				if n.ExitID == old.ID {
					affected[n.ID] = true
				}
			}
		}
	} else if kind == "nodes" {
		for _, n := range nodes {
			if selected[n.ID] {
				found[n.ID], affected[n.ID] = true, true
			}
		}
	} else {
		return p, errors.New("无效的资源类型")
	}
	if len(found) != len(selected) {
		return p, errors.New("部分选择项已不存在，请刷新后重试")
	}
	for _, old := range nodes {
		if !affected[old.ID] {
			continue
		}
		if old.DefaultDirect && input.Action != "enable" {
			return p, errors.New(defaultDirectMessage)
		}
		if old.ManagedBy == publicManager {
			return p, errors.New("自动节点由公共代理池管理")
		}
		p.oldNodes = append(p.oldNodes, old)
		if input.Action == "delete" {
			p.removeNodes = append(p.removeNodes, old.ID)
			continue
		}
		next := old
		if kind == "nodes" {
			next.Enabled = input.Action == "enable"
			if next.Enabled && next.ExitID != "" {
				resource, e := a.store.pool(next.ExitID)
				if e != nil || !resource.Enabled {
					return p, errors.New("绑定的 IP 出口未启用，无法启用节点")
				}
			}
		} else if input.Action == "disable" {
			next.Enabled = false
		}
		// Enabling an exit does not unexpectedly enable previously disabled nodes.
		p.newNodes = append(p.newNodes, next)
	}
	if len(p.removeNodes) > 0 {
		records, err := a.store.records()
		if err != nil {
			return p, err
		}
		for _, old := range records {
			next := old
			next.Credentials.VLESS = map[string]string{}
			changed := false
			for id, key := range old.Credentials.VLESS {
				if affected[id] {
					changed = true
				} else {
					next.Credentials.VLESS[id] = key
				}
			}
			if changed {
				p.oldRecords = append(p.oldRecords, old)
				p.newRecords = append(p.newRecords, next)
			}
		}
	}
	return p, nil
}

func (a *App) applyResourceChange(p resourceChange, apply func() error) error {
	if err := a.store.saveInfrastructure(p.newPools, p.newNodes, p.newRecords, p.removePools, p.removeNodes); err != nil {
		return errors.New("批量操作未保存，原配置未改变")
	}
	if err := apply(); err != nil {
		a.status, a.syncError = "error", "resource change apply failed"
		if err := a.store.saveInfrastructure(p.oldPools, p.oldNodes, p.oldRecords, nil, nil); err != nil {
			return errors.New("批量操作应用失败且恢复未完成，请检查系统状态")
		}
		if err := apply(); err != nil {
			return errors.New("已恢复原配置，核心同步等待重试")
		}
		a.status, a.syncError = "applied", ""
		return errors.New("批量操作应用失败，已恢复出口、节点与凭据")
	}
	a.status, a.syncError, a.appliedAt = "applied", "", time.Now().Unix()
	return nil
}

func (a *App) changeResources(w http.ResponseWriter, r *http.Request, actor Record, kind string, input batchRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.heavyMu.TryLock() {
		failure(w, 409, "资源任务正在运行，请稍后重试删除")
		return
	}
	defer a.heavyMu.Unlock()
	current, err := a.store.record(actor.ID)
	if err != nil || !current.Enabled || current.Role != "owner" {
		failure(w, 403, "管理员权限已变更，请重新登录")
		return
	}
	p, err := a.planResourceChange(kind, input)
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	if len(p.oldNodes) > 0 && !a.cfg.Dev {
		if err := a.collect(); err != nil {
			failure(w, 502, "流量同步暂未完成，请稍后重试删除")
			return
		}
	}
	if err := a.applyResourceChange(p, a.reconcile); err != nil {
		failure(w, 502, err.Error())
		return
	}
	a.store.audit(actor.Username, "batch-"+kind+"-"+input.Action, fmt.Sprintf("selected=%d resources=%d nodes=%d", len(input.IDs), len(p.oldPools), len(p.oldNodes)))
	jsonResponse(w, 200, object{"ok": true, "selected": len(input.IDs), "resources_affected": len(p.oldPools), "nodes_affected": len(p.oldNodes), "resources_deleted": len(p.removePools), "nodes_deleted": len(p.removeNodes)})
}

func (a *App) batchResources(w http.ResponseWriter, r *http.Request, actor Record, kind string) {
	var input batchRequest
	if !decode(w, r, &input) {
		return
	}
	a.changeResources(w, r, actor, kind, input)
}
