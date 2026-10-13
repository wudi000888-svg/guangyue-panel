package controlplane

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrivateGatewayHealthPinsNodeCertificateAndSNI(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeContainerTestCertificate(t, cert, key, 1)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	sni := make(chan string, 2)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		sni <- hello.ServerName
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probePrivateGateway(ctx, cert, "test.invalid", server.Listener.Addr().String()); err != nil {
		t.Fatal("matching live gateway was not recognized:", err)
	}
	select {
	case got := <-sni:
		if got != "test.invalid" {
			t.Fatal("probe did not use the node domain for SNI")
		}
	case <-time.After(time.Second):
		t.Fatal("gateway did not receive the TLS probe")
	}
	// Another TLS server on 443, even with a matching domain and valid lifetime,
	// cannot pass the exact gateway leaf certificate pin.
	writeContainerTestCertificate(t, cert, key, 2)
	if err := probePrivateGateway(ctx, cert, "test.invalid", server.Listener.Addr().String()); err == nil {
		t.Fatal("unrelated valid TLS listener masqueraded as this gateway")
	}
	if err := probePrivateGateway(ctx, cert, "different.invalid", server.Listener.Addr().String()); err == nil {
		t.Fatal("incorrect node domain passed gateway health")
	}
	if err := os.Remove(cert); err != nil {
		t.Fatal(err)
	}
	if err := probePrivateGateway(ctx, cert, "test.invalid", server.Listener.Addr().String()); err == nil {
		t.Fatal("stale readiness data without a certificate passed gateway health")
	}
}

func TestPrivateGatewayProbeIsBoundedAndDoesNotTrustTCPAlone(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeContainerTestCertificate(t, cert, key, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := probePrivateGateway(ctx, cert, "test.invalid", listener.Addr().String()); err == nil {
		t.Fatal("TCP-only listener was mistaken for a live gateway")
	}
	if time.Since(started) > time.Second {
		t.Fatal("TLS probe ignored cancellation")
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("probe never connected")
	}
}

func TestPrivateGatewayHealthRejectsInvalidCertificateLifetime(t *testing.T) {
	for _, expired := range []bool{true, false} {
		t.Run(map[bool]string{true: "expired", false: "future"}[expired], func(t *testing.T) {
			dir := t.TempDir()
			cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
			before, after := time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
			if !expired {
				before, after = time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
			}
			writeContainerTestCertificateValidity(t, cert, key, 3, before, after)
			if err := probePrivateGateway(context.Background(), cert, "test.invalid", "127.0.0.1:1"); err == nil || err.Error() != "gateway certificate expired or not yet valid" {
				t.Fatal("invalid lifetime reached the network or passed health checks:", err)
			}
		})
	}
}

func TestGatewayDashboardRevalidatesPreviouslyHealthyGateway(t *testing.T) {
	t.Setenv("GUANGYUE_CONTAINER", "")
	a := &App{cfg: Config{PrivateGateway: true}, gatewayHealthSnapshot: gatewayHealthSnapshot{Active: true, Checked: time.Now().Add(-time.Minute)}, gatewayHealthProbe: func(context.Context) error { return errors.New("gateway exited") }}
	_ = a.gatewayServiceStatus()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.gatewayHealthMu.Lock()
		v, busy := a.gatewayHealthSnapshot, a.gatewayHealthBusy
		a.gatewayHealthMu.Unlock()
		if !busy && !v.Active && time.Since(v.Checked) < time.Second {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("a stale success stayed healthy after the live gateway stopped")
}

func TestGatewayDashboardHealthDoesNotBlockOrDuplicateProbes(t *testing.T) {
	t.Setenv("GUANGYUE_CONTAINER", "")
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	a := &App{cfg: Config{PrivateGateway: true}, gatewayHealthProbe: func(ctx context.Context) error {
		calls.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}}
	first := a.gatewayServiceStatus()
	if first["name"] != "HTTPS入口" || first["checking"] != true || first["active"] != false {
		t.Fatal("gateway reported a false live state before a real TLS probe")
	}
	<-started
	before := time.Now()
	for range 20 {
		_ = a.gatewayServiceStatus()
	}
	if time.Since(before) > 100*time.Millisecond || calls.Load() != 1 {
		t.Fatal("dashboard blocked or started duplicate gateway probes")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := a.gatewayServiceStatus()
		if status["checking"] == false && status["active"] == true {
			if status["checked_at"] == int64(0) || calls.Load() != 1 {
				t.Fatal("live gateway did not cache the verified result")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gateway live result was not published")
}

func TestGatewayDashboardUsesContainerAndKeepsNativeLegacyBehavior(t *testing.T) {
	t.Setenv("GUANGYUE_CONTAINER", "1")
	a := &App{gatewayHealthProbe: func(context.Context) error { return errors.New("unavailable") }}
	if got := a.gatewayServiceStatus(); got["name"] != "HTTPS入口" || got["active"] != false {
		t.Fatal("container health incorrectly looked for a host nginx PID")
	}
	t.Setenv("GUANGYUE_CONTAINER", "")
	b := &App{cfg: Config{Dev: true}}
	if got := b.gatewayServiceStatus(); got["name"] != "Nginx" || got["active"] != true {
		t.Fatal("native legacy nginx service behavior changed")
	}
}
