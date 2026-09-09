package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memberQualityResponse struct {
	Pool     string              `json:"pool"`
	Active   bool                `json:"active"`
	ReadOnly bool                `json:"readonly"`
	Nodes    []MemberNodeQuality `json:"nodes"`
}

func memberQualityFixture(t *testing.T) (*App, Record, Record) {
	t.Helper()
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	nodes, _ := a.store.nodes()
	for _, n := range nodes {
		n.Name = n.ID
		n.ProbeIP = "8.8.8.8"
		n.Country = "美国"
		n.CountryCode = "US"
		n.CheckedAt = 100
		n.Exit = "subscription"
		n.ExitID = "private-pool-identifier"
		n.Host = "secret-origin.example"
		n.Port = 8765
		n.Username = "secret-upstream-user"
		n.Password = "secret-upstream-password"
		n.UpstreamType = "hidden-upstream-type"
		n.Upstream = object{"password": "secret-upstream-inner"}
		n.BridgePassword = "secret-bridge-password"
		n.BridgePort = 21099
		n.ProbeError = "secret-probe-error"
		n.Speed = &SpeedResult{Error: "secret-speed-error"}
		n.Quality = &IPQuality{SchemaVersion: 4, At: 99, IP: "8.8.8.8", CountryCode: "US", Tags: []QualityTag{{Key: "network", Label: "数据中心 IP", Status: "confirmed"}}, Streams: []QualityTag{{Key: "netflix", Label: "Netflix · 页面可达", Status: "page", Source: "https://www.netflix.com/title/81280792?token=secret-link-token#private-fragment"}}}
		if err := a.store.saveNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []Node{{ID: "ungranted-vless", Name: "ungranted-vless", Protocol: "vless", Enabled: true}, {ID: "disabled-hy2", Name: "disabled-hy2", Protocol: "hy2", Enabled: false}, {ID: "public-hy2", Name: "public-hy2", Protocol: "hy2", Enabled: true, ManagedBy: publicManager}, {ID: "public-vless", Name: "public-vless", Protocol: "vless", Enabled: true, ManagedBy: publicManager}, {ID: "unknown-protocol", Name: "unknown-protocol", Protocol: "other", Enabled: true}} {
		if err := a.store.saveNode(n); err != nil {
			t.Fatal(err)
		}
	}
	member.Credentials.VLESS["public-vless"] = uuid()
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	return a, owner, member
}
func readMemberQuality(t *testing.T, a *App, actor Record, path string) memberQualityResponse {
	t.Helper()
	w := req(t, a, actor, "GET", path, nil)
	if w.Code != 200 {
		t.Fatalf("quality response %d: %s", w.Code, w.Body.String())
	}
	var result memberQualityResponse
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid quality response")
	}
	return result
}
func TestMemberQualityScopesMatchAuthorizedSubscriptions(t *testing.T) {
	a, _, member := memberQualityFixture(t)
	for _, item := range []struct {
		pool, protocol string
		ids            []string
	}{{"private", "", []string{"hy2-main", "vless-main"}}, {"public", "", []string{"public-hy2", "public-vless"}}, {"private", "vless", []string{"vless-main"}}, {"public", "hy2", []string{"public-hy2"}}} {
		response := readMemberQuality(t, a, member, "/api/node-quality?pool="+item.pool+"&protocol="+item.protocol)
		if !response.Active || !response.ReadOnly || response.Pool != item.pool || len(response.Nodes) != len(item.ids) {
			t.Fatalf("wrong scoped response: %+v", response)
		}
		want := map[string]bool{}
		for _, id := range item.ids {
			want[id] = true
		}
		for _, node := range response.Nodes {
			if !want[node.ID] {
				t.Fatalf("unauthorized node %+v", node)
			}
		}
	}
	member.HY2 = false
	member.Credentials.VLESS = map[string]string{}
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	if len(readMemberQuality(t, a, member, "/api/node-quality").Nodes) != 0 {
		t.Fatal("missing credential or disabled protocol still authorized")
	}
}
func TestMemberQualityDeniesMutationAndOtherUserSelection(t *testing.T) {
	a, owner, member := memberQualityFixture(t)
	for _, path := range []string{"/api/node-quality?user_id=1", "/api/node-quality?user_id=999", "/api/nodes/vless-main/quality", "/api/ips", "/api/import-sources", "/api/public-pool"} {
		if req(t, a, member, "GET", path, nil).Code != 403 {
			t.Fatalf("owner data endpoint accessible: %s", path)
		}
	}
	for _, path := range []string{"/api/node-quality", "/api/nodes/vless-main/quality", "/api/ips/private-pool-identifier/quality", "/api/nodes/vless-main/speed"} {
		if req(t, a, member, "POST", path, object{}).Code != 403 {
			t.Fatalf("member can start expensive work: %s", path)
		}
	}
	if req(t, a, Record{}, "GET", "/api/node-quality", nil).Code != 401 {
		t.Fatal("anonymous quality access")
	}
	for _, path := range []string{"/api/node-quality?pool=all", "/api/node-quality?protocol=all", "/api/node-quality?node_id=vless-main", "/api/node-quality?pool=private&pool=public"} {
		if req(t, a, member, "GET", path, nil).Code != 400 {
			t.Fatalf("unsupported query accepted: %s", path)
		}
	}
	if req(t, a, owner, "GET", "/api/node-quality?user_id=2", nil).Code != 403 {
		t.Fatal("self-service endpoint supports hidden user impersonation")
	}
}
func TestMemberQualityUsesSavedReportsWithoutHeavyWorkOrCredentials(t *testing.T) {
	a, _, member := memberQualityFixture(t)
	other := testUser(t, a, "other-private-member", "user")
	a.syncError = "secret-upstream-password"
	a.trafficError = "secret-upstream-inner"
	beforeNodes, _ := a.store.nodes()
	beforeJSON, _ := json.Marshal(beforeNodes)
	beforeBudget := a.store.meta(proxyCheckBudgetKey)
	beforeGeneration := a.store.meta("desired_generation")
	a.heavyMu.Lock()
	a.qualityMu.Lock()
	// An owner quality request would fail the held heavy lock. A member read uses only SQLite.
	w := req(t, a, member, "GET", "/api/node-quality", nil)
	a.qualityMu.Unlock()
	a.heavyMu.Unlock()
	if w.Code != 200 {
		t.Fatal("read depended on detector lock")
	}
	for _, path := range []string{"/api/node-quality", "/api/state"} {
		w = req(t, a, member, "GET", path, nil)
		body := w.Body.String()
		for _, secret := range []string{"secret-origin.example", "secret-upstream-user", "secret-upstream-password", "secret-upstream-inner", "secret-bridge-password", "secret-probe-error", "secret-speed-error", "hidden-upstream-type", "private-pool-identifier", "secret-link-token", "private-fragment", "other-private-member", other.Credentials.Token, member.Credentials.HY2} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s leaked %s", path, secret)
			}
		}
		if !strings.Contains(body, "数据中心 IP") || !strings.Contains(body, "Netflix") {
			t.Fatal("read discarded permitted quality evidence")
		}
	}
	afterNodes, _ := a.store.nodes()
	afterJSON, _ := json.Marshal(afterNodes)
	if string(beforeJSON) != string(afterJSON) || beforeBudget != a.store.meta(proxyCheckBudgetKey) || beforeGeneration != a.store.meta("desired_generation") {
		t.Fatal("read altered reports, credentials, quota, or core generation")
	}
}
func TestMemberQualityAccountStateAndRevocationApplyImmediately(t *testing.T) {
	a, _, member := memberQualityFixture(t)
	for _, change := range []func(*Record){func(r *Record) { r.Expires = time.Now().Add(-time.Minute).Unix() }, func(r *Record) { r.Expires = 0; r.Quota = 10; r.Upload = 10 }, func(r *Record) { r.Quota = 0; r.Upload = 0; r.Expires = 0; r.Enabled = false }} {
		change(&member)
		if err := a.store.save(&member); err != nil {
			t.Fatal(err)
		}
		if !member.Enabled {
			for _, path := range []string{"/api/node-quality", "/api/state", "/api/subscription"} {
				if req(t, a, member, "GET", path, nil).Code != 401 {
					t.Fatal("disabled session retained access")
				}
			}
			continue
		}
		for _, pool := range []string{"private", "public"} {
			q := readMemberQuality(t, a, member, "/api/node-quality?pool="+pool)
			if q.Active || len(q.Nodes) != 0 {
				t.Fatal("inactive account received quality")
			}
			w := req(t, a, member, "GET", "/api/subscription?pool="+pool, nil)
			var info struct {
				Raw    string `json:"raw"`
				Active bool   `json:"active"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &info)
			if w.Code != 200 || info.Raw != "" || info.Active {
				t.Fatal("inactive account received node credentials")
			}
		}
		w := req(t, a, member, "GET", "/api/state", nil)
		var state struct {
			Nodes []Node `json:"nodes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if w.Code != 200 || len(state.Nodes) != 0 {
			t.Fatal("state bypasses expired/quota authorization")
		}
	}
}
func TestMemberQualityStateUsesAllowlistAndPreservesOwnerView(t *testing.T) {
	a, owner, member := memberQualityFixture(t)
	member.HY2 = false
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, member, "GET", "/api/state", nil)
	var state struct {
		Nodes  []Node       `json:"nodes"`
		IPPool []IPResource `json:"ip_pool"`
		Users  []User       `json:"users"`
	}
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Nodes) != 2 || len(state.Users) != 1 || state.Users[0].ID != member.ID || len(state.IPPool) != 0 {
		t.Fatal("member state scope incorrect")
	}
	for _, node := range state.Nodes {
		if node.Protocol != "vless" || node.Exit != "" || node.Host != "" || node.ExitID != "" || node.UpstreamType != "" || node.Speed != nil || node.ProbeError != "" {
			t.Fatalf("private configuration survived allowlist: %+v", node)
		}
	}
	w = req(t, a, owner, "GET", "/api/state", nil)
	if !strings.Contains(w.Body.String(), "secret-origin.example") || !strings.Contains(w.Body.String(), "disabled-hy2") || !strings.Contains(w.Body.String(), "ungranted-vless") {
		t.Fatal("owner management view unexpectedly filtered")
	}
}
func TestMemberQualityWithholdsStaleExitAndRechecksCurrentActor(t *testing.T) {
	a, _, member := memberQualityFixture(t)
	nodes, _ := a.store.nodes()
	for _, node := range nodes {
		if node.ID == "vless-main" {
			node.ProbeIP = "1.1.1.1"
			if err := a.store.saveNode(node); err != nil {
				t.Fatal(err)
			}
		}
	}
	result := readMemberQuality(t, a, member, "/api/node-quality?protocol=vless")
	if len(result.Nodes) != 1 || result.Nodes[0].Quality != nil || result.Nodes[0].ProbeIP != "1.1.1.1" {
		t.Fatal("old exit report reused")
	}
	staleActor := member
	member.Enabled = false
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.memberNodeQuality(w, httptest.NewRequest(http.MethodGet, "/api/node-quality", nil), staleActor)
	if w.Code != 401 {
		t.Fatal("in-flight actor snapshot bypassed revocation")
	}
}

func TestMemberQualityRedactsUnexpectedErrorsAndPrivateEvidenceLinks(t *testing.T) {
	rawError := "dial https://upstream-user:upstream-pass@private-origin.example:8443 failed"
	node := Node{ProbeIP: "8.8.8.8", Quality: &IPQuality{At: 1, IP: "8.8.8.8", Error: rawError, AI: []QualityTag{{Key: "claude", Evidence: rawError, Source: "https://user:password@claude.ai/path"}}, Providers: []QualitySource{{Name: "source", Error: rawError, Network: QualityTag{Evidence: rawError, Source: "https://private-origin.example/path?token=token-secret"}}, {Name: "rate-limited", Error: "HTTP 429"}}}}
	report := memberQualityReport(node)
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"upstream-user", "upstream-pass", "private-origin", "password@", "token-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("member report exposed transport error: %s", secret)
		}
	}
	if report.Error != "检测未完成，等待管理员更新报告" || report.Providers[1].Error != "HTTP 429" || node.Quality.Error != rawError || node.Quality.Providers[0].Error != rawError {
		t.Fatal("error detail lost or owner report mutated")
	}
}
