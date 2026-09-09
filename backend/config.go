package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const version = "0.14.0"

type Config struct {
	StateDir       string `json:"state_dir"`
	Listen         string `json:"listen"`
	InternalListen string `json:"internal_listen"`
	PublicURL      string `json:"public_url"`
	VLESSHost      string `json:"vless_host"`
	HY2Host        string `json:"hy2_host"`
	RealitySNI     string `json:"reality_sni"`
	RealityTarget  string `json:"reality_target"`
	RealityPrivate string `json:"reality_private"`
	RealityPublic  string `json:"reality_public"`
	ShortID        string `json:"short_id"`
	StatsSecret    string `json:"stats_secret"`
	Cert           string `json:"cert"`
	CertKey        string `json:"cert_key"`
	Xray           string `json:"xray"`
	Mihomo         string `json:"mihomo"`
	Hysteria       string `json:"hysteria"`
	WebDir         string `json:"web_dir"`
	Dev            bool   `json:"dev"`
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b, 0600)
}
func loadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.StateDir == "" || c.PublicURL == "" || c.VLESSHost == "" || c.HY2Host == "" {
		return c, errors.New("incomplete application configuration")
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:19100"
	}
	if c.InternalListen == "" {
		c.InternalListen = "127.0.0.1:19101"
	}
	if c.Mihomo == "" {
		c.Mihomo = "/opt/guangyue-personal/bin/mihomo"
	}
	return c, nil
}
func initConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return errors.New("configuration already exists")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	c := Config{StateDir: "/var/lib/guangyue", Listen: "127.0.0.1:19100", InternalListen: "127.0.0.1:19101", PublicURL: "https://panel.example.com", VLESSHost: "node.example.com", HY2Host: "node.example.com", RealitySNI: "www.cloudflare.com", RealityTarget: "www.cloudflare.com:443", RealityPrivate: base64.RawURLEncoding.EncodeToString(private.Bytes()), RealityPublic: base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()), ShortID: digest(randomToken(8))[:16], StatsSecret: randomToken(32), Cert: "/var/lib/guangyue/tls/fullchain.pem", CertKey: "/var/lib/guangyue/tls/privkey.pem", Xray: "/opt/guangyue-personal/bin/xray", Hysteria: "/opt/guangyue-personal/bin/hysteria", WebDir: "/opt/guangyue-personal/web"}
	return writeJSON(path, c)
}

type Vault struct{ a cipher.AEAD }

func openVault(dir string) (*Vault, error) {
	path := filepath.Join(dir, "master.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		_, err = rand.Read(key)
		if err == nil {
			err = atomicWrite(path, key, 0600)
		}
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(block)
	return &Vault{a}, err
}
func (v *Vault) seal(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, v.a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.a.Seal(nonce, nonce, b, nil), nil
}
func (v *Vault) open(b []byte, value any) error {
	n := v.a.NonceSize()
	if len(b) < n {
		return errors.New("invalid encrypted record")
	}
	plain, err := v.a.Open(nil, b[:n], b[n:], nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, value)
}

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	Enabled      bool   `json:"enabled"`
	VLESS        bool   `json:"vless"`
	HY2          bool   `json:"hy2"`
	Expires      int64  `json:"expires"`
	Quota        int64  `json:"quota"`
	Upload       int64  `json:"upload"`
	Download     int64  `json:"download"`
	VLESSTraffic int64  `json:"vless_traffic"`
	HY2Traffic   int64  `json:"hy2_traffic"`
	Created      int64  `json:"created"`
	LastSub      int64  `json:"last_sub"`
}

func (u User) active() bool {
	return u.Enabled && (u.Expires == 0 || u.Expires > time.Now().Unix()) && (u.Quota == 0 || u.Upload+u.Download < u.Quota)
}

type Credentials struct {
	PublicToken  string            `json:"public_token"`
	HYGeneration uint64            `json:"hy_generation"`
	HY2          string            `json:"hy2"`
	Token        string            `json:"token"`
	VLESS        map[string]string `json:"vless"`
}
type Record struct {
	User
	Credentials Credentials
	Password    []byte
}
type Node struct {
	DNS            *NodeDNS     `json:"dns,omitempty"`
	DefaultDirect  bool         `json:"default_direct,omitempty"`
	RealitySNI     string       `json:"reality_sni,omitempty"`
	RealityIP      string       `json:"reality_ip,omitempty"`
	Quality        *IPQuality   `json:"quality,omitempty"`
	ManagedBy      string       `json:"managed_by,omitempty"`
	Upstream       object       `json:"upstream,omitempty"`
	UpstreamType   string       `json:"upstream_type,omitempty"`
	BridgePort     int          `json:"bridge_port,omitempty"`
	BridgePassword string       `json:"bridge_password,omitempty"`
	Speed          *SpeedResult `json:"speed,omitempty"`
	ExitID         string       `json:"exit_id,omitempty"`
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Protocol       string       `json:"protocol"`
	Enabled        bool         `json:"enabled"`
	Exit           string       `json:"exit"`
	Host           string       `json:"host"`
	Port           int          `json:"port"`
	Username       string       `json:"username"`
	Password       string       `json:"password,omitempty"`
	HasPassword    bool         `json:"has_password"`
	ProbeIP        string       `json:"probe_ip"`
	ProbedAt       int64        `json:"probed_at"`
	Country        string       `json:"country"`
	CountryCode    string       `json:"country_code"`
	CheckedAt      int64        `json:"checked_at"`
	ProbeError     string       `json:"probe_error"`
}

func (n Node) public() Node {
	n.RealityIP = ""
	n.HasPassword = n.Password != "" || n.Upstream != nil
	n.Upstream = nil
	n.BridgePassword = ""
	n.BridgePort = 0
	n.Password = ""
	return n
}

// Preserve the original main-node credential; derive distinct credentials for
// additional nodes without storing another plaintext-equivalent secret map.
func hyNodePassword(r Record, n Node) string {
	if n.ID == "hy2-main" {
		return r.Credentials.HY2
	}
	mac := hmac.New(sha256.New, []byte(r.Credentials.HY2))
	_, _ = mac.Write([]byte("guangyue/hy2/node/v1/" + n.ID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func hyNodeIdentity(r Record, n Node) string { return hyIdentity(r) + "@" + n.ID }
