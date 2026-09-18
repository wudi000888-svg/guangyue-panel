package controlplane

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSubsiteConfiguration(t *testing.T) {
	for _, edition := range []string{"lite", "pro"} {
		t.Run(edition, func(t *testing.T) {
			cfg := Config{Edition: edition, Role: "business", SiteID: "east", ControllerURL: "https://master.example.com", ConnectToken: "gye_" + randomToken(32)}
			if err := cfg.validateEdition(); err != nil {
				t.Fatal(err)
			}
			if cfg.DatabaseOptions().Driver != "sqlite" || cfg.controller() || cfg.deploymentRole() != "business" {
				t.Fatal("sub-site gained master capabilities or lost SQLite")
			}
			for _, mutate := range []func(*Config){
				func(c *Config) { c.ConnectToken = "" },
				func(c *Config) { c.ControllerURL = "" },
				func(c *Config) { c.ControllerURL = "http://master.example.com" },
				func(c *Config) { c.RedisURL = "redis://localhost/0" },
				func(c *Config) { c.Database.Driver = "postgres" },
			} {
				bad := cfg
				mutate(&bad)
				if bad.validateEdition() == nil {
					t.Fatal("incomplete or incompatible sub-site configuration accepted")
				}
			}
		})
	}
	legacy := Config{Edition: "lite"}
	if legacy.validateEdition() != nil || legacy.deploymentRole() != "standalone" {
		t.Fatal("legacy Lite configuration must retain independent management")
	}
	legacy.Role = "controller"
	if legacy.validateEdition() == nil {
		t.Fatal("Lite became a master")
	}
}

func TestMasterControlsLiteSubsitePermissions(t *testing.T) {
	master := testApp(t)
	master.cfg.Edition, master.cfg.Role, master.cfg.SiteID = "pro", "controller", "control"
	owner := testUser(t, master, "owner", "owner")
	user := testUser(t, master, "member", "user")
	server := httptest.NewServer(master.routes())
	defer server.Close()
	agents := []*App{}
	for _, id := range []string{"east", "west"} {
		site, token := createBusinessTest(t, master, owner, id)
		policy := object{"name": id, "group": "", "enabled": true, "exclusive": false, "revision": site.Revision, "nodes": []Node{}, "grants": []BusinessGrant{{UserID: user.ID}}}
		if w := req(t, master, user, "PUT", "/api/business-sites/"+id, policy); w.Code != 403 {
			t.Fatal("member changed sub-site permissions")
		}
		if w := req(t, master, owner, "PUT", "/api/business-sites/"+id, policy); w.Code != 200 {
			t.Fatalf("master could not grant access: %d %s", w.Code, w.Body)
		}
		agent := testApp(t)
		agent.cfg.Edition, agent.cfg.Role, agent.cfg.SiteID = "lite", "business", id
		agent.cfg.ControllerURL, agent.cfg.ConnectToken = server.URL, token
		agent.cfg.RealityPublic, agent.cfg.ShortID = randomToken(32), "aabbccddaabbccdd"
		// A managed child always has a local owner so it can be used while the
		// controller is offline and can be paired later by a Pro master.
		agent.store.business = true
		if err := agent.store.bootstrap(agent.cfg.StateDir); err != nil {
			t.Fatal(err)
		}
		agents = append(agents, agent)
	}
	sync := func() {
		t.Helper()
		for _, agent := range agents {
			for i := 0; i < 2; i++ { // Apply, then acknowledge the policy.
				if err := agent.syncBusinessAgent(context.Background(), server.Client()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	sync()
	for _, agent := range agents {
		// Child administrators retain local visibility and management. Only the
		// controller-only fleet APIs remain unavailable on Lite.
		local, err := agent.store.record(-1)
		if err != nil || local.Role != "owner" {
			t.Fatal("managed child lost local owner")
		}
		if w := req(t, agent, local, "GET", "/api/state", nil); w.Code != 200 {
			t.Fatalf("local state unavailable: %d %s", w.Code, w.Body.String())
		}
		for _, path := range []string{"/api/state", "/api/operations", "/api/fleet"} {
			if w := req(t, agent, local, "GET", path, nil); w.Code != 200 {
				t.Fatalf("local child endpoint unavailable %s: %d", path, w.Code)
			}
		}
		if w := req(t, agent, local, "POST", "/api/fleet/peers", object{}); w.Code != 403 {
			t.Fatalf("Lite child gained peer takeover: %d", w.Code)
		}
		if w := req(t, agent, local, "GET", "/api/business-sites", nil); w.Code != 403 {
			t.Fatalf("child exposed controller site directory: %d", w.Code)
		}
	}
	// Global protocol and expiration settings reach every assigned sub-site.
	expires := time.Now().Add(time.Hour).Unix()
	input := userInput{Username: user.Username, Enabled: true, VLESS: false, HY2: true, Expires: expires}
	if w := req(t, master, owner, "PUT", fmt.Sprintf("/api/users/%d", user.ID), input); w.Code != 200 {
		t.Fatalf("master permission change: %d %s", w.Code, w.Body)
	}
	sync()
	for _, agent := range agents {
		members := managedRecords(t, agent)
		if len(members) != 1 || members[0].VLESS || !members[0].HY2 || members[0].Expires != expires || members[0].Role != "user" {
			t.Fatal("master protocol, expiry or role policy not enforced")
		}
	}
	// Removing one site's node groups must not affect another site's access.
	site, _ := master.store.businessSite("east")
	denied := businessDefaultNodes()
	for i := range denied {
		denied[i] = normalizeNodePolicy(denied[i])
		denied[i].GroupIDs = []string{}
	}
	policy := object{"name": site.Name, "group": site.Group, "enabled": true, "exclusive": false, "revision": site.Revision, "nodes": []Node{}, "default_nodes": denied, "grants": site.Grants}
	if w := req(t, master, owner, "PUT", "/api/business-sites/east", policy); w.Code != 200 {
		t.Fatalf("master node permission change: %d %s", w.Code, w.Body)
	}
	sync()
	for i, agent := range agents {
		members := managedRecords(t, agent)
		nodes, _ := agent.store.nodes()
		for _, n := range nodes {
			if nodeGroupAllowed(members[0], n) != (i == 1) {
				t.Fatal("site-specific node permissions crossed site boundaries")
			}
		}
	}
	current, _ := master.store.record(user.ID)
	catalog, err := master.subscriptionCatalog(current, false, "")
	if err != nil {
		t.Fatal(err)
	}
	remote := 0
	for _, entry := range catalog {
		if entry.siteID != "" {
			remote++
			if entry.siteID != "west" || entry.node.Protocol != "hy2" {
				t.Fatal("subscription bypassed sub-site permissions")
			}
		}
	}
	if remote != 1 {
		t.Fatal("authorized west node missing")
	}
	input.Enabled = false
	if w := req(t, master, owner, "PUT", fmt.Sprintf("/api/users/%d", user.ID), input); w.Code != 200 {
		t.Fatal("master could not disable member")
	}
	sync()
	for _, agent := range agents {
		for _, u := range managedRecords(t, agent) {
			if u.Active() {
				t.Fatal("disabled member retained sub-site authorization")
			}
		}
	}
}

func managedRecords(t *testing.T, a *App) []Record {
	t.Helper()
	all := mustCoreRecords(t, a)
	users := make([]Record, 0, len(all))
	for _, r := range all {
		if r.Role == "user" {
			users = append(users, r)
		}
	}
	return users
}
