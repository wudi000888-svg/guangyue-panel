package controlplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicBlacklistPersistsExpiresAndRespectsChangedThresholds(t *testing.T) {
	a := testApp(t)
	c := publicTestSettings(t, a)
	p := publicTestPool("8.8.8.8")
	slow := publicTestPool("1.1.1.1")
	if _, err := a.store.blockPublic(p, "HTTPS 不通", c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.blockPublic(slow, "延迟超过阈值", c); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	blocks, err := reopened.publicBlocks(c)
	if err != nil || len(blocks) != 2 {
		t.Fatal("blacklist did not survive reopening")
	}
	if blocks[p.ID].ExpiresAt-blocks[p.ID].At != 86400 || blocks[slow.ID].ExpiresAt-blocks[slow.ID].At != 3600 {
		t.Fatal("wrong cooldown")
	}
	c.MaxLatencyMS++
	blocks, _ = reopened.publicBlocks(c)
	if len(blocks) != 1 || blocks[p.ID].ID == "" {
		t.Fatal("changed performance policy must preserve connectivity bans only")
	}
	_, _ = reopened.db.Exec("UPDATE public_blacklist SET expires=0")
	reopened.trimPublicBlocks()
	blocks, _ = reopened.publicBlocks(c)
	if len(blocks) != 0 {
		t.Fatal("expired blocks survived")
	}
}

func TestPublicPreflightBlacklistsWithoutTLSAndSkipsNextCycle(t *testing.T) {
	a := testApp(t)
	c := publicTestSettings(t, a)
	deps := publicTestChecks("socks5://8.8.8.8:1080\nsocks5://1.1.1.1:1080")
	var tcp, tls atomic.Int32
	deps.preflight = func(context.Context, IPResource) error { tcp.Add(1); return errors.New("refused") }
	deps.performance = func(_ context.Context, p IPResource, _ PublicSettings) publicProbeResult {
		tls.Add(1)
		return publicProbeResult{p, ""}
	}
	a.collectPublicWith(context.Background(), c, deps)
	if tcp.Load() != 2 || tls.Load() != 0 {
		t.Fatal("TCP rejection used expensive checks")
	}
	a.collectPublicWith(context.Background(), c, deps)
	if tcp.Load() != 2 || tls.Load() != 0 || a.publicStatus.Skipped != 2 {
		t.Fatal("blacklisted candidates were probed again")
	}
	blocks, _ := a.store.publicBlocks(c)
	if len(blocks) != 2 {
		t.Fatal("failures missing from persistent blacklist")
	}
}

func TestPublicPipelineHasBoundedParallelism(t *testing.T) {
	a := testApp(t)
	c := publicTestSettings(t, a)
	c.CandidateLimit = 8
	deps := publicTestChecks("socks5://8.8.8.1:1080\nsocks5://8.8.8.2:1080\nsocks5://8.8.8.3:1080\nsocks5://8.8.8.4:1080\nsocks5://8.8.8.5:1080\nsocks5://8.8.8.6:1080\nsocks5://8.8.8.7:1080\nsocks5://8.8.8.8:1080")
	var tcp, tls, tcpPeak, tlsPeak atomic.Int32
	peak := func(p *atomic.Int32, n int32) {
		for old := p.Load(); n > old; old = p.Load() {
			if p.CompareAndSwap(old, n) {
				return
			}
		}
	}
	tcpGate := make(chan struct{})
	deps.preflight = func(context.Context, IPResource) error {
		n := tcp.Add(1)
		peak(&tcpPeak, n)
		if n == 8 {
			close(tcpGate)
		}
		select {
		case <-tcpGate:
		case <-time.After(time.Second):
		}
		tcp.Add(-1)
		return nil
	}
	deps.performance = func(_ context.Context, p IPResource, _ PublicSettings) publicProbeResult {
		n := tls.Add(1)
		peak(&tlsPeak, n)
		time.Sleep(3 * time.Millisecond)
		tls.Add(-1)
		return publicProbeResult{p, "HTTPS 不通"}
	}
	a.collectPublicWith(context.Background(), c, deps)
	if tcpPeak.Load() != 8 || tlsPeak.Load() != 2 {
		t.Fatalf("pipeline concurrency TCP=%d HTTPS=%d", tcpPeak.Load(), tlsPeak.Load())
	}
}

type publicRoundTrip func(*http.Request) (*http.Response, error)

func (f publicRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type publicCountReader struct {
	reads int
	bytes int64
}

func (r *publicCountReader) Read(b []byte) (int, error) {
	r.reads++
	if r.bytes == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(b)), r.bytes)
	r.bytes -= n
	return int(n), nil
}

func TestPublicDownloadRejectsLatencyBeforeReadingAndCapsTotalSample(t *testing.T) {
	ctx := context.Background()
	budget := &publicDownloadBudget{}
	body := &publicCountReader{bytes: publicSampleBytes}
	rt := publicRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(body)}, nil
	})
	s, reason := publicDownload(ctx, rt, "https://sample.test", 0, budget)
	if reason != "延迟超过阈值" || s.Bytes != 0 || body.reads != 0 || budget.reserved.Load() != 0 {
		t.Fatal("slow headers consumed sample body/budget")
	}
	rt = publicRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(&publicCountReader{bytes: publicSampleBytes})}, nil
	})
	var total int64
	for i := int32(0); i < publicDownloadSamples; i++ {
		s, reason = publicDownload(ctx, rt, "https://sample.test", 10000, budget)
		if reason != "" {
			t.Fatal(reason)
		}
		total += s.Bytes
	}
	_, reason = publicDownload(ctx, rt, "https://sample.test", 10000, budget)
	if total != 4<<20 || reason != "本轮下载预算已用完" {
		t.Fatal("download sample budget was not enforced")
	}
}

func TestPublicBudgetAndCancellationDoNotBlacklistHealthyCandidates(t *testing.T) {
	a := testApp(t)
	c := publicTestSettings(t, a)
	deps := publicTestChecks("socks5://8.8.8.8:1080")
	deps.performance = func(_ context.Context, p IPResource, _ PublicSettings) publicProbeResult {
		return publicProbeResult{p, "本轮下载预算已用完"}
	}
	a.collectPublicWith(context.Background(), c, deps)
	blocks, _ := a.store.publicBlocks(c)
	if len(blocks) != 0 {
		t.Fatal("budget limitation became a blacklist")
	}
	ctx, cancel := context.WithCancel(context.Background())
	deps.preflight = func(context.Context, IPResource) error { cancel(); return errors.New("cancelled") }
	a.collectPublicWith(ctx, c, deps)
	blocks, _ = a.store.publicBlocks(c)
	if len(blocks) != 0 {
		t.Fatal("cancelled scan blacklisted candidates")
	}
}

func TestPublicSourceCacheAvoidsRequestsAndUsesConditionalRefresh(t *testing.T) {
	a := testApp(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") == "fixture-v1" {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", "fixture-v1")
		_, _ = io.WriteString(w, "8.8.8.8:8080\n")
	}))
	defer srv.Close()
	original := publicSources
	publicSources = append(append([]PublicSource{}, original...), PublicSource{ID: "cache-test", URL: srv.URL, Protocol: "http"})
	defer func() { publicSources = original }()
	b, err := a.fetchPublicSource(context.Background(), srv.URL)
	if err != nil || !bytes.Contains(b, []byte("8.8.8.8")) {
		t.Fatal("initial source fetch failed")
	}
	_, err = a.fetchPublicSource(context.Background(), srv.URL)
	if err != nil || requests.Load() != 1 {
		t.Fatal("fresh source cache made another request")
	}
	_, _ = a.store.db.Exec("UPDATE public_source_cache SET fetched=0 WHERE id='cache-test'")
	b, err = a.fetchPublicSource(context.Background(), srv.URL)
	if err != nil || requests.Load() != 2 || !bytes.Contains(b, []byte("8.8.8.8")) {
		t.Fatal("conditional source refresh failed")
	}
}

func TestPublicIPDetectionUsesFallbackAndTraceResponse(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/failed" {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, "fl=fixture\nip=8.8.8.8\nloc=US\n")
	}))
	defer srv.Close()
	ip := publicFindIP(context.Background(), srv.Client(), []string{srv.URL + "/failed", srv.URL + "/trace"})
	if ip != "8.8.8.8" || requests.Load() != 2 {
		t.Fatal("fallback target did not return observed IP")
	}
	if publicResponseIP("ip=127.0.0.1\n") != "" || publicResponseIP(strings.Repeat("x", 20)) != "" {
		t.Fatal("invalid IP response accepted")
	}
}
