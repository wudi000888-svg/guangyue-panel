package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Clipboard text may be escaped by a chat/Markdown editor. Only undo escapes
// used in URI text, and unwrap a link when its visible text and target agree.
func cleanShareText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.NewReplacer(`\:`, ":", `\_`, "_", `\&`, "&", `\/`, "/").Replace(text)
	if strings.HasPrefix(text, "```") && strings.HasSuffix(text, "```") {
		if _, rest, ok := strings.Cut(text, "\n"); ok {
			text = strings.TrimSpace(strings.TrimSuffix(rest, "```"))
		}
	}
	text = strings.Trim(text, "`")
	if strings.HasPrefix(text, "<") && strings.HasSuffix(text, ">") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "<"), ">")
	}
	// Handle an inline peer link introduced by rich text, without accepting an
	// unrelated Markdown target as a credential or changing percent escapes.
	for i := 0; i < 4; i++ {
		start := strings.Index(text, "[")
		if start < 0 {
			break
		}
		tail := text[start+1:]
		middle := strings.Index(tail, "](")
		if middle < 0 {
			break
		}
		end := strings.Index(tail[middle+2:], ")")
		if end < 0 {
			break
		}
		label, target := tail[:middle], tail[middle+2:middle+2+end]
		visible := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://")
		replacement := ""
		if label == visible {
			replacement = label
		} else if strings.Contains(target, "://") && start == 0 && start+1+middle+2+end+1 == len(text) {
			replacement = target
		} else {
			break
		}
		text = text[:start] + replacement + tail[middle+2+end+1:]
	}
	return strings.TrimSpace(text)
}

func uriFlag(q url.Values, key string) (bool, bool, error) {
	values, ok := q[key]
	if !ok {
		return false, false, nil
	}
	if len(values) != 1 {
		return false, true, errors.New("节点链接含重复参数")
	}
	switch strings.ToLower(values[0]) {
	case "1", "true":
		return true, true, nil
	case "0", "false":
		return false, true, nil
	}
	return false, true, errors.New("节点开关参数必须为 0/1 或 true/false")
}
func parseVMessJSON(v object, fragment string) (object, error) {
	if header := str(v["type"]); header != "" && header != "none" {
		return nil, errors.New("暂不支持 VMess 伪装头")
	}
	tls := strings.ToLower(str(v["tls"]))
	if tls != "" && tls != "tls" && tls != "none" && tls != "false" && tls != "0" && tls != "true" && tls != "1" {
		return nil, errors.New("VMess TLS 参数无效")
	}
	name := str(v["ps"])
	if fragment != "" {
		name = fragment
	}
	p := object{"type": "vmess", "name": name, "server": str(v["add"]), "port": number(v["port"]), "uuid": str(v["id"]), "alterId": number(v["aid"]), "cipher": str(v["scy"]), "network": str(v["net"]), "tls": tls == "tls" || tls == "true" || tls == "1", "udp": true}
	if p["network"] == "" {
		p["network"] = "tcp"
	}
	if p["network"] == "http" {
		p["network"] = "h2"
	}
	if sni := str(v["sni"]); sni != "" {
		p["servername"] = sni
	}
	if alpn := str(v["alpn"]); alpn != "" {
		p["alpn"] = strings.Split(alpn, ",")
	}
	if fp := str(v["fp"]); fp != "" {
		p["client-fingerprint"] = fp
	}
	switch str(p["network"]) {
	case "ws":
		headers := object{}
		if str(v["host"]) != "" {
			headers["Host"] = str(v["host"])
		}
		p["ws-opts"] = object{"path": str(v["path"]), "headers": headers}
	case "grpc":
		p["grpc-opts"] = object{"grpc-service-name": str(v["path"])}
	case "h2":
		p["h2-opts"] = object{"path": str(v["path"]), "host": strings.Split(str(v["host"]), ",")}
	}
	return p, nil
}

func parseURI(line string) (object, error) {
	line = cleanShareText(line)
	scheme, body, ok := strings.Cut(line, "://")
	if !ok {
		return nil, errors.New("节点链接格式无效")
	}
	scheme = strings.ToLower(scheme)
	if scheme == "hy2" {
		scheme = "hysteria2"
	}
	if scheme == "socks" {
		scheme = "socks5"
	}
	payload, fragmentRaw, _ := strings.Cut(body, "#")
	fragment, e := url.PathUnescape(fragmentRaw)
	if e != nil {
		return nil, errors.New("节点名称编码无效")
	}
	authority, query, _ := strings.Cut(payload, "?")
	q, e := url.ParseQuery(query)
	if e != nil {
		return nil, errors.New("节点链接参数编码无效")
	}
	for _, values := range q {
		if len(values) > 1 {
			return nil, errors.New("节点链接含重复参数")
		}
	}
	// Shadowrocket encodes method:credential@server:port as a single Base64
	// authority. SIP002 SS may instead encode only method:password as userinfo.
	encoded := false
	var u *url.URL
	if !strings.Contains(authority, "@") && (scheme == "vless" || scheme == "vmess" || scheme == "ss") {
		b, err := unbase(authority)
		if err != nil {
			return nil, errors.New("节点链接的 Base64 认证格式无效")
		}
		if scheme == "vmess" {
			v := object{}
			if json.Unmarshal(b, &v) == nil && v != nil {
				if len(q) > 0 {
					return nil, errors.New("VMess JSON 链接不应附加连接参数")
				}
				return parseVMessJSON(v, fragment)
			}
		}
		decoded := string(b)
		at := strings.LastIndex(decoded, "@")
		if at < 1 || strings.ContainsAny(decoded, "\r\n\x00") {
			return nil, errors.New("节点链接的编码地址无效")
		}
		credential, address := decoded[:at], decoded[at+1:]
		user, pass, hasPass := strings.Cut(credential, ":")
		u = &url.URL{Scheme: scheme, Host: address}
		u.User = url.User(user)
		if hasPass {
			u.User = url.UserPassword(user, pass)
		}
		encoded = true
	} else {
		u, e = url.Parse(scheme + "://" + authority)
		if e != nil {
			return nil, errors.New("节点地址格式无效")
		}
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("节点地址含多余路径，请通过 path 参数指定传输路径")
	}
	if u.User == nil && scheme != "http" && scheme != "socks5" {
		return nil, errors.New("节点链接缺少认证信息")
	}
	common := "remarks remark udp "
	allowed := map[string]string{
		"vless":     "security sni peer fp pbk sid type flow encryption path host serviceName alpn insecure allowInsecure tls xtls headerType obfs obfsParam",
		"vmess":     "security sni peer fp type path host serviceName alpn insecure allowInsecure tls headerType obfs obfsParam alterId aid cipher verify_cert",
		"trojan":    "security sni peer fp type path host serviceName alpn insecure allowInsecure tls headerType obfs obfsParam",
		"hysteria2": "sni peer alpn insecure allowInsecure obfs obfs-password upmbps downmbps tls",
		"ss":        "", "http": "tls sni peer insecure allowInsecure", "socks5": "tls insecure allowInsecure"}
	keys, supported := allowed[scheme]
	if !supported {
		return nil, errors.New("暂不支持此协议")
	}
	for key := range q {
		if !strings.Contains(" "+common+keys+" ", " "+key+" ") {
			return nil, errors.New("链接含暂不支持的参数")
		}
	}
	if header := q.Get("headerType"); header != "" && header != "none" {
		return nil, errors.New("暂不支持此 headerType 伪装类型")
	}
	name := fragment
	if name == "" {
		name = q.Get("remarks")
	}
	if name == "" {
		name = q.Get("remark")
	}
	p := object{"type": scheme, "name": name, "server": u.Hostname(), "port": number(u.Port()), "udp": true}
	if u.Port() == "" && (scheme == "trojan" || scheme == "hysteria2") {
		p["port"] = 443
	}
	user, pass, hasPass := "", "", false
	if u.User != nil {
		user = u.User.Username()
		pass, hasPass = u.User.Password()
	}
	switch scheme {
	case "vless":
		if !hasPass {
			if b, err := unbase(user); err == nil && strings.HasPrefix(string(b), "none:") {
				user, pass, hasPass = "none", strings.TrimPrefix(string(b), "none:"), true
			}
		}
		if hasPass {
			if user != "none" {
				return nil, errors.New("VLESS 认证前缀必须为 none")
			}
			user = pass
		}
		p["uuid"] = user
		if q.Get("encryption") != "" && q.Get("encryption") != "none" {
			return nil, errors.New("暂不支持此 VLESS 加密方式")
		}
		flow := q.Get("flow")
		if xtls := q.Get("xtls"); xtls != "" {
			switch xtls {
			case "0":
				if flow != "" {
					return nil, errors.New("flow 与 xtls 参数冲突")
				}
			case "2":
				if flow != "" && flow != "xtls-rprx-vision" {
					return nil, errors.New("flow 与 xtls 参数冲突")
				}
				flow = "xtls-rprx-vision"
			default:
				return nil, errors.New("仅支持 XTLS Vision（xtls=2），旧 XTLS 模式暂不支持")
			}
		}
		if flow != "" {
			p["flow"] = flow
		}
	case "vmess":
		cipher := q.Get("cipher")
		if hasPass {
			if cipher != "" && cipher != user {
				return nil, errors.New("VMess 加密参数冲突")
			}
			cipher, user = user, pass
		}
		if cipher == "" {
			cipher = "auto"
		}
		p["uuid"], p["cipher"], p["alterId"] = user, cipher, number(q.Get("alterId"))
		if q.Get("aid") != "" {
			p["alterId"] = number(q.Get("aid"))
		}
	case "ss":
		if !hasPass {
			b, err := unbase(user)
			if err != nil {
				return nil, err
			}
			parts := strings.SplitN(string(b), ":", 2)
			if len(parts) != 2 {
				return nil, errors.New("SS 认证格式无效")
			}
			user, pass = parts[0], parts[1]
		}
		p["cipher"], p["password"] = user, pass
	case "trojan", "hysteria2":
		if hasPass {
			user += ":" + pass
		}
		p["password"] = user
	case "http", "socks5":
		if user != "" {
			p["username"], p["password"] = user, pass
		}
	}
	security := q.Get("security")
	if security != "" && security != "none" && security != "tls" && security != "reality" {
		return nil, errors.New("暂不支持此安全选项")
	}
	tls, tlsSet, err := uriFlag(q, "tls")
	if err != nil {
		return nil, err
	}
	if scheme == "vless" && q.Get("pbk") != "" {
		if security == "none" || tlsSet && !tls {
			return nil, errors.New("Reality 公钥与 TLS 开关冲突")
		}
		security = "reality"
	}
	if tlsSet {
		if security != "" && tls != (security != "none") {
			return nil, errors.New("TLS 与 security 参数冲突")
		}
		if security == "" && tls {
			security = "tls"
		}
	}
	if security == "reality" && scheme != "vless" {
		return nil, errors.New("此协议不支持 Reality 参数")
	}
	switch scheme {
	case "vless", "vmess":
		p["tls"] = security == "tls" || security == "reality"
	case "trojan", "hysteria2":
		if tlsSet && !tls || security == "none" {
			return nil, errors.New("此协议必须启用 TLS")
		}
	case "http", "socks5":
		if tls {
			p["tls"] = true
		}
	}
	if security == "reality" {
		if q.Get("pbk") == "" {
			return nil, errors.New("缺少 Reality 公钥")
		}
		p["reality-opts"] = object{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
	}
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("peer")
	}
	if sni != "" {
		key := "sni"
		if scheme == "vless" || scheme == "vmess" {
			key = "servername"
		}
		p[key] = sni
	}
	if fp := q.Get("fp"); fp != "" {
		p["client-fingerprint"] = fp
	} else if security == "reality" {
		p["client-fingerprint"] = "chrome"
	}
	if alpn := q.Get("alpn"); alpn != "" {
		p["alpn"] = strings.Split(alpn, ",")
	}
	insecure, seen := false, false
	for _, key := range []string{"insecure", "allowInsecure", "verify_cert"} {
		flag, present, e := uriFlag(q, key)
		if e != nil {
			return nil, e
		}
		if present {
			if key == "verify_cert" {
				flag = !flag
			}
			if seen && insecure != flag {
				return nil, errors.New("证书验证参数冲突")
			}
			insecure, seen = flag, true
		}
	}
	if insecure {
		p["skip-cert-verify"] = true
	}
	if udp, present, e := uriFlag(q, "udp"); e != nil {
		return nil, e
	} else if present {
		p["udp"] = udp
	}
	if scheme == "hysteria2" {
		for _, key := range []string{"obfs", "obfs-password"} {
			if q.Get(key) != "" {
				p[key] = q.Get(key)
			}
		}
		if q.Get("upmbps") != "" {
			p["up"] = q.Get("upmbps")
		}
		if q.Get("downmbps") != "" {
			p["down"] = q.Get("downmbps")
		}
	}
	if scheme == "vless" || scheme == "vmess" || scheme == "trojan" {
		network := q.Get("type")
		if network == "" {
			network = q.Get("obfs")
		}
		if network == "" || network == "none" {
			network = "tcp"
		}
		if network == "websocket" {
			network = "ws"
		}
		p["network"] = network
		host := q.Get("host")
		if host == "" {
			host = q.Get("obfsParam")
		}
		headers := object{}
		if host != "" {
			if strings.HasPrefix(host, "{") {
				if json.Unmarshal([]byte(host), &headers) != nil {
					return nil, errors.New("WebSocket 请求头格式无效")
				}
				for _, v := range headers {
					if _, ok := v.(string); !ok {
						return nil, errors.New("WebSocket 请求头格式无效")
					}
				}
			} else {
				headers["Host"] = host
			}
		}
		switch network {
		case "ws":
			p["ws-opts"] = object{"path": q.Get("path"), "headers": headers}
		case "grpc":
			service := q.Get("serviceName")
			if service == "" && (encoded || q.Has("obfs")) {
				service = q.Get("path")
			}
			p["grpc-opts"] = object{"grpc-service-name": service}
		case "h2":
			if str(headers["Host"]) != "" {
				host = str(headers["Host"])
			}
			p["h2-opts"] = object{"path": q.Get("path"), "host": strings.Split(host, ",")}
		}
	}
	return p, nil
}
