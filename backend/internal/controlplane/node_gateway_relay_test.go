package controlplane

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	a, err := net.DialTCP("tcp4", nil, l.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.AcceptTCP()
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	a.SetDeadline(time.Now().Add(5 * time.Second))
	b.SetDeadline(time.Now().Add(5 * time.Second))
	return a, b
}

func TestRelayPreservesDelayedResponseAfterHalfClose(t *testing.T) {
	client, ingress := tcpPair(t)
	egress, server := tcpPair(t)
	done := make(chan struct{})
	go func() { defer close(done); relayConnections(ingress, egress) }()
	client.Write([]byte("request"))
	client.CloseWrite()
	request, err := io.ReadAll(server)
	if err != nil || string(request) != "request" {
		t.Fatalf("request %q: %v", request, err)
	}
	// A server can wait for request EOF before starting a slow response.
	time.Sleep(1200 * time.Millisecond)
	server.Write([]byte("complete response"))
	server.CloseWrite()
	response, err := io.ReadAll(client)
	if err != nil || string(response) != "complete response" {
		t.Fatalf("truncated response %q: %v", response, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not finish after both EOFs")
	}
}

func TestRelayFailureClosesIdlePeer(t *testing.T) {
	client, ingress := tcpPair(t)
	egress, server := tcpPair(t)
	done := make(chan struct{})
	go func() { defer close(done); relayConnections(ingress, egress) }()
	client.SetLinger(0)
	client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reset left the opposite reader running")
	}
	if _, err := server.Read(make([]byte, 1)); err == nil {
		t.Fatal("upstream still open")
	}
}

func TestGatewayMetadataChangesKeepLiveRoute(t *testing.T) {
	g, n, _, _, _ := dnsGatewayFixture(t, "block")
	old := g.routes[n.ID]
	c, err := testGatewayDial(t, g, n, "93.184.216.34:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	n.Name = "new label"
	n.ExitID = "different-pool-id"
	n.DefaultDirect = true
	n.HasPassword = true
	n.ManagedBy = "public"
	n.ProbeIP = "203.0.113.1"
	if err = g.update(Config{StatsSecret: "fixture"}, []Node{n}); err != nil {
		t.Fatal(err)
	}
	if g.routes[n.ID] != old {
		t.Fatal("metadata update replaced live resolver")
	}
	c.SetDeadline(time.Now().Add(time.Second))
	c.Write([]byte("ok"))
	b := make([]byte, 2)
	if _, err = io.ReadFull(c, b); err != nil || string(b) != "ok" {
		t.Fatal("metadata update interrupted data", err)
	}
	if err = g.update(Config{StatsSecret: "changed"}, []Node{n}); err != nil {
		t.Fatal(err)
	}
	if g.routes[n.ID] == old || old.ctx.Err() == nil {
		t.Fatal("credential change did not invalidate old route")
	}
}

func TestGatewayShutdownCancelsPendingDNSDial(t *testing.T) {
	g, n, _, _, _ := dnsGatewayFixture(t, "block")
	started := make(chan struct{})
	done := make(chan struct{})
	g.routes[n.ID].resolver.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() {
		defer close(done)
		c, _ := testGatewayDial(t, g, n, "pending.invalid:443")
		if c != nil {
			c.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("DNS dial did not start")
	}
	closed := make(chan struct{})
	go func() { g.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for the DNS timeout")
	}
	<-done
}
