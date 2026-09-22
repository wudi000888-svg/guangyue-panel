package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type mountFixture struct {
	master, child                  *App
	owner, localOwner, user, local Record
	site                           BusinessSite
	tokenID                        string
}

func newMountFixture(t *testing.T) *mountFixture {
	t.Helper()
	f := &mountFixture{master: testApp(t), child: testApp(t)}
	f.master.cfg.Edition = "pro"
	f.child.cfg.RealityPublic = randomToken(32)
	f.child.cfg.ShortID = "aabbccddaabbccdd"
	f.child.cfg.VLESSHost = "child-vless.example.com"
	f.child.cfg.HY2Host = "child-hy.example.com"
	f.owner = testUser(t, f.master, "owner", "owner")
	f.user = testUser(t, f.master, "member", "user")
	f.user.InitMeter("period-master", time.Now().Unix())
	f.user.Quota = 1000
	if err := f.master.store.save(&f.user); err != nil {
		t.Fatal(err)
	}
	f.localOwner = testUser(t, f.child, "owner", "owner")
	f.local = testUser(t, f.child, "member", "user")
	server := httptest.NewServer(f.child.routes())
	t.Cleanup(server.Close)
	f.child.cfg.PublicURL = server.URL
	id, token, _ := fleetTokenWithOptions(t, f.child, f.localOwner, "manage", true)
	f.tokenID = id
	w := req(t, f.master, f.owner, "POST", "/api/business-sites/import", object{"token": token, "url": server.URL, "name": "Tokyo"})
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &f.site) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	w = req(t, f.master, f.owner, "GET", "/api/business-sites/"+f.site.ID+"/node-pool", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &f.site) != nil {
		t.Fatal("catalog", w.Code, w.Body.String())
	}
	return f
}
func (f *mountFixture) policy(t *testing.T, nodes []MountedNode, grants []BusinessGrant) {
	t.Helper()
	// Manual mount tests must opt the synthetic user into the selected master
	// groups explicitly. The production default remains the legacy local groups;
	// mounted nodes are now forbidden from using that local group.
	if len(grants) > 0 {
		ids := []string{}
		seen := map[string]bool{}
		for _, n := range nodes {
			for _, id := range n.GroupIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		f.user.Entitlement = nil
		f.user.NodeGroupIDs = &ids
		if err := f.master.store.save(&f.user); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := f.master.store.businessSite(f.site.ID)
	w := req(t, f.master, f.owner, "PUT", "/api/business-sites/"+s.ID+"/node-pool", object{"revision": s.Revision, "catalog_revision": s.Mount.Catalog.Revision, "nodes": nodes, "grants": grants})
	if w.Code != 200 {
		t.Fatal("mount policy", w.Code, w.Body.String())
	}
	if json.Unmarshal(w.Body.Bytes(), &f.site) != nil || f.site.Mount.Error != "" {
		t.Fatal("mount sync", w.Body.String())
	}
}
func (f *mountFixture) sync(t *testing.T) {
	t.Helper()
	if err := f.master.syncMountSite(context.Background(), f.site.ID); err != nil {
		t.Fatal(err)
	}
}
func (f *mountFixture) delegated(t *testing.T) Record {
	t.Helper()
	users, _ := f.child.store.records()
	for _, u := range users {
		if u.Mount != nil && u.Mount.UserID == f.user.ID {
			return u
		}
	}
	t.Fatal("missing delegated identity")
	return Record{}
}
func TestMountedNodeIsolationAndSubscriptions(t *testing.T) {
	f := newMountFixture(t)
	before, _ := f.child.store.nodes()
	if len(f.site.Mount.Nodes) != 0 {
		t.Fatal("auto mounted")
	}
	mounts := []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}
	f.policy(t, mounts, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	shadow := f.delegated(t)
	if shadow.ID == f.local.ID || shadow.Credentials.HY2 == f.local.Credentials.HY2 || shadow.Credentials.VLESS["vless-main"] == f.user.Credentials.VLESS["vless-main"] {
		t.Fatal("identity collision")
	}
	after, _ := f.child.store.nodes()
	local, _ := f.child.store.record(f.local.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(f.local, local) {
		t.Fatal("modified child local data")
	}
	for _, u := range mustCoreRecords(t, f.child) {
		if u.ID == shadow.ID {
			if !memberMayUseNode(u, before[1]) && before[1].Protocol == "vless" {
				t.Fatal("missing permission")
			}
			for _, n := range before {
				if n.Protocol == "hy2" && memberMayUseNode(u, n) {
					t.Fatal("default group bypass")
				}
			}
		}
	}
	entries, err := f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if err != nil || len(entries) != 1 {
		t.Fatal("missing mounted entry", err, len(entries))
	}
	entry := entries[0]
	link, _ := url.Parse(entry.uri)
	if link.Hostname() != f.child.cfg.VLESSHost || link.User.Username() != shadow.Credentials.VLESS["vless-main"] || !strings.HasPrefix(entry.name, "[挂载·Tokyo]") || !entry.mounted {
		t.Fatal("not a direct child subscription")
	}
	for _, format := range []string{"raw", "base64", "mihomo"} {
		b, _, e := renderSubscriptionEntries(f.master.cfg, nil, entries, format, false)
		if e != nil {
			t.Fatal(e)
		}
		if format == "base64" {
			b, e = base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
			if e != nil {
				t.Fatal(e)
			}
		}
		if !strings.Contains(string(b), f.child.cfg.VLESSHost) {
			t.Fatal("missing child endpoint", format)
		}
	}
	for _, source := range []string{"private", "public", "local"} {
		es, e := f.master.subscriptionCatalogSource(f.user, source, "")
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range es {
			if v.mounted {
				t.Fatal("legacy scope expanded", source)
			}
		}
	}
	for _, u := range mustCoreRecords(t, f.master) {
		if u.ID == f.user.ID && u.Quota != 400 {
			t.Fatal("master failed to reserve budget", u.Quota)
		}
	}
	w := req(t, f.master, f.owner, "GET", "/api/business-sites", nil)
	if strings.Contains(w.Body.String(), shadow.Credentials.HY2) || strings.Contains(w.Body.String(), shadow.Credentials.VLESS["vless-main"]) {
		t.Fatal("credentials exposed in directory")
	}
	// Removing group permission blocks cached subscriptions immediately.
	f.user.Entitlement = &domain.Entitlement{GroupIDs: []string{}} // no implicit fallback
	f.master.store.save(&f.user)
	entries, _ = f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if len(entries) != 0 {
		t.Fatal("cached group bypass")
	}
	f.sync(t)
	shadow = f.delegated(t)
	if len(shadow.Mount.NodeIDs) != 0 {
		t.Fatal("remote group access retained")
	}
}

func TestSubsiteEgressPoolUsesDedicatedRelayPath(t *testing.T) {
	f := newMountFixture(t)
	w := req(t, f.master, f.owner, "POST", "/api/business-sites/"+f.site.ID+"/egress-pool", object{"node_id": "vless-main"})
	if w.Code != 201 {
		t.Fatalf("sub-site egress: %d %s", w.Code, w.Body.String())
	}
	var pool IPResource
	if err := json.Unmarshal(w.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	if pool.PoolGroup != "subsite" || pool.Source != f.site.ID || pool.Exit != "subscription" {
		t.Fatalf("invalid sub-site relay resource: %+v", pool)
	}
	stored, err := f.master.store.pool(pool.ID)
	if err != nil || stored.Upstream == nil || stored.UpstreamType == "" {
		t.Fatalf("relay credentials were not stored privately: %v", err)
	}
	if !strings.Contains(pool.Notes, "主站中转，子站 IP 出口") {
		t.Fatalf("relay resource is not labelled as a sub-site exit: %q", pool.Notes)
	}
	w = req(t, f.master, f.owner, "POST", "/api/business-sites/"+f.site.ID+"/egress-pool", object{"node_id": "hy2-main"})
	if w.Code != 201 {
		t.Fatalf("second protocol relay: %d %s", w.Code, w.Body.String())
	}
	pools, err := f.master.store.pools()
	if err != nil || len(pools) != 2 {
		t.Fatalf("sub-site relay resources were not persisted: %d %v", len(pools), err)
	}
}
func TestMountedAccountingRevocationAndPeriods(t *testing.T) {
	f := newMountFixture(t)
	ns, _ := f.child.store.nodes()
	n := ns[0]
	for _, v := range ns {
		if v.ID == "vless-main" {
			n = v
		}
	}
	n = normalizeNodePolicy(n)
	n.RateMilli = 1500
	n.RateRevision = "rate-one"
	f.child.store.saveNode(n)
	// Refresh metadata after changing the authoritative child rate.
	w := req(t, f.master, f.owner, "GET", "/api/business-sites/"+f.site.ID+"/node-pool", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	mounts := []MountedNode{{NodeID: n.ID, Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}
	f.policy(t, mounts, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	shadow := f.delegated(t)
	account := func(value int64) {
		t.Helper()
		if e := f.child.store.account([]Counter{{Key: "mounted", Generation: "one", UserID: shadow.ID, NodeID: n.ID, Protocol: "vless", Direction: "up", Value: value}}); e != nil {
			t.Fatal(e)
		}
	}
	account(2)
	f.sync(t)
	f.sync(t)
	u, _ := f.master.store.record(f.user.ID)
	if u.Upload != 2 || u.QuotaUsed() != 3 {
		t.Fatal("meter replay or rate error", u.Upload, u.QuotaUsed())
	}
	n.RateMilli = 500
	n.RateRevision = "rate-two"
	f.child.store.saveNode(n)
	account(4)
	f.sync(t)
	u, _ = f.master.store.record(u.ID)
	if u.Upload != 4 || u.QuotaUsed() != 4 {
		t.Fatal("rate history reweighted")
	}
	applyTestEntitlement(t, f.master, f.owner, entitlementRequest{IDs: []int64{u.ID}, Action: "reset"})
	if e := f.master.advanceQuotaPeriods(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	f.sync(t)
	if e := f.master.advanceQuotaPeriods(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	f.sync(t)
	account(6)
	f.sync(t)
	u, _ = f.master.store.record(u.ID)
	if u.Upload != 6 || u.QuotaUsed() != 1 {
		t.Fatal("period settlement failed", u.Upload, u.QuotaUsed())
	}
	f.policy(t, nil, nil)
	shadow = f.delegated(t)
	if shadow.Active() || len(shadow.Mount.NodeIDs) != 0 {
		t.Fatal("unmount not revoked")
	}
	s, _ := f.master.store.businessSite(f.site.ID)
	if siteReservation(s, u.ID) != 0 || len(s.Issued) != 0 {
		t.Fatal("settled reservation retained")
	}
	local, _ := f.child.store.record(f.local.ID)
	if !local.Active() {
		t.Fatal("local autonomy lost")
	}
}

func TestMountedMonthlyBudgetStopsNewAuthorizations(t *testing.T) {
	f := newMountFixture(t)
	groupIDs := []string{defaultSubsiteGroup}
	f.user.NodeGroupIDs = &groupIDs
	if err := f.master.store.save(&f.user); err != nil {
		t.Fatal(err)
	}
	s, _ := f.master.store.businessSite(f.site.ID)
	w := req(t, f.master, f.owner, "PUT", "/api/business-sites/"+s.ID+"/node-pool", object{
		"revision": s.Revision, "catalog_revision": s.Mount.Catalog.Revision,
		"monthly_budget": 1, "nodes": []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}},
		"assignment": "manual", "grants": []BusinessGrant{{UserID: f.user.ID, Budget: 600}},
	})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	shadow := f.delegated(t)
	if err := f.child.store.account([]Counter{{Key: "budget", Generation: "one", UserID: shadow.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 2}}); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	f.sync(t)
	s, _ = f.master.store.businessSite(f.site.ID)
	if s.MonthlyUsage != 2 {
		t.Fatalf("monthly usage was not accumulated: %d", s.MonthlyUsage)
	}
	shadow = f.delegated(t)
	if len(shadow.Mount.NodeIDs) != 0 {
		t.Fatal("monthly budget did not revoke new authorization after exhaustion")
	}
	entries, err := f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("exhausted monthly budget still exposed mounted subscription")
	}
	if s.MonthlyBudget != 1 || s.MonthlyBudgetPeriod == "" {
		t.Fatal("monthly budget setting was not persisted")
	}
	if len(s.Mount.Nodes) != 1 || len(s.Mount.Nodes[0].GroupIDs) != 1 || s.Mount.Nodes[0].GroupIDs[0] != defaultSubsiteGroup {
		t.Fatal("budget exhaustion changed mounted node-group policy")
	}
	// Cooldown expiry resets the shared counter and makes the existing mount
	// eligible again. The policy itself is deliberately retained.
	s.BudgetCooldownUntil = time.Now().Unix() - 1
	if err := f.master.store.saveBusinessSite(s); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	entries, err = f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if err != nil || len(entries) != 1 {
		t.Fatal("cooldown did not restore mounted subscription", err, len(entries))
	}
	if len(s.Mount.Nodes) != 1 || s.Mount.Nodes[0].GroupIDs[0] != defaultSubsiteGroup {
		t.Fatal("cooldown changed mounted node-group policy")
	}
}

func TestMountedTokenSharingAndLease(t *testing.T) {
	f := newMountFixture(t)
	mounts := []MountedNode{{NodeID: "hy2-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}
	f.policy(t, mounts, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	shadow := f.delegated(t)
	shadow.Mount.LeaseUntil = time.Now().Unix() - 1
	f.child.store.save(&shadow)
	for _, u := range mustCoreRecords(t, f.child) {
		if u.ID == shadow.ID && u.Active() {
			t.Fatal("expired lease active")
		}
		if u.ID == f.local.ID && !u.Active() {
			t.Fatal("local expired")
		}
	}
	f.sync(t)
	share, _ := f.child.store.mountSharing(f.tokenID)
	share.Enabled = false
	w := req(t, f.child, f.localOwner, "PUT", "/api/fleet/tokens/"+f.tokenID+"/sharing", share)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, u := range mustCoreRecords(t, f.child) {
		if u.ID == shadow.ID && u.Active() {
			t.Fatal("sharing disabled but active")
		}
	}
	f.sync(t)
	entries, _ := f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if len(entries) != 0 {
		t.Fatal("disabled sharing distributed")
	}
	share, _ = f.child.store.mountSharing(f.tokenID)
	share.Enabled = true
	req(t, f.child, f.localOwner, "PUT", "/api/fleet/tokens/"+f.tokenID+"/sharing", share)
	f.sync(t)
	w = req(t, f.child, f.localOwner, "DELETE", "/api/fleet/tokens/"+f.tokenID, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, u := range mustCoreRecords(t, f.child) {
		if u.ID == shadow.ID && u.Active() {
			t.Fatal("revoked token active")
		}
	}
	if err := f.master.syncMountSite(context.Background(), f.site.ID); err == nil {
		t.Fatal("revoked token accepted")
	}
}

func TestMountedGuardPreservesLocalCoreUsers(t *testing.T) {
	now := time.Now().Unix()
	config := []byte(`{"inbounds":[{"settings":{"clients":[{"email":"u1.local@personal","id":"local"},{"email":"u2.remote@personal","id":"expired"},{"email":"u3.remote@personal","id":"valid"}]}},{"settings":{"address":"127.0.0.1"}}]}`)
	b, err := filterExpiredMountClients(config, mountLeases{Version: 1, Users: map[string]int64{"2": now - 1, "3": now + 300}}, now)
	if err != nil || strings.Contains(string(b), "expired") || !strings.Contains(string(b), "local") || !strings.Contains(string(b), "valid") {
		t.Fatal("guard removed local user or retained expired mount", err)
	}
	dir := t.TempDir()
	if err = writeJSON(filepath.Join(dir, "mount-leases.json"), mountLeases{Version: 1, Users: map[string]int64{"2": now - 10}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go guardMountLeases(ctx, dir, map[string]bool{"2": true}, mountLeases{Users: map[string]int64{"2": now + 300}}, cancel)
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stopped panel can leave mounted core access alive")
	}
}
func TestMountedGuardSeesGrantWithdrawnBetweenPolls(t *testing.T) {
	dir := t.TempDir()
	initial := mountLeases{Version: 1, Users: map[string]int64{"2": 0}, Revisions: map[string]int64{"2": 1}}
	// A core starts without this identity, then admits grant 2 via its API.
	// By the guard's first poll, withdrawal has already restored deadline 0.
	withdrawn := mountLeases{Version: 1, Users: map[string]int64{"2": 0}, Revisions: map[string]int64{"2": 2}}
	if err := writeJSON(filepath.Join(dir, "mount-leases.json"), withdrawn); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go guardMountLeases(ctx, dir, map[string]bool{}, initial, cancel)
	select {
	case <-ctx.Done():
	case <-time.After(9 * time.Second):
		t.Fatal("missed an authorization withdrawn between lease polls")
	}
}
func TestMountedPendingRemovalAndLocalBlock(t *testing.T) {
	f := newMountFixture(t)
	f.policy(t, []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	shadow := f.delegated(t)
	w := req(t, f.child, f.localOwner, "DELETE", "/api/users/"+businessUsageKey(shadow.ID), nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.sync(t)
	after := f.delegated(t)
	if after.ID != shadow.ID || after.Enabled || after.Active() {
		t.Fatal("remote recreated locally blocked identity")
	}
	before, _ := f.master.store.businessSite(f.site.ID)
	w = req(t, f.master, f.owner, "DELETE", "/api/business-sites/"+f.site.ID, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	site, e := f.master.store.businessSite(f.site.ID)
	if e != nil || !site.Removed || len(site.Grants) > 0 || len(site.Mount.Accounts) > 0 || !reflect.DeepEqual(site.Issued, before.Issued) {
		t.Fatal("removal discarded reservations", e)
	}
	f.sync(t)
	site, _ = f.master.store.businessSite(f.site.ID)
	if len(site.Issued) != 0 {
		t.Fatal("final ack failed to settle")
	}
}
func TestUnifiedSubscriptionNeverExpandsPublicToken(t *testing.T) {
	f := newMountFixture(t)
	f.policy(t, []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	path := "/public-sub/" + businessUsageKey(f.user.ID) + "/" + f.user.Credentials.PublicToken + "?source=all&format=mihomo"
	w := req(t, f.master, Record{}, "GET", path, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "child-vless.example.com") {
		t.Fatal("public token expanded scope", w.Code)
	}
	w = req(t, f.master, f.owner, "GET", "/api/subscription?pool=all&user_id="+businessUsageKey(f.user.ID), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "?source=all") || !strings.Contains(w.Body.String(), "child-vless.example.com") {
		t.Fatal("unified subscription missing mount", w.Body.String())
	}
}

func TestMountedBindingRetryAndAtomicFailure(t *testing.T) {
	f := newMountFixture(t)
	c, e := f.child.childMountCatalog(f.tokenID)
	if e != nil {
		t.Fatal(e)
	}
	in := MountSync{MasterID: uuid(), Sequence: 1, CatalogRevision: c.Revision, Users: []MountUser{{UserID: 99, NodeIDs: []string{"vless-main"}, Generation: digest("fixture"), PeriodID: "fixture-period", Quota: 1000, VLESS: true}}}
	if _, e = f.child.store.db.Exec("CREATE TRIGGER mount_failure BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT,'fixture'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.child.applyMountSync(f.tokenID, in, c); e == nil {
		t.Fatal("injected failure accepted")
	}
	if raw, _ := f.child.store.readMeta("mount-binding:" + f.tokenID); raw != "" {
		t.Fatal("binding committed before users")
	}
	f.child.store.db.Exec("DROP TRIGGER mount_failure")
	first, e := f.child.applyMountSync(f.tokenID, in, c)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.child.applyMountSync(f.tokenID, in, c)
	if e != nil || first.LeaseUntil != again.LeaseUntil || !reflect.DeepEqual(first.Accounts, again.Accounts) {
		t.Fatal("retry renewed lease or rotated credentials", e)
	}
	in.MasterID = uuid()
	in.Sequence++
	if _, e = f.child.applyMountSync(f.tokenID, in, c); e == nil {
		t.Fatal("second master hijacked token")
	}
}
func TestMountedTokenReplacementRetainsLedger(t *testing.T) {
	f := newMountFixture(t)
	f.policy(t, []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	_, token, _ := fleetTokenWithOptions(t, f.child, f.localOwner, "manage", true)
	old, _ := f.master.store.businessSite(f.site.ID)
	w := req(t, f.master, f.owner, "POST", "/api/business-sites/import", object{"token": token, "url": old.Connection.URL})
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var replacement BusinessSite
	json.Unmarshal(w.Body.Bytes(), &replacement)
	if replacement.ID == old.ID || replacement.Mount != nil || len(replacement.Grants) > 0 {
		t.Fatal("new token inherited old credentials")
	}
	retired, e := f.master.store.businessSite(old.ID)
	if e != nil || !retired.Removed || siteReservation(retired, f.user.ID) != 600 {
		t.Fatal("replacement lost unsettled ledger", e)
	}
}

func TestMountedRejectsRestoredCountersAndPreservesEditedBudget(t *testing.T) {
	f := newMountFixture(t)
	nodes := []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}
	f.policy(t, nodes, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	shadow := f.delegated(t)
	if err := f.child.store.account([]Counter{{Key: "restore-test", Generation: "one", UserID: shadow.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 20}}); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	// The UI submits the unchanged absolute limit when only node aliases change,
	// even if usage has increased since it first displayed the remaining budget.
	f.policy(t, nodes, []BusinessGrant{{UserID: f.user.ID, Budget: 600, Quota: 600}})
	s, _ := f.master.store.businessSite(f.site.ID)
	if s.Grants[0].Quota != 600 || s.Grants[0].Budget != 600 {
		t.Fatal("editing topped up quota")
	}
	if err := f.child.store.save(&shadow); err != nil {
		t.Fatal(err)
	} // restore pre-traffic fixture
	if err := f.master.syncMountSite(context.Background(), f.site.ID); err == nil {
		t.Fatal("restored child counters allowed fresh access")
	}
}
