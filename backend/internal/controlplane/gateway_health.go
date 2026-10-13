package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/coreprocess"
)

type gatewayHealthSnapshot struct {
	Active  bool
	Checked time.Time
}

func (a *App) gatewayServiceStatus() object {
	if !a.cfg.PrivateGateway && !coreprocess.ContainerMode() {
		return object{"name": "Nginx", "active": a.cfg.Dev || corePID("nginx.service") > 0}
	}
	a.gatewayHealthMu.Lock()
	defer a.gatewayHealthMu.Unlock()
	if !a.gatewayHealthBusy && (a.gatewayHealthSnapshot.Checked.IsZero() || time.Since(a.gatewayHealthSnapshot.Checked) >= 10*time.Second) {
		a.gatewayHealthBusy = true
		// Gateway liveness is independent of node APIs and accounting. Dashboard
		// requests never wait on a network call or create multiple concurrent probes.
		go a.refreshGatewayHealth()
	}
	v := a.gatewayHealthSnapshot
	status := object{"name": "HTTPS入口", "active": v.Active, "checking": v.Checked.IsZero(), "checked_at": int64(0)}
	if !v.Checked.IsZero() {
		status["checked_at"] = v.Checked.Unix()
	}
	return status
}

func (a *App) refreshGatewayHealth() {
	ctx := a.publicContext
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	probe := a.gatewayHealthProbe
	if probe == nil {
		probe = func(ctx context.Context) error {
			return probePrivateGateway(ctx, a.cfg.Cert, a.cfg.HY2Host, "127.0.0.1:443")
		}
	}
	err := probe(ctx)
	a.gatewayHealthMu.Lock()
	defer a.gatewayHealthMu.Unlock()
	a.gatewayHealthSnapshot = gatewayHealthSnapshot{Active: err == nil, Checked: time.Now()}
	a.gatewayHealthBusy = false
}

// The node certificate is independently published by the private gateway.
// Pin its current DER and verify hostname/lifetime: a stale ready.json or any
// other TLS service occupying 443 cannot claim this gateway is alive.
func probePrivateGateway(ctx context.Context, certPath, nodeHost, address string) error {
	f, err := os.Open(certPath)
	if err != nil {
		return errors.New("gateway certificate unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	_ = f.Close()
	if err != nil || len(b) > 1<<20 {
		return errors.New("gateway certificate unavailable")
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("gateway certificate invalid")
	}
	expected, err := x509.ParseCertificate(block.Bytes)
	if err != nil || expected.VerifyHostname(nodeHost) != nil {
		return errors.New("gateway certificate hostname mismatch")
	}
	now := time.Now()
	if now.Before(expected.NotBefore) || !now.Before(expected.NotAfter) {
		return errors.New("gateway certificate expired or not yet valid")
	}
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: nodeHost,
		// This loopback health probe trusts only the pinned ACME certificate,
		// not an OS CA fallback. VerifyConnection performs mandatory checks.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, expected.Raw) {
				return errors.New("gateway certificate pin mismatch")
			}
			peer := state.PeerCertificates[0]
			now := time.Now()
			if peer.VerifyHostname(nodeHost) != nil || now.Before(peer.NotBefore) || !now.Before(peer.NotAfter) {
				return errors.New("gateway certificate validation failed")
			}
			return nil
		},
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: config}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return errors.New("gateway TLS probe failed")
	}
	_ = conn.Close()
	return nil
}
