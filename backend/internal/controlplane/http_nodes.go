package controlplane

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) saveNode(w http.ResponseWriter, r *http.Request, actor Record) {
	var n Node
	if !decode(w, r, &n) {
		return
	}
	if n.SaveRequestID != "" && !validNodeSaveRequestID(n.SaveRequestID) {
		failure(w, 400, "节点保存标识无效")
		return
	}
	// These creation receipts are server-owned and survive subsequent edits.
	n.CreateRequestID, n.CreateRequestHash = "", ""
	requestHash := digest(strconv.FormatInt(actor.ID, 10) + ":" + string(jsonBytes(n)))
	// Protection is server-owned. Reject forbidden changes before DNS/TLS work,
	// then check again against the current row after acquiring the write lock.
	n.DefaultDirect = false
	a.mu.Lock()
	snapshot, snapshotErr := a.store.nodes()
	currentOwner, ownerErr := a.store.record(actor.ID)
	a.mu.Unlock()
	if ownerErr != nil || !currentOwner.Enabled || currentOwner.Role != "owner" {
		failure(w, 403, "管理员权限已变更，请重新登录")
		return
	}
	if snapshotErr != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	if replayNodeCreate(w, snapshot, n, requestHash) {
		return
	}
	for _, previous := range snapshot {
		if previous.ID == n.ID {
			if err := validateDefaultDirectEdit(previous, n); err != nil {
				failure(w, 409, err.Error())
				return
			}
		}
	}
	if n.Protocol != "vless" && strings.TrimSpace(n.RealitySNI) != "" {
		failure(w, 400, "自定义 SNI 仅适用于 VLESS Reality 节点")
		return
	}
	var err error
	n.RealitySNI, err = normalizeRealitySNI(n.RealitySNI, a.cfg)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	// This address is always derived by the server, never accepted from JSON.
	n.RealityIP = ""
	if n.RealitySNI != "" {
		a.mu.Lock()
		snapshot, readErr := a.store.nodes()
		a.mu.Unlock()
		if readErr != nil {
			failure(w, 500, "读取节点失败")
			return
		}
		n.RealityIP, err = resolveRealityTarget(r.Context(), a.cfg, snapshot, n.RealitySNI, net.DefaultResolver.LookupIPAddr, checkRealityTLS)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		if !a.cfg.Dev {
			if !a.realityProbeMu.TryLock() {
				failure(w, 409, "已有 SNI 验证正在运行，请稍后重试")
				return
			}
			err = probeRealityCore(r.Context(), a.cfg, n.RealitySNI, n.RealityIP)
			a.realityProbeMu.Unlock()
			if err != nil {
				failure(w, 400, err.Error())
				return
			}
		}
	}
	n.ManagedBy, n.Quality = "", nil
	a.mu.Lock()
	defer a.mu.Unlock()
	currentActor, actorErr := a.store.record(actor.ID)
	if actorErr != nil || !currentActor.Enabled || currentActor.Role != "owner" {
		failure(w, 403, "管理员权限已变更，请重新登录")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	// Another request with this key may have finished during the network probes.
	if replayNodeCreate(w, nodes, n, requestHash) {
		return
	}
	var old *Node
	for _, x := range nodes {
		if x.ID == n.ID {
			if x.ManagedBy == publicManager {
				failure(w, 409, "自动节点由公共代理池管理，请调整采集策略")
				return
			}
			v := x
			old = &v
			if err := validateDefaultDirectEdit(x, n); err != nil {
				failure(w, 409, err.Error())
				return
			}
			n.DefaultDirect = x.DefaultDirect
			n.CreateRequestID, n.CreateRequestHash = x.CreateRequestID, x.CreateRequestHash
			if n.Protocol != x.Protocol {
				failure(w, 400, "节点协议不能变更")
				return
			}
			if n.Password == "" && n.HasPassword {
				n.Password = x.Password
			}
			// Node-square publication is an administrator catalog setting. Node
			// edits come from older forms that do not carry it, so preserve the
			// existing value while changing transport or health settings.
			n.SquareVisible = x.SquareVisible
		}
	}
	if err = a.store.validateNodePolicy(&n, old); err != nil {
		failure(w, 400, err.Error())
		return
	}
	if old == nil {
		if n.ID != "" || (n.Protocol != "vless" && n.Protocol != "hy2") || len(nodes) >= 16 {
			failure(w, 400, "支持新增 VLESS / HY2，最多共 16 个节点")
			return
		}
		n.ID = n.Protocol + "-" + digest(randomToken(8))[:8]
		if n.SaveRequestID != "" {
			n.CreateRequestID, n.CreateRequestHash = n.SaveRequestID, requestHash
		}
	}
	if n.ExitID == "" {
		n.Upstream, n.UpstreamType, n.BridgePort, n.BridgePassword = nil, "", 0, ""
	}
	if n.ExitID != "" {
		if err = a.checkExclusiveBusinessExit(n.ExitID); err != nil {
			failure(w, 409, err.Error())
			return
		}
		p, e := a.store.pool(n.ExitID)
		if e != nil || !p.Enabled {
			failure(w, 400, "请选择已启用的 IP 池资源")
			return
		}
		if p.PoolGroup == "public" {
			failure(w, 400, "公共出口请使用自动生成的节点")
			return
		}
		n = bindPool(n, p)
	}
	if n.Exit == "" && n.ExitID == "" {
		n.Exit = "direct"
	}
	if n.Exit == "subscription" && n.ExitID == "" {
		failure(w, 400, "机场出口必须从 IP 池中选择")
		return
	}
	if n.Exit == "direct" {
		n.Upstream, n.UpstreamType, n.BridgePort, n.BridgePassword = nil, "", 0, ""
		n.Host = ""
		n.Port = 0
		n.Username = ""
		n.Password = ""
	}
	if err = validateNode(n); err != nil {
		failure(w, 400, err.Error())
		return
	}
	if n.DNS == nil {
		if old != nil {
			n.DNS = old.DNS
		} else {
			n.DNS = defaultNodeDNS()
		}
	}
	if err = validateNodeDNS(n.DNS); err != nil {
		failure(w, 400, err.Error())
		return
	}
	n.Speed = nil
	if old != nil && sameExit(*old, n) {
		n.Speed = old.Speed
		n.Quality = old.Quality
	}
	// Detection metadata is server-owned, including in local development mode.
	n.Name = "待检测 · " + strings.ToUpper(n.Protocol)
	n.ProbeIP, n.Country, n.CountryCode, n.ProbeError = "", "", "", ""
	n.ProbedAt, n.CheckedAt = 0, 0
	if old != nil && sameExit(*old, n) {
		n.Name, n.ProbeIP, n.Country, n.CountryCode = old.Name, old.ProbeIP, old.Country, old.CountryCode
		n.ProbedAt, n.CheckedAt, n.ProbeError = old.ProbedAt, old.CheckedAt, old.ProbeError
	}
	if n.ExitID != "" {
		p, _ := a.store.pool(n.ExitID)
		n = bindPool(n, p)
	}
	if !a.cfg.Dev {
		result, e := probeNode(r.Context(), n)
		if e != nil {
			failure(w, 400, e.Error())
			return
		}
		applyEgress(&n, result, nil)
	}
	if n.Exit == "direct" && n.Quality == nil {
		if q := a.reusableQuality(n); q != nil {
			n.Quality = q
			applyQualityIdentity(&n, *q)
		}
	}
	// Direct is a built-in choice. Only proxy exits are adopted into the pool.
	shared, savePools := []Node{n}, []IPResource{}
	if n.Exit != "direct" {
		pools, e := a.store.pools()
		if e != nil {
			failure(w, 500, "读取 IP 池失败")
			return
		}
		var selected *IPResource
		for _, p := range pools {
			if n.ExitID == p.ID || n.ExitID == "" && sameExit(n, p.Node) {
				copy := p
				selected = &copy
				break
			}
		}
		if selected == nil {
			if len(pools) >= 256 {
				failure(w, 400, "IP 池最多 256 项")
				return
			}
			p := resourceFromNode(n)
			selected = &p
		}
		if err = a.checkExclusiveBusinessExit(selected.ID); err != nil {
			failure(w, 409, err.Error())
			return
		}
		if !selected.Enabled {
			failure(w, 400, "该 IP 已停用，请在 IP 管理中启用")
			return
		}
		selected.Name, selected.ProbeIP, selected.Country, selected.CountryCode = n.Name, n.ProbeIP, n.Country, n.CountryCode
		if selected.ProbeIP == "" {
			selected.Name = "待检测出口"
		}
		selected.ProbedAt, selected.CheckedAt, selected.ProbeError = n.ProbedAt, n.CheckedAt, n.ProbeError
		if !a.cfg.Dev {
			selected.Reachable = true
		}
		n = bindPool(n, *selected)
		shared = poolBindings(nodes, selected.ID)
		for i := range shared {
			shared[i] = bindPool(shared[i], *selected)
		}
		shared = append(shared, n)
		savePools = append(savePools, *selected)
	}
	if !a.cfg.Dev {
		if err = a.collect(); err != nil {
			failure(w, 502, "流量同步未完成，节点设置未改变")
			return
		}
	}
	// A hostname has one Nginx destination and one Reality handshake target.
	// Keep its pinned address consistent across nodes in the same transaction.
	realityBefore := []Node{}
	if n.RealitySNI != "" {
		for _, other := range nodes {
			if other.ID == n.ID || other.RealitySNI != n.RealitySNI || other.RealityIP == n.RealityIP {
				continue
			}
			realityBefore = append(realityBefore, other)
			found := false
			for i := range shared {
				if shared[i].ID == other.ID {
					shared[i].RealityIP = n.RealityIP
					found = true
				}
			}
			if !found {
				other.RealityIP = n.RealityIP
				shared = append(shared, other)
			}
		}
	}
	var changedRecords []Record
	if old == nil && n.Protocol == "vless" {
		records, e := a.store.records()
		if e != nil {
			failure(w, 500, "读取用户失败")
			return
		}
		for _, record := range records {
			record.Credentials.VLESS[n.ID] = uuid()
			changedRecords = append(changedRecords, record)
		}
	}
	// A receipt must never describe a partially committed VLESS node.
	if err = a.store.saveInfrastructure(savePools, shared, changedRecords, nil, nil); err != nil {
		failure(w, 500, "保存节点失败")
		return
	}
	a.status = "pending"
	if err = a.reconcile(); err != nil {
		for _, previous := range realityBefore {
			_ = a.store.saveNode(previous)
		}
		if old != nil {
			_ = a.store.saveNode(*old)
		} else {
			_, _ = a.store.db.Exec("DELETE FROM nodes WHERE id=?", n.ID)
		}
		_ = a.store.setMeta("x_nodes", "")
		_ = a.store.setMeta("hy_nodes", "")
		rollback := a.reconcile()
		a.syncError = err.Error()
		a.status = "error"
		if rollback != nil {
			a.syncError += "; rollback pending"
		}
		a.store.audit(actor.Username, "node_apply_failed", n.Name)
		failure(w, 502, "核心应用失败，已恢复原节点设置；请检查运行状态")
		return
	}
	a.status = "applied"
	a.syncError = ""
	a.appliedAt = time.Now().Unix()
	a.store.audit(actor.Username, "save_node", n.Name)
	jsonResponse(w, 200, n.public())
}
func (a *App) deleteNode(w http.ResponseWriter, r *http.Request, actor Record) {
	id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	a.mu.Lock()
	defer a.mu.Unlock()
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取失败")
		return
	}
	var old *Node
	counts := map[string]int{}
	for _, n := range nodes {
		if n.ManagedBy != publicManager {
			counts[n.Protocol]++
		}
		if n.ID == id {
			v := n
			old = &v
		}
	}
	if old == nil {
		failure(w, 404, "节点不存在")
		return
	}
	if old.ManagedBy == publicManager {
		failure(w, 409, "自动节点由公共代理池管理，请调整采集策略")
		return
	}
	if old.DefaultDirect {
		failure(w, 409, defaultDirectMessage)
		return
	}
	if counts[old.Protocol] <= 1 {
		failure(w, 400, "HY2 和 VLESS 均需至少保留一个节点")
		return
	}
	if !a.cfg.Dev {
		if err = a.collect(); err != nil {
			failure(w, 502, "流量同步暂未完成，请稍后重试删除")
			return
		}
	}
	if _, err = a.store.db.Exec("DELETE FROM nodes WHERE id=?", id); err != nil {
		failure(w, 500, "删除失败")
		return
	}
	if err = a.reconcile(); err != nil {
		_ = a.store.saveNode(*old)
		_ = a.reconcile()
		failure(w, 502, "删除应用失败，已恢复节点")
		return
	}
	a.store.audit(actor.Username, "delete_node", old.Name)
	jsonResponse(w, 200, object{"ok": true})
}
