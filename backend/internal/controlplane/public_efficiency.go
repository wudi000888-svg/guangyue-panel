package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const publicSampleBytes int64 = 256 << 10
const publicDownloadSamples int32 = 16 // At most 4 MiB per collection cycle.
const publicBlacklistLimit = 8192

type PublicBlock struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	Reason    string `json:"reason"`
	At        int64  `json:"at"`
	ExpiresAt int64  `json:"expires_at"`
	Policy    string `json:"policy,omitempty"`
}

func publicPerformancePolicy(c PublicSettings) string {
	return strconv.FormatInt(c.MaxLatencyMS, 10) + "/" + strconv.FormatFloat(c.MinMbps, 'g', -1, 64)
}
func (s *Store) publicBlocks(c PublicSettings) (map[string]PublicBlock, error) {
	rows, err := s.db.Query("SELECT doc FROM public_blacklist WHERE expires>? ORDER BY expires DESC LIMIT ?", time.Now().Unix(), publicBlacklistLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := map[string]PublicBlock{}
	for rows.Next() {
		var b []byte
		var v PublicBlock
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if json.Unmarshal(b, &v) == nil && (v.Policy == "" || v.Policy == publicPerformancePolicy(c)) {
			blocks[v.ID] = v
		}
	}
	return blocks, rows.Err()
}
func publicDeferred(reason string) bool {
	return reason == "本轮下载预算已用完" || reason == "检测已取消"
}
func (s *Store) blockPublic(p IPResource, reason string, c PublicSettings) (PublicBlock, error) {
	v := PublicBlock{ID: p.ID, Address: p.Exit + "://" + net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), Reason: reason, At: time.Now().Unix()}
	v.ExpiresAt = v.At + 24*3600
	if !strings.HasPrefix(reason, "TCP") && !strings.HasPrefix(reason, "HTTPS") && reason != "配置无效" {
		v.Policy = publicPerformancePolicy(c)
		v.ExpiresAt = v.At + 3600
	}
	b, _ := json.Marshal(v)
	_, err := s.db.Exec("INSERT INTO public_blacklist(id,doc,expires) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc,expires=excluded.expires", v.ID, b, v.ExpiresAt)
	return v, err
}
func (s *Store) trimPublicBlocks() {
	_, _ = s.db.Exec("DELETE FROM public_blacklist WHERE expires<=?", time.Now().Unix())
	_, _ = s.db.Exec("DELETE FROM public_blacklist WHERE id IN (SELECT id FROM public_blacklist ORDER BY expires DESC LIMIT 2147483647 OFFSET ?)", publicBlacklistLimit)
}
func publicBlockSummary(blocks map[string]PublicBlock) object {
	entries := make([]PublicBlock, 0, len(blocks))
	for _, v := range blocks {
		entries = append(entries, v)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].At > entries[j].At })
	if len(entries) > 12 {
		entries = entries[:12]
	}
	return object{"count": len(blocks), "recent": entries}
}

// Cache public candidate lists only; private subscriptions use their own store.
func (a *App) fetchPublicSource(ctx context.Context, address string) ([]byte, error) {
	return a.fetchPublicSourceMode(ctx, address, false, nil)
}
func (a *App) fetchPublicSourceMode(ctx context.Context, address string, force bool, transport http.RoundTripper) (body []byte, resultErr error) {
	var source *PublicSource
	catalog, err := a.store.publicSourceCatalog()
	if err != nil {
		return nil, errors.New("无法读取公开来源")
	}
	for i := range catalog {
		if catalog[i].URL == address && catalog[i].Kind != "text" && catalog[i].Enabled {
			source = &catalog[i]
			break
		}
	}
	if source == nil {
		return nil, errors.New("未知公开来源")
	}
	if source.Custom && validatePublicSourceURL(address) != nil {
		return nil, errors.New("公开来源地址无效")
	}
	var previous []byte
	var etag, modified string
	var fetched int64
	_ = a.store.db.QueryRow("SELECT body,etag,modified,fetched FROM public_source_cache WHERE id=?", source.ID).Scan(&previous, &etag, &modified, &fetched)
	if !force && len(previous) > 0 && len(previous) <= 1<<20 && time.Since(time.Unix(fetched, 0)) < 15*time.Minute {
		return previous, nil
	}
	a.recordPublicSource(source.ID, true, nil, 0, nil)
	status := 0
	defer func() { a.recordPublicSource(source.ID, false, body, status, resultErr) }()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, errors.New("公开来源地址无效")
	}
	req.Header.Set("User-Agent", "Guangyue/"+version)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if modified != "" {
		req.Header.Set("If-Modified-Since", modified)
	}
	if transport == nil {
		tr := &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 3 * time.Second}
		if source.Custom {
			tr.DialContext = publicSourceDial
		}
		defer tr.CloseIdleConnections()
		transport = tr
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if source.Custom {
		client.CheckRedirect = publicSourceRedirect
	}
	r, err := client.Do(req)
	if err != nil {
		return nil, errors.New("公开来源请求失败")
	}
	defer r.Body.Close()
	status = r.StatusCode
	if status == 304 && len(previous) > 0 {
		_, err = a.store.db.Exec("UPDATE public_source_cache SET fetched=? WHERE id=?", time.Now().Unix(), source.ID)
		return previous, err
	}
	if status != 200 {
		return nil, errors.New("公开来源暂不可用")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return nil, errors.New("公开来源超过大小限制或下载失败")
	}
	if len(parsePublicList(b, *source)) == 0 {
		return nil, errors.New("公开来源未返回有效代理，保留上次列表")
	}
	if source.Custom {
		b, _, err = normalizedPublicText(string(b), *source, true)
		if err != nil {
			return nil, err
		}
	}
	_, err = a.store.db.Exec("INSERT INTO public_source_cache(id,body,etag,modified,fetched) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,etag=excluded.etag,modified=excluded.modified,fetched=excluded.fetched", source.ID, b, r.Header.Get("ETag"), r.Header.Get("Last-Modified"), time.Now().Unix())
	return b, err
}

func publicTCP(ctx context.Context, p IPResource) error {
	c, err := (&net.Dialer{Timeout: 1200 * time.Millisecond}).DialContext(ctx, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)))
	if err != nil {
		return err
	}
	return c.Close()
}

var publicIPTargets = []string{"https://api.ipify.org", "https://www.cloudflare.com/cdn-cgi/trace", "https://checkip.amazonaws.com/"}
var publicSpeedTargets = []string{"https://speed.cloudflare.com/__down?bytes=262144", "https://proof.ovh.net/files/1Mb.dat"}

func publicResponseIP(body string) string {
	ip := strings.TrimSpace(body)
	if publicIP(ip) {
		return ip
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "ip=") && publicIP(strings.TrimSpace(line[3:])) {
			return strings.TrimSpace(line[3:])
		}
	}
	return ""
}
func publicFindIP(ctx context.Context, client *http.Client, targets []string) string {
	for _, address := range targets {
		if ctx.Err() != nil {
			return ""
		}
		r := qualityGet(ctx, client, address, 4096)
		if r.err == nil && r.code == 200 {
			if ip := publicResponseIP(r.body); ip != "" {
				return ip
			}
		}
	}
	return ""
}

// Verified direct targets are chosen once per cycle; target outages cannot ban proxies.
func publicHealthyTargets(ctx context.Context, ipTargets, speedTargets []string) ([]string, string, error) {
	t, _ := egressTransport(Node{Protocol: "vless", Exit: "direct", Enabled: true})
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	all := append(append([]string{}, ipTargets...), speedTargets...)
	ok := make([]bool, len(all))
	var wg sync.WaitGroup
	for i, address := range all {
		wg.Add(1)
		go func(i int, address string) {
			defer wg.Done()
			r := qualityGet(ctx, c, address, 1024)
			ok[i] = publicBaselineResponseOK(r, i < len(ipTargets))
		}(i, address)
	}
	wg.Wait()
	healthy := []string{}
	speed := ""
	for i, address := range ipTargets {
		if ok[i] {
			healthy = append(healthy, address)
		}
	}
	for i, address := range speedTargets {
		if ok[len(ipTargets)+i] {
			speed = address
			break
		}
	}
	if len(healthy) == 0 || speed == "" {
		return nil, "", errors.New("检测目标或 VPS 网络异常，本轮未变更公共节点及黑名单")
	}
	return healthy, speed, nil
}

func publicBaselineResponseOK(r qualityPage, identity bool) bool {
	if r.code != 200 {
		return false
	}
	if identity {
		return r.err == nil && publicResponseIP(r.body) != ""
	}
	// Speed endpoints intentionally return files larger than this 1 KiB
	// connectivity sample. Only the explicit byte cap is an acceptable stop;
	// short responses and interrupted reads remain failed baseline checks.
	return len(r.body) >= 1024 && (r.err == nil || r.truncated && errors.Is(r.err, errQualityByteBudget))
}

type publicDownloadBudget struct{ reserved atomic.Int32 }

func (b *publicDownloadBudget) acquire() bool {
	for {
		n := b.reserved.Load()
		if n >= publicDownloadSamples {
			return false
		}
		if b.reserved.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func publicDownload(ctx context.Context, t http.RoundTripper, address string, latencyLimit int64, budget *publicDownloadBudget) (SpeedResult, string) {
	s := SpeedResult{At: time.Now().Unix()}
	if !budget.acquire() {
		return s, "本轮下载预算已用完"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Range", "bytes=0-262143")
	req.Header.Set("User-Agent", "Guangyue/"+version)
	start := time.Now()
	c := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Do(req)
	if err != nil {
		budget.reserved.Add(-1)
		s.Error = "下载连接失败"
		return s, "下载检测失败"
	}
	defer r.Body.Close()
	s.LatencyMS = max(1, time.Since(start).Milliseconds())
	if r.StatusCode != 200 && r.StatusCode != 206 || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		budget.reserved.Add(-1)
		s.Error = "测速服务响应异常"
		return s, "下载检测失败"
	}
	if s.LatencyMS > latencyLimit {
		budget.reserved.Add(-1)
		s.Error = "延迟超过阈值，已停止下载"
		return s, "延迟超过阈值"
	}
	start = time.Now()
	s.Bytes, err = io.CopyBuffer(io.Discard, io.LimitReader(r.Body, publicSampleBytes), make([]byte, 16<<10))
	s.DurationMS = max(1, time.Since(start).Milliseconds())
	s.Partial = s.Bytes < publicSampleBytes
	if err != nil || s.Partial {
		s.Error = "下载中断或样本不足"
		return s, "下载检测失败或样本不足"
	}
	s.Mbps = float64(s.Bytes) * 8 / time.Since(start).Seconds() / 1e6
	return s, ""
}

func publicFastPerformance(ctx context.Context, p IPResource, c PublicSettings, ipTargets []string, speedTarget string, budget *publicDownloadBudget) publicProbeResult {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	t, err := egressTransport(p.Node)
	if err != nil {
		return publicProbeResult{p, "配置无效"}
	}
	defer t.CloseIdleConnections()
	requestTimeout := min(4*time.Second, max(1800*time.Millisecond, time.Duration(c.MaxLatencyMS)*time.Millisecond+time.Second))
	client := &http.Client{Transport: t, Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ip := publicFindIP(ctx, client, ipTargets)
	if parent.Err() != nil {
		return publicProbeResult{p, "检测已取消"}
	}
	if ip == "" {
		return publicProbeResult{p, "HTTPS 不通"}
	}
	speedCtx, stop := context.WithTimeout(ctx, min(5*time.Second, requestTimeout+2*time.Second))
	defer stop()
	s, reason := publicDownload(speedCtx, t, speedTarget, c.MaxLatencyMS, budget)
	p.Speed = &s
	p.CheckedAt = time.Now().Unix()
	if reason != "" {
		return publicProbeResult{p, reason}
	}
	if s.Mbps < c.MinMbps {
		return publicProbeResult{p, "下载速度低于阈值"}
	}
	if p.ProbeIP != ip {
		p.Quality = nil
		p.Country = ""
		p.CountryCode = ""
	}
	p.ProbeIP = ip
	p.Reachable = true
	p.ProbedAt = p.CheckedAt
	p.ProbeError = ""
	p.Name = egressName(p.Country, ip)
	return publicProbeResult{p, ""}
}
