package main

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type QualityTag struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Evidence   string `json:"evidence"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	Source     string `json:"source,omitempty"`
}
type IPQuality struct {
	SchemaVersion     int             `json:"schema_version"`
	City              string          `json:"city,omitempty"`
	Region            string          `json:"region,omitempty"`
	Timezone          string          `json:"timezone,omitempty"`
	NetworkCIDR       string          `json:"network_cidr,omitempty"`
	Signals           []QualityTag    `json:"signals,omitempty"`
	AI                []QualityTag    `json:"ai,omitempty"`
	Providers         []QualitySource `json:"providers,omitempty"`
	At                int64           `json:"at"`
	IP                string          `json:"ip"`
	CountryCode       string          `json:"country_code"`
	RegisteredCountry string          `json:"registered_country"`
	ASN               string          `json:"asn"`
	Organization      string          `json:"organization"`
	Tags              []QualityTag    `json:"tags"`
	Streams           []QualityTag    `json:"streams"`
	Sources           []string        `json:"sources"`
	Error             string          `json:"error,omitempty"`
}
type qualityPage struct {
	code          int
	latencyMS     int64
	truncated     bool
	body, address string
	err           error
}

var scriptMarkup = regexp.MustCompile(`(?is)<(?:script|style)\b[^>]*>.*?</(?:script|style)>`)
var htmlMarkup = regexp.MustCompile(`<[^>]+>`)
var errQualityByteBudget = errors.New("response exceeds quality byte budget")

func visibleQualityText(body string) string {
	return strings.ToLower(htmlMarkup.ReplaceAllString(scriptMarkup.ReplaceAllString(body, ""), " "))
}

const qualityAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func qualityGet(ctx context.Context, client *http.Client, address string, limit int64) qualityPage {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return qualityPage{err: err}
	}
	req.Header.Set("User-Agent", qualityAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	start := time.Now()
	res, err := client.Do(req)
	latency := max(1, time.Since(start).Milliseconds())
	if err != nil {
		return qualityPage{err: err, address: address, latencyMS: latency}
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	truncated := int64(len(b)) > limit
	if truncated {
		b = b[:limit]
		if err == nil {
			err = errQualityByteBudget
		}
	}
	return qualityPage{code: res.StatusCode, latencyMS: latency, truncated: truncated, body: string(b), address: res.Request.URL.String(), err: err}
}

func qualityRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 4 || req.URL.Scheme != "https" {
		return errors.New("unsupported redirect")
	}
	h := req.URL.Hostname()
	for _, domain := range []string{"netflix.com", "disneyplus.com", "youtube.com", "claude.ai", "anthropic.com"} {
		if h == domain || strings.HasSuffix(h, "."+domain) {
			return nil
		}
	}
	return errors.New("unsupported redirect")
}

func qualityPageReadable(p qualityPage) bool {
	return p.err == nil || p.truncated && errors.Is(p.err, errQualityByteBudget)
}

func qualityPageEvidence(tag QualityTag, p qualityPage) QualityTag {
	tag.HTTPStatus, tag.LatencyMS, tag.Source = p.code, p.latencyMS, p.address
	if p.truncated {
		tag.Evidence += "；页面仅读取预算内的内容"
	}
	return tag
}

func classifyNetflix(pages []qualityPage) QualityTag {
	t := QualityTag{Key: "netflix", Label: "Netflix · 未知", Status: "unknown", Evidence: "公开目录探测不验证登录、DRM 或付费播放"}
	for _, p := range pages {
		if !qualityPageReadable(p) {
			continue
		}
		b := strings.ToLower(p.body)
		if p.code == 200 && strings.Contains(b, "og:video") {
			return qualityPageEvidence(QualityTag{Key: t.Key, Label: "Netflix · 目录元数据可用", Status: "confirmed", Evidence: "片目页返回视频元数据；未验证登录、DRM 或付费播放"}, p)
		}
	}
	for _, p := range pages {
		if qualityPageReadable(p) && (strings.Contains(visibleQualityText(p.body), "not available in your country") || strings.Contains(visibleQualityText(p.body), "not available in your region")) {
			return qualityPageEvidence(QualityTag{Key: t.Key, Label: "Netflix · 区域限制", Status: "blocked", Evidence: "公开页面明确返回地区不可用提示"}, p)
		}
	}
	// 403, 404 and a generic login page are not sufficient evidence of originals-only.
	for _, p := range pages {
		if qualityPageReadable(p) && p.code == 200 && strings.Contains(strings.ToLower(p.body), "netflix") {
			t.Label, t.Status, t.Evidence = "Netflix · 页面可达", "page", "页面可访问，但未识别到授权片目元数据；不能据此认定全解锁或仅自制"
			return qualityPageEvidence(t, p)
		}
	}
	if len(pages) > 0 {
		t = qualityPageEvidence(t, pages[0])
	}
	return t
}

func classifyStream(key, name string, p qualityPage) QualityTag {
	t := qualityPageEvidence(QualityTag{Key: key, Label: name + " · 未知", Status: "unknown", Evidence: "请求失败、验证页或缺少可识别信号；未验证付费播放"}, p)
	if !qualityPageReadable(p) {
		return t
	}
	b := strings.ToLower(p.body)
	visible := visibleQualityText(p.body)
	for _, marker := range []string{"not available in your region", "not available in your country", "not available in your location", "is not available in this country", "unavailable in your region"} {
		if strings.Contains(visible, marker) {
			t.Label, t.Status, t.Evidence = name+" · 区域限制", "blocked", "公开页面明确返回地区不可用提示"
			return t
		}
	}
	if p.code != 200 || strings.Contains(b, "captcha") || strings.Contains(b, "verify you are human") || strings.Contains(b, "consent.youtube.com") {
		return t
	}
	if key == "youtube" && (strings.Contains(b, "purchasebuttonoverride") || strings.Contains(b, "start trial")) {
		t.Label, t.Status, t.Evidence = name+" · 提供订阅入口", "confirmed", "Premium 页面返回购买或试用入口；未验证账户资格或播放"
	} else if strings.Contains(b, strings.ToLower(name)) || key == "disney" && strings.Contains(b, "disney") {
		t.Label, t.Status, t.Evidence = name+" · 页面可达", "page", "公开页面可访问；实际播放及地区授权未验证"
	}
	return t
}

func classifyAI(key, name string, p qualityPage) QualityTag {
	t := qualityPageEvidence(QualityTag{Key: key, Label: name + " · 未知", Status: "unknown", Evidence: "请求失败或未返回可识别信号；未验证登录与模型调用"}, p)
	if !qualityPageReadable(p) {
		return t
	}
	visible := visibleQualityText(p.body)
	body := strings.ToLower(p.body)
	for _, marker := range []string{"not available in your country", "not available in your region", "unsupported country", "unsupported region", "country is not supported", "not supported in your country", "not supported in your region"} {
		if strings.Contains(visible, marker) {
			t.Label, t.Status, t.Evidence = name+" · 区域限制", "blocked", "公开页面明确提示当前国家或地区不可用"
			return t
		}
	}
	if strings.Contains(body, "captcha") || strings.Contains(body, "verify you are human") || strings.Contains(body, "cf-chl-") || strings.Contains(body, "just a moment") {
		t.Label, t.Evidence = name+" · 人机验证", "服务返回人机验证页面，接口可用性尚未验证"
		return t
	}
	if p.code == 403 {
		t.Label, t.Status, t.Evidence = name+" · 访问被拒", "blocked", "HTTP 403；未发现地区限制证据，可能由访问策略拒绝"
		return t
	}
	if p.code == 429 {
		t.Label, t.Evidence = name+" · 请求受限", "HTTP 429；当前请求受到限流"
		return t
	}
	if p.code == 200 && strings.Contains(body, strings.ToLower(name)) {
		t.Label, t.Status, t.Evidence = name+" · 页面可达", "page", "公开页面成功返回；未验证账户登录、订阅资格或模型 API 调用"
	}
	return t
}

func classifyIP(meta object, registered, country string) (QualityTag, QualityTag, string, string) {
	asn, org := "", ""
	asnType, companyType := "", ""
	if v, ok := meta["asn"].(map[string]any); ok {
		asn = "AS" + str(v["asn"])
		org = str(v["org"])
		asnType = str(v["type"])
	} else {
		asn = str(meta["asn"])
	}
	if v, ok := meta["company"].(map[string]any); ok {
		if org == "" {
			org = str(v["name"])
		}
		companyType = str(v["type"])
	} else if org == "" {
		org = str(meta["company"])
	}
	network := QualityTag{Key: "network", Label: "网络类型未识别", Status: "unknown", Evidence: "提供方未明确分类；ASN 或机构名称不能证明住宅宽带或托管类型"}
	dataCenter, _ := meta["is_datacenter"].(bool)
	mobile, _ := meta["is_mobile"].(bool)
	switch {
	case dataCenter || companyType == "hosting" || asnType == "hosting":
		network = QualityTag{Key: "network", Label: "数据中心 IP", Status: "confirmed", Evidence: "提供方明确标记为数据中心或托管网络"}
	case mobile:
		network = QualityTag{Key: "network", Label: "移动运营商网络", Status: "confirmed", Evidence: "提供方明确标记为移动网络；不据此判断住宅宽带"}
	case companyType == "isp" || asnType == "isp":
		network = QualityTag{Key: "network", Label: "ISP 运营商网络", Status: "confirmed", Evidence: "提供方明确分类为 ISP；企业专线与住宅接入无法仅凭该字段区分"}
	}
	native := QualityTag{Key: "native", Label: "注册与定位对照未知", Status: "unknown", Evidence: "缺少有效 RIR 注册国家或出口地理国家；注册国家对照不能证明原生、广播或住宅属性"}
	registered, country = qualityCountryCode(registered), qualityCountryCode(country)
	if registered != "" && country != "" {
		if registered == country {
			native.Label, native.Status = "注册与定位国家一致", "confirmed"
		} else {
			native.Label, native.Status = "注册与定位国家不一致", "info"
		}
		native.Evidence = "RIR 注册国家 " + registered + "，出口地理国家 " + country + "；这是国家对照结果，不代表物理接入位置或 BGP 宣告性质"
	}
	return network, native, safeLabel(asn), safeLabel(org)
}

func rdapCountry(ctx context.Context, ip string) string {
	t := &http.Transport{DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext, TLSHandshakeTimeout: 4 * time.Second}
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, Timeout: 9 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" {
			return errors.New("RDAP redirect rejected")
		}
		for _, h := range []string{"rdap.org", "rdap.apnic.net", "rdap.arin.net", "rdap.db.ripe.net", "rdap.lacnic.net", "rdap.afrinic.net"} {
			if r.URL.Hostname() == h {
				return nil
			}
		}
		return errors.New("RDAP redirect rejected")
	}}
	p := qualityGet(ctx, c, "https://rdap.org/ip/"+ip, 256<<10)
	var r struct{ Country, StartAddress, EndAddress string }
	if p.err != nil || p.code != 200 || json.Unmarshal([]byte(p.body), &r) != nil {
		return ""
	}
	start, end, target := net.ParseIP(r.StartAddress).To16(), net.ParseIP(r.EndAddress).To16(), net.ParseIP(ip).To16()
	if start == nil || end == nil || target == nil || strings.Compare(string(target), string(start)) < 0 || strings.Compare(string(target), string(end)) > 0 {
		return ""
	}
	return qualityCountryCode(r.Country)
}

func (a *App) measureQuality(ctx context.Context, n Node) IPQuality {
	ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
	defer cancel()
	q := IPQuality{SchemaVersion: qualitySchemaVersion, At: time.Now().Unix(), Tags: []QualityTag{}, Streams: []QualityTag{}, Sources: []string{"ipify / ipwho.is", "ipapi.is", "ipwho.is", "ip.guide", "ipapi.co", "DB-IP", "Net.Coffee", "ProxyCheck", "RIR RDAP", "流媒体与 AI 公开页面"}}
	unknownNetwork, unknownNative, _, _ := classifyIP(object{}, "", "")
	q.Tags = []QualityTag{unknownNetwork, unknownNative}
	q.Streams = []QualityTag{classifyNetflix(nil), classifyStream("disney", "Disney+", qualityPage{}), classifyStream("youtube", "YouTube Premium", qualityPage{})}
	q.AI = []QualityTag{classifyAI("claude", "Claude", qualityPage{}), classifyAI("anthropic", "Anthropic", qualityPage{})}
	q.Signals = qualitySignals(nil)
	if n.ManagedBy == publicManager {
		q.Tags = append(q.Tags, QualityTag{Key: "public", Label: "公开代理", Status: "info", Evidence: "该资源来自公开代理列表"})
	}
	e, err := probeNode(ctx, n)
	if err != nil {
		q.Error = "出口未联通，无法检测 IP 质量"
		return q
	}
	q.IP, q.CountryCode = e.IP, e.CountryCode
	t, err := egressTransport(n)
	if err != nil {
		q.Error = "出口配置不可用"
		return q
	}
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, Timeout: 8 * time.Second, CheckRedirect: qualityRedirect}
	addresses := []string{"https://www.netflix.com/title/81280792", "https://www.netflix.com/title/70143836", "https://www.disneyplus.com/", "https://www.youtube.com/premium", "https://claude.ai/", "https://www.anthropic.com/"}
	pages := make([]qualityPage, len(addresses))
	geo := QualityGeo{}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 2)
	for i, address := range addresses {
		wg.Add(1)
		go func(i int, address string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				pages[i] = qualityPage{address: address, err: ctx.Err()}
				return
			}
			pages[i] = qualityGet(ctx, c, address, 384<<10)
		}(i, address)
	}
	wg.Add(1)
	go func() { defer wg.Done(); geo = a.qualityGeo(ctx, e.IP) }()
	wg.Wait()
	q.RegisteredCountry = geo.Registered
	q.Providers = geo.Sources
	applyQualityDetails(&q, geo.Sources)
	network, native, consensus, asn, org := crossCheckQuality(geo, q.CountryCode)
	q.ASN, q.Organization = asn, org
	q.Tags = []QualityTag{network, native, consensus}
	if n.ManagedBy == publicManager {
		q.Tags = append(q.Tags, QualityTag{Key: "public", Label: "公开代理", Status: "info", Evidence: "该资源来自公开代理列表"})
	}
	q.Streams = []QualityTag{classifyNetflix(pages[:2]), classifyStream("disney", "Disney+", pages[2]), classifyStream("youtube", "YouTube Premium", pages[3])}
	q.AI = []QualityTag{classifyAI("claude", "Claude", pages[4]), classifyAI("anthropic", "Anthropic", pages[5])}
	last := qualityGet(ctx, c, "https://api.ipify.org", 128)
	if last.err != nil || strings.TrimSpace(last.body) != q.IP {
		q.Error = "检测期间出口变化或末次校验失败，服务可达性结论待复测"
		if last.err == nil && publicIP(strings.TrimSpace(last.body)) && strings.TrimSpace(last.body) != q.IP {
			q.Tags = append(q.Tags, QualityTag{Key: "rotating", Label: "出口轮换", Status: "info", Evidence: "同次检测观测到不同公网 IP，标签对应检测时的地址快照"})
		}
		for i := range q.Streams {
			q.Streams[i].Status = "unknown"
			q.Streams[i].Label = map[string]string{"netflix": "Netflix", "disney": "Disney+", "youtube": "YouTube Premium"}[q.Streams[i].Key] + " · 待复测"
			q.Streams[i].Evidence = q.Error
		}
		for i := range q.AI {
			q.AI[i].Status = "unknown"
			q.AI[i].Label = map[string]string{"claude": "Claude", "anthropic": "Anthropic"}[q.AI[i].Key] + " · 待复测"
			q.AI[i].Evidence = q.Error
		}
	}
	return q
}

func (a *App) saveQuality(snapshot Node, pool bool, result IPQuality) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if pool || snapshot.ExitID != "" {
		id := snapshot.ID
		if !pool {
			id = snapshot.ExitID
		}
		p, err := a.store.pool(id)
		if err != nil || !sameExit(p.Node, snapshot) || p.ProbeIP != snapshot.ProbeIP {
			return errors.New("出口已变化，请重新检测")
		}
		p.Quality = &result
		applyQualityIdentity(&p.Node, result)
		nodes, err := a.store.nodes()
		if err != nil {
			return err
		}
		bound := poolBindings(nodes, id)
		for i := range bound {
			bound[i] = bindPool(bound[i], p)
		}
		return a.store.savePoolNodes([]IPResource{p}, bound)
	}
	nodes, err := a.store.nodes()
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.ID == snapshot.ID {
			if !sameExit(n, snapshot) || n.ProbeIP != snapshot.ProbeIP {
				return errors.New("出口已变化，请重新检测")
			}
			n.Quality = &result
			applyQualityIdentity(&n, result)
			return a.store.saveNode(n)
		}
	}
	return errors.New("节点已不存在")
}

func applyQualityIdentity(n *Node, q IPQuality) {
	if !publicIP(q.IP) {
		return
	}
	if n.ProbeIP != q.IP {
		n.Country, n.CountryCode = "", ""
	}
	n.ProbeIP = q.IP
	if region, err := language.ParseRegion(q.CountryCode); err == nil && region.IsCountry() {
		n.CountryCode = q.CountryCode
		n.Country = display.SimplifiedChinese.Regions().Name(region)
	}
	n.Name = egressName(n.Country, n.ProbeIP)
}

func (a *App) qualityTest(w http.ResponseWriter, r *http.Request) {
	if !a.heavyMu.TryLock() {
		failure(w, 409, "后台任务繁忙，请稍后重试")
		return
	}
	defer a.heavyMu.Unlock()
	if !a.qualityMu.TryLock() {
		failure(w, 409, "已有质量检测正在进行，请稍后重试")
		return
	}
	defer a.qualityMu.Unlock()
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 5 {
		failure(w, 404, "资源不存在")
		return
	}
	pool := parts[2] == "ips"
	var n Node
	if pool {
		p, err := a.store.pool(parts[3])
		if err == nil {
			n = p.Node
		}
	} else {
		nodes, _ := a.store.nodes()
		for _, x := range nodes {
			if x.ID == parts[3] {
				n = x
			}
		}
	}
	if n.ID == "" {
		failure(w, 404, "资源不存在")
		return
	}
	if !n.Enabled {
		failure(w, 400, "请先启用资源")
		return
	}
	q := a.measureQuality(r.Context(), n)
	if err := a.saveQuality(n, pool, q); err != nil {
		failure(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, q)
}

// One due resource per minute keeps anonymous metadata requests and memory bounded.
func (a *App) refreshQuality(ctx context.Context) {
	if !a.heavyMu.TryLock() {
		return
	}
	defer a.heavyMu.Unlock()
	a.publicMu.Lock()
	collecting := a.publicCancel != nil
	a.publicMu.Unlock()
	if collecting {
		return
	}
	if !a.qualityMu.TryLock() {
		return
	}
	defer a.qualityMu.Unlock()
	pools, err := a.store.pools()
	if err != nil {
		return
	}
	for _, p := range pools {
		interval := 24 * time.Hour
		if p.Quality != nil && p.Quality.Error != "" {
			interval = 15 * time.Minute
		}
		if p.Enabled && p.Reachable && (p.Quality == nil || p.Quality.SchemaVersion < qualitySchemaVersion || time.Since(time.Unix(p.Quality.At, 0)) > interval) {
			q := a.measureQuality(ctx, p.Node)
			if ctx.Err() == nil {
				_ = a.saveQuality(p.Node, true, q)
			}
			return
		}
	}
}
