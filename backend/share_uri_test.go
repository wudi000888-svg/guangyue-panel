package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleUUID = "11111111-1111-4111-8111-111111111111"

func parsedLink(t *testing.T, line string) importItem {
	t.Helper()
	items, w, e := parseSubscription([]byte(line))
	if e != nil || len(w) > 0 || len(items) != 1 {
		t.Fatalf("expected one valid node: error=%v, warnings=%v", e, w)
	}
	return items[0]
}
func TestShadowrocketVLESSRealityMatchesOrdinaryURI(t *testing.T) {
	sr := "vless://" + encodeBase64([]byte("none:"+sampleUUID+"@edge.example:443")) + "?remarks=Example&tls=1&peer=www.example.com&udp=1&xtls=2&pbk=sample-public-key&sid=0123456789abcdef"
	standard := "vless://" + sampleUUID + "@edge.example:443?security=reality&headerType=none&encryption=none&sni=www.example.com&fp=chrome&type=tcp&flow=xtls-rprx-vision&pbk=sample-public-key&sid=0123456789abcdef#Example"
	first, second := parsedLink(t, sr), parsedLink(t, standard)
	if first.Label != "Example" || upstreamKey(first.Proxy) != upstreamKey(second.Proxy) {
		t.Fatal("Shadowrocket changed credentials, SNI, Reality or Vision options")
	}
	for _, raw := range []string{sr, encodeBase64([]byte(sr)), strings.ReplaceAll(sr, "vless://", `vless\://`), "[导出节点](" + sr + ")"} {
		if upstreamKey(parsedLink(t, raw).Proxy) != upstreamKey(first.Proxy) {
			t.Fatal("escaped/wrapped node changed")
		}
	}
	malformed := strings.Replace(sr, "peer=www.example.com&udp=1&xtls=2&pbk=sample-public-key&sid=0123456789abcdef", `peer=[www.example.com&udp=1&xtls=2&pbk=sample-public-key&sid=0123456789abcdef](http://www.example.com\&udp=1\&xtls=2\&pbk=sample-public-key\&sid=0123456789abcdef)`, 1)
	if upstreamKey(parsedLink(t, malformed).Proxy) != upstreamKey(first.Proxy) {
		t.Fatal("rich text peer link changed connection")
	}
}
func TestShadowrocketPlainTLSAndTransports(t *testing.T) {
	for _, tc := range []struct {
		name, tail, network string
		tls                 bool
	}{
		{"direct", "tls=0&udp=0", "tcp", false},
		{"tls", "tls=1&peer=tls.example&xtls=0", "tcp", true},
		{"websocket", "tls=1&peer=tls.example&obfs=websocket&obfsParam=cdn.example&path=%2Fsocket", "ws", true},
		{"grpc", "tls=1&peer=tls.example&obfs=grpc&path=my-service", "grpc", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parsedLink(t, "vless://"+encodeBase64([]byte("none:"+sampleUUID+"@[2001:db8::1]:443"))+"?remarks=普通 节点&"+tc.tail).Proxy
			if p["tls"] != tc.tls || p["network"] != tc.network || p["server"] != "2001:db8::1" || p["uuid"] != sampleUUID {
				t.Fatal("incorrect plain/TLS transport conversion")
			}
			if _, ok := p["reality-opts"]; ok {
				t.Fatal("invented Reality on ordinary TLS node")
			}
			if tc.name == "direct" && p["udp"] != false {
				t.Fatal("ignored UDP disable")
			}
			if tc.network == "grpc" && p["grpc-opts"].(map[string]any)["grpc-service-name"] != "my-service" {
				t.Fatal("missing gRPC service name")
			}
		})
	}
}
func TestOtherSingleLinkFormatsRemainSupported(t *testing.T) {
	vm := object{"v": "2", "ps": "legacy VMess", "add": "edge.example", "port": "443", "id": sampleUUID, "aid": "0", "scy": "auto", "net": "ws", "type": "none", "tls": "tls", "sni": "tls.example", "host": "cdn.example", "path": "/ws"}
	b, _ := json.Marshal(vm)
	for _, tc := range []struct{ link, kind string }{
		{"vmess://" + encodeBase64(b) + "#VMess", "vmess"},
		{"vmess://" + encodeBase64([]byte("auto:"+sampleUUID+"@edge.example:443")) + "?remarks=VMess&tls=1&peer=tls.example&obfs=websocket&obfsParam=cdn.example&path=%2Fws", "vmess"},
		{"ss://" + encodeBase64([]byte("aes-128-gcm:secret@edge.example:8443")) + "?remarks=SS&udp=1", "ss"},
		{"ss://" + encodeBase64([]byte("aes-128-gcm:secret")) + "@edge.example:8443#SS", "ss"},
		{"ss://aes-128-gcm:secret@edge.example:8443#SS", "ss"},
		{"trojan://secret@edge.example:443?peer=tls.example&remarks=Trojan", "trojan"},
		{"hy2://secret@edge.example:443?sni=tls.example&insecure=0#HY2", "hysteria2"},
		{"socks5://edge.example:1080#SOCKS5", "socks5"},
		{"http://edge.example:8080#HTTP", "http"},
	} {
		if parsedLink(t, tc.link).Proxy["type"] != tc.kind {
			t.Fatal("single link protocol changed")
		}
	}
}
func TestShareLinkRejectsLossyOrConflictingSettings(t *testing.T) {
	sr := "vless://" + encodeBase64([]byte("none:"+sampleUUID+"@edge.example:443")) + "?"
	for _, tail := range []string{"tls=0&pbk=key", "tls=1&security=none", "tls=1&xtls=1", "tls=1&xtls=2&flow=other", "headerType=http", "tls=maybe", "udp=2", "tls=1&tls=0", "allowInsecure=1&insecure=0", "security=reality", "security=none&pbk=key", "type=xhttp"} {
		if _, _, e := parseSubscription([]byte(sr + tail)); e == nil {
			t.Fatalf("incompatible settings accepted: %s", tail)
		}
	}
	if _, _, e := parseSubscription([]byte(sr + "tls=1&verify_cert=0")); e == nil {
		t.Fatal("protocol-specific option silently ignored")
	}
}
func TestSingleURIWorksInDefaultLinkInputAndDeduplicates(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	sr := "vless://" + encodeBase64([]byte("none:"+sampleUUID+"@edge.example:443")) + "?tls=1&peer=tls.example&remarks=单节点"
	w := req(t, a, owner, "POST", "/api/ips/import", object{"url": sr})
	if w.Code != 200 {
		t.Fatal("default link input rejected single URI")
	}
	if strings.Contains(w.Body.String(), sampleUUID) {
		t.Fatal("public import response leaked UUID")
	}
	plain := "vless://" + sampleUUID + "@edge.example:443?security=tls&sni=tls.example&type=tcp#另一个名称"
	w = req(t, a, owner, "POST", "/api/ips/import", object{"content": plain})
	var result struct{ Added, Duplicates int }
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Added != 0 || result.Duplicates != 1 {
		t.Fatal("equivalent Shadowrocket/ordinary links did not deduplicate")
	}
}
func TestPrivateUserSubscriptionFixture(t *testing.T) {
	dir := os.Getenv("GY_SHARE_FIXTURES")
	if dir == "" {
		t.Skip("private supplied subscription is optional outside acceptance")
	}
	b, e := os.ReadFile(filepath.Join(dir, "subscription.raw"))
	if e != nil {
		t.Fatal("private fixture unavailable")
	}
	items, w, e := parseSubscription(b)
	if e != nil || len(items) != 4 || len(w) != 0 {
		t.Fatalf("user subscription expected 4 entries; count=%d warnings=%v error=%v", len(items), w, e)
	}
	b, e = os.ReadFile(filepath.Join(dir, "input.json"))
	if e != nil {
		t.Fatal("private input unavailable")
	}
	var input struct{ Single string }
	if json.Unmarshal(b, &input) != nil {
		t.Fatal("private input invalid")
	}
	p := parsedLink(t, input.Single).Proxy
	if p["type"] != "vless" || p["flow"] != "xtls-rprx-vision" || p["tls"] != true || p["reality-opts"] == nil {
		t.Fatal("user single node lost required Reality settings")
	}
}
