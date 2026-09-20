package controlplane

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIndependentNodeAccessAndPlanAuthority(t *testing.T) {
	a := testApp(t)
	owner, u := testUser(t, a, "owner", "owner"), testUser(t, a, "member", "user")
	u.Quota, u.Upload, u.Expires = 10000, 123, time.Now().Unix()+86400
	u.InitMeter("existing-period", time.Now().Unix())
	a.store.save(&u)
	baseline := u
	apply := func(mode string, groups ...string) Record {
		t.Helper()
		applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "groups", GroupMode: mode, GroupIDs: groups})
		got, err := a.store.record(u.ID)
		if err != nil {
			t.Fatal(err)
		}
		unchanged := got
		unchanged.NodeGroupIDs = baseline.NodeGroupIDs
		if !reflect.DeepEqual(unchanged, baseline) {
			t.Fatal("node assignment changed account, usage, credentials or plan")
		}
		return got
	}
	got := apply("add", defaultSubsiteGroup)
	if !reflect.DeepEqual(*got.NodeGroupIDs, []string{defaultSubsiteGroup, legacyPrivateGroup, legacyPublicGroup}) {
		t.Fatal("append lost inherited access")
	}
	got = apply("replace", defaultSubsiteGroup)
	if !reflect.DeepEqual(*got.NodeGroupIDs, []string{defaultSubsiteGroup}) {
		t.Fatal("replace retained other groups")
	}
	got = apply("replace")
	if got.NodeGroupIDs == nil || len(*got.NodeGroupIDs) != 0 {
		t.Fatal("empty permission list not persisted")
	}
	entries, err := a.subscriptionCatalogSource(got, "all", "")
	if err != nil || len(entries) != 0 {
		t.Fatal("empty permissions fell back to default nodes", err)
	}
	got = apply("inherit")
	if got.NodeGroupIDs != nil {
		t.Fatal("inherit retained override")
	}
	entries, err = a.subscriptionCatalogSource(got, "all", "")
	if err != nil || len(entries) != 2 {
		t.Fatal("default nodes not restored", err)
	}
	p := createTestPlan(t, a, owner)
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "assign", PlanID: p.ID})
	baseline, _ = a.store.record(u.ID)
	for _, mode := range []string{"add", "replace", "inherit"} {
		groups := []string{defaultSubsiteGroup}
		if mode == "inherit" {
			groups = nil
		}
		w := req(t, a, owner, "POST", "/api/entitlements/batch", entitlementRequest{IDs: []int64{u.ID}, Action: "groups", GroupMode: mode, GroupIDs: groups, Preview: true})
		if w.Code != 409 {
			t.Fatal("plan permissions could be overridden", mode, w.Code)
		}
	}
	for _, override := range [][]string{{}, {defaultSubsiteGroup, legacyPublicGroup}} {
		got = baseline
		got.NodeGroupIDs = &override
		if err := a.store.save(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(userNodeGroupIDs(got.User), p.GroupIDs) {
			t.Fatal("legacy override replaced plan groups")
		}
		resolved := []Record{got}
		if err := a.store.resolveAccess(resolved); err != nil {
			t.Fatal(err)
		}
		if !nodeGroupAllowed(resolved[0], Node{PolicyVersion: 1, GroupIDs: p.GroupIDs}) || nodeGroupAllowed(resolved[0], Node{PolicyVersion: 1, GroupIDs: []string{defaultSubsiteGroup}}) {
			t.Fatal("resolved policy bypassed plan")
		}
		entries, err := a.subscriptionCatalogSource(got, "all", "")
		if err != nil || len(entries) != 2 {
			t.Fatal("plan subscription incorrectly filtered", err, len(entries))
		}
		stored, _ := a.store.record(got.ID)
		stored.NodeGroupIDs = baseline.NodeGroupIDs
		if !reflect.DeepEqual(stored, baseline) {
			t.Fatal("authorization changed quota, expiry, usage or credentials")
		}
	}
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "assign", PlanID: p.ID})
	got, _ = a.store.record(u.ID)
	if got.NodeGroupIDs != nil {
		t.Fatal("new plan retained stale override")
	}
}

func TestDirectNodeAccessPreviewAndAuthorization(t *testing.T) {
	a := testApp(t)
	owner, u := testUser(t, a, "owner", "owner"), testUser(t, a, "member", "user")
	request := entitlementRequest{IDs: []int64{u.ID}, Action: "groups", GroupMode: "replace", GroupIDs: []string{defaultSubsiteGroup}, Preview: true}
	for _, actor := range []Record{{}, u} {
		if w := req(t, a, actor, "POST", "/api/entitlements/batch", request); w.Code != 401 && w.Code != 403 {
			t.Fatal("non-owner changed permissions", w.Code)
		}
	}
	for _, groups := range [][]string{{"missing"}, {defaultSubsiteGroup, defaultSubsiteGroup}} {
		bad := request
		bad.GroupIDs = groups
		if w := req(t, a, owner, "POST", "/api/entitlements/batch", bad); w.Code != 400 {
			t.Fatal("invalid groups accepted", w.Code)
		}
	}
	w := req(t, a, owner, "POST", "/api/entitlements/batch", request)
	var preview struct{ Expected string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil {
		t.Fatal("preview failed", w.Body.String())
	}
	got, _ := a.store.record(u.ID)
	if got.NodeGroupIDs != nil {
		t.Fatal("preview changed user")
	}
	applyTestEntitlement(t, a, owner, entitlementRequest{IDs: []int64{u.ID}, Action: "groups", GroupMode: "replace", GroupIDs: []string{legacyPrivateGroup}})
	request.Preview, request.Expected, request.OperationID = false, preview.Expected, randomToken(18)
	if w := req(t, a, owner, "POST", "/api/entitlements/batch", request); w.Code != 409 {
		t.Fatal("stale preview accepted", w.Code)
	}
	// A group's change also invalidates a previously confirmed selection.
	request.Preview = true
	w = req(t, a, owner, "POST", "/api/entitlements/batch", request)
	json.Unmarshal(w.Body.Bytes(), &preview)
	groups, _ := a.store.nodeGroups()
	for _, g := range groups {
		if g.ID == defaultSubsiteGroup {
			g.Name = "Changed group"
			if w := req(t, a, owner, "POST", "/api/node-groups", g); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
		}
	}
	request.Preview, request.Expected = false, preview.Expected
	if w := req(t, a, owner, "POST", "/api/entitlements/batch", request); w.Code != 409 {
		t.Fatal("changed group preview accepted", w.Code)
	}
	u.Archived = true
	a.store.save(&u)
	request.Preview = true
	if w := req(t, a, owner, "POST", "/api/entitlements/batch", request); w.Code != 409 {
		t.Fatal("archived user changed", w.Code)
	}
}

func TestDirectNodeAccessMountsBothProtocolsAndRevokes(t *testing.T) {
	f := newMountFixture(t)
	w := req(t, f.master, f.owner, "PUT", "/api/business-sites/"+f.site.ID+"/node-pool", object{"revision": f.site.Revision, "catalog_revision": f.site.Mount.Catalog.Revision, "assignment": "groups", "nodes": []MountedNode{{NodeID: "vless-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}, {NodeID: "hy2-main", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	applyTestEntitlement(t, f.master, f.owner, entitlementRequest{IDs: []int64{f.user.ID}, Action: "groups", GroupMode: "replace", GroupIDs: []string{defaultSubsiteGroup}})
	f.sync(t)
	u, _ := f.master.store.record(f.user.ID)
	entries, err := f.master.subscriptionCatalogSource(u, "all", "")
	if err != nil || len(entries) != 2 {
		t.Fatal("both child protocols missing", err, len(entries))
	}
	for _, entry := range entries {
		uri, err := url.Parse(entry.uri)
		if err != nil || !entry.mounted || uri.Hostname() != f.child.cfg.VLESSHost && uri.Hostname() != f.child.cfg.HY2Host {
			t.Fatal("subscription did not use direct child endpoint")
		}
	}
	shadow := f.delegated(t)
	if len(shadow.Mount.NodeIDs) != 2 || u.Entitlement != nil {
		t.Fatal("direct access required plan or lost protocol")
	}
	if w := req(t, f.child, f.localOwner, "POST", "/api/entitlements/batch", entitlementRequest{IDs: []int64{shadow.ID}, Action: "groups", GroupMode: "replace", Preview: true}); w.Code != 409 {
		t.Fatal("delegated identity permissions locally overridden", w.Code)
	}
	applyTestEntitlement(t, f.master, f.owner, entitlementRequest{IDs: []int64{u.ID}, Action: "groups", GroupMode: "replace"})
	u, _ = f.master.store.record(u.ID)
	entries, err = f.master.subscriptionCatalogSource(u, "all", "")
	if err != nil || len(entries) != 0 {
		t.Fatal("cached subscriptions survived revocation", err)
	}
	f.sync(t)
	if len(f.delegated(t).Mount.NodeIDs) != 0 {
		t.Fatal("child authorization survived revocation")
	}
	local, _ := f.child.store.record(f.local.ID)
	if !reflect.DeepEqual(local, f.local) {
		t.Fatal("child local account changed")
	}
}

func TestNodeGroupMembersDistinguishProtocolAndSite(t *testing.T) {
	f := newMountFixture(t)
	nodes := []MountedNode{{NodeID: "vless-main", Name: "Same node", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}, {NodeID: "hy2-main", Name: "Same node", Enabled: true, GroupIDs: []string{defaultSubsiteGroup}}}
	f.policy(t, nodes, nil)
	w := req(t, f.master, f.owner, "GET", "/api/node-groups", nil)
	var out struct {
		Members map[string][]struct {
			SiteID    string `json:"site_id"`
			SiteName  string `json:"site_name"`
			Source    string
			NodeID    string `json:"node_id"`
			Protocol  string
			EntryHost string `json:"entry_host"`
			EntryPort int    `json:"entry_port"`
			Status    string
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("directory failed", w.Code)
	}
	got := out.Members[defaultSubsiteGroup]
	if len(got) != 2 {
		t.Fatal("same-name nodes collapsed", len(got))
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.Protocol] = true
		host := f.child.cfg.VLESSHost
		if m.Protocol == "hy2" {
			host = f.child.cfg.HY2Host
		}
		if m.SiteID != f.site.ID || m.SiteName != f.site.Name || m.Source != "mounted" || m.EntryHost != host || m.EntryPort != 443 || m.Status != "ready" {
			t.Fatal("incomplete mounted identity", m)
		}
	}
	if !seen["hy2"] || !seen["vless"] {
		t.Fatal("protocol identity missing")
	}
	for _, m := range out.Members[legacyPrivateGroup] {
		if m.Source != "local" || m.SiteName == "" {
			t.Fatal("local identity missing")
		}
	}
	for _, secret := range []string{f.owner.Credentials.Token, f.user.Credentials.HY2, f.user.Credentials.VLESS["vless-main"]} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("directory exposed credentials")
		}
	}
	s, _ := f.master.store.businessSite(f.site.ID)
	s.Mount.LeaseUntil = 1
	f.master.store.saveBusinessSite(s)
	w = req(t, f.master, f.owner, "GET", "/api/node-groups", nil)
	json.Unmarshal(w.Body.Bytes(), &out)
	for _, m := range out.Members[defaultSubsiteGroup] {
		if m.Status != "pending" {
			t.Fatal("expired authorization marked ready")
		}
	}
}

func TestDirectNodeAccessCommerceVersionCompatibility(t *testing.T) {
	u := Record{User: User{Quota: 1000, Enabled: true, VLESS: true}}
	previous := digest(string(jsonBytes(object{"entitlement": u.Entitlement, "quota": u.Quota, "expires": u.Expires, "enabled": u.Enabled, "vless": u.VLESS, "hy2": u.HY2, "period": ""})))
	if userCommerceVersion(u) != previous {
		t.Fatal("upgrade invalidated legacy pending order")
	}
	u.NodeGroupIDs = pointer([]string{})
	if userCommerceVersion(u) == previous {
		t.Fatal("permission change invisible to checkout")
	}
}
