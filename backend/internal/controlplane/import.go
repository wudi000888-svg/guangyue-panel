package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

type importWarning struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}
type importItem struct {
	Label  string
	Proxy  object
	Native bool
}

func unbase(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, e := enc.DecodeString(s); e == nil {
			return b, nil
		}
	}
	return nil, errors.New("Base64 编码无效")
}
func number(v any) int { i, _ := strconv.Atoi(fmt.Sprint(v)); return i }
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func safeLabel(s string) string {
	r := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if len(r) > 64 {
		r = r[:64]
	}
	return string(r)
}
func normalizeProxy(p object) (importItem, error) {
	kind := strings.ToLower(str(p["type"]))
	if kind == "hy2" {
		kind = "hysteria2"
	}
	p["type"] = kind
	common := "name type server port udp tfo "
	options := map[string]string{
		"vless":     "uuid flow tls alpn skip-cert-verify servername client-fingerprint network reality-opts ws-opts grpc-opts h2-opts packet-encoding encryption",
		"vmess":     "uuid alterId cipher tls alpn skip-cert-verify servername client-fingerprint network ws-opts grpc-opts h2-opts packet-encoding",
		"trojan":    "password sni alpn skip-cert-verify client-fingerprint network ws-opts grpc-opts",
		"ss":        "cipher password udp-over-tcp udp-over-tcp-version",
		"hysteria2": "password sni alpn skip-cert-verify fingerprint obfs obfs-password up down ports hop-interval",
		"socks5":    "username password tls skip-cert-verify fingerprint",
		"http":      "username password tls skip-cert-verify sni headers"}
	allowed, ok := options[kind]
	if !ok {
		return importItem{}, errors.New("暂不支持此协议")
	}
	keys := strings.Fields(common + allowed)
	for k := range p {
		valid := false
		for _, key := range keys {
			if k == key {
				valid = true
				break
			}
		}
		if !valid {
			return importItem{}, errors.New("含暂不支持的节点选项")
		}
	}
	// Only protocol options enter the core. Subscription rules, providers, local
	// file references, dialer proxies, controllers and scripts are never imported.
	nested := map[string]string{"reality-opts": "public-key short-id", "ws-opts": "path headers max-early-data early-data-header-name", "grpc-opts": "grpc-service-name", "h2-opts": "path host"}
	for key, fields := range nested {
		if value, exists := p[key]; exists {
			m, ok := value.(map[string]any)
			if !ok {
				return importItem{}, errors.New("传输选项格式无效")
			}
			for k := range m {
				if !strings.Contains(" "+fields+" ", " "+k+" ") {
					return importItem{}, errors.New("含暂不支持的传输选项")
				}
			}
		}
	}
	host := str(p["server"])
	port := number(p["port"])
	if host == "" || len(host) > 253 || port < 1 || port > 65535 || strings.ContainsAny(host, " /\\\t\n\r?#@:") && net.ParseIP(host) == nil {
		return importItem{}, errors.New("服务器或端口无效")
	}
	p["port"] = port
	if kind == "vless" || kind == "vmess" {
		if str(p["uuid"]) == "" {
			return importItem{}, errors.New("缺少 UUID")
		}
	}
	if kind == "ss" || kind == "trojan" || kind == "hysteria2" {
		if str(p["password"]) == "" {
			return importItem{}, errors.New("缺少认证信息")
		}
	}
	if network := str(p["network"]); network != "" && network != "tcp" && network != "ws" && network != "grpc" && network != "h2" {
		return importItem{}, errors.New("暂不支持此传输方式")
	}
	if kind == "vmess" {
		p["alterId"] = number(p["alterId"])
		if str(p["cipher"]) == "" {
			p["cipher"] = "auto"
		}
	}
	label := safeLabel(str(p["name"]))
	if label == "" {
		label = strings.ToUpper(kind) + " · " + host
	}
	delete(p, "name")
	// Stable JSON shape makes raw/Base64/YAML representations deduplicate alike.
	b, err := json.Marshal(p)
	if err != nil || len(b) > 16<<10 {
		return importItem{}, errors.New("节点参数过大或格式无效")
	}
	_ = json.Unmarshal(b, &p)
	return importItem{Label: label, Proxy: p}, nil
}
func parseSubscription(b []byte) ([]importItem, []importWarning, error) {
	if len(b) > 4<<20 {
		return nil, nil, errors.New("订阅最大 4 MiB")
	}
	text := cleanShareText(strings.TrimPrefix(string(b), "\ufeff"))
	raw := []object{}
	warnings := []importWarning{}
	if !strings.Contains(text, "proxies:") && !strings.HasPrefix(text, "{") && !strings.Contains(text, "://") {
		decoded, e := unbase(text)
		if e != nil {
			return nil, nil, e
		}
		text = strings.TrimSpace(string(decoded))
	}
	if strings.Contains(text, "proxies:") || strings.HasPrefix(text, "{") {
		var doc struct {
			Proxies []object `yaml:"proxies"`
		}
		if e := yaml.Unmarshal([]byte(text), &doc); e != nil {
			return nil, nil, errors.New("Clash / Mihomo 订阅格式无效")
		}
		raw = doc.Proxies
	} else {
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		filtered := []string{}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				filtered = append(filtered, line)
			}
		}
		lines = filtered
		if len(lines) > 256 {
			return nil, nil, errors.New("单次最多导入 256 个节点")
		}
		for i, line := range lines {
			p, e := parseURI(line)
			if e != nil {
				warnings = append(warnings, importWarning{i + 1, e.Error()})
				raw = append(raw, nil)
			} else {
				raw = append(raw, p)
			}
		}
	}
	if len(raw) > 256 {
		return nil, nil, errors.New("单次最多导入 256 个节点")
	}
	items := []importItem{}
	for i, p := range raw {
		if p == nil {
			continue
		}
		item, e := normalizeProxy(p)
		if e != nil {
			warnings = append(warnings, importWarning{i + 1, e.Error()})
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, warnings, errors.New("订阅中没有可导入的节点")
	}
	return items, warnings, nil
}
func fetchSubscription(ctx context.Context, address string) ([]byte, error) {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("请填写有效的 HTTPS 订阅链接")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	t := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !publicIP(ip.IP.String()) {
				return nil, errors.New("订阅地址必须是公网地址")
			}
		}
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
		}
		return nil, errors.New("无法连接订阅服务器")
	}}
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" || r.URL.User != nil {
			return errors.New("重定向无效")
		}
		return nil
	}}
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", "ClashMeta/Guangyue-"+version)
	res, e := c.Do(req)
	if e != nil {
		return nil, errors.New("订阅下载失败，请检查链接与服务器连通性")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("订阅服务器返回 HTTP %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if e != nil || len(b) > 4<<20 {
		return nil, errors.New("订阅下载失败或超过 4 MiB")
	}
	return b, nil
}
func (a *App) importSubscription(w http.ResponseWriter, r *http.Request, actor Record) {
	var input struct {
		URL           string `json:"url"`
		Content       string `json:"content"`
		PlainProtocol string `json:"plain_protocol"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		failure(w, 400, "导入请求格式无效或超过大小限制")
		return
	}
	if (input.URL == "") == (input.Content == "") {
		failure(w, 400, "请选择订阅链接或订阅内容其中一种")
		return
	}
	if input.PlainProtocol != "" && input.PlainProtocol != "http" && input.PlainProtocol != "socks5" {
		failure(w, 400, "普通代理文件协议仅支持 HTTP 或 SOCKS5")
		return
	}
	b := []byte(input.Content)
	var err error
	if input.URL != "" {
		address := cleanShareText(input.URL)
		scheme, _, _ := strings.Cut(address, "://")
		single := strings.Contains(" vless vmess ss trojan hysteria2 hy2 socks5 socks ", " "+strings.ToLower(scheme)+" ")
		if strings.EqualFold(scheme, "http") {
			u, e := url.Parse(address)
			single = e == nil && (u.User != nil || u.Port() != "" && u.Path == "")
		}
		if single {
			b = []byte(address)
		} else {
			a.mu.Lock()
			source, duplicate, createErr := a.createSource(sourceInput{URL: address, Enabled: true})
			a.mu.Unlock()
			if createErr != nil {
				failure(w, 400, createErr.Error())
				return
			}
			jsonResponse(w, 200, object{"queued": true, "source_id": source.ID, "added": 0, "duplicates": 0, "duplicate_source": duplicate, "warnings": []importWarning{}, "resources": []IPResource{}})
			return
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
	}
	items, warnings, err := parseImportContent(b, input.PlainProtocol)
	if err != nil {
		jsonResponse(w, 400, object{"error": err.Error(), "warnings": warnings})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Reject feeding this installation back into itself before creating a route loop.
	localHosts := map[string]bool{a.cfg.VLESSHost: true, a.cfg.HY2Host: true}
	localNodes, _ := a.store.nodes()
	for _, n := range localNodes {
		if n.Exit == "direct" && n.ProbeIP != "" {
			localHosts[n.ProbeIP] = true
		}
	}
	for _, item := range items {
		if number(item.Proxy["port"]) == 443 && localHosts[str(item.Proxy["server"])] {
			failure(w, 400, "不能将本站节点导入为本站出口，请使用上游机场订阅")
			return
		}
	}
	old, err := a.store.pools()
	if err != nil {
		failure(w, 500, "读取 IP 池失败")
		return
	}
	keys, ports := map[string]bool{}, map[int]bool{}
	for _, p := range old {
		if p.Exit == "subscription" {
			keys[upstreamKey(p.Upstream)] = true
			ports[p.BridgePort] = true
		}
	}
	added := []IPResource{}
	duplicates := 0
	bridgeChanged := false
	for _, item := range items {
		if item.Native {
			node := nativeImportNode(item)
			duplicate := false
			for _, collection := range [][]IPResource{old, added} {
				for _, existing := range collection {
					if existing.PoolGroup != "public" && sameExit(existing.Node, node) {
						duplicate = true
					}
				}
			}
			if duplicate {
				duplicates++
				continue
			}
			if len(old)+len(added) >= 256 {
				failure(w, 400, "IP 池最多 256 项，请先清理后重试")
				return
			}
			resource := resourceFromNode(node)
			resource.Label = item.Label
			added = append(added, resource)
			continue
		}
		key := upstreamKey(item.Proxy)
		if keys[key] {
			duplicates++
			continue
		}
		keys[key] = true
		port := 21000
		for ports[port] && port <= 21255 {
			port++
		}
		if port > 21255 || len(old)+len(added) >= 256 {
			failure(w, 400, "IP 池最多 256 项，请先清理后重试")
			return
		}
		ports[port] = true
		p := IPResource{Node: Node{ID: "ip-" + digest(randomToken(8))[:10], Protocol: "vless", Enabled: true, Exit: "subscription", Host: str(item.Proxy["server"]), Port: number(item.Proxy["port"]), Name: "待检测出口", Upstream: item.Proxy, UpstreamType: str(item.Proxy["type"]), BridgePort: port, BridgePassword: randomToken(24)}, Label: item.Label, Revision: randomToken(12)}
		added = append(added, p)
		bridgeChanged = true
	}
	if len(added) > 0 {
		all := append(append([]IPResource{}, old...), added...)
		if bridgeChanged {
			err = a.validateBridge(all)
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		if err = a.store.savePoolNodes(added, nil); err != nil {
			failure(w, 500, "导入保存失败")
			return
		}
		if bridgeChanged {
			err = a.ensureBridge()
		}
		if err != nil {
			ids := []string{}
			for _, p := range added {
				ids = append(ids, p.ID)
			}
			restoreErr := a.store.savePoolState(nil, nil, ids)
			rollback := a.ensureBridge()
			if rollback != nil || restoreErr != nil {
				a.status, a.syncError = "error", "机场出口恢复失败"
				failure(w, 502, "出口核心应用失败，恢复尚未完成，请检查运维状态")
				return
			}
			failure(w, 502, "出口核心未就绪，导入已撤销")
			return
		}
	}
	public := []IPResource{}
	for _, p := range added {
		public = append(public, p.public(nil))
	}
	a.store.audit(actor.Username, "import_subscription", fmt.Sprintf("新增 %d，重复 %d，跳过 %d", len(added), duplicates, len(warnings)))
	jsonResponse(w, 200, object{"added": len(added), "duplicates": duplicates, "warnings": warnings, "resources": public})
}
