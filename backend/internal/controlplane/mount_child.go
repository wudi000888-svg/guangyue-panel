package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"net/http"
	"sort"
	"time"
)

func (a *App) validMountToken(id string) bool {
	var owner, expires int64
	var scope string
	if a.store.db.QueryRow("SELECT actor_id,scope,expires FROM fleet_tokens WHERE id=?", id).Scan(&owner, &scope, &expires) != nil || scope != "manage" || expires != 0 && expires <= time.Now().Unix() {
		return false
	}
	r, err := a.store.record(owner)
	return err == nil && r.Enabled && r.Role == "owner"
}

// Called for every core authorization, including the HY2 authentication endpoint.
func (a *App) filterMountRecords(records []Record) error {
	shares := map[string]MountSharing{}
	valid := map[string]bool{}
	for i := range records {
		r := &records[i]
		if r.Mount == nil {
			continue
		}
		id := r.Mount.TokenID
		if _, ok := shares[id]; !ok {
			s, err := a.store.mountSharing(id)
			if err != nil {
				return err
			}
			shares[id] = s
			valid[id] = a.validMountToken(id)
		}
		r.Mount = cloneMountAccess(r.Mount)
		permitted := []string{}
		if valid[id] {
			for _, n := range r.Mount.NodeIDs {
				if shares[id].allows(n) {
					permitted = append(permitted, n)
				}
			}
		}
		r.Mount.NodeIDs = permitted
		if len(permitted) == 0 {
			r.Enabled = false
		}
	}
	return nil
}
func (a *App) childMountCatalog(tokenID string) (MountCatalog, error) {
	nodes, err := a.store.nodes()
	if err != nil {
		return MountCatalog{}, err
	}
	groups, err := a.store.nodeGroups()
	if err != nil {
		return MountCatalog{}, err
	}
	share, err := a.store.mountSharing(tokenID)
	if err != nil {
		return MountCatalog{}, err
	}
	out := MountCatalog{Info: BusinessInfo{Edition: a.cfg.edition(), SiteID: a.cfg.siteID(), Version: version, Protocol: 2, VLESSHost: a.cfg.VLESSHost, HY2Host: a.cfg.HY2Host, RealityPublic: a.cfg.RealityPublic, RealitySNI: a.cfg.RealitySNI, ShortID: a.cfg.ShortID}, Nodes: []Node{}, Groups: groups, Paused: a.store.meta("site_paused") == "true" || !share.Enabled}
	signature := []object{}
	for _, n := range nodes {
		if !share.allows(n.ID) {
			continue
		}
		v := memberNodeState(n)
		v.GroupIDs = n.GroupIDs
		v.RateRevision = n.RateRevision
		v.RealitySNI = n.RealitySNI
		v.Exit = n.Exit
		v.DefaultDirect = n.DefaultDirect
		out.Nodes = append(out.Nodes, v)
		signature = append(signature, object{"id": n.ID, "enabled": n.Enabled, "protocol": n.Protocol, "exit": n.Exit, "sni": n.RealitySNI, "rate": n.RateMilli, "rate_revision": n.RateRevision})
	}
	b, err := json.Marshal(object{"info": out.Info, "nodes": signature, "share": share, "paused": out.Paused})
	out.Revision = digest(string(b))
	return out, err
}
func (a *App) mountGateway(w http.ResponseWriter, r *http.Request, actor Record, tokenID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.validMountToken(tokenID) || actor.Role != "owner" {
		failure(w, 403, "挂载授权已撤销")
		return
	}
	if a.cfg.businessAgent() {
		failure(w, 409, "请先切换为令牌管理模式")
		return
	}
	catalog, err := a.childMountCatalog(tokenID)
	if err != nil {
		failure(w, 500, "读取子站节点池失败")
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/node-pool" {
		jsonResponse(w, 200, catalog)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/api/node-mounts/sync" {
		failure(w, 404, "接口不存在")
		return
	}
	var in MountSync
	if !decodeBusiness(w, r, &in) {
		return
	}
	out, err := a.applyMountSync(tokenID, in, catalog)
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, out)
}
func (a *App) applyMountSync(tokenID string, in MountSync, catalog MountCatalog) (MountResult, error) {
	out := MountResult{Sequence: in.Sequence, Accounts: []MountAccount{}, Usage: map[int64]BusinessUsage{}, NodeUsage: []NodeUsage{}}
	if len(in.MasterID) != 36 || !uuidPattern.MatchString(in.MasterID) || in.Sequence < 1 || len(in.Users) > 256 {
		return out, errors.New("挂载用户授权格式无效")
	}
	binding := mountBinding{}
	raw, err := a.store.readMeta("mount-binding:" + tokenID)
	if err != nil {
		return out, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &binding); err != nil {
			return out, err
		}
	}
	b, _ := json.Marshal(in)
	hash := digest(string(b))
	if binding.MasterID != "" && binding.MasterID != in.MasterID {
		return out, errors.New("此令牌已绑定另一主站，请为每个主站生成独立令牌")
	}
	if in.Sequence < binding.Sequence || in.Sequence == binding.Sequence && hash != binding.Hash {
		return out, errors.New("挂载授权版本已过期")
	}
	if len(in.Users) > 0 && in.CatalogRevision != catalog.Revision {
		return out, errors.New("子站节点目录已变化，请重新同步")
	}
	requested := map[int64]MountUser{}
	for _, u := range in.Users {
		if u.UserID <= 0 || len(u.Generation) != 64 || u.PeriodID == "" || len(u.PeriodID) > 100 || len(u.NodeIDs) > 64 || u.Quota < 0 || u.Quota > 1<<60 || u.Expires < 0 {
			return out, errors.New("挂载用户权限无效")
		}
		if _, ok := requested[u.UserID]; ok {
			return out, errors.New("挂载用户重复")
		}
		seen := map[string]bool{}
		for _, id := range u.NodeIDs {
			n, ok := mountSource(catalog, id)
			if !ok || seen[id] || !n.Enabled {
				return out, errors.New("来源节点不存在、停用或重复")
			}
			seen[id] = true
		}
		sort.Strings(u.NodeIDs)
		requested[u.UserID] = u
	}
	if err = a.collect(); err != nil {
		return out, errors.New("子站流量采样失败，请重试")
	}
	records, err := a.store.records()
	if err != nil {
		return out, err
	}
	now := time.Now().Unix()
	deadline := now + mountLeaseSeconds
	if in.Sequence == binding.Sequence {
		deadline = binding.LeaseUntil
	}
	delegated := map[int64]Record{}
	for _, r := range records {
		if r.Mount != nil && r.Mount.TokenID == tokenID {
			delegated[r.Mount.UserID] = r
		}
	}
	// Refuse fresh access after a child restored counters behind a confirmed
	// master watermark. Otherwise a restore could spend the same budget twice.
	for id, floor := range in.UsageFloor {
		r, exists := delegated[id]
		next := businessUsageOf(r.User)
		if !exists && floor.rawTotal() > 0 || next.Upload < floor.Upload || next.Download < floor.Download || next.VLESS < floor.VLESS || next.HY2 < floor.HY2 || next.total() < floor.total() {
			return out, errors.New("子站计量落后于主站确认水位，请恢复账本后重试")
		}
	}
	if len(in.UsageAck) > 8192 {
		return out, errors.New("挂载计量确认过大")
	}
	acks := make([]NodeUsage, 0, len(in.UsageAck))
	for _, row := range in.UsageAck {
		r, ok := delegated[row.UserID]
		if !ok {
			return out, errors.New("确认用户不属于此连接")
		}
		row.UserID = r.ID
		acks = append(acks, row)
	}
	if err = a.store.ackBusinessUsage(acks); err != nil {
		return out, err
	}
	// Only create new identities for this binding; never overwrite a local ID.
	for id, u := range requested {
		if _, ok := delegated[id]; !ok {
			r := Record{User: User{Username: "mnt-" + digest(tokenID + "/" + businessUsageKey(id))[:24], Role: "user", Enabled: true, Created: now, Mount: &domain.MountAccess{TokenID: tokenID, MasterID: in.MasterID, UserID: id}}, Password: []byte("!"), Credentials: Credentials{Token: randomToken(32), PublicToken: randomToken(32), HY2: randomToken(32), VLESS: map[string]string{}}}
			r.InitMeter(u.PeriodID, now)
			delegated[id] = r
		}
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for id, r := range delegated {
		r.Mount = cloneMountAccess(r.Mount)
		u, exists := requested[id]
		if !exists || catalog.Paused {
			r.Mount.NodeIDs = []string{}
			r.Mount.LeaseUntil = 0
		} else {
			if r.Meter.PeriodID != u.PeriodID {
				if len(r.Mount.NodeIDs) > 0 || r.Mount.LeaseUntil > now {
					return out, errors.New("上一配额周期尚未结算")
				}
				r.Meter.PeriodID = u.PeriodID
				r.Meter.Start = now
				r.Meter.UploadRemainder = 0
				r.Meter.DownloadRemainder = 0
			}
			if r.Mount.Generation != u.Generation {
				r.Credentials.HY2 = randomToken(32)
				r.Credentials.VLESS = map[string]string{}
				r.Credentials.HYGeneration++
				r.Mount.Generation = u.Generation
			}
			r.Mount.LeaseUntil = deadline
			r.Mount.Revision = in.Sequence
			r.Mount.NodeIDs = append([]string{}, u.NodeIDs...)
			r.Expires = u.Expires
			r.Quota = u.Quota
			r.VLESS = u.VLESS
			r.HY2 = u.HY2
			for _, id := range u.NodeIDs {
				n, _ := mountSource(catalog, id)
				if n.Protocol == "vless" && r.Credentials.VLESS[id] == "" {
					r.Credentials.VLESS[id] = uuid()
				}
			}
		}
		doc, e := json.Marshal(r.User)
		if e != nil {
			return out, e
		}
		credentials, e := a.store.vault.seal(r.Credentials)
		if e != nil {
			return out, e
		}
		if r.ID == 0 {
			err = tx.QueryRow("INSERT INTO users(username,doc,credentials,password,token_hash) VALUES(?,?,?,?,?) RETURNING id", r.Username, doc, credentials, r.Password, digest(r.Credentials.Token)).Scan(&r.ID)
		} else {
			_, err = tx.Exec("UPDATE users SET doc=?,credentials=? WHERE id=?", doc, credentials, r.ID)
		}
		if err != nil {
			return out, err
		}
	}
	binding = mountBinding{MasterID: in.MasterID, Sequence: in.Sequence, Hash: hash, LeaseUntil: deadline}
	b, _ = json.Marshal(binding)
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "mount-binding:"+tokenID, string(b)); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	a.status = "pending"
	if err = a.reconcileIfNeeded(); err != nil {
		return out, errors.New("子站挂载授权尚未应用，请检查核心状态")
	}
	if err = a.settleHYRevocations(); err != nil {
		return out, errors.New("旧挂载连接尚未撤销，请重试")
	}
	// Revocation can sample final traffic. Read again before acknowledging budgets.
	records, err = a.store.records()
	if err != nil {
		return out, err
	}
	mapped := map[int64]int64{}
	for _, r := range records {
		if r.Mount == nil || r.Mount.TokenID != tokenID {
			continue
		}
		id := r.Mount.UserID
		mapped[r.ID] = id
		out.Usage[id] = businessUsageOf(r.User)
		if !r.Active() || catalog.Paused || len(r.Mount.NodeIDs) == 0 {
			continue
		}
		credentials := Credentials{HY2: r.Credentials.HY2, VLESS: map[string]string{}}
		for _, n := range r.Mount.NodeIDs {
			if value := r.Credentials.VLESS[n]; value != "" {
				credentials.VLESS[n] = value
			}
		}
		out.Accounts = append(out.Accounts, MountAccount{UserID: id, NodeIDs: r.Mount.NodeIDs, Generation: r.Mount.Generation, Credentials: credentials})
	}
	rows, err := a.store.pendingNodeUsage()
	if err != nil {
		return out, err
	}
	for _, v := range rows {
		if id, ok := mapped[v.UserID]; ok {
			v.UserID = id
			out.NodeUsage = append(out.NodeUsage, v)
		}
	}
	if len(out.NodeUsage) > 8192 {
		return out, fmt.Errorf("挂载流量历史超过单次同步限制")
	}
	out.LeaseUntil = deadline
	return out, nil
}
