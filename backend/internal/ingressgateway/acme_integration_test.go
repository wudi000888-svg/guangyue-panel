package ingressgateway

// A local ACME test CA, including signed JWS/nonces and an actual TLS-ALPN-01
// callback to this gateway. No fixture certificates bypass the issuer here.
import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

type localOrder struct {
	id                                int
	domain, token, authStatus, status string
	certificate                       []byte
}

type localACME struct {
	mu                                sync.Mutex
	url, gateway                      string
	account                           *ecdsa.PublicKey
	ca                                *x509.Certificate
	caKey                             *ecdsa.PrivateKey
	caDER                             []byte
	nonces                            map[string]bool
	orders                            map[int]*localOrder
	signedRequests, callbacks, issued int
	fault                             error
}

func newLocalACME(t *testing.T) (*localACME, *httptest.Server) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Local gateway test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(7 * 24 * time.Hour), KeyUsage: x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	f := &localACME{ca: parsed, caKey: key, caDER: der, nonces: map[string]bool{}, orders: map[int]*localOrder{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	f.url = srv.URL
	t.Cleanup(srv.Close)
	return f, srv
}

func randomLocalToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (f *localACME) signed(r *http.Request) ([]byte, error) {
	var envelope struct{ Protected, Payload, Signature string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&envelope); err != nil {
		return nil, err
	}
	protected, err := base64.RawURLEncoding.DecodeString(envelope.Protected)
	if err != nil {
		return nil, err
	}
	var header struct {
		Alg, Nonce, URL, Kid string
		JWK                  struct{ Kty, Crv, X, Y string }
	}
	if err = json.Unmarshal(protected, &header); err != nil {
		return nil, err
	}
	if header.Alg != "ES256" || header.URL != f.url+r.URL.Path {
		return nil, errors.New("wrong JWS algorithm or URL")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.nonces[header.Nonce] {
		return nil, errors.New("unknown or repeated nonce")
	}
	delete(f.nonces, header.Nonce)
	key := f.account
	if r.URL.Path == "/new-account" {
		x, err := base64.RawURLEncoding.DecodeString(header.JWK.X)
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(header.JWK.Y)
		if err != nil {
			return nil, err
		}
		key = &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if header.JWK.Kty != "EC" || header.JWK.Crv != "P-256" || !key.Curve.IsOnCurve(key.X, key.Y) {
			return nil, errors.New("invalid account JWK")
		}
	} else if header.Kid != f.url+"/account/1" || key == nil {
		return nil, errors.New("invalid account ID")
	}
	sig, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || len(sig) != 64 {
		return nil, errors.New("invalid signature encoding")
	}
	hash := sha256.Sum256([]byte(envelope.Protected + "." + envelope.Payload))
	if !ecdsa.Verify(key, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("invalid JWS signature")
	}
	if r.URL.Path == "/new-account" {
		f.account = key
	}
	f.signedRequests++
	return base64.RawURLEncoding.DecodeString(envelope.Payload)
}

func (f *localACME) fail(w http.ResponseWriter, err error) {
	f.mu.Lock()
	if f.fault == nil {
		f.fault = err
	}
	f.mu.Unlock()
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:ietf:params:acme:error:malformed", "detail": "local ACME validation failed", "status": 400})
}

func (f *localACME) serve(w http.ResponseWriter, r *http.Request) {
	nonce := randomLocalToken()
	f.mu.Lock()
	f.nonces[nonce] = true
	f.mu.Unlock()
	w.Header().Set("Replay-Nonce", nonce)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "0")
	if r.URL.Path == "/directory" {
		_ = json.NewEncoder(w).Encode(map[string]any{"newNonce": f.url + "/nonce", "newAccount": f.url + "/new-account", "newOrder": f.url + "/new-order", "meta": map[string]string{"termsOfService": f.url + "/terms"}})
		return
	}
	if r.URL.Path == "/nonce" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != "POST" {
		f.fail(w, errors.New("ACME requires POST"))
		return
	}
	payload, err := f.signed(r)
	if err != nil {
		f.fail(w, err)
		return
	}
	if r.URL.Path == "/new-account" {
		var account struct {
			Terms bool `json:"termsOfServiceAgreed"`
		}
		if err = json.Unmarshal(payload, &account); err != nil || !account.Terms {
			f.fail(w, errors.New("terms not accepted"))
			return
		}
		w.Header().Set("Location", f.url+"/account/1")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "valid", "orders": f.url + "/orders"})
		return
	}
	if r.URL.Path == "/new-order" {
		var in struct {
			Identifiers []struct{ Type, Value string }
		}
		if err = json.Unmarshal(payload, &in); err != nil || len(in.Identifiers) != 1 || in.Identifiers[0].Type != "dns" || in.Identifiers[0].Value != "panel.example.com" {
			f.fail(w, errors.New("wrong order identifier"))
			return
		}
		f.mu.Lock()
		id := len(f.orders) + 1
		o := &localOrder{id: id, domain: in.Identifiers[0].Value, token: randomLocalToken(), authStatus: "pending", status: "pending"}
		f.orders[id] = o
		body := f.orderJSON(o)
		f.mu.Unlock()
		w.Header().Set("Location", fmt.Sprintf("%s/order/%d", f.url, id))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		f.fail(w, errors.New("unexpected ACME URL"))
		return
	}
	id, _ := strconv.Atoi(parts[1])
	f.mu.Lock()
	o := f.orders[id]
	f.mu.Unlock()
	if o == nil {
		f.fail(w, errors.New("unknown order"))
		return
	}
	switch parts[0] {
	case "authz":
		f.mu.Lock()
		body := map[string]any{"identifier": map[string]string{"type": "dns", "value": o.domain}, "status": o.authStatus, "challenges": []any{map[string]string{"type": "tls-alpn-01", "url": fmt.Sprintf("%s/challenge/%d", f.url, id), "token": o.token, "status": o.authStatus}}}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(body)
	case "challenge":
		if err = f.validateChallenge(o); err != nil {
			f.fail(w, err)
			return
		}
		f.mu.Lock()
		o.authStatus = "valid"
		o.status = "ready"
		f.callbacks++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"type": "tls-alpn-01", "status": "valid", "token": o.token, "url": fmt.Sprintf("%s/challenge/%d", f.url, id)})
	case "order":
		f.mu.Lock()
		body := f.orderJSON(o)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(body)
	case "finalize":
		var in struct{ CSR string }
		if err = json.Unmarshal(payload, &in); err != nil {
			f.fail(w, err)
			return
		}
		der, err := base64.RawURLEncoding.DecodeString(in.CSR)
		if err != nil {
			f.fail(w, err)
			return
		}
		csr, err := x509.ParseCertificateRequest(der)
		if err != nil || csr.CheckSignature() != nil || len(csr.DNSNames) != 1 || csr.DNSNames[0] != o.domain {
			f.fail(w, errors.New("invalid signed CSR"))
			return
		}
		f.mu.Lock()
		if o.status != "ready" {
			f.mu.Unlock()
			f.fail(w, errors.New("order was not validated"))
			return
		}
		f.issued++
		number := f.issued
		f.mu.Unlock()
		lifetime := 72 * time.Hour
		if number == 1 {
			lifetime = 8 * time.Second
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(number + 1)), Subject: csr.Subject, DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(lifetime), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		crt, err := x509.CreateCertificate(rand.Reader, leaf, f.ca, csr.PublicKey, f.caKey)
		if err != nil {
			f.fail(w, err)
			return
		}
		chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: crt}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.caDER})...)
		f.mu.Lock()
		o.certificate = chain
		o.status = "valid"
		body := f.orderJSON(o)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(body)
	case "cert":
		f.mu.Lock()
		chain := append([]byte(nil), o.certificate...)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		_, _ = w.Write(chain)
	default:
		f.fail(w, errors.New("unexpected ACME resource"))
	}
}

func (f *localACME) orderJSON(o *localOrder) map[string]any {
	return map[string]any{"status": o.status, "identifiers": []any{map[string]string{"type": "dns", "value": o.domain}}, "authorizations": []string{fmt.Sprintf("%s/authz/%d", f.url, o.id)}, "finalize": fmt.Sprintf("%s/finalize/%d", f.url, o.id), "certificate": fmt.Sprintf("%s/cert/%d", f.url, o.id)}
}

func (f *localACME) validateChallenge(o *localOrder) error {
	f.mu.Lock()
	gateway, domain, token, key := f.gateway, o.domain, o.token, f.account
	f.mu.Unlock()
	if gateway == "" || key == nil {
		return errors.New("challenge gateway not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := (&tls.Dialer{Config: &tls.Config{ServerName: domain, NextProtos: []string{acme.ALPNProto}, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", gateway)
	if err != nil {
		return fmt.Errorf("actual TLS-ALPN callback failed: %w", err)
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if state.NegotiatedProtocol != acme.ALPNProto || len(state.PeerCertificates) != 1 {
		return errors.New("challenge negotiated wrong ALPN/certificate chain")
	}
	cert := state.PeerCertificates[0]
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != domain {
		return errors.New("challenge SAN mismatch")
	}
	thumb, err := acme.JWKThumbprint(key)
	if err != nil {
		return err
	}
	want := sha256.Sum256([]byte(token + "." + thumb))
	for _, e := range cert.Extensions {
		if e.Id.String() == "1.3.6.1.5.5.7.1.31" {
			var got []byte
			rest, err := asn1.Unmarshal(e.Value, &got)
			if err != nil || len(rest) != 0 || !e.Critical || !bytes.Equal(got, want[:]) {
				return errors.New("ACME key authorization digest mismatch")
			}
			return nil
		}
	}
	return errors.New("ACME identifier extension missing")
}

func TestLocalACMETLSALPNIssuanceAndActualBackgroundRenewal(t *testing.T) {
	f, _ := newLocalACME(t)
	c := testConfig(t)
	c.NodeDomain = c.PanelDomain
	c.CertFile = ""
	c.KeyFile = ""
	c.AcceptTOS = true
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	// The production JSON forbids arbitrary ACME URLs. Only this local test
	// injects a CA endpoint, with its own loopback-only HTTP transport.
	s.certificates.manager.Client = &acme.Client{DirectoryURL: f.url + "/directory", HTTPClient: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}}
	s.certificates.manager.RenewBefore = 4 * time.Second
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.gateway = l.Addr().String()
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer waitCancel()
	ready, err := WaitReady(waitCtx, c)
	if err != nil {
		f.mu.Lock()
		fault := f.fault
		f.mu.Unlock()
		t.Fatalf("real local issuance not ready: %v; CA=%v", err, fault)
	}
	first := ready.NodeFingerprint
	for {
		s.certificates.mu.RLock()
		current := s.certificates.generation.Fingerprint
		s.certificates.mu.RUnlock()
		if current != "" && current != first {
			break
		}
		select {
		case <-waitCtx.Done():
			f.mu.Lock()
			fault, issued, callbacks := f.fault, f.issued, f.callbacks
			f.mu.Unlock()
			t.Fatalf("background renewal missing: fault=%v issued=%d callbacks=%d", fault, issued, callbacks)
		case <-time.After(50 * time.Millisecond):
		}
	}
	f.mu.Lock()
	fault, issued, callbacks, signed := f.fault, f.issued, f.callbacks, f.signedRequests
	f.mu.Unlock()
	if fault != nil || issued != 2 || callbacks != 2 || signed < 10 {
		t.Fatalf("real ACME flow incomplete: fault=%v issued=%d callbacks=%d signed=%d", fault, issued, callbacks, signed)
	}
	if err := s.certificates.writeReadiness(true); err != nil {
		t.Fatal(err)
	}
	latest, err := WaitReady(waitCtx, c)
	if err != nil || latest.NodeFingerprint == first {
		t.Fatal("renewed pair/marker not ready")
	}
	pool := x509.NewCertPool()
	pool.AddCert(f.ca)
	conn, err := tls.Dial("tcp", l.Addr().String(), &tls.Config{RootCAs: pool, ServerName: c.PanelDomain, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
