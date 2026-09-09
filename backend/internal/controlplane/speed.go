package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const speedBytes int64 = 8 << 20
const speedURL = "https://speed.cloudflare.com/__down?bytes=8388608"

type SpeedResult struct {
	At         int64   `json:"at"`
	LatencyMS  int64   `json:"latency_ms"`
	Mbps       float64 `json:"mbps"`
	Bytes      int64   `json:"bytes"`
	DurationMS int64   `json:"duration_ms"`
	Partial    bool    `json:"partial"`
	Error      string  `json:"error,omitempty"`
}

func measureSpeed(ctx context.Context, t http.RoundTripper, address string, limit int64) SpeedResult {
	result := SpeedResult{At: time.Now().Unix()}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	client := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "Guangyue/"+version)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		result.Error = "测速连接失败，请检查出口连通性"
		return result
	}
	defer resp.Body.Close()
	result.LatencyMS = max(1, time.Since(start).Milliseconds())
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		result.Error = "测速服务暂不可用，请稍后重试"
		return result
	}
	download := time.Now()
	result.Bytes, err = io.CopyBuffer(io.Discard, io.LimitReader(resp.Body, limit), make([]byte, 32<<10))
	elapsed := time.Since(download)
	result.DurationMS = max(1, elapsed.Milliseconds())
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		result.Error = "下载中断，请重试"
		return result
	}
	if result.Bytes < 64<<10 {
		result.Error = "有效下载数据不足，请重试"
		return result
	}
	result.Partial = result.Bytes < limit
	result.Mbps = float64(result.Bytes) * 8 / elapsed.Seconds() / 1e6
	return result
}
func (a *App) speedTest(w http.ResponseWriter, r *http.Request, actor Record) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 5 {
		failure(w, 404, "资源不存在")
		return
	}
	result, status, err := a.measureResourceSpeed(r.Context(), parts[2] == "ips", parts[3], actor)
	if err != nil {
		failure(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, result)
}
func (a *App) measureResourceSpeed(ctx context.Context, pool bool, id string, actor Record) (SpeedResult, int, error) {
	if !a.heavyMu.TryLock() {
		return SpeedResult{}, 409, errors.New("后台任务繁忙，请稍后重试")
	}
	defer a.heavyMu.Unlock()
	if !a.speedMu.TryLock() {
		return SpeedResult{}, 409, errors.New("已有测速正在进行，请稍后重试")
	}
	defer a.speedMu.Unlock()
	var snapshot Node
	a.mu.Lock()
	if pool {
		p, err := a.store.pool(id)
		if err == nil {
			snapshot = p.Node
		}
	} else {
		nodes, _ := a.store.nodes()
		for _, n := range nodes {
			if n.ID == id {
				snapshot = n
			}
		}
	}
	a.mu.Unlock()
	if snapshot.ID == "" {
		return SpeedResult{}, 404, errors.New("资源不存在")
	}
	if !snapshot.Enabled {
		return SpeedResult{}, 400, errors.New("请先启用再测速")
	}
	t, err := egressTransport(snapshot)
	if err != nil {
		return SpeedResult{}, 400, errors.New("出口配置无效")
	}
	defer t.CloseIdleConnections()
	result := measureSpeed(ctx, t, speedURL, speedBytes)
	a.mu.Lock()
	defer a.mu.Unlock()
	if pool {
		p, e := a.store.pool(id)
		if e != nil || !sameExit(p.Node, snapshot) || !p.Enabled {
			return SpeedResult{}, 409, errors.New("出口已变更，测速结果未保存")
		}
		p.Speed = &result
		err = a.store.savePoolNodes([]IPResource{p}, nil)
	} else {
		nodes, e := a.store.nodes()
		if e != nil {
			return SpeedResult{}, 500, errors.New("读取失败")
		}
		found := false
		for _, n := range nodes {
			if n.ID == id {
				if !sameExit(n, snapshot) || !n.Enabled {
					break
				}
				found = true
				n.Speed = &result
				err = a.store.saveNode(n)
				break
			}
		}
		if !found {
			return SpeedResult{}, 409, errors.New("节点出口已变更，测速结果未保存")
		}
	}
	if err != nil {
		return SpeedResult{}, 500, errors.New("保存测速结果失败")
	}
	a.store.audit(actor.Username, "speed_test", snapshot.Name)
	return result, 200, nil
}
