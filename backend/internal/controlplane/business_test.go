package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createBusinessTest(t *testing.T, a *App, owner Record, id string) (BusinessSite, string) {
	t.Helper()
	w := req(t, a, owner, "POST", "/api/business-sites", object{"id": id, "name": id})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var out struct {
		Site       BusinessSite `json:"site"`
		Enrollment struct {
			Token string `json:"enrollment_token"`
		} `json:"enrollment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	site, err := a.store.businessSite(out.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	return site, out.Enrollment.Token
}
func businessTokenRequest(t *testing.T, a *App, path, token string, in any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(in)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Requested-With", "guangyue")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}
func TestBusinessLifecycle(t *testing.T) {
	master := testApp(t)
	master.cfg.Edition = "pro"
	master.cfg.Role = "controller"
	master.cfg.SiteID = "control"
	owner := testUser(t, master, "owner", "owner")
	user := testUser(t, master, "alice", "user")
	other := testUser(t, master, "bob", "user")
	user.Quota = 10000
	master.store.save(&user)
	site, token := createBusinessTest(t, master, owner, "east")
	site.Grants = []BusinessGrant{{UserID: user.ID, Quota: 4000}}
	if err := master.validateBusinessPolicy(site); err != nil {
		t.Fatal(err)
	}
	master.store.saveBusinessSite(site)
	agent := testApp(t)
	agent.cfg.Edition = "pro"
	agent.cfg.Role = "business"
	agent.cfg.SiteID = site.ID
	agent.cfg.EnrollmentToken = token
	agent.cfg.RealityPublic = randomToken(32)
	agent.cfg.ShortID = "aabbccddaabbccdd"
	server := httptest.NewServer(master.routes())
	defer server.Close()
	agent.cfg.ControllerURL = server.URL
	ctx := context.Background()
	if err := agent.syncBusinessAgent(ctx, server.Client()); err != nil {
		t.Fatal(err)
	}
	members, _ := agent.store.records()
	if len(members) != 1 || members[0].ID != user.ID {
		t.Fatalf("wrong scoped members: %d", len(members))
	}
	b, _ := json.Marshal(members)
	for _, secret := range []string{user.Credentials.Token, user.Credentials.HY2, other.Credentials.HY2, string(user.Password)} {
		if strings.Contains(string(b), secret) {
			t.Fatal("controller secret sent to agent")
		}
	}
	if w := req(t, agent, Record{}, "GET", "/api/state", nil); w.Code != 404 {
		t.Fatalf("agent exposed management %d", w.Code)
	}
	if err := agent.syncBusinessAgent(ctx, server.Client()); err != nil {
		t.Fatal(err)
	}
	catalog, err := master.subscriptionCatalog(user, false, "")
	if err != nil || len(catalog) != 4 {
		t.Fatalf("catalog: %d %v", len(catalog), err)
	}
	foreign, _ := master.subscriptionCatalog(other, false, "")
	if len(foreign) != 2 {
		t.Fatal("unassigned user received business nodes")
	}
	state, _ := agent.store.businessAgentState()
	if w := businessTokenRequest(t, master, "/api/business/sync", state.Token, BusinessHeartbeat{SiteID: "west"}); w.Code != 403 {
		t.Fatal("site ID substitution accepted")
	}
	info := BusinessInfo{SiteID: site.ID, Protocol: 1, Version: version, VLESSHost: agent.cfg.VLESSHost, HY2Host: agent.cfg.HY2Host, RealitySNI: agent.cfg.RealitySNI, RealityPublic: agent.cfg.RealityPublic, ShortID: agent.cfg.ShortID}
	if w := businessTokenRequest(t, master, "/api/business/enroll", token, info); w.Code != 401 {
		t.Fatal("enrollment token still usable")
	}
	members[0].Upload = 300
	members[0].Download = 700
	members[0].VLESSTraffic = 400
	members[0].HY2Traffic = 600
	agent.store.save(&members[0])
	for i := 0; i < 2; i++ {
		if err = agent.syncBusinessAgent(ctx, server.Client()); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := master.store.record(user.ID)
	if current.Upload+current.Download != 1000 {
		t.Fatal("traffic replay double counted")
	}
	records, _ := master.coreRecords()
	for _, u := range records {
		if u.ID == user.ID && u.Quota != 7000 {
			t.Fatalf("local reservation: %d", u.Quota)
		}
	}
	site, _ = master.store.businessSite(site.ID)
	site.Enabled = false
	site.Grants = nil
	master.store.saveBusinessSite(site)
	if err = agent.syncBusinessAgent(ctx, server.Client()); err != nil {
		t.Fatal(err)
	}
	site, _ = master.store.businessSite(site.ID)
	bad := BusinessHeartbeat{SiteID: site.ID, Applied: site.Desired, Usage: map[int64]BusinessUsage{}}
	if w := businessTokenRequest(t, master, "/api/business/sync", state.Token, bad); w.Code != 409 {
		t.Fatal("partial report released quota")
	}
	if err = agent.syncBusinessAgent(ctx, server.Client()); err != nil {
		t.Fatal(err)
	}
	site, _ = master.store.businessSite(site.ID)
	if siteReservation(site, user.ID) != 0 {
		t.Fatal("acknowledged revoke retained quota")
	}
	if w := req(t, master, owner, "DELETE", "/api/business-sites/"+site.ID, nil); w.Code != 200 {
		t.Fatalf("retire: %d %s", w.Code, w.Body)
	}
	state.LeaseUntil = time.Now().Add(-time.Minute).Unix()
	agent.store.saveBusinessAgent(state)
	for _, u := range mustCoreRecords(t, agent) {
		if u.Active() {
			t.Fatal("expired lease retained authorization")
		}
	}
}
func mustCoreRecords(t *testing.T, a *App) []Record {
	t.Helper()
	v, e := a.coreRecords()
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestBusinessAllocationsAndCounterRollback(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro"
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "alice", "user")
	u.Quota = 1000
	a.store.save(&u)
	site, _ := createBusinessTest(t, a, owner, "east")
	site.Grants = []BusinessGrant{{u.ID, 800}}
	a.store.saveBusinessSite(site)
	second, _ := createBusinessTest(t, a, owner, "west")
	second.Grants = []BusinessGrant{{u.ID, 300}}
	if a.validateBusinessPolicy(second) == nil {
		t.Fatal("oversubscribed quota accepted")
	}
	site.Usage[u.ID] = BusinessUsage{Upload: 40, Download: 60, VLESS: 100}
	a.store.saveBusinessSite(site)
	if a.acceptBusinessUsage(&site, BusinessHeartbeat{Usage: map[int64]BusinessUsage{u.ID: {Upload: 30, Download: 60, VLESS: 90}}}) == nil {
		t.Fatal("counter rollback accepted")
	}
	nodes, _ := a.store.nodes()
	pool := IPResource{Node: Node{ID: "external", Protocol: "vless", Exit: "socks5", Host: "proxy.example", Port: 1080, Enabled: true}, PoolGroup: "private"}
	a.store.savePoolNodes([]IPResource{pool}, nil)
	site.Exclusive = true
	site.Nodes = []Node{{ID: "exit-vless", ExitID: pool.ID, Protocol: "vless", Enabled: true}}
	a.store.saveBusinessSite(site)
	if a.checkExclusiveBusinessExit(pool.ID) == nil {
		t.Fatal("local exclusive binding accepted")
	}
	second.Grants = nil
	second.Nodes = site.Nodes
	if a.validateBusinessPolicy(second) == nil {
		t.Fatal("cross-site exclusive binding accepted")
	}
	_ = nodes
}
func TestDirectQualitySharedAcrossProtocols(t *testing.T) {
	a := testApp(t)
	nodes, _ := a.store.nodes()
	speed := &SpeedResult{Error: "protocol-specific"}
	nodes[1].Speed = speed
	a.store.saveNode(nodes[1])
	q := IPQuality{SchemaVersion: qualitySchemaVersion, At: time.Now().Unix(), IP: "8.8.8.8", CountryCode: "US", Tags: []QualityTag{{Key: "network", Label: "数据中心 IP", Status: "confirmed"}}}
	if err := a.saveQuality(nodes[0], false, q); err != nil {
		t.Fatal(err)
	}
	nodes, _ = a.store.nodes()
	for _, n := range nodes {
		if n.Quality == nil || n.Quality.At != q.At || n.ProbeIP != q.IP {
			t.Fatal("direct report not shared")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, status, err := a.measureResourceQuality(ctx, false, nodes[1].ID)
	if err != nil || status != http.StatusOK || result.At != q.At {
		t.Fatal("second protocol did not reuse cached report")
	}
	nodes, _ = a.store.nodes()
	if nodes[1].Speed == nil || nodes[1].Speed.Error != speed.Error {
		t.Fatal("quality changed protocol speed")
	}
	changed := nodes[0]
	changed.ProbeIP = "9.9.9.9"
	if a.reusableQuality(changed) != nil {
		t.Fatal("old IP report reused")
	}
	changed = nodes[0]
	changed.ManagedBy = publicManager
	changed.Quality = nil
	if a.reusableQuality(changed) != nil {
		t.Fatal("private report crossed public ownership")
	}
}

func TestBusinessCoreGuardExpiresWithoutPanel(t *testing.T) {
	dir := t.TempDir()
	if validBusinessLeaseFile(dir) {
		t.Fatal("missing lease enabled cores")
	}
	if err := writeJSON(filepath.Join(dir, "business-lease.json"), object{"lease_until": time.Now().Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !awaitBusinessLease(ctx, dir) {
		t.Fatal("valid lease refused")
	}
	go guardBusinessLease(ctx, dir, cancel)
	if err := os.Remove(filepath.Join(dir, "business-lease.json")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("core guard did not revoke after lease loss")
	}
}

func TestBusinessCommandsAndIdentityPinning(t *testing.T) {
	master := testApp(t)
	master.cfg.Edition = "pro"
	owner := testUser(t, master, "owner", "owner")
	site, token := createBusinessTest(t, master, owner, "east")
	info := BusinessInfo{SiteID: site.ID, Protocol: 1, Version: version, VLESSHost: "east.example.com", HY2Host: "east.example.com", RealitySNI: "www.cloudflare.com", RealityPublic: randomToken(32), ShortID: "aabbccddaabbccdd"}
	first := businessTokenRequest(t, master, "/api/business/enroll", token, info)
	if first.Code != 200 {
		t.Fatal("enroll failed")
	}
	again := businessTokenRequest(t, master, "/api/business/enroll", token, info)
	if again.Code != 200 || again.Body.String() != first.Body.String() {
		t.Fatal("enrollment retry lost token")
	}
	info.RealityPublic = randomToken(32)
	if w := businessTokenRequest(t, master, "/api/business/enroll", token, info); w.Code != 409 {
		t.Fatal("replacement machine reused enrollment")
	}
	site, _ = master.store.businessSite(site.ID)
	site.Grants = []BusinessGrant{{owner.ID, 0}}
	master.store.saveBusinessSite(site)
	snapshot, e := master.businessSnapshot(&site)
	if e != nil {
		t.Fatal(e)
	}
	agent := testApp(t)
	agent.cfg.Edition = "pro"
	agent.cfg.Role = "business"
	agent.cfg.SiteID = site.ID
	agent.initTasks()
	if e = agent.applyBusinessSnapshot(context.Background(), snapshot); e != nil {
		t.Fatal(e)
	}
	input := object{"kind": "quality", "node_id": "vless-main"}
	task := req(t, master, owner, "POST", "/api/business-sites/east/tasks", input)
	if task.Code != 202 {
		t.Fatal("remote task refused")
	}
	var cmd BusinessCommand
	json.Unmarshal(task.Body.Bytes(), &cmd)
	for i := 0; i < 2; i++ {
		if e = agent.queueBusinessCommands(context.Background(), []BusinessCommand{cmd}); e != nil {
			t.Fatal(e)
		}
	}
	tasks, e := agent.jobs.List(context.Background())
	if e != nil || len(tasks) != 1 {
		t.Fatal("remote command replay duplicated work")
	}
	site2, _ := createBusinessTest(t, master, owner, "west")
	site2.Grants = []BusinessGrant{{owner.ID, 0}}
	master.store.saveBusinessSite(site2)
	snapshot2, e := master.businessSnapshot(&site2)
	if e != nil {
		t.Fatal(e)
	}
	if e = agent.applyBusinessSnapshot(context.Background(), snapshot2); e == nil {
		t.Fatal("different site snapshot accepted")
	}
	secondAgent := testApp(t)
	secondAgent.cfg.Edition = "pro"
	secondAgent.cfg.Role = "business"
	secondAgent.cfg.SiteID = site2.ID
	if e = secondAgent.applyBusinessSnapshot(context.Background(), snapshot2); e != nil {
		t.Fatal(e)
	}
	aUsers, _ := agent.store.records()
	bUsers, _ := secondAgent.store.records()
	if aUsers[0].Credentials.HY2 == bUsers[0].Credentials.HY2 || aUsers[0].Credentials.VLESS["vless-main"] == bUsers[0].Credentials.VLESS["vless-main"] {
		t.Fatal("site credentials reused")
	}
}
