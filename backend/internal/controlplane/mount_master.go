package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

func (a *App) mountLock(id string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return &a.mountSyncMu[h.Sum32()%64]
}
func sameMountConnection(a, b BusinessSite) bool {
	return a.Connection != nil && b.Connection != nil && a.Connection.Token == b.Connection.Token && a.Connection.InstanceID == b.Connection.InstanceID && a.Connection.URL == b.Connection.URL
}

func (a *App) fetchMountCatalog(ctx context.Context, v BusinessSite) (MountCatalog, error) {
	out, err := a.callSubsite(ctx, *v.Connection, httpapi.GatewayRequest{Method: "GET", Path: "/api/node-pool"})
	if err != nil {
		return MountCatalog{}, err
	}
	var c MountCatalog
	if json.Unmarshal(out.Body, &c) != nil || len(c.Revision) != 64 || len(c.Nodes) > 1024 || !validBusinessInfo(c.Info) {
		return c, errors.New("子站节点目录无效，请升级子站")
	}
	seen := map[string]bool{}
	for _, n := range c.Nodes {
		if n.ID == "" || len(n.ID) > 100 || seen[n.ID] || n.PolicyVersion != 1 || n.RateMilli < 0 || n.RateMilli > 100000 || len(n.RateRevision) > 100 || n.Protocol != "vless" && n.Protocol != "hy2" {
			return c, errors.New("子站节点目录无效")
		}
		seen[n.ID] = true
	}
	return c, nil
}
func (a *App) mountSiteAPI(w http.ResponseWriter, r *http.Request, actor Record, v BusinessSite) {
	if v.Removed && r.Method != "POST" || v.Connection.Scope != "manage" {
		failure(w, 403, "子站连接不可用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	lock := a.mountLock(v.ID)
	lock.Lock()
	defer lock.Unlock()
	if r.Method == "GET" {
		catalog, err := a.fetchMountCatalog(ctx, v)
		if err != nil {
			failure(w, 502, err.Error())
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		current, err := a.store.businessSite(v.ID)
		if err != nil || !sameMountConnection(current, v) || current.Removed {
			failure(w, 409, "子站连接已更改，请刷新")
			return
		}
		if current.Mount == nil {
			current.Mount = &SiteMount{Nodes: []MountedNode{}, Assignment: "groups"}
		}
		if current.Mount.Catalog.Revision != catalog.Revision {
			current.Mount.Accounts = nil
			current.Mount.LeaseUntil = 0
		}
		current.Mount.Catalog = catalog
		if err = a.store.saveBusinessSite(current); err != nil {
			failure(w, 500, "保存节点目录失败")
			return
		}
		jsonResponse(w, 200, current.public())
		return
	}
	if r.Method == "PUT" {
		var in struct {
			Assignment      string          `json:"assignment"`
			Revision        string          `json:"revision"`
			CatalogRevision string          `json:"catalog_revision"`
			Nodes           []MountedNode   `json:"nodes"`
			Grants          []BusinessGrant `json:"grants"`
		}
		if !decodeBusiness(w, r, &in) {
			return
		}
		a.mu.Lock()
		current, err := a.store.businessSite(v.ID)
		if err != nil || current.Mount == nil || !sameMountConnection(current, v) || current.Removed || current.Revision != in.Revision || current.Mount.Catalog.Revision != in.CatalogRevision {
			a.mu.Unlock()
			failure(w, 409, "节点池已变化，请刷新后重试")
			return
		}
		previousGrants := append([]BusinessGrant{}, current.Grants...)
		current.Mount.Assignment = in.Assignment
		current.Mount.Nodes = in.Nodes
		current.Grants = in.Grants
		if len(in.Nodes) == 0 {
			current.Grants = nil
		}
		if err = a.validateMountPolicy(&current); err != nil {
			a.mu.Unlock()
			failure(w, 400, err.Error())
			return
		}
		for i, g := range current.Grants {
			for _, old := range previousGrants {
				if old.UserID == g.UserID && old.Quota == g.Quota {
					current.Grants[i].Budget = old.Budget
				}
			}
		}
		current.Revision = randomToken(12)
		current.Mount.Accounts = nil
		current.Mount.LeaseUntil = 0
		current.Mount.Error = "等待授权或撤销确认"
		err = a.store.saveBusinessSite(current)
		if err == nil {
			a.status = "pending"
			a.store.audit(actor.Username, "mount-policy", v.ID)
		}
		a.mu.Unlock()
		if err != nil {
			failure(w, 500, "保存挂载策略失败")
			return
		}
	} else if r.Method != "POST" {
		failure(w, 405, "方法不支持")
		return
	}
	// Policy is durable even when the child is offline. A pending response must
	// not invite the browser to create a duplicate mount or claim revocation.
	err := a.syncMountSiteLocked(ctx, v.ID)
	a.mu.Lock()
	current, e := a.store.businessSite(v.ID)
	a.mu.Unlock()
	if e != nil {
		failure(w, 500, "读取挂载状态失败")
		return
	}
	if err != nil {
		jsonResponse(w, 202, current.public())
		return
	}
	jsonResponse(w, 200, current.public())
}
func (a *App) validateMountPolicy(v *BusinessSite) error {
	if v.Mount.Assignment != "" && v.Mount.Assignment != "manual" && v.Mount.Assignment != "groups" {
		return errors.New("节点池分配模式无效")
	}
	if v.Mount.Assignment == "groups" {
		v.Grants = nil
	}
	if len(v.Mount.Nodes) > 64 || len(v.Grants) > 256 || len(v.Mount.Nodes)*len(v.Grants) > 4096 {
		return errors.New("每站最多挂载 64 个节点、256 个用户，节点与用户组合不超过 4096")
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
	for i := range v.Mount.Nodes {
		n := &v.Mount.Nodes[i]
		n.Name = strings.TrimSpace(n.Name)
		if _, ok := mountSource(v.Mount.Catalog, n.NodeID); !ok || seen[n.NodeID] || utf8.RuneCountInString(n.Name) > 64 {
			return errors.New("来源节点不存在、重复或别名过长")
		}
		seen[n.NodeID] = true
		gs := map[string]bool{}
		for _, id := range n.GroupIDs {
			if !known[id] || gs[id] {
				return errors.New("主站节点组不存在或重复")
			}
			gs[id] = true
		}
		sort.Strings(n.GroupIDs)
	}
	sort.Slice(v.Mount.Nodes, func(i, j int) bool { return v.Mount.Nodes[i].NodeID < v.Mount.Nodes[j].NodeID })
	if v.Mount.Assignment == "groups" {
		if err := a.refreshMountGrants(v); err != nil {
			return err
		}
		return a.validateBusinessAllocations(*v)
	}
	previous, err := a.store.businessSite(v.ID)
	if err != nil {
		return err
	}
	previousGrants := map[int64]BusinessGrant{}
	for _, g := range previous.Grants {
		previousGrants[g.UserID] = g
	}
	users := map[int64]bool{}
	for i := range v.Grants {
		g := &v.Grants[i]
		u, err := a.store.record(g.UserID)
		if err != nil || u.Mount != nil || u.Archived || users[g.UserID] || g.Budget < 0 || g.Budget > domain.CounterLimit {
			return errors.New("用户或站点额度无效")
		}
		users[g.UserID] = true
		if u.Meter == nil {
			return errors.New("用户配额周期尚未初始化")
		}
		g.PeriodID = u.Meter.PeriodID
		if old, ok := previousGrants[g.UserID]; ok && g.Quota > 0 && g.Quota == old.Quota {
			g.Budget = old.Budget
			continue
		}
		g.Quota = 0
		if g.Budget > 0 {
			g.Quota, err = domain.AddCounter(v.Usage[g.UserID].total(), g.Budget)
			if err != nil {
				return err
			}
		} else if u.Quota > 0 {
			return errors.New("有限配额用户必须设置子站额度")
		}
	}
	return a.validateBusinessAllocations(*v)
}
func (a *App) syncMountSite(ctx context.Context, id string) error {
	lock := a.mountLock(id)
	lock.Lock()
	defer lock.Unlock()
	return a.syncMountSiteLocked(ctx, id)
}
func (a *App) syncMountSiteLocked(ctx context.Context, id string) (resultErr error) {
	a.mu.Lock()
	v, err := a.store.businessSite(id)
	a.mu.Unlock()
	if err != nil || v.Connection == nil || v.Mount == nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			a.mu.Lock()
			defer a.mu.Unlock()
			c, e := a.store.businessSite(id)
			if e == nil && sameMountConnection(c, v) && c.Mount != nil {
				c.Mount.Error = resultErr.Error()
				_ = a.store.saveBusinessSite(c)
			}
		}
	}()
	catalog, err := a.fetchMountCatalog(ctx, v)
	if err != nil {
		return err
	}
	a.mu.Lock()
	current, err := a.store.businessSite(id)
	if err != nil || !sameMountConnection(current, v) {
		a.mu.Unlock()
		return errors.New("子站连接已变化")
	}
	v = current
	if v.Mount.Catalog.Revision != catalog.Revision {
		v.Mount.Accounts = nil
		v.Mount.LeaseUntil = 0
	}
	v.Mount.Catalog = catalog
	if err = a.refreshMountGrants(&v); err != nil {
		a.mu.Unlock()
		return err
	}
	users, err := a.store.records()
	if err == nil {
		err = a.store.resolveAccess(users)
	}
	masterID, e := a.store.siteInstanceID()
	if err == nil {
		err = e
	}
	if err != nil {
		a.mu.Unlock()
		return err
	}
	request := MountSync{UsageFloor: v.Usage, UsageAck: v.Mount.UsageAck, MasterID: masterID, Sequence: v.Mount.Sequence + 1, CatalogRevision: catalog.Revision, Users: []MountUser{}}
	grants := []BusinessGrant{}
	for _, u := range users {
		if u.Mount != nil || !u.Active() || !v.Enabled || v.Removed || catalog.Paused || !a.businessOwnerEnabled(v) {
			continue
		}
		for _, g := range v.Grants {
			if g.UserID != u.ID || g.Quota > 0 && v.Usage[u.ID].total() >= g.Quota {
				continue
			}
			nodes := []string{}
			for _, m := range v.Mount.Nodes {
				n, ok := mountSource(catalog, m.NodeID)
				if !ok || !m.Enabled || !n.Enabled {
					continue
				}
				n = mountNodePolicy(m, n)
				if nodeGroupAllowed(u, n) && (n.Protocol == "vless" && u.VLESS || n.Protocol == "hy2" && u.HY2) {
					nodes = append(nodes, n.ID)
				}
			}
			if len(nodes) == 0 {
				continue
			}
			sort.Strings(nodes)
			request.Users = append(request.Users, MountUser{UserID: u.ID, NodeIDs: nodes, Generation: mountGeneration(u), PeriodID: u.Meter.PeriodID, Quota: g.Quota, Expires: u.Expires, VLESS: u.VLESS, HY2: u.HY2})
			grants = append(grants, g)
			v.PeriodRules[businessUsageKey(u.ID)+"/"+u.Meter.PeriodID] = true
		}
	}
	// Reserve before sending: even a lost response may have authorized traffic.
	v.SentGrants = grants
	v.SentNodes = nil
	for _, g := range grants {
		if g.Quota == 0 {
			v.IssuedUnlimited[g.UserID] = true
		}
		v.Issued[g.UserID] = max(v.Issued[g.UserID], g.Quota)
	}
	v.Mount.Sequence = request.Sequence
	v.Desired = fmt.Sprint(request.Sequence)
	err = a.store.saveBusinessSite(v)
	// A newly reserved budget must be removed from the local core first.
	if err == nil {
		err = a.reconcileIfNeeded()
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(request)
	response, err := a.callSubsite(ctx, *v.Connection, httpapi.GatewayRequest{Method: "POST", Path: "/api/node-mounts/sync", Body: body})
	if err != nil {
		return err
	}
	var out MountResult
	if json.Unmarshal(response.Body, &out) != nil || out.Sequence != request.Sequence || out.LeaseUntil > time.Now().Unix()+mountLeaseSeconds+30 || len(out.Accounts) > 256 {
		return errors.New("挂载授权确认无效")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err = a.store.businessSite(id)
	if err != nil || !sameMountConnection(current, v) || current.Revision != v.Revision || current.Mount.Sequence != request.Sequence {
		return errors.New("挂载策略已变化，等待下次同步")
	}
	v = current
	// The child owns rates. Freeze every reported revision, then independently
	// verify weighted cumulative totals and monotonic per-node watermarks.
	if len(out.NodeUsage) > 8192 {
		return errors.New("挂载计量明细过大")
	}
	for _, row := range out.NodeUsage {
		key := row.NodeID + "/" + row.RateRevision
		if row.NodeID == "" || len(row.NodeID) > 100 || row.RateRevision == "" || len(row.RateRevision) > 100 || row.RateMilli < 0 || row.RateMilli > 100000 || row.RateMilli%10 != 0 {
			return errors.New("子站计量倍率无效")
		}
		if old, ok := v.RateRules[key]; ok && old != row.RateMilli {
			return errors.New("子站修改了已结算的倍率版本")
		}
		v.RateRules[key] = row.RateMilli
	}
	baseline := map[int64]BusinessUsage{}
	for id := range out.Usage {
		baseline[id] = BusinessUsage{}
	}
	expected := map[int64]MountUser{}
	for _, u := range request.Users {
		expected[u.UserID] = u
	}
	seen := map[int64]bool{}
	for _, account := range out.Accounts {
		u, ok := expected[account.UserID]
		if !ok || seen[account.UserID] || account.Generation != u.Generation || len(account.Credentials.HY2) < 24 {
			return errors.New("子站返回未授权用户")
		}
		seen[account.UserID] = true
		for _, node := range account.NodeIDs {
			found := false
			for _, id := range u.NodeIDs {
				found = found || id == node
			}
			if !found {
				return errors.New("子站返回未授权节点")
			}
		}
	}
	v.Mount.UsageAck = out.NodeUsage
	v.Mount.Accounts = out.Accounts
	v.Mount.LeaseUntil = min(out.LeaseUntil, time.Now().Unix()+mountLeaseSeconds)
	v.Mount.LastSync = time.Now().Unix()
	v.Mount.Error = ""
	v.LeaseUntil = v.Mount.LeaseUntil
	v.LastSeen = v.Mount.LastSync
	if err = a.acceptBusinessUsage(&v, BusinessHeartbeat{Protocol: 2, Applied: fmt.Sprint(request.Sequence), Usage: out.Usage, NodeUsage: out.NodeUsage, UsageBaseline: baseline}); err != nil {
		return err
	}
	a.status = "pending"
	return nil
}
func (a *App) mountLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	retiredCursor := 0
	for {
		a.mu.Lock()
		sites, err := a.store.businessSites()
		a.mu.Unlock()
		if err == nil {
			queue := make(chan string, len(sites))
			retired := []string{}
			for _, s := range sites {
				if s.Connection == nil || s.Mount == nil {
					continue
				}
				if !s.Removed {
					queue <- s.ID
					continue
				}
				if len(s.Issued) > 0 || len(s.IssuedUnlimited) > 0 || len(s.SentGrants) > 0 {
					retired = append(retired, s.ID)
				}
			}
			// Historic offline ledgers must not starve live lease renewals.
			count := min(4, len(retired))
			for i := 0; i < count; i++ {
				queue <- retired[(retiredCursor+i)%len(retired)]
			}
			if len(retired) > 0 {
				retiredCursor = (retiredCursor + count) % len(retired)
			}
			close(queue)
			var workers sync.WaitGroup
			for range 4 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for id := range queue {
						if ctx.Err() != nil {
							return
						}
						call, cancel := context.WithTimeout(ctx, 15*time.Second)
						_ = a.syncMountSite(call, id)
						cancel()
					}
				}()
			}
			workers.Wait()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
