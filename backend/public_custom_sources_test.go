package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type publicSourceResult struct {
	Source    PublicSource      `json:"source"`
	State     PublicSourceState `json:"source_status"`
	Entries   int               `json:"entries"`
	Queued    bool              `json:"queued"`
	Screening bool              `json:"screening_pending"`
}

func addPublicTestSource(t *testing.T, a *App, owner Record, input object) publicSourceResult {
	t.Helper()
	return decoded[publicSourceResult](t, req(t, a, owner, "POST", "/api/public-pool/sources", input), 201)
}

func TestCustomPublicSourcesPermissionsEncryptionAndPersistence(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	path := "/api/public-pool/sources"
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		p := path
		if method == "PUT" || method == "DELETE" {
			p += "/anything"
		}
		if req(t, a, member, method, p, object{}).Code != 403 || req(t, a, Record{}, method, p, object{}).Code != 401 {
			t.Fatal("unauthorized custom source access")
		}
	}
	address := "https://provider.example/list/fixture-private-token?key=fixture-secret"
	v := addPublicTestSource(t, a, owner, object{"name": "Provider", "url": address, "enabled": true})
	if v.Source.URL != "" || v.Source.Address != "https://provider.example/••••" || !v.Queued || v.Screening {
		t.Fatal("unsafe source display or disabled collection changed")
	}
	var encrypted []byte
	if err := a.store.db.QueryRow("SELECT doc FROM public_custom_sources WHERE id=?", v.Source.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("fixture-private-token")) {
		t.Fatal("unencrypted source")
	}
	for _, p := range []string{path, "/api/public-pool"} {
		w := req(t, a, owner, "GET", p, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-private-token") || strings.Contains(w.Body.String(), "fixture-secret") {
			t.Fatal("source URL exposed")
		}
	}
	second, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()
	catalog, err := second.publicSourceCatalog()
	if err != nil || len(catalog) != len(publicSources)+1 || catalog[len(catalog)-1].URL != address || !second.publicSourceState(v.Source.ID).Queued {
		t.Fatal("source or queue did not persist")
	}
	if req(t, a, owner, "POST", path, object{"name": "Duplicate", "url": address, "enabled": true}).Code != 409 {
		t.Fatal("duplicate source allowed")
	}
}

func TestCustomPublicSourceURLRejectsPrivateAndRedirectTargets(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for _, address := range []string{"http://provider.example/list", "https://localhost/list", "https://127.0.0.1/list", "https://[::ffff:127.0.0.1]/list", "https://10.0.0.1/list", "https://169.254.169.254/list", "https://proxy.local/list", "https://proxy.localhost/list", "https://user:pass@provider.example/list", "https://provider.example/list#token", "https://provider.example:65536/list", "https://192.0.2.1/list", "https://[2001:db8::1]/list"} {
		if validatePublicSourceURL(address) == nil {
			t.Fatalf("unsafe URL accepted: %s", address)
		}
		if req(t, a, owner, "POST", "/api/public-pool/sources", object{"name": "Bad", "url": address, "enabled": true}).Code != 400 {
			t.Fatal("unsafe source stored")
		}
	}
	for _, address := range []string{"https://provider.example/list?key=fixture", "https://8.8.8.8/list", "https://[2606:4700:4700::1111]/list"} {
		if validatePublicSourceURL(address) != nil {
			t.Fatal("public URL rejected")
		}
	}
	for _, address := range []string{"https://127.0.0.1:8080/list", "http://provider.example/list"} {
		r, _ := http.NewRequest("GET", address, nil)
		if publicSourceRedirect(r, []*http.Request{{}}) == nil {
			t.Fatal("unsafe redirect accepted")
		}
	}
	r, _ := http.NewRequest("GET", "https://other.example/list", nil)
	r.Header.Set("If-None-Match", "secret-validator")
	r.Header.Set("Referer", "https://provider.example/fixture-token")
	if publicSourceRedirect(r, []*http.Request{{}}) != nil || r.Header.Get("If-None-Match") != "" || r.Header.Get("Referer") != "" {
		t.Fatal("redirect validator leak")
	}
	if publicSourceRedirect(r, make([]*http.Request, 4)) == nil {
		t.Fatal("unbounded redirects")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := publicSourceDial(ctx, "tcp", "127.0.0.1:80"); err == nil {
		conn.Close()
		t.Fatal("dial reached private IP")
	}
}

func TestCustomPublicTextImportBoundsDedupAndNoInfrastructureMutation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	before, _ := a.store.nodes()
	v := addPublicTestSource(t, a, owner, object{"name": "File list", "text": "8.8.8.8:8080\n8.8.8.8:8080\n127.0.0.1:80\nsocks5://1.1.1.1:1080\nhttp://user:secret@9.9.9.9:80", "protocol": "http", "enabled": true})
	if v.Entries != 2 || v.State.LastSuccess == 0 || v.State.Bytes == 0 || v.Queued || v.Screening || v.Source.Kind != "text" {
		t.Fatal("text import observations incorrect")
	}
	var b []byte
	a.store.db.QueryRow("SELECT body FROM public_source_cache WHERE id=?", v.Source.ID).Scan(&b)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "127.0.0.1") || len(parsePublicList(b, PublicSource{})) != 2 {
		t.Fatal("invalid targets retained in normalized cache")
	}
	pools, _ := a.store.pools()
	after, _ := a.store.nodes()
	if len(pools) != 0 || !reflect.DeepEqual(before, after) || a.store.publicSettings().Enabled {
		t.Fatal("candidate import altered infrastructure")
	}
	if req(t, a, owner, "POST", "/api/public-pool/sources/fetch", object{"ids": []string{v.Source.ID}}).Code != 400 {
		t.Fatal("text source queued for HTTP fetch")
	}
	var excess strings.Builder
	for i := 1; i <= maxCustomPublicEntries+1; i++ {
		fmt.Fprintf(&excess, "8.8.8.8:%d\n", i)
	}
	for _, input := range []object{
		{"text": "127.0.0.1:80", "protocol": "http"},
		{"text": strings.Repeat("a", maxPublicSourceBytes+1)},
		{"text": excess.String(), "protocol": "http"},
		{"text": "8.8.8.8:80", "url": "https://provider.example/list"},
		{"text": "8.8.8.8:80", "protocol": "socks4"},
	} {
		input["name"] = "Invalid"
		if req(t, a, owner, "POST", "/api/public-pool/sources", input).Code != 400 {
			t.Fatal("invalid or overbudget list accepted")
		}
	}
}

func TestCustomPublicURLFetchUsesQueueAndPreservesGoodCache(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v := addPublicTestSource(t, a, owner, object{"name": "Remote", "url": "https://provider.example/list?token=fixture-secret", "protocol": "http", "enabled": true})
	catalog, _ := a.store.publicSourceCatalog()
	source := catalog[len(catalog)-1]
	calls := 0
	rt := publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		h.Set("ETag", "fixture-tag")
		if calls == 2 {
			if r.Header.Get("If-None-Match") != "fixture-tag" {
				t.Fatal("custom conditional fetch missing")
			}
			return &http.Response{StatusCode: 304, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("8.8.8.8:80\nhttp://user:secret@1.1.1.1:80"))}, nil
	})
	fetch := func(ctx context.Context, address string) ([]byte, error) {
		return a.fetchPublicSourceMode(ctx, address, true, rt)
	}
	if !a.runPublicSource(context.Background(), fetch, time.Now().Unix()) || calls != 1 {
		t.Fatal("custom source not dispatched")
	}
	original, err := a.fetchPublicSourceMode(context.Background(), source.URL, true, rt)
	if err != nil || calls != 2 || strings.Contains(string(original), "secret") {
		t.Fatal("custom cache normalization or refresh failed")
	}
	bad := publicRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fixture network failure with secret")
	})
	if _, err = a.fetchPublicSourceMode(context.Background(), source.URL, true, bad); err == nil {
		t.Fatal("failed refresh accepted")
	}
	var cached []byte
	a.store.db.QueryRow("SELECT body FROM public_source_cache WHERE id=?", v.Source.ID).Scan(&cached)
	state := a.store.publicSourceState(v.Source.ID)
	if !bytes.Equal(cached, original) || state.Entries != 1 || state.Error == "" || strings.Contains(state.Error, "secret") {
		t.Fatal("cache lost or error leaked secret")
	}
}

func TestCustomPublicDisableDeleteRetainsValidatedExitsAndGuardsRevision(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v := addPublicTestSource(t, a, owner, object{"name": "Manual list", "text": "socks5://8.8.8.8:1080", "enabled": true})
	pool := publicTestPool("8.8.8.8")
	pool.Source = v.Source.ID
	if err := a.applyPublicSetLocked([]IPResource{pool}); err != nil {
		t.Fatal(err)
	}
	beforeNodes, _ := a.store.nodes()
	beforeUser, _ := a.store.record(owner.ID)
	path := "/api/public-pool/sources/" + v.Source.ID
	if req(t, a, owner, "PUT", path, object{"name": "Changed", "enabled": false, "revision": "stale"}).Code != 409 {
		t.Fatal("stale overwrite")
	}
	updated := decoded[publicSourceResult](t, req(t, a, owner, "PUT", path, object{"name": "Changed", "enabled": false, "revision": v.Source.Revision}), 200)
	if containsSourceID(a.store.publicSettings().Sources, v.Source.ID) || updated.Source.Enabled {
		t.Fatal("disabled source still selected")
	}
	if req(t, a, owner, "POST", "/api/public-pool/sources/fetch", object{"ids": []string{v.Source.ID}}).Code != 400 {
		t.Fatal("disabled source queued")
	}
	a.heavyMu.Lock()
	busy := req(t, a, owner, "DELETE", path+"?revision="+updated.Source.Revision, nil)
	a.heavyMu.Unlock()
	if busy.Code != 409 {
		t.Fatal("deletion raced an active worker")
	}
	deleted := decoded[struct {
		Retained int `json:"retained_resources"`
	}](t, req(t, a, owner, "DELETE", path+"?revision="+updated.Source.Revision, nil), 200)
	if deleted.Retained != 1 {
		t.Fatal("deletion does not report retained exit")
	}
	afterNodes, _ := a.store.nodes()
	afterUser, _ := a.store.record(owner.ID)
	if !reflect.DeepEqual(beforeNodes, afterNodes) || !reflect.DeepEqual(beforeUser.Credentials, afterUser.Credentials) {
		t.Fatal("source deletion changed exits or subscription credentials")
	}
	for _, table := range []string{"public_custom_sources", "public_source_cache", "public_source_tasks"} {
		var count int
		a.store.db.QueryRow("SELECT count(*) FROM "+table+" WHERE id=?", v.Source.ID).Scan(&count)
		if count != 0 {
			t.Fatal("deleted source retained stored state")
		}
	}
	if req(t, a, owner, "DELETE", "/api/public-pool/sources/proxifly?revision=x", nil).Code != 400 {
		t.Fatal("builtin source deleted")
	}
}

func TestCustomPublicCandidatesUseScreeningBlacklistAndSeparateSubscriptions(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v := addPublicTestSource(t, a, owner, object{"name": "Candidates", "text": "socks5://8.8.8.8:1080\nsocks5://1.1.1.1:1080", "enabled": true})
	c := a.store.publicSettings()
	c.Enabled = true
	c.Sources = []string{v.Source.ID}
	savePublicTestSettings(t, a, c)
	checks := publicTestChecks("")
	checks.fetch = func(context.Context, string) ([]byte, error) {
		t.Error("text import attempted network source fetch")
		return nil, errors.New("unexpected")
	}
	checked := 0
	checks.preflight = func(_ context.Context, p IPResource) error {
		checked++
		if p.Host == "8.8.8.8" {
			return errors.New("unreachable")
		}
		return nil
	}
	// Block one candidate before the cycle; it must consume no network checks.
	blocked := publicTestPool("8.8.8.8")
	a.store.blockPublic(blocked, "TCP 不通", c)
	before, _ := a.store.record(owner.ID)
	a.collectPublicWith(context.Background(), c, checks)
	pools, _ := a.store.pools()
	nodes, _ := a.store.nodes()
	after, _ := a.store.record(owner.ID)
	if checked != 1 || len(pools) != 1 || pools[0].Host != "1.1.1.1" || pools[0].PoolGroup != "public" || pools[0].Source != v.Source.ID {
		t.Fatal("import bypassed blacklist or filtering")
	}
	public := 0
	for _, n := range nodes {
		if n.ManagedBy == publicManager {
			public++
		}
	}
	if public != 2 || before.Credentials.Token != after.Credentials.Token || before.Credentials.PublicToken != after.Credentials.PublicToken {
		t.Fatal("public protocol lifecycle or subscription identity changed")
	}
}

func TestCustomPublicSourceCountBound(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for i := 0; i < maxCustomPublicSources; i++ {
		addPublicTestSource(t, a, owner, object{"name": fmt.Sprint("Source", i), "text": "http://8.8.8.8:80"})
	}
	if req(t, a, owner, "POST", "/api/public-pool/sources", object{"name": "Over limit", "text": "http://8.8.8.8:80"}).Code != 400 {
		t.Fatal("unbounded custom sources")
	}
}
