package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type nodeGatewayRoute struct {
	node           Node
	hash, password string
	resolver       *exitResolver
	ctx            context.Context
	cancel         context.CancelFunc
}

type nodeGateway struct {
	listener                  net.Listener
	mu                        sync.Mutex
	routes                    map[string]*nodeGatewayRoute
	sessions                  map[net.Conn]string
	slots, dnsSlots, udpSlots chan struct{}
	ctx                       context.Context
	cancel                    context.CancelFunc
	closed                    bool
	wg                        sync.WaitGroup
}

func (a *App) ensureNodeGateway(nodes []Node) error {
	if a.cfg.Dev {
		return nil
	}
	if a.dnsGateway == nil {
		needed := false
		for _, n := range nodes {
			needed = needed || n.Enabled && secureDNS(n)
		}
		if !needed {
			return nil
		}
		listener, err := net.Listen("tcp4", nodeDNSAddress)
		if err != nil {
			return errors.New("节点 DNS 监听器不可用")
		}
		a.dnsGateway = newNodeGateway(listener)
	}
	return a.dnsGateway.update(a.cfg, nodes)
}
func newNodeGateway(listener net.Listener) *nodeGateway {
	g := &nodeGateway{listener: listener, routes: map[string]*nodeGatewayRoute{}, sessions: map[net.Conn]string{}, slots: make(chan struct{}, 128), dnsSlots: make(chan struct{}, 12), udpSlots: make(chan struct{}, 32)}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case g.slots <- struct{}{}:
				g.mu.Lock()
				if g.closed {
					g.mu.Unlock()
					c.Close()
					<-g.slots
					return
				}
				g.sessions[c] = ""
				g.wg.Add(1)
				g.mu.Unlock()
				go func() {
					defer g.wg.Done()
					defer func() { c.Close(); g.mu.Lock(); delete(g.sessions, c); g.mu.Unlock(); <-g.slots }()
					g.serve(c)
				}()
			default:
				c.Close()
			}
		}
	}()
	return g
}
func (g *nodeGateway) update(c Config, nodes []Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return net.ErrClosed
	}
	next := map[string]*nodeGatewayRoute{}
	created := []*nodeGatewayRoute{}
	committed := false
	defer func() {
		if !committed {
			for _, route := range created {
				route.cancel()
				route.resolver.transport.CloseIdleConnections()
			}
		}
	}()
	for _, n := range nodes {
		if !n.Enabled || !secureDNS(n) {
			continue
		}
		if err := validateNodeDNS(n.DNS); err != nil {
			return err
		}
		// Metadata changes should not interrupt active data connections.
		b, _ := json.Marshal(runtimeNode(n))
		hash := digest(string(b) + dnsCredential(c, n))
		if old := g.routes[n.ID]; old != nil && old.hash == hash {
			next[n.ID] = old
			continue
		}
		resolver, err := newExitResolver(n, g.dnsSlots)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(g.ctx)
		route := &nodeGatewayRoute{node: n, hash: hash, password: dnsCredential(c, n), resolver: resolver, ctx: ctx, cancel: cancel}
		created = append(created, route)
		next[n.ID] = route
	}
	for id, old := range g.routes {
		if next[id] != old {
			old.cancel()
			old.resolver.transport.CloseIdleConnections()
			for conn, nodeID := range g.sessions {
				if nodeID == id {
					conn.Close()
				}
			}
		}
	}
	g.routes = next
	committed = true
	return nil
}
func (g *nodeGateway) close() {
	g.mu.Lock()
	g.closed = true
	g.cancel()
	g.listener.Close()
	for conn := range g.sessions {
		conn.Close()
	}
	for _, route := range g.routes {
		route.resolver.transport.CloseIdleConnections()
	}
	g.mu.Unlock()
	g.wg.Wait()
}
func (a *App) stopNodeGateway() {
	if a.dnsGateway != nil {
		a.dnsGateway.close()
	}
}

func readSocksAddress(r io.Reader) (string, int, error) {
	var kind [1]byte
	if _, err := io.ReadFull(r, kind[:]); err != nil {
		return "", 0, err
	}
	size := 0
	switch kind[0] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return "", 0, err
		}
		size = int(n[0])
	default:
		return "", 0, errors.New("invalid address")
	}
	b := make([]byte, size+2)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", 0, err
	}
	host := string(b[:size])
	if kind[0] != 3 {
		host = net.IP(b[:size]).String()
	}
	return host, int(binary.BigEndian.Uint16(b[size:])), nil
}
func socksAddress(host string, port int) []byte {
	ip := net.ParseIP(host)
	var b []byte
	if v4 := ip.To4(); v4 != nil {
		b = append([]byte{1}, v4...)
	} else if ip != nil {
		b = append([]byte{4}, ip.To16()...)
	} else {
		b = append([]byte{3, byte(len(host))}, []byte(host)...)
	}
	return append(b, byte(port>>8), byte(port))
}
func socksReply(c net.Conn, code byte, port int) error {
	_, err := c.Write(append([]byte{5, code, 0}, socksAddress("127.0.0.1", port)...))
	return err
}
func (g *nodeGateway) serve(c net.Conn) {
	c.SetDeadline(time.Now().Add(8 * time.Second))
	var header [2]byte
	if _, err := io.ReadFull(c, header[:]); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	auth := false
	for _, m := range methods {
		auth = auth || m == 2
	}
	if !auth {
		c.Write([]byte{5, 255})
		return
	}
	if _, err := c.Write([]byte{5, 2}); err != nil {
		return
	}
	if _, err := io.ReadFull(c, header[:]); err != nil || header[0] != 1 {
		return
	}
	user := make([]byte, header[1])
	if _, err := io.ReadFull(c, user); err != nil {
		return
	}
	var size [1]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return
	}
	password := make([]byte, size[0])
	if _, err := io.ReadFull(c, password); err != nil {
		return
	}
	g.mu.Lock()
	route := g.routes[string(user)]
	if route == nil || subtle.ConstantTimeCompare(password, []byte(route.password)) != 1 {
		g.mu.Unlock()
		c.Write([]byte{1, 1})
		return
	}
	g.sessions[c] = route.node.ID
	g.mu.Unlock()
	if _, err := c.Write([]byte{1, 0}); err != nil {
		return
	}
	var request [3]byte
	if _, err := io.ReadFull(c, request[:]); err != nil || request[0] != 5 || request[2] != 0 {
		return
	}
	host, port, err := readSocksAddress(c)
	if err != nil {
		return
	}
	switch request[1] {
	case 1:
		if port == 53 {
			if socksReply(c, 0, 0) != nil {
				return
			}
			c.SetDeadline(time.Time{})
			g.dnsTCP(route.ctx, c, route.resolver)
			return
		}
		if port == 853 || port < 1 {
			socksReply(c, 2, 0)
			return
		}
		ctx, cancel := context.WithTimeout(route.ctx, 12*time.Second)
		defer cancel()
		ips, err := route.resolver.addresses(ctx, host)
		if err != nil {
			socksReply(c, 4, 0)
			return
		}
		var upstream net.Conn
		for _, ip := range ips {
			upstream, err = dialExitTCP(ctx, route.node, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
			if err == nil {
				break
			}
		}
		if err != nil {
			socksReply(c, 5, 0)
			return
		}
		defer upstream.Close()
		if socksReply(c, 0, 0) != nil {
			return
		}
		c.SetDeadline(time.Time{})
		upstream.SetDeadline(time.Time{})
		relayConnections(c, upstream)
	case 3:
		c.SetDeadline(time.Time{})
		g.udpAssociate(c, route)
	default:
		socksReply(c, 7, 0)
	}
}
func (g *nodeGateway) dnsTCP(parent context.Context, c net.Conn, d *exitResolver) {
	for {
		c.SetDeadline(time.Now().Add(30 * time.Second))
		var prefix [2]byte
		if _, err := io.ReadFull(c, prefix[:]); err != nil {
			return
		}
		size := int(binary.BigEndian.Uint16(prefix[:]))
		if size < 12 || size > 4096 {
			return
		}
		wire := make([]byte, size)
		if _, err := io.ReadFull(c, wire); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(parent, 7*time.Second)
		reply, err := d.exchange(ctx, wire)
		cancel()
		if err != nil {
			reply = dnsError(wire, 2)
		}
		if len(reply) == 0 {
			return
		}
		binary.BigEndian.PutUint16(prefix[:], uint16(len(reply)))
		if _, err := c.Write(append(prefix[:], reply...)); err != nil {
			return
		}
	}
}

// Preserve TCP half-close: request EOF does not imply response EOF. Each
// direction retains its idle timeout; an actual I/O error releases both peers.
func relayConnections(a, b net.Conn) {
	done := make(chan struct{})
	copyOne := func(dst, src net.Conn) {
		buffer := make([]byte, 16<<10)
		for {
			src.SetReadDeadline(time.Now().Add(2 * time.Minute))
			n, err := src.Read(buffer)
			if n > 0 {
				dst.SetWriteDeadline(time.Now().Add(30 * time.Second))
				written, writeErr := dst.Write(buffer[:n])
				if writeErr != nil || written != n {
					a.Close()
					b.Close()
					return
				}
			}
			if err == io.EOF {
				if tcp, ok := dst.(interface{ CloseWrite() error }); ok {
					if tcp.CloseWrite() == nil {
						return
					}
				}
			}
			if err != nil {
				a.Close()
				b.Close()
				return
			}
		}
	}
	go func() { defer close(done); copyOne(a, b) }()
	copyOne(b, a)
	<-done
}

type bufferedExitConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedExitConn) Read(b []byte) (int, error) { return c.reader.Read(b) }
func (c *bufferedExitConn) CloseWrite() error {
	if x, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return x.CloseWrite()
	}
	return nil
}
func dialExitTCP(ctx context.Context, n Node, address string) (net.Conn, error) {
	n = effectiveExit(n)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	switch n.Exit {
	case "direct":
		return dialer.DialContext(ctx, "tcp", address)
	case "socks5":
		var auth *proxy.Auth
		if n.Username != "" {
			auth = &proxy.Auth{User: n.Username, Password: n.Password}
		}
		dial, err := proxy.SOCKS5("tcp", net.JoinHostPort(n.Host, strconv.Itoa(n.Port)), auth, dialer)
		if err != nil {
			return nil, err
		}
		return dial.(proxy.ContextDialer).DialContext(ctx, "tcp", address)
	case "http":
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(n.Host, strconv.Itoa(n.Port)))
		if err != nil {
			return nil, err
		}
		conn.SetDeadline(time.Now().Add(6 * time.Second))
		request := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if n.Username != "" {
			request.SetBasicAuth(n.Username, n.Password)
			request.Header.Set("Proxy-Authorization", request.Header.Get("Authorization"))
			request.Header.Del("Authorization")
		}
		if err = request.Write(conn); err != nil {
			conn.Close()
			return nil, err
		}
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, request)
		if err != nil || response.StatusCode != 200 {
			conn.Close()
			return nil, errors.New("exit CONNECT rejected")
		}
		conn.SetDeadline(time.Time{})
		return &bufferedExitConn{Conn: conn, reader: reader}, nil
	}
	return nil, errors.New("unsupported exit")
}
