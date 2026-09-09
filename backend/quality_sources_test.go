package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestQualityProviderParsingAndAddressValidation(t *testing.T) {
	for _, test := range []struct{ name, body, asn, network string }{
		{"ipapi.is", `{"ip":"9.9.9.9","location":{"country":"Singapore","country_code":"SG","city":"Singapore","state":"Central","timezone":"Asia/Singapore"},"asn":{"asn":132203,"org":"Tencent","type":"hosting","route":"9.9.0.0/16","abuser_score":"0.15 (Elevated)"},"company":{"name":"Tencent","type":"hosting","abuser_score":0.08},"is_datacenter":true,"is_proxy":false,"is_vpn":false,"is_tor":false,"is_abuser":true,"is_crawler":false}`, "AS132203", "confirmed"},
		{"ipwho.is", `{"ip":"9.9.9.9","success":true,"country":"Singapore","country_code":"SG","city":"Singapore","region":"Central","timezone":{"id":"Asia/Singapore"},"connection":{"asn":132203,"isp":"Tencent","org":"Tencent"}}`, "AS132203", "unknown"},
		{"ip.guide", `{"ip":"9.9.9.9","location":{"country":"Singapore","city":"Singapore","timezone":"Asia/Singapore"},"network":{"cidr":"9.9.0.0/16","autonomous_system":{"asn":132203,"organization":"Tencent","country":"CN"}}}`, "AS132203", "unknown"},
		{"ipapi.co", `{"ip":"9.9.9.9","country_name":"Singapore","country_code":"SG","city":"Singapore","region":"Central","timezone":"Asia/Singapore","asn":"AS132203","org":"Tencent","network":"9.9.0.0/16"}`, "AS132203", "unknown"},
		{"DB-IP", `{"ipAddress":"9.9.9.9","countryName":"Singapore","countryCode":"SG","city":"Singapore","stateProv":"Central"}`, "", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := parseQualitySource(test.name, "9.9.9.9", qualityPage{code: 200, body: test.body})
			if q.Status != "ok" || q.ASN != test.asn || countryName(q.Country) != "singapore" || q.Network.Status != test.network || q.City != "Singapore" {
				t.Fatalf("parse %+v", q)
			}
			if q.IP != "9.9.9.9" || q.RegisteredCountry != "" {
				t.Fatal("address or registry provenance lost")
			}
			if parseQualitySource(test.name, "8.8.8.8", qualityPage{code: 200, body: test.body}).Status == "ok" {
				t.Fatal("another IP accepted")
			}
			if test.name == "ipapi.is" {
				if len(q.Signals) != 6 || q.Signals["proxy"] == nil || *q.Signals["proxy"] || q.Signals["abuse"] == nil || !*q.Signals["abuse"] {
					t.Fatal("tri-state provider signals lost")
				}
				if len(q.RiskScores) != 2 || *q.RiskScores[0].Value != 0.15 || q.RiskScores[0].Label != "Elevated" || *q.RiskScores[1].Value != 0.08 {
					t.Fatalf("provider scores changed: %+v", q.RiskScores)
				}
			} else if len(q.Signals) != 0 {
				t.Fatal("absent risk flags treated as false")
			}
		})
	}
}
func TestQualityProviderRejectsInvalidMetadataAndUnrelatedNetworks(t *testing.T) {
	for _, p := range []qualityPage{{code: 429, body: `{"ip":"8.8.8.8"}`}, {code: 200, body: `invalid`}, {code: 200, body: `{"ip":"8.8.8.8","success":false}`}, {code: 200, body: `{"ip":"8.8.8.8","city":"Singapore"}`}, {code: 200, body: `{"ip":"8.8.8.8","country":"US"}`, err: errors.New("truncated")}} {
		got := parseQualitySource("ipwho.is", "8.8.8.8", p)
		if got.Status == "ok" || got.Error == "" {
			t.Fatalf("invalid response accepted: %+v", got)
		}
	}
	if qualityCIDR("9.9.0.0/16", "8.8.8.8") != "" || qualityCIDR("9.9.0.0/16", "9.9.9.9") != "9.9.0.0/16" {
		t.Fatal("CIDR coverage not validated")
	}
	if got := parseQualitySource("ipwho.is", "2001:4860:4860::8888", qualityPage{code: 200, body: `{"ip":"2001:4860:4860:0:0:0:0:8888","country_code":"US","connection":{"asn":15169,"org":"Google"}}`}); got.Status != "ok" {
		t.Fatal("equivalent IPv6 representation rejected")
	}
}
func TestQualitySignalsPreserveMissingFalseAndConflictingEvidence(t *testing.T) {
	yes, no := true, false
	sources := []QualitySource{{Name: "one", Status: "ok", Signals: map[string]*bool{"hosting": &yes, "proxy": &no, "vpn": &yes}}, {Name: "two", Status: "ok", Signals: map[string]*bool{"hosting": &no, "proxy": &no}}, {Name: "failed", Status: "unavailable", Signals: map[string]*bool{"proxy": &yes}}}
	result := qualitySignals(sources)
	states := map[string]string{}
	for _, tag := range result {
		states[tag.Key] = tag.Status
		if strings.Contains(tag.Label, "疑似") {
			t.Fatal("guess label emitted")
		}
	}
	if len(result) != 6 || states["hosting"] != "conflict" || states["proxy"] != "clean" || states["vpn"] != "info" || states["tor"] != "unknown" || states["crawler"] != "unknown" {
		t.Fatalf("tri-state collapsed: %+v", states)
	}
	b, _ := json.Marshal(sources[0])
	if !strings.Contains(string(b), `"proxy":false`) {
		t.Fatal("false omitted from API")
	}
}
func TestQualityProviderScoresAreNotInventedOrRescaled(t *testing.T) {
	source := QualitySource{}
	for _, invalid := range []any{"", "garbage", "0.4 guessed", "-0.1", "78 (Good)", 2} {
		addProviderScore(&source, "test", invalid)
	}
	if len(source.RiskScores) != 0 {
		t.Fatal("invalid or unsupported score accepted")
	}
	addProviderScore(&source, "test", "0 (Low)")
	if len(source.RiskScores) != 1 || source.RiskScores[0].Value == nil || *source.RiskScores[0].Value != 0 || source.RiskScores[0].Scale != "0–1 · ipapi.is" {
		t.Fatal("zero score lost or scale invented")
	}
}
func TestQualityConflictsDoNotClaimNativeOrResidential(t *testing.T) {
	geo := QualityGeo{Registered: "SG", Sources: []QualitySource{{Name: "one", Status: "ok", Country: "SG", ASN: "AS1", Network: QualityTag{Label: "数据中心 IP", Status: "confirmed"}}, {Name: "two", Status: "ok", Country: "US", ASN: "AS2", Network: QualityTag{Label: "ISP 运营商网络", Status: "confirmed"}}}}
	network, native, consensus, asn, org := crossCheckQuality(geo, "SG")
	if network.Status != "conflict" || native.Status != "conflict" || consensus.Label != "多源信息分歧" || asn != "" || org != "" {
		t.Fatal("conflicting evidence overclaimed")
	}
	geo.Sources[1].Country = "Singapore"
	geo.Sources[1].ASN = "AS1"
	geo.Sources[1].Network.Label = "数据中心 IP"
	_, native, consensus, _, _ = crossCheckQuality(geo, "SG")
	if consensus.Label != "多源信息一致" || native.Label != "注册与定位国家一致" {
		t.Fatal("matching aliases rejected")
	}
}
func TestQualityCacheIsScopedToIPAndMarkedAsCached(t *testing.T) {
	a := testApp(t)
	now := time.Now().Unix()
	g := QualityGeo{SchemaVersion: qualitySchemaVersion, At: now, Registered: "SG", Sources: []QualitySource{{Name: "ipapi.is", Status: "ok", At: now}, {Name: "ipwho.is", Status: "ok", At: now}, {Name: "ip.guide", Status: "ok", At: now}}}
	b, _ := json.Marshal(g)
	a.store.db.Exec("INSERT INTO quality_geo_cache VALUES(?,?,?)", "9.9.9.9", b, now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := a.qualityGeo(ctx, "9.9.9.9")
	if v.Registered != "SG" || len(v.Sources) != 3 || !v.Sources[0].Cached || v.Sources[0].At != now {
		t.Fatal("cache or provenance not reused")
	}
	if a.store.meta(proxyCheckBudgetKey) != "" {
		t.Fatal("cache reuse consumed provider request allowance")
	}
	g.SchemaVersion = qualitySchemaVersion - 1
	b, _ = json.Marshal(g)
	a.store.db.Exec("UPDATE quality_geo_cache SET doc=? WHERE ip=?", b, "9.9.9.9")
	v = a.qualityGeo(ctx, "9.9.9.9")
	if v.SchemaVersion != qualitySchemaVersion || v.Registered != "" || len(v.Sources) != 8 {
		t.Fatal("old heuristic metadata reused")
	}
}
func TestQualityAIUsesExplicitPageAndRestrictionSignals(t *testing.T) {
	tests := []struct {
		body          string
		code          int
		status, label string
	}{
		{"<h1>Claude</h1>", 200, "page", "Claude · 页面可达"},
		{"<script>const translation='not available in your country'</script><h1>Claude</h1>", 200, "page", "Claude · 页面可达"},
		{"Claude is not available in your country", 403, "blocked", "Claude · 区域限制"},
		{"Claude: verify you are human", 403, "unknown", "Claude · 人机验证"},
		{"Forbidden", 403, "blocked", "Claude · 访问被拒"},
		{"Rate limited", 429, "unknown", "Claude · 请求受限"},
		{"Unrelated content", 200, "unknown", "Claude · 未知"},
	}
	for _, test := range tests {
		tag := classifyAI("claude", "Claude", qualityPage{body: test.body, code: test.code, address: "https://claude.ai/", latencyMS: 123})
		if tag.Status != test.status || tag.Label != test.label || tag.HTTPStatus != test.code || tag.LatencyMS != 123 || tag.Source != "https://claude.ai/" {
			t.Fatalf("service evidence %+v", tag)
		}
	}
}
func TestQualityHTTPReadBudgetAndLimitedPageEvidence(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("Claude " + strings.Repeat("x", 4096))), Header: make(http.Header), Request: r}, nil
	})}
	p := qualityGet(context.Background(), client, "https://claude.ai/", 64)
	if len(p.body) != 64 || !p.truncated || !errors.Is(p.err, errQualityByteBudget) || p.code != 200 || p.latencyMS < 1 {
		t.Fatalf("byte budget failed: %+v", p)
	}
	if tag := classifyAI("claude", "Claude", p); tag.Status != "page" {
		t.Fatal("observed page prefix lost")
	}
	if tag := classifyAI("claude", "Claude", qualityPage{body: "Claude", code: 200, err: errors.New("connection interrupted")}); tag.Status != "unknown" {
		t.Fatal("real read failure claimed reachable")
	}
}

func TestQualityDetailsKeepCountryAndCityFromCompatibleEvidence(t *testing.T) {
	q := IPQuality{CountryCode: "SG"}
	applyQualityDetails(&q, []QualitySource{{Name: "one", Status: "ok", CountryCode: "US", Country: "United States", City: "New York", Timezone: "America/New_York"}, {Name: "two", Status: "ok", CountryCode: "SG", Country: "Singapore", City: "Singapore", Region: "Central", Timezone: "Asia/Singapore"}})
	if q.City != "Singapore" || q.Region != "Central" || q.Timezone != "Asia/Singapore" {
		t.Fatal("mixed country and location evidence")
	}
	geo := QualityGeo{Sources: []QualitySource{{Name: "one", Status: "ok", Country: "United States of America", CountryCode: "US"}, {Name: "two", Status: "ok", Country: "United States", CountryCode: "US"}}}
	_, _, consensus, _, _ := crossCheckQuality(geo, "US")
	if consensus.Status != "confirmed" {
		t.Fatal("country codes ignored in favor of display aliases")
	}
}

func TestQualityNetCoffeeRejectsAnotherIPDespiteMatchingPrefix(t *testing.T) {
	body := `{"ip":"9.9.9.120","cidr":"9.9.9.0/24","is_datacenter":true,"isResidential":false,"isBroadcast":null,"is_vpn":false,"is_proxy":false,"is_tor":false,"is_crawler":false,"is_abuser":false,"is_mobile":false,"company_type":"hosting","company_name":"6 COLLYER QUAY","abuser_score":"0.0565 (High)","asn":132203,"asOrganization":"Shenzhen Tencent Computer Systems Company Limited","country":"Singapore","countryCode":"sg","region":"","city":"Singapore","timezone":"Asia/Singapore","rpki_status":"invalid","trust_score":73}`
	rejected := parseQualitySource("Net.Coffee", "9.9.9.9", qualityPage{code: 200, body: body})
	if rejected.Status != "unavailable" || rejected.Error != "返回地址与查询地址不符" || rejected.IP != "" || rejected.NetworkCIDR != "" || rejected.Country != "" || rejected.RPKIStatus != "" || len(rejected.Signals) != 0 || len(rejected.RiskScores) != 0 || rejected.Network.Status != "unknown" {
		t.Fatalf("another address borrowed from prefix: %+v", rejected)
	}
	matched := parseQualitySource("Net.Coffee", "9.9.9.120", qualityPage{code: 200, body: body})
	if matched.Status != "ok" || matched.CountryCode != "SG" || matched.NetworkCIDR != "9.9.9.0/24" || matched.ASN != "AS132203" || matched.RPKIStatus != "invalid" || matched.Network.Label != "数据中心 IP" {
		t.Fatalf("exact address not parsed: %+v", matched)
	}
	if matched.Signals["residential"] == nil || *matched.Signals["residential"] || matched.Signals["broadcast"] != nil || matched.Signals["mobile"] == nil || *matched.Signals["mobile"] {
		t.Fatal("explicit null or false converted to inferred identity")
	}
	if len(matched.RiskScores) != 2 || matched.RiskScores[0].Name != "Net.Coffee trust score" || *matched.RiskScores[0].Value != 73 || matched.RiskScores[0].Scale != "0–100 · Net.Coffee" || *matched.RiskScores[1].Value != 0.0565 || matched.RiskScores[1].Label != "High" || matched.RiskScores[1].Scale != "0–1 · Net.Coffee" {
		t.Fatalf("provider score altered: %+v", matched.RiskScores)
	}
}
func TestQualityNetCoffeeIdentityNullsAndSafeScoreBounds(t *testing.T) {
	for _, item := range []struct {
		score    string
		accepted bool
	}{{"0", true}, {"100", true}, {"100.1", false}, {"-1", false}, {`"garbage"`, false}, {"null", false}, {"true", false}} {
		body := `{"ip":"8.8.8.8","countryCode":"uS","is_datacenter":false,"isResidential":null,"isBroadcast":null,"is_mobile":null,"trust_score":` + item.score + `,"rpki_status":"VALID"}`
		q := parseQualitySource("Net.Coffee", "8.8.8.8", qualityPage{code: 200, body: body})
		if q.Status != "ok" || q.CountryCode != "US" || q.RPKIStatus != "valid" || q.Network.Status != "unknown" || q.Signals["residential"] != nil || q.Signals["broadcast"] != nil || q.Signals["mobile"] != nil || (len(q.RiskScores) == 1) != item.accepted {
			t.Fatalf("unsafe identity or score from %s: %+v", item.score, q)
		}
	}
	q := parseQualitySource("Net.Coffee", "8.8.8.8", qualityPage{code: 200, body: `{"ip":"8.8.8.8","countryCode":"US","isResidential":true,"isBroadcast":false,"is_mobile":true,"rpki_status":"looks-valid","abuser_score":3}`})
	if q.Signals["residential"] == nil || !*q.Signals["residential"] || q.Signals["broadcast"] == nil || *q.Signals["broadcast"] || q.RPKIStatus != "" || len(q.RiskScores) != 0 || q.Network.Label != "移动运营商网络" {
		t.Fatal("raw classification or invalid RPKI value mishandled")
	}
	tags := qualitySignals([]QualitySource{q})
	if len(tags) != 6 {
		t.Fatal("raw identity unexpectedly changes aggregate security contract")
	}
	for _, tag := range tags {
		if tag.Status != "unknown" {
			t.Fatal("identity booleans leaked into security flags")
		}
	}
}

func TestQualityIPAPIAnonymousResponsePreservesAvailableLocation(t *testing.T) {
	body := `{"ip":"9.9.9.9","is_bogon":false,"company":"6 COLLYER QUAY","asn":"AS132203 Shenzhen Tencent Computer Systems Company Limited","city":"Singapore","region":"Singapore","country":"Singapore","lat":1.28999,"lon":103.85028,"timezone":"Asia/Singapore","docs":"https://ipapi.is/free-tier.html"}`
	q := parseQualitySource("ipapi.is", "9.9.9.9", qualityPage{code: 200, body: body})
	if q.Status != "ok" || q.ASN != "AS132203" || q.City != "Singapore" || q.Region != "Singapore" || q.Timezone != "Asia/Singapore" || q.Country != "Singapore" {
		t.Fatalf("free-tier fields lost: %+v", q)
	}
	if len(q.Signals) != 0 || len(q.RiskScores) != 0 || q.Network.Status != "unknown" {
		t.Fatal("missing anonymous risk data inferred")
	}
	body = `{"ip":"9.9.9.9","country":"Singapore","city":"old-city","region":"old-region","timezone":"old-timezone","location":{"country_code":"SG","city":"Singapore","state":"Central","timezone":"Asia/Singapore"}}`
	q = parseQualitySource("ipapi.is", "9.9.9.9", qualityPage{code: 200, body: body})
	if q.City != "Singapore" || q.Region != "Central" || q.Timezone != "Asia/Singapore" {
		t.Fatal("top-level fallback overwrites nested location")
	}
}

func TestQualityInterruptedOversizePageIsNotAvailabilityEvidence(t *testing.T) {
	p := qualityPage{body: "Claude", code: 200, truncated: true, err: io.ErrUnexpectedEOF}
	if qualityPageReadable(p) || classifyAI("claude", "Claude", p).Status != "unknown" {
		t.Fatal("interrupted truncated page treated as availability evidence")
	}
}

func TestQualityProxyCheckV3UsesExactIPAndIndependentDetections(t *testing.T) {
	body := `{"status":"ok","9.9.9.9":{"network":{"asn":"AS132203","range":"9.9.9.0/17","provider":"Shenzhen Tencent Computer Systems Company Limited","organisation":"Tencent Cloud Computing","type":"Hosting"},"location":{"country_name":"Singapore","country_code":"SG","region_name":null,"city_name":"Singapore","timezone":"Asia/Singapore"},"detections":{"proxy":false,"vpn":true,"compromised":false,"scraper":false,"tor":false,"hosting":true,"anonymous":true,"risk":50,"confidence":100},"attack_history":null}}`
	q := parseQualitySource("ProxyCheck", "9.9.9.9", qualityPage{code: 200, body: body})
	if q.Status != "ok" || q.IP != "9.9.9.9" || q.ASN != "AS132203" || q.NetworkCIDR != "9.9.0.0/17" || q.City != "Singapore" || q.Timezone != "Asia/Singapore" || q.Network.Label != "数据中心 IP" {
		t.Fatalf("v3 metadata lost: %+v", q)
	}
	if q.Signals["proxy"] == nil || *q.Signals["proxy"] || q.Signals["vpn"] == nil || !*q.Signals["vpn"] || q.Signals["hosting"] == nil || !*q.Signals["hosting"] || q.Signals["abuse"] != nil {
		t.Fatal("VPN/proxy/compromised conflated")
	}
	if len(q.RiskScores) != 2 || *q.RiskScores[0].Value != 50 || *q.RiskScores[1].Value != 100 || q.RiskScores[0].Name != "ProxyCheck risk score" || q.RiskScores[1].Name != "ProxyCheck detection confidence" || q.RiskScores[0].Scale != "0–100 · ProxyCheck" {
		t.Fatalf("risk and confidence not distinguished: %+v", q.RiskScores)
	}
	other := parseQualitySource("ProxyCheck", "9.9.9.44", qualityPage{code: 200, body: body})
	if other.Status != "unavailable" || len(other.Signals) > 0 || len(other.RiskScores) > 0 || other.IP != "" {
		t.Fatal("borrowed another IP response")
	}
	tags := qualitySignals([]QualitySource{q})
	for _, tag := range tags {
		if tag.Key == "abuse" && tag.Status != "unknown" {
			t.Fatal("null attack history treated as no abuse")
		}
		if tag.Key == "proxy" && tag.Status != "clean" {
			t.Fatal("VPN overrode explicit proxy=false")
		}
	}
}
func TestQualityProxyCheckV3RejectsErrorOrAmbiguousAddressAndMissingFlags(t *testing.T) {
	for _, body := range []string{
		`{"status":"denied","8.8.8.8":{"location":{"country_code":"US"},"detections":{"risk":100}}}`,
		`{"status":"ok","8.8.8.8":{"ip":"1.1.1.1","location":{"country_code":"US"}}}`,
		`{"status":"ok","2001:4860:4860::8888":{"location":{"country_code":"US"}},"2001:4860:4860:0:0:0:0:8888":{"location":{"country_code":"US"}}}`,
	} {
		ip := "8.8.8.8"
		if strings.Contains(body, "2001:") {
			ip = "2001:4860:4860::8888"
		}
		q := parseQualitySource("ProxyCheck", ip, qualityPage{code: 200, body: body})
		if q.Status != "unavailable" || len(q.Signals) != 0 || len(q.RiskScores) != 0 {
			t.Fatalf("invalid result accepted: %+v", q)
		}
	}
	q := parseQualitySource("ProxyCheck", "8.8.8.8", qualityPage{code: 200, body: `{"status":"warning","8.8.8.8":{"location":{"country_code":"US"},"detections":{"proxy":null,"vpn":"no","risk":101,"confidence":-1},"attack_history":null}}`})
	if q.Status != "ok" || len(q.Signals) != 0 || len(q.RiskScores) != 0 || q.Network.Status != "unknown" {
		t.Fatal("missing flags or out-of-range scores invented")
	}
	q = parseQualitySource("ProxyCheck", "2001:4860:4860::8888", qualityPage{code: 200, body: `{"status":"ok","2001:4860:4860:0:0:0:0:8888":{"location":{"country_code":"US"}}}`})
	if q.Status != "ok" || q.IP != "2001:4860:4860::8888" {
		t.Fatal("equivalent IPv6 address key rejected")
	}
}
func TestQualityProxyCheckDailyBudgetPersistsAndCapsConcurrentCalls(t *testing.T) {
	a := testApp(t)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	start := qualityProviderBudget{Day: "2026-09-08", Used: 95}
	b, _ := json.Marshal(start)
	a.store.setMeta(proxyCheckBudgetKey, string(b))
	successes := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		go func() { successes <- a.store.reserveProxyCheckQuery(context.Background(), now, false) == nil }()
	}
	accepted := 0
	for i := 0; i < 20; i++ {
		if <-successes {
			accepted++
		}
	}
	if accepted != 5 {
		t.Fatalf("daily limit raced: accepted %d", accepted)
	}
	stored := qualityProviderBudget{}
	if json.Unmarshal([]byte(a.store.meta(proxyCheckBudgetKey)), &stored) != nil || stored.Used != 100 {
		t.Fatal("request count not persisted")
	}
	// A new Store wrapper sharing only the persisted database still observes the limit.
	restarted := Store{db: a.store.db}
	if !errors.Is(restarted.reserveProxyCheckQuery(context.Background(), now, false), errProxyCheckDailyBudget) {
		t.Fatal("budget depended on process memory")
	}
	if restarted.reserveProxyCheckQuery(context.Background(), now.Add(24*time.Hour), false) != nil {
		t.Fatal("next UTC day did not renew budget")
	}
	if restarted.reserveProxyCheckQuery(context.Background(), now.Add(24*time.Hour), true) != nil || !errors.Is(restarted.reserveProxyCheckQuery(context.Background(), now.Add(24*time.Hour), false), errProxyCheckDailyBudget) {
		t.Fatal("provider quota exhaustion did not halt retries")
	}
}
