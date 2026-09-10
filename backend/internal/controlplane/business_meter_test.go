package controlplane

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBusinessWeightedQuotasSparseAcknowledgementAndReset(t *testing.T) {
	master := testApp(t)
	master.cfg.Edition = "pro"
	master.cfg.Role = "controller"
	master.cfg.SiteID = "control"
	owner := testUser(t, master, "owner", "owner")
	user := testUser(t, master, "alice", "user")
	user.Quota = 1000
	master.store.save(&user)
	site, token := createBusinessTest(t, master, owner, "east")
	site.Grants = []BusinessGrant{{UserID: user.ID, Quota: 600, Budget: 600}}
	n := normalizeNodePolicy(businessDefaultNodes()[0])
	n.RateMilli = 1500
	n.RateRevision = "rate-1500"
	site.DefaultNodes = []Node{n}
	master.store.saveBusinessSite(site)
	agent := testApp(t)
	agent.cfg.Edition = "pro"
	agent.cfg.Role = "business"
	agent.cfg.SiteID = site.ID
	agent.cfg.EnrollmentToken = token
	agent.cfg.RealityPublic = randomToken(32)
	agent.cfg.ShortID = "aabbccddaabbccdd"
	server := httptest.NewServer(master.routes())
	defer server.Close()
	agent.cfg.ControllerURL = server.URL
	sync := func() {
		t.Helper()
		if err := agent.syncBusinessAgent(context.Background(), server.Client()); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	sync()
	account := func(value int64) {
		t.Helper()
		if err := agent.store.account([]Counter{{Key: "node-up", Generation: "one", UserID: user.ID, NodeID: "vless-main", Protocol: "vless", Direction: "up", Value: value}}); err != nil {
			t.Fatal(err)
		}
	}
	account(1)
	sync()
	sync()
	u, _ := master.store.record(user.ID)
	if u.Upload != 1 || u.QuotaUsed() != 1 {
		t.Fatalf("first weighted byte: raw=%d quota=%d", u.Upload, u.QuotaUsed())
	}
	pending, err := agent.store.pendingNodeUsage()
	if err != nil || len(pending) != 0 {
		t.Fatal("acknowledged rows repeated forever")
	}
	account(2)
	sync()
	u, _ = master.store.record(user.ID)
	if u.QuotaUsed() != 3 {
		t.Fatal("fraction was lost between reports")
	}
	site, _ = master.store.businessSite(site.ID)
	n.RateMilli = 500
	n.RateRevision = "rate-500"
	site.DefaultNodes = []Node{n}
	master.store.saveBusinessSite(site)
	sync()
	account(4)
	sync()
	sync()
	u, _ = master.store.record(user.ID)
	if u.Upload != 4 || u.QuotaUsed() != 4 {
		t.Fatal("rate switch reweighted business history")
	}
	oldPeriod := u.Meter.PeriodID
	applyTestEntitlement(t, master, owner, entitlementRequest{IDs: []int64{user.ID}, Action: "reset"})
	if err = master.advanceQuotaPeriods(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	u, _ = master.store.record(user.ID)
	if !u.Meter.PendingReset || u.Meter.PeriodID != oldPeriod {
		t.Fatal("offline site duplicated quota")
	}
	sync()
	sync()
	if err = master.advanceQuotaPeriods(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	u, _ = master.store.record(user.ID)
	if u.QuotaUsed() != 0 || u.Upload != 4 || u.Meter.PeriodID == oldPeriod {
		t.Fatal("reset did not preserve lifetime accounting")
	}
	sync()
	account(5)
	sync()
	u, _ = master.store.record(user.ID)
	if u.QuotaUsed() != 0 || u.Upload != 5 {
		t.Fatal("new period carried old weighted usage")
	}
	site, _ = master.store.businessSite(site.ID)
	if site.Grants[0].Quota != 604 {
		t.Fatalf("grant not rebased in weighted units: %d", site.Grants[0].Quota)
	}
	// A fabricated extra delta in the closed period cannot consume the new one.
	rows, _ := agent.store.nodeUsage()
	var late NodeUsage
	for _, row := range rows {
		if row.PeriodID == oldPeriod {
			late = row
			break
		}
	}
	late.Upload++
	users, _ := agent.store.records()
	report := BusinessHeartbeat{Protocol: 2, Usage: map[int64]BusinessUsage{}, UsageBaseline: map[int64]BusinessUsage{}, NodeUsage: []NodeUsage{late}}
	for _, r := range users {
		report.Usage[r.ID] = businessUsageOf(r.User)
		report.UsageBaseline[r.ID] = BusinessUsage{Upload: r.Meter.InitialUpload, Download: r.Meter.InitialDownload}
	}
	if err = master.acceptBusinessUsage(&site, report); err == nil {
		t.Fatal("late closed-period traffic accepted")
	}
	after, _ := master.store.record(user.ID)
	if after.QuotaUsed() != u.QuotaUsed() || after.Upload != u.Upload {
		t.Fatal("rejected report changed quota")
	}
}

func TestBusinessExitDeletionCleansGroupsAndRestoresOnFailure(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro"
	owner := testUser(t, a, "owner", "owner")
	p := IPResource{Node: Node{ID: "outside", Protocol: "vless", Exit: "http", Host: "8.8.8.8", Port: 80, Enabled: true}, PoolGroup: "private"}
	a.store.savePoolNodes([]IPResource{p}, nil)
	site, _ := createBusinessTest(t, a, owner, "east")
	n := normalizeNodePolicy(Node{ID: "business-node", ExitID: p.ID, Protocol: "vless", Enabled: true})
	site.Nodes = []Node{n}
	a.store.saveBusinessSite(site)
	change, err := a.planResourceChange("ips", batchRequest{IDs: []string{p.ID}, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err = a.applyResourceChange(change, func() error {
		attempts++
		if attempts == 1 {
			return errNoNodes
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected apply error")
	}
	restored, _ := a.store.businessSite(site.ID)
	if len(restored.Nodes) != 1 || len(restored.Nodes[0].GroupIDs) != 1 {
		t.Fatal("failed deletion lost business membership")
	}
	if err = a.applyResourceChange(change, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	removed, _ := a.store.businessSite(site.ID)
	if len(removed.Nodes) != 0 {
		t.Fatal("deleted exit retained business node membership")
	}
}
