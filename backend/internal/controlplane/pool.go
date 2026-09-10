package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Nodes keep a resolved copy for core rendering and backwards-compatible backups.
// The resource is authoritative whenever a node is assigned or a resource changes.
type IPResource struct {
	Node
	PoolGroup      string   `json:"pool_group,omitempty"`
	Source         string   `json:"source,omitempty"`
	SubscriptionID string   `json:"subscription_id,omitempty"`
	SourceStale    bool     `json:"source_stale,omitempty"`
	Label          string   `json:"label"`
	Notes          string   `json:"notes"`
	Revision       string   `json:"revision"`
	Reachable      bool     `json:"reachable"`
	NodeIDs        []string `json:"node_ids,omitempty"`
}

func (s *Store) pools() ([]IPResource, error) {
	rows, err := s.db.Query("SELECT doc FROM ip_pool ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IPResource{}
	for rows.Next() {
		var b []byte
		var p IPResource
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = s.vault.open(b, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) pool(id string) (IPResource, error) {
	var b []byte
	var p IPResource
	if err := s.db.QueryRow("SELECT doc FROM ip_pool WHERE id=?", id).Scan(&b); err != nil {
		return p, err
	}
	err := s.vault.open(b, &p)
	return p, err
}

// Encrypt and commit the shared resource and every affected node atomically.
func (s *Store) savePoolNodes(pools []IPResource, nodes []Node) error {
	return s.savePoolState(pools, nodes, nil)
}

func (s *Store) savePoolState(pools []IPResource, nodes []Node, remove []string) error {
	return s.saveInfrastructure(pools, nodes, nil, remove, nil)
}

// Collector membership and the corresponding member credentials change together.
func (s *Store) saveInfrastructure(pools []IPResource, nodes []Node, records []Record, removePools, removeNodes []string, restoreSites ...BusinessSite) error {
	changedSites := []BusinessSite{}
	if len(removePools) > 0 {
		sites, e := s.businessSites()
		if e != nil {
			return e
		}
		_, changedSites = pruneBusinessNodes(sites, removePools)
	}
	changedSites = append(changedSites, restoreSites...)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range pools {
		p.NodeIDs = nil
		b, e := s.vault.seal(p)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO ip_pool(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", p.ID, b); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		b, e := s.vault.seal(normalizeNodePolicy(n))
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO nodes(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", n.ID, b); err != nil {
			return err
		}
	}
	for _, r := range records {
		b, e := s.vault.seal(r.Credentials)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE users SET credentials=? WHERE id=?", b, r.ID); err != nil {
			return err
		}
	}
	for _, id := range removeNodes {
		if _, err = tx.Exec("DELETE FROM nodes WHERE id=?", id); err != nil {
			return err
		}
	}
	for _, id := range removePools {
		if _, err = tx.Exec("DELETE FROM ip_pool WHERE id=?", id); err != nil {
			return err
		}
	}
	for _, site := range changedSites {
		b, e := s.vault.seal(site)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, site.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func resourceFromNode(n Node) IPResource {
	n.DefaultDirect = false
	n.DNS = nil
	n.RealitySNI, n.RealityIP = "", ""
	n.ID, n.ExitID, n.Protocol, n.Enabled = "ip-"+digest(randomToken(8))[:10], "", "vless", true
	n.HasPassword = false
	label := n.Host
	if n.Exit == "direct" {
		label = "VPS 直连"
	}
	if n.ProbeIP == "" {
		n.Name = "待检测出口"
	}
	return IPResource{Node: n, Label: label, Revision: randomToken(12), Reachable: n.ProbeIP != "" && n.ProbeError == ""}
}

func bindPool(n Node, p IPResource) Node {
	if !sameExit(n, p.Node) {
		n.Speed = nil
	}
	n.Upstream, n.UpstreamType, n.BridgePort, n.BridgePassword = p.Upstream, p.UpstreamType, p.BridgePort, p.BridgePassword
	n.ExitID, n.Exit, n.Host, n.Port = p.ID, p.Exit, p.Host, p.Port
	n.Username, n.Password, n.HasPassword = p.Username, p.Password, false
	n.Name, n.ProbeIP, n.Country, n.CountryCode = p.Name, p.ProbeIP, p.Country, p.CountryCode
	n.ProbedAt, n.CheckedAt, n.ProbeError = p.ProbedAt, p.CheckedAt, p.ProbeError
	n.Quality = p.Quality
	if n.ProbeIP == "" {
		n.Name = "待检测 · " + strings.ToUpper(n.Protocol)
	}
	return n
}

func (p IPResource) public(nodes []Node) IPResource {
	p.Node = p.Node.public()
	p.NodeIDs = []string{}
	for _, n := range nodes {
		if n.ExitID == p.ID {
			p.NodeIDs = append(p.NodeIDs, n.ID)
		}
	}
	return p
}

func (s *Store) migratePools() error {
	nodes, err := s.nodes()
	if err != nil {
		return err
	}
	existing, err := s.pools()
	if err != nil {
		return err
	}
	pools, changed, remove := []IPResource{}, []Node{}, []string{}
	for _, p := range existing {
		if p.Exit == "direct" {
			remove = append(remove, p.ID)
		} else {
			pools = append(pools, p)
		}
	}
	for _, n := range nodes {
		if n.ExitID != "" {
			p, e := s.pool(n.ExitID)
			if e != nil || !sameExit(n, p.Node) {
				return fmt.Errorf("invalid IP pool reference for node %s", n.ID)
			}
		}
		if n.Exit == "direct" {
			if n.ExitID != "" {
				n.ExitID = ""
				changed = append(changed, n)
			}
			continue
		}
		if n.ExitID != "" {
			continue
		}
		index := -1
		for i, p := range pools {
			if sameExit(n, p.Node) {
				index = i
				break
			}
		}
		if index < 0 {
			pools = append(pools, resourceFromNode(n))
			index = len(pools) - 1
		}
		n.ExitID = pools[index].ID
		changed = append(changed, n)
	}
	if len(changed) == 0 && len(remove) == 0 {
		return nil
	}
	return s.savePoolState(pools, changed, remove)
}

func cleanPool(input IPResource, old *IPResource) IPResource {
	n := Node{Protocol: "vless", Enabled: input.Enabled, Exit: input.Exit, Host: strings.TrimSpace(input.Host), Port: input.Port, Username: input.Username, Password: input.Password, Name: "待检测出口"}
	if old != nil && n.Password == "" && input.HasPassword {
		n.Password = old.Password
	}
	if n.Exit == "direct" {
		n.Host, n.Port, n.Username, n.Password = "", 0, "", ""
	}
	p := IPResource{Node: n, Label: strings.TrimSpace(input.Label), Notes: strings.TrimSpace(input.Notes), Revision: randomToken(12)}
	if old != nil {
		p.ID = old.ID
		if sameExit(old.Node, n) {
			p.Node = bindPool(n, *old)
			p.ID, p.ExitID, p.Enabled = old.ID, "", input.Enabled
			p.Reachable = old.Reachable
			p.Speed = old.Speed
		}
	} else {
		p.ID = "ip-" + digest(randomToken(8))[:10]
	}
	if p.ProbeIP == "" {
		p.Name = "待检测出口"
	}
	return p
}

func poolBindings(nodes []Node, id string) []Node {
	bound := []Node{}
	for _, n := range nodes {
		if n.ExitID == id {
			bound = append(bound, n)
		}
	}
	return bound
}

func (a *App) listIPs(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pools, err := a.store.pools()
	nodes, e := a.store.nodes()
	if err != nil || e != nil {
		failure(w, 500, "读取 IP 池失败")
		return
	}
	for i := range pools {
		pools[i] = pools[i].public(nodes)
	}
	jsonResponse(w, 200, pools)
}

func (a *App) saveIP(w http.ResponseWriter, r *http.Request, actor Record) {
	var input IPResource
	if !decode(w, r, &input) {
		return
	}
	input.RealitySNI, input.RealityIP = "", ""
	if input.Exit != "http" && input.Exit != "socks5" && !(input.Exit == "subscription" && input.ID != "") {
		failure(w, 400, "IP 池仅保存 HTTP / SOCKS5 代理，本机直连请在节点出口中选择")
		return
	}
	a.mu.Lock()
	var old *IPResource
	if input.ID != "" {
		p, err := a.store.pool(input.ID)
		if err != nil {
			a.mu.Unlock()
			failure(w, 404, "IP 资源不存在")
			return
		}
		if p.Revision != input.Revision {
			a.mu.Unlock()
			failure(w, 409, "IP 资源已更新，请刷新后重试")
			return
		}
		if p.PoolGroup == "public" {
			a.mu.Unlock()
			failure(w, 409, "公共资源由自动采集管理，请在公共代理池调整策略")
			return
		}
		old = &p
	}
	p := cleanPool(input, old)
	if old != nil && old.Exit == "subscription" {
		p = *old
		p.Enabled, p.Label, p.Notes, p.Revision = input.Enabled, strings.TrimSpace(input.Label), strings.TrimSpace(input.Notes), randomToken(12)
	} else if input.Exit == "subscription" {
		a.mu.Unlock()
		failure(w, 400, "机场出口请通过订阅导入")
		return
	}
	nodes, err := a.store.nodes()
	a.mu.Unlock()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	if len([]rune(p.Label)) > 64 || len([]rune(p.Notes)) > 240 {
		failure(w, 400, "备注名称最多 64 字，说明最多 240 字")
		return
	}
	if err = validateNode(p.Node); err != nil {
		failure(w, 400, err.Error())
		return
	}
	if !p.Enabled && len(poolBindings(nodes, p.ID)) > 0 {
		failure(w, 409, "该 IP 仍被节点使用，请先切换这些节点的出口")
		return
	}
	var probeErr error
	if p.Enabled && !a.cfg.Dev {
		if !a.probeMu.TryLock() {
			failure(w, 409, "已有出口检测正在进行，请稍后重试")
			return
		}
		result, e := probeNode(r.Context(), p.Node)
		a.probeMu.Unlock()
		probeErr = e
		applyEgress(&p.Node, result, e)
		p.Reachable = e == nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	pools, err := a.store.pools()
	if err != nil {
		failure(w, 500, "读取 IP 池失败")
		return
	}
	if old != nil {
		current, e := a.store.pool(p.ID)
		if e != nil || current.Revision != old.Revision {
			failure(w, 409, "IP 资源已更新，请刷新后重试")
			return
		}
	} else if len(pools) >= 256 {
		failure(w, 400, "IP 池最多 256 项")
		return
	}
	for _, other := range pools {
		if other.ID != p.ID && sameExit(other.Node, p.Node) {
			failure(w, 409, "相同出口已在 IP 池中，请直接选择已有资源")
			return
		}
	}
	nodes, err = a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	bound := poolBindings(nodes, p.ID)
	if !p.Enabled && len(bound) > 0 {
		failure(w, 409, "该 IP 仍被节点使用，请先切换这些节点的出口")
		return
	}
	if probeErr != nil && len(bound) > 0 {
		failure(w, 400, "出口检测失败，已保留绑定节点的原配置："+probeErr.Error())
		return
	}
	updated := []Node{}
	for _, n := range bound {
		updated = append(updated, bindPool(n, p))
	}
	if len(bound) > 0 && !a.cfg.Dev {
		_ = a.collect()
	}
	if err = a.store.savePoolNodes([]IPResource{p}, updated); err != nil {
		failure(w, 500, "保存 IP 池失败")
		return
	}
	if len(bound) > 0 {
		if err = a.reconcile(); err != nil {
			restore := []IPResource{}
			if old != nil {
				restore = append(restore, *old)
			}
			restoreErr := a.store.savePoolNodes(restore, bound)
			for _, n := range bound {
				key := "x_nodes"
				if n.Protocol == "hy2" {
					key = "hy_nodes"
				}
				if e := a.store.setMeta(key, ""); e != nil {
					restoreErr = errors.Join(restoreErr, e)
				}
			}
			rollback := a.reconcile()
			a.status, a.syncError = "error", "IP 出口应用失败"
			if restoreErr != nil || rollback != nil {
				a.syncError += "；恢复尚未完成"
			}
			a.store.audit(actor.Username, "ip_apply_failed", p.Name)
			failure(w, 502, a.syncError+"，请检查运维状态")
			return
		}
		a.status, a.syncError, a.appliedAt = "applied", "", time.Now().Unix()
	}
	a.store.audit(actor.Username, "save_ip", p.Label+" · "+p.Name)
	jsonResponse(w, 200, p.public(updated))
}

func (a *App) deleteIP(w http.ResponseWriter, r *http.Request, actor Record) {
	id := strings.TrimPrefix(r.URL.Path, "/api/ips/")
	if _, err := a.store.pool(id); err != nil {
		failure(w, 404, "IP 资源不存在")
		return
	}
	a.changeResources(w, r, actor, "ips", batchRequest{IDs: []string{id}, Action: "delete"})
}

func (a *App) storePoolEgressLocked(snapshot IPResource, result Egress, probeErr error) (IPResource, error) {
	p, err := a.store.pool(snapshot.ID)
	if err != nil {
		return p, err
	}
	if p.Revision != snapshot.Revision || !sameExit(p.Node, snapshot.Node) || p.CheckedAt > snapshot.CheckedAt {
		return p, errors.New("IP 配置或检测结果已更新，请重新检测")
	}
	applyEgress(&p.Node, result, probeErr)
	p.Reachable = probeErr == nil
	nodes, err := a.store.nodes()
	if err != nil {
		return p, err
	}
	bound := poolBindings(nodes, p.ID)
	for i := range bound {
		bound[i] = bindPool(bound[i], p)
	}
	err = a.store.savePoolNodes([]IPResource{p}, bound)
	return p.public(bound), err
}

func (a *App) detectIP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/ips/"), "/detect")
	if !a.probeMu.TryLock() {
		failure(w, 409, "已有出口检测正在进行，请稍后重试")
		return
	}
	defer a.probeMu.Unlock()
	p, err := a.store.pool(id)
	if err != nil {
		failure(w, 404, "IP 资源不存在")
		return
	}
	result, probeErr := probeNode(r.Context(), p.Node)
	a.mu.Lock()
	updated, err := a.storePoolEgressLocked(p, result, probeErr)
	a.mu.Unlock()
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	// A failed check is still a completed management operation; persist its status.
	jsonResponse(w, 200, updated)
}

func (a *App) refreshPools(ctx context.Context) {
	if !a.probeMu.TryLock() {
		return
	}
	defer a.probeMu.Unlock()
	pools, err := a.store.pools()
	if err != nil {
		return
	}
	for _, p := range pools {
		if p.PoolGroup == "public" {
			continue
		}
		interval := time.Hour
		if p.ProbeError != "" {
			interval = 15 * time.Minute
		}
		if !p.Enabled || p.CheckedAt > 0 && time.Since(time.Unix(p.CheckedAt, 0)) < interval {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		result, probeErr := probeNode(ctx, p.Node)
		a.mu.Lock()
		_, _ = a.storePoolEgressLocked(p, result, probeErr)
		a.mu.Unlock()
	}
}
