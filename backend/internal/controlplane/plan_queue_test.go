package controlplane

import (
	"testing"
	"time"
)

func TestPlanCanAuthorizeAnIndividualNode(t *testing.T) {
	a := testApp(t)
	owner, user := testUser(t, a, "owner", "owner"), testUser(t, a, "member", "user")
	p := Plan{Name: "Single node", Quota: 1024, ValidDays: 30, Cycle: "30d", Timezone: "UTC", NodeIDs: []string{"vless-main"}, VLESS: true}
	p = decoded[Plan](t, req(t, a, owner, "POST", "/api/plans", p), 200)
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{user.ID}, Action: "assign", PlanID: p.ID})
	user, _ = a.store.record(user.ID)
	if user.Entitlement == nil || len(user.Entitlement.NodeIDs) != 1 || user.Entitlement.NodeIDs[0] != "vless-main" {
		t.Fatalf("individual node was not copied to entitlement: %+v", user.Entitlement)
	}
	if !memberMayUseNode(user, Node{ID: "vless-main", Protocol: "vless", Enabled: true}) {
		t.Fatal("selected node is not authorized")
	}
	if memberMayUseNode(user, Node{ID: "hy2-main", Protocol: "hy2", Enabled: true}) {
		t.Fatal("unselected node leaked into authorization")
	}
}

func TestPurchasingPlanQueuesAndRestoresPreviousPlan(t *testing.T) {
	a := testApp(t)
	owner, user := testUser(t, a, "owner", "owner"), testUser(t, a, "member", "user")
	aPlan := Plan{Name: "A", Quota: 1000, ValidDays: 2, Cycle: "30d", Timezone: "UTC", GroupIDs: []string{legacyPrivateGroup}, VLESS: true}
	aPlan = decoded[Plan](t, req(t, a, owner, "POST", "/api/plans", aPlan), 200)
	bPlan := Plan{Name: "B", Quota: 2000, ValidDays: 1, Cycle: "30d", Timezone: "UTC", GroupIDs: []string{legacyPublicGroup}, VLESS: true}
	bPlan = decoded[Plan](t, req(t, a, owner, "POST", "/api/plans", bPlan), 200)
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{user.ID}, Action: "assign", PlanID: aPlan.ID})
	before, _ := a.store.record(user.ID)
	pausedAt := time.Now().Unix()
	before.Expires = pausedAt + 3600
	before.Meter.End = pausedAt + 7200
	if err := a.store.save(&before); err != nil {
		t.Fatal(err)
	}
	enableSales(t, a)
	offer := decoded[Offer](t, req(t, a, owner, "POST", "/api/commerce/offers", object{"plan_id": bPlan.ID, "plan_version": bPlan.Version, "price": "0", "enabled": true}), 200)
	order := newOrder(t, a, user, offer)
	orderDo(t, a, user, order, "confirm")
	if err := a.commerceWork(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	active, _ := a.store.record(user.ID)
	if active.Entitlement == nil || active.Entitlement.PlanID != bPlan.ID || len(active.PlanQueue) != 1 {
		t.Fatalf("purchase did not activate B and queue A: %+v", active)
	}
	if active.PlanQueue[0].Entitlement.PlanID != aPlan.ID || active.PlanQueue[0].Expires != before.Expires {
		t.Fatalf("queued A snapshot lost: %+v", active.PlanQueue[0])
	}
	active.Expires = pausedAt - 1
	active.Meter.PendingReset = false
	if err := a.store.save(&active); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceQuotaPeriods(pausedAt); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceQuotaPeriods(pausedAt + 1); err != nil {
		t.Fatal(err)
	}
	restored, _ := a.store.record(user.ID)
	if restored.Entitlement == nil || restored.Entitlement.PlanID != aPlan.ID || len(restored.PlanQueue) != 0 {
		t.Fatalf("queued A was not restored: %+v", restored)
	}
	if restored.Expires <= pausedAt+3590 || restored.Expires > pausedAt+3602 {
		t.Fatalf("remaining A lifetime was not preserved: %d", restored.Expires)
	}
}
