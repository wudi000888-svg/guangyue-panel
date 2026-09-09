package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func decoded[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

type inboxResult struct {
	Items  []PanelMessage `json:"items"`
	Next   int64          `json:"next_before"`
	Unread int            `json:"unread"`
}

func deliver(t *testing.T, a *App, owner Record, to int64, all bool) int64 {
	t.Helper()
	v := decoded[struct {
		ID int64 `json:"id"`
	}](t, req(t, a, owner, "POST", "/api/messages", object{"title": "Maintenance <script>", "body": "First line\nSecond line", "category": "maintenance", "recipient_id": to, "all": all}), 201)
	return v.ID
}
func TestPanelSettingsPermissionsPersistenceAndRevision(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	v := a.store.siteSettings()
	if req(t, a, member, "PUT", "/api/settings", v).Code != 403 || req(t, a, Record{}, "GET", "/api/settings", nil).Code != 401 {
		t.Fatal("settings permissions")
	}
	before := a.store.meta("desired_generation")
	v.PanelName = "Example Network"
	v.Organization = "Example Ltd"
	v.DefaultLocale = "en"
	v.SupportEmail = "support@example.com"
	v.LoginNotice = "Maintenance\nAccess by invitation"
	saved := decoded[SiteSettings](t, req(t, a, owner, "PUT", "/api/settings", v), 200)
	if saved.Revision == "" || a.store.siteSettings() != saved || a.store.meta("desired_generation") != before || a.status != "" {
		t.Fatal("settings persistence or protocol side effect")
	}
	if req(t, a, owner, "PUT", "/api/settings", v).Code != 409 {
		t.Fatal("stale settings overwrite")
	}
	public := decoded[SiteSettings](t, req(t, a, Record{}, "GET", "/api/site", nil), 200)
	if public.PanelName != v.PanelName || public.DefaultLocale != "en" || public.Revision != "" {
		t.Fatal("public branding")
	}
	second, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()
	if second.siteSettings() != saved {
		t.Fatal("settings do not survive reopening")
	}
}
func TestPanelSettingsValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for _, change := range []func(*SiteSettings){func(v *SiteSettings) { v.PanelName = " " }, func(v *SiteSettings) { v.PanelName = strings.Repeat("界", 41) }, func(v *SiteSettings) { v.Organization = "" }, func(v *SiteSettings) { v.DefaultLocale = "fr" }, func(v *SiteSettings) { v.SupportEmail = "Name <x@example.com>" }, func(v *SiteSettings) { v.LoginNotice = strings.Repeat("a", 501) }} {
		v := a.store.siteSettings()
		change(&v)
		if req(t, a, owner, "PUT", "/api/settings", v).Code != 400 {
			t.Fatal("invalid setting accepted")
		}
	}
}
func TestMessageRecipientIsolationAndReadState(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	id := deliver(t, a, owner, alice.ID, false)
	if req(t, a, Record{}, "GET", "/api/messages", nil).Code != 401 || req(t, a, alice, "POST", "/api/messages", object{}).Code != 403 || req(t, a, alice, "GET", "/api/messages?folder=sent", nil).Code != 403 {
		t.Fatal("message permissions")
	}
	list := decoded[inboxResult](t, req(t, a, alice, "GET", "/api/messages", nil), 200)
	if len(list.Items) != 1 || list.Unread != 1 || list.Items[0].Body != "First line\nSecond line" {
		t.Fatal("message lost")
	}
	if len(decoded[inboxResult](t, req(t, a, bob, "GET", "/api/messages", nil), 200).Items) != 0 {
		t.Fatal("cross-member read")
	}
	for _, method := range []string{"POST", "DELETE"} {
		path := fmt.Sprintf("/api/messages/%d", id)
		if method == "POST" {
			path += "/read"
		}
		if req(t, a, bob, method, path, object{}).Code != 404 {
			t.Fatal("cross-member mutation")
		}
	}
	readPath := fmt.Sprintf("/api/messages/%d/read", id)
	for i := 0; i < 2; i++ {
		if req(t, a, alice, "POST", readPath, object{}).Code != 200 {
			t.Fatal("read must be idempotent")
		}
	}
	if a.store.unreadMessages(alice.ID) != 0 {
		t.Fatal("unread count")
	}
	sent := decoded[inboxResult](t, req(t, a, owner, "GET", "/api/messages?folder=sent", nil), 200)
	if sent.Items[0].ReadCount != 1 || sent.Items[0].Recipients != 1 {
		t.Fatal("read receipt")
	}
}
func TestMessageBroadcastDeletionAndRestart(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	id := deliver(t, a, owner, 0, true)
	if a.store.unreadMessages(owner.ID) != 1 || a.store.unreadMessages(alice.ID) != 1 || a.store.unreadMessages(bob.ID) != 1 {
		t.Fatal("broadcast recipients")
	}
	if req(t, a, alice, "DELETE", fmt.Sprintf("/api/messages/%d", id), object{}).Code != 200 {
		t.Fatal("remove inbox")
	}
	if len(decoded[inboxResult](t, req(t, a, alice, "GET", "/api/messages", nil), 200).Items) != 0 || a.store.unreadMessages(alice.ID) != 0 || a.store.unreadMessages(bob.ID) != 1 {
		t.Fatal("deletion not isolated")
	}
	if req(t, a, bob, "POST", "/api/messages/read-all", object{}).Code != 200 || a.store.unreadMessages(bob.ID) != 0 {
		t.Fatal("mark all")
	}
	second, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()
	if second.unreadMessages(owner.ID) != 1 || second.unreadMessages(bob.ID) != 0 {
		t.Fatal("read states lost after reopen")
	}
	late := testUser(t, a, "late", "user")
	if second.unreadMessages(late.ID) != 0 {
		t.Fatal("broadcast must target current members only")
	}
}
func TestMessageValidationRollbackAndRateLimit(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for _, v := range []object{{"title": "x", "body": "y", "category": "notice", "recipient_id": 999}, {"title": "x", "body": "y", "category": "notice", "all": true, "recipient_id": owner.ID}, {"title": "x", "body": "y", "category": "invalid", "recipient_id": owner.ID}, {"title": "x", "body": strings.Repeat("a", 8001), "category": "notice", "recipient_id": owner.ID}} {
		w := req(t, a, owner, "POST", "/api/messages", v)
		if w.Code != 400 && w.Code != 404 {
			t.Fatalf("invalid message status %d", w.Code)
		}
	}
	var count int
	a.store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if count != 0 {
		t.Fatal("invalid send left a message")
	}
	for i := 0; i < 10; i++ {
		deliver(t, a, owner, owner.ID, false)
	}
	if req(t, a, owner, "POST", "/api/messages", object{"title": "x", "body": "y", "category": "notice", "recipient_id": owner.ID}).Code != 429 {
		t.Fatal("send limit")
	}
}
func TestMessagePaginationAndStorageLimit(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	tx, _ := a.store.db.Begin()
	for i := 0; i < 2001; i++ {
		r, e := tx.Exec("INSERT INTO messages(sender_id,sender_name,title,body,category,created) VALUES(?,?,?,?,?,0)", owner.ID, owner.Username, "Old notice", "body", "notice")
		if e != nil {
			t.Fatal(e)
		}
		id, _ := r.LastInsertId()
		if _, e = tx.Exec("INSERT INTO message_recipients(message_id,user_id) VALUES(?,?)", id, owner.ID); e != nil {
			t.Fatal(e)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deliver(t, a, owner, owner.ID, false)
	var count, recipients int
	a.store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	a.store.db.QueryRow("SELECT COUNT(*) FROM message_recipients").Scan(&recipients)
	if count != 2000 || recipients != 2000 {
		t.Fatal("unbounded storage or orphan recipients")
	}
	one := decoded[inboxResult](t, req(t, a, owner, "GET", "/api/messages", nil), 200)
	two := decoded[inboxResult](t, req(t, a, owner, "GET", fmt.Sprintf("/api/messages?before=%d", one.Next), nil), 200)
	if len(one.Items) != 30 || len(two.Items) != 30 || one.Next == 0 || two.Items[0].ID >= one.Items[29].ID {
		t.Fatal("unstable pagination")
	}
}
