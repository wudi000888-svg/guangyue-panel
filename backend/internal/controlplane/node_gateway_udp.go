package controlplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// Each authenticated association has a private loopback socket. Its source port
// is pinned on the first datagram, and no relay is created for HTTP-only exits.
func (g *nodeGateway) udpAssociate(control net.Conn, route *nodeGatewayRoute) {
	select {
	case g.udpSlots <- struct{}{}:
		defer func() { <-g.udpSlots }()
	default:
		socksReply(control, 1, 0)
		return
	}
	local, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		socksReply(control, 1, 0)
		return
	}
	defer local.Close()
	if socksReply(control, 0, local.LocalAddr().(*net.UDPAddr).Port) != nil {
		return
	}
	ctx, cancel := context.WithCancel(route.ctx)
	defer cancel()
	go func() { io.Copy(io.Discard, io.LimitReader(control, 1)); cancel(); local.Close() }()
	var peer *net.UDPAddr
	var relay *exitUDP
	var reader sync.WaitGroup
	defer func() {
		if relay != nil {
			relay.close()
		}
		reader.Wait()
		control.Close()
	}()
	buffer := make([]byte, 65535)
	for {
		local.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, source, err := local.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if !source.IP.IsLoopback() {
			continue
		}
		if peer == nil {
			peer = source
		}
		if peer.String() != source.String() {
			continue
		}
		host, port, payload, err := parseSocksDatagram(buffer[:n])
		if err != nil {
			continue
		}
		if port == 53 {
			queryCtx, stop := context.WithTimeout(ctx, 7*time.Second)
			reply, err := route.resolver.exchange(queryCtx, payload)
			stop()
			if err != nil {
				reply = dnsError(payload, 2)
			}
			if len(reply) > 0 {
				local.WriteToUDP(append(append([]byte{0, 0, 0}, socksAddress(host, port)...), reply...), peer)
			}
			continue
		}
		if port == 853 || port < 1 || effectiveExit(route.node).Exit == "http" {
			continue
		}
		queryCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		ips, err := route.resolver.addresses(queryCtx, host)
		stop()
		if err != nil {
			continue
		}
		if relay == nil {
			relay, err = openExitUDP(ctx, route.node)
			if err != nil {
				continue
			}
			reader.Add(1)
			go func() {
				defer reader.Done()
				reply := make([]byte, 65535)
				for {
					body, e := relay.read(reply)
					if e != nil {
						// Close the authenticated association so clients can
						// reconnect after the remote relay expires.
						cancel()
						local.Close()
						control.Close()
						return
					}
					local.WriteToUDP(body, peer)
				}
			}()
		}
		for _, ip := range ips {
			if relay.write(ip, port, payload) == nil {
				break
			}
		}
	}
}
func parseSocksDatagram(b []byte) (string, int, []byte, error) {
	if len(b) < 7 || b[0] != 0 || b[1] != 0 || b[2] != 0 {
		return "", 0, nil, errors.New("invalid or fragmented SOCKS datagram")
	}
	r := bytes.NewReader(b[3:])
	host, port, err := readSocksAddress(r)
	if err != nil {
		return "", 0, nil, err
	}
	return host, port, b[len(b)-r.Len():], nil
}

type exitUDP struct {
	socket  *net.UDPConn
	control net.Conn
	direct  bool
	mu      sync.Mutex
	allowed map[string]bool
}

func (u *exitUDP) close() {
	u.socket.Close()
	if u.control != nil {
		u.control.Close()
	}
}
func (u *exitUDP) write(ip net.IP, port int, body []byte) error {
	address := &net.UDPAddr{IP: ip, Port: port}
	u.mu.Lock()
	if len(u.allowed) >= 64 && !u.allowed[address.String()] {
		u.mu.Unlock()
		return errors.New("UDP destination limit")
	}
	u.allowed[address.String()] = true
	u.mu.Unlock()
	u.socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if u.direct {
		_, err := u.socket.WriteToUDP(body, address)
		return err
	}
	_, err := u.socket.Write(append(append([]byte{0, 0, 0}, socksAddress(ip.String(), port)...), body...))
	return err
}
func (u *exitUDP) read(buffer []byte) ([]byte, error) {
	for {
		u.socket.SetReadDeadline(time.Now().Add(60 * time.Second))
		var host string
		var port int
		var body []byte
		if u.direct {
			n, addr, err := u.socket.ReadFromUDP(buffer)
			if err != nil {
				return nil, err
			}
			host, port, body = addr.IP.String(), addr.Port, buffer[:n]
		} else {
			n, err := u.socket.Read(buffer)
			if err != nil {
				return nil, err
			}
			host, port, body, err = parseSocksDatagram(buffer[:n])
			if err != nil {
				continue
			}
		}
		u.mu.Lock()
		allowed := u.allowed[net.JoinHostPort(host, strconv.Itoa(port))]
		u.mu.Unlock()
		if !allowed {
			continue
		}
		return append(append([]byte{0, 0, 0}, socksAddress(host, port)...), body...), nil
	}
}
func openExitUDP(ctx context.Context, n Node) (*exitUDP, error) {
	n = effectiveExit(n)
	if n.Exit == "direct" {
		network, addr := "udp4", &net.UDPAddr{IP: net.IPv4zero}
		if n.DNS.IPv6 == "allow" {
			network = "udp"
			addr = &net.UDPAddr{IP: net.IPv6unspecified}
		}
		sock, err := net.ListenUDP(network, addr)
		if err != nil {
			return nil, err
		}
		return &exitUDP{socket: sock, direct: true, allowed: map[string]bool{}}, nil
	}
	if n.Exit != "socks5" {
		return nil, errors.New("exit has no UDP support")
	}
	conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(n.Host, strconv.Itoa(n.Port)))
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	conn.SetDeadline(time.Now().Add(6 * time.Second))
	method := byte(0)
	if n.Username != "" {
		method = 2
	}
	if _, err = conn.Write([]byte{5, 1, method}); err != nil {
		return nil, err
	}
	var reply [2]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil || reply[0] != 5 || reply[1] != method {
		return nil, errors.New("UDP proxy authentication unavailable")
	}
	if method == 2 {
		if len(n.Username) > 255 || len(n.Password) > 255 {
			return nil, errors.New("invalid proxy authentication size")
		}
		request := append([]byte{1, byte(len(n.Username))}, []byte(n.Username)...)
		request = append(request, byte(len(n.Password)))
		request = append(request, []byte(n.Password)...)
		if _, err = conn.Write(request); err != nil {
			return nil, err
		}
		if _, err = io.ReadFull(conn, reply[:]); err != nil || reply[0] != 1 || reply[1] != 0 {
			return nil, errors.New("UDP proxy authentication failed")
		}
	}
	if _, err = conn.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return nil, err
	}
	var header [3]byte
	if _, err = io.ReadFull(conn, header[:]); err != nil || header[0] != 5 || header[1] != 0 || header[2] != 0 {
		return nil, errors.New("UDP proxy associate rejected")
	}
	host, port, err := readSocksAddress(conn)
	if err != nil || port == 0 {
		return nil, errors.New("invalid UDP relay")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host, _, _ = net.SplitHostPort(conn.RemoteAddr().String())
	}
	relay, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	sock, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	success = true
	return &exitUDP{socket: sock, control: conn, allowed: map[string]bool{}}, nil
}
