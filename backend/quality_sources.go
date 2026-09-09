package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const qualitySchemaVersion = 4

type QualityRiskScore struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value,omitempty"`
	Label string   `json:"label,omitempty"`
	Scale string   `json:"scale,omitempty"`
}
type QualitySource struct {
	Name              string             `json:"name"`
	Status            string             `json:"status"`
	Country           string             `json:"country"`
	CountryCode       string             `json:"country_code,omitempty"`
	IP                string             `json:"ip,omitempty"`
	City              string             `json:"city,omitempty"`
	Region            string             `json:"region,omitempty"`
	Timezone          string             `json:"timezone,omitempty"`
	NetworkCIDR       string             `json:"network_cidr,omitempty"`
	RegisteredCountry string             `json:"registered_country,omitempty"`
	RPKIStatus        string             `json:"rpki_status,omitempty"`
	ASN               string             `json:"asn"`
	Organization      string             `json:"organization"`
	Network           QualityTag         `json:"network"`
	Signals           map[string]*bool   `json:"signals,omitempty"`
	RiskScores        []QualityRiskScore `json:"risk_scores,omitempty"`
	Error             string             `json:"error,omitempty"`
	At                int64              `json:"at"`
	Cached            bool               `json:"cached"`
}
type QualityGeo struct {
	SchemaVersion int             `json:"schema_version"`
	At            int64           `json:"at"`
	Registered    string          `json:"registered"`
	Sources       []QualitySource `json:"sources"`
}

var asnNumber = regexp.MustCompile(`(?i)^(?:AS)?([0-9]{1,10})(?:\s|$)`)
var providerScore = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*(?:\(([^)]{1,80})\))?$`)

func cleanASN(value string) string {
	m := asnNumber.FindStringSubmatch(strings.TrimSpace(value))
	if len(m) > 1 {
		return "AS" + m[1]
	}
	return ""
}
func countryName(value string) string {
	if len(value) == 2 {
		if r, e := language.ParseRegion(strings.ToUpper(value)); e == nil && r.IsCountry() {
			return strings.ToLower(display.English.Regions().Name(r))
		}
	}
	return strings.ToLower(strings.TrimSpace(value))
}
func mapValue(v any) object {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return object{}
}
func qualityCIDR(value, ip string) string {
	_, network, err := net.ParseCIDR(value)
	if err == nil && network.Contains(net.ParseIP(ip)) {
		return network.String()
	}
	return ""
}
func qualityCountryCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) == 2 {
		if r, err := language.ParseRegion(value); err == nil && r.IsCountry() {
			return value
		}
	}
	return ""
}
func setQualitySignal(v *QualitySource, key string, value any) {
	if b, ok := value.(bool); ok {
		if v.Signals == nil {
			v.Signals = map[string]*bool{}
		}
		v.Signals[key] = &b
	}
}
func addProviderScore(v *QualitySource, name string, value any) {
	addScaledProviderScore(v, name, value, 1, "0–1 · ipapi.is")
}
func addScaledProviderScore(v *QualitySource, name string, value any, maximum float64, scale string) {
	// Retain the provider's own scale and label; do not manufacture a normalized risk score.
	text := strings.TrimSpace(str(value))
	if text == "" {
		return
	}
	m := providerScore.FindStringSubmatch(text)
	if len(m) == 0 {
		return
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n < 0 || n > maximum {
		return
	}
	s := QualityRiskScore{Name: name, Value: &n, Scale: scale}
	if len(m) > 2 {
		s.Label = safeLabel(m[2])
	}
	v.RiskScores = append(v.RiskScores, s)
}
func parseQualitySource(name, ip string, p qualityPage) QualitySource {
	unknown, _, _, _ := classifyIP(object{}, "", "")
	v := QualitySource{Name: name, Status: "unavailable", At: time.Now().Unix(), Network: unknown}
	if p.err != nil {
		v.Error = "请求失败或超时"
		return v
	}
	if p.code != 200 {
		v.Error = fmt.Sprintf("HTTP %d", p.code)
		return v
	}
	var m object
	if json.Unmarshal([]byte(p.body), &m) != nil {
		v.Error = "响应格式无效"
		return v
	}
	if name == "ProxyCheck" {
		status := str(m["status"])
		if status != "ok" && status != "warning" {
			v.Error = "提供方返回错误"
			return v
		}
		matched := ""
		target := net.ParseIP(ip)
		for key := range m {
			parsed := net.ParseIP(key)
			if parsed != nil && target != nil && parsed.Equal(target) {
				if matched != "" {
					v.Error = "返回地址与查询地址不符"
					return v
				}
				matched = key
			}
		}
		if matched == "" {
			v.Error = "返回地址与查询地址不符"
			return v
		}
		m = mapValue(m[matched])
		if len(m) == 0 {
			v.Error = "缺少有效元数据"
			return v
		}
		if existing := str(m["ip"]); existing != "" {
			parsed := net.ParseIP(existing)
			if parsed == nil || !parsed.Equal(target) {
				v.Error = "返回地址与查询地址不符"
				return v
			}
		}
		m["ip"] = matched
	}
	ipField := "ip"
	if name == "DB-IP" {
		ipField = "ipAddress"
	}
	parsed, target := net.ParseIP(str(m[ipField])), net.ParseIP(ip)
	if parsed == nil || target == nil || !parsed.Equal(target) {
		v.Error = "返回地址与查询地址不符"
		return v
	}
	v.IP = parsed.String()
	normalized := object{}
	switch name {
	case "ipapi.is":
		if m["error"] != nil && m["error"] != false {
			v.Error = "提供方返回错误"
			return v
		}
		loc := mapValue(m["location"])
		v.Country, v.CountryCode = str(loc["country"]), str(loc["country_code"])
		if v.Country == "" {
			v.Country = str(m["country"])
		}
		if v.CountryCode == "" {
			v.CountryCode = str(m["country_code"])
		}
		v.City, v.Region, v.Timezone = str(loc["city"]), str(loc["state"]), str(loc["timezone"])
		if v.City == "" {
			v.City = str(m["city"])
		}
		if v.Region == "" {
			v.Region = str(m["region"])
		}
		if v.Timezone == "" {
			v.Timezone = str(m["timezone"])
		}
		normalized = m
		asn, company := mapValue(m["asn"]), mapValue(m["company"])
		v.NetworkCIDR = qualityCIDR(str(asn["route"]), ip)
		if v.NetworkCIDR == "" {
			v.NetworkCIDR = qualityCIDR(str(company["network"]), ip)
		}
		for key, field := range map[string]string{"hosting": "is_datacenter", "proxy": "is_proxy", "vpn": "is_vpn", "tor": "is_tor", "abuse": "is_abuser", "crawler": "is_crawler"} {
			setQualitySignal(&v, key, m[field])
		}
		addProviderScore(&v, "ASN abuse score", asn["abuser_score"])
		addProviderScore(&v, "Company abuse score", company["abuser_score"])
	case "ipwho.is":
		if m["success"] == false {
			v.Error = "提供方返回错误"
			return v
		}
		v.Country, v.CountryCode = str(m["country"]), str(m["country_code"])
		v.City, v.Region = str(m["city"]), str(m["region"])
		v.Timezone = str(mapValue(m["timezone"])["id"])
		c := mapValue(m["connection"])
		v.ASN, v.Organization = cleanASN(str(c["asn"])), str(c["isp"])
		if v.Organization == "" {
			v.Organization = str(c["org"])
		}
		security := mapValue(m["security"])
		for _, key := range []string{"proxy", "vpn", "tor"} {
			setQualitySignal(&v, key, security[key])
		}
		setQualitySignal(&v, "hosting", security["hosting"])
	case "ip.guide":
		loc, network := mapValue(m["location"]), mapValue(m["network"])
		v.Country, v.City, v.Timezone = str(loc["country"]), str(loc["city"]), str(loc["timezone"])
		v.NetworkCIDR = qualityCIDR(str(network["cidr"]), ip)
		system := mapValue(network["autonomous_system"])
		v.ASN, v.Organization = cleanASN(str(system["asn"])), str(system["organization"])
		// ASN registration country is not the address range's RIR registration country.
	case "ipapi.co":
		if m["error"] == true {
			v.Error = "提供方返回错误"
			return v
		}
		v.Country, v.CountryCode = str(m["country_name"]), str(m["country_code"])
		v.City, v.Region, v.Timezone = str(m["city"]), str(m["region"]), str(m["timezone"])
		v.ASN, v.Organization, v.NetworkCIDR = cleanASN(str(m["asn"])), str(m["org"]), qualityCIDR(str(m["network"]), ip)
	case "DB-IP":
		v.Country, v.CountryCode = str(m["countryName"]), str(m["countryCode"])
		v.City, v.Region = str(m["city"]), str(m["stateProv"])
	case "ProxyCheck":
		network, loc, detections := mapValue(m["network"]), mapValue(m["location"]), mapValue(m["detections"])
		v.Country, v.CountryCode = str(loc["country_name"]), str(loc["country_code"])
		v.City, v.Region, v.Timezone = str(loc["city_name"]), str(loc["region_name"]), str(loc["timezone"])
		v.ASN, v.Organization = cleanASN(str(network["asn"])), str(network["organisation"])
		if v.Organization == "" {
			v.Organization = str(network["provider"])
		}
		v.NetworkCIDR = qualityCIDR(str(network["range"]), ip)
		normalized = object{"is_datacenter": detections["hosting"]}
		if strings.EqualFold(str(network["type"]), "Hosting") {
			normalized["company"] = object{"type": "hosting"}
		}
		for _, key := range []string{"hosting", "proxy", "vpn", "tor", "compromised", "anonymous"} {
			setQualitySignal(&v, key, detections[key])
		}
		setQualitySignal(&v, "crawler", detections["scraper"])
		// A compromised device and recorded abuse are different observations. An absent
		// attack_history is not a clean abuse record and never supplies an abuse=false flag.
		addScaledProviderScore(&v, "ProxyCheck risk score", detections["risk"], 100, "0–100 · ProxyCheck")
		addScaledProviderScore(&v, "ProxyCheck detection confidence", detections["confidence"], 100, "0–100 · ProxyCheck")
	case "Net.Coffee":
		if m["error"] != nil && m["error"] != false {
			v.Error = "提供方返回错误"
			return v
		}
		v.Country, v.CountryCode = str(m["country"]), str(m["countryCode"])
		v.City, v.Region, v.Timezone = str(m["city"]), str(m["region"]), str(m["timezone"])
		v.ASN, v.Organization = cleanASN(str(m["asn"])), str(m["asOrganization"])
		v.NetworkCIDR = qualityCIDR(str(m["cidr"]), ip)
		if v.Organization == "" {
			v.Organization = str(m["company_name"])
		}
		normalized = object{"is_datacenter": m["is_datacenter"], "is_mobile": m["is_mobile"], "asn": object{"asn": m["asn"], "org": m["asOrganization"], "type": m["asn_kind"]}, "company": object{"name": m["company_name"], "type": m["company_type"]}}
		for key, field := range map[string]string{"hosting": "is_datacenter", "proxy": "is_proxy", "vpn": "is_vpn", "tor": "is_tor", "abuse": "is_abuser", "crawler": "is_crawler", "residential": "isResidential", "broadcast": "isBroadcast", "mobile": "is_mobile"} {
			setQualitySignal(&v, key, m[field])
		}
		switch status := strings.ToLower(strings.TrimSpace(str(m["rpki_status"]))); status {
		case "valid", "invalid", "not-found":
			v.RPKIStatus = status
		}
		addScaledProviderScore(&v, "Net.Coffee trust score", m["trust_score"], 100, "0–100 · Net.Coffee")
		addScaledProviderScore(&v, "Net.Coffee abuse score", m["abuser_score"], 1, "0–1 · Net.Coffee")

	default:
		v.Error = "未知数据来源"
		return v
	}
	v.CountryCode = qualityCountryCode(v.CountryCode)
	if v.Country == "" {
		v.Country = v.CountryCode
	}
	tag, _, asn, org := classifyIP(normalized, "", "")
	v.Network = tag
	if v.ASN == "" {
		v.ASN = cleanASN(asn)
	}
	if v.Organization == "" {
		v.Organization = org
	}
	if hosting := v.Signals["hosting"]; hosting != nil && *hosting {
		v.Network = QualityTag{Key: "network", Label: "数据中心 IP", Status: "confirmed", Evidence: name + " 明确标记为托管或数据中心网络"}
	}
	v.Country, v.City, v.Region, v.Timezone, v.Organization = safeLabel(v.Country), safeLabel(v.City), safeLabel(v.Region), safeLabel(v.Timezone), safeLabel(v.Organization)
	if v.ASN == "" && v.Country == "" && v.Organization == "" && len(v.Signals) == 0 && len(v.RiskScores) == 0 {
		v.Error = "缺少有效元数据"
		return v
	}
	v.Status = "ok"
	return v
}

const proxyCheckDailyLimit = 100
const proxyCheckBudgetKey = "quality_proxycheck_daily_budget"

type qualityProviderBudget struct {
	Day  string `json:"day"`
	Used int    `json:"used"`
}

var errProxyCheckDailyBudget = errors.New("ProxyCheck 本日匿名查询预算已用完")

func (s *Store) reserveProxyCheckQuery(ctx context.Context, now time.Time, exhausted bool) error {
	// Reserve before dispatch. Failed attempts count too, so retries cannot exceed the
	// anonymous allowance. The transaction keeps concurrent calls and restarts bounded.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", proxyCheckBudgetKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	budget := qualityProviderBudget{}
	if raw != "" && json.Unmarshal([]byte(raw), &budget) != nil {
		return errors.New("ProxyCheck 查询预算状态无效")
	}
	day := now.UTC().Format("2006-01-02")
	if budget.Day != day {
		budget = qualityProviderBudget{Day: day}
	}
	if budget.Used < 0 || budget.Used > proxyCheckDailyLimit {
		return errors.New("ProxyCheck 查询预算状态无效")
	}
	if exhausted {
		budget.Used = proxyCheckDailyLimit
	} else {
		if budget.Used >= proxyCheckDailyLimit {
			return errProxyCheckDailyBudget
		}
		budget.Used++
	}
	b, _ := json.Marshal(budget)
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", proxyCheckBudgetKey, string(b)); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) qualityGeo(ctx context.Context, ip string) QualityGeo {
	var b []byte
	var at int64
	var cached QualityGeo
	if a.store.db.QueryRow("SELECT doc,at FROM quality_geo_cache WHERE ip=?", ip).Scan(&b, &at) == nil && json.Unmarshal(b, &cached) == nil && cached.SchemaVersion == qualitySchemaVersion {
		healthy := 0
		for _, s := range cached.Sources {
			if s.Status == "ok" && s.Name != "RIR RDAP" {
				healthy++
			}
		}
		ttl := int64(900)
		if healthy >= 3 {
			ttl = 21600
		}
		if time.Now().Unix()-at < ttl {
			for i := range cached.Sources {
				cached.Sources[i].Cached = true
			}
			return cached
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()
	providers := []struct{ name, address string }{{"ipapi.is", "https://api.ipapi.is/?q=" + ip}, {"ipwho.is", "https://ipwho.is/" + ip}, {"ip.guide", "https://ip.guide/" + ip}, {"ipapi.co", "https://ipapi.co/" + ip + "/json/"}, {"DB-IP", "https://api.db-ip.com/v2/free/" + ip}, {"Net.Coffee", "https://ip.net.coffee/api/iprisk/" + ip}, {"ProxyCheck", "https://proxycheck.io/v3/" + ip}}
	result := QualityGeo{SchemaVersion: qualitySchemaVersion, At: time.Now().Unix(), Sources: make([]QualitySource, len(providers))}
	if !publicIP(ip) {
		return result
	}
	slots := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, provider := range providers {
		wg.Add(1)
		go func(i int, name, address string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				result.Sources[i] = QualitySource{Name: name, Status: "unavailable", Error: "检测预算已用完", At: time.Now().Unix()}
				return
			}
			if name == "ProxyCheck" {
				if err := a.store.reserveProxyCheckQuery(ctx, time.Now(), false); err != nil {
					reason := "ProxyCheck 查询预算不可用"
					if errors.Is(err, errProxyCheckDailyBudget) {
						reason = err.Error()
					}
					result.Sources[i] = QualitySource{Name: name, Status: "unavailable", At: time.Now().Unix(), Error: reason}
					return
				}
			}
			transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second}
			defer transport.CloseIdleConnections()
			page := qualityGet(ctx, &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, address, 32<<10)
			if name == "ProxyCheck" && page.code == http.StatusTooManyRequests {
				_ = a.store.reserveProxyCheckQuery(ctx, time.Now(), true)
			}
			result.Sources[i] = parseQualitySource(name, ip, page)
		}(i, provider.name, provider.address)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
			return
		}
		result.Registered = rdapCountry(ctx, ip)
	}()
	wg.Wait()
	result.Sources = append(result.Sources, QualitySource{Name: "RIR RDAP", Status: map[bool]string{true: "ok", false: "unavailable"}[result.Registered != ""], IP: ip, Country: result.Registered, CountryCode: result.Registered, RegisteredCountry: result.Registered, At: result.At})
	b, _ = json.Marshal(result)
	_, _ = a.store.db.Exec("INSERT INTO quality_geo_cache(ip,doc,at) VALUES(?,?,?) ON CONFLICT(ip) DO UPDATE SET doc=excluded.doc,at=excluded.at", ip, b, result.At)
	_, _ = a.store.db.Exec("DELETE FROM quality_geo_cache WHERE ip NOT IN (SELECT ip FROM quality_geo_cache ORDER BY at DESC LIMIT 256)")
	return result
}
func crossCheckQuality(geo QualityGeo, country string) (QualityTag, QualityTag, QualityTag, string, string) {
	network, native, _, _ := classifyIP(object{}, geo.Registered, country)
	asn, org := "", ""
	countries, asns := map[string]bool{}, map[string]bool{}
	types := map[string]int{}
	usable, countryEvidence, asnEvidence := 0, 0, 0
	if country != "" {
		countries[countryName(country)] = true
	}
	for _, s := range geo.Sources {
		if s.Status != "ok" || s.Name == "RIR RDAP" {
			continue
		}
		usable++
		sourceCountry := s.Country
		if s.CountryCode != "" {
			sourceCountry = s.CountryCode
		}
		if sourceCountry != "" {
			countries[countryName(sourceCountry)] = true
			countryEvidence++
		}
		if s.ASN != "" {
			asns[s.ASN] = true
			asnEvidence++
			if asn == "" {
				asn, org = s.ASN, s.Organization
			}
		}
		if s.Network.Status != "unknown" && s.Network.Label != "" {
			types[s.Network.Label]++
			if network.Status == "unknown" {
				network = s.Network
			}
		}
	}
	consensus := QualityTag{Key: "consensus", Label: "多源证据不足", Status: "unknown", Evidence: "至少需要两家提供相同国家或 ASN 的数据提供方"}
	if len(countries) > 1 || len(asns) > 1 {
		consensus = QualityTag{Key: "consensus", Label: "多源信息分歧", Status: "conflict", Evidence: "国家或 ASN 在数据源间不一致，按来源展示原始结果"}
		if len(countries) > 1 {
			native = QualityTag{Key: "native", Label: "地理国家存在分歧", Status: "conflict", Evidence: "地理来源存在国家冲突，无法与注册国家作单一对照"}
		}
	}
	if len(countries) <= 1 && len(asns) <= 1 && usable >= 2 && (countryEvidence >= 2 || asnEvidence >= 2) {
		consensus = QualityTag{Key: "consensus", Label: "多源信息一致", Status: "confirmed", Evidence: "至少两家来源在国家或 ASN 上一致；注册对照与住宅接入分别展示"}
	}
	if len(types) > 1 {
		network = QualityTag{Key: "network", Label: "网络类型分歧", Status: "conflict", Evidence: "提供方对网络类型返回不同分类，保留各方证据"}
	}
	if len(asns) > 1 {
		asn, org = "", ""
	}
	return network, native, consensus, asn, org
}
func qualitySignals(sources []QualitySource) []QualityTag {
	result := make([]QualityTag, 0, 6)
	for _, item := range []struct{ key, name string }{{"hosting", "托管网络"}, {"proxy", "代理标记"}, {"vpn", "VPN 标记"}, {"tor", "Tor 出口"}, {"abuse", "滥用记录"}, {"crawler", "爬虫标记"}} {
		positive, negative := []string{}, []string{}
		for _, s := range sources {
			if s.Status != "ok" {
				continue
			}
			if value := s.Signals[item.key]; value != nil {
				if *value {
					positive = append(positive, s.Name)
				} else {
					negative = append(negative, s.Name)
				}
			}
		}
		tag := QualityTag{Key: item.key, Label: item.name + " · 无数据", Status: "unknown", Evidence: "可用提供方未返回此项，缺失值不视为否"}
		switch {
		case len(positive) > 0 && len(negative) > 0:
			tag.Label, tag.Status, tag.Evidence = item.name+" · 来源冲突", "conflict", "标记来源："+strings.Join(positive, ", ")+"；未标记来源："+strings.Join(negative, ", ")
		case len(positive) > 0:
			tag.Label, tag.Status, tag.Evidence = item.name+" · 已标记", "info", "返回 true 的提供方："+strings.Join(positive, ", ")
		case len(negative) > 0:
			tag.Label, tag.Status, tag.Evidence = item.name+" · 未标记", "clean", "返回 false 的提供方："+strings.Join(negative, ", ")+"；仅表示这些数据库当前未标记"
		}
		result = append(result, tag)
	}
	return result
}
func applyQualityDetails(q *IPQuality, sources []QualitySource) {
	// Prefer one complete source for city/region/timezone rather than composing an impossible location.
	for _, s := range sources {
		if s.Status != "ok" || s.Name == "RIR RDAP" {
			continue
		}
		if q.NetworkCIDR == "" {
			q.NetworkCIDR = s.NetworkCIDR
		}
		sourceCountry := s.Country
		if s.CountryCode != "" {
			sourceCountry = s.CountryCode
		}
		if q.City == "" && s.City != "" && (q.CountryCode == "" || countryName(sourceCountry) == countryName(q.CountryCode)) {
			q.City, q.Region, q.Timezone = s.City, s.Region, s.Timezone
		}
	}
	q.Signals = qualitySignals(sources)
}
