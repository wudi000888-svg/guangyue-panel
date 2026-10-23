package controlplane

import (
	"net/netip"
	"slices"
	"testing"
)

// Member tunnels must not reach loopback (Xray API, panel), private networks
// or cloud metadata, whatever exit a node uses.
func TestCoreConfigsBlockPrivateEgress(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	for _, mode := range []string{"direct", "http", "socks5"} {
		n := Node{ID: "vless-main", Name: "Node", Protocol: "vless", Enabled: true, Exit: mode, Host: "203.0.113.5", Port: 1080}
		xc := xrayConfig(a.cfg, []Record{u}, []Node{n})
		routing := xc["routing"].(object)
		if routing["domainStrategy"] != "IPOnDemand" {
			t.Fatalf("%s: domains are not resolved before IP rules", mode)
		}
		rules := routing["rules"].([]object)
		blockIP, firstUser := -1, -1
		for i, rule := range rules {
			if _, ok := rule["user"]; ok && firstUser < 0 {
				firstUser = i
			}
			if ips, ok := rule["ip"].([]string); ok && rule["outboundTag"] == "block" && slices.Contains(ips, "127.0.0.0/8") && blockIP < 0 {
				blockIP = i
			}
		}
		if blockIP < 0 || firstUser < 0 || blockIP > firstUser {
			t.Fatalf("%s: private egress block must precede user routes (block=%d user=%d)", mode, blockIP, firstUser)
		}

		n.Protocol = "hy2"
		hc := hy2Config(a.cfg, []Node{n})
		acl, ok := hc["acl"].(object)["inline"].([]string)
		if !ok || len(acl) == 0 || acl[len(acl)-1] != "exit(all)" {
			t.Fatalf("%s: HY2 ACL missing or not terminated by exit(all): %v", mode, acl)
		}
		for _, want := range []string{"reject(127.0.0.0/8)", "reject(169.254.0.0/16)", "reject(::1/128)", "reject(localhost)"} {
			if !slices.Contains(acl, want) {
				t.Fatalf("%s: HY2 ACL lacks %s", mode, want)
			}
		}
	}
}

func TestBlockedEgressCoversSensitiveAddresses(t *testing.T) {
	prefixes := []netip.Prefix{}
	for _, cidr := range blockedEgress {
		p := netip.MustParsePrefix(cidr)
		if p.Addr().Is4In6() {
			t.Fatalf("Xray rejects IPv4-mapped prefixes: %s", cidr)
		}
		prefixes = append(prefixes, p)
	}
	covered := func(s string) bool {
		// Both cores match IPv4-mapped IPv6 destinations as IPv4.
		addr := netip.MustParseAddr(s).Unmap()
		for _, p := range prefixes {
			if p.Contains(addr) {
				return true
			}
		}
		return false
	}
	for _, s := range []string{"127.0.0.1", "127.255.0.9", "10.0.0.1", "172.16.5.4", "192.168.1.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "::1", "fd00::1", "fe80::1", "::ffff:127.0.0.1"} {
		if !covered(s) {
			t.Errorf("%s is reachable", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "203.0.113.5", "2606:4700:4700::1111"} {
		if covered(s) {
			t.Errorf("public address %s is blocked", s)
		}
	}
}
