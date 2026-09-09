package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const publicManager = "public_proxy"

type PublicSource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	Protocol string `json:"protocol"`
	Kind     string `json:"kind"`
	Custom   bool   `json:"custom"`
	Address  string `json:"address"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision,omitempty"`
}

var publicSources = []PublicSource{
	{ID: "proxifly", Name: "Proxifly · 全球公开列表", URL: "https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt"},
	{ID: "monosans-http", Name: "Monosans · HTTP", URL: "https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/http.txt", Protocol: "http"},
	{ID: "monosans-socks5", Name: "Monosans · SOCKS5", URL: "https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/socks5.txt", Protocol: "socks5"},
	{ID: "speedx-http", Name: "TheSpeedX · HTTP", URL: "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/http.txt", Protocol: "http"},
	{ID: "speedx-socks5", Name: "TheSpeedX · SOCKS5", URL: "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/socks5.txt", Protocol: "socks5"},
	{ID: "clarketm-http", Name: "clarketm · HTTP", URL: "https://raw.githubusercontent.com/clarketm/proxy-list/master/proxy-list-raw.txt", Protocol: "http"},
}

type PublicSettings struct {
	Enabled         bool     `json:"enabled"`
	IntervalMinutes int      `json:"interval_minutes"`
	MaxResources    int      `json:"max_resources"`
	MaxLatencyMS    int64    `json:"max_latency_ms"`
	MinMbps         float64  `json:"min_mbps"`
	CandidateLimit  int      `json:"candidate_limit"`
	Sources         []string `json:"sources"`
	Revision        string   `json:"revision"`
}
type PublicEvent struct {
	At      int64  `json:"at"`
	Address string `json:"address"`
	Reason  string `json:"reason"`
}
type PublicStatus struct {
	Skipped       int           `json:"skipped"`
	Blacklisted   int           `json:"blacklisted"`
	TCPChecked    int           `json:"tcp_checked"`
	TCPPassed     int           `json:"tcp_passed"`
	DownloadBytes int64         `json:"download_bytes"`
	DurationMS    int64         `json:"duration_ms"`
	Running       bool          `json:"running"`
	Phase         string        `json:"phase"`
	StartedAt     int64         `json:"started_at"`
	FinishedAt    int64         `json:"finished_at"`
	NextAt        int64         `json:"next_at"`
	Discovered    int           `json:"discovered"`
	Checked       int           `json:"checked"`
	Accepted      int           `json:"accepted"`
	Added         int           `json:"added"`
	Removed       int           `json:"removed"`
	Rejected      int           `json:"rejected"`
	Cursor        int           `json:"cursor"`
	Warnings      []string      `json:"warnings"`
	Events        []PublicEvent `json:"events"`
	Error         string        `json:"error,omitempty"`
}

func defaultPublicSettings() PublicSettings {
	return PublicSettings{IntervalMinutes: 60, MaxResources: 3, MaxLatencyMS: 1500, MinMbps: 2, CandidateLimit: 80, Sources: []string{"proxifly", "monosans-http", "monosans-socks5", "speedx-http", "speedx-socks5", "clarketm-http"}}
}
func (s *Store) publicSettings() PublicSettings {
	c := defaultPublicSettings()
	_ = json.Unmarshal([]byte(s.meta("public_settings")), &c)
	return c
}
func validatePublicSettings(c PublicSettings) error {
	return validatePublicSettingsSources(c, publicSources)
}
func validatePublicSettingsSources(c PublicSettings, sources []PublicSource) error {
	if c.IntervalMinutes < 10 || c.IntervalMinutes > 1440 || c.MaxResources < 1 || c.MaxResources > 5 || c.MaxLatencyMS < 100 || c.MaxLatencyMS > 10000 || c.MinMbps < 0.1 || c.MinMbps > 1000 || c.CandidateLimit < 4 || c.CandidateLimit > 200 || c.Enabled && len(c.Sources) < 1 || len(c.Sources) > len(sources) {
		return errors.New("间隔 10–1440 分钟，保留 1–5 项，延迟 100–10000 ms，速度 0.1–1000 Mbps，每轮候选 4–200 项")
	}
	seen := map[string]bool{}
	for _, id := range c.Sources {
		found := false
		for _, s := range sources {
			if s.ID == id && (!s.Custom || s.Enabled) {
				found = true
			}
		}
		if !found || seen[id] {
			return errors.New("请选择有效且不重复的公开来源")
		}
		seen[id] = true
	}
	return nil
}
func publicIP(s string) bool {
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
func parsePublicList(b []byte, source PublicSource) []IPResource {
	result := []IPResource{}
	seen := map[string]bool{}
	if len(b) > 4<<20 {
		return result
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || len(line) > 256 {
			continue
		}
		if !strings.Contains(line, "://") {
			if source.Protocol == "" {
				continue
			}
			line = source.Protocol + "://" + line
		}
		u, err := url.Parse(line)
		if err != nil || (u.Scheme != "http" && u.Scheme != "socks5") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !publicIP(u.Hostname()) {
			continue
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		address := u.Scheme + "://" + net.JoinHostPort(net.ParseIP(u.Hostname()).String(), strconv.Itoa(port))
		if seen[address] {
			continue
		}
		seen[address] = true
		result = append(result, IPResource{Node: Node{ID: "pub-" + digest(address)[:14], Protocol: "vless", Enabled: true, Exit: u.Scheme, Host: u.Hostname(), Port: port, ManagedBy: publicManager}, PoolGroup: "public", Source: source.ID, Label: "公共 · " + strings.ToUpper(u.Scheme), Revision: digest(address)[:16]})
		if len(result) >= 20000 || source.Custom && len(result) >= maxCustomPublicEntries+1 {
			break
		}
	}
	return result
}

func (a *App) publicPoolInfo(w http.ResponseWriter, r *http.Request) {
	a.publicMu.Lock()
	defer a.publicMu.Unlock()
	c := a.store.publicSettings()
	s := a.publicStatus
	if s.StartedAt == 0 {
		_ = json.Unmarshal([]byte(a.store.meta("public_status")), &s)
		s.Running = false
	}
	if c.Enabled && !s.Running {
		s.NextAt = s.FinishedAt + int64(c.IntervalMinutes*60)
		if s.FinishedAt == 0 {
			s.NextAt = time.Now().Unix()
		}
	}
	blocks, err := a.store.publicBlocks(c)
	if err != nil {
		failure(w, 500, "无法读取黑名单")
		return
	}
	sources, err := a.store.publicSourceCatalog()
	if err != nil {
		failure(w, 500, "无法读取公开来源")
		return
	}
	jsonResponse(w, 200, object{"settings": c, "status": s, "sources": safePublicSources(sources), "source_status": a.publicSourceStates(), "blacklist": publicBlockSummary(blocks)})
}

func (a *App) savePublicSettings(w http.ResponseWriter, r *http.Request, actor Record) {
	var c PublicSettings
	if !decode(w, r, &c) {
		return
	}
	sources, err := a.store.publicSourceCatalog()
	if err != nil {
		failure(w, 500, "无法读取公开来源")
		return
	}
	if err := validatePublicSettingsSources(c, sources); err != nil {
		failure(w, 400, err.Error())
		return
	}
	a.publicMu.Lock()
	a.mu.Lock()
	old := a.store.publicSettings()
	if old.Revision != c.Revision {
		a.mu.Unlock()
		a.publicMu.Unlock()
		failure(w, 409, "策略已更新，请刷新后重试")
		return
	}
	nodes, err := a.store.nodes()
	manual := 0
	for _, n := range nodes {
		if n.ManagedBy != publicManager {
			manual++
		}
	}
	if err != nil || c.Enabled && manual+2*c.MaxResources > 16 {
		a.mu.Unlock()
		a.publicMu.Unlock()
		failure(w, 400, "节点容量不足，每个公共出口需要 2 个节点名额，总上限 16")
		return
	}
	if a.publicCancel != nil {
		a.publicCancel()
	}
	c.Revision = randomToken(12)
	b, _ := json.Marshal(c)
	err = a.store.setMeta("public_settings", string(b))
	if err == nil && !c.Enabled {
		err = a.applyPublicSetLocked(nil)
	}
	if err != nil {
		oldBytes, _ := json.Marshal(old)
		_ = a.store.setMeta("public_settings", string(oldBytes))
		a.mu.Unlock()
		a.publicMu.Unlock()
		failure(w, 502, "策略应用失败，已尝试恢复原设置，请检查运维状态")
		return
	}
	a.publicStatus = PublicStatus{Phase: "idle", Events: []PublicEvent{}, Warnings: []string{}}
	if !c.Enabled {
		a.publicStatus.Phase = "disabled"
	}
	b, _ = json.Marshal(a.publicStatus)
	_ = a.store.setMeta("public_status", string(b))
	a.store.audit(actor.Username, "public_pool_settings", fmt.Sprintf("启用 %t，保留 %d 项，间隔 %d 分钟", c.Enabled, c.MaxResources, c.IntervalMinutes))
	a.mu.Unlock()
	a.publicMu.Unlock()
	if c.Enabled {
		if a.jobs != nil {
			_ = a.enqueueSystem(r.Context(), "public-cycle", "")
		} else {
			_ = a.startPublicCycle(true)
		}
	}
	jsonResponse(w, 200, c)
}

func (a *App) runPublicPool(w http.ResponseWriter, r *http.Request) {
	if a.jobs != nil {
		if !a.store.publicSettings().Enabled {
			failure(w, 409, "请先开启公共代理自动采集")
			return
		}
		if err := a.enqueueSystem(r.Context(), "public-cycle", ""); err != nil {
			failure(w, 429, "任务队列已满，请稍后重试")
			return
		}
		jsonResponse(w, 202, object{"ok": true})
		return
	}
	if err := a.startPublicCycle(true); err != nil {
		failure(w, 409, err.Error())
		return
	}
	jsonResponse(w, 202, object{"ok": true})
}

// Caller holds the control lock; the transaction never touches manual membership.
func (a *App) applyPublicSetLocked(desired []IPResource) error {
	oldPools, err := a.store.pools()
	if err != nil {
		return err
	}
	oldNodes, err := a.store.nodes()
	if err != nil {
		return err
	}
	records, err := a.store.records()
	if err != nil {
		return err
	}
	oldRecords := make([]Record, len(records))
	copy(oldRecords, records)
	oldPublicPools, oldPublicNodes := []IPResource{}, []Node{}
	manual := 0
	for _, p := range oldPools {
		if p.PoolGroup == "public" {
			oldPublicPools = append(oldPublicPools, p)
		}
	}
	for _, n := range oldNodes {
		if n.ManagedBy == publicManager {
			oldPublicNodes = append(oldPublicNodes, n)
		} else {
			manual++
		}
	}
	if manual+2*len(desired) > 16 || len(oldPools)-len(oldPublicPools)+len(desired) > 256 {
		return errors.New("公共池容量不足")
	}
	newNodes := []Node{}
	wantPools, wantNodes := map[string]bool{}, map[string]bool{}
	for _, p := range desired {
		if p.PoolGroup != "public" || p.ManagedBy != publicManager || !p.Enabled || !p.Reachable || !publicIP(p.Host) {
			return errors.New("未通过验证的公共代理")
		}
		for _, old := range oldPools {
			if old.PoolGroup != "public" && (old.ID == p.ID || sameExit(old.Node, p.Node)) {
				return errors.New("公共代理与自有资源冲突")
			}
		}
		wantPools[p.ID] = true
		for _, proto := range []string{"vless", "hy2"} {
			// A re-discovered address gets a new node incarnation, so deleted
			// HY2 credentials cannot become valid again when that proxy returns.
			id := "auto-" + proto + "-" + strings.TrimPrefix(p.ID, "pub-") + "-" + digest(randomToken(8))[:6]
			for _, old := range oldPublicNodes {
				if old.ExitID == p.ID && old.Protocol == proto {
					id = old.ID
					break
				}
			}
			for _, old := range oldNodes {
				if old.ID == id && old.ManagedBy != publicManager {
					return errors.New("节点身份冲突")
				}
			}
			n := bindPool(Node{ID: id, Protocol: proto, Enabled: true, ManagedBy: publicManager}, p)
			n.Speed = p.Speed
			newNodes = append(newNodes, n)
			wantNodes[id] = true
		}
	}
	removePools, removeNodes := []string{}, []string{}
	for _, p := range oldPublicPools {
		if !wantPools[p.ID] {
			removePools = append(removePools, p.ID)
		}
	}
	for _, n := range oldPublicNodes {
		if !wantNodes[n.ID] {
			removeNodes = append(removeNodes, n.ID)
		}
	}
	for i := range records {
		m := map[string]string{}
		for k, v := range records[i].Credentials.VLESS {
			m[k] = v
		}
		records[i].Credentials.VLESS = m
		for _, id := range removeNodes {
			delete(m, id)
		}
		for _, n := range newNodes {
			if n.Protocol == "vless" && m[n.ID] == "" {
				m[n.ID] = uuid()
			}
		}
	}
	if !a.cfg.Dev {
		_ = a.collect()
		recordsNow, e := a.store.records()
		if e != nil {
			return e
		}
		for i := range records {
			for _, cur := range recordsNow {
				if cur.ID == records[i].ID {
					oldRecords[i] = cur
					records[i].User = cur.User
				}
			}
		}
	}
	if err = a.store.saveInfrastructure(desired, newNodes, records, removePools, removeNodes); err != nil {
		return err
	}
	if err = a.reconcile(); err != nil {
		newPoolIDs, newNodeIDs := []string{}, []string{}
		oldP, oldN := map[string]bool{}, map[string]bool{}
		for _, p := range oldPublicPools {
			oldP[p.ID] = true
		}
		for _, n := range oldPublicNodes {
			oldN[n.ID] = true
		}
		for _, p := range desired {
			if !oldP[p.ID] {
				newPoolIDs = append(newPoolIDs, p.ID)
			}
		}
		for _, n := range newNodes {
			if !oldN[n.ID] {
				newNodeIDs = append(newNodeIDs, n.ID)
			}
		}
		restore := a.store.saveInfrastructure(oldPublicPools, oldPublicNodes, oldRecords, newPoolIDs, newNodeIDs)
		_ = a.store.setMeta("x_nodes", "")
		_ = a.store.setMeta("hy_nodes", "")
		rollback := a.reconcile()
		a.status, a.syncError = "error", "公共节点应用失败"
		if restore != nil || rollback != nil {
			a.syncError += "；恢复尚未完成"
		}
		return errors.New(a.syncError)
	}
	a.status, a.syncError, a.appliedAt = "applied", "", time.Now().Unix()
	return nil
}

func (a *App) startPublicCycle(force bool) error {
	a.publicMu.Lock()
	defer a.publicMu.Unlock()
	if a.publicStopping || a.publicContext != nil && a.publicContext.Err() != nil {
		return errors.New("服务正在结束")
	}
	c := a.store.publicSettings()
	if !c.Enabled {
		return errors.New("请先开启公共代理自动采集")
	}
	if a.publicCancel != nil {
		return errors.New("已有采集任务正在执行或结束")
	}
	previous := a.publicStatus
	if previous.StartedAt == 0 {
		_ = json.Unmarshal([]byte(a.store.meta("public_status")), &previous)
	}
	if !force && previous.FinishedAt > 0 && time.Since(time.Unix(previous.FinishedAt, 0)) < time.Duration(c.IntervalMinutes)*time.Minute {
		return nil
	}
	parent := a.publicContext
	if !a.heavyMu.TryLock() {
		return errors.New("已有订阅更新、测速或质量检测正在执行，请稍后重试")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	a.publicCancel = cancel
	a.publicStatus = PublicStatus{Running: true, Phase: "baseline", StartedAt: time.Now().Unix(), Cursor: previous.Cursor, Warnings: []string{}, Events: []PublicEvent{}}
	a.publicWG.Add(1)
	go func() {
		defer a.publicWG.Done()
		defer a.heavyMu.Unlock()
		defer cancel()
		a.collectPublic(ctx, c)
		a.publicMu.Lock()
		a.publicCancel = nil
		a.publicMu.Unlock()
	}()
	return nil
}
func (a *App) stopPublic() {
	a.publicMu.Lock()
	a.publicStopping = true
	if a.publicCancel != nil {
		a.publicCancel()
	}
	a.publicMu.Unlock()
	a.publicWG.Wait()
}
func (a *App) publicLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = a.startPublicCycle(false)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type publicProbeResult struct {
	pool   IPResource
	reason string
}

type publicChecks struct {
	preflight   func(context.Context, IPResource) error
	baseline    func(context.Context) error
	fetch       func(context.Context, string) ([]byte, error)
	performance func(context.Context, IPResource, PublicSettings) publicProbeResult
	quality     func(context.Context, Node) IPQuality
}

func publicBaseline(ctx context.Context) error {
	t, _ := egressTransport(Node{Protocol: "vless", Exit: "direct", Enabled: true})
	defer t.CloseIdleConnections()
	baseline := &http.Client{Transport: t, Timeout: 8 * time.Second}
	for _, address := range []string{"https://api.ipify.org", "https://speed.cloudflare.com/__down?bytes=1024"} {
		r := qualityGet(ctx, baseline, address, 2048)
		if r.err != nil || r.code != 200 {
			return errors.New("检测目标或 VPS 网络异常，本轮未变更公共节点")
		}
	}
	return nil
}

func (a *App) collectPublic(ctx context.Context, c PublicSettings) {
	var ips []string
	var speed string
	budget := &publicDownloadBudget{}
	checks := publicChecks{preflight: publicTCP, fetch: a.fetchPublicSource}
	checks.baseline = func(ctx context.Context) error {
		var err error
		ips, speed, err = publicHealthyTargets(ctx, publicIPTargets, publicSpeedTargets)
		return err
	}
	checks.performance = func(ctx context.Context, p IPResource, c PublicSettings) publicProbeResult {
		return publicFastPerformance(ctx, p, c, ips, speed, budget)
	}
	a.collectPublicWith(ctx, c, checks)
}

func (a *App) collectPublicWith(ctx context.Context, c PublicSettings, checks publicChecks) {
	started := time.Now()
	a.publicMu.Lock()
	s := a.publicStatus
	a.publicMu.Unlock()
	progress := func(phase string) {
		s.Phase = phase
		a.publicMu.Lock()
		if a.store.publicSettings().Revision == c.Revision {
			a.publicStatus = s
		}
		a.publicMu.Unlock()
	}
	event := func(p IPResource, reason string) {
		s.Events = append(s.Events, PublicEvent{At: time.Now().Unix(), Address: net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), Reason: reason})
		if len(s.Events) > 12 {
			s.Events = s.Events[len(s.Events)-12:]
		}
	}
	defer func() {
		s.Running = false
		s.FinishedAt = time.Now().Unix()
		s.DurationMS = time.Since(started).Milliseconds()
		s.NextAt = s.FinishedAt + int64(c.IntervalMinutes*60)
		if ctx.Err() != nil {
			s.Phase = "cancelled"
			s.Error = "任务已取消或达到本轮时间上限"
		} else if s.Error != "" {
			s.Phase = "error"
		} else {
			s.Phase = "idle"
		}
		a.store.trimPublicBlocks()
		if blocks, err := a.store.publicBlocks(c); err == nil {
			s.Blacklisted = len(blocks)
		}
		a.publicMu.Lock()
		defer a.publicMu.Unlock()
		if a.store.publicSettings().Revision == c.Revision {
			a.publicStatus = s
			b, _ := json.Marshal(s)
			_ = a.store.setMeta("public_status", string(b))
		}
	}()
	if err := checks.baseline(ctx); err != nil {
		s.Error = err.Error()
		return
	}
	blocks, err := a.store.publicBlocks(c)
	if err != nil {
		s.Error = "无法读取黑名单，本轮未检测"
		return
	}
	remember := func(r publicProbeResult) {
		if r.reason == "" || publicDeferred(r.reason) || ctx.Err() != nil {
			return
		}
		b, err := a.store.blockPublic(r.pool, r.reason, c)
		if err != nil {
			s.Error = "黑名单写入失败"
			return
		}
		blocks[b.ID] = b
	}
	a.mu.Lock()
	pools, err := a.store.pools()
	nodes, e := a.store.nodes()
	a.mu.Unlock()
	if err != nil || e != nil {
		s.Error = "无法读取现有资源"
		return
	}
	manual := 0
	ownIPs := map[string]bool{}
	oldCount := 0
	for _, n := range nodes {
		if n.ManagedBy != publicManager {
			manual++
		}
		if n.Exit == "direct" && n.ProbeIP != "" {
			ownIPs[n.ProbeIP] = true
		}
	}
	for _, p := range pools {
		if p.PoolGroup == "public" {
			oldCount++
		}
	}
	capacity := max(0, min(c.MaxResources, (16-manual)/2, 256-len(pools)+oldCount))
	desired := []IPResource{}
	seen := map[string]bool{}
	// Eight cheap socket checks feed only two TLS/download workers.
	probeBatch := func(probeCtx context.Context, batch []IPResource) []publicProbeResult {
		results := make([]publicProbeResult, len(batch))
		tcpPassed := make([]bool, len(batch))
		slots := make(chan struct{}, 2)
		var wg sync.WaitGroup
		for i, p := range batch {
			wg.Add(1)
			go func(i int, p IPResource) {
				defer wg.Done()
				p.Speed = nil
				if checks.preflight != nil {
					if err := checks.preflight(probeCtx, p); err != nil {
						reason := "TCP 不通"
						if probeCtx.Err() != nil {
							reason = "检测已取消"
						}
						results[i] = publicProbeResult{p, reason}
						return
					}
					tcpPassed[i] = true
				}
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-probeCtx.Done():
					results[i] = publicProbeResult{p, "检测已取消"}
					return
				}
				results[i] = checks.performance(probeCtx, p, c)
				if probeCtx.Err() != nil {
					results[i].reason = "检测已取消"
				}
			}(i, p)
		}
		wg.Wait()
		s.Checked += len(batch)
		if checks.preflight != nil {
			s.TCPChecked += len(batch)
			for _, ok := range tcpPassed {
				if ok {
					s.TCPPassed++
				}
			}
		}
		for _, r := range results {
			if r.pool.Speed != nil {
				s.DownloadBytes += r.pool.Speed.Bytes
			}
		}
		return results
	}
	progress("recheck")
	existing := []IPResource{}
	for _, p := range pools {
		if p.PoolGroup != "public" {
			continue
		}
		seen[p.ID] = true
		if _, banned := blocks[p.ID]; banned {
			s.Skipped++
			event(p, "黑名单中，撤下自动节点")
			continue
		}
		if len(existing) >= capacity {
			event(p, "池容量缩减，撤下自动节点")
			continue
		}
		existing = append(existing, p)
	}
	for i, r := range probeBatch(ctx, existing) {
		if r.reason == "" {
			desired = append(desired, r.pool)
		} else if publicDeferred(r.reason) {
			desired = append(desired, existing[i])
		} else {
			s.Rejected++
			remember(r)
			event(r.pool, r.reason+"，已加入黑名单并撤下节点")
		}
	}
	if ctx.Err() != nil || s.Error != "" {
		return
	}
	progress("fetch")
	selected := []PublicSource{}
	catalog, err := a.store.publicSourceCatalog()
	if err != nil {
		s.Error = "无法读取公开来源，本轮未变更公共节点"
		return
	}
	for _, source := range catalog {
		for _, id := range c.Sources {
			if id == source.ID && source.Enabled {
				selected = append(selected, source)
				break
			}
		}
	}
	lists := make([][]IPResource, len(selected))
	fetchErrors := make([]error, len(selected))
	slots := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, source := range selected {
		wg.Add(1)
		go func(i int, source PublicSource) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			var b []byte
			var err error
			if source.Kind == "text" {
				err = a.store.db.QueryRow("SELECT body FROM public_source_cache WHERE id=?", source.ID).Scan(&b)
			} else {
				b, err = checks.fetch(ctx, source.URL)
			}
			fetchErrors[i] = err
			if err == nil {
				lists[i] = parsePublicList(b, source)
			}
		}(i, source)
	}
	wg.Wait()
	for i, list := range lists {
		s.Discovered += len(list)
		if fetchErrors[i] != nil {
			s.Warnings = append(s.Warnings, selected[i].Name+" 暂不可用")
		}
	}
	candidates := []IPResource{}
	allSeen := map[string]bool{}
	for i := 0; ; i++ {
		found := false
		for _, list := range lists {
			if i < len(list) {
				found = true
				p := list[i]
				if !allSeen[p.ID] {
					allSeen[p.ID] = true
					candidates = append(candidates, p)
				}
			}
		}
		if !found {
			break
		}
	}
	offset := 0
	if len(candidates) > 0 {
		offset = s.Cursor % len(candidates)
	}
	// Exclude blocks before applying the candidate budget so dead entries cost no probes.
	eligible := []IPResource{}
	for i := 0; i < len(candidates); i++ {
		p := candidates[(offset+i)%len(candidates)]
		if seen[p.ID] || ownIPs[p.Host] {
			continue
		}
		seen[p.ID] = true
		if _, banned := blocks[p.ID]; banned {
			s.Skipped++
			continue
		}
		duplicate := false
		for _, old := range pools {
			if old.PoolGroup != "public" && sameExit(old.Node, p.Node) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		eligible = append(eligible, p)
	}
	progress("screen")
	deadline := time.Now().Add(45 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Add(-30*time.Second).Before(deadline) {
		deadline = end.Add(-30 * time.Second)
	}
	scanCtx, scanCancel := context.WithDeadline(ctx, deadline)
	defer scanCancel()
	checked := 0
	for checked < len(eligible) && checked < c.CandidateLimit && len(desired) < capacity {
		if scanCtx.Err() != nil {
			s.Warnings = append(s.Warnings, "本轮筛选达到 45 秒预算，其余候选下轮继续")
			break
		}
		end := min(checked+8, len(eligible), c.CandidateLimit)
		batch := eligible[checked:end]
		results := probeBatch(scanCtx, batch)
		checked = end
		s.Cursor = offset + checked
		budgetUsed := false
		for _, r := range results {
			if publicDeferred(r.reason) {
				if r.reason == "本轮下载预算已用完" {
					budgetUsed = true
				}
				continue
			}
			if r.reason != "" {
				s.Rejected++
				remember(r)
				event(r.pool, r.reason+"，已加入黑名单")
			} else if len(desired) < capacity {
				desired = append(desired, r.pool)
				event(r.pool, "通过 HTTPS / 延迟 / 下载筛选")
			}
		}
		progress("screen")
		if s.Error != "" {
			return
		}
		if budgetUsed {
			s.Warnings = append(s.Warnings, "本轮采集测速已达到 4 MiB 样本预算")
			break
		}
	}
	if ctx.Err() != nil {
		return
	}
	progress("quality")
	for i := range desired {
		p := &desired[i]
		// Production streaming/ASN checks run later in the existing one-per-minute queue.
		if checks.quality != nil && p.Quality == nil {
			q := checks.quality(ctx, p.Node)
			p.Quality = &q
			applyQualityIdentity(&p.Node, q)
		}
		if p.CountryCode == "" {
			dt, _ := egressTransport(Node{Protocol: "vless", Exit: "direct", Enabled: true})
			page := qualityGet(ctx, &http.Client{Transport: dt, Timeout: 2 * time.Second}, "https://ipwho.is/"+p.ProbeIP+"?fields=success,ip,country_code", 4096)
			dt.CloseIdleConnections()
			var geo struct {
				Success bool   `json:"success"`
				IP      string `json:"ip"`
				Code    string `json:"country_code"`
			}
			if page.err == nil && json.Unmarshal([]byte(page.body), &geo) == nil && geo.Success && geo.IP == p.ProbeIP {
				applyQualityIdentity(&p.Node, IPQuality{IP: geo.IP, CountryCode: geo.Code})
			}
		}
	}
	s.Accepted = len(desired)
	oldIDs := map[string]bool{}
	for _, p := range pools {
		if p.PoolGroup == "public" {
			oldIDs[p.ID] = true
		}
	}
	retained := 0
	for _, p := range desired {
		if oldIDs[p.ID] {
			retained++
		} else {
			s.Added++
		}
	}
	s.Removed = oldCount - retained
	progress("apply")
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.store.publicSettings()
	if ctx.Err() != nil || !current.Enabled || current.Revision != c.Revision {
		return
	}
	if err := a.applyPublicSetLocked(desired); err != nil {
		s.Error = err.Error()
		return
	}
	a.store.audit("system", "public_pool_cycle", fmt.Sprintf("检查 %d，黑名单跳过 %d，新增 %d，剔除 %d，保留 %d，下载 %d KiB", s.Checked, s.Skipped, s.Added, s.Removed, s.Accepted, s.DownloadBytes/1024))
}
