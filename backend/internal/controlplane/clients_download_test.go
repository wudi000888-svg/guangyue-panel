package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testClientAsset = "https://github.com/example/client/releases/download/v1/client.zip"

func clientRelayFixture(t *testing.T, handler http.HandlerFunc) *clientDownloadRelay {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)
	relay := newClientDownloadRelay()
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	// Keep certificate verification enabled against the test server's IP SAN.
	testHost, _, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	transport.TLSClientConfig.ServerName = testHost
	// Tests keep the trusted HTTPS hostname and redirect policy while routing
	// its TCP connection to an isolated test origin. Production uses publicSourceDial.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	relay.client = &http.Client{Transport: transport, CheckRedirect: clientDownloadRedirect}
	relay.maxBytes = 1024
	relay.cacheBytes = 2048
	relay.timeout = 2 * time.Second
	t.Cleanup(relay.close)
	return relay
}

func waitClientDownload(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for relay state")
}

func relayGet(relay *clientDownloadRelay, raw string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	relay.serve(w, httptest.NewRequest("GET", "/download", nil), raw, "client.zip")
	return w
}

func TestClientDownloadTrustBoundary(t *testing.T) {
	for _, raw := range []string{
		"http://github.com/example/client/releases/download/v1/a.zip",
		"https://github.com.evil.test/example/client/releases/download/v1/a.zip",
		"https://github.com@127.0.0.1/a.zip",
		"https://github.com:8443/example/client/releases/download/v1/a.zip",
		"https://github.com/example/client/releases/latest",
		"https://github.com/example/client/releases/download/v1/a.html",
		"https://github.com/example/client/releases/download/v1/a.zip?url=http://localhost",
		"https://release-assets.githubusercontent.com/asset.zip",
		"https://github.com/example/client/releases/download/v1/..%2fa.zip",
	} {
		if _, err := clientDownloadURL(raw, true); err == nil {
			t.Fatalf("unsafe initial URL accepted: %s", raw)
		}
	}
	if _, err := clientDownloadURL(testClientAsset, true); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com"} {
		r := httptest.NewRequest("GET", "https://"+host+"/asset?signature=secret", nil)
		r.Header.Set("Cookie", "private")
		r.Header.Set("Authorization", "secret")
		r.Header.Set("Referer", "private")
		if err := clientDownloadRedirect(r, []*http.Request{{}}); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			t.Fatal("credentials forwarded")
		}
	}
	r := httptest.NewRequest("GET", testClientAsset, nil)
	if clientDownloadRedirect(r, make([]*http.Request, 5)) == nil {
		t.Fatal("unbounded redirects")
	}
	for _, address := range []string{"127.0.0.1:443", "[::1]:443", "10.0.0.1:443", "169.254.169.254:443"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := publicSourceDial(ctx, "tcp", address)
		cancel()
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatalf("private address accepted: %s", address)
		}
	}
}

func TestClientDownloadRedirectRejectedAndSanitized(t *testing.T) {
	for _, target := range []string{"http://objects.githubusercontent.com/a.zip", "https://evil.test/a.zip?private_token=secret", "https://127.0.0.1/a.zip", "https://github.com/example/client/blob/main/a.zip"} {
		t.Run(target, func(t *testing.T) {
			var calls atomic.Int32
			relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Redirect(w, r, target, 302) })
			w := relayGet(relay, testClientAsset)
			if w.Code != 502 || calls.Load() != 1 || strings.Contains(w.Body.String(), "private_token") || strings.Contains(w.Body.String(), target) {
				t.Fatalf("untrusted redirect was followed or exposed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestClientDownloadTrustedAssetRedirect(t *testing.T) {
	var calls atomic.Int32
	relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host == "github.com" {
			http.Redirect(w, r, "https://release-assets.githubusercontent.com/asset?signature=test-only", 302)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "PK-official-asset")
	})
	w := relayGet(relay, testClientAsset)
	if w.Code != 200 || w.Body.String() != "PK-official-asset" || calls.Load() != 2 {
		t.Fatalf("trusted release asset redirect failed: %d %q", w.Code, w.Body.String())
	}
}

func TestClientDownloadSharedFetchRangeAndHead(t *testing.T) {
	const body = "PK-test-installer-content"
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("browser credentials reached upstream")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = io.WriteString(w, body[:3])
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-finish:
			_, _ = io.WriteString(w, body[3:])
		case <-r.Context().Done():
		}
	})
	head := httptest.NewRecorder()
	relay.serve(head, httptest.NewRequest("HEAD", "/download", nil), testClientAsset, "client.zip")
	if calls.Load() != 0 || head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("HEAD downloaded an uncached file")
	}
	first, second := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- relayGet(relay, testClientAsset) }()
	select {
	case <-started:
	case early := <-first:
		t.Fatalf("upstream failed before streaming: %d %s", early.Code, early.Body.String())
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not start")
	}
	go func() { second <- relayGet(relay, testClientAsset) }()
	waitClientDownload(t, func() bool { relay.mu.Lock(); defer relay.mu.Unlock(); return relay.entries[testClientAsset].refs == 2 })
	close(finish)
	for _, w := range []*httptest.ResponseRecorder{<-first, <-second} {
		if w.Code != 200 || w.Body.String() != body || w.Header().Get("Content-Disposition") != "attachment; filename=client.zip" {
			t.Fatalf("bad stream: %d %q", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("shared file fetched %d times", calls.Load())
	}
	request := httptest.NewRequest("GET", "/download", nil)
	request.Header.Set("Range", "bytes=3-6")
	w := httptest.NewRecorder()
	relay.serve(w, request, testClientAsset, "client.zip")
	if w.Code != 206 || w.Body.String() != body[3:7] || w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("ETag") == "" || calls.Load() != 1 {
		t.Fatalf("cached Range failed: %d %q", w.Code, w.Body.String())
	}
	head = httptest.NewRecorder()
	relay.serve(head, httptest.NewRequest("HEAD", "/download", nil), testClientAsset, "client.zip")
	if head.Code != 200 || head.Header().Get("Content-Length") != fmt.Sprint(len(body)) || head.Body.Len() != 0 || calls.Load() != 1 {
		t.Fatal("cached HEAD failed")
	}
}

func TestClientDownloadCancellationAndLimits(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "PK")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(canceled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/download", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); relay.serve(httptest.NewRecorder(), request, testClientAsset, "client.zip") }()
	select {
	case <-started:
	case <-done:
		t.Fatal("upstream failed before cancellation test")
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not start")
	}
	cancel()
	<-done
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream continued after all clients canceled")
	}
	waitClientDownload(t, func() bool { relay.mu.Lock(); defer relay.mu.Unlock(); return len(relay.entries) == 0 })
	files, err := os.ReadDir(relay.dir)
	if err != nil || len(files) != 0 {
		t.Fatal("partial cache survived cancellation", err)
	}
	for i := 0; i < cap(relay.requestSlots); i++ {
		relay.requestSlots <- struct{}{}
	}
	w := relayGet(relay, testClientAsset)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatal("concurrency limit not enforced")
	}
	for i := 0; i < cap(relay.requestSlots); i++ {
		<-relay.requestSlots
	}
}

func TestClientDownloadSizeAndCacheEviction(t *testing.T) {
	var calls atomic.Int32
	relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "1234567890")
	})
	relay.maxBytes = 16
	relay.cacheBytes = 32
	for _, name := range []string{"a.zip", "b.zip", "c.zip"} {
		if w := relayGet(relay, strings.Replace(testClientAsset, "client.zip", name, 1)); w.Code != 200 {
			t.Fatalf("cache request failed %d", w.Code)
		}
	}
	relay.mu.Lock()
	_, firstPresent := relay.entries[strings.Replace(testClientAsset, "client.zip", "a.zip", 1)]
	count := len(relay.entries)
	relay.mu.Unlock()
	if firstPresent || count != 2 || calls.Load() != 3 {
		t.Fatal("cache did not evict oldest idle entry")
	}
	oversized := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1025")
		w.WriteHeader(200)
	})
	if w := relayGet(oversized, testClientAsset); w.Code != 502 {
		t.Fatal("oversized file accepted", w.Code)
	}
	html := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>login</html>")
	})
	if w := relayGet(html, testClientAsset); w.Code != 502 {
		t.Fatal("HTML accepted as installer", w.Code)
	}
}

func TestClientDownloadTruncatedStreamFailsClient(t *testing.T) {
	relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "PK-truncated")
		w.(http.Flusher).Flush()
	})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { relay.serve(w, r, testClientAsset, "client.zip") }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		return
	} // connection may abort before the response reaches the client
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	// Some Go/http combinations surface a short Content-Length response as a
	// clean EOF after the server has closed the connection. It is still a
	// failed transfer: only a complete body with the declared length is a
	// successful installer response.
	if err == nil && response.StatusCode == 200 && response.ContentLength >= 0 &&
		int64(len(body)) == response.ContentLength {
		t.Fatal("truncated installer was reported as a successful transfer")
	}
}

func TestClientDownloadFailedReadersRemainInDiskBudget(t *testing.T) {
	relay := clientRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "PK-short")
		w.(http.Flusher).Flush()
	})
	relay.cacheBytes = relay.maxBytes
	entry, err := relay.acquire(testClientAsset, "client.zip", false)
	if err != nil {
		t.Fatal(err)
	}
	waitClientDownload(t, func() bool { relay.mu.Lock(); defer relay.mu.Unlock(); return entry.done })
	if entry.err == nil {
		t.Fatal("truncated fetch did not fail")
	}
	if _, err := os.Stat(entry.file); err != nil {
		t.Fatal("failed file unlinked while a reader still holds it", err)
	}
	other, err := relay.acquire(strings.Replace(testClientAsset, "client.zip", "other.zip", 1), "other.zip", false)
	if other != nil {
		relay.release(other)
	}
	if err == nil {
		t.Fatal("failed reader's disk allocation was not charged")
	}
	relay.release(entry)
	if _, err := os.Stat(entry.file); !os.IsNotExist(err) {
		t.Fatal("failed file remained after last reader closed")
	}
}

func TestClientDownloadAuthenticationToggleAndCatalog(t *testing.T) {
	a := testApp(t)
	user := testUser(t, a, "download-member", "user")
	const target = "/api/clients/download/clash-verge/0"
	if w := req(t, a, Record{}, "GET", target, nil); w.Code != 401 {
		t.Fatal("anonymous client download allowed", w.Code)
	}
	if w := req(t, a, user, "GET", target, nil); w.Code != 503 {
		t.Fatal("disabled client relay allowed", w.Code)
	}
	site := a.store.siteSettings()
	site.ClientDownloadRelay = true
	data, _ := json.Marshal(site)
	if err := a.store.setMeta("site_settings", string(data)); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/clients/download/missing/0", "/api/clients/download/clash-verge/-1", "/api/clients/download/clash-verge/00", "/api/clients/download/shadowrocket/0", "/api/clients/download/cfa/0", "/api/clients/download/clash-verge/0?url=https://example.com"} {
		if w := req(t, a, user, "GET", target, nil); w.Code != 404 {
			t.Fatalf("invalid catalog request %s: %d", target, w.Code)
		}
	}
	if w := req(t, a, user, "HEAD", target, nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("authenticated member cannot inspect download", w.Code)
	}
	a.closeClientDownloads()
}
