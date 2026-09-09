package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxCustomPublicSources = 16
const maxCustomPublicEntries = 2000
const maxPublicSourceBytes = 1 << 20

// Custom URLs may contain subscription tokens. Persist the entire document in
// the same encrypted vault as private subscriptions; expose only its hostname.
func (s *Store) publicSourceCatalog() ([]PublicSource, error) {
	out := append([]PublicSource{}, publicSources...)
	for i := range out {
		out[i].Kind, out[i].Enabled = "builtin", true
		out[i].Address = sourceAddress(out[i].URL)
	}
	rows, err := s.db.Query("SELECT doc FROM public_custom_sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var source PublicSource
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = s.vault.open(b, &source); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}
func safePublicSources(sources []PublicSource) []PublicSource {
	out := append([]PublicSource{}, sources...)
	for i := range out {
		if out[i].Custom {
			out[i].URL = ""
		}
	}
	return out
}
func validatePublicSourceURL(address string) error {
	if validateSourceURL(address) != nil {
		return errors.New("公开来源必须是有效的 HTTPS 链接")
	}
	u, _ := url.Parse(address)
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("公开来源端口无效")
		}
	}
	if net.ParseIP(host) != nil {
		if !publicIP(host) {
			return errors.New("公开来源必须使用公网地址")
		}
		return nil
	}
	if !strings.Contains(host, ".") || strings.ContainsAny(host, "%\\:/ ") {
		return errors.New("公开来源必须使用公网域名")
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".onion"} {
		if strings.HasSuffix(host, suffix) {
			return errors.New("公开来源必须使用公网域名")
		}
	}
	return nil
}

// Resolve once, reject the complete answer if it includes a private address,
// then connect to the verified IP directly so DNS cannot change between checks.
func publicSourceDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("公开来源地址无效")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("公开来源域名解析失败")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP.String()) {
			return nil, errors.New("公开来源必须使用公网地址")
		}
	}
	for _, ip := range ips {
		conn, e := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if e == nil {
			return conn, nil
		}
	}
	return nil, errors.New("公开来源连接失败")
}

func publicSourceRedirect(r *http.Request, via []*http.Request) error {
	if len(via) > 3 || validatePublicSourceURL(r.URL.String()) != nil {
		return errors.New("公开来源重定向无效")
	}
	// Validators belong to the original resource. Do not forward them to a new
	// host/path and accidentally reuse the old body after an unrelated HTTP 304.
	r.Header.Del("If-None-Match")
	r.Header.Del("If-Modified-Since")
	r.Header.Del("Referer")
	return nil
}

type publicSourceInput struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Text     string `json:"text"`
	Protocol string `json:"protocol"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}

func decodePublicSource(w http.ResponseWriter, r *http.Request, v *publicSourceInput) bool {
	r.Body = http.MaxBytesReader(w, r.Body, (2<<20)+16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		failure(w, 400, "公开来源请求格式无效或超过大小限制")
		return false
	}
	return true
}

// Both pasted/file lists and fetched URL lists are candidates only. Storing
// them never writes ip_pool, nodes, users, or either subscription credential.
func normalizedPublicText(text string, source PublicSource, truncate bool) ([]byte, int, error) {
	if len(text) > maxPublicSourceBytes || !utf8.ValidString(text) {
		return nil, 0, errors.New("公开代理列表最多 1 MiB，需为 UTF-8 文本")
	}
	items := parsePublicList([]byte(text), source)
	if len(items) == 0 {
		return nil, 0, errors.New("列表中没有有效的公网 HTTP 或 SOCKS5 代理")
	}
	if len(items) > maxCustomPublicEntries {
		if !truncate {
			return nil, 0, errors.New("每个自定义来源最多 2000 个有效且不重复的代理")
		}
		items = items[:maxCustomPublicEntries]
	}
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Exit + "://" + net.JoinHostPort(item.Host, strconv.Itoa(item.Port)) + "\n")
	}
	return []byte(b.String()), len(items), nil
}

func (a *App) managePublicSources(w http.ResponseWriter, r *http.Request, actor Record) {
	path := strings.TrimPrefix(r.URL.Path, "/api/public-pool/sources")
	if path == "" && r.Method == "GET" {
		catalog, err := a.store.publicSourceCatalog()
		if err != nil {
			failure(w, 500, "无法读取公开来源")
			return
		}
		jsonResponse(w, 200, object{"sources": safePublicSources(catalog), "source_status": a.publicSourceStates()})
		return
	}
	if r.Method != "POST" && r.Method != "PUT" && r.Method != "DELETE" || r.Method == "POST" && path != "" || r.Method != "POST" && (path == "" || strings.Contains(strings.TrimPrefix(path, "/"), "/")) {
		failure(w, 404, "公开来源操作不存在")
		return
	}
	var in publicSourceInput
	if r.Method != "DELETE" && !decodePublicSource(w, r, &in) {
		return
	}
	if r.Method == "DELETE" {
		in.Revision = r.URL.Query().Get("revision")
	}
	in.Name = strings.TrimSpace(in.Name)
	if r.Method != "DELETE" && (in.Name == "" || utf8.RuneCountInString(in.Name) > 80) {
		failure(w, 400, "公开来源名称需为 1–80 个字符")
		return
	}
	if !a.heavyMu.TryLock() {
		failure(w, 409, "已有采集、抓取或检测任务正在执行，请稍后重试来源变更")
		return
	}
	defer a.heavyMu.Unlock()
	a.publicMu.Lock()
	defer a.publicMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.publicSourceMu.Lock()
	defer a.publicSourceMu.Unlock()
	catalog, err := a.store.publicSourceCatalog()
	if err != nil {
		failure(w, 500, "无法读取公开来源")
		return
	}
	c := a.store.publicSettings()
	var source PublicSource
	var body []byte
	entries := 0
	if r.Method == "POST" {
		if len(catalog)-len(publicSources) >= maxCustomPublicSources {
			failure(w, 400, "最多保存 16 个自定义公开来源")
			return
		}
		if in.Protocol != "" && in.Protocol != "http" && in.Protocol != "socks5" {
			failure(w, 400, "代理类型请选择自动、HTTP 或 SOCKS5")
			return
		}
		in.URL = strings.TrimSpace(in.URL)
		if (in.URL == "") == (strings.TrimSpace(in.Text) == "") {
			failure(w, 400, "请提供一个 HTTPS 链接或代理列表")
			return
		}
		source = PublicSource{ID: "pubsrc-" + digest(randomToken(16))[:14], Name: in.Name, URL: in.URL, Protocol: in.Protocol, Custom: true, Enabled: in.Enabled, Revision: randomToken(12)}
		if in.URL != "" {
			if err = validatePublicSourceURL(in.URL); err != nil {
				failure(w, 400, err.Error())
				return
			}
			for _, existing := range catalog {
				if existing.URL == in.URL {
					failure(w, 409, "该公开来源已经存在")
					return
				}
			}
			source.Kind, source.Address = "url", sourceAddress(in.URL)
		} else {
			source.Kind, source.Address = "text", ""
			body, entries, err = normalizedPublicText(in.Text, source, false)
			if err != nil {
				failure(w, 400, err.Error())
				return
			}
		}
	} else {
		id := strings.TrimPrefix(path, "/")
		for _, v := range catalog {
			if v.ID == id {
				source = v
				break
			}
		}
		if source.ID == "" {
			failure(w, 404, "公开来源不存在")
			return
		}
		if !source.Custom {
			failure(w, 400, "内置来源请在采集策略中选择启用状态")
			return
		}
		if in.Revision != source.Revision {
			failure(w, 409, "公开来源已更新，请刷新后重试")
			return
		}
		if r.Method == "PUT" {
			if in.URL != "" || in.Text != "" || in.Protocol != "" {
				failure(w, 400, "修改来源仅支持名称与启用状态")
				return
			}
			source.Name, source.Enabled, source.Revision = in.Name, in.Enabled, randomToken(12)
		}
	}
	selected := []string{}
	for _, id := range c.Sources {
		if id != source.ID {
			selected = append(selected, id)
		}
	}
	if source.Enabled && r.Method != "DELETE" {
		selected = append(selected, source.ID)
	}
	if c.Enabled && len(selected) == 0 {
		failure(w, 409, "此来源是唯一采集来源，请先选择其他来源或关闭自动采集")
		return
	}
	c.Sources, c.Revision = selected, randomToken(12)
	settings, _ := json.Marshal(c)
	tx, err := a.store.db.Begin()
	if err != nil {
		failure(w, 500, "公开来源保存失败")
		return
	}
	defer tx.Rollback()
	if r.Method == "DELETE" {
		for _, table := range []string{"public_custom_sources", "public_source_cache", "public_source_tasks"} {
			if _, err = tx.Exec("DELETE FROM "+table+" WHERE id=?", source.ID); err != nil {
				failure(w, 500, "公开来源删除失败")
				return
			}
		}
	} else {
		sealed, e := a.store.vault.seal(source)
		if e != nil {
			failure(w, 500, "公开来源保存失败")
			return
		}
		key := source.URL
		if source.Kind == "text" {
			key = source.ID
		}
		if _, err = tx.Exec("INSERT INTO public_custom_sources(id,url_hash,doc) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", source.ID, digest(key), sealed); err != nil {
			failure(w, 500, "公开来源保存失败")
			return
		}
		var state PublicSourceState
		var stateDoc string
		_ = tx.QueryRow("SELECT doc FROM public_source_tasks WHERE id=?", source.ID).Scan(&stateDoc)
		_ = json.Unmarshal([]byte(stateDoc), &state)
		state.ID = source.ID
		state.Queued = source.Enabled && source.Kind == "url" && (r.Method == "POST" || state.LastSuccess == 0)
		if body != nil {
			now := time.Now().Unix()
			if _, err = tx.Exec("INSERT INTO public_source_cache(id,body,etag,modified,fetched) VALUES(?,?,?,?,?)", source.ID, body, "", "", now); err != nil {
				failure(w, 500, "公开代理列表保存失败")
				return
			}
			state.LastAttempt, state.LastSuccess, state.Entries, state.Bytes = now, now, entries, len(body)
		}
		b, _ := json.Marshal(state)
		if _, err = tx.Exec("INSERT INTO public_source_tasks(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", source.ID, b); err != nil {
			failure(w, 500, "公开来源任务保存失败")
			return
		}
	}
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('public_settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(settings)); err != nil || tx.Commit() != nil {
		failure(w, 500, "公开来源策略保存失败")
		return
	}
	if c.Enabled && source.Enabled && source.Kind == "text" && r.Method != "DELETE" {
		_ = a.store.setMeta("public_source_screen_pending", "1")
	}
	a.store.audit(actor.Username, "manage_public_source", source.ID+" "+r.Method)
	if r.Method == "DELETE" {
		retained := 0
		pools, _ := a.store.pools()
		for _, p := range pools {
			if p.PoolGroup == "public" && p.Source == source.ID {
				retained++
			}
		}
		jsonResponse(w, 200, object{"ok": true, "retained_resources": retained})
		return
	}
	code := 200
	if r.Method == "POST" {
		code = 201
	}
	state := a.store.publicSourceState(source.ID)
	jsonResponse(w, code, object{"source": safePublicSources([]PublicSource{source})[0], "source_status": state, "entries": state.Entries, "queued": state.Queued, "screening_pending": c.Enabled && source.Enabled})
}
