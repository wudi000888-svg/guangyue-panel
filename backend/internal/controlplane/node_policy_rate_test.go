package controlplane

import (
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func TestRestorePreviousPlanClearsNodeRateOverride(t *testing.T) {
	r := Record{User: User{
		Entitlement: &domain.Entitlement{PlanID: "active", NodeRates: map[string]int64{"vless-main": 2000}},
		PlanQueue: []domain.PlanSlot{{
			Entitlement: &domain.Entitlement{PlanID: "previous"},
		}},
	}}
	if !restorePreviousPlan(&r, 100) {
		t.Fatal("previous plan was not restored")
	}
	if r.Entitlement == nil || len(r.Entitlement.NodeRates) != 0 {
		t.Fatalf("stale node-rate override survived restore: %+v", r.Entitlement)
	}
}

