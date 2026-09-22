package controlplane

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func TestFixedGroupsUpgradeRemovesCustomGroupsWithoutWideningAccess(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "member", "user")
	u.Entitlement = &domain.Entitlement{GroupIDs: []string{"custom"}, NodeIDs: []string{"vless-main"}}
	u.PlanQueue = []domain.PlanSlot{{Entitlement: &domain.Entitlement{GroupIDs: []string{"custom", legacyPublicGroup}}}}
	if err := a.store.save(&u); err != nil {
		t.Fatal(err)
	}
	g := NodeGroup{ID: "custom", Name: "Old", Scope: "private", Enabled: true}
	if _, err := a.store.db.Exec("INSERT INTO node_groups(id,doc) VALUES(?,?)", g.ID, jsonBytes(g)); err != nil {
		t.Fatal(err)
	}
	p := Plan{ID: "old-plan", Name: "Old", GroupIDs: []string{g.ID}}
	if _, err := a.store.db.Exec("INSERT INTO plans(id,doc) VALUES(?,?)", p.ID, jsonBytes(p)); err != nil {
		t.Fatal(err)
	}
	for _, offer := range []Offer{{ID: "old-offer", Plan: p, Enabled: true}, {ID: "default-offer", Plan: Plan{ID: "default-plan", GroupIDs: []string{defaultSubsiteGroup, legacyPrivateGroup}}, Enabled: true}} {
		if _, err := a.store.db.Exec("INSERT INTO commerce_offers(id,version,enabled,doc) VALUES(?,0,1,?)", offer.ID, jsonBytes(offer)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.db.Exec("DELETE FROM meta WHERE key='fixed_node_groups_v1'"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.initFixedNodeGroups(); err != nil {
		t.Fatal(err)
	}
	groups, err := a.store.nodeGroups()
	if err != nil || len(groups) != 3 {
		t.Fatalf("fixed groups: %+v %v", groups, err)
	}
	for _, group := range groups {
		if !group.Enabled || len(fixedGroupIDs([]string{group.ID})) != 1 {
			t.Fatalf("unexpected group: %+v", group)
		}
	}
	p, _ = a.store.plan(p.ID)
	u, _ = a.store.record(u.ID)
	if len(p.GroupIDs) != 0 || len(u.Entitlement.GroupIDs) != 0 || len(u.Entitlement.NodeIDs) != 0 || !reflect.DeepEqual(u.PlanQueue[0].Entitlement.GroupIDs, []string{legacyPublicGroup}) {
		t.Fatal("retired custom access was widened or remained active")
	}
	offers, _ := readDocuments[Offer](a.store, "commerce_offers")
	for _, o := range offers {
		if o.Enabled != (o.ID == "default-offer") {
			t.Fatalf("incorrect offer state: %+v", o)
		}
	}
	if err := a.store.initFixedNodeGroups(); err != nil {
		t.Fatal(err)
	}
	again, _ := a.store.nodeGroups()
	if !reflect.DeepEqual(groups, again) {
		t.Fatal("migration is not idempotent")
	}
}

func TestFixedLocalAndPublicGroupsIgnoreManualAssignments(t *testing.T) {
	a := testApp(t)
	for _, manager := range []string{"", publicManager} {
		n := Node{ID: "node", ManagedBy: manager, PolicyVersion: 1, RateMilli: 1000, GroupIDs: []string{"custom", defaultSubsiteGroup}}
		if err := a.store.validateNodePolicy(&n, nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(n.GroupIDs, []string{localNodeGroup(n)}) {
			t.Fatal("node was manually regrouped")
		}
	}
}

func TestPlanNodeSelectionIsScopedToGroupAndSite(t *testing.T) {
	u := Record{User: User{Entitlement: &domain.Entitlement{GroupIDs: []string{legacyPrivateGroup, defaultSubsiteGroup, legacyPublicGroup}, NodeIDs: []string{legacyPrivateGroup + "/vless-main", defaultSubsiteGroup + "/child-a/vless-main"}}}}
	for _, tc := range []struct {
		n    Node
		want bool
	}{
		{Node{ID: "vless-main"}, true},
		{Node{ID: "another-vless"}, false},
		{Node{ID: "public-node", ManagedBy: publicManager}, true},
		{mountNodePolicy("child-a", MountedNode{Enabled: true}, Node{ID: "vless-main", PolicyVersion: 1}), true},
		{mountNodePolicy("child-b", MountedNode{Enabled: true}, Node{ID: "vless-main", PolicyVersion: 1}), false},
		{mountNodePolicy("child-a", MountedNode{Enabled: true}, Node{ID: "another-vless", PolicyVersion: 1}), false},
	} {
		if got := nodeGroupAllowed(u, tc.n); got != tc.want {
			t.Fatalf("%s: got %t want %t", planNodeKey(tc.n), got, tc.want)
		}
	}
	u.Entitlement.GroupIDs = []string{}
	if nodeGroupAllowed(u, Node{ID: "vless-main"}) {
		t.Fatal("individual selection bypassed package groups")
	}
}

func TestMountedPackageSelectionReachesChildAndSubscription(t *testing.T) {
	f := newMountFixture(t)
	autoMountPolicy(t, f, []string{legacyPrivateGroup, "retired-custom"})
	s, _ := f.master.store.businessSite(f.site.ID)
	if !reflect.DeepEqual(s.Mount.Nodes[0].GroupIDs, []string{defaultSubsiteGroup}) {
		t.Fatal("mount was not automatically classified")
	}
	key := defaultSubsiteGroup + "/" + s.ID + "/vless-main"
	p := Plan{Name: "Mounted selection", Quota: 1000, ValidDays: 30, Cycle: "30d", GroupIDs: []string{defaultSubsiteGroup}, NodeIDs: []string{key}, VLESS: true}
	p = decoded[Plan](t, req(t, f.master, f.owner, "POST", "/api/plans", p), 200)
	assignPlan(&f.user, p, time.Now().Unix())
	if err := f.master.store.save(&f.user); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	shadow := f.delegated(t)
	if !reflect.DeepEqual(shadow.Mount.NodeIDs, []string{"vless-main"}) {
		t.Fatalf("wrong child nodes: %+v", shadow.Mount)
	}
	entries, err := f.master.subscriptionCatalogSource(f.user, "mounted", "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("subscription: %d %v", len(entries), err)
	}
	w := req(t, f.master, f.owner, "GET", "/api/node-groups", nil)
	var directory struct {
		Members map[string][]struct {
			Key string `json:"selection_key"`
		} `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	if len(directory.Members[defaultSubsiteGroup]) != 1 || directory.Members[defaultSubsiteGroup][0].Key != key {
		t.Fatal("package picker cannot identify the child node")
	}
}
