package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func publicTestSettings(t *testing.T, a *App) PublicSettings {
	t.Helper()
	c := defaultPublicSettings()
	c.Enabled = true
	c.MaxResources = 2
	c.Revision = "test-revision"
	c.Sources = []string{"proxifly"}
	b, _ := json.Marshal(c)
	if err := a.store.setMeta("public_settings", string(b)); err != nil {
		t.Fatal(err)
	}
	return c
}
func publicTestPool(host string) IPResource {
	p := parsePublicList([]byte("socks5://"+host+":1080"), publicSources[0])[0]
	p.Reachable = true
	p.ProbeIP = host
	p.Country = "美国"
	p.CountryCode = "US"
	p.Name = egressName(p.Country, host)
	return p
}
func publicTestChecks(feed string) publicChecks {
	return publicChecks{
		baseline: func(context.Context) error { return nil }, fetch: func(context.Context, string) ([]byte, error) { return []byte(feed), nil },
		performance: func(_ context.Context, p IPResource, _ PublicSettings) publicProbeResult {
			p.Reachable = true
			p.ProbeIP = p.Host
			p.CheckedAt = time.Now().Unix()
			p.ProbedAt = p.CheckedAt
			p.Speed = &SpeedResult{At: p.CheckedAt, LatencyMS: 200, Mbps: 12, Bytes: 1 << 20}
			return publicProbeResult{p, ""}
		},
		quality: func(_ context.Context, n Node) IPQuality {
			return IPQuality{At: time.Now().Unix(), IP: n.ProbeIP, CountryCode: "US", Tags: []QualityTag{}, Streams: []QualityTag{}}
		},
	}
}

func TestPublicSourceParserRejectsNonPublicOrCredentialTargets(t *testing.T) {
	feed := `http://8.8.8.8:8080
http://8.8.8.8:8080
socks5://[2606:4700:4700::1111]:1080
http://user:pass@1.1.1.1:80
http://localhost:80
http://127.0.0.1:80
http://10.0.0.1:80
http://100.64.1.1:80
http://169.254.169.254:80
http://192.0.2.10:80
http://198.18.0.1:80
http://198.51.100.1:80
http://203.0.113.1:80
http://240.0.0.1:80
http://[2001:db8::1]:80
http://[::ffff:127.0.0.1]:80
http://1.1.1.1:0
http://1.1.1.1:65536
http://1.1.1.1:80/path
http://1.1.1.1:80?url=secret
socks4://1.1.1.1:1080
https://1.1.1.1:443`
	p := parsePublicList([]byte(feed), publicSources[0])
	if len(p) != 2 {
		t.Fatalf("expected two valid unique proxies, got %d", len(p))
	}
	for _, v := range p {
		if v.PoolGroup != "public" || v.ManagedBy != publicManager || v.Username != "" || v.Reachable {
			t.Fatal("source metadata or validation state incorrect")
		}
	}
	plain := parsePublicList([]byte("8.8.8.8:8080\n"), publicSources[1])
	if len(plain) != 1 || plain[0].ID != p[0].ID {
		t.Fatal("provider representations do not deduplicate")
	}
}

func TestPublicCollectorAddsRechecksReplacesAndDisablesOwnNodes(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	c := publicTestSettings(t, a)
	private := IPResource{Node: Node{ID: "private-proxy", Protocol: "vless", Exit: "http", Host: "9.9.9.9", Port: 9090, Enabled: true}, Label: "existing private"}
	if err := a.store.savePoolNodes([]IPResource{private}, nil); err != nil {
		t.Fatal(err)
	}
	beforeNodes, _ := a.store.nodes()
	beforeOwner := owner.Credentials
	deps := publicTestChecks("socks5://8.8.8.8:1080\nsocks5://1.1.1.1:1080")
	a.collectPublicWith(context.Background(), c, deps)
	nodes, _ := a.store.nodes()
	pools, _ := a.store.pools()
	if len(nodes) != 6 || len(pools) != 3 {
		t.Fatalf("unexpected initial membership %d / %d", len(nodes), len(pools))
	}
	o, _ := a.store.record(owner.ID)
	m, _ := a.store.record(member.ID)
	for _, n := range nodes {
		if n.ManagedBy == publicManager && n.Protocol == "vless" {
			if o.Credentials.VLESS[n.ID] == "" || o.Credentials.VLESS[n.ID] == m.Credentials.VLESS[n.ID] {
				t.Fatal("managed nodes missing independent member credentials")
			}
		}
	}
	firstCredentials := o.Credentials
	a.collectPublicWith(context.Background(), c, deps)
	o, _ = a.store.record(owner.ID)
	if !reflect.DeepEqual(firstCredentials, o.Credentials) {
		t.Fatal("healthy refresh rotated stable credentials")
	}
	deps.fetch = func(context.Context, string) ([]byte, error) {
		return []byte("socks5://8.8.8.8:1080\nsocks5://1.1.1.1:1080\nsocks5://8.8.4.4:1080"), nil
	}
	good := deps.performance
	counts := map[string]int{}
	var mu sync.Mutex
	deps.performance = func(ctx context.Context, p IPResource, c PublicSettings) publicProbeResult {
		mu.Lock()
		counts[p.Host]++
		mu.Unlock()
		if p.Host == "8.8.8.8" {
			return publicProbeResult{p, "延迟超过阈值"}
		}
		if p.Host == "1.1.1.1" {
			return publicProbeResult{p, "下载速度低于阈值"}
		}
		return good(ctx, p, c)
	}
	a.collectPublicWith(context.Background(), c, deps)
	pools, _ = a.store.pools()
	nodes, _ = a.store.nodes()
	o, _ = a.store.record(owner.ID)
	if len(nodes) != 4 || len(pools) != 2 || counts["8.8.8.8"] != 1 || counts["1.1.1.1"] != 1 {
		t.Fatal("failed proxies were not immediately blocked and replaced")
	}
	for _, n := range nodes {
		if n.ManagedBy == publicManager && n.Host != "8.8.4.4" {
			t.Fatal("failed automatic node survived")
		}
	}
	for id := range firstCredentials.VLESS {
		if strings.HasPrefix(id, "auto-") && o.Credentials.VLESS[id] != "" {
			t.Fatal("removed node credential not removed")
		}
	}
	for _, n := range beforeNodes {
		found := false
		for _, cur := range nodes {
			if reflect.DeepEqual(cur, n) {
				found = true
			}
		}
		if !found {
			t.Fatal("manual node changed")
		}
	}
	if o.Credentials.Token != beforeOwner.Token || o.Credentials.HY2 != beforeOwner.HY2 || o.Credentials.VLESS["vless-main"] != beforeOwner.VLESS["vless-main"] {
		t.Fatal("original credentials changed")
	}
	c.Enabled = false
	w := req(t, a, owner, "PUT", "/api/public-pool", c)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	nodes, _ = a.store.nodes()
	pools, _ = a.store.pools()
	if len(nodes) != 2 || len(pools) != 1 || pools[0].ID != private.ID {
		t.Fatal("disable did not exclusively clear automatic membership")
	}
	o, _ = a.store.record(owner.ID)
	if !reflect.DeepEqual(o.Credentials, beforeOwner) {
		t.Fatal("disable left stale credentials")
	}
	if a.store.publicSettings().Enabled {
		t.Fatal("disabled switch did not persist")
	}
}

func TestPublicDestinationOutageAndCancelledRunPreserveCurrentPool(t *testing.T) {
	a := testApp(t)
	testUser(t, a, "owner", "owner")
	c := publicTestSettings(t, a)
	p := publicTestPool("8.8.8.8")
	if err := a.applyPublicSetLocked([]IPResource{p}); err != nil {
		t.Fatal(err)
	}
	deps := publicTestChecks("")
	deps.baseline = func(context.Context) error { return errors.New("destination down") }
	deps.performance = func(context.Context, IPResource, PublicSettings) publicProbeResult {
		t.Error("proxy tested during destination outage")
		return publicProbeResult{}
	}
	a.collectPublicWith(context.Background(), c, deps)
	nodes, _ := a.store.nodes()
	if len(nodes) != 4 {
		t.Fatal("destination outage evicted valid pool")
	}
	ctx, cancel := context.WithCancel(context.Background())
	deps = publicTestChecks("")
	deps.fetch = func(context.Context, string) ([]byte, error) { cancel(); return nil, nil }
	a.collectPublicWith(ctx, c, deps)
	nodes, _ = a.store.nodes()
	if len(nodes) != 4 {
		t.Fatal("cancelled scan mutated membership")
	}
}

func TestPublicDisabledDuringScanCannotRepopulatePool(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	c := publicTestSettings(t, a)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	deps := publicTestChecks("socks5://8.8.8.8:1080")
	original := deps.performance
	deps.performance = func(ctx context.Context, p IPResource, c PublicSettings) publicProbeResult {
		close(entered)
		<-release
		return original(ctx, p, c)
	}
	go func() { a.collectPublicWith(context.Background(), c, deps); close(done) }()
	<-entered
	c.Enabled = false
	w := req(t, a, owner, "PUT", "/api/public-pool", c)
	close(release)
	<-done
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	pools, _ := a.store.pools()
	nodes, _ := a.store.nodes()
	if len(pools) != 0 || len(nodes) != 2 {
		t.Fatal("stale scan restored disabled public resources")
	}
}

func TestPublicTransactionRollsBackOnCoreWriteFailure(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	old, _ := a.store.nodes()
	path := filepath.Join(a.cfg.StateDir, "xray.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.applyPublicSetLocked([]IPResource{publicTestPool("8.8.8.8")}); err == nil {
		t.Fatal("core apply failure accepted")
	}
	nodes, _ := a.store.nodes()
	pools, _ := a.store.pools()
	r, _ := a.store.record(owner.ID)
	if !reflect.DeepEqual(nodes, old) || len(pools) != 0 || !reflect.DeepEqual(r.Credentials, owner.Credentials) {
		t.Fatal("failed apply did not restore membership and credentials")
	}
}

func TestPublicAdminBoundaryAndManualNodeProtection(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	for _, route := range []struct{ method, path string }{{"GET", "/api/public-pool"}, {"PUT", "/api/public-pool"}, {"POST", "/api/public-pool/run"}, {"POST", "/api/nodes/vless-main/quality"}} {
		if req(t, a, member, route.method, route.path, object{}).Code != 403 {
			t.Fatal("member reached infrastructure management")
		}
	}
	if req(t, a, owner, "POST", "/api/public-pool/run", object{}).Code != 409 {
		t.Fatal("disabled collection started")
	}
	p := publicTestPool("8.8.8.8")
	if err := a.applyPublicSetLocked([]IPResource{p}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := a.store.nodes()
	for _, n := range nodes {
		if n.ManagedBy == publicManager {
			if req(t, a, owner, "DELETE", "/api/nodes/"+n.ID, nil).Code != 409 {
				t.Fatal("manual API deleted automatic node")
			}
			if req(t, a, owner, "POST", "/api/nodes", n).Code != 409 {
				t.Fatal("manual API edited automatic node")
			}
		}
	}
	if req(t, a, owner, "DELETE", "/api/nodes/vless-main", nil).Code != 400 {
		t.Fatal("last manual VLESS could be removed relying on ephemeral node")
	}
	if req(t, a, owner, "POST", "/api/nodes", Node{Protocol: "vless", Enabled: true, ExitID: p.ID}).Code != 400 {
		t.Fatal("manual node bound an ephemeral pool entry")
	}
	if req(t, a, owner, "POST", "/api/ips", p).Code != 409 || req(t, a, owner, "DELETE", "/api/ips/"+p.ID, nil).Code != 409 {
		t.Fatal("public pool management guard missing")
	}
	c := defaultPublicSettings()
	c.Sources = []string{"http://127.0.0.1"}
	if validatePublicSettings(c) == nil {
		t.Fatal("arbitrary collector URL accepted")
	}
	c = defaultPublicSettings()
	c.MaxResources = 6
	if validatePublicSettings(c) == nil {
		t.Fatal("unbounded pool accepted")
	}
}

func TestQualitySignalsDoNotClaimPlaybackOrResidentialWithoutEvidence(t *testing.T) {
	if got := classifyStream("disney", "Disney+", qualityPage{code: 200, body: `<script>var message="not available in your region";</script><h1>Disney+</h1>`}); got.Status == "blocked" {
		t.Fatal("unused script localization misclassified as regional block")
	}
	if got := classifyNetflix([]qualityPage{{code: 403, body: "Forbidden"}, {code: 404, body: "Not found"}}); got.Status != "unknown" {
		t.Fatal("generic HTTP failures were misclassified")
	}
	if got := classifyNetflix([]qualityPage{{code: 200, body: "Netflix sign in"}}); got.Status != "page" {
		t.Fatal("generic page claimed unlock")
	}
	if got := classifyNetflix([]qualityPage{{code: 200, body: `<meta property="og:video" content="video">`}}); got.Status != "confirmed" || !strings.Contains(got.Evidence, "未验证") {
		t.Fatal("positive public signal lacks playback limitation")
	}
	if got := classifyStream("disney", "Disney+", qualityPage{code: 200, body: "Disney+ is not available in your region"}); got.Status != "blocked" {
		t.Fatal("explicit geo restriction lost")
	}
	if got := classifyStream("youtube", "YouTube Premium", qualityPage{code: 200, body: "Verify you are human: YouTube Premium"}); got.Status != "unknown" {
		t.Fatal("challenge treated as availability")
	}
	n, location, _, _ := classifyIP(object{}, "", "SG")
	if n.Status != "unknown" || location.Status != "unknown" {
		t.Fatal("missing metadata inferred as native/residential")
	}
	n, location, _, _ = classifyIP(object{"asn": "AS132203 Tencent", "company": "Tencent Cloud"}, "US", "SG")
	if n.Status != "unknown" || location.Label != "注册与定位国家不一致" {
		t.Fatal("organization name was overclaimed or registry comparison missing")
	}
	n, location, _, _ = classifyIP(object{"company": object{"type": "isp", "name": "Example ISP"}}, "SG", "SG")
	if n.Label != "ISP 运营商网络" || location.Label != "注册与定位国家一致" {
		t.Fatal("explicit ISP type or registry comparison missing")
	}
}

func TestRediscoveredPublicProxyCannotReviveDeletedHY2Credentials(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	p := publicTestPool("8.8.8.8")
	if err := a.applyPublicSetLocked([]IPResource{p}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := a.store.nodes()
	var old Node
	for _, n := range nodes {
		if n.ManagedBy == publicManager && n.Protocol == "hy2" {
			old = n
		}
	}
	if err := a.applyPublicSetLocked(nil); err != nil {
		t.Fatal(err)
	}
	if err := a.applyPublicSetLocked([]IPResource{p}); err != nil {
		t.Fatal(err)
	}
	nodes, _ = a.store.nodes()
	for _, n := range nodes {
		if n.ManagedBy == publicManager && n.Protocol == "hy2" {
			if n.ID == old.ID || hyNodePassword(owner, n) == hyNodePassword(owner, old) {
				t.Fatal("rediscovery revived the deleted HY2 identity")
			}
		}
	}
}

func TestQualityMetadataCannotRestartCoresOrFollowChangedExit(t *testing.T) {
	a := testApp(t)
	n := Node{ID: "fixture", Protocol: "vless", Enabled: true, Exit: "http", Host: "8.8.8.8", Port: 8080, ProbeIP: "8.8.8.8"}
	before := nodeHash([]Node{n}, "vless")
	n.Quality = &IPQuality{IP: "8.8.8.8", At: time.Now().Unix()}
	n.ManagedBy = publicManager
	if nodeHash([]Node{n}, "vless") != before {
		t.Fatal("quality metadata changes core hash")
	}
	applyEgress(&n, Egress{IP: "1.1.1.1", CountryCode: "US"}, nil)
	if n.Quality != nil {
		t.Fatal("quality of old egress survived IP change")
	}
	old := n
	if err := a.store.saveNode(n); err != nil {
		t.Fatal(err)
	}
	n.Host = "9.9.9.9"
	if err := a.store.saveNode(n); err != nil {
		t.Fatal(err)
	}
	if a.saveQuality(old, false, IPQuality{IP: old.ProbeIP}) == nil {
		t.Fatal("stale quality overwrote changed exit")
	}
}
