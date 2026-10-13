// Package ingressgateway owns only the panel's TCP 443 entry. It never changes
// host Nginx, routing, firewall rules, or runs privileged maintenance commands.
package ingressgateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Listen           string `json:"listen,omitempty"`
	PanelDomain      string `json:"panel_domain"`
	NodeDomain       string `json:"node_domain"`
	RealitySNI       string `json:"reality_sni"`
	PanelUpstream    string `json:"panel_upstream,omitempty"`
	RealityUpstream  string `json:"reality_upstream,omitempty"`
	RealitySocketDir string `json:"reality_socket_dir,omitempty"`
	StateDir         string `json:"state_dir,omitempty"`
	TLSDir           string `json:"tls_dir,omitempty"`
	AcceptTOS        bool   `json:"accept_tos"`
	ACMEEmail        string `json:"acme_email,omitempty"`
	ACMEDirectory    string `json:"acme_directory,omitempty"`
	CertFile         string `json:"cert_file,omitempty"`
	KeyFile          string `json:"key_file,omitempty"`
}

var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

func validDomain(s string) bool {
	return len(s) <= 63 && domainPattern.MatchString(s)
}

func loopbackAddress(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || port == "" || port == "0" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 1 && p <= 65535
}

func (c *Config) Defaults() {
	if c.Listen == "" {
		c.Listen = ":443"
	}
	if c.PanelUpstream == "" {
		c.PanelUpstream = "http://127.0.0.1:19100"
	}
	if c.RealityUpstream == "" {
		c.RealityUpstream = "127.0.0.1:18443"
	}
	if c.RealitySocketDir == "" {
		c.RealitySocketDir = "/run/guangyue-reality"
	}
	if c.StateDir == "" {
		c.StateDir = "/var/lib/guangyue/gateway"
	}
	if c.TLSDir == "" {
		c.TLSDir = "/var/lib/guangyue/tls"
	}
}

func (c Config) Validate() error {
	if c.Listen != ":443" {
		return errors.New("gateway public listener must be :443")
	}
	if !validDomain(c.PanelDomain) || !validDomain(c.NodeDomain) || !validDomain(c.RealitySNI) {
		return errors.New("gateway domains must be lowercase ASCII FQDNs of at most 63 characters")
	}
	if c.RealitySNI == c.PanelDomain || c.RealitySNI == c.NodeDomain {
		return errors.New("Reality SNI must differ from hosted domains")
	}
	u, err := url.Parse(c.PanelUpstream)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !loopbackAddress(u.Host) {
		return errors.New("panel upstream must be a loopback HTTP origin")
	}
	if !loopbackAddress(c.RealityUpstream) {
		return errors.New("Reality upstream must be a loopback TCP address")
	}
	for _, p := range []string{c.StateDir, c.TLSDir, c.RealitySocketDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return errors.New("gateway state and socket paths must be absolute directories")
		}
	}
	if c.StateDir == c.TLSDir {
		return errors.New("gateway and TLS state directories must differ")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return errors.New("provide both cert_file and key_file")
	}
	if c.CertFile == "" && !c.AcceptTOS {
		return errors.New("explicit ACME terms acceptance is required")
	}
	if c.ACMEDirectory != "" {
		if c.ACMEDirectory != "https://acme-v02.api.letsencrypt.org/directory" && c.ACMEDirectory != "https://acme-staging-v02.api.letsencrypt.org/directory" {
			return errors.New("ACME directory must be an official Let's Encrypt production or staging directory")
		}
	}
	if strings.ContainsAny(c.ACMEEmail, "\r\n") || len(c.ACMEEmail) > 254 {
		return errors.New("invalid ACME contact")
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("gateway configuration could not be read")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, errors.New("invalid gateway configuration")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, errors.New("gateway configuration has trailing data")
	}
	c.Defaults()
	return c, c.Validate()
}

func (c Config) configHash() string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c Config) domains() []string {
	if c.PanelDomain == c.NodeDomain {
		return []string{c.PanelDomain}
	}
	return []string{c.PanelDomain, c.NodeDomain}
}

func (c Config) owns(name string) bool { return name == c.PanelDomain || name == c.NodeDomain }

func (c Config) acmeCacheDir() string {
	issuer := "production"
	if c.ACMEDirectory == "https://acme-staging-v02.api.letsencrypt.org/directory" {
		issuer = "staging"
	}
	return filepath.Join(c.StateDir, "acme-cache", issuer)
}
