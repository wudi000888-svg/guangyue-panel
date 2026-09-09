package controlplane

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type MemberNodeQuality struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Protocol    string     `json:"protocol"`
	ProbeIP     string     `json:"probe_ip"`
	Country     string     `json:"country"`
	CountryCode string     `json:"country_code"`
	CheckedAt   int64      `json:"checked_at"`
	Quality     *IPQuality `json:"quality"`
}

func memberMayUseNode(actor Record, n Node) bool {
	if !actor.Active() || !n.Enabled {
		return false
	}
	switch n.Protocol {
	case "vless":
		return actor.VLESS && actor.Credentials.VLESS[n.ID] != ""
	case "hy2":
		return actor.HY2
	default:
		return false
	}
}

func memberReportError(value string, provider bool) string {
	if value == "" {
		return ""
	}
	if provider {
		switch value {
		case "请求失败或超时", "响应格式无效", "返回地址与查询地址不符", "提供方返回错误", "未知数据来源", "缺少有效元数据", "检测预算已用完", "ProxyCheck 本日匿名查询预算已用完", "ProxyCheck 查询预算不可用":
			return value
		}
		if strings.HasPrefix(value, "HTTP ") {
			code, err := strconv.Atoi(strings.TrimPrefix(value, "HTTP "))
			if err == nil && code >= 100 && code <= 599 {
				return "HTTP " + strconv.Itoa(code)
			}
		}
		return "数据源暂不可用"
	}
	switch value {
	case "出口未联通，无法检测 IP 质量", "出口配置不可用", "检测期间出口变化或末次校验失败，服务可达性结论待复测", "检测期间出口变化或末次校验失败，流媒体结论待复测":
		return value
	}
	return "检测未完成，等待管理员更新报告"
}

func memberQualityReport(n Node) *IPQuality {
	if n.Quality == nil {
		return nil
	}
	q := *n.Quality
	// A report for an earlier exit must not follow a later node reconfiguration.
	if q.IP != "" {
		reported, observed := net.ParseIP(q.IP), net.ParseIP(n.ProbeIP)
		if reported == nil || observed == nil || !reported.Equal(observed) {
			return nil
		}
	}
	previousError := q.Error
	q.Error = memberReportError(q.Error, false)
	replacements := map[string]string{previousError: q.Error}
	q.Providers = append([]QualitySource{}, q.Providers...)
	for i := range q.Providers {
		old := q.Providers[i].Error
		q.Providers[i].Error = memberReportError(old, true)
		replacements[old] = q.Providers[i].Error
	}
	// Reports contain public IP metadata, never Node/Pool transport configuration.
	// Strip query strings and restrict service evidence links to known public sites.
	cleanTags := func(tags []QualityTag) []QualityTag {
		out := append([]QualityTag{}, tags...)
		for i := range out {
			for old, replacement := range replacements {
				if old != "" && old != replacement {
					out[i].Evidence = strings.ReplaceAll(out[i].Evidence, old, replacement)
				}
			}
			if out[i].Source == "" {
				continue
			}
			u, err := url.Parse(out[i].Source)
			keep := false
			if err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" {
				h := strings.ToLower(u.Hostname())
				for _, domain := range []string{"netflix.com", "disneyplus.com", "youtube.com", "claude.ai", "anthropic.com"} {
					if h == domain || strings.HasSuffix(h, "."+domain) {
						keep = true
						break
					}
				}
			}
			if keep {
				u.RawQuery = ""
				u.Fragment = ""
				out[i].Source = u.String()
			} else {
				out[i].Source = ""
			}
		}
		return out
	}
	q.Tags, q.Signals, q.Streams, q.AI = cleanTags(q.Tags), cleanTags(q.Signals), cleanTags(q.Streams), cleanTags(q.AI)
	for i := range q.Providers {
		q.Providers[i].Network = cleanTags([]QualityTag{q.Providers[i].Network})[0]
	}
	return &q
}

func memberNodeState(n Node) Node {
	// Construct an allowlist instead of clearing an evolving set of owner fields.
	view := Node{ID: n.ID, Name: n.Name, Protocol: n.Protocol, Enabled: n.Enabled, ProbeIP: n.ProbeIP, Country: n.Country, CountryCode: n.CountryCode, ProbedAt: n.ProbedAt, CheckedAt: n.CheckedAt, Quality: memberQualityReport(n), DNS: n.DNS}
	if n.ManagedBy == publicManager {
		view.ManagedBy = publicManager
	}
	return view
}

func (a *App) memberNodeQuality(w http.ResponseWriter, r *http.Request, actor Record) {
	query := r.URL.Query()
	for key, values := range query {
		if key == "user_id" {
			failure(w, 403, "节点质量仅供当前账号查看")
			return
		}
		if key != "pool" && key != "protocol" || len(values) != 1 {
			failure(w, 400, "节点质量查询参数无效")
			return
		}
	}
	pool := query.Get("pool")
	if pool == "" {
		pool = "private"
	}
	if pool != "private" && pool != "public" {
		failure(w, 400, "订阅池不存在")
		return
	}
	protocol := query.Get("protocol")
	if protocol != "" && protocol != "vless" && protocol != "hy2" {
		failure(w, 400, "节点协议无效")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.store.record(actor.ID)
	if err != nil || !current.Enabled {
		failure(w, 401, "请登录")
		return
	}
	views := []MemberNodeQuality{}
	if current.Active() {
		nodes, err := a.store.nodes()
		if err != nil {
			failure(w, 500, "读取节点失败")
			return
		}
		for _, n := range nodes {
			if !memberMayUseNode(current, n) || (n.ManagedBy == publicManager) != (pool == "public") || protocol != "" && n.Protocol != protocol {
				continue
			}
			views = append(views, MemberNodeQuality{ID: n.ID, Name: n.Name, Protocol: n.Protocol, ProbeIP: n.ProbeIP, Country: n.Country, CountryCode: n.CountryCode, CheckedAt: n.CheckedAt, Quality: memberQualityReport(n)})
		}
	}
	active := current.Active()
	if !active {
		views = []MemberNodeQuality{}
	}
	jsonResponse(w, 200, object{"pool": pool, "active": active, "readonly": true, "nodes": views})
}
