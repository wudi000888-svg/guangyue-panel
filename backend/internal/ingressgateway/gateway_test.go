package ingressgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func vector16(b []byte) []byte {
	out := make([]byte, 2, len(b)+2)
	binary.BigEndian.PutUint16(out, uint16(len(b)))
	return append(out, b...)
}
func extension(kind uint16, b []byte) []byte {
	out := []byte{byte(kind >> 8), byte(kind)}
	return append(out, vector16(b)...)
}

func helloBytes(name string, protos ...string) []byte {
	entry := append([]byte{0}, vector16([]byte(name))...)
	ext := extension(0, vector16(entry))
	if len(protos) > 0 {
		var list []byte
		for _, p := range protos {
			list = append(list, byte(len(p)))
			list = append(list, p...)
		}
		ext = append(ext, extension(16, vector16(list))...)
	}
	b := []byte{3, 3}
	b = append(b, make([]byte, 32)...)
	b = append(b, 0, 0, 2, 0x13, 1, 1, 0)
	b = append(b, vector16(ext)...)
	h := []byte{1, byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))}
	return append(h, b...)
}

func record(b []byte) []byte {
	out := []byte{22, 3, 1, byte(len(b) >> 8), byte(len(b))}
	return append(out, b...)
}

func TestClientHelloFragmentsAreReplayedExactly(t *testing.T) {
	hello := helloBytes("Panel.Example.com", "h2", "http/1.1")
	for _, split := range []int{1, 2, 3, 4, 7, 37, len(hello) - 1, len(hello)} {
		raw := record(hello[:split])
		if split < len(hello) {
			raw = append(raw, record(hello[split:])...)
		}
		parsed, err := readClientHello(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if parsed.serverName != "panel.example.com" || len(parsed.protocols) != 2 || !bytes.Equal(parsed.raw, raw) {
			t.Fatalf("fragmented ClientHello was altered: %+v", parsed)
		}
	}
}

func TestClientHelloRejectsMalformedNamesALPNAndSizes(t *testing.T) {
	for _, name := range []string{"", "../../run/secret", "example.com/secret", "a.com\x00evil.com", "-a.example", "a..example", "localhost", strings.Repeat("a", 64) + ".com"} {
		if _, err := readClientHello(bytes.NewReader(record(helloBytes(name)))); err == nil {
			t.Fatalf("unsafe SNI accepted %q", name)
		}
	}
	if _, err := readClientHello(bytes.NewReader(record(helloBytes("panel.example.com", "acme-tls/1", "h2")))); err == nil {
		t.Fatal("mixed challenge ALPN accepted")
	}
	for _, raw := range [][]byte{{22, 3, 1, 255, 255}, {23, 3, 1, 0, 1, 0}, {22, 3, 1, 0, 0}, record([]byte{1, 255, 255, 255}), record(helloBytes("panel.example.com")[:41])} {
		if _, err := readClientHello(bytes.NewReader(raw)); err == nil {
			t.Fatal("malformed/oversized record accepted")
		}
	}
	h := helloBytes("panel.example.com")
	h = append(h, 0)
	h[3]++
	if _, err := readClientHello(bytes.NewReader(record(h))); err == nil {
		t.Fatal("trailing extension data accepted")
	}
	var raw []byte
	for i := 0; i < maxHelloRecords; i++ {
		raw = append(raw, record([]byte{1})...)
	}
	if _, err := readClientHello(bytes.NewReader(raw)); err == nil {
		t.Fatal("unbounded fragmentation accepted")
	}
}

func FuzzClientHello(f *testing.F) {
	f.Add(record(helloBytes("panel.example.com", "h2")))
	f.Add(record(helloBytes("panel.example.com", acme.ALPNProto)))
	f.Add([]byte{22, 3, 1, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxHelloBytes*2 {
			return
		}
		hello, err := readClientHello(bytes.NewReader(data))
		if err == nil && (!validDomain(hello.serverName) || len(hello.raw) > maxHelloBytes) {
			t.Fatal("parser escaped limits")
		}
	})
}

func certPair(t *testing.T, domains []string, challenge bool, expired bool) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	now := time.Now()
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: domains[0]}, DNSNames: domains, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(72 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if challenge {
		tmpl.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 31}, Critical: true, Value: []byte{4, 32}}}
	}
	if expired {
		tmpl.NotAfter = now.Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})
	cert, err := tls.X509KeyPair(crt, pemKey)
	if err != nil {
		t.Fatal(err)
	}
	return cert, crt, pemKey
}

func testConfig(t *testing.T) Config {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "gygw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	_, crt, key := certPair(t, []string{"panel.example.com", "node.example.com"}, false, false)
	certPath, keyPath := filepath.Join(root, "import.crt"), filepath.Join(root, "import.key")
	if err := os.WriteFile(certPath, crt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	sockets := filepath.Join(root, "reality")
	if err := os.Mkdir(sockets, 0700); err != nil {
		t.Fatal(err)
	}
	c := Config{PanelDomain: "panel.example.com", NodeDomain: "node.example.com", RealitySNI: "www.cloudflare.com", CertFile: certPath, KeyFile: keyPath, StateDir: filepath.Join(root, "gateway"), TLSDir: filepath.Join(root, "tls"), RealitySocketDir: sockets}
	c.Defaults()
	return c
}

func startGateway(t *testing.T, c Config) (*Server, string) {
	t.Helper()
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("gateway stop: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("gateway did not release connections on shutdown")
		}
	})
	return s, listener.Addr().String()
}

func TestHTTPSHTTP2AndForwardedIPCannotBeSpoofed(t *testing.T) {
	seen := make(chan http.Header, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Set("X-Upstream-Host", r.Host)
		io.WriteString(w, "panel works")
	}))
	defer backend.Close()
	c := testConfig(t)
	c.PanelUpstream = backend.URL
	_, addr := startGateway(t, c)
	pool := x509.NewCertPool()
	crt, _ := os.ReadFile(c.CertFile)
	pool.AppendCertsFromPEM(crt)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: c.PanelDomain}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	r, _ := http.NewRequest("GET", "https://"+addr+"/api/health?proof=1", nil)
	r.Host = c.PanelDomain
	for k, v := range map[string]string{"X-Real-IP": "203.0.113.12", "X-Forwarded-For": "203.0.113.13", "X-Forwarded-Proto": "http", "Forwarded": "for=203.0.113.14"} {
		r.Header.Set(k, v)
	}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.ProtoMajor != 2 || string(body) != "panel works" || resp.Header.Get("X-Upstream-Host") != c.PanelDomain {
		t.Fatalf("HTTP2 proxy failed: status=%d proto=%s body=%q", resp.StatusCode, resp.Proto, body)
	}
	h := <-seen
	if h.Get("X-Real-IP") != "127.0.0.1" || h.Get("X-Forwarded-For") != "127.0.0.1" || h.Get("X-Forwarded-Proto") != "https" || h.Get("Forwarded") != "" {
		t.Fatalf("spoofed address survived: %#v", h)
	}
	r, _ = http.NewRequest("GET", "https://"+addr+"/", nil)
	r.Host = c.NodeDomain
	resp, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 421 {
		t.Fatal("domain fronting accepted")
	}
}

func TestCertificatePublicationRenewalAndReadiness(t *testing.T) {
	c := testConfig(t)
	s, addr := startGateway(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ready, err := WaitReady(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Ready || ready.CertGeneration == "" {
		t.Fatal("initial imported cert not ready")
	}
	old := ready.NodeFingerprint
	cert, crt, key := certPair(t, []string{c.NodeDomain}, false, false)
	cache := publishingCache{Cache: autocert.DirCache(c.acmeCacheDir()), store: s.certificates}
	if err := cache.Put(context.Background(), "opaque-renewal-cache-key", append(key, crt...)); err != nil {
		t.Fatal(err)
	}
	if err := s.certificates.writeReadiness(true); err != nil {
		t.Fatal(err)
	}
	ready, err = WaitReady(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if ready.NodeFingerprint == old {
		t.Fatal("background cache renewal was not published")
	}
	loaded, err := tls.LoadX509KeyPair(filepath.Join(c.TLSDir, "current/fullchain.pem"), filepath.Join(c.TLSDir, "current/privkey.pem"))
	if err != nil || !bytes.Equal(loaded.Certificate[0], cert.Certificate[0]) {
		t.Fatal("published pair does not match renewed cert")
	}
	for _, name := range []string{"fullchain.pem", "privkey.pem"} {
		info, err := os.Stat(filepath.Join(c.TLSDir, "current", name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("published private pair permissions")
		}
	}
	challenge, challengeCRT, challengeKey := certPair(t, []string{c.NodeDomain}, true, false)
	if _, err := regularCertificate(&challenge, c.NodeDomain); err == nil {
		t.Fatal("temporary ACME cert accepted")
	}
	if err := cache.Put(context.Background(), "token", append(challengeKey, challengeCRT...)); err != nil {
		t.Fatal(err)
	}
	if s.certificates.generation.Fingerprint != ready.NodeFingerprint {
		t.Fatal("temporary challenge replaced node certificate")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = conn.Write(record(helloBytes(c.PanelDomain, acme.ALPNProto)))
	var b [1]byte
	_, _ = conn.Read(b[:]) // Imported cert mode answers no ACME challenge.
	wrong := c
	wrong.PanelDomain = "another.example.com"
	badCtx, badCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer badCancel()
	if _, err := WaitReady(badCtx, wrong); err == nil {
		t.Fatal("different configuration accepted stale ready receipt")
	}
}

func TestCertificateRestartPreservesGenerationAndLegacyFiles(t *testing.T) {
	c := testConfig(t)
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	generation := s.certificates.generation.Generation
	legacy := filepath.Join(c.TLSDir, "cert-legacy")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	s2, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if s2.certificates.generation.Generation != generation {
		t.Fatal("same certificate created unnecessary generation")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("legacy certificate data was deleted")
	}
}

func TestRealityForwardingAddsSocketDerivedProxyAndExactFragments(t *testing.T) {
	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	c := testConfig(t)
	c.RealityUpstream = backend.Addr().String()
	_, addr := startGateway(t, c)
	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	hello := helloBytes(c.RealitySNI, "h2")
	raw := append(record(hello[:3]), record(hello[3:])...)
	if _, err = client.Write(raw); err != nil {
		t.Fatal(err)
	}
	accepted, err := backend.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	_ = accepted.SetDeadline(time.Now().Add(3 * time.Second))
	r := bufio.NewReader(accepted)
	header, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	src := client.LocalAddr().(*net.TCPAddr)
	dst := client.RemoteAddr().(*net.TCPAddr)
	expected := []byte("PROXY TCP4 127.0.0.1 127.0.0.1 " + big.NewInt(int64(src.Port)).String() + " " + big.NewInt(int64(dst.Port)).String() + "\r\n")
	if header != string(expected) {
		t.Fatalf("PROXY header not socket-derived: %q", header)
	}
	received := make([]byte, len(raw))
	if _, err = io.ReadFull(r, received); err != nil || !bytes.Equal(raw, received) {
		t.Fatal("fragmented records altered by proxy")
	}
	_, _ = accepted.Write([]byte("reply"))
	got := make([]byte, 5)
	if _, err = io.ReadFull(client, got); err != nil || string(got) != "reply" {
		t.Fatal("Reality reverse relay failed")
	}
}

func TestDynamicRealitySocketsAndUnknownSNI(t *testing.T) {
	c := testConfig(t)
	socket := filepath.Join(c.RealitySocketDir, "dynamic.example.com.sock")
	u, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	s, addr := startGateway(t, c)
	if n, p, err := s.realityTarget("dynamic.example.com"); err != nil || n != "unix" || p != socket {
		t.Fatal("dynamic managed socket was not selected")
	}
	if err := os.Symlink(socket, filepath.Join(c.RealitySocketDir, "alias.example.com.sock")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alias.example.com", "unknown.example.com", "../../secret", c.PanelDomain} {
		if _, _, err := s.realityTarget(name); err == nil {
			t.Fatalf("untrusted socket path accepted: %s", name)
		}
	}
	for _, name := range []string{"unknown.example.com", c.RealitySNI} {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write(record(helloBytes(name, acme.ALPNProto)))
		var b [1]byte
		_, err = conn.Read(b[:])
		conn.Close()
		if err == nil {
			t.Fatal("ACME accepted for unknown/Reality SNI")
		}
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	raw := record(helloBytes("dynamic.example.com"))
	_, _ = conn.Write(raw)
	accepted, err := u.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	_ = accepted.SetDeadline(time.Now().Add(time.Second))
	r := bufio.NewReader(accepted)
	header, _ := r.ReadString('\n')
	if !strings.HasPrefix(header, "PROXY TCP4 ") {
		t.Fatal("Unix target missed original TCP client PROXY header")
	}
	got := make([]byte, len(raw))
	if _, err = io.ReadFull(r, got); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("dynamic target replay failed")
	}
}

func TestInvalidGatewayConfigurationAndReadOnlyLoad(t *testing.T) {
	c := testConfig(t)
	for _, mutate := range []func(*Config){func(c *Config) { c.Listen = ":8443" }, func(c *Config) { c.PanelDomain = "a.com;include" }, func(c *Config) { c.RealitySNI = c.PanelDomain }, func(c *Config) { c.PanelUpstream = "http://203.0.113.1:19100" }, func(c *Config) { c.RealityUpstream = "evil.example:18443" }, func(c *Config) { c.StateDir = "relative" }, func(c *Config) { c.CertFile = ""; c.KeyFile = ""; c.AcceptTOS = false }, func(c *Config) { c.ACMEDirectory = "https://arbitrary.example/directory" }} {
		bad := c
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %+v", bad)
		}
	}
	b, _ := json.Marshal(c)
	configFile := filepath.Join(t.TempDir(), "gateway.json")
	os.WriteFile(configFile, b, 0600)
	if _, err := LoadConfig(configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("configuration load mutated state")
	}
	os.WriteFile(configFile, append(b, []byte(" {}")...), 0600)
	if _, err := LoadConfig(configFile); err == nil {
		t.Fatal("trailing config accepted")
	}
}

func TestPublicTLSCannotTriggerACMEIssuance(t *testing.T) {
	c := testConfig(t)
	c.CertFile = ""
	c.KeyFile = ""
	c.AcceptTOS = true
	s, err := newCertificateStore(c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := s.tlsConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: c.PanelDomain, SupportedProtos: []string{"h2"}}); err == nil {
		t.Fatal("unissued public certificate unexpectedly returned")
	}
	if time.Since(start) > time.Second {
		t.Fatal("normal TLS tried to obtain certificate")
	}
	if entries, _ := os.ReadDir(c.acmeCacheDir()); len(entries) != 0 {
		t.Fatal("public TLS wrote ACME data")
	}
}

func TestStagingCertificateCacheCannotSatisfyProduction(t *testing.T) {
	c := testConfig(t)
	production := c.acmeCacheDir()
	c.ACMEDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"
	if staging := c.acmeCacheDir(); staging == production || filepath.Base(staging) != "staging" {
		t.Fatal("staging and production share certificate cache")
	}
}

func TestShutdownReleasesIncompleteClientHellos(t *testing.T) {
	c := testConfig(t)
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte{22, 3})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow client prevented shutdown")
	}
}
