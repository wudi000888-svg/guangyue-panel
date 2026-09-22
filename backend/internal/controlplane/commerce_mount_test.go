package controlplane

import (
	"fmt"
	"testing"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func TestPurchaseReallocatesOldMountBudgetsAndExhaustionRevokes(t *testing.T) {
	for _, oldQuota := range []int64{0, 1000} {
		t.Run(fmt.Sprint(oldQuota), func(t *testing.T) {
			f := newMountFixture(t)
			a := f.master
			f.user.Quota = oldQuota
			f.user.Entitlement = &domain.Entitlement{GroupIDs: []string{defaultSubsiteGroup}}
			if err := a.store.save(&f.user); err != nil {
				t.Fatal(err)
			}
			autoMountPolicy(t, f, []string{defaultSubsiteGroup})
			shadow := f.delegated(t)
			if shadow.Quota != oldQuota {
				t.Fatal("old quota not issued")
			}
			// Publication and purchase work despite a larger or unlimited old grant.
			p := decoded[Plan](t, req(t, a, f.owner, "POST", "/api/plans", Plan{Name: "Smaller", Quota: 100, ValidDays: 30, Cycle: "30d", Timezone: "UTC", GroupIDs: []string{defaultSubsiteGroup}, VLESS: true}), 200)
			offer := decoded[Offer](t, req(t, a, f.owner, "POST", "/api/commerce/offers", object{"plan_id": p.ID, "plan_version": p.Version, "price": "0", "enabled": true}), 200)
			order := newOrder(t, a, f.user, offer)
			order = orderDo(t, a, f.user, order, "confirm")
			if err := a.fulfilOrder(order, time.Now().Unix()); err != nil {
				t.Fatal(err)
			}
			current, _ := a.store.order(order.ID)
			if current.State != "provisioning" {
				t.Fatal("old remote grant was not settled first")
			}
			f.sync(t)
			if err := a.fulfilOrder(current, time.Now().Unix()); err != nil {
				t.Fatal(err)
			}
			current, _ = a.store.order(order.ID)
			if current.State != "completed" {
				t.Fatal("purchase blocked by previous budget", current.State)
			}
			f.sync(t)
			shadow = f.delegated(t)
			if shadow.Quota != 100 {
				t.Fatal("new finite budget not applied", shadow.Quota)
			}
			if err := f.child.store.account([]Counter{{Key: "new-plan", Generation: "one", UserID: shadow.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: 100}}); err != nil {
				t.Fatal(err)
			}
			f.sync(t)
			f.sync(t)
			user, _ := a.store.record(f.user.ID)
			entries, err := a.subscriptionCatalogSource(user, "all", "")
			if err != nil || len(entries) != 0 || user.QuotaUsed() != 100 {
				t.Fatal("exhausted plan kept subscription", len(entries), user.QuotaUsed(), err)
			}
			if len(f.delegated(t).Mount.NodeIDs) != 0 {
				t.Fatal("exhausted child authorization retained")
			}
			site, _ := a.store.businessSite(f.site.ID)
			if len(site.Mount.Nodes) != 1 {
				t.Fatal("quota exhaustion removed group membership")
			}
		})
	}
}

func TestPurchaseLegacySharesAreBoundedByNewPackage(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	u.Quota = 90
	u.VLESS = true
	u.Entitlement = &domain.Entitlement{GroupIDs: []string{legacyPrivateGroup, defaultSubsiteGroup}}
	sites := []BusinessSite{{ID: "one", Enabled: true, OwnerID: owner.ID, Grants: []BusinessGrant{{UserID: u.ID, Quota: 0}}}, {ID: "two", Enabled: true, OwnerID: owner.ID, Grants: []BusinessGrant{{UserID: u.ID, Quota: 5000, Budget: 5000}}}}
	shares, err := a.purchaseSiteBudgets(u, sites)
	if err != nil {
		t.Fatal(err)
	}
	if shares["one"] != 30 || shares["two"] != 30 {
		t.Fatal("legacy grants not bounded with local capacity", shares)
	}
	u.Entitlement.GroupIDs = []string{legacyPrivateGroup}
	shares, err = a.purchaseSiteBudgets(u, sites)
	if err != nil || len(shares) != 0 {
		t.Fatal("package exclusion kept legacy grants", shares, err)
	}
}

func TestRenewalDoesNotRejectOldReservations(t *testing.T) {
	f := newMountFixture(t)
	a := f.master
	p := decoded[Plan](t, req(t, a, f.owner, "POST", "/api/plans", Plan{Name: "Renew", Quota: 100, ValidDays: 30, Cycle: "30d", Timezone: "UTC", GroupIDs: []string{defaultSubsiteGroup}, VLESS: true}), 200)
	offer := decoded[Offer](t, req(t, a, f.owner, "POST", "/api/commerce/offers", object{"plan_id": p.ID, "plan_version": p.Version, "price": "0", "enabled": true}), 200)
	assignPlan(&f.user, p, time.Now().Unix())
	f.user.Meter.Upload = 40
	if err := a.store.save(&f.user); err != nil {
		t.Fatal(err)
	}
	autoMountPolicy(t, f, []string{defaultSubsiteGroup})
	site, _ := a.store.businessSite(f.site.ID)
	for i := range site.Grants {
		if site.Grants[i].UserID == f.user.ID {
			site.Grants[i].Quota = 1000
			site.Grants[i].Budget = 1000
		}
	}
	if err := a.store.saveBusinessSite(site); err != nil {
		t.Fatal(err)
	}
	order := newOrder(t, a, f.user, offer)
	if order.Action != "renew" {
		t.Fatal("not a renewal", order.Action)
	}
	order = orderDo(t, a, f.user, order, "confirm")
	if err := a.fulfilOrder(order, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	current, _ := a.store.order(order.ID)
	after, _ := a.store.record(f.user.ID)
	if current.State != "completed" || after.Expires != f.user.Expires+30*86400 || after.QuotaUsed() != 40 || after.Meter.PeriodID != f.user.Meter.PeriodID {
		t.Fatal("renewal blocked or changed current quota", current.State)
	}
}
