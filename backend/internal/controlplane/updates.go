package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var updaterClient = &http.Client{Timeout: 28 * time.Second, Transport: &http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", "/run/guangyue-update.sock")
	},
	MaxConnsPerHost: 2, MaxIdleConnsPerHost: 1, IdleConnTimeout: 15 * time.Second,
}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
var updateRequestID = regexp.MustCompile(`^[a-f0-9-]{36}$`)

func (a *App) updatesAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	path := strings.TrimPrefix(r.URL.Path, "/api/updates")
	var body []byte
	switch {
	case path == "" && r.Method == "GET":
		path = "/state"
	case path == "/check" && r.Method == "POST":
		var in struct{}
		if !decode(w, r, &in) {
			return
		}
		body = []byte(`{}`)
	case path == "/apply" && r.Method == "POST":
		var in struct {
			Action          string `json:"action"`
			Version         string `json:"version"`
			ExpectedVersion string `json:"expected_version"`
			RequestID       string `json:"request_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if (in.Action != "update" && in.Action != "rollback") || !releaseVersion.MatchString(in.Version) || !releaseVersion.MatchString(in.ExpectedVersion) || !updateRequestID.MatchString(in.RequestID) {
			failure(w, 400, "更新请求无效")
			return
		}
		body, _ = json.Marshal(in)
	default:
		failure(w, 404, "接口不存在")
		return
	}
	client := a.updateClient
	if client == nil {
		client = updaterClient
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, "http://updater"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		if r.Method == "GET" {
			jsonResponse(w, 200, object{"available": false, "current_version": version, "error": "此部署尚未启用在线更新服务"})
			return
		}
		failure(w, 503, "更新服务暂不可用，请稍后重试")
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || !json.Valid(data) || (response.StatusCode != 200 && response.StatusCode != 409) {
		failure(w, 502, "更新服务响应无效")
		return
	}
	if path == "/apply" && response.StatusCode == 200 {
		a.store.audit(actor.Username, "version_operation", string(body))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}
