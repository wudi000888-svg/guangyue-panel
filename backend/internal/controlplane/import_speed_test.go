package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubscriptionFormatsAndSecrets(t *testing.T) {
	raw := "vless://11111111-1111-4111-8111-111111111111@edge.example:443?security=reality&sni=www.example.com&fp=chrome&pbk=public&sid=abcd&type=tcp&flow=xtls-rprx-vision#Example\nhy2://secret-password@edge.example:443?sni=edge.example#HY2\nss://" + encodeBase64([]byte("aes-128-gcm:secret-ss")) + "@edge.example:8443#SS"
	for _, b := range [][]byte{[]byte(raw), []byte(encodeBase64([]byte(raw)))} {
		items, warnings, e := parseSubscription(b)
		if e != nil || len(items) != 3 || len(warnings) != 0 {
			t.Fatalf("raw/base64 parse: %v %v", e, warnings)
		}
		if items[1].Proxy["type"] != "hysteria2" || items[2].Proxy["password"] != "secret-ss" {
			t.Fatal("credential or protocol changed")
		}
	}
	items, warnings, e := parseSubscription([]byte("proxies:\n - name: test\n   type: vless\n   server: edge.example\n   port: 443\n   uuid: 11111111-1111-4111-8111-111111111111\n   tls: true\n   network: ws\n   ws-opts:\n     path: /ws\n     headers: {Host: edge.example}\nrules: [MATCH,DIRECT]\n"))
	if e != nil || len(items) != 1 || len(warnings) != 0 {
		t.Fatalf("YAML parsing: %v %v", e, warnings)
	}
}
func TestSubscriptionRejectsUnsafeAndUnsupportedOptions(t *testing.T) {
	for _, doc := range []string{
		"proxies: [{type: ss, server: edge.example, port: 443, password: x, cipher: aes-128-gcm, dialer-proxy: DIRECT}]",
		"proxies: [{type: trojan, server: edge.example, port: 443, password: x, certificate: /etc/passwd}]",
		"proxies: [{type: ss, server: edge.example, port: 443, password: x, cipher: aes-128-gcm, plugin: obfs-local}]",
		"vless://user@edge.example:443?type=xhttp", "tuic://secret@edge.example:443",
	} {
		items, warnings, e := parseSubscription([]byte(doc))
		if e == nil || len(items) > 0 || len(warnings) == 0 {
			t.Fatal("unsafe or unsupported entry accepted")
		}
	}
	if _, _, e := parseSubscription(bytes.Repeat([]byte{'a'}, (4<<20)+1)); e == nil {
		t.Fatal("unbounded input")
	}
	for _, u := range []string{"http://example.com/sub", "https://127.0.0.1/sub", "https://user:pass@example.com/sub", "https://[::1]/sub"} {
		if _, e := fetchSubscription(context.Background(), u); e == nil {
			t.Fatal("unsafe fetch allowed")
		}
	}
}
func TestImportDedupBindingAndEncryptedPersistence(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	input := object{"content": "hy2://private-airport-secret@edge.example:443#Airport"}
	if req(t, a, member, "POST", "/api/ips/import", input).Code != 403 {
		t.Fatal("member import allowed")
	}
	w := req(t, a, owner, "POST", "/api/ips/import", input)
	if w.Code != 200 {
		t.Fatalf("import: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-airport-secret") || strings.Contains(w.Body.String(), "bridge_password") {
		t.Fatal("secret leak")
	}
	pools, _ := a.store.pools()
	if len(pools) != 1 || pools[0].Exit != "subscription" {
		t.Fatal("not imported")
	}
	p := pools[0]
	var stored []byte
	_ = a.store.db.QueryRow("SELECT doc FROM ip_pool WHERE id=?", p.ID).Scan(&stored)
	if bytes.Contains(stored, []byte("private-airport-secret")) {
		t.Fatal("plaintext credential")
	}
	w = req(t, a, owner, "POST", "/api/ips/import", input)
	var result struct{ Added, Duplicates int }
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.Added != 0 || result.Duplicates != 1 {
		t.Fatal("duplicate added")
	}
	for _, protocol := range []string{"vless", "hy2"} {
		n := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", object{"protocol": protocol, "enabled": true, "exit_id": p.ID}))
		if n.ExitID != p.ID || n.Exit != "subscription" || n.Upstream != nil || n.BridgePassword != "" {
			t.Fatal("binding or public filtering failed")
		}
	}
	nodes, _ := a.store.nodes()
	hc := hy2Config(a.cfg, nodes)
	b, _ := json.Marshal(hc)
	if !bytes.Contains(b, []byte("127.0.0.1:19186")) {
		t.Fatal("HY2 did not route to DNS gateway before subscription bridge")
	}
	cfg := bridgeConfig(pools, "controller-secret")
	b, _ = json.Marshal(cfg)
	if !bytes.Contains(b, []byte("MATCH,REJECT")) || !bytes.Contains(b, []byte("private-airport-secret")) {
		t.Fatal("bridge missing fail-closed route")
	}
	if req(t, a, owner, "DELETE", "/api/ips/"+p.ID, nil).Code != 200 {
		t.Fatal("independent imported resource did not cascade")
	}
	if e := a.store.migratePools(); e != nil {
		t.Fatal(e)
	}
}
func TestSpeedMetadataDoesNotRestartCoreOrSurviveExitChange(t *testing.T) {
	n := Node{ID: "n", Exit: "direct", Protocol: "hy2", Enabled: true}
	before := nodeHash([]Node{n}, "hy2")
	n.Speed = &SpeedResult{At: 10, Mbps: 1}
	if nodeHash([]Node{n}, "hy2") != before {
		t.Fatal("speed restarts core")
	}
	p := resourceFromNode(Node{Exit: "http", Protocol: "vless", Host: "edge.example", Port: 80})
	if bindPool(n, p).Speed != nil {
		t.Fatal("stale speed survives route change")
	}
}
func TestSpeedMeasuresBoundedBodyAndRejectsErrorPage(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/error" {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
		_, _ = io.Copy(w, bytes.NewReader(make([]byte, 512<<10)))
	}))
	defer server.Close()
	result := measureSpeed(context.Background(), http.DefaultTransport, server.URL, 128<<10)
	if result.Error != "" || result.Bytes != 128<<10 || result.Mbps <= 0 || hits != 1 {
		t.Fatalf("measurement invalid: %+v", result)
	}
	result = measureSpeed(context.Background(), http.DefaultTransport, server.URL+"/error", 128<<10)
	if result.Error == "" || result.Mbps != 0 {
		t.Fatal("error page measured as speed")
	}
}
func TestSpeedAuthorizationAndSingleFlight(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	for _, path := range []string{"/api/nodes/hy2-main/speed", "/api/ips/test/speed"} {
		if req(t, a, member, "POST", path, object{}).Code != 403 {
			t.Fatal("member speed allowed")
		}
	}
	a.speedMu.Lock()
	w := req(t, a, owner, "POST", "/api/nodes/hy2-main/speed", object{})
	a.speedMu.Unlock()
	if w.Code != 409 {
		t.Fatal("concurrent speed allowed")
	}
	n := Node{ID: "hy2-main", Protocol: "hy2", Exit: "direct", Enabled: false}
	_ = a.store.saveNode(n)
	if req(t, a, owner, "POST", "/api/nodes/hy2-main/speed", object{}).Code != 400 {
		t.Fatal("disabled node tested")
	}
}
