package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

type Egress struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	Name        string `json:"name"`
	Warning     string `json:"warning"`
}

func egressTransport(n Node) (*http.Transport, error) {
	if err := validateNode(n); err != nil {
		return nil, err
	}
	n = effectiveExit(n)
	t := &http.Transport{TLSHandshakeTimeout: 4 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext}
	switch n.Exit {
	case "http":
		u := &url.URL{Scheme: "http", Host: net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}
		if n.Username != "" {
			u.User = url.UserPassword(n.Username, n.Password)
		}
		t.Proxy = http.ProxyURL(u)
	case "socks5":
		var auth *proxy.Auth
		if n.Username != "" {
			auth = &proxy.Auth{User: n.Username, Password: n.Password}
		}
		d, err := proxy.SOCKS5("tcp", net.JoinHostPort(n.Host, strconv.Itoa(n.Port)), auth, &net.Dialer{Timeout: 4 * time.Second})
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5 context unavailable")
		}
		t.DialContext = cd.DialContext
	}
	return t, nil
}

func probeNode(ctx context.Context, n Node) (Egress, error) {
	t, err := egressTransport(n)
	if err != nil {
		return Egress{}, err
	}
	defer t.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return detectEgress(ctx, &http.Client{Transport: t, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}

func probeGet(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Guangyue-Personal/"+version)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("probe service unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if len(b) > int(limit) {
		return nil, errors.New("probe response too large")
	}
	return b, err
}

func detectEgress(ctx context.Context, client *http.Client) (Egress, error) {
	b, err := probeGet(ctx, client, "https://api.ipify.org", 128)
	ip := net.ParseIP(strings.TrimSpace(string(b)))
	if err != nil || ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return Egress{}, errors.New("出口 HTTPS 检测失败，未返回有效公网 IP；请检查地址、认证与连通性")
	}
	r := Egress{IP: ip.String()}
	// Query the observed address explicitly: rotating proxies may change IP between requests.
	b, err = probeGet(ctx, client, "https://ipwho.is/"+r.IP+"?fields=success,ip,country_code", 4096)
	var geo struct {
		Success bool   `json:"success"`
		IP      string `json:"ip"`
		Code    string `json:"country_code"`
	}
	if err == nil && json.Unmarshal(b, &geo) == nil && geo.Success && ip.Equal(net.ParseIP(geo.IP)) {
		code := strings.ToUpper(geo.Code)
		region, parseErr := language.ParseRegion(code)
		if parseErr == nil && region.IsCountry() && len(code) == 2 {
			r.CountryCode = code
			r.Country = display.SimplifiedChinese.Regions().Name(region)
		}
	}
	if r.Country == "" {
		r.Warning = "出口已联通，国家查询暂不可用"
	}
	r.Name = egressName(r.Country, r.IP)
	return r, nil
}

func egressName(country, ip string) string {
	if country == "" {
		country = "国家待识别"
	}
	return country + " · " + ip
}

func sameExit(a, b Node) bool {
	return upstreamKey(a.Upstream) == upstreamKey(b.Upstream) && a.BridgePort == b.BridgePort && a.BridgePassword == b.BridgePassword && a.Exit == b.Exit && a.Host == b.Host && a.Port == b.Port && a.Username == b.Username && a.Password == b.Password
}

func applyEgress(n *Node, result Egress, err error) {
	n.CheckedAt = time.Now().Unix()
	if err != nil {
		n.ProbeError = err.Error()
		return
	}
	if n.Quality != nil && n.Quality.IP != result.IP {
		n.Quality = nil
	}
	if result.Country == "" && n.ProbeIP == result.IP && n.Country != "" {
		result.Country, result.CountryCode = n.Country, n.CountryCode
	}
	n.ProbeIP, n.Country, n.CountryCode = result.IP, result.Country, result.CountryCode
	n.Name = egressName(n.Country, n.ProbeIP)
	n.ProbedAt, n.ProbeError = n.CheckedAt, result.Warning
}

// Persist only if the tested exit is still current; network I/O never holds the control lock.
func (a *App) storeEgress(snapshot Node, result Egress, probeErr error) (Node, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	nodes, err := a.store.nodes()
	if err != nil {
		return Node{}, err
	}
	for _, n := range nodes {
		if n.ID == snapshot.ID {
			if !sameExit(n, snapshot) || n.CheckedAt > snapshot.CheckedAt {
				return Node{}, errors.New("出口配置或检测结果已更新，请重新检测")
			}
			if n.ExitID != "" {
				p, e := a.store.pool(n.ExitID)
				if e != nil || !sameExit(n, p.Node) {
					return Node{}, errors.New("IP 池配置已变更，请刷新后重试")
				}
				if _, e = a.storePoolEgressLocked(p, result, probeErr); e != nil {
					return Node{}, e
				}
				updated, e := a.store.nodes()
				for _, node := range updated {
					if node.ID == n.ID {
						return node, e
					}
				}
				return Node{}, errors.New("节点已不存在")
			}
			applyEgress(&n, result, probeErr)
			return n, a.store.saveNode(n)
		}
	}
	return Node{}, errors.New("节点已不存在")
}

func (a *App) detectNode(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/detect")
	if !a.probeMu.TryLock() {
		failure(w, 409, "已有出口检测正在进行，请稍后重试")
		return
	}
	defer a.probeMu.Unlock()
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	for _, n := range nodes {
		if n.ID != id {
			continue
		}
		result, probeErr := probeNode(r.Context(), n)
		updated, err := a.storeEgress(n, result, probeErr)
		if err != nil {
			failure(w, 409, err.Error())
			return
		}
		if probeErr != nil {
			failure(w, 400, probeErr.Error())
			return
		}
		jsonResponse(w, 200, updated.public())
		return
	}
	failure(w, 404, "节点不存在")
}

func (a *App) refreshEgress(ctx context.Context) {
	if !a.probeMu.TryLock() {
		return
	}
	defer a.probeMu.Unlock()
	nodes, err := a.store.nodes()
	if err != nil {
		return
	}
	type cached struct {
		node   Node
		result Egress
		err    error
	}
	cache := []cached{}
	for _, n := range nodes {
		interval := time.Hour
		if n.ProbeError != "" {
			interval = 15 * time.Minute
		}
		if n.ExitID != "" || !n.Enabled || n.CountryCode != "" && time.Since(time.Unix(n.CheckedAt, 0)) < interval || n.ProbeError != "" && time.Since(time.Unix(n.CheckedAt, 0)) < interval {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		var result Egress
		var probeErr error
		found := false
		for _, c := range cache {
			if sameExit(c.node, n) {
				result, probeErr, found = c.result, c.err, true
				break
			}
		}
		if !found {
			result, probeErr = probeNode(ctx, n)
			cache = append(cache, cached{n, result, probeErr})
		}
		_, _ = a.storeEgress(n, result, probeErr)
	}
}

func (a *App) egressLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		a.refreshPools(ctx)
		a.refreshEgress(ctx)
		if ctx.Err() == nil {
			a.refreshQuality(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
