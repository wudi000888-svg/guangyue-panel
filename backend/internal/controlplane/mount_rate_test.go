package controlplane

import (
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func TestMountedPlanRateIsAppliedByChildMeter(t *testing.T) {
	f := newMountFixture(t)
	f.policy(t, []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}, []BusinessGrant{{UserID: f.user.ID, Budget: 600}})
	f.user, _ = f.master.store.record(f.user.ID)
	f.user.Entitlement = &domain.Entitlement{
		Revision: "mounted-rate",
		GroupIDs: []string{defaultSubsiteGroup},
		NodeIDs: []string{defaultSubsiteGroup + "/" + f.site.ID + "/vless-main"},
		NodeRates: map[string]int64{defaultSubsiteGroup + "/" + f.site.ID + "/vless-main": 2000},
	}
	if err := f.master.store.save(&f.user); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	shadow := f.delegated(t)
	if shadow.Entitlement == nil || shadow.Entitlement.NodeRates["vless-main"] != 2000 {
		t.Fatalf("child did not receive package rate: %+v", shadow.Entitlement)
	}
	if err := f.child.store.account([]Counter{{Key: "mounted-rate", Generation: "one", UserID: shadow.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 100}}); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	f.sync(t)
	current, err := f.master.store.record(f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Meter == nil || current.Meter.Upload != 200 {
		t.Fatalf("mounted traffic was charged at the default rate: %#v", current.Meter)
	}
}
