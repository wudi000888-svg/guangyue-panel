package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (a *App) userUsage(w http.ResponseWriter, r *http.Request, actor Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := actor.ID
	if value := r.URL.Query().Get("user_id"); value != "" {
		var e error
		id, e = strconv.ParseInt(value, 10, 64)
		if e != nil {
			failure(w, 400, "用户编号无效")
			return
		}
	}
	if id != actor.ID && actor.Role != "owner" {
		failure(w, 403, "只能查看自己的用量")
		return
	}
	u, err := a.store.record(id)
	if err != nil {
		failure(w, 404, "用户不存在")
		return
	}
	period := r.URL.Query().Get("period_id")
	if period == "" && u.Meter != nil {
		period = u.Meter.PeriodID
	}
	if len(period) > 100 {
		failure(w, 400, "周期标识无效")
		return
	}
	periods := []User{}
	rows, err := a.store.db.Query("SELECT doc FROM quota_periods WHERE user_id=?", id)
	if err != nil {
		failure(w, 500, "读取用量失败")
		return
	}
	for rows.Next() {
		var b []byte
		var prior User
		if err = rows.Scan(&b); err == nil {
			err = json.Unmarshal(b, &prior)
		}
		if err != nil {
			rows.Close()
			failure(w, 500, "读取用量失败")
			return
		}
		periods = append(periods, prior)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		failure(w, 500, "读取用量失败")
		return
	}
	entries := []object{}
	for _, source := range []struct {
		query  string
		remote bool
	}{{"SELECT node_id,rate_milli,rate_revision,upload,download FROM node_usage WHERE user_id=? AND period_id=?", false}, {"SELECT site_id,node_id,rate_milli,rate_revision,upload,download FROM business_node_usage WHERE user_id=? AND period_id=?", true}} {
		rows, err = a.store.db.Query(source.query, id, period)
		if err != nil {
			failure(w, 500, "读取节点用量失败")
			return
		}
		for rows.Next() {
			site := a.cfg.siteID()
			var node, revision string
			var rate, up, down int64
			args := []any{&node, &rate, &revision, &up, &down}
			if source.remote {
				args = append([]any{&site}, args...)
			}
			if err = rows.Scan(args...); err != nil {
				rows.Close()
				failure(w, 500, "读取节点用量失败")
				return
			}
			entries = append(entries, object{"site_id": site, "node_id": node, "rate_milli": rate, "rate_revision": revision, "upload": up, "download": down})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			failure(w, 500, "读取节点用量失败")
			return
		}
	}
	jsonResponse(w, 200, object{"user": u.User, "period_id": period, "periods": periods, "nodes": entries, "quota_used": u.QuotaUsed(), "readonly": true})
}

func (a *App) validateGroupCapability(group string) error {
	if !a.cfg.controller() {
		return nil
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return err
	}
	for _, s := range sites {
		if s.Info == nil || s.Info.Protocol >= 2 {
			continue
		}
		ns, err := a.materializeBusinessNodes(s)
		if err != nil {
			return err
		}
		for _, n := range ns {
			for _, id := range normalizeNodePolicy(n).GroupIDs {
				if id == group {
					return fmt.Errorf("请先升级业务站 %s，旧版本不支持节点组权限变更", s.Name)
				}
			}
		}
	}
	return nil
}
