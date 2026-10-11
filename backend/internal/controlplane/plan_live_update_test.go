package controlplane

import (
	"encoding/json"
	"testing"
)

// Active subscriptions are backed by a plan's current node selection. Editing
// a plan must invalidate the issued selection immediately; members should not
// need to be assigned the plan again or wait for a new order to see a node
// added to that plan.
func TestEditingPlanRefreshesActiveSubscriptionNodes(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")

	plan := Plan{
		Name:      "Live nodes",
		Quota:     1024,
		ValidDays: 30,
		Cycle:     "30d",
		Timezone:  "UTC",
		GroupIDs:  []string{legacyPrivateGroup},
		NodeIDs:   []string{"vless-main"},
		VLESS:     true,
		HY2:       true,
	}
	plan = decoded[Plan](t, req(t, a, owner, "POST", "/api/plans", plan), 200)
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{member.ID}, Action: "assign", PlanID: plan.ID})
	member, err := a.store.record(member.ID)
	if err != nil {
		t.Fatal(err)
	}

	before, err := a.subscriptionCatalogSource(member, "local", "")
	if err != nil || len(before) != 1 || before[0].node.ID != "vless-main" {
		t.Fatalf("initial plan subscription = %d (%v), want vless only", len(before), err)
	}
	beforeHash, err := a.desiredCoreHash()
	if err != nil {
		t.Fatal(err)
	}

	// Add HY2 to the same plan. This is the operation that currently leaves
	// already-issued entitlements with their old NodeIDs and stale subscriptions.
	plan.NodeIDs = []string{"vless-main", "hy2-main"}
	updated := req(t, a, owner, "POST", "/api/plans", plan)
	if updated.Code != 200 {
		t.Fatalf("update plan: %d %s", updated.Code, updated.Body.String())
	}

	current, err := a.store.record(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := a.subscriptionCatalogSource(current, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("updated plan subscription = %d, want vless + hy2", len(after))
	}
	seen := map[string]bool{}
	for _, entry := range after {
		seen[entry.node.ID] = true
	}
	if !seen["vless-main"] || !seen["hy2-main"] {
		t.Fatalf("updated plan nodes = %#v", seen)
	}

	// Keep the assertion explicit so an implementation that only refreshes a
	// frontend cache while leaving the persisted entitlement stale cannot pass.
	if current.Entitlement == nil || len(current.Entitlement.NodeIDs) != 2 {
		t.Fatal("active entitlement did not receive the edited node selection")
	}
	afterHash, err := a.desiredCoreHash()
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash == afterHash {
		t.Fatal("core authorization hash did not change with the edited node selection")
	}
	var saved Plan
	if err := json.Unmarshal(updated.Body.Bytes(), &saved); err != nil || saved.Version != plan.Version+1 {
		t.Fatalf("updated plan version missing: %+v (%v)", saved, err)
	}
}
