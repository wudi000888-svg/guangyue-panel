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
			cfg := Config{Edition: edition, Role: "business", SiteID: "east", ControllerURL: "https://master.example.com", EnrollmentToken: "gye_" + randomToken(32)}
			if err := cfg.validateEdition(); err != nil {
				t.Fatal(err)
			}
			if cfg.DatabaseOptions().Driver != "sqlite" || cfg.controller() || cfg.deploymentRole() != "business" {
				t.Fatal("sub-site gained master capabilities or lost SQLite")
			}
			for _, mutate := range []func(*Config){
				func(c *Config) { c.EnrollmentToken = "" },
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
		agent.cfg.ControllerURL, agent.cfg.EnrollmentToken = server.URL, token
		agent.cfg.RealityPublic, agent.cfg.ShortID = randomToken(32), "aabbccddaabbccdd"
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
		// Even a stale local owner session cannot bypass the managed-site boundary.
		local := testUser(t, agent, "local-owner", "owner")
		for _, path := range []string{"/api/state", "/api/users", "/api/nodes", "/api/business-sites", "/api/fleet", "/api/fleet-gateway", "/api/login", "/api/updates", "/sub/token", "/"} {
			for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
				if w := req(t, agent, local, method, path, object{}); w.Code != 404 {
					t.Fatalf("sub-site exposed %s %s: %d", method, path, w.Code)
				}
			}
		}
		if _, err := agent.store.db.Exec("DELETE FROM users WHERE id=?", local.ID); err != nil {
			t.Fatal(err)
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
		members := mustCoreRecords(t, agent)
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
		members := mustCoreRecords(t, agent)
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
		for _, u := range mustCoreRecords(t, agent) {
			if u.Active() {
				t.Fatal("disabled member retained sub-site authorization")
			}
		}
	}
}
