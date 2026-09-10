package controlplane

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUpdateAccessAndNarrowRequest(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	calls := 0
	a.updateClient = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("forwarded browser credentials")
		}
		if r.URL.Host != "updater" || r.URL.Path != "/apply" {
			t.Fatal("unexpected updater path")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{"stage":"queued"}`)), Header: make(http.Header)}, nil
	})}
	valid := object{"action": "update", "version": "0.18.0", "expected_version": "0.17.0", "request_id": "11111111-2222-4333-a444-555555555555"}
	if w := req(t, a, Record{}, "GET", "/api/updates", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := req(t, a, member, "POST", "/api/updates/apply", valid); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, bad := range []object{{"action": "shell", "version": "0.18.0"}, {"action": "update", "version": "../0.18.0"}, {"action": "update", "version": "0.18.0", "url": "https://example.com"}} {
		if w := req(t, a, owner, "POST", "/api/updates/apply", bad); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("unvalidated request reached helper")
	}
	if w := req(t, a, owner, "POST", "/api/updates/apply", valid); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, scope := range []string{"read", "manage"} {
		if httpapi.AllowedGateway("POST", "/api/updates/apply", scope) {
			t.Fatal("federation allowed root operation")
		}
	}
}
func TestUpdateRejectsUnboundedOrNonJSONResponse(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for _, body := range []string{"<html>bad</html>", `{"value":"` + string(bytes.Repeat([]byte{'x'}, 2<<20)) + `"}`} {
		a.updateClient = &http.Client{Transport: updateRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}, nil
		})}
		if w := req(t, a, owner, "GET", "/api/updates", nil); w.Code != 502 {
			t.Fatal(w.Code)
		}
	}
}
