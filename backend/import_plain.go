package main

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func nativeImportNode(item importItem) Node {
	return Node{Protocol: "vless", Enabled: true, Exit: str(item.Proxy["type"]), Host: str(item.Proxy["server"]), Port: number(item.Proxy["port"]), Username: str(item.Proxy["username"]), Password: str(item.Proxy["password"])}
}

// Parse bare exported credentials before any share-link Markdown cleanup.
// SplitN preserves colons, escapes and URL-like text inside the password.
func plainProxyAddress(line string) (host, tail string, found bool) {
	line = strings.TrimLeft(line, " \t")
	host, tail, found = strings.Cut(line, ":")
	if strings.HasPrefix(line, "[") {
		host, tail, found = strings.Cut(line[1:], "]:")
	}
	return host, tail, found && net.ParseIP(host) != nil
}

func parsePlainProxy(line, protocol string) (importItem, error) {
	invalid := errors.New("代理行格式应为 IP:端口 或 IP:端口:账号:密码")
	host, tail, found := plainProxyAddress(line)
	if !found {
		return importItem{}, invalid
	}
	parts := strings.SplitN(tail, ":", 3)
	if len(parts) == 2 || parts[0] == "" {
		return importItem{}, invalid
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return importItem{}, errors.New("代理端口必须为 1–65535 的整数")
		}
	}
	port, err := strconv.Atoi(parts[0])
	if err != nil || port < 1 || port > 65535 {
		return importItem{}, errors.New("代理端口必须为 1–65535 的整数")
	}
	username, password := "", ""
	if len(parts) == 3 {
		username, password = parts[1], parts[2]
		if username == "" || password == "" || len(username) > 128 || len(password) > 256 || strings.IndexFunc(username+password, unicode.IsControl) >= 0 {
			return importItem{}, errors.New("代理账号或密码为空、过长或含控制字符")
		}
	}
	item := importItem{Label: strings.ToUpper(protocol) + " · " + net.ParseIP(host).String(), Proxy: object{"type": protocol, "server": net.ParseIP(host).String(), "port": port, "username": username, "password": password}, Native: true}
	if err := validateNode(nativeImportNode(item)); err != nil {
		return importItem{}, errors.New("代理地址或认证参数无效")
	}
	return item, nil
}

func importURIText(line string) bool {
	scheme, _, found := strings.Cut(cleanShareText(line), "://")
	return found && scheme != "" && !strings.ContainsAny(scheme, ": \t")
}

// Content imports accept bare export files as native exits. Managed subscription
// refreshes continue using parseSubscription and its existing bridge semantics.
func parseImportContent(body []byte, protocol string) ([]importItem, []importWarning, error) {
	if len(body) > 4<<20 {
		return nil, nil, errors.New("订阅最大 4 MiB")
	}
	if protocol == "" {
		protocol = "http"
	}
	if protocol != "http" && protocol != "socks5" {
		return nil, nil, errors.New("普通代理文件协议仅支持 HTTP 或 SOCKS5")
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	wrapped := strings.TrimSpace(text)
	_, _, bareStart := plainProxyAddress(strings.SplitN(wrapped, "\n", 2)[0])
	if !bareStart && (strings.HasPrefix(wrapped, "`") || strings.HasPrefix(wrapped, "<") && strings.HasSuffix(wrapped, ">") || strings.HasPrefix(wrapped, "[") && strings.Contains(wrapped, "](")) {
		return parseSubscription(body)
	}
	// A whole-content Base64 representation has no colon. Decode at most once.
	if !strings.Contains(text, ":") {
		if decoded, err := unbase(text); err == nil && utf8.Valid(decoded) && strings.Contains(string(decoded), ":") {
			text = strings.TrimPrefix(string(decoded), "\ufeff")
		}
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		return parseSubscription([]byte(text))
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "proxies:") {
			return parseSubscription([]byte(text))
		}
	}
	items, warnings := []importItem{}, []importWarning{}
	candidates := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		candidates++
		if candidates > 256 {
			return nil, nil, errors.New("单次最多导入 256 个节点")
		}
		var item importItem
		var err error
		_, _, bare := plainProxyAddress(line)
		if bare {
			item, err = parsePlainProxy(line, protocol)
		} else if importURIText(line) {
			var proxy object
			proxy, err = parseURI(line)
			if err == nil {
				item, err = normalizeProxy(proxy)
			}
		} else {
			item, err = parsePlainProxy(line, protocol)
		}
		if err != nil {
			warnings = append(warnings, importWarning{Index: index + 1, Reason: err.Error()})
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, warnings, errors.New("订阅中没有可导入的节点")
	}
	return items, warnings, nil
}
