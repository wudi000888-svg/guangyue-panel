package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEgressCountryMatchesObservedIP(t *testing.T) {
	for _, tc := range []struct{ name, ip, body, country string }{
		{"singapore", "203.0.113.10", `{"success":true,"ip":"203.0.113.10","country_code":"SG"}`, "新加坡"},
		{"ipv6", "2606:4700:4700::1111", `{"success":true,"ip":"2606:4700:4700::1111","country_code":"US"}`, "美国"},
		{"wrong_ip", "203.0.113.10", `{"success":true,"ip":"1.1.1.1","country_code":"US"}`, ""},
		{"provider_error", "203.0.113.10", `{"success":false}`, ""},
		{"invalid_country", "203.0.113.10", `{"success":true,"ip":"203.0.113.10","country_code":"ZZ"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.ip
				if calls == 2 {
					if r.URL.Scheme != "https" || r.URL.Host != "ipwho.is" || r.URL.Path != "/"+tc.ip {
						t.Fatal("lookup did not query the observed exit")
					}
					body = tc.body
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}
			r, err := detectEgress(context.Background(), client)
			if err != nil || calls != 2 || r.IP != tc.ip || r.Country != tc.country {
				t.Fatalf("unexpected detection: %+v, %v", r, err)
			}
			if (r.Warning != "") != (tc.country == "") {
				t.Fatal("country lookup failure not distinguished from connectivity failure")
			}
		})
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("upstream unavailable") })}
	if _, err := detectEgress(context.Background(), client); err == nil {
		t.Fatal("unreachable exit accepted")
	}
}

func TestEgressFailureAndStaleWrite(t *testing.T) {
	a := testApp(t)
	nodes, _ := a.store.nodes()
	n := nodes[0]
	applyEgress(&n, Egress{IP: "203.0.113.10", Country: "新加坡", CountryCode: "SG"}, nil)
	if err := a.store.saveNode(n); err != nil {
		t.Fatal(err)
	}
	old := n
	updated := n
	updated.Exit, updated.Host, updated.Port = "http", "127.0.0.1", 8080
	if err := a.store.saveNode(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeEgress(old, Egress{IP: "8.8.8.8", Country: "美国", CountryCode: "US"}, nil); err == nil {
		t.Fatal("stale detection overwrote new exit")
	}
	applyEgress(&n, Egress{}, errors.New("offline"))
	if n.Name != "新加坡 · 203.0.113.10" || n.ProbeError == "" {
		t.Fatal("failure erased last known identity")
	}
	applyEgress(&n, Egress{IP: "8.8.8.8", Warning: "lookup unavailable"}, nil)
	if n.Country != "" || n.CountryCode != "" || n.Name != "国家待识别 · 8.8.8.8" {
		t.Fatal("new IP inherited stale country")
	}
}

func TestAutomaticNamesDoNotRestartCoresOrDuplicateSubscriptions(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	nodes, _ := a.store.nodes()
	before := nodeHash(nodes, "vless")
	for i := range nodes {
		applyEgress(&nodes[i], Egress{IP: "203.0.113.10", Country: "新加坡", CountryCode: "SG"}, nil)
	}
	if nodeHash(nodes, "vless") != before {
		t.Fatal("metadata update would restart Xray")
	}
	duplicate := nodes[1]
	duplicate.ID = "vless-duplicate"
	u.Credentials.VLESS[duplicate.ID] = uuid()
	nodes = append(nodes, duplicate)
	b, _, err := subscription(a.cfg, u, nodes, "mihomo", "")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
	}
	if err = yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range doc.Proxies {
		if seen[p.Name] || !strings.HasPrefix(p.Name, "新加坡 · 203.0.113.10 · ") {
			t.Fatalf("invalid subscription name: %s", p.Name)
		}
		seen[p.Name] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 distinct exported nodes, got %d", len(seen))
	}
}

func TestNodeNameAndCountryCannotBeSpoofed(t *testing.T) {
	a := testApp(t)
	o := testUser(t, a, "owner", "owner")
	w := req(t, a, o, "POST", "/api/nodes", Node{Protocol: "vless", Exit: "direct", Enabled: true, Name: "伪造国家 · 1.1.1.1", ProbeIP: "1.1.1.1", Country: "伪造国家", CountryCode: "US", ProbedAt: 123})
	if w.Code != 200 {
		t.Fatalf("new node without a manual name rejected: %d", w.Code)
	}
	n := decodeNode(t, w)
	if n.Country == "伪造国家" || n.ProbeIP == "1.1.1.1" || n.Name == "伪造国家 · 1.1.1.1" {
		t.Fatal("client-controlled detection accepted")
	}
	user := testUser(t, a, "user", "user")
	if req(t, a, user, "POST", "/api/nodes/vless-main/detect", object{}).Code != 403 {
		t.Fatal("ordinary user can trigger detection")
	}
}
