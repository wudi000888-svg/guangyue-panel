package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPrivateAndPublicSubscriptionsHaveIndependentURLsContentsAndRotation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	if err := a.applyPublicSetLocked([]IPResource{publicTestPool("8.8.8.8")}); err != nil {
		t.Fatal(err)
	}
	member, _ = a.store.record(member.ID)
	private := "/sub/" + member.Credentials.Token
	mixed := private
	public := "/public-sub/" + strconv.FormatInt(member.ID, 10) + "/" + member.Credentials.PublicToken
	if member.Credentials.PublicToken == member.Credentials.Token || len(member.Credentials.PublicToken) != 43 {
		t.Fatal("public credential is not independent")
	}
	for _, format := range []string{"raw", "base64", "mihomo"} {
		for _, v := range []struct {
			path   string
			public bool
		}{{private, false}, {public, true}} {
			separator := "?"
			if strings.Contains(v.path, "?") {
				separator = "&"
			}
			w := req(t, a, Record{}, "GET", v.path+separator+"format="+format, nil)
			if w.Code != 200 {
				t.Fatal("subscription unavailable")
			}
			body := w.Body.Bytes()
			if format == "base64" {
				var err error
				body, err = base64.StdEncoding.DecodeString(string(body))
				if err != nil {
					t.Fatal(err)
				}
			}
			if format == "mihomo" {
				var doc struct {
					Proxies []object `yaml:"proxies"`
				}
				wantNodes := 4
				if v.public {
					wantNodes = 2
				}
				if yaml.Unmarshal(body, &doc) != nil || len(doc.Proxies) != wantNodes {
					t.Fatal("wrong YAML membership")
				}
				hasPublic, hasLocal := false, false
				for _, p := range doc.Proxies {
					if strings.Contains(str(p["name"]), "公共") {
						hasPublic = true
					} else {
						hasLocal = true
					}
				}
				if !hasPublic || v.public && hasLocal || !v.public && !hasLocal {
					t.Fatal("mixed YAML subscription")
				}
			} else {
				lines := strings.Split(strings.TrimSpace(string(body)), "\n")
				wantLines := 4
				if v.public {
					wantLines = 2
				}
				if len(lines) != wantLines {
					t.Fatalf("wrong URI count format=%s public=%v want=%d got=%d body=%q", format, v.public, wantLines, len(lines), string(body))
				}
				if !strings.Contains(string(body), "%E5%85%AC%E5%85%B1") {
					t.Fatal("authorized public node missing from canonical subscription")
				}
			}
			want := "6"
			if v.public {
				want = "1"
			}
			if w.Header().Get("Profile-Update-Interval") != want || w.Header().Get("Cache-Control") != "no-store, private" {
				t.Fatal("wrong subscription headers")
			}
		}
	}
	if w := req(t, a, Record{}, "GET", mixed+"?format=raw", nil); w.Code != 200 || len(strings.Split(strings.TrimSpace(w.Body.String()), "\n")) != 4 {
		t.Fatalf("mixed subscription did not use all authorized groups: %d %s", w.Code, w.Body.String())
	}
	if req(t, a, Record{}, "GET", "/sub/"+member.Credentials.PublicToken, nil).Code != 404 {
		t.Fatal("public token can access private subscription")
	}
	if req(t, a, Record{}, "GET", "/public-sub/"+strconv.FormatInt(owner.ID, 10)+"/"+member.Credentials.PublicToken, nil).Code != 404 {
		t.Fatal("public token crosses member boundary")
	}
	for _, pool := range []string{"private", "public"} {
		w := req(t, a, member, "GET", "/api/subscription?pool="+pool, nil)
		var info object
		_ = json.Unmarshal(w.Body.Bytes(), &info)
		if w.Code != 200 || str(info["pool"]) != "mixed" {
			t.Fatal("subscription info scope missing")
		}
	}
	if w := req(t, a, member, "GET", private, nil); w.Code != 200 || len(strings.Split(strings.TrimSpace(w.Body.String()), "\n")) != 4 {
		t.Fatal("member source override was not forced to mixed")
	}
	if req(t, a, owner, "POST", "/api/users/"+strconv.FormatInt(member.ID, 10)+"/rotate-public-sub", object{}).Code != 200 {
		t.Fatal("public rotation failed")
	}
	if req(t, a, Record{}, "GET", public, nil).Code != 404 || req(t, a, Record{}, "GET", private, nil).Code != 200 {
		t.Fatal("public rotation affected normal subscription")
	}
	member, _ = a.store.record(member.ID)
	public = "/public-sub/" + strconv.FormatInt(member.ID, 10) + "/" + member.Credentials.PublicToken
	if req(t, a, owner, "POST", "/api/users/"+strconv.FormatInt(member.ID, 10)+"/rotate-sub", object{}).Code != 200 {
		t.Fatal("normal rotation failed")
	}
	if req(t, a, Record{}, "GET", public, nil).Code != 200 || req(t, a, Record{}, "GET", private, nil).Code != 404 {
		t.Fatal("normal rotation affected public subscription")
	}
}

func TestEmptyPublicSubscriptionRemovesNodesWithoutDirectFallback(t *testing.T) {
	a := testApp(t)
	member := testUser(t, a, "member", "user")
	path := "/public-sub/" + strconv.FormatInt(member.ID, 10) + "/" + member.Credentials.PublicToken
	w := req(t, a, Record{}, "GET", path+"?format=mihomo", nil)
	var doc object
	if w.Code != 200 || yaml.Unmarshal(w.Body.Bytes(), &doc) != nil {
		t.Fatal("empty public subscription is not valid YAML")
	}
	if !strings.Contains(w.Body.String(), "REJECT") || strings.Contains(w.Body.String(), "DIRECT") || !strings.Contains(w.Body.String(), "proxies: []") {
		t.Fatal("empty subscription did not fail closed")
	}
	w = req(t, a, Record{}, "GET", path+"?format=raw", nil)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("empty URI subscription retained nodes")
	}
	member.Enabled = false
	_ = a.store.save(&member)
	if req(t, a, Record{}, "GET", path+"?format=mihomo", nil).Code != 403 {
		t.Fatal("disabled member received public subscription")
	}
}
