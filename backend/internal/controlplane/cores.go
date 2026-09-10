package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/coreprocess"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type object = map[string]any

var errNoNodes = errors.New("no authorized nodes")

func xrayConfig(c Config, records []Record, nodes []Node) object {
	clients := map[string][]object{}
	outbounds := []object{{"tag": "block", "protocol": "blackhole"}, {"tag": "direct", "protocol": "freedom", "settings": object{"domainStrategy": "UseIPv4"}}}
	rules := []object{{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"}}
	for _, n := range nodes {
		if n.Protocol != "vless" || !n.Enabled {
			continue
		}
		n = coreExit(c, n)
		tag := "direct"
		if n.Exit != "direct" {
			tag = n.ID
			server := object{"address": n.Host, "port": n.Port}
			if n.Username != "" {
				server["users"] = []object{{"user": n.Username, "pass": n.Password}}
			}
			protocol := n.Exit
			if protocol == "socks5" {
				protocol = "socks"
			}
			outbounds = append(outbounds, object{"tag": tag, "protocol": protocol, "settings": object{"servers": []object{server}}})
		}
		users := []string{}
		for _, r := range records {
			if !r.Active() || !r.VLESS {
				continue
			}
			id := r.Credentials.VLESS[n.ID]
			if id == "" {
				continue
			}
			e := email(r.ID, n.ID)
			group := realityGroupTag(c, n)
			clients[group] = append(clients[group], object{"id": id, "email": e, "flow": "xtls-rprx-vision", "level": 0})
			users = append(users, e)
		}
		if len(users) > 0 {
			rules = append(rules, object{"type": "field", "user": users, "outboundTag": tag, "ruleTag": n.ID})
		}
	}
	inbounds := []object{}
	for _, group := range realityGroups(c, nodes) {
		users := clients[group.Tag]
		if users == nil {
			users = []object{}
		}
		inbound := object{"tag": group.Tag, "listen": group.Listen, "protocol": "vless", "settings": object{"decryption": "none", "clients": users}, "streamSettings": object{"network": "tcp", "security": "reality", "tcpSettings": object{"acceptProxyProtocol": true}, "realitySettings": object{"dest": group.Target, "serverNames": []string{group.SNI}, "privateKey": c.RealityPrivate, "shortIds": []string{c.ShortID}}}}
		if group.Port != 0 {
			inbound["port"] = group.Port
		}
		inbounds = append(inbounds, inbound)
	}
	inbounds = append(inbounds, object{"tag": "api", "listen": "127.0.0.1", "port": 19185, "protocol": "dokodemo-door", "settings": object{"address": "127.0.0.1"}})
	return object{"log": object{"loglevel": "warning", "access": "none"}, "api": object{"tag": "api", "services": []string{"HandlerService", "StatsService", "RoutingService"}}, "stats": object{}, "policy": object{"levels": object{"0": object{"statsUserUplink": true, "statsUserDownlink": true, "connIdle": 300, "bufferSize": 32}}}, "inbounds": inbounds, "outbounds": outbounds, "routing": object{"domainStrategy": "AsIs", "rules": rules}}
}
func hy2Outbound(n Node) object {
	n = effectiveExit(n)
	outbound := object{"name": "exit", "type": "direct", "direct": object{"mode": 4}}
	switch n.Exit {
	case "http":
		u := url.URL{Scheme: "http", Host: net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}
		if n.Username != "" {
			u.User = url.UserPassword(n.Username, n.Password)
		}
		outbound = object{"name": "exit", "type": "http", "http": object{"url": u.String()}}
	case "socks5":
		s := object{"addr": net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}
		if n.Username != "" {
			s["username"] = n.Username
			s["password"] = n.Password
		}
		outbound = object{"name": "exit", "type": "socks5", "socks5": s}
	}
	return outbound
}

func hy2Config(c Config, nodes []Node) object {
	routes := object{}
	for _, n := range nodes {
		if n.Protocol == "hy2" && n.Enabled {
			routes[n.ID] = hy2Outbound(coreExit(c, n))
		}
	}
	return object{"listen": ":443", "tls": object{"cert": c.Cert, "key": c.CertKey}, "auth": object{"type": "http", "http": object{"url": "http://" + c.InternalListen + "/auth"}}, "trafficStats": object{"listen": "127.0.0.1:19199", "secret": c.StatsSecret}, "quic": object{"initStreamReceiveWindow": 2097152, "maxStreamReceiveWindow": 2097152, "initConnReceiveWindow": 5242880, "maxConnReceiveWindow": 5242880, "maxConcurrentIncomingStreams": 256}, "outbounds": []object{hy2Outbound(Node{Exit: "direct"})}, "nodeRouting": true, "nodeOutbounds": routes, "masquerade": object{"type": "string", "string": object{"content": "Not Found", "statusCode": 404}}}
}

var command = coreprocess.Command

func (a *App) xapi(sub string, args ...string) ([]byte, error) {
	base := []string{"api", sub, "--server=127.0.0.1:19185", "--timeout=3"}
	return command(8*time.Second, a.cfg.Xray, append(base, args...)...)
}

var corePID = coreprocess.PID
var restartCore = coreprocess.Restart

func waitPort(port string) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", port, 300*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("core did not become ready")
}
func (a *App) validateHY(cfg object) error {
	if err := a.verifyHYCore(); err != nil {
		return err
	}
	// Isolated startup validates the real core parser, TLS files, and outbound options.
	b, _ := json.Marshal(cfg)
	var candidate object
	_ = json.Unmarshal(b, &candidate)
	candidate["listen"] = "127.0.0.1:0"
	candidate["trafficStats"] = object{"listen": "127.0.0.1:0", "secret": a.cfg.StatsSecret}
	path := filepath.Join(a.cfg.StateDir, "validate-hy2.json")
	if err := writeJSON(path, candidate); err != nil {
		return err
	}
	defer os.Remove(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, a.cfg.Hysteria, "server", "--disable-update-check", "-c", path)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return errors.New("HY2 candidate configuration rejected")
	case <-time.After(500 * time.Millisecond):
		cancel()
		<-done
		return nil
	}
}
func writeChanged(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	old, _ := os.ReadFile(path)
	if bytes.Equal(old, b) {
		return nil
	}
	return atomicWrite(path, b, 0600)
}
func (a *App) prepare() error {
	if !a.cfg.Dev {
		if err := a.verifyHYCore(); err != nil {
			return err
		}
	}
	records, err := a.coreRecords()
	if err != nil {
		return err
	}
	nodes, err := a.store.nodes()
	if err != nil {
		return err
	}
	if err = validateRealityNodes(a.cfg, nodes); err != nil {
		return err
	}
	if err = writeChanged(filepath.Join(a.cfg.StateDir, "xray.json"), xrayConfig(a.cfg, records, nodes)); err != nil {
		return err
	}
	return writeChanged(filepath.Join(a.cfg.StateDir, "hy2.json"), hy2Config(a.cfg, nodes))
}

func (a *App) verifyHYCore() error {
	b, err := command(4*time.Second, a.cfg.Hysteria, "version")
	if err != nil || !bytes.Contains(b, []byte("guangyue-node1")) {
		return errors.New("HY2 core lacks authenticated node routing; install the bundled node-routing build")
	}
	return nil
}

// Only fields that affect routing belong in runtime identity. Quality scans,
// labels, ownership and default-node protection must not restart live cores.
func runtimeNode(n Node) Node {
	n.ExitID = ""
	n.Name = ""
	n.ProbeIP = ""
	n.ProbedAt = 0
	n.Country = ""
	n.CountryCode = ""
	n.CheckedAt = 0
	n.ProbeError = ""
	n.HasPassword = false
	n.Speed = nil
	n.Quality = nil
	n.ManagedBy = ""
	n.DefaultDirect = false
	return n
}

func nodeHash(nodes []Node, protocol string) string {
	list := []Node{}
	for _, n := range nodes {
		if n.Protocol == protocol {
			n = runtimeNode(n)
			list = append(list, n)
		}
	}
	b, _ := json.Marshal(list)
	return digest(string(b))
}

func (a *App) applyCoreConfiguration() error {
	if err := a.ensureBridge(); err != nil {
		return err
	}
	records, err := a.coreRecords()
	if err != nil {
		return err
	}
	nodes, err := a.store.nodes()
	if err != nil {
		return err
	}
	if err = a.ensureNodeGateway(nodes); err != nil {
		return err
	}
	xc, hc := xrayConfig(a.cfg, records, nodes), hy2Config(a.cfg, nodes)
	if err = validateRealityNodes(a.cfg, nodes); err != nil {
		return err
	}
	xp, hp := filepath.Join(a.cfg.StateDir, "xray.json"), filepath.Join(a.cfg.StateDir, "hy2.json")
	if a.cfg.Dev {
		return a.prepare()
	}
	xhash, hhash := nodeHash(nodes, "vless"), nodeHash(nodes, "hy2")
	previousX, err := a.store.readMeta("x_nodes")
	if err != nil {
		return err
	}
	previousHY, err := a.store.readMeta("hy_nodes")
	if err != nil {
		return err
	}
	xchange := previousX != xhash
	hchange := previousHY != hhash
	if xchange {
		candidate := filepath.Join(a.cfg.StateDir, "candidate-xray.json")
		if err = writeJSON(candidate, xc); err != nil {
			return err
		}
		if _, err = command(8*time.Second, a.cfg.Xray, "run", "-test", "-c", candidate); err != nil {
			return errors.New("Xray configuration validation failed")
		}
	}
	if hchange {
		if err = a.validateHY(hc); err != nil {
			return err
		}
	}
	if err = writeChanged(xp, xc); err != nil {
		return err
	}
	if err = writeChanged(hp, hc); err != nil {
		return err
	}
	if xchange {
		if err = restartCore("guangyue-xray.service"); err != nil {
			return err
		}
		time.Sleep(1200 * time.Millisecond)
		if err = waitPort("127.0.0.1:19185"); err != nil {
			return err
		}
		if err = a.store.setMeta("x_nodes", xhash); err != nil {
			return err
		}
		a.lastUsers = ""
	}
	if hchange {
		if err = restartCore("guangyue-hy2.service"); err != nil {
			return err
		}
		time.Sleep(1200 * time.Millisecond)
		if err = waitPort("127.0.0.1:19199"); err != nil {
			return err
		}
		if err = a.store.setMeta("hy_nodes", hhash); err != nil {
			return err
		}
	}
	if err = a.reconcileRealityUsers(xc, xp, xhash); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, r := range records {
		if !r.Active() || !r.HY2 {
			continue
		}
		for _, n := range nodes {
			if n.Protocol == "hy2" && n.Enabled {
				allowed[hyNodeIdentity(r, n)] = true
			}
		}
	}
	b, err := a.hyRequest("GET", "/online", nil)
	if err != nil {
		return errors.New("HY2 online API unavailable")
	}
	var online map[string]int
	if err = json.Unmarshal(b, &online); err != nil {
		return errors.New("invalid HY2 online response")
	}
	kick := []string{}
	a.oldHYConnections = 0
	// HY2 consumes a kick marker on the next traffic event, one connection at
	// a time. Distinct credential generations prevent stale kicks from hitting
	// reauthorized users; the online inventory drives retries across restarts.
	for id, count := range online {
		if !allowed[id] && count > 0 {
			kick = append(kick, id)
			a.oldHYConnections += count
		}
	}
	if len(kick) > 0 {
		if _, err = a.hyRequest("POST", "/kick", kick); err != nil {
			return errors.New("HY2 revocation API unavailable")
		}
	}
	if a.oldHYConnections == 0 {
		if _, err = a.store.db.Exec("DELETE FROM revocations"); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) hyRequest(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:19199"+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", a.cfg.StatsSecret)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("HY2 API failure")
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}
func (a *App) collect() error {
	if a.cfg.Dev {
		return nil
	}
	counters := []Counter{}
	var errs []error
	b, err := a.xapi("statsquery", "-pattern=user>>>")
	if err == nil {
		var result struct {
			Stat []struct {
				Name  string      `json:"name"`
				Value json.Number `json:"value"`
			} `json:"stat"`
		}
		err = json.Unmarshal(b, &result)
		if err == nil {
			generation := strconv.Itoa(corePID("guangyue-xray.service"))
			for _, s := range result.Stat {
				parts := strings.Split(s.Name, ">>>")
				if len(parts) != 4 {
					continue
				}
				value, e := s.Value.Int64()
				if e != nil {
					continue
				}
				direction := "down"
				if parts[3] == "uplink" {
					direction = "up"
				}
				counters = append(counters, Counter{Key: "x:" + s.Name, Generation: generation, UserID: idFromEmail(parts[1]), Protocol: "vless", Direction: direction, Value: value})
			}
		}
	}
	if err != nil {
		errs = append(errs, errors.New("Xray traffic unavailable"))
	}
	b, err = a.hyRequest("GET", "/traffic", nil)
	if err == nil {
		var result map[string]struct {
			TX int64 `json:"tx"`
			RX int64 `json:"rx"`
		}
		err = json.Unmarshal(b, &result)
		if err == nil {
			generation := strconv.Itoa(corePID("guangyue-hy2.service"))
			for id, s := range result {
				uid, _ := strconv.ParseInt(strings.TrimPrefix(strings.SplitN(id, ".", 2)[0], "u"), 10, 64)
				// HY2 Tx/Rx is measured at the server-to-target side: Tx is upload.
				counters = append(counters, Counter{Key: "hy:" + id + ":upload", Generation: generation, UserID: uid, Protocol: "hy2", Direction: "up", Value: s.TX}, Counter{Key: "hy:" + id + ":download", Generation: generation, UserID: uid, Protocol: "hy2", Direction: "down", Value: s.RX})
			}
		}
	}
	if err != nil {
		errs = append(errs, errors.New("HY2 traffic unavailable"))
	}
	if err = a.store.account(counters); err != nil {
		return err
	}
	return errors.Join(errs...)
}

func validateNode(n Node) error {
	if n.Protocol != "vless" && n.Protocol != "hy2" {
		return errors.New("协议无效")
	}
	if n.Exit == "direct" {
		return nil
	}
	if n.Exit == "subscription" {
		if n.Upstream == nil || n.BridgePort < 21000 || n.BridgePort > 21255 || len(n.BridgePassword) < 16 {
			return errors.New("机场出口配置缺失，请重新导入")
		}
		return nil
	}
	if n.Exit != "http" && n.Exit != "socks5" {
		return errors.New("出口类型无效")
	}
	if n.Port < 1 || n.Port > 65535 || len(n.Host) > 253 || n.Host == "" || strings.ContainsAny(n.Host, " /\\\t\n\r?#@:") && net.ParseIP(n.Host) == nil {
		return errors.New("出口地址或端口无效")
	}
	if len(n.Username) > 128 || len(n.Password) > 256 || (n.Username == "") != (n.Password == "") {
		return errors.New("出口认证需同时填写用户名和密码")
	}
	return nil
}
func subscription(c Config, r Record, nodes []Node, format, protocol string) ([]byte, string, error) {
	return renderSubscription(c, r, nodes, format, protocol, false)
}

type subscriptionEntry struct {
	siteID   string
	siteName string
	node     Node
	name     string
	uri      string
	proxy    object
}

func subscriptionEntries(c Config, r Record, nodes []Node, protocol string) []subscriptionEntry {
	entries := []subscriptionEntry{}
	usedNames := map[string]int{}
	for _, n := range nodes {
		if !n.Enabled || (protocol != "" && protocol != n.Protocol) {
			continue
		}
		if n.Protocol == "vless" && (!r.VLESS || r.Credentials.VLESS[n.ID] == "") || n.Protocol == "hy2" && !r.HY2 {
			continue
		}
		if n.Protocol != "vless" && n.Protocol != "hy2" {
			continue
		}
		entry := subscriptionEntry{node: n}
		base := n.Name + " · " + strings.ToUpper(n.Protocol)
		if n.ManagedBy == publicManager {
			base += " · 公共"
		}
		usedNames[base]++
		name := base
		if usedNames[base] > 1 {
			name += fmt.Sprintf(" · %d", usedNames[base])
		}
		switch n.Protocol {
		case "vless":
			if !r.VLESS {
				continue
			}
			id := r.Credentials.VLESS[n.ID]
			if id == "" {
				continue
			}
			sni := effectiveRealitySNI(c, n)
			q := url.Values{"encryption": {"none"}, "security": {"reality"}, "sni": {sni}, "fp": {"chrome"}, "pbk": {c.RealityPublic}, "sid": {c.ShortID}, "type": {"tcp"}, "flow": {"xtls-rprx-vision"}}
			u := url.URL{Scheme: "vless", User: url.User(id), Host: net.JoinHostPort(c.VLESSHost, "443"), RawQuery: q.Encode(), Fragment: name}
			entry.uri = u.String()
			entry.proxy = object{"name": name, "type": "vless", "server": c.VLESSHost, "port": 443, "uuid": id, "network": "tcp", "tls": true, "udp": n.Exit != "http", "flow": "xtls-rprx-vision", "servername": sni, "client-fingerprint": "chrome", "reality-opts": object{"public-key": c.RealityPublic, "short-id": c.ShortID}}
		case "hy2":
			if !r.HY2 {
				continue
			}
			u := url.URL{Scheme: "hysteria2", User: url.User(hyNodePassword(r, n)), Host: net.JoinHostPort(c.HY2Host, "443"), RawQuery: url.Values{"sni": {c.HY2Host}}.Encode(), Fragment: name}
			entry.uri = u.String()
			entry.proxy = object{"name": name, "type": "hysteria2", "server": c.HY2Host, "port": 443, "password": hyNodePassword(r, n), "sni": c.HY2Host}
		}
		entry.name = name
		entries = append(entries, entry)
	}
	return entries
}

func renderSubscription(c Config, r Record, nodes []Node, format, protocol string, public bool) ([]byte, string, error) {
	return renderSubscriptionEntries(c, nodes, subscriptionEntries(c, r, nodes, protocol), format, public)
}

func renderSubscriptionEntries(c Config, nodes []Node, entries []subscriptionEntry, format string, public bool) ([]byte, string, error) {
	lines := []string{}
	proxies := []object{}
	names := []string{}
	for _, entry := range entries {
		lines = append(lines, entry.uri)
		proxies = append(proxies, entry.proxy)
		names = append(names, entry.name)
	}
	if len(lines) == 0 && !public {
		return nil, "", errNoNodes
	}
	raw := []byte{}
	if len(lines) > 0 {
		raw = []byte(strings.Join(lines, "\n") + "\n")
	}
	switch format {
	case "", "raw":
		return raw, "text/plain; charset=utf-8", nil
	case "base64":
		return []byte(encodeBase64(raw)), "text/plain; charset=utf-8", nil
	case "mihomo":
		fallback, group := "DIRECT", "广月"
		if public {
			fallback, group = "REJECT", "广月公共"
		}
		protectedDNS := false
		for _, entry := range entries {
			protectedDNS = protectedDNS || secureDNS(entry.node)
		}
		if protectedDNS {
			fallback = "REJECT"
		}
		names = append(names, fallback)
		doc := object{"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "warning", "proxies": proxies, "proxy-groups": []object{{"name": group, "type": "select", "proxies": names}}, "rules": []string{"MATCH," + group}}
		if protectedDNS {
			doc["ipv6"] = true
			doc["dns"] = object{"enable": true, "ipv6": true, "enhanced-mode": "fake-ip", "nameserver": []string{"tcp://1.1.1.1#" + group}, "fallback": []string{}, "proxy-server-nameserver": []string{"https://1.1.1.1/dns-query"}}
			doc["tun"] = object{"enable": false, "auto-route": true, "strict-route": true, "dns-hijack": []string{"any:53", "tcp://any:53"}}
			hosts := object{}
			for _, entry := range entries {
				n := entry.node
				if n.DefaultDirect && n.Exit == "direct" && publicIP(n.ProbeIP) {
					if host, ok := entry.proxy["server"].(string); ok {
						hosts[host] = n.ProbeIP
					}
				}
			}
			if len(hosts) > 0 {
				doc["hosts"] = hosts
			}
		}
		b, err := yaml.Marshal(doc)
		return b, "application/yaml; charset=utf-8", err
	default:
		return nil, "", fmt.Errorf("unsupported subscription format")
	}
}
