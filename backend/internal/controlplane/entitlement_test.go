package controlplane

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func createTestPlan(t *testing.T, a *App, owner Record) Plan {
	t.Helper()
	p := Plan{Name: "Office", Quota: 1000, ValidDays: 30, Cycle: "30d", GroupIDs: []string{legacyPrivateGroup}, VLESS: true, HY2: true}
	w := req(t, a, owner, "POST", "/api/plans", p)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanPriceRoundTrip(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	p := Plan{Name: "Starter", Price: 1999, Quota: 1000, ValidDays: 30, Cycle: "30d", GroupIDs: []string{legacyPrivateGroup}, VLESS: true}
	w := req(t, a, owner, "POST", "/api/plans", p)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved Plan
	if e := json.Unmarshal(w.Body.Bytes(), &saved); e != nil {
		t.Fatal(e)
	}
	if saved.Price != 1999 {
		t.Fatalf("saved price=%d", saved.Price)
	}
	plans, e := a.store.plans()
	if e != nil || len(plans) != 1 || plans[0].Price != 1999 {
		t.Fatalf("stored plans=%+v err=%v", plans, e)
	}
}
func applyTestEntitlement(t *testing.T, a *App, owner Record, in entitlementRequest) {
	t.Helper()
	in.Preview = true
	w := req(t, a, owner, "POST", "/api/entitlements/batch", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var preview struct {
		Expected string `json:"expected"`
	}
	json.Unmarshal(w.Body.Bytes(), &preview)
	in.Preview = false
	in.Expected = preview.Expected
	in.OperationID = randomToken(18)
	w = req(t, a, owner, "POST", "/api/entitlements/batch", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w2 := req(t, a, owner, "POST", "/api/entitlements/batch", in)
	if w2.Code != 200 || w2.Body.String() != w.Body.String() {
		t.Fatal("idempotent entitlement replay changed result")
	}
}
func TestEntitlementSnapshotsRenewResetAndAccess(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	p := createTestPlan(t, a, owner)
	u.Upload = 50
	u.VLESSTraffic = 50
	a.store.save(&u)
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "assign", PlanID: p.ID})
	u, _ = a.store.record(u.ID)
	before := u.User
	if u.QuotaUsed() != 50 || u.Entitlement.Version != 1 {
		t.Fatal("assignment erased existing usage")
	}
	p.Quota = 2000
	p.Name = "Office v2"
	w := req(t, a, owner, "POST", "/api/plans", p)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	u, _ = a.store.record(u.ID)
	if u.Quota != 1000 || u.Entitlement.Name != "Office" {
		t.Fatal("template edit mutated issued snapshot")
	}
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "renew", Days: 5})
	u, _ = a.store.record(u.ID)
	if u.Expires != before.Expires+5*86400 || u.QuotaUsed() != 50 {
		t.Fatal("renew reset usage or expiration")
	}
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "reset"})
	u, _ = a.store.record(u.ID)
	if u.Active() {
		t.Fatal("reset did not revoke old period")
	}
	if err := a.advanceQuotaPeriods(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	u, _ = a.store.record(u.ID)
	if u.QuotaUsed() != 0 || u.Upload != 50 || u.Meter.PeriodID == before.Meter.PeriodID {
		t.Fatal("reset lost lifetime counters")
	}
	nodes, _ := a.store.nodes()
	public := Node{ID: "public-hy", Protocol: "hy2", Enabled: true, ManagedBy: publicManager}
	if memberMayUseNode(u, public) {
		t.Fatal("private plan leaked public permission")
	}
	if !memberMayUseNode(u, nodes[0]) && !memberMayUseNode(u, nodes[1]) {
		t.Fatal("plan cannot use private nodes")
	}
	groups, _ := a.store.nodeGroups()
	for _, g := range groups {
		if g.ID == legacyPrivateGroup {
			g.Enabled = false
			w = req(t, a, owner, "POST", "/api/node-groups", g)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
		}
	}
	entries, err := a.subscriptionCatalog(u, false, "")
	if err != nil || len(entries) != 0 {
		t.Fatal("disabled group remained in subscription")
	}
	records, _ := a.coreRecords()
	for _, r := range records {
		if r.ID == u.ID {
			for _, n := range nodes {
				if memberMayUseNode(r, n) {
					t.Fatal("disabled group retained core authorization")
				}
			}
		}
	}
}
func TestNodeMeterRatesChangeDeletionAndNoLogs(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "metered", "user")
	owner := testUser(t, a, "owner", "owner")
	switchRuntimeTest(t, a, owner, runtimeNoLogs)
	n := normalizeNodePolicy(Node{ID: "extra", Protocol: "vless", Enabled: true})
	n.RateMilli = 1500
	n.RateRevision = "r1"
	a.store.saveNode(n)
	c := Counter{Key: "rate-test", Generation: "one", UserID: u.ID, NodeID: n.ID, Protocol: "vless", Direction: "up", Value: 1}
	for i := int64(1); i <= 3; i++ {
		c.Value = i
		if err := a.store.account([]Counter{c, c}); err != nil {
			t.Fatal(err)
		}
	}
	u, _ = a.store.record(u.ID)
	if u.Upload != 3 || u.QuotaUsed() != 4 || u.Meter.UploadRemainder != 500 {
		t.Fatal("fractional accounting failed")
	}
	n.RateMilli = 500
	n.RateRevision = "r2"
	a.store.saveNode(n)
	c.Value = 6
	a.store.account([]Counter{c})
	u, _ = a.store.record(u.ID)
	if u.Upload != 6 || u.QuotaUsed() != 6 {
		t.Fatal("rate change reweighted history")
	}
	_, err := a.store.db.Exec("DELETE FROM nodes WHERE id=?", n.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Value = 8
	if err = a.store.account([]Counter{c}); err != nil {
		t.Fatal(err)
	}
	u, _ = a.store.record(u.ID)
	if u.QuotaUsed() != 7 {
		t.Fatal("deleted node lost its final multiplier")
	}
	rows, err := a.store.nodeUsage()
	if err != nil || len(rows) != 2 {
		t.Fatal("rate history was erased on delete")
	}
	if runtimeTableCount(t, a.store, "traffic") != 0 {
		t.Fatal("no_logs wrote optional traffic history")
	}
}
func TestNodeDeleteRemovesGroupAndPreservesUsage(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	n := normalizeNodePolicy(Node{ID: "extra", Protocol: "vless", Exit: "direct", Enabled: true})
	n.GroupIDs = []string{legacyPrivateGroup}
	a.store.saveNode(n)
	u.Credentials.VLESS[n.ID] = uuid()
	a.store.save(&u)
	a.store.account([]Counter{{Key: "deleted", Generation: "one", UserID: u.ID, NodeID: n.ID, Protocol: "vless", Direction: "up", Value: 17}})
	w := req(t, a, owner, "POST", "/api/nodes/batch", batchRequest{IDs: []string{n.ID}, Action: "delete"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = req(t, a, owner, "GET", "/api/node-groups", nil)
	var view struct {
		Members map[string][]struct {
			NodeID string `json:"node_id"`
		} `json:"members"`
	}
	json.Unmarshal(w.Body.Bytes(), &view)
	for _, members := range view.Members {
		for _, m := range members {
			if m.NodeID == n.ID {
				t.Fatal("deleted node remained in group")
			}
		}
	}
	u, _ = a.store.record(u.ID)
	if u.Credentials.VLESS[n.ID] != "" || u.QuotaUsed() != 17 {
		t.Fatal("cascade erased usage or retained credentials")
	}
	rows, _ := a.store.nodeUsage()
	if len(rows) != 1 || rows[0].Upload != 17 {
		t.Fatal("deleted usage history missing")
	}
}
func TestMeterMetadataDoesNotRestartRoutes(t *testing.T) {
	n := Node{ID: "main", Protocol: "vless", Exit: "direct", Enabled: true}
	old := runtimeNode(n)
	n = normalizeNodePolicy(n)
	n.GroupIDs = []string{"group"}
	n.RateMilli = 1500
	if !reflect.DeepEqual(old, runtimeNode(n)) {
		t.Fatal("rate or membership polluted route identity")
	}
}

func TestEntitlementMigrationAndUsageIsolation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	alice.Upload, alice.Download, alice.Quota, alice.Expires = 123, 456, 4096, time.Now().Add(time.Hour).Unix()
	if err := a.store.save(&alice); err != nil {
		t.Fatal(err)
	}
	before := alice
	_, err := a.store.db.Exec("DELETE FROM meta WHERE key='entitlements_v1'")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.initEntitlements(); err != nil {
		t.Fatal(err)
	}
	alice, _ = a.store.record(alice.ID)
	if alice.QuotaUsed() != 579 || alice.Quota != before.Quota || alice.Expires != before.Expires || !reflect.DeepEqual(alice.Credentials, before.Credentials) || !reflect.DeepEqual(alice.Password, before.Password) {
		t.Fatal("migration changed legacy usage or identity")
	}
	meter := *alice.Meter
	if err = a.store.initEntitlements(); err != nil {
		t.Fatal(err)
	}
	alice, _ = a.store.record(alice.ID)
	if !reflect.DeepEqual(meter, *alice.Meter) {
		t.Fatal("migration replay changed meter")
	}
	if w := req(t, a, bob, "GET", "/api/usage?user_id="+businessUsageKey(alice.ID), nil); w.Code != 403 {
		t.Fatal("usage crossed account boundary")
	}
	for _, actor := range []Record{alice, owner} {
		if w := req(t, a, actor, "GET", "/api/usage?user_id="+businessUsageKey(alice.ID), nil); w.Code != 200 {
			t.Fatal("authorized usage unavailable")
		}
	}
}

func TestRejectedEntitlementEditPreservesSessionsAndNewUserMeter(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	p := createTestPlan(t, a, owner)
	input := userInput{Username: "new-member", Password: randomToken(18), Enabled: true, VLESS: true, HY2: true}
	w := req(t, a, owner, "POST", "/api/users", input)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var u User
	json.Unmarshal(w.Body.Bytes(), &u)
	if u.Meter == nil || u.Meter.Start != u.Created || u.Meter.PeriodID == "" {
		t.Fatal("new user has no stable quota period")
	}
	member, _ := a.store.record(u.ID)
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "assign", PlanID: p.ID})
	member, _ = a.store.record(u.ID)
	req(t, a, member, "GET", "/api/usage", nil)
	var before, after int
	a.store.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id=?", u.ID).Scan(&before)
	input.Quota = member.Quota + 1
	input.Expires = member.Expires
	w = req(t, a, owner, "PUT", "/api/users/"+businessUsageKey(u.ID), input)
	a.store.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id=?", u.ID).Scan(&after)
	current, _ := a.store.record(u.ID)
	if w.Code != 409 || before != after || !reflect.DeepEqual(current.Password, member.Password) {
		t.Fatal("rejected plan edit changed password or revoked sessions")
	}
}

func TestNodePolicyPreviewRejectsConcurrentChange(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	input := object{"ids": []string{"vless-main"}, "rate_milli": 1500, "preview": true}
	w := req(t, a, owner, "POST", "/api/nodes/policy", input)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var preview struct {
		Expected map[string]string `json:"expected"`
	}
	json.Unmarshal(w.Body.Bytes(), &preview)
	nodes, _ := a.store.nodes()
	for _, n := range nodes {
		if n.ID == "vless-main" && n.RateMilli != 1000 {
			t.Fatal("preview mutated rate")
		}
	}
	if w = req(t, a, owner, "POST", "/api/nodes/policy", object{"ids": []string{"vless-main"}, "rate_milli": 500}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	input["preview"], input["expected"] = false, preview.Expected
	if w = req(t, a, owner, "POST", "/api/nodes/policy", input); w.Code != 409 {
		t.Fatal("stale policy preview overwrote a concurrent change")
	}
}
