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

func TestValidatePlanAcceptsOnlyHalfStepRatesUpToTen(t *testing.T) {
	a := testApp(t)
	nodes, err := a.store.nodes()
	if err != nil { t.Fatal(err) }
	for _, rate := range []int64{500, 1000, 1500, 10000} {
		p := validRatePlan()
		p.NodeIDs = []string{nodes[0].ID}
		p.NodeRates = map[string]int64{nodes[0].ID: rate}
		if err := a.validatePlan(&p); err != nil { t.Fatalf("rate %d rejected: %v", rate, err) }
	}
	for _, rate := range []int64{0, 250, 10500, 1250} {
		p := validRatePlan()
		p.NodeIDs = []string{nodes[0].ID}
		p.NodeRates = map[string]int64{nodes[0].ID: rate}
		if err := a.validatePlan(&p); err == nil { t.Fatalf("rate %d accepted", rate) }
	}
}
