package controlplane

import (
	"bytes"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
	"io"
	"net/http"
	"testing"
)

func TestNetworkOptimizationAccessAndValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	calls := 0
	a.updateClient = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/network" {
			t.Fatal("unbounded helper route")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{"available":true}`)), Header: make(http.Header)}, nil
	})}
	for _, actor := range []Record{{}, member} {
		if w := req(t, a, actor, "POST", "/api/network-settings", object{"bbr": true, "hy2": true, "revision": "initial"}); w.Code != 401 && w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	for _, body := range []object{{"bbr": true, "revision": "initial"}, {"bbr": 1, "hy2": true, "revision": "initial"}, {"bbr": true, "hy2": true, "revision": "initial", "command": "id"}} {
		if w := req(t, a, owner, "POST", "/api/network-settings", body); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input forwarded")
	}
	if w := req(t, a, owner, "POST", "/api/network-settings", object{"bbr": true, "hy2": false, "revision": "initial"}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, scope := range []string{"read", "manage"} {
		if httpapi.AllowedGateway("POST", "/api/network-settings", scope) {
			t.Fatal("remote root access")
		}
	}
}

func TestHYOptimizationPreservesRoutingAndChangesReconciliationHash(t *testing.T) {
	nodes := []Node{{ID: "one", Protocol: "hy2", Enabled: true, Exit: "direct"}}
	standard := hy2Config(Config{}, nodes)
	optimized := hy2Config(Config{HY2Optimized: true}, nodes)
	if _, ok := standard["ignoreClientBandwidth"]; ok {
		t.Fatal("disabled mode overrides upstream default")
	}
	if optimized["ignoreClientBandwidth"] != true || optimized["congestion"].(object)["type"] != "bbr" {
		t.Fatal("missing adaptive BBR")
	}
	if _, ok := optimized["bandwidth"]; ok {
		t.Fatal("arbitrary rate cap")
	}
	if len(optimized["nodeOutbounds"].(object)) != 1 {
		t.Fatal("lost node routing")
	}
	if hy2NodeHash(nodes, false) == hy2NodeHash(nodes, true) {
		t.Fatal("changing profile would not restart core")
	}
	if hy2NodeHash(nodes, false) != nodeHash(nodes, "hy2") {
		t.Fatal("unoptimized upgrade unnecessarily restarts core")
	}
}

func TestNetworkWaitsForLegacyHelperWithoutKeepingItAlive(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	a.updateClient = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(bytes.NewBufferString("old helper")), Header: make(http.Header)}, nil
	})}
	result := decoded[object](t, req(t, a, owner, "GET", "/api/network-settings", nil), 200)
	if result["available"] != false || result["retry_after"] != float64(130) {
		t.Fatal("must allow the old helper's 120-second idle shutdown")
	}
}
