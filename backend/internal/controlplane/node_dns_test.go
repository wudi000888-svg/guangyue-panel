package controlplane

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
)

func testDNSWire(t *testing.T, kind dnsmessage.Type) []byte {
	t.Helper()
	name, _ := dnsmessage.NewName("egress-only.invalid.")
	wire, err := (&dnsmessage.Message{Header: dnsmessage.Header{ID: 41, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: kind, Class: dnsmessage.ClassINET}}}).Pack()
	if err != nil {
		t.Fatal(err)
	}
	return wire
}
func dnsGatewayFixture(t *testing.T, ipv6 string) (*nodeGateway, Node, *atomic.Int32, *atomic.Bool, *sync.Map) {
	t.Helper()
	count := &atomic.Int32{}
	fail := &atomic.Bool{}
	seen := &sync.Map{}
	doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if fail.Load() {
			http.Error(w, "failure", 503)
			return
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		var m dnsmessage.Message
		if m.Unpack(b) != nil {
			w.WriteHeader(400)
			return
		}
		m.Header.Response = true
		m.Header.RecursionAvailable = true
		q := m.Questions[0]
		resource := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 60}}
		if q.Type == dnsmessage.TypeAAAA {
			var ip [16]byte
			copy(ip[:], net.ParseIP("2606:4700:4700::1111").To16())
			resource.Body = &dnsmessage.AAAAResource{AAAA: ip}
		} else {
			resource.Body = &dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}}
		}
		m.Answers = []dnsmessage.Resource{resource}
		out, _ := m.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(out)
	}))
	t.Cleanup(doh.Close)
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			w.WriteHeader(405)
			return
		}
		seen.Store(r.Host, true)
		target := echo.Addr().String()
		if r.Host == "1.1.1.1:443" {
			target = doh.Listener.Addr().String()
		}
		remote, e := net.DialTimeout("tcp", target, time.Second)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		client, buffer, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		go func() { defer client.Close(); defer remote.Close(); relayConnections(client, remote) }()
	}))
	t.Cleanup(upstream.Close)
	host, portText, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	node := Node{ID: "test-node", Protocol: "vless", Enabled: true, Exit: "http", Host: host, Port: port, DNS: defaultNodeDNS()}
	node.DNS.IPv6 = ipv6
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	gateway := newNodeGateway(listener)
	t.Cleanup(gateway.close)
	if err := gateway.update(Config{StatsSecret: "fixture"}, []Node{node}); err != nil {
		t.Fatal(err)
	}
	gateway.routes[node.ID].resolver.transport.TLSClientConfig = doh.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	gateway.routes[node.ID].resolver.transport.TLSClientConfig.ServerName = doh.Certificate().DNSNames[0]
	return gateway, node, count, fail, seen
}
func testGatewayDial(t *testing.T, g *nodeGateway, n Node, address string) (net.Conn, error) {
	t.Helper()
	d, e := proxy.SOCKS5("tcp", g.listener.Addr().String(), &proxy.Auth{User: n.ID, Password: dnsCredential(Config{StatsSecret: "fixture"}, n)}, &net.Dialer{Timeout: 2 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return d.(proxy.ContextDialer).DialContext(ctx, "tcp", address)
}
func testGatewayDNS(t *testing.T, g *nodeGateway, n Node, address string, kind dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	c, e := testGatewayDial(t, g, n, address)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	wire := testDNSWire(t, kind)
	prefix := []byte{byte(len(wire) >> 8), byte(len(wire))}
	c.Write(append(prefix, wire...))
	if _, e = io.ReadFull(c, prefix); e != nil {
		t.Fatal(e)
	}
	body := make([]byte, binary.BigEndian.Uint16(prefix))
	if _, e = io.ReadFull(c, body); e != nil {
		t.Fatal(e)
	}
	var m dnsmessage.Message
	if m.Unpack(body) != nil {
		t.Fatal("invalid wire response")
	}
	return m
}
func TestNodeDNSQueriesAndTrafficStayOnSelectedHTTPExit(t *testing.T) {
	g, n, count, fail, seen := dnsGatewayFixture(t, "block")
	a := testGatewayDNS(t, g, n, "8.8.8.8:53", dnsmessage.TypeA)
	if len(a.Answers) != 1 || count.Load() != 1 {
		t.Fatal("A query did not reach DoH via exit")
	}
	aaaa := testGatewayDNS(t, g, n, "[2001:4860:4860::8888]:53", dnsmessage.TypeAAAA)
	if len(aaaa.Answers) != 0 || aaaa.Header.RCode != 0 || count.Load() != 1 {
		t.Fatal("blocked AAAA leaked to an external DNS")
	}
	if _, ok := seen.Load("8.8.8.8:53"); ok {
		t.Fatal("original DNS destination was contacted")
	}
	c, err := testGatewayDial(t, g, n, "egress-only.invalid:443")
	if err != nil {
		t.Fatal("domain target did not resolve through exit", err)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte("hello"))
	b := make([]byte, 5)
	_, err = io.ReadFull(c, b)
	c.Close()
	if err != nil || string(b) != "hello" {
		t.Fatal("resolved TCP data path failed")
	}
	if _, ok := seen.Load("93.184.216.34:443"); !ok {
		t.Fatal("proxy resolved the original domain instead of selected DoH")
	}
	if c, err := testGatewayDial(t, g, n, "[2606:4700:4700::1111]:443"); err == nil {
		c.Close()
		t.Fatal("IPv6 literal bypassed policy")
	}
	fail.Store(true)
	d := g.routes[n.ID].resolver
	d.mu.Lock()
	clear(d.cache)
	d.mu.Unlock()
	if c, err := testGatewayDial(t, g, n, "no-local-fallback.invalid:443"); err == nil {
		c.Close()
		t.Fatal("failed DoH fell back")
	}
	if _, ok := seen.Load("no-local-fallback.invalid:443"); ok {
		t.Fatal("failed DNS domain sent to proxy")
	}
	denied := testGatewayDNS(t, g, n, "8.8.4.4:53", dnsmessage.TypeA)
	if denied.Header.RCode != dnsmessage.RCodeServerFailure {
		t.Fatal("failed DoH did not fail closed")
	}
}
func TestNodeDNSIPv6AllowedAndNodeRevocation(t *testing.T) {
	g, n, _, _, seen := dnsGatewayFixture(t, "allow")
	aaaa := testGatewayDNS(t, g, n, "[2001:4860:4860::8888]:53", dnsmessage.TypeAAAA)
	if len(aaaa.Answers) != 1 || aaaa.Answers[0].Header.Type != dnsmessage.TypeAAAA {
		t.Fatal("AAAA not relayed")
	}
	c, err := testGatewayDial(t, g, n, "[2606:4700:4700::1111]:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := seen.Load("[2606:4700:4700::1111]:443"); !ok {
		t.Fatal("IPv6 did not use HTTP exit")
	}
	if err := g.update(Config{StatsSecret: "fixture"}, nil); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("deleted node connection survived")
	}
	if c, err := testGatewayDial(t, g, n, "93.184.216.34:443"); err == nil {
		c.Close()
		t.Fatal("deleted gateway node authenticated")
	}
}
func TestNodeDNSUDP53InterceptsBothAddressFamilies(t *testing.T) {
	g, n, count, _, _ := dnsGatewayFixture(t, "block")
	c, err := net.DialTimeout("tcp", g.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte{5, 1, 2})
	reply := make([]byte, 2)
	io.ReadFull(c, reply)
	password := dnsCredential(Config{StatsSecret: "fixture"}, n)
	auth := append([]byte{1, byte(len(n.ID))}, []byte(n.ID)...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, []byte(password)...)
	c.Write(auth)
	io.ReadFull(c, reply)
	c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header := make([]byte, 3)
	if _, err := io.ReadFull(c, header); err != nil || header[1] != 0 {
		t.Fatal("UDP association", err)
	}
	host, port, err := readSocksAddress(c)
	if err != nil {
		t.Fatal(err)
	}
	relay, _ := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	for _, target := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		wire := testDNSWire(t, dnsmessage.TypeA)
		udp.SetDeadline(time.Now().Add(3 * time.Second))
		udp.Write(append(append([]byte{0, 0, 0}, socksAddress(target, 53)...), wire...))
		buffer := make([]byte, 4096)
		size, err := udp.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		host, port, body, err := parseSocksDatagram(buffer[:size])
		if err != nil || host != target || port != 53 {
			t.Fatal("DNS response source mismatch")
		}
		var m dnsmessage.Message
		if m.Unpack(body) != nil || len(m.Answers) != 1 {
			t.Fatal("bad UDP DNS answer")
		}
	}
	if count.Load() != 1 {
		t.Fatal("DNS cache not shared across transport address families")
	}
}
func TestNewNodesDefaultToDNSProtectionAndCoreBindings(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for _, protocol := range []string{"vless", "hy2"} {
		n := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", Node{Protocol: protocol, Enabled: true}))
		if !secureDNS(n) || n.DNS.IPv6 != "block" {
			t.Fatal("new node DNS default")
		}
		exit := coreExit(a.cfg, n)
		if exit.Host != "127.0.0.1" || exit.Port != 19186 || exit.Username != n.ID || exit.Password == "" {
			t.Fatal("core not pinned to authenticated gateway")
		}
		if resourceFromNode(n).DNS != nil {
			t.Fatal("node DNS policy leaked into shared exit")
		}
	}
	for _, url := range []string{"http://1.1.1.1/dns-query", "https://localhost/dns-query", "https://127.0.0.1/dns-query", "https://[::1]/dns-query", "https://1.1.1.1:8443/dns-query"} {
		if validateNodeDNS(&NodeDNS{Mode: "secure", DoH: url, IPv6: "allow"}) == nil {
			t.Fatal("invalid DNS endpoint accepted")
		}
	}
}

func TestNodeGatewaySOCKSUDPForwardsIPv4AndIPv6WithoutDNSFallback(t *testing.T) {
	// The upstream fixture echoes datagrams with their requested source address.
	// This verifies native UDP relay framing and both address families without
	// requiring an Internet IPv6 route in the test environment.
	relay, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, peer, e := relay.ReadFromUDP(buffer)
			if e != nil {
				return
			}
			relay.WriteToUDP(buffer[:n], peer)
		}
	}()
	go func() {
		for {
			conn, e := upstream.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				greeting := make([]byte, 3)
				if _, e := io.ReadFull(conn, greeting); e != nil {
					return
				}
				conn.Write([]byte{5, 0})
				request := make([]byte, 3)
				if _, e := io.ReadFull(conn, request); e != nil || request[1] != 3 {
					return
				}
				if _, _, e := readSocksAddress(conn); e != nil {
					return
				}
				socksReply(conn, 0, relay.LocalAddr().(*net.UDPAddr).Port)
				io.Copy(io.Discard, conn)
			}()
		}
	}()
	listen, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := newNodeGateway(listen)
	defer g.close()
	n := Node{ID: "udp-native", Protocol: "hy2", Enabled: true, Exit: "socks5", Host: "127.0.0.1", Port: upstream.Addr().(*net.TCPAddr).Port, DNS: defaultNodeDNS()}
	n.DNS.IPv6 = "allow"
	if err := g.update(Config{StatsSecret: "fixture"}, []Node{n}); err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", g.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte{5, 1, 2})
	reply := make([]byte, 2)
	io.ReadFull(c, reply)
	password := dnsCredential(Config{StatsSecret: "fixture"}, n)
	auth := append([]byte{1, byte(len(n.ID))}, []byte(n.ID)...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, []byte(password)...)
	c.Write(auth)
	io.ReadFull(c, reply)
	c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header := make([]byte, 3)
	if _, err := io.ReadFull(c, header); err != nil || header[1] != 0 {
		t.Fatal("UDP associate failed")
	}
	host, port, err := readSocksAddress(c)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	client, err := net.DialUDP("udp", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, ip := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		packet := append(append([]byte{0, 0, 0}, socksAddress(ip, 3478)...), []byte("real-udp-payload")...)
		client.SetDeadline(time.Now().Add(3 * time.Second))
		client.Write(packet)
		buffer := make([]byte, 4096)
		count, err := client.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		got, port, body, err := parseSocksDatagram(buffer[:count])
		if err != nil || got != ip || port != 3478 || string(body) != "real-udp-payload" {
			t.Fatal("native UDP relay changed address or payload")
		}
	}
}
