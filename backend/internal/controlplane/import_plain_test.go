package controlplane

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPlainImportPreservesCredentialsAndPhysicalLines(t *testing.T) {
	password := `secret:with\_escapes\&amp;[link](https://example.com) `
	content := "\ufeff\r\n# export\r\n203.0.113.7:8080:private-user:" + password + "\r\n\r\n203.0.113.8:wrong:private-user:private-error-secret\r\n[2001:db8::1]:1080:user:password\r\n"
	for _, protocol := range []string{"", "http", "socks5"} {
		items, warnings, err := parseImportContent([]byte(content), protocol)
		if err != nil || len(items) != 2 || len(warnings) != 1 || warnings[0].Index != 5 {
			t.Fatal("physical line parsing failed")
		}
		if !items[0].Native || items[0].Proxy["password"] != password || items[0].Proxy["username"] != "private-user" || items[1].Proxy["server"] != "2001:db8::1" {
			t.Fatal("bare export credentials changed")
		}
		if strings.Contains(warnings[0].Reason, "private-") {
			t.Fatal("diagnostic leaked credentials")
		}
		want := protocol
		if want == "" {
			want = "http"
		}
		if items[0].Proxy["type"] != want {
			t.Fatal("plain protocol selection ignored")
		}
	}
}

func TestPlainImportLimitsAndMalformedRows(t *testing.T) {
	for _, line := range []string{"not a proxy", "203.0.113.1:0:u:p", "203.0.113.1:65536:u:p", "203.0.113.1:8080::secret", "203.0.113.1:8080:u:", "host.example:8080:u:p", "203.0.113.1:8080:u:bad\x00secret", "203.0.113.1:8080:" + strings.Repeat("u", 129) + ":p"} {
		items, warnings, err := parseImportContent([]byte("\n# header\n"+line), "http")
		if err == nil || len(items) != 0 || len(warnings) != 1 || warnings[0].Index != 3 || strings.Contains(warnings[0].Reason, "secret") {
			t.Fatal("invalid credential row was not rejected safely")
		}
	}
	valid := "203.0.113.1:8080:u:p\n"
	if _, _, err := parseImportContent([]byte(strings.Repeat(valid, 257)), "http"); err == nil {
		t.Fatal("row bound ignored")
	}
	if items, _, err := parseImportContent([]byte(strings.Repeat("# comment\n\n"+valid, 256)), "http"); err != nil || len(items) != 256 {
		t.Fatal("comments counted as proxy candidates")
	}
	if _, _, err := parseImportContent(bytes.Repeat([]byte{'x'}, (4<<20)+1), "http"); err == nil {
		t.Fatal("byte bound ignored")
	}
	if _, _, err := parseImportContent([]byte(valid), "trojan"); err == nil {
		t.Fatal("invalid plain protocol accepted")
	}
}

func TestPlainImportUnauthenticatedAndBracketIPv6MarkdownPassword(t *testing.T) {
	content := "[2001:db8::1]:1080:u:keep-[link](https://example.com)\n203.0.113.7:8080\n[2001:db8::2]:1080\n"
	for _, protocol := range []string{"http", "socks5"} {
		items, warnings, err := parseImportContent([]byte(content), protocol)
		if err != nil || len(items) != 3 || len(warnings) != 0 {
			t.Fatal("bare IPv6 or unauthenticated import failed")
		}
		if items[0].Proxy["password"] != "keep-[link](https://example.com)" {
			t.Fatal("IPv6 password was interpreted as Markdown")
		}
		for _, item := range items[1:] {
			if !item.Native || item.Proxy["type"] != protocol || item.Proxy["username"] != "" || item.Proxy["password"] != "" {
				t.Fatal("unauthenticated export gained credentials or bridge")
			}
		}
	}
}

func TestPlainImportKeepsURIBase64AndYAMLBehavior(t *testing.T) {
	uri := "hy2://fixture-secret@edge.example:443#Example"
	for _, text := range []string{uri, encodeBase64([]byte(uri)), "```\n" + uri + "\n```", "<" + uri + ">", "proxies:\n - name: fixture\n   type: http\n   server: edge.example\n   port: 8080\n   username: fixture\n   password: fixture\n"} {
		items, warnings, err := parseImportContent([]byte(text), "socks5")
		if err != nil || len(items) != 1 || len(warnings) != 0 || items[0].Native {
			t.Fatal("existing subscription representation changed")
		}
	}
	items, warnings, err := parseImportContent([]byte("203.0.113.1:8080:u:p\n"+uri), "http")
	if err != nil || len(items) != 2 || len(warnings) != 0 || !items[0].Native || items[1].Native {
		t.Fatal("mixed native and URI import lost its routing type")
	}
}

func TestPlainImportNativePersistenceAndDedup(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	manual := resourceFromNode(Node{Protocol: "vless", Exit: "http", Host: "203.0.113.1", Port: 8080, Username: "u", Password: "old-secret"})
	if err := a.store.savePoolNodes([]IPResource{manual}, nil); err != nil {
		t.Fatal(err)
	}
	content := "203.0.113.1:8080:u:old-secret\n203.0.113.2:8080:u:new-secret\n203.0.113.2:8080:u:new-secret\n"
	input := object{"content": content, "plain_protocol": "http"}
	if req(t, a, member, "POST", "/api/ips/import", input).Code != 403 {
		t.Fatal("member imported exits")
	}
	// Prove pure native import does not invoke the unavailable bridge binary.
	a.cfg.Dev = false
	a.cfg.Mihomo = "/nonexistent-mihomo-fixture"
	w := req(t, a, owner, "POST", "/api/ips/import", input)
	var result struct{ Added, Duplicates int }
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || result.Added != 1 || result.Duplicates != 2 {
		t.Fatal("native import or existing/file dedup failed")
	}
	if strings.Contains(w.Body.String(), "-secret") {
		t.Fatal("response leaked a password")
	}
	pools, err := a.store.pools()
	if err != nil || len(pools) != 2 {
		t.Fatal("unexpected pool size")
	}
	for _, p := range pools {
		if p.Exit != "http" || p.Upstream != nil || p.BridgePort != 0 || p.BridgePassword != "" || p.SubscriptionID != "" || p.PoolGroup == "public" {
			t.Fatal("bare export allocated a subscription bridge or public source")
		}
		var stored []byte
		if err := a.store.db.QueryRow("SELECT doc FROM ip_pool WHERE id=?", p.ID).Scan(&stored); err != nil || bytes.Contains(stored, []byte("-secret")) {
			t.Fatal("credential persistence was not encrypted")
		}
	}
	w = req(t, a, owner, "POST", "/api/ips/import", input)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Added != 0 || result.Duplicates != 3 {
		t.Fatal("repeat file did not deduplicate")
	}
	w = req(t, a, owner, "POST", "/api/ips/import", object{"content": "203.0.113.2:8080:u:new-secret", "plain_protocol": "socks5"})
	if w.Code != 200 {
		t.Fatal("SOCKS5 native import failed")
	}
	a.cfg.Dev = true
	pools, _ = a.store.pools()
	for _, p := range pools {
		if p.Exit == "socks5" {
			for _, protocol := range []string{"vless", "hy2"} {
				n := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", object{"protocol": protocol, "enabled": true, "exit_id": p.ID}))
				if n.Exit != "socks5" || n.Host != p.Host || n.Upstream != nil || n.BridgePort != 0 {
					t.Fatal("native binding was not direct to the chosen proxy")
				}
			}
		}
	}
}

func TestPrivateWebshareFixture(t *testing.T) {
	path := os.Getenv("GY_WEBSHARE_FIXTURE")
	if path == "" {
		t.Skip("private supplied file only during acceptance")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private file unavailable")
	}
	items, warnings, err := parseImportContent(body, "http")
	if err != nil || len(items) != 10 || len(warnings) != 0 {
		t.Fatal("private file did not yield 10 valid exports")
	}
	for _, item := range items {
		if !item.Native || item.Proxy["type"] != "http" {
			t.Fatal("private file did not produce native HTTP exits")
		}
	}
}
