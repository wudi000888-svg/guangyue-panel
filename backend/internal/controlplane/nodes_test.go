package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func decodeNode(t *testing.T, w *httptest.ResponseRecorder) Node {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("node API: %d %s", w.Code, w.Body.String())
	}
	var n Node
	if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDirectMigrationRemovesPoolWithoutChangingSubscriptions(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "owner", "owner")
	nodes, _ := a.store.nodes()
	before, _, _ := subscription(a.cfg, u, nodes, "raw", "")
	xh, hh := nodeHash(nodes, "vless"), nodeHash(nodes, "hy2")
	p := resourceFromNode(nodes[0])
	for i := range nodes {
		nodes[i].ExitID = p.ID
	}
	if err := a.store.savePoolNodes([]IPResource{p}, nodes); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.store.migratePools(); err != nil {
			t.Fatal(err)
		}
	}
	pools, _ := a.store.pools()
	nodes, _ = a.store.nodes()
	if len(pools) != 0 {
		t.Fatal("local direct resource remained in pool")
	}
	for _, n := range nodes {
		if n.ExitID != "" || n.Exit != "direct" {
			t.Fatal("direct route lost during migration")
		}
	}
	after, _, _ := subscription(a.cfg, u, nodes, "raw", "")
	if !bytes.Equal(before, after) || xh != nodeHash(nodes, "vless") || hh != nodeHash(nodes, "hy2") {
		t.Fatal("migration changed subscriptions or routes")
	}
	if req(t, a, u, "POST", "/api/ips", IPResource{Node: Node{Exit: "direct", Enabled: true}}).Code != 400 {
		t.Fatal("direct resource can be added again")
	}
}

func TestMultipleHY2NodesDefaultDirectAndIndependentCredentials(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	nodes, _ := a.store.nodes()
	oldSub, _, _ := subscription(a.cfg, member, nodes, "raw", "")
	hy := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", Node{PolicyVersion: 1, RateMilli: 1000, GroupIDs: []string{legacyPrivateGroup}, Protocol: "hy2", Enabled: true}))
	vl := decodeNode(t, req(t, a, owner, "POST", "/api/nodes", Node{PolicyVersion: 1, RateMilli: 1000, GroupIDs: []string{legacyPrivateGroup}, Protocol: "vless", Enabled: true}))
	for _, n := range []Node{hy, vl} {
		if n.Exit != "direct" || n.ExitID != "" || n.Host != "" || n.Port != 0 {
			t.Fatal("new node does not default to unpooled direct")
		}
	}
	pools, _ := a.store.pools()
	if len(pools) != 0 {
		t.Fatal("new direct node added a pool resource")
	}
	if req(t, a, member, "POST", "/api/nodes", Node{Protocol: "hy2", Enabled: true}).Code != 403 {
		t.Fatal("member can create nodes")
	}
	member, _ = a.store.record(member.ID)
	nodes, _ = a.store.nodes()
	raw, _, _ := subscription(a.cfg, member, nodes, "raw", "")
	for _, line := range strings.Split(strings.TrimSpace(string(oldSub)), "\n") {
		if !strings.Contains(string(raw), line) {
			t.Fatal("adding nodes changed existing subscription credentials")
		}
	}
	passwords := map[string]bool{}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		u, _ := url.Parse(line)
		if u.Scheme == "hysteria2" {
			count++
			if u.Port() != "443" || passwords[u.User.Username()] {
				t.Fatal("HY2 nodes do not have distinct credentials on 443")
			}
			passwords[u.User.Username()] = true
		}
	}
	if count != 2 {
		t.Fatal("new HY2 missing from subscription")
	}
	auth := func(password string) (bool, string) {
		b, _ := json.Marshal(object{"auth": password})
		w := httptest.NewRecorder()
		a.hyAuth(w, httptest.NewRequest("POST", "/auth", bytes.NewReader(b)))
		var result struct {
			OK bool   `json:"ok"`
			ID string `json:"id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result.OK, result.ID
	}
	if ok, id := auth(hyNodePassword(member, hy)); !ok || id != hyNodeIdentity(member, hy) {
		t.Fatal("node credential does not select authenticated node identity")
	}
	hy.Enabled = false
	_ = decodeNode(t, req(t, a, owner, "POST", "/api/nodes", hy))
	if ok, _ := auth(hyNodePassword(member, hy)); ok {
		t.Fatal("disabled HY2 authenticates through another node")
	}
	if ok, _ := auth(member.Credentials.HY2); !ok {
		t.Fatal("disabling additional HY2 disabled main node")
	}
	hy.Enabled = true
	_ = decodeNode(t, req(t, a, owner, "POST", "/api/nodes", hy))
	p := decodePool(t, req(t, a, owner, "POST", "/api/ips", IPResource{Node: Node{Exit: "http", Host: "203.0.113.12", Port: 8080, Enabled: true}}))
	hy.ExitID = p.ID
	hy = decodeNode(t, req(t, a, owner, "POST", "/api/nodes", hy))
	nodes, _ = a.store.nodes()
	cfg := hy2Config(a.cfg, nodes)
	routes := cfg["nodeOutbounds"].(object)
	if cfg["listen"] != ":443" || cfg["nodeRouting"] != true || routes[hy.ID].(object)["type"] != "socks5" || routes["hy2-main"].(object)["type"] != "direct" {
		t.Fatal("HY2 nodes share the wrong outbound")
	}
	if req(t, a, owner, "DELETE", "/api/nodes/"+hy.ID, nil).Code != 200 {
		t.Fatal("additional HY2 cannot be deleted")
	}
	if req(t, a, owner, "DELETE", "/api/nodes/hy2-main", nil).Code != 400 {
		t.Fatal("last HY2 can be deleted")
	}
	if ok, _ := auth(hyNodePassword(member, hy)); ok {
		t.Fatal("deleted HY2 still authenticates")
	}
}
