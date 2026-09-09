package controlplane

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{StateDir: dir}
	if strings.HasPrefix(t.Name(), "TestPostgresBehaviors/") {
		cfg.Edition = "pro"
		cfg.SiteID = "behavior_" + digest(t.Name() + randomToken(8))[:16]
		cfg.Database.Driver = "postgres"
		cfg.Database.DSN = os.Getenv("GY_TEST_POSTGRES_DSN")
	}
	s, err := openConfiguredStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cfg.Edition == "pro" {
			_, _ = s.db.Exec("DROP SCHEMA " + s.db.Schema() + " CASCADE")
		}
		s.db.Close()
	})
	a := &App{store: s, cfg: Config{StateDir: dir, PublicURL: "https://panel.test", VLESSHost: "vless.test", HY2Host: "hy.test", RealitySNI: "reality.test", RealityPublic: "public", ShortID: "12345678", Dev: true}, loginSlots: make(chan struct{}, 2), limits: map[string][]time.Time{}, started: time.Now()}
	if err = s.saveNode(Node{ID: "vless-main", Name: "VLESS 测试", Protocol: "vless", Enabled: true, Exit: "direct"}); err != nil {
		t.Fatal(err)
	}
	if err = s.saveNode(Node{ID: "hy2-main", Name: "HY2 测试", Protocol: "hy2", Enabled: true, Exit: "direct"}); err != nil {
		t.Fatal(err)
	}
	if err = s.migratePools(); err != nil {
		t.Fatal(err)
	}
	return a
}
func testUser(t *testing.T, a *App, name, role string) Record {
	t.Helper()
	p, err := bcrypt.GenerateFromPassword([]byte("test-password-123456"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	r := Record{User: User{Username: name, Role: role, Enabled: true, VLESS: true, HY2: true}, Credentials: Credentials{HY2: randomToken(24), Token: randomToken(32), VLESS: map[string]string{"vless-main": uuid()}}, Password: p}
	if err = a.store.save(&r); err != nil {
		t.Fatal(err)
	}
	return r
}
func req(t *testing.T, a *App, user Record, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "guangyue")
	if user.ID > 0 {
		token := randomToken(32)
		_, err := a.store.db.Exec("INSERT INTO sessions VALUES(?,?,?)", digest(token), user.ID, time.Now().Add(time.Hour).Unix())
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: "gy_session", Value: token})
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func TestUserIsolationAndCSRF(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	for _, test := range []struct {
		method, path string
		want         int
	}{{"GET", "/api/subscription?user_id=1", 403}, {"GET", "/api/backup", 403}, {"POST", "/api/users", 403}, {"DELETE", "/api/users/3", 403}} {
		w := req(t, a, alice, test.method, test.path, object{})
		if w.Code != test.want {
			t.Fatalf("%s %s: %d", test.method, test.path, w.Code)
		}
	}
	w := req(t, a, alice, "GET", "/api/state", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if strings.Contains(w.Body.String(), "bob") || strings.Contains(w.Body.String(), bob.Credentials.Token) || strings.Contains(w.Body.String(), alice.Credentials.HY2) {
		t.Fatal("cross-user or credential leak")
	}
	w = req(t, a, owner, "GET", "/api/subscription?user_id=3", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), bob.Credentials.Token) {
		t.Fatal("owner cannot manage subscription")
	}
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"owner","password":"test-password-123456"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF header accepted")
	}
	r = httptest.NewRequest("POST", "/api/login", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "guangyue")
	r.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
}
func TestSubscriptionsAndCredentialRotation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "alice", "user")
	nodes, _ := a.store.nodes()
	for _, format := range []string{"raw", "base64", "mihomo"} {
		body, _, err := subscription(a.cfg, u, nodes, format, "")
		if err != nil {
			t.Fatal(err)
		}
		switch format {
		case "base64":
			body, err = base64.StdEncoding.DecodeString(string(body))
			if err != nil {
				t.Fatal(err)
			}
			fallthrough
		case "raw":
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			if len(lines) != 2 {
				t.Fatal("expected both protocols")
			}
			for _, line := range lines {
				parsed, err := url.Parse(line)
				if err != nil || parsed.Port() != "443" {
					t.Fatal("invalid URI")
				}
			}
		case "mihomo":
			var doc struct {
				Proxies []map[string]any `yaml:"proxies"`
			}
			if yaml.Unmarshal(body, &doc) != nil || len(doc.Proxies) != 2 {
				t.Fatal("invalid Mihomo config")
			}
		}
	}
	w := req(t, a, owner, "POST", "/api/users/2/rotate-sub", object{})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	rotated, _ := a.store.record(u.ID)
	if rotated.Credentials.Token == u.Credentials.Token || rotated.Credentials.HY2 != u.Credentials.HY2 || rotated.Credentials.VLESS["vless-main"] != u.Credentials.VLESS["vless-main"] {
		t.Fatal("subscription rotation changed protocol credentials")
	}
	if req(t, a, Record{}, "GET", "/sub/"+u.Credentials.Token, nil).Code != 404 {
		t.Fatal("old token valid")
	}
	w = req(t, a, owner, "POST", "/api/users/2/revoke", object{})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	revoked, _ := a.store.record(u.ID)
	if revoked.Credentials.Token == rotated.Credentials.Token || revoked.Credentials.HY2 == u.Credentials.HY2 || revoked.Credentials.VLESS["vless-main"] == u.Credentials.VLESS["vless-main"] {
		t.Fatal("revocation retained credentials")
	}
	var pending int
	_ = a.store.db.QueryRow("SELECT COUNT(*) FROM revocations").Scan(&pending)
	if pending != 1 {
		t.Fatal("missing HY2 kick")
	}
}

func TestBackupRestorePreservesEncryptedCredentialsAndRevocations(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "owner", "owner")
	u.Upload = 123456
	if err := a.store.save(&u); err != nil {
		t.Fatal(err)
	}
	if err := a.store.queueKick(99); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, u, "GET", "/api/backup", nil)
	if w.Code != 200 {
		t.Fatalf("backup: %s", w.Body.String())
	}
	path := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := atomicWrite(path, w.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	a.store.db.Close()
	if err := restoreBackup(a.cfg, path); err != nil {
		t.Fatal(err)
	}
	restored, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	record, err := restored.record(u.ID)
	if err != nil || record.Upload != 123456 || record.Credentials.HY2 != u.Credentials.HY2 || record.Credentials.Token != u.Credentials.Token {
		t.Fatal("restore lost data or credentials", err)
	}
	var sessions, revocations int
	_ = restored.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions)
	_ = restored.db.QueryRow("SELECT COUNT(*) FROM revocations").Scan(&revocations)
	if sessions != 0 || revocations != 1 {
		t.Fatal("restored sessions or lost durable revocation")
	}
}
func TestQuotaExpiryAndHY2Auth(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	auth := func() bool {
		b, _ := json.Marshal(object{"auth": u.Credentials.HY2, "addr": "127.0.0.1:1000", "tx": 0})
		w := httptest.NewRecorder()
		a.hyAuth(w, httptest.NewRequest("POST", "/auth", bytes.NewReader(b)))
		var o struct {
			OK bool `json:"ok"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &o)
		return o.OK
	}
	if !auth() {
		t.Fatal("valid auth rejected")
	}
	cases := []User{u.User, u.User, u.User, u.User}
	cases[0].Enabled = false
	cases[1].Expires = time.Now().Add(-time.Second).Unix()
	cases[2].Quota = 100
	cases[2].Upload = 100
	cases[3].HY2 = false
	for _, state := range cases {
		u.User = state
		if err := a.store.save(&u); err != nil {
			t.Fatal(err)
		}
		if auth() {
			t.Fatal("ineligible HY2 user authenticated")
		}
	}
	if req(t, a, Record{}, "GET", "/sub/"+u.Credentials.Token, nil).Code != 200 {
		t.Fatal("VLESS-only subscription should remain valid")
	}
	u.Enabled = false
	_ = a.store.save(&u)
	if req(t, a, Record{}, "GET", "/sub/"+u.Credentials.Token, nil).Code != 403 {
		t.Fatal("disabled subscription accepted")
	}
}
func TestTrafficCheckpointRestartAndQuota(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	u.Quota = 500
	_ = a.store.save(&u)
	c := Counter{Key: "x:test", Generation: "1", UserID: u.ID, Protocol: "vless", Direction: "down", Value: 300}
	if err := a.store.account([]Counter{c, c}); err != nil {
		t.Fatal(err)
	}
	r, _ := a.store.record(u.ID)
	if r.Download != 300 {
		t.Fatal("duplicate sample double counted")
	}
	c.Value = 350
	_ = a.store.account([]Counter{c})
	c.Generation = "2"
	c.Value = 200
	_ = a.store.account([]Counter{c})
	r, _ = a.store.record(u.ID)
	if r.Download != 550 || r.Active() {
		t.Fatal("restart counter or quota incorrect")
	}
	c.Value = 10
	_ = a.store.account([]Counter{c})
	r, _ = a.store.record(u.ID)
	if r.Download != 560 {
		t.Fatal("counter reset lost traffic")
	}
}
func TestEncryptedSecretsAndTampering(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	var ciphertext []byte
	if err := a.store.db.QueryRow("SELECT credentials FROM users WHERE id=?", u.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(u.Credentials.HY2)) || bytes.Contains(ciphertext, []byte(u.Credentials.Token)) {
		t.Fatal("secrets stored plaintext")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	var c Credentials
	if a.store.vault.open(ciphertext, &c) == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
func TestOutboundRenderingAndValidation(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	for _, mode := range []string{"direct", "http", "socks5"} {
		n := Node{ID: "vless-main", Name: "Node", Protocol: "vless", Enabled: true, Exit: mode, Host: "127.0.0.1", Port: 1080, Username: "a:@", Password: "p:/@"}
		if err := validateNode(n); err != nil {
			t.Fatal(err)
		}
		xc := xrayConfig(a.cfg, []Record{u}, []Node{n})
		out := xc["outbounds"].([]object)
		if out[0]["protocol"] != "blackhole" {
			t.Fatal("unmatched user can leak to direct")
		}
		n.Protocol = "hy2"
		hc := hy2Config(a.cfg, []Node{n})
		hyout := []object{hc["nodeOutbounds"].(object)[n.ID].(object)}
		if len(hyout) != 1 || hyout[0]["type"] != mode {
			t.Fatal("HY2 exit contains implicit direct fallback")
		}
		if mode == "http" {
			s := hyout[0]["http"].(object)["url"].(string)
			parsed, err := url.Parse(s)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := parsed.User.Password()
			if parsed.User.Username() != n.Username || p != n.Password {
				t.Fatal("proxy credential escaping failed")
			}
		}
	}
	for _, n := range []Node{{Name: "bad", Protocol: "vless", Exit: "http", Host: "host/path", Port: 80}, {Name: "bad", Protocol: "vless", Exit: "socks5", Host: "localhost", Port: 0}, {Name: "bad", Protocol: "hy2", Exit: "http", Host: "localhost", Port: 80, Username: "u"}} {
		if validateNode(n) == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
}
func TestOwnerProtectionAndLoginCookie(t *testing.T) {
	a := testApp(t)
	o := testUser(t, a, "owner", "owner")
	if req(t, a, o, "DELETE", "/api/users/1", nil).Code != 400 {
		t.Fatal("owner deletable")
	}
	w := req(t, a, Record{}, "POST", "/api/login", object{"username": "owner", "password": "test-password-123456"})
	if w.Code != 200 {
		t.Fatal("login failed")
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak session cookie")
	}
	w = req(t, a, Record{}, "POST", "/api/login", object{"username": "owner", "password": "wrong"})
	if w.Code != 401 {
		t.Fatal("wrong password accepted")
	}
}

func TestXrayZeroExitStillRequiresUserReadback(t *testing.T) {
	a := testApp(t)
	testUser(t, a, "alice", "user")
	a.cfg.Dev = false
	b, err := os.ReadFile("testdata/xray-noop.sh")
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.Xray = filepath.Join(t.TempDir(), "xray")
	if err = atomicWrite(a.cfg.Xray, b, 0700); err != nil {
		t.Fatal(err)
	}
	nodes, _ := a.store.nodes()
	_ = a.store.setMeta("x_nodes", nodeHash(nodes, "vless"))
	_ = a.store.setMeta("hy_nodes", nodeHash(nodes, "hy2"))
	err = a.reconcile()
	if err == nil || !strings.Contains(err.Error(), "verification mismatch") {
		t.Fatalf("zero-exit CLI failure reported success: %v", err)
	}
}

func TestHY2ReauthorizationUsesNewConnectionIdentity(t *testing.T) {
	a := testApp(t)
	o := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "alice", "user")
	oldIdentity := hyIdentity(u)
	w := req(t, a, o, "PUT", "/api/users/2", object{"username": "alice", "enabled": true, "vless": true, "hy2": true, "expires": 0, "quota": 10000})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	updated, err := a.store.record(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hyIdentity(updated) == oldIdentity || updated.Credentials.HY2 != u.Credentials.HY2 {
		t.Fatal("reauthorization must avoid old HY2 kick markers without changing the client password")
	}
}

func TestNoAuthorizedNodesDoesNotExportDirectOnlyProfile(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	u.VLESS, u.HY2 = false, false
	_ = a.store.save(&u)
	w := req(t, a, Record{}, "GET", "/sub/"+u.Credentials.Token+"?format=mihomo", nil)
	if w.Code != 403 {
		t.Fatal("empty authorization produced a direct-only profile")
	}
}
