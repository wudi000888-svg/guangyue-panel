package controlplane

import "testing"

func validRatePlan() Plan {
	return Plan{
		Name:      "rate validation",
		Quota:     1024,
		ValidDays: 30,
		Cycle:     "30d",
		Timezone:  "UTC",
		GroupIDs:  []string{legacyPrivateGroup},
		VLESS:     true,
	}
}

func TestValidatePlanRequiresIndividualNodeForRateOverride(t *testing.T) {
	a := testApp(t)
	p := validRatePlan()
	p.NodeRates = map[string]int64{"vless-main": 2000}
	if err := a.validatePlan(&p); err == nil {
		t.Fatal("group-wide rate override was accepted without an individual node selection")
	}
}

func TestValidatePlanAcceptsCanonicalMountedRateKey(t *testing.T) {
	f := newMountFixture(t)
	f.policy(t, []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}, []BusinessGrant{{UserID: f.user.ID, Budget: 500}})
	p := validRatePlan()
	p.GroupIDs = []string{defaultSubsiteGroup}
	p.NodeIDs = []string{defaultSubsiteGroup + "/" + f.site.ID + "/vless-main"}
	p.NodeRates = map[string]int64{p.NodeIDs[0]: 2000}
	if err := f.master.validatePlan(&p); err != nil {
		t.Fatalf("canonical mounted node rate rejected: %v", err)
	}
}
