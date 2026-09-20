package controlplane

import (
	"encoding/json"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"reflect"
	"testing"
)

func TestSubsiteGroupInstallUpgradeAndCustomization(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "member", "user")
	groups, err := a.store.nodeGroups()
	if err != nil {
		t.Fatal(err)
	}
	var seeded NodeGroup
	for _, g := range groups {
		if g.ID == defaultSubsiteGroup {
			seeded = g
		}
	}
	if seeded.Name != "默认子站节点组" || !seeded.Enabled {
		t.Fatal("missing installation default")
	}
	seeded.Scope = "private"
	wrongScope, _ := json.Marshal(seeded)
	if _, err = a.store.db.Exec("UPDATE node_groups SET doc=? WHERE id=?", wrongScope, seeded.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.store.initEntitlements(); err != nil {
		t.Fatal(err)
	}
	groups, _ = a.store.nodeGroups()
	for _, g := range groups {
		if g.ID == defaultSubsiteGroup && g.Scope != "subsite" {
			t.Fatal("reserved child group scope was not repaired")
		}
	}
	seeded.Scope = "subsite"
	records := []Record{u}
	if err = a.store.resolveAccess(records); err != nil {
		t.Fatal(err)
	}
	if nodeGroupAllowed(records[0], Node{PolicyVersion: 1, GroupIDs: []string{defaultSubsiteGroup}}) {
		t.Fatal("default widened legacy user's permissions")
	}
	// Recreate the pre-upgrade state, with the original entitlement migration done.
	if _, err = a.store.db.Exec("DELETE FROM node_groups WHERE id=?", defaultSubsiteGroup); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("DELETE FROM meta WHERE key='subsite_group_v1'"); err != nil {
		t.Fatal(err)
	}
	if err = a.store.initEntitlements(); err != nil {
		t.Fatal(err)
	}
	groups, _ = a.store.nodeGroups()
	if len(groups) != 3 {
		t.Fatal("upgrade did not seed group", len(groups))
	}
	seeded.Name = "自定义组"
	seeded.Enabled = false
	b, _ := json.Marshal(seeded)
	if _, err = a.store.db.Exec("UPDATE node_groups SET doc=? WHERE id=?", b, seeded.ID); err != nil {
		t.Fatal(err)
	}
	// Even a partially applied migration must not overwrite an existing group.
	a.store.db.Exec("DELETE FROM meta WHERE key='subsite_group_v1'")
	if err = a.store.initEntitlements(); err != nil {
		t.Fatal(err)
	}
	groups, _ = a.store.nodeGroups()
	for _, g := range groups {
		if g.ID == seeded.ID && !reflect.DeepEqual(g, seeded) {
			t.Fatal("customization overwritten")
		}
	}
	a.store.db.Exec("DELETE FROM node_groups WHERE id=?", defaultSubsiteGroup)
	if err = a.store.initEntitlements(); err != nil {
		t.Fatal(err)
	}
	groups, _ = a.store.nodeGroups()
	for _, g := range groups {
		if g.ID == seeded.ID {
			t.Fatal("deleted group recreated")
		}
	}
	after, _ := a.store.record(u.ID)
	if !reflect.DeepEqual(u, after) {
		t.Fatal("migration modified user")
	}
}

func autoMountPolicy(t *testing.T, f *mountFixture, groups []string) {
	t.Helper()
	s, _ := f.master.store.businessSite(f.site.ID)
	nodes := []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: groups}}
	w := req(t, f.master, f.owner, "PUT", "/api/business-sites/"+s.ID+"/node-pool", object{"revision": s.Revision, "catalog_revision": s.Mount.Catalog.Revision, "nodes": nodes, "assignment": "groups"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &f.site); err != nil {
		t.Fatal(err)
	}
}
func TestAutomaticMountFollowsMainGroupsAndRevokes(t *testing.T) {
	f := newMountFixture(t)
	if f.site.Mount.Assignment != "groups" {
		t.Fatal("new mount is not automatic")
	}
	autoMountPolicy(t, f, []string{defaultSubsiteGroup})
	if len(f.site.Grants) != 0 {
		t.Fatal("empty default group authorized users")
	}
	f.user.Entitlement = &domain.Entitlement{GroupIDs: []string{defaultSubsiteGroup}}
	if err := f.master.store.save(&f.user); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	shadow := f.delegated(t)
	if len(shadow.Mount.NodeIDs) != 1 || shadow.Quota != 1000 {
		t.Fatal("group did not authorize child", shadow.Quota)
	}
	entries, err := f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if err != nil || len(entries) != 1 {
		t.Fatal("direct child subscription missing", err)
	}
	if err = f.child.store.account([]Counter{{Key: "auto", Generation: "one", UserID: shadow.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 100}}); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	f.sync(t)
	u, _ := f.master.store.record(f.user.ID)
	if u.QuotaUsed() != 100 {
		t.Fatal("main database accounting", u.QuotaUsed())
	}
	u.Entitlement.GroupIDs = nil
	if err = f.master.store.save(&u); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	shadow = f.delegated(t)
	s, _ := f.master.store.businessSite(f.site.ID)
	if len(shadow.Mount.NodeIDs) != 0 || len(s.Grants) != 0 || siteReservation(s, u.ID) != 0 {
		t.Fatal("removed permission retained authorization or reservation")
	}
	local, _ := f.child.store.record(f.local.ID)
	if !reflect.DeepEqual(local, f.local) {
		t.Fatal("child local account changed")
	}
}

func TestAutomaticMountBudgetsRespectOtherSitesAndOfflineReservations(t *testing.T) {
	f := newMountFixture(t)
	autoMountPolicy(t, f, []string{legacyPrivateGroup})
	s, _ := f.master.store.businessSite(f.site.ID)
	if siteReservation(s, f.user.ID) != 500 {
		t.Fatal("local quota not retained")
	}
	other := s
	other.ID = "other"
	m := *s.Mount
	other.Mount = &m
	other.Grants = nil
	other.Issued = map[int64]int64{}
	other.IssuedUnlimited = map[int64]bool{}
	blob, err := f.master.store.vault.seal(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.master.store.db.Exec("INSERT INTO business_sites(id,doc) VALUES(?,?)", other.ID, blob); err != nil {
		t.Fatal(err)
	}
	if err := f.master.store.saveBusinessSite(other); err != nil {
		t.Fatal(err)
	}
	if err := f.master.refreshMountGrants(&other); err != nil {
		t.Fatal(err)
	}
	if siteReservation(other, f.user.ID) != 333 {
		t.Fatal("second site's fair reservation", siteReservation(other, f.user.ID))
	}
	if err := f.master.validateBusinessAllocations(other); err != nil {
		t.Fatal(err)
	}
	// A removed/offline site keeps its unacknowledged grant. It cannot be reused.
	other.Removed = true
	other.Grants = nil
	other.Issued = map[int64]int64{f.user.ID: 700}
	if err := f.master.store.saveBusinessSite(other); err != nil {
		t.Fatal(err)
	}
	// Model a settled reduction on the current site before that external grant.
	s.Issued = map[int64]int64{}
	s.Grants = nil
	if err := f.master.refreshMountGrants(&s); err != nil {
		t.Fatal(err)
	}
	if siteReservation(s, f.user.ID) != 300 {
		t.Fatal("offline allocation reused", siteReservation(s, f.user.ID))
	}
	if err := f.master.validateBusinessAllocations(s); err != nil {
		t.Fatal(err)
	}
	other.Issued[f.user.ID] = 1000
	f.master.store.saveBusinessSite(other)
	if err := f.master.refreshMountGrants(&s); err != nil {
		t.Fatal(err)
	}
	for _, g := range s.Grants {
		if g.UserID == f.user.ID {
			t.Fatal("exhausted budget became unlimited")
		}
	}
}
func TestAutomaticMountManualCompatibilityAndInactiveUsers(t *testing.T) {
	f := newMountFixture(t)
	f.policy(t, []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{legacyPrivateGroup}}}, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	s, _ := f.master.store.businessSite(f.site.ID)
	before := append([]BusinessGrant{}, s.Grants...)
	if err := f.master.refreshMountGrants(&s); err != nil || !reflect.DeepEqual(before, s.Grants) {
		t.Fatal("legacy manual policy changed", err)
	}
	autoMountPolicy(t, f, []string{legacyPrivateGroup})
	f.user.VLESS = false
	f.master.store.save(&f.user)
	f.sync(t)
	s, _ = f.master.store.businessSite(f.site.ID)
	for _, g := range s.Grants {
		if g.UserID == f.user.ID {
			t.Fatal("protocol-disabled user authorized")
		}
	}
	f.user.VLESS = true
	f.user.Quota = 0
	f.master.store.save(&f.user)
	f.sync(t)
	s, _ = f.master.store.businessSite(f.site.ID)
	found := false
	for _, g := range s.Grants {
		if g.UserID == f.user.ID {
			found = true
			if g.Quota != 0 {
				t.Fatal("unlimited user restricted")
			}
		}
	}
	if !found {
		t.Fatal("unlimited user missing")
	}
	f.user.Enabled = false
	f.master.store.save(&f.user)
	f.sync(t)
	if shadow := f.delegated(t); len(shadow.Mount.NodeIDs) != 0 {
		t.Fatal("disabled user retained access")
	}
}

// A saved mount has no second user-selection gate for subscribers, including
// when the site was configured with the pre-upgrade manual selector.
func TestPlanMountAccessWithoutUserAssignment(t *testing.T) {
	for _, assignment := range []string{"groups", "manual"} {
		t.Run(assignment, func(t *testing.T) {
			f := newMountFixture(t)
			plan := createTestPlan(t, f.master, f.owner)
			plan.GroupIDs = []string{defaultSubsiteGroup}
			w := req(t, f.master, f.owner, "POST", "/api/plans", plan)
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil {
				t.Fatal("save plan", w.Code)
			}
			applyTestEntitlement(t, f.master, f.owner, entitlementRequest{IDs: []int64{f.user.ID}, Action: "assign", PlanID: plan.ID})
			user, _ := f.master.store.record(f.user.ID)
			empty := []string{}
			user.NodeGroupIDs = &empty // A pre-upgrade denial must not suppress plan access.
			if err := f.master.store.save(&user); err != nil {
				t.Fatal(err)
			}
			before := user
			nodes := []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}, {NodeID: "hy2-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}
			w = req(t, f.master, f.owner, "PUT", "/api/business-sites/"+f.site.ID+"/node-pool", object{"revision": f.site.Revision, "catalog_revision": f.site.Mount.Catalog.Revision, "assignment": assignment, "nodes": nodes})
			if w.Code != 200 {
				t.Fatal("save without user selection", w.Code, w.Body.String())
			}
			shadow := f.delegated(t)
			if len(shadow.Mount.NodeIDs) != 2 || shadow.Quota != plan.Quota {
				t.Fatal("plan did not authorize both child protocols")
			}
			entries, err := f.master.subscriptionCatalogSource(user, "mixed", "")
			if err != nil || len(entries) != 2 {
				t.Fatal("mixed subscription missing child nodes", err, len(entries))
			}
			for _, entry := range entries {
				if !entry.mounted {
					t.Fatal("child-only plan acquired local nodes")
				}
			}
			stored, _ := f.master.store.record(user.ID)
			if !reflect.DeepEqual(before, stored) {
				t.Fatal("mount changed account terms or credentials")
			}
			if err := f.child.store.account([]Counter{{Key: "plan-auto", Generation: "one", UserID: shadow.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 100}}); err != nil {
				t.Fatal(err)
			}
			f.sync(t)
			f.sync(t)
			stored, _ = f.master.store.record(user.ID)
			if stored.QuotaUsed() != 100 {
				t.Fatal("plan mount usage not settled")
			}
			local := createTestPlan(t, f.master, f.owner)
			applyTestEntitlement(t, f.master, f.owner, entitlementRequest{IDs: []int64{user.ID}, Action: "assign", PlanID: local.ID})
			stored, _ = f.master.store.record(user.ID)
			override := []string{defaultSubsiteGroup}
			stored.NodeGroupIDs = &override // Nor may an old grant widen a local-only plan.
			f.master.store.save(&stored)
			entries, err = f.master.subscriptionCatalogSource(stored, "mixed", "")
			if err != nil {
				t.Fatal("local plan subscription", err)
			}
			for _, entry := range entries {
				if entry.mounted {
					t.Fatal("cached mounted subscription survived plan change")
				}
			}
			f.sync(t)
			if len(f.delegated(t).Mount.NodeIDs) != 0 {
				t.Fatal("child retained revoked plan access")
			}
			site, _ := f.master.store.businessSite(f.site.ID)
			if len(site.Grants) != 0 || siteReservation(site, user.ID) != 0 {
				t.Fatal("revoked plan retained grants or settled reservation")
			}
			stored, _ = f.master.store.record(user.ID)
			entries, err = f.master.subscriptionCatalogSource(stored, "mixed", "")
			if err != nil || len(entries) != 2 {
				t.Fatal("local access not restored after child settlement", err, len(entries))
			}
			if stored.QuotaUsed() != 100 || stored.Meter.PeriodID != before.Meter.PeriodID || !reflect.DeepEqual(stored.Credentials, before.Credentials) {
				t.Fatal("plan change reset usage or credentials")
			}
			childLocal, _ := f.child.store.record(f.local.ID)
			if !reflect.DeepEqual(childLocal, f.local) {
				t.Fatal("changed child independent account")
			}
		})
	}
}
