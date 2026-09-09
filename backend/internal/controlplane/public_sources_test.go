package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublicSourceQueuePermissionsValidationAndPersistence(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	path := "/api/public-pool/sources/fetch"
	for _, body := range []object{{"ids": []string{}}, {"ids": []string{"https://other.example/list"}}, {"ids": []string{"proxifly", "proxifly"}}} {
		if req(t, a, owner, "POST", path, body).Code != 400 {
			t.Fatal("invalid sources accepted")
		}
	}
	if req(t, a, member, "POST", path, object{"ids": []string{"proxifly"}}).Code != 403 {
		t.Fatal("member queued global fetch")
	}
	for range 2 {
		if req(t, a, owner, "POST", path, object{"ids": []string{"proxifly", "monosans-http"}}).Code != 202 {
			t.Fatal("queue rejected")
		}
	}
	reopened, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	for _, id := range []string{"proxifly", "monosans-http"} {
		if !reopened.publicSourceState(id).Queued {
			t.Fatal("queue lost after reopening")
		}
	}
	if a.store.publicSettings().Enabled {
		t.Fatal("fetch request enabled collection")
	}
	pools, _ := a.store.pools()
	if len(pools) != 0 {
		t.Fatal("unverified source became pool resource")
	}
}
func TestPublicSourceManualRefreshBypassesTTLAndKeepsConditionalCache(t *testing.T) {
	a := testApp(t)
	source := publicSources[1]
	calls := 0
	rt := publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		h.Set("ETag", "fixture-tag")
		if calls == 2 {
			if r.Header.Get("If-None-Match") != "fixture-tag" {
				t.Fatal("missing conditional request")
			}
			return &http.Response{StatusCode: 304, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("8.8.8.8:8080\n127.0.0.1:80\n8.8.8.8:8080\n"))}, nil
	})
	original, err := a.fetchPublicSourceMode(context.Background(), source.URL, false, rt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.fetchPublicSourceMode(context.Background(), source.URL, false, rt)
	if err != nil || calls != 1 {
		t.Fatal("automatic fetch ignored cache")
	}
	got, err := a.fetchPublicSourceMode(context.Background(), source.URL, true, rt)
	if err != nil || calls != 2 || !bytes.Equal(got, original) {
		t.Fatal("manual fetch failed conditional refresh")
	}
	state := a.store.publicSourceState(source.ID)
	if state.LastSuccess == 0 || state.Entries != 1 || state.HTTPStatus != 304 || state.Bytes != len(original) {
		t.Fatal("source observations missing")
	}
	bad := publicRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<html>blocked</html>"))}, nil
	})
	if _, err = a.fetchPublicSourceMode(context.Background(), source.URL, true, bad); err == nil {
		t.Fatal("invalid source accepted")
	}
	var saved []byte
	_ = a.store.db.QueryRow("SELECT body FROM public_source_cache WHERE id=?", source.ID).Scan(&saved)
	state = a.store.publicSourceState(source.ID)
	if !bytes.Equal(saved, original) || state.Error == "" || state.Entries != 1 {
		t.Fatal("failed fetch cleared previous valid source")
	}
}
func TestPublicSourceQueueSerialGapCooldownAndCancel(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	now := time.Now().Unix()
	path := "/api/public-pool/sources/fetch"
	req(t, a, owner, "POST", path, object{"ids": []string{"proxifly", "monosans-http"}})
	calls := 0
	fetch := func(context.Context, string) ([]byte, error) { calls++; return nil, errors.New("fixture failure") }
	a.heavyMu.Lock()
	if a.runPublicSource(context.Background(), fetch, now) {
		t.Fatal("overlapped heavy task")
	}
	a.heavyMu.Unlock()
	if !a.runPublicSource(context.Background(), fetch, now) || calls != 1 {
		t.Fatal("no dispatch")
	}
	if a.runPublicSource(context.Background(), fetch, now+14) || calls != 1 {
		t.Fatal("global gap ignored")
	}
	if !a.runPublicSource(context.Background(), fetch, now+15) || calls != 2 {
		t.Fatal("second source not dispatched")
	}
	req(t, a, owner, "POST", path, object{"ids": []string{"proxifly"}})
	if a.runPublicSource(context.Background(), fetch, now+59) {
		t.Fatal("per-source cooldown ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := func(context.Context, string) ([]byte, error) { cancel(); return nil, context.Canceled }
	if !a.runPublicSource(ctx, cancelled, now+60) || !a.store.publicSourceState("proxifly").Queued || a.publicSourceID != "" {
		t.Fatal("interrupted fetch not left recoverable")
	}
}
func TestPublicSourceFetchQueuesScreeningOnlyForEnabledSources(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	c := publicTestSettings(t, a)
	now := time.Now().Unix()
	path := "/api/public-pool/sources/fetch"
	fetch := func(context.Context, string) ([]byte, error) { return []byte("http://8.8.8.8:80"), nil }
	c.Enabled = false
	savePublicTestSettings(t, a, c)
	req(t, a, owner, "POST", path, object{"ids": []string{"proxifly"}})
	a.runPublicSource(context.Background(), fetch, now)
	if a.store.meta("public_source_screen_pending") == "1" {
		t.Fatal("disabled collection was scheduled")
	}
	c.Enabled = true
	c.Sources = []string{"monosans-http"}
	savePublicTestSettings(t, a, c)
	req(t, a, owner, "POST", path, object{"ids": []string{"proxifly"}})
	a.runPublicSource(context.Background(), fetch, now+61)
	if a.store.meta("public_source_screen_pending") == "1" {
		t.Fatal("disabled source triggered collection")
	}
	req(t, a, owner, "POST", path, object{"ids": []string{"monosans-http"}})
	a.runPublicSource(context.Background(), fetch, now+80)
	if a.store.meta("public_source_screen_pending") != "1" {
		t.Fatal("enabled source did not schedule screening")
	}
}

func savePublicTestSettings(t *testing.T, a *App, c PublicSettings) {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.setMeta("public_settings", string(b)); err != nil {
		t.Fatal(err)
	}
}
