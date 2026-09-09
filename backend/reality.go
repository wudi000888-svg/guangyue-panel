package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const realitySocketDir = "/run/guangyue-reality"

func effectiveRealitySNI(c Config, n Node) string {
	if n.RealitySNI != "" {
		return n.RealitySNI
	}
	return c.RealitySNI
}

// The same bounded lowercase hostname is also used as a Unix socket basename.
// Keep this grammar and limit aligned with the fixed Nginx stream map.
func normalizeRealitySNI(value string, c Config) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if len(value) > 63 || net.ParseIP(value) != nil || !strings.Contains(value, ".") {
		return "", errors.New("SNI 必须是最多 63 个 ASCII 字符的公网域名")
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("SNI 域名格式无效，请勿填写协议、端口或路径")
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return "", errors.New("SNI 域名格式无效，请勿填写协议、端口或路径")
			}
		}
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".onion", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(value, suffix) {
			return "", errors.New("SNI 不能使用内部或保留域名")
		}
	}
	for _, host := range realityInstallationHosts(c) {
		if value == host {
			return "", errors.New("SNI 不能指向本站面板或节点域名")
		}
	}
	if value == strings.ToLower(c.RealitySNI) {
		return "", nil
	}
	return value, nil
}

func realityInstallationHosts(c Config) []string {
	hosts := []string{c.VLESSHost, c.HY2Host}
	if u, err := url.Parse(c.PublicURL); err == nil {
		hosts = append(hosts, u.Hostname())
		// The installation's apex serves its HTTPS redirect and must not
		// collide with a static Nginx website mapping either.
		if apex, err := publicsuffix.EffectiveTLDPlusOne(u.Hostname()); err == nil && apex != u.Hostname() {
			hosts = append(hosts, apex)
		}
	}
	for i := range hosts {
		hosts[i] = strings.ToLower(strings.TrimSuffix(hosts[i], "."))
	}
	return hosts
}

type realityResolver func(context.Context, string) ([]net.IPAddr, error)
type realityTLSProbe func(context.Context, string, string) error

func checkRealityTLS(ctx context.Context, sni, ip string) error {
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 4 * time.Second}, Config: &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}}}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "h2" {
		return errors.New("target does not support TLS 1.3 and HTTP/2")
	}
	return nil
}

// Resolve and check the complete answer before dialing any address. Only the
// verified IP is persisted and rendered as Reality's target, closing DNS rebinding.
func resolveRealityTarget(ctx context.Context, c Config, nodes []Node, sni string, lookup realityResolver, probe realityTLSProbe) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	forbidden := map[string]bool{}
	knownInstallationIP := false
	add := func(ip net.IP, installation bool) {
		if ip != nil {
			forbidden[ip.String()] = true
			knownInstallationIP = knownInstallationIP || installation && publicIP(ip.String())
		}
	}
	for _, n := range nodes {
		if n.Exit == "direct" {
			add(net.ParseIP(n.ProbeIP), true)
		}
	}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			ip, _, _ := net.ParseCIDR(address.String())
			add(ip, true)
		}
	}
	for _, host := range realityInstallationHosts(c) {
		ips, _ := lookup(ctx, host)
		for _, ip := range ips {
			add(ip.IP, true)
		}
	}
	if !knownInstallationIP {
		return "", errors.New("无法确认本站公网地址，请稍后重试")
	}
	ips, err := lookup(ctx, sni)
	if err != nil || len(ips) == 0 {
		return "", errors.New("SNI 域名解析失败")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP.String()) {
			return "", errors.New("SNI 必须全部解析到公网地址")
		}
		if forbidden[ip.IP.String()] {
			return "", errors.New("SNI 不能指向本机或本站服务地址")
		}
	}
	// Prefer IPv4, matching this VPS's normal direct route; still validate every
	// answer above, and try IPv6 when IPv4 cannot establish a verified handshake.
	sort.SliceStable(ips, func(i, j int) bool { return ips[i].IP.To4() != nil && ips[j].IP.To4() == nil })
	for _, ip := range ips {
		attempt, stop := context.WithTimeout(ctx, 4*time.Second)
		err = probe(attempt, sni, ip.IP.String())
		stop()
		if err == nil {
			return ip.IP.String(), nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return "", errors.New("SNI 目标未通过证书、TLS 1.3 和 HTTP/2 验证")
}

type realityInboundGroup struct {
	SNI, Target, Tag, Listen string
	Port                     int
}

func realityGroups(c Config, nodes []Node) []realityInboundGroup {
	groups := []realityInboundGroup{{SNI: c.RealitySNI, Target: c.RealityTarget, Tag: "vless-in", Listen: "127.0.0.1", Port: 18443}}
	seen := map[string]bool{c.RealitySNI: true}
	for _, n := range nodes {
		if n.Protocol != "vless" || !n.Enabled || n.RealitySNI == "" || seen[n.RealitySNI] {
			continue
		}
		seen[n.RealitySNI] = true
		groups = append(groups, realityInboundGroup{SNI: n.RealitySNI, Target: net.JoinHostPort(n.RealityIP, "443"), Tag: realityGroupTag(c, n), Listen: filepath.Join(realitySocketDir, n.RealitySNI+".sock") + ",0660"})
	}
	sort.Slice(groups[1:], func(i, j int) bool { return groups[i+1].SNI < groups[j+1].SNI })
	return groups
}

func realityGroupTag(c Config, n Node) string {
	if n.RealitySNI == "" || n.RealitySNI == c.RealitySNI {
		return "vless-in"
	}
	return "vless-sni-" + digest(n.RealitySNI)[:16]
}

func validateRealityNodes(c Config, nodes []Node) error {
	targets := map[string]string{}
	for _, n := range nodes {
		if n.RealitySNI == "" {
			continue
		}
		normalized, err := normalizeRealitySNI(n.RealitySNI, c)
		if n.Protocol != "vless" || err != nil || normalized != n.RealitySNI || !publicIP(n.RealityIP) {
			return errors.New("节点 SNI 配置无效，请重新保存节点")
		}
		if old, exists := targets[n.RealitySNI]; exists && old != n.RealityIP {
			return errors.New("相同 SNI 的目标地址不一致，请重新保存节点")
		}
		targets[n.RealitySNI] = n.RealityIP
	}
	return nil
}
