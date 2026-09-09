package controlplane

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type subscriptionNodeResponse struct {
	Raw      string             `json:"raw"`
	Active   bool               `json:"active"`
	Protocol string             `json:"protocol"`
	User     User               `json:"user"`
	Nodes    []SubscriptionNode `json:"nodes"`
}

func TestSubscriptionCardsUseAuthorizedEntriesForSelectedAccount(t *testing.T) {
	a, owner, member := memberQualityFixture(t)
	for _, actor := range []Record{owner, member} {
		for _, pool := range []string{"private", "public"} {
			for _, protocol := range []string{"", "hy2", "vless"} {
				path := "/api/subscription?user_id=" + strconv.FormatInt(member.ID, 10) + "&pool=" + pool + "&protocol=" + protocol
				w := req(t, a, actor, "GET", path, nil)
				got := decoded[subscriptionNodeResponse](t, w, 200)
				nodes, _ := a.store.nodes()
				want, _, _ := scopedSubscription(a.cfg, member, nodes, "raw", protocol, pool == "public")
				lines := []string{}
				seen := map[string]bool{}
				for _, n := range got.Nodes {
					if seen[n.ID] || protocol != "" && n.Protocol != protocol {
						t.Fatal("duplicate or wrong protocol in card")
					}
					seen[n.ID] = true
					lines = append(lines, n.URI)
					u, err := url.Parse(n.URI)
					if err != nil || u.Fragment != n.Name {
						t.Fatal("card does not describe its own link")
					}
					if n.Protocol == "vless" && u.User.Username() != member.Credentials.VLESS[n.ID] {
						t.Fatal("card uses another account or node credential")
					}
				}
				if got.User.ID != member.ID || got.Raw != string(want) || got.Protocol != protocol || strings.TrimSpace(got.Raw) != strings.Join(lines, "\n") {
					t.Fatal("cards diverged from scoped subscription")
				}
				for _, secret := range []string{"secret-origin", "secret-upstream", "secret-bridge", "private-pool-identifier", "secret-link-token"} {
					if strings.Contains(w.Body.String(), secret) {
						t.Fatal("card leaked upstream configuration")
					}
				}
			}
		}
	}
}

func TestSubscriptionCardsPairDuplicateNamesByNodeIdentity(t *testing.T) {
	a, owner, member := memberQualityFixture(t)
	all, _ := a.store.nodes()
	var template Node
	for _, n := range all {
		if n.ID == "vless-main" {
			template = n
		}
	}
	template.Name = "Shared exit"
	if err := a.store.saveNode(template); err != nil {
		t.Fatal(err)
	}
	second := template
	second.ID = "second-vless"
	second.ProbeIP = "1.1.1.1"
	second.Quality = &IPQuality{IP: "1.1.1.1", At: 200, SchemaVersion: 4}
	if err := a.store.saveNode(second); err != nil {
		t.Fatal(err)
	}
	member.Credentials.VLESS[second.ID] = uuid()
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	got := decoded[subscriptionNodeResponse](t, req(t, a, owner, "GET", "/api/subscription?user_id="+strconv.FormatInt(member.ID, 10)+"&protocol=vless", nil), 200)
	if len(got.Nodes) != 2 {
		t.Fatalf("want two authorized VLESS cards, got %d", len(got.Nodes))
	}
	if got.Nodes[0].Name == got.Nodes[1].Name {
		t.Fatal("duplicate displayed subscription names")
	}
	for _, n := range got.Nodes {
		u, _ := url.Parse(n.URI)
		if u.User.Username() != member.Credentials.VLESS[n.ID] || n.Quality == nil || n.Quality.IP != n.ProbeIP {
			t.Fatal("link/report joined to another node")
		}
	}
}

func TestSubscriptionCardsEnforceProtocolAndAccountStatus(t *testing.T) {
	a, owner, member := memberQualityFixture(t)
	path := "/api/subscription?user_id=" + strconv.FormatInt(member.ID, 10)
	if req(t, a, member, "GET", "/api/subscription?user_id="+strconv.FormatInt(owner.ID, 10), nil).Code != 403 {
		t.Fatal("member selected another account")
	}
	if req(t, a, owner, "GET", path+"&protocol=unknown", nil).Code != 400 {
		t.Fatal("invalid protocol accepted")
	}
	member.VLESS = false
	member.HY2 = false
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	got := decoded[subscriptionNodeResponse](t, req(t, a, member, "GET", path, nil), 200)
	if got.Active || len(got.Nodes) != 0 || got.Raw != "" {
		t.Fatal("ungranted protocols exposed")
	}
	member.VLESS = true
	member.HY2 = true
	member.Expires = time.Now().Unix() - 1
	if err := a.store.save(&member); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, owner, "GET", path, nil)
	got = decoded[subscriptionNodeResponse](t, w, 200)
	if got.Active || len(got.Nodes) != 0 || got.Raw != "" {
		t.Fatal("expired user exposed links/reports")
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if string(raw["nodes"]) != "[]" {
		t.Fatal("empty cards must be an array")
	}
}
