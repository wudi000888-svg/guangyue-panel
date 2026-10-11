package controlplane

import (
	"strings"
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

// Package-scoped rates must reach the accounting path, not only the rendered
// subscription. A rate change also needs a distinct usage revision so the
// business-site reconciliation cannot merge bytes charged under two policies.
func TestAccountUsesEntitlementNodeRateAndRevision(t *testing.T) {
	a := testApp(t)
	member := testUser(t, a, "meter-rate", "user")
	member.Entitlement = &domain.Entitlement{
		PlanID:    "rate-plan",
		Revision:  "ent-one",
		NodeRates: map[string]int64{"vless-main": 2000},
	}
	member.InitMeter("period-rate", 1)
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	if err := a.store.account([]Counter{{Key: "rate", Generation: "one", UserID: member.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 1024}}); err != nil {
		t.Fatal(err)
	}
	first, err := a.store.record(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Meter == nil || first.Meter.Upload != 2048 {
		t.Fatalf("first weighted upload = %#v, want 2048", first.Meter)
	}

	first.Entitlement.NodeRates = map[string]int64{"vless-main": 500}
	first.Entitlement.Revision = "ent-two"
	if err = a.store.save(&first); err != nil {
		t.Fatal(err)
	}
	if err = a.store.account([]Counter{{Key: "rate", Generation: "two", UserID: member.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 1024}}); err != nil {
		t.Fatal(err)
	}
	second, err := a.store.record(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Meter == nil || second.Meter.Upload != 2560 {
		t.Fatalf("combined weighted upload = %#v, want 2560", second.Meter)
	}
	rows, err := a.store.nodeUsage()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("node usage rows = %d, want separate rows for each rate", len(rows))
	}
	seen := map[int64]string{}
	for _, row := range rows {
		seen[row.RateMilli] = row.RateRevision
	}
	if len(seen) != 2 || seen[2000] == "" || seen[500] == "" || seen[2000] == seen[500] {
		t.Fatalf("rate revisions were merged: %+v", rows)
	}
	if !strings.Contains(seen[2000], ":plan:ent-one:") || !strings.Contains(seen[500], ":plan:ent-two:") {
		t.Fatalf("missing entitlement revision in usage rows: %+v", rows)
	}
}
