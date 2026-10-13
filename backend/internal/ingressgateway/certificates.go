package ingressgateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

type CertificateGeneration struct {
	Schema      int    `json:"schema"`
	Generation  string `json:"generation"`
	Fingerprint string `json:"fingerprint"`
	Domain      string `json:"domain"`
	NotAfter    int64  `json:"not_after"`
	ChangedAt   int64  `json:"changed_at"`
}

type Readiness struct {
	Schema          int      `json:"schema"`
	Ready           bool     `json:"ready"`
	PID             int      `json:"pid"`
	Instance        string   `json:"instance"`
	ConfigHash      string   `json:"config_hash"`
	Domains         []string `json:"domains"`
	CertGeneration  string   `json:"certificate_generation"`
	NodeFingerprint string   `json:"node_fingerprint"`
	CheckedAt       int64    `json:"checked_at"`
	Error           string   `json:"error,omitempty"`
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("gateway private directory has unsafe permissions or type")
	}
	return nil
}

func atomicFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".gateway-pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	return err
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func atomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(path, append(b, '\n'))
}

func regularCertificate(cert *tls.Certificate, domain string) (*x509.Certificate, error) {
	if cert == nil || len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		return nil, errors.New("certificate pair is incomplete")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = leaf.VerifyHostname(domain); err != nil {
		return nil, errors.New("certificate does not cover gateway domain")
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("gateway certificate is not currently valid")
	}
	for _, e := range leaf.Extensions {
		if e.Id.String() == "1.3.6.1.5.5.7.1.31" {
			return nil, errors.New("ACME challenge certificates cannot be published as node certificates")
		}
	}
	return leaf, nil
}

type certificateStore struct {
	config     Config
	mu         sync.RWMutex
	certs      map[string]*tls.Certificate
	generation CertificateGeneration
	manager    *autocert.Manager
	instance   string
	lastError  string
}

// The normal certificate cache also receives background renewals. Observe valid
// domain certificates after the durable write, without publishing challenge
// certificates, account keys or any caller-supplied command.
type publishingCache struct {
	autocert.Cache
	store *certificateStore
}

func (c publishingCache) Put(ctx context.Context, key string, data []byte) error {
	if err := c.Cache.Put(ctx, key, data); err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(data, data)
	if err != nil {
		return nil
	} // ACME account/cache metadata is opaque.
	return c.store.observe(&cert)
}

func newCertificateStore(config Config) (*certificateStore, error) {
	for _, d := range []string{config.StateDir, config.TLSDir, config.acmeCacheDir()} {
		if err := ensurePrivateDir(d); err != nil {
			return nil, err
		}
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	s := &certificateStore{config: config, certs: map[string]*tls.Certificate{}, instance: hex.EncodeToString(token[:])}
	// Retain a verified generation across restarts. This avoids spurious HY2
	// restarts and never deletes certificate generations written by legacy code.
	if raw, err := os.ReadFile(filepath.Join(config.TLSDir, "certificate-generation.json")); err == nil && len(raw) < 8192 {
		var previous CertificateGeneration
		if json.Unmarshal(raw, &previous) == nil && previous.Schema == 1 && previous.Domain == config.NodeDomain && filepath.Base(previous.Generation) == previous.Generation {
			if current, err := os.Readlink(filepath.Join(config.TLSDir, "current")); err == nil && current == previous.Generation {
				if cert, err := tls.LoadX509KeyPair(filepath.Join(config.TLSDir, "current/fullchain.pem"), filepath.Join(config.TLSDir, "current/privkey.pem")); err == nil {
					digest := sha256.Sum256(cert.Certificate[0])
					if _, err = regularCertificate(&cert, config.NodeDomain); err == nil && hex.EncodeToString(digest[:]) == previous.Fingerprint {
						s.generation = previous
					}
				}
			}
		}
	}
	// No previous process's ready receipt may satisfy a new installation wait.
	if err := os.Remove(filepath.Join(config.StateDir, "ready.json")); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if config.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		if err != nil {
			return nil, errors.New("test/imported certificate pair could not be loaded")
		}
		for _, name := range config.domains() {
			if _, err := regularCertificate(&cert, name); err != nil {
				return nil, err
			}
		}
		if err = s.observe(&cert); err != nil {
			return nil, err
		}
	} else {
		s.manager = &autocert.Manager{Prompt: func(string) bool { return config.AcceptTOS }, Email: config.ACMEEmail,
			HostPolicy: autocert.HostWhitelist(config.domains()...), RenewBefore: 30 * 24 * time.Hour}
		if config.ACMEDirectory != "" {
			s.manager.Client = &acme.Client{DirectoryURL: config.ACMEDirectory}
		}
		// A successful staging rehearsal must never satisfy production issuance
		// from an untrusted staging certificate cached under the same hostname.
		s.manager.Cache = publishingCache{Cache: autocert.DirCache(config.acmeCacheDir()), store: s}
	}
	return s, nil
}

func (s *certificateStore) observe(cert *tls.Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := false
	for _, name := range s.config.domains() {
		if _, err := regularCertificate(cert, name); err == nil {
			s.certs[name] = cert
			matched = true
		}
	}
	if !matched {
		return nil
	}
	if _, err := regularCertificate(cert, s.config.NodeDomain); err == nil {
		if err := s.publishLocked(cert); err != nil {
			s.lastError = "node certificate publication failed"
			return err
		}
	}
	s.lastError = ""
	return nil
}

func (s *certificateStore) publishLocked(cert *tls.Certificate) error {
	leaf, err := regularCertificate(cert, s.config.NodeDomain)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(cert.Certificate[0])
	fingerprint := hex.EncodeToString(digest[:])
	if s.generation.Fingerprint == fingerprint {
		return nil
	}
	private, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return err
	}
	var chain []byte
	for _, der := range cert.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	dir, err := os.MkdirTemp(s.config.TLSDir, "cert-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(dir)
		}
	}()
	if err = atomicFile(filepath.Join(dir, "fullchain.pem"), chain); err != nil {
		return err
	}
	if err = atomicFile(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})); err != nil {
		return err
	}
	// Validate the exact serialized pair before the current symlink changes.
	if _, err = tls.LoadX509KeyPair(filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")); err != nil {
		return err
	}
	if err = syncDirectory(dir); err != nil {
		return err
	}
	pending := filepath.Join(s.config.TLSDir, ".gateway-current-"+s.instance)
	if err = os.Symlink(filepath.Base(dir), pending); err != nil {
		return err
	}
	defer os.Remove(pending)
	if err = os.Rename(pending, filepath.Join(s.config.TLSDir, "current")); err != nil {
		return err
	}
	published = true
	if err = syncDirectory(s.config.TLSDir); err != nil {
		return err
	}
	generation := CertificateGeneration{Schema: 1, Generation: filepath.Base(dir), Fingerprint: fingerprint,
		Domain: s.config.NodeDomain, NotAfter: leaf.NotAfter.Unix(), ChangedAt: time.Now().Unix()}
	if err = atomicJSON(filepath.Join(s.config.TLSDir, "certificate-generation.json"), generation); err != nil {
		return err
	}
	s.generation = generation
	// Historical generations remain private for recovery. Retention belongs to
	// the fixed maintenance worker; this unprivileged gateway never deletes
	// pre-existing/imported certificate directories.
	return nil
}

func (s *certificateStore) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1", acme.ALPNProto},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := strings.ToLower(hello.ServerName)
			if !s.config.owns(name) {
				return nil, errors.New("unrecognized gateway domain")
			}
			if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acme.ALPNProto {
				if s.manager == nil {
					return nil, errors.New("ACME is disabled for imported certificates")
				}
				return s.manager.GetCertificate(hello)
			}
			// Public traffic cannot trigger issuance or exhaust five-minute ACME
			// operations. Only the bounded background domain loop obtains certs.
			s.mu.RLock()
			cert := s.certs[name]
			s.mu.RUnlock()
			if _, err := regularCertificate(cert, name); err != nil {
				return nil, errors.New("gateway certificate is not ready")
			}
			return cert, nil
		}}
}

func (s *certificateStore) run(ctx context.Context) {
	if s.manager != nil {
		for _, domain := range s.config.domains() {
			go func(name string) {
				pause := time.Second
				for ctx.Err() == nil {
					hello := &tls.ClientHelloInfo{ServerName: name, SupportedProtos: []string{"h2", "http/1.1"},
						SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256}, SupportedCurves: []tls.CurveID{tls.CurveP256}, CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256}}
					cert, err := s.manager.GetCertificate(hello)
					if err == nil {
						err = s.observe(cert)
					}
					if err == nil {
						pause = 5 * time.Minute
					} else {
						s.mu.Lock()
						s.lastError = "ACME issuance or certificate publication is pending; verify domain DNS and public TCP 443"
						s.mu.Unlock()
						pause = min(max(30*time.Second, pause*2), 5*time.Minute)
					}
					timer := time.NewTimer(pause)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}(domain)
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		_ = s.writeReadiness(true)
		select {
		case <-ctx.Done():
			_ = s.writeReadiness(false)
			return
		case <-ticker.C:
		}
	}
}

func (s *certificateStore) writeReadiness(listening bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ready := listening && s.generation.Generation != "" && s.lastError == ""
	for _, name := range s.config.domains() {
		if _, err := regularCertificate(s.certs[name], name); err != nil {
			ready = false
		}
	}
	return atomicJSON(filepath.Join(s.config.StateDir, "ready.json"), Readiness{Schema: 1, Ready: ready, PID: os.Getpid(), Instance: s.instance,
		ConfigHash: s.config.configHash(), Domains: s.config.domains(), CertGeneration: s.generation.Generation, NodeFingerprint: s.generation.Fingerprint, CheckedAt: time.Now().Unix(), Error: s.lastError})
}

// WaitReady is local and read-only. It requires a live process, a fresh receipt
// for the exact configuration, and the actual published certificate pair.
func WaitReady(ctx context.Context, config Config) (Readiness, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state Readiness
		b, err := os.ReadFile(filepath.Join(config.StateDir, "ready.json"))
		if err == nil && len(b) < 16384 && json.Unmarshal(b, &state) == nil && state.Schema == 1 && state.Ready && state.PID > 0 && state.ConfigHash == config.configHash() && state.CheckedAt <= time.Now().Unix()+5 && time.Now().Unix()-state.CheckedAt < 15 && syscall.Kill(state.PID, 0) == nil {
			cert, err := tls.LoadX509KeyPair(filepath.Join(config.TLSDir, "current/fullchain.pem"), filepath.Join(config.TLSDir, "current/privkey.pem"))
			if err == nil {
				if _, err = regularCertificate(&cert, config.NodeDomain); err == nil {
					h := sha256.Sum256(cert.Certificate[0])
					target, readErr := os.Readlink(filepath.Join(config.TLSDir, "current"))
					if readErr == nil && target == state.CertGeneration && hex.EncodeToString(h[:]) == state.NodeFingerprint {
						return state, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return state, fmt.Errorf("gateway readiness not confirmed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
