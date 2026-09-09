package controlplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const nodeDNSAddress = "127.0.0.1:19186"

type NodeDNS struct {
	Mode string `json:"mode"`
	DoH  string `json:"doh,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

func defaultNodeDNS() *NodeDNS {
	return &NodeDNS{Mode: "secure", DoH: "https://1.1.1.1/dns-query", IPv6: "block"}
}
func secureDNS(n Node) bool { return n.DNS != nil && n.DNS.Mode == "secure" }
func validateNodeDNS(d *NodeDNS) error {
	if d == nil {
		return nil
	}
	if d.Mode == "system" {
		d.DoH, d.IPv6 = "", ""
		return nil
	}
	if d.Mode != "secure" || d.IPv6 != "block" && d.IPv6 != "allow" {
		return errors.New("DNS 模式或 IPv6 策略无效")
	}
	u, err := url.Parse(d.DoH)
	if err != nil || u.Scheme != "https" || u.User != nil || !publicIP(u.Hostname()) || u.Fragment != "" || u.RawQuery != "" || u.Port() != "" && u.Port() != "443" || u.Path == "" || len(d.DoH) > 256 {
		return errors.New("加密 DNS 请填写带公网 IPv4 或 IPv6 地址的 HTTPS URL，不含认证或查询参数")
	}
	return nil
}
func dnsCredential(c Config, n Node) string {
	return digest("node-dns/v1/" + c.StatsSecret + "/" + n.ID)
}
func coreExit(c Config, n Node) Node {
	if secureDNS(n) {
		n.Exit = "socks5"
		n.Host = "127.0.0.1"
		n.Port = 19186
		n.Username = n.ID
		n.Password = dnsCredential(c, n)
		return n
	}
	return effectiveExit(n)
}

type dnsCacheEntry struct {
	wire  []byte
	until time.Time
}
type exitResolver struct {
	node      Node
	transport *http.Transport
	client    *http.Client
	mu        sync.Mutex
	cache     map[string]dnsCacheEntry
	slots     chan struct{}
}

func newExitResolver(n Node, slots chan struct{}) (*exitResolver, error) {
	t, err := egressTransport(n)
	if err != nil {
		return nil, err
	}
	t.MaxResponseHeaderBytes = 16 << 10
	t.MaxIdleConns = 2
	t.MaxIdleConnsPerHost = 2
	t.IdleConnTimeout = 30 * time.Second
	return &exitResolver{node: n, transport: t, client: &http.Client{Transport: t, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cache: map[string]dnsCacheEntry{}, slots: slots}, nil
}
func dnsError(wire []byte, rcode dnsmessage.RCode) []byte {
	var m dnsmessage.Message
	if m.Unpack(wire) != nil {
		return nil
	}
	m.Header.Response = true
	m.Header.RCode = rcode
	m.Header.RecursionAvailable = true
	m.Answers = nil
	m.Authorities = nil
	m.Additionals = nil
	out, _ := m.Pack()
	return out
}
func (d *exitResolver) exchange(ctx context.Context, wire []byte) ([]byte, error) {
	var query dnsmessage.Message
	if len(wire) > 4096 || query.Unpack(wire) != nil || query.Header.Response || query.Header.OpCode != 0 || len(query.Questions) != 1 || query.Questions[0].Class != dnsmessage.ClassINET {
		return nil, errors.New("invalid DNS query")
	}
	question := query.Questions[0]
	if d.node.DNS.IPv6 == "block" && (question.Type == dnsmessage.TypeAAAA || question.Type == 64 || question.Type == 65) {
		return dnsError(wire, dnsmessage.RCodeSuccess), nil
	}
	// Cache only ordinary A/AAAA lookups; other flags and EDNS options must keep
	// their semantics. IDs are restored per caller, never shared across clients.
	cacheable := (question.Type == dnsmessage.TypeA || question.Type == dnsmessage.TypeAAAA) && len(query.Additionals) == 0
	key := string(wire[2:])
	d.mu.Lock()
	cached, ok := d.cache[key]
	d.mu.Unlock()
	if cacheable && ok && time.Now().Before(cached.until) {
		var m dnsmessage.Message
		_ = m.Unpack(cached.wire)
		m.Header.ID = query.Header.ID
		ttl := uint32(time.Until(cached.until).Seconds())
		for i := range m.Answers {
			if m.Answers[i].Header.TTL > ttl {
				m.Answers[i].Header.TTL = ttl
			}
		}
		out, _ := m.Pack()
		return out, nil
	}
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	default:
		return nil, errors.New("DNS capacity reached")
	}
	request, err := http.NewRequestWithContext(ctx, "POST", d.node.DNS.DoH, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	response, err := d.client.Do(request)
	if err != nil {
		return nil, errors.New("egress DNS unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/dns-message") {
		return nil, errors.New("egress DNS rejected")
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil || len(answer) > 65535 {
		return nil, errors.New("invalid DNS response size")
	}
	var parsed dnsmessage.Message
	if parsed.Unpack(answer) != nil || !parsed.Header.Response || parsed.Header.ID != query.Header.ID || len(parsed.Questions) != 1 || parsed.Questions[0] != question {
		return nil, errors.New("mismatched DNS response")
	}
	// Strip unsolicited IPv6 answers as well as directly queried AAAA responses.
	if d.node.DNS.IPv6 == "block" {
		filter := func(rows []dnsmessage.Resource) []dnsmessage.Resource {
			out := rows[:0]
			for _, r := range rows {
				if r.Header.Type != dnsmessage.TypeAAAA && r.Header.Type != 64 && r.Header.Type != 65 {
					out = append(out, r)
				}
			}
			return out
		}
		parsed.Answers = filter(parsed.Answers)
		parsed.Authorities = filter(parsed.Authorities)
		parsed.Additionals = filter(parsed.Additionals)
		answer, err = parsed.Pack()
		if err != nil {
			return nil, err
		}
	}
	if cacheable && len(answer) <= 4096 && parsed.Header.RCode == dnsmessage.RCodeSuccess && len(parsed.Answers) > 0 {
		ttl := uint32(60)
		for _, r := range parsed.Answers {
			if r.Header.TTL < ttl {
				ttl = r.Header.TTL
			}
		}
		if ttl > 0 {
			d.mu.Lock()
			if len(d.cache) >= 64 {
				clear(d.cache)
			}
			d.cache[key] = dnsCacheEntry{wire: append([]byte{}, answer...), until: time.Now().Add(time.Duration(ttl) * time.Second)}
			d.mu.Unlock()
		}
	}
	return answer, nil
}
func (d *exitResolver) addresses(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip.String()) || d.node.DNS.IPv6 == "block" && ip.To4() == nil {
			return nil, errors.New("address blocked")
		}
		return []net.IP{ip}, nil
	}
	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, err
	}
	types := []dnsmessage.Type{dnsmessage.TypeA}
	if d.node.DNS.IPv6 == "allow" {
		types = append(types, dnsmessage.TypeAAAA)
	}
	addresses := []net.IP{}
	for _, kind := range types {
		query := dnsmessage.Message{Header: dnsmessage.Header{ID: 1, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: kind, Class: dnsmessage.ClassINET}}}
		wire, _ := query.Pack()
		reply, err := d.exchange(ctx, wire)
		if err != nil {
			continue
		}
		var answer dnsmessage.Message
		if answer.Unpack(reply) != nil {
			continue
		}
		for _, r := range answer.Answers {
			var ip net.IP
			switch v := r.Body.(type) {
			case *dnsmessage.AResource:
				ip = net.IP(v.A[:])
			case *dnsmessage.AAAAResource:
				ip = net.IP(v.AAAA[:])
			}
			if ip != nil && publicIP(ip.String()) {
				addresses = append(addresses, ip)
			}
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("egress DNS returned no usable address")
	}
	return addresses, nil
}
