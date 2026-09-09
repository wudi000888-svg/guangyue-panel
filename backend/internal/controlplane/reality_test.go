package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func realityTestConfig() Config {
	return Config{RealitySNI: "www.cloudflare.com", RealityTarget: "www.cloudflare.com:443", PublicURL: "https://panel.company.com", VLESSHost: "node.company.com", HY2Host: "hy.company.com"}
}

func TestRealitySNIHostnameBoundary(t *testing.T) {
	c := realityTestConfig()
	for _, value := range []string{"https://www.microsoft.com", "www.microsoft.com:443", "1.1.1.1", "[2606:4700::1111]", "*.microsoft.com", "a/b.com", "a\\b.com", "a..com", "-a.com", "a-.com", "a.com.", "a.com\nx", "a_%2f.com", "中文.com", "a.local", "a.test", "a.internal", "PANEL.COMPANY.COM", "node.company.com", "hy.company.com", "company.com", strings.Repeat("a", 60) + ".com"} {
		if _, err := normalizeRealitySNI(value, c); err == nil {
			t.Errorf("unsafe SNI accepted: %q", value)
		}
	}
	for value, want := range map[string]string{"": "", " WWW.CLOUDFLARE.COM ": "", " WWW.MICROSOFT.COM ": "www.microsoft.com", strings.Repeat("a", 59) + ".com": strings.Repeat("a", 59) + ".com", "xn--bcher-kva.de": "xn--bcher-kva.de"} {
		got, err := normalizeRealitySNI(value, c)
		if err != nil || got != want {
			t.Errorf("valid SNI changed: %q => %q, %v", value, got, err)
		}
	}
}

func TestRealityDNSRejectsPrivateMixedAndSelfBeforeDial(t *testing.T) {
	for _, addresses := range [][]string{{"127.0.0.1"}, {"10.0.0.1"}, {"169.254.169.254"}, {"198.18.0.1"}, {"1.1.1.1", "192.168.1.1"}, {"::ffff:127.0.0.1"}, {"203.0.113.10"}, {"8.8.4.4"}, {"2001:db8::1"}} {
		calls := 0
		lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "target.company.net" {
				return []net.IPAddr{{IP: net.ParseIP("8.8.4.4")}}, nil
			}
			out := []net.IPAddr{}
			for _, address := range addresses {
				out = append(out, net.IPAddr{IP: net.ParseIP(address)})
			}
			return out, nil
		}
		_, err := resolveRealityTarget(context.Background(), realityTestConfig(), nil, "target.company.net", lookup, func(context.Context, string, string) error { calls++; return nil })
		if err == nil || calls != 0 {
			t.Fatalf("unsafe DNS answer dialed: %v", addresses)
		}
	}
}

func TestRealityPinsOnlyVerifiedTLSAddressAndKeepsDNSOutOfDial(t *testing.T) {
	calls, targetLookups := []string{}, 0
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "target.company.net" {
			return []net.IPAddr{{IP: net.ParseIP("8.8.4.4")}}, nil
		}
		targetLookups++
		if targetLookups > 1 {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	ip, err := resolveRealityTarget(context.Background(), realityTestConfig(), nil, "target.company.net", lookup, func(ctx context.Context, sni, address string) error {
		if sni != "target.company.net" || net.ParseIP(address) == nil {
			t.Fatal("probe does not retain original SNI and validated IP")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 4*time.Second {
			t.Fatal("probe is unbounded")
		}
		calls = append(calls, address)
		if address == "1.1.1.1" {
			return errors.New("certificate rejected")
		}
		return nil
	})
	if err != nil || ip != "8.8.8.8" || targetLookups != 1 || len(calls) != 2 {
		t.Fatalf("target pinning failed: %s, %v, %v", ip, calls, err)
	}
}

func realityNodesFixture(t *testing.T) (*App, Record, []Node) {
	a := testApp(t)
	a.cfg.RealitySNI, a.cfg.RealityTarget = "www.cloudflare.com", "www.cloudflare.com:443"
	u := testUser(t, a, "reality-member", "user")
	nodes, _ := a.store.nodes()
	for _, entry := range []struct{ id, sni, ip string }{{"custom-a", "www.microsoft.com", "1.1.1.1"}, {"custom-b", "www.microsoft.com", "1.1.1.1"}, {"custom-c", "www.apple.com", "8.8.8.8"}} {
		nodes = append(nodes, Node{ID: entry.id, Name: entry.id, Protocol: "vless", Enabled: true, Exit: "socks5", Host: "127.0.0.1", Port: 21001, RealitySNI: entry.sni, RealityIP: entry.ip})
		u.Credentials.VLESS[entry.id] = uuid()
	}
	return a, u, nodes
}

func TestRealityGroupsPreserveMultiuserRoutesAndRestrictCredentials(t *testing.T) {
	a, member, nodes := realityNodesFixture(t)
	owner := testUser(t, a, "owner", "owner")
	owner.Credentials.VLESS["custom-a"] = uuid()
	disabled := member
	disabled.ID, disabled.Enabled = 99, false
	config := xrayConfig(a.cfg, []Record{member, owner, disabled}, nodes)
	inbounds := config["inbounds"].([]object)
	if len(inbounds) != 4 { // default + two distinct SNI groups + control API
		t.Fatalf("one process should share one inbound per SNI: %d", len(inbounds))
	}
	seen := map[string]bool{}
	for _, inbound := range inbounds {
		if inbound["protocol"] != "vless" {
			continue
		}
		settings := inbound["streamSettings"].(object)["realitySettings"].(object)
		sni := settings["serverNames"].([]string)[0]
		if sni != a.cfg.RealitySNI {
			if inbound["listen"] != filepath.Join(realitySocketDir, sni+".sock")+",0660" || inbound["port"] != nil || !strings.HasSuffix(str(settings["dest"]), ":443") || strings.Contains(str(settings["dest"]), sni) {
				t.Fatal("custom target is not pinned or UDS path is inconsistent")
			}
		} else if inbound["tag"] != "vless-in" || inbound["port"] != 18443 {
			t.Fatal("default inbound changed")
		}
		for _, client := range inbound["settings"].(object)["clients"].([]object) {
			email := client["email"].(string)
			if seen[email] || idFromEmail(email) == disabled.ID {
				t.Fatal("credential crossed group or disabled user survived")
			}
			seen[email] = true
			for _, n := range nodes {
				if email == emailForTest(member.ID, n.ID) && effectiveRealitySNI(a.cfg, n) != sni {
					t.Fatal("node credential installed in wrong SNI group")
				}
			}
		}
	}
	if len(seen) != 6 { // member has default + three custom, owner default + custom-a
		t.Fatalf("wrong authorized user/node count: %d", len(seen))
	}
	rules := config["routing"].(object)["rules"].([]object)
	for _, n := range nodes {
		if n.RealitySNI == "" {
			continue
		}
		found := false
		for _, rule := range rules {
			if rule["ruleTag"] == n.ID && rule["outboundTag"] == n.ID {
				found = true
			}
		}
		if !found {
			t.Fatal("same-SNI nodes lost distinct exit routing")
		}
	}
}

func TestRealityGroupDisablingExpiryQuotaAndSNIMoveKeepAuthorization(t *testing.T) {
	a, member, nodes := realityNodesFixture(t)
	for _, change := range []func(*Record){func(r *Record) { r.Enabled = false }, func(r *Record) { r.VLESS = false }, func(r *Record) { r.Expires = time.Now().Add(-time.Second).Unix() }, func(r *Record) { r.Quota, r.Upload = 1, 1 }} {
		revoked := member
		change(&revoked)
		for _, inbound := range xrayConfig(a.cfg, []Record{revoked}, nodes)["inbounds"].([]object) {
			if inbound["protocol"] == "vless" && len(inbound["settings"].(object)["clients"].([]object)) != 0 {
				t.Fatal("revoked account retained credentials in a Reality group")
			}
		}
	}
	// Move one node to the existing Apple group without changing its identity.
	moved := append([]Node{}, nodes...)
	moved[2].RealitySNI, moved[2].RealityIP = "www.apple.com", "8.8.8.8"
	moved[3].Enabled = false
	config := xrayConfig(a.cfg, []Record{member}, moved)
	if len(config["inbounds"].([]object)) != 3 { // default, Apple, API; Microsoft now unused
		t.Fatal("disabled last node retained an active custom listener")
	}
	wantEmail, wantID := email(member.ID, moved[2].ID), member.Credentials.VLESS[moved[2].ID]
	found := false
	for _, inbound := range config["inbounds"].([]object) {
		if inbound["protocol"] != "vless" {
			continue
		}
		for _, client := range inbound["settings"].(object)["clients"].([]object) {
			if client["email"] == email(member.ID, moved[3].ID) {
				t.Fatal("disabled node retained credentials")
			}
			if client["email"] == wantEmail {
				found = client["id"] == wantID && inbound["tag"] == realityGroupTag(a.cfg, moved[2])
			}
		}
	}
	if !found {
		t.Fatal("SNI change rotated credentials or retained the old group")
	}
}

func emailForTest(id int64, node string) string { return email(id, node) }

func TestRealitySubscriptionURIMihomoAndCardsAgree(t *testing.T) {
	a, member, nodes := realityNodesFixture(t)
	entries := subscriptionEntries(a.cfg, member, nodes, "vless")
	for _, entry := range entries {
		uri, err := url.Parse(entry.uri)
		if err != nil || uri.Query().Get("sni") != entry.proxy["servername"] {
			t.Fatal("URI and Mihomo differ")
		}
		for _, n := range nodes {
			if n.ID == entry.node.ID && uri.Query().Get("sni") != effectiveRealitySNI(a.cfg, n) {
				t.Fatal("card and core SNI differ")
			}
		}
	}
	encoded, _, err := subscription(a.cfg, member, nodes, "mihomo", "vless")
	var doc struct {
		Proxies []object `yaml:"proxies"`
	}
	if err != nil || yaml.Unmarshal(encoded, &doc) != nil || len(doc.Proxies) != 4 {
		t.Fatal("custom SNI changed subscription membership")
	}
}

func TestRealityRejectsCorruptPersistedTargetsAndMasksInternalIP(t *testing.T) {
	a, _, nodes := realityNodesFixture(t)
	if err := validateRealityNodes(a.cfg, nodes); err != nil {
		t.Fatal(err)
	}
	n := nodes[len(nodes)-1]
	if n.public().RealitySNI != n.RealitySNI || n.public().RealityIP != "" || memberNodeState(n).RealitySNI != "" || resourceFromNode(n).RealitySNI != "" {
		t.Fatal("ingress configuration leaked across member/pool views")
	}
	for _, ip := range []string{"127.0.0.1", "", "8.8.8.8:443"} {
		bad := append([]Node{}, nodes...)
		bad[len(bad)-1].RealityIP = ip
		if validateRealityNodes(a.cfg, bad) == nil {
			t.Fatal("unverified persisted target accepted")
		}
	}
	bad := append([]Node{}, nodes...)
	bad[len(bad)-2].RealityIP = "9.9.9.9"
	if validateRealityNodes(a.cfg, bad) == nil {
		t.Fatal("same-SNI targets diverged")
	}
}

func TestRealityCustomInboundRequiresEffectiveUserReadback(t *testing.T) {
	a, member, nodes := realityNodesFixture(t)
	member.Credentials.VLESS = map[string]string{"custom-c": uuid()}
	script, err := os.ReadFile("testdata/xray-noop.sh")
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "#!/bin/sh", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$(dirname \"$0\")/calls\"", 1))
	a.cfg.Xray = filepath.Join(t.TempDir(), "xray")
	if err = atomicWrite(a.cfg.Xray, script, 0700); err != nil {
		t.Fatal(err)
	}
	err = a.reconcileRealityUsers(xrayConfig(a.cfg, []Record{member}, nodes), "unused.json", "test-generation")
	if err == nil || !strings.Contains(err.Error(), "verification mismatch") {
		t.Fatalf("custom inbound zero-exit RPC failure accepted: %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(filepath.Dir(a.cfg.Xray), "calls"))
	tag := realityGroupTag(a.cfg, nodes[len(nodes)-1])
	if strings.Count(string(calls), "-tag="+tag) != 2 || a.lastUsers != "" {
		t.Fatal("custom inbound lacked independent readback")
	}
}

func TestRealityAPIRejectsInvalidSNIWithoutMutatingNode(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	before, _ := a.store.nodes()
	for _, n := range []Node{{ID: "hy2-main", Protocol: "hy2", Enabled: true, Exit: "direct", RealitySNI: "www.microsoft.com"}, {ID: "vless-main", Protocol: "vless", Enabled: true, Exit: "direct", RealitySNI: "https://www.microsoft.com"}, {ID: "vless-main", Protocol: "vless", Enabled: true, Exit: "direct", RealitySNI: "panel.test"}} {
		if w := req(t, a, owner, "POST", "/api/nodes", n); w.Code != 400 {
			t.Fatalf("invalid SNI API response: %d", w.Code)
		}
	}
	after, _ := a.store.nodes()
	b, _ := json.Marshal(before)
	c, _ := json.Marshal(after)
	if string(b) != string(c) {
		t.Fatal("failed SNI validation changed saved node")
	}
}
