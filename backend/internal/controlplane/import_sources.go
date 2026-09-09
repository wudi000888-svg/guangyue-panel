package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type ImportSource struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	URL         string          `json:"url,omitempty"`
	Address     string          `json:"address"`
	Enabled     bool            `json:"enabled"`
	Queued      bool            `json:"queued"`
	Revision    string          `json:"revision"`
	Created     int64           `json:"created"`
	LastAttempt int64           `json:"last_attempt"`
	LastSuccess int64           `json:"last_success"`
	NextAt      int64           `json:"next_at"`
	Failures    int             `json:"failures"`
	Error       string          `json:"error"`
	ResourceIDs []string        `json:"resource_ids"`
	Added       int             `json:"added"`
	Removed     int             `json:"removed"`
	Retained    int             `json:"retained"`
	Warnings    []importWarning `json:"warnings"`
}

func (s *Store) importSources() ([]ImportSource, error) {
	rows, err := s.db.Query("SELECT doc FROM import_sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportSource{}
	for rows.Next() {
		var b []byte
		var v ImportSource
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = s.vault.open(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) importSource(id string) (ImportSource, error) {
	var b []byte
	var v ImportSource
	err := s.db.QueryRow("SELECT doc FROM import_sources WHERE id=?", id).Scan(&b)
	if err == nil {
		err = s.vault.open(b, &v)
	}
	return v, err
}
func (s *Store) saveImportSource(v ImportSource) error {
	b, e := s.vault.seal(v)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("INSERT INTO import_sources(id,url_hash,doc) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET url_hash=excluded.url_hash,doc=excluded.doc", v.ID, digest(v.URL), b)
	return e
}
func validateSourceURL(address string) error {
	u, e := url.Parse(address)
	if e != nil || len(address) > 8192 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("请填写有效的 HTTPS 订阅链接")
	}
	return nil
}
func sourceAddress(address string) string {
	u, e := url.Parse(address)
	if e != nil {
		return ""
	}
	return "https://" + u.Host + "/••••"
}

// Assign a stable minute of day instead of scheduling every source at midnight.
func sourceNextAt(id string, now int64) int64 {
	v, _ := strconv.ParseUint(digest(id)[:8], 16, 64)
	slot := int64(v%1440) * 60
	at := (now/86400)*86400 + slot
	if at <= now {
		at += 86400
	}
	return at
}
func sourceFailureNext(id string, failures int, now int64) int64 {
	if failures > 5 {
		failures = 5
	}
	if failures < 1 {
		failures = 1
	}
	delay := int64(3600) << (failures - 1)
	v, _ := strconv.ParseUint(digest(id)[:4], 16, 64)
	return now + delay + int64(v%600)
}

type sourceInput struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}

func (a *App) createSource(input sourceInput) (ImportSource, bool, error) {
	address := cleanShareText(strings.TrimSpace(input.URL))
	if e := validateSourceURL(address); e != nil {
		return ImportSource{}, false, e
	}
	input.Name = strings.TrimSpace(input.Name)
	if utf8.RuneCountInString(input.Name) > 80 {
		return ImportSource{}, false, errors.New("订阅名称最长 80 个字符")
	}
	sources, e := a.store.importSources()
	if e != nil {
		return ImportSource{}, false, e
	}
	for _, s := range sources {
		if s.URL == address {
			s.Queued = true
			return s, true, a.store.saveImportSource(s)
		}
	}
	if len(sources) >= 32 {
		return ImportSource{}, false, errors.New("最多保存 32 个订阅来源")
	}
	if input.Name == "" {
		u, _ := url.Parse(address)
		input.Name = u.Hostname()
	}
	now := time.Now().Unix()
	v := ImportSource{ID: "src-" + digest(randomToken(12))[:14], Name: input.Name, URL: address, Address: sourceAddress(address), Enabled: input.Enabled, Queued: true, Revision: randomToken(12), Created: now, ResourceIDs: []string{}, Warnings: []importWarning{}}
	v.NextAt = sourceNextAt(v.ID, now)
	return v, false, a.store.saveImportSource(v)
}
func (a *App) importSources(w http.ResponseWriter, r *http.Request, actor Record) {
	path := strings.TrimPrefix(r.URL.Path, "/api/import-sources")
	a.mu.Lock()
	defer a.mu.Unlock()
	if path == "" && r.Method == "GET" {
		sources, e := a.store.importSources()
		if e != nil {
			failure(w, 500, "读取订阅来源失败")
			return
		}
		items := []object{}
		pools, e := a.store.pools()
		if e != nil {
			failure(w, 500, "读取订阅来源失败")
			return
		}
		for _, s := range sources {
			v := object{"source": s, "running": a.importRunning == s.ID, "resources": 0, "stale": 0}
			count, stale := 0, 0
			allIDs := []string{}
			for _, p := range pools {
				if containsSourceID(s.ResourceIDs, p.ID) || p.SubscriptionID == s.ID {
					allIDs = append(allIDs, p.ID)
				}
				if containsSourceID(s.ResourceIDs, p.ID) {
					count++
				}
				if p.SubscriptionID == s.ID && p.SourceStale {
					stale++
				}
			}
			s.URL = ""
			v["source"] = s
			v["resources"], v["stale"] = count, stale
			v["resource_ids"] = allIDs
			items = append(items, v)
		}
		jsonResponse(w, 200, object{"items": items, "running_id": a.importRunning, "interval_hours": 24, "max_parallel": 1})
		return
	}
	if path == "" && r.Method == "POST" {
		in := sourceInput{Enabled: true}
		if !decode(w, r, &in) {
			return
		}
		v, duplicate, e := a.createSource(in)
		if e != nil {
			failure(w, 400, e.Error())
			return
		}
		a.store.audit(actor.Username, "create-import-source", v.ID)
		v.URL = ""
		jsonResponse(w, 201, object{"source": v, "duplicate": duplicate})
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	v, e := a.store.importSource(parts[0])
	if e != nil {
		failure(w, 404, "订阅来源不存在")
		return
	}
	if len(parts) == 2 && parts[1] == "refresh" && r.Method == "POST" {
		if a.importRunning != v.ID {
			v.Queued = true
			if e = a.store.saveImportSource(v); e != nil {
				failure(w, 500, "更新排队失败")
				return
			}
		}
		jsonResponse(w, 202, object{"queued": true})
		return
	}
	if len(parts) != 1 {
		failure(w, 404, "接口不存在")
		return
	}
	if r.Method == "PUT" {
		var in sourceInput
		if !decode(w, r, &in) {
			return
		}
		if in.Revision != v.Revision {
			failure(w, 409, "订阅来源已被修改，请重新加载")
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" || utf8.RuneCountInString(name) > 80 {
			failure(w, 400, "请填写 1–80 字的订阅名称")
			return
		}
		if in.URL != "" {
			if e = validateSourceURL(in.URL); e != nil {
				failure(w, 400, e.Error())
				return
			}
			all, e := a.store.importSources()
			if e != nil {
				failure(w, 500, "读取订阅来源失败")
				return
			}
			for _, s := range all {
				if s.ID != v.ID && s.URL == in.URL {
					failure(w, 409, "该订阅链接已存在")
					return
				}
			}
			v.URL = in.URL
			v.Address = sourceAddress(in.URL)
			v.Queued = true
		}
		v.Name, v.Enabled, v.Revision = name, in.Enabled, randomToken(12)
		if !v.Enabled {
			v.Queued = false
		}
		if v.Enabled && v.NextAt < time.Now().Unix() {
			v.NextAt = sourceNextAt(v.ID, time.Now().Unix())
		}
		if e = a.store.saveImportSource(v); e != nil {
			failure(w, 500, "保存订阅来源失败")
			return
		}
		v.URL = ""
		jsonResponse(w, 200, v)
		return
	}
	if r.Method == "DELETE" {
		a.deleteImportSource(w, r, actor, v)
		return
	}
	failure(w, 405, "方法不支持")
}
func containsSourceID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func (s *Store) persistSourceSync(source ImportSource, write []IPResource, remove []string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, p := range write {
		p.NodeIDs = nil
		b, e := s.vault.seal(p)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO ip_pool(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", p.ID, b); e != nil {
			return e
		}
	}
	for _, id := range remove {
		if _, e = tx.Exec("DELETE FROM ip_pool WHERE id=?", id); e != nil {
			return e
		}
	}
	b, e := s.vault.seal(source)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE import_sources SET doc=? WHERE id=?", b, source.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) syncSourceLocked(source ImportSource, items []importItem, warnings []importWarning) error {
	old, e := a.store.pools()
	if e != nil {
		return e
	}
	nodes, e := a.store.nodes()
	if e != nil {
		return e
	}
	sources, e := a.store.importSources()
	if e != nil {
		return e
	}
	hosts := map[string]bool{a.cfg.VLESSHost: true, a.cfg.HY2Host: true}
	for _, n := range nodes {
		if n.Exit == "direct" && n.ProbeIP != "" {
			hosts[n.ProbeIP] = true
		}
	}
	desired := map[string]importItem{}
	for _, item := range items {
		if number(item.Proxy["port"]) == 443 && hosts[str(item.Proxy["server"])] {
			return errors.New("不能将本站节点导入为本站出口，请使用上游机场订阅")
		}
		desired[upstreamKey(item.Proxy)] = item
	}
	// A partial parse never authorizes pruning: unknown entries may still represent an old resource.
	write, target := []IPResource{}, []IPResource{}
	remove, addedIDs := []string{}, []string{}
	keys := map[string]IPResource{}
	ports := map[int]bool{}
	retained := 0
	for _, p := range old {
		absent := p.SubscriptionID == source.ID && p.Exit == "subscription" && desired[upstreamKey(p.Upstream)].Proxy == nil
		shared := false
		for _, s := range sources {
			if s.ID != source.ID && containsSourceID(s.ResourceIDs, p.ID) {
				shared = true
				break
			}
		}
		if absent && len(warnings) == 0 && len(poolBindings(nodes, p.ID)) == 0 && !shared {
			remove = append(remove, p.ID)
			continue
		}
		if absent {
			retained++
			if !p.SourceStale {
				p.SourceStale = true
				p.Revision = randomToken(12)
				write = append(write, p)
			}
		} else if p.SubscriptionID == source.ID && p.SourceStale {
			p.SourceStale = false
			p.Revision = randomToken(12)
			write = append(write, p)
		}
		target = append(target, p)
		if p.Exit == "subscription" && p.PoolGroup != "public" {
			keys[upstreamKey(p.Upstream)] = p
			ports[p.BridgePort] = true
		}
	}
	ids := []string{}
	ordered := make([]string, 0, len(desired))
	for key := range desired {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		item := desired[key]
		if p, ok := keys[key]; ok {
			ids = append(ids, p.ID)
			continue
		}
		if len(target) >= 256 {
			return errors.New("IP 池最多 256 项，请先清理后重试")
		}
		port := 21000
		for ports[port] && port <= 21255 {
			port++
		}
		if port > 21255 {
			return errors.New("出口端口资源不足")
		}
		ports[port] = true
		p := IPResource{Node: Node{ID: "ip-" + digest(randomToken(12))[:10], Protocol: "vless", Enabled: true, Exit: "subscription", Host: str(item.Proxy["server"]), Port: number(item.Proxy["port"]), Name: "待检测出口", Upstream: item.Proxy, UpstreamType: str(item.Proxy["type"]), BridgePort: port, BridgePassword: randomToken(24)}, Label: item.Label, Revision: randomToken(12), SubscriptionID: source.ID}
		write = append(write, p)
		target = append(target, p)
		addedIDs = append(addedIDs, p.ID)
		ids = append(ids, p.ID)
	}
	previous := source
	now := time.Now().Unix()
	source.ResourceIDs = ids
	source.Added = len(addedIDs)
	source.Removed = len(remove)
	source.Retained = retained
	source.Warnings = warnings
	source.Error = ""
	source.Failures = 0
	source.LastSuccess = now
	source.NextAt = sourceNextAt(source.ID, now)
	source.Queued = false
	if len(addedIDs)+len(remove) > 0 {
		if e = a.validateBridge(target); e != nil {
			return e
		}
	}
	if e = a.store.persistSourceSync(source, write, remove); e != nil {
		return e
	}
	if len(addedIDs)+len(remove) > 0 {
		if e = a.ensureBridge(); e != nil {
			restore := a.store.persistSourceSync(previous, old, addedIDs)
			rollback := a.ensureBridge()
			if restore != nil || rollback != nil {
				a.status, a.syncError = "error", "订阅更新恢复失败"
				return errors.New("出口核心恢复尚未完成，请检查运维状态")
			}
			return errors.New("出口核心更新失败，已保留原资源")
		}
	}
	a.store.audit("system", "refresh-import-source", fmt.Sprintf("%s: +%d -%d retained %d", source.ID, len(addedIDs), len(remove), retained))
	return nil
}
func (a *App) runImportSource(ctx context.Context, fetch func(context.Context, string) ([]byte, error), now int64) bool {
	return a.runImportSourceID(ctx, fetch, now, "")
}
func (a *App) runImportSourceID(ctx context.Context, fetch func(context.Context, string) ([]byte, error), now int64, onlyID string) bool {
	if !a.heavyMu.TryLock() {
		return false
	}
	defer a.heavyMu.Unlock()
	a.mu.Lock()
	sources, e := a.store.importSources()
	if e != nil {
		a.mu.Unlock()
		return false
	}
	next, _ := strconv.ParseInt(a.store.meta("import_next_dispatch"), 10, 64)
	if now < next {
		a.mu.Unlock()
		return false
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Queued != sources[j].Queued {
			return sources[i].Queued
		}
		return sources[i].NextAt < sources[j].NextAt
	})
	var snapshot ImportSource
	for _, s := range sources {
		if (onlyID == "" || s.ID == onlyID) && (s.Queued || (s.Enabled && s.NextAt <= now)) {
			snapshot = s
			break
		}
	}
	if snapshot.ID == "" {
		a.mu.Unlock()
		return false
	}
	snapshot.LastAttempt = now
	snapshot.Queued = false
	a.importRunning = snapshot.ID
	if a.store.saveImportSource(snapshot) != nil {
		a.importRunning = ""
		a.mu.Unlock()
		return false
	}
	_ = a.store.setMeta("import_next_dispatch", strconv.FormatInt(now+60, 10))
	a.mu.Unlock()
	parentContext := ctx
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	b, err := fetch(ctx, snapshot.URL)
	var items []importItem
	var warnings []importWarning
	if err == nil {
		items, warnings, err = parseSubscription(b)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() { a.importRunning = "" }()
	current, e := a.store.importSource(snapshot.ID)
	if e != nil || current.Revision != snapshot.Revision {
		return true
	}
	if parentContext.Err() != nil {
		current.Queued = true
		_ = a.store.saveImportSource(current)
		return true
	}
	if ctx.Err() != nil {
		err = errors.New("订阅更新超时，已保留原资源")
	}
	if err == nil {
		err = a.syncSourceLocked(current, items, warnings)
	}
	if err != nil {
		current.Error = err.Error()
		current.Failures++
		current.NextAt = sourceFailureNext(current.ID, current.Failures, now)
		current.Warnings = warnings
		_ = a.store.saveImportSource(current)
	}
	return true
}
func (a *App) importSourceLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runImportSource(ctx, fetchSubscription, time.Now().Unix())
		}
	}
}
