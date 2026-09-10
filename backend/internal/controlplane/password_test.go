package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFlexiblePasswordCreateAndLogin(t *testing.T) {
	for _, password := range []string{"7", "1234", "中", " ", strings.Repeat("a", 72), strings.Repeat("中", 24)} {
		t.Run(fmt.Sprintf("bytes_%d_%x", len(password), password[:1]), func(t *testing.T) {
			a := testApp(t)
			owner := testUser(t, a, "owner", "owner")
			w := req(t, a, owner, "POST", "/api/users", userInput{Username: "member", Password: password, Enabled: true, VLESS: true, HY2: true})
			if w.Code != 201 {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			w = req(t, a, Record{}, "POST", "/api/login", object{"username": "member", "password": password})
			if w.Code != 200 {
				t.Fatalf("short or unicode password login: %d", w.Code)
			}
			// Previously stored bcrypt hashes retain their exact login behavior.
			if w = req(t, a, Record{}, "POST", "/api/login", object{"username": "owner", "password": "test-password-123456"}); w.Code != 200 {
				t.Fatal("existing password no longer works")
			}
		})
	}
}

func TestFlexiblePasswordValidation(t *testing.T) {
	for _, password := range []string{"", strings.Repeat("a", 73), strings.Repeat("中", 25)} {
		t.Run(fmt.Sprint(len(password)), func(t *testing.T) {
			a := testApp(t)
			owner := testUser(t, a, "owner", "owner")
			if w := req(t, a, owner, "POST", "/api/users", userInput{Username: "member", Password: password, Enabled: true}); w.Code != 400 {
				t.Fatalf("invalid create accepted: %d", w.Code)
			}
			if w := req(t, a, owner, "POST", "/api/password", object{"current": "test-password-123456", "password": password}); w.Code != 400 {
				t.Fatalf("invalid change accepted: %d", w.Code)
			}
		})
	}
}

func TestShortPasswordChangeAndAdminReset(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	login := func(password string) *http.Cookie {
		t.Helper()
		w := req(t, a, Record{}, "POST", "/api/login", object{"username": "member", "password": password})
		if w.Code != 200 {
			t.Fatalf("login: %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	sessionStatus := func(cookie *http.Cookie) int {
		r := httptest.NewRequest("GET", "/api/state", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		return w.Code
	}
	old := login("test-password-123456")
	if w := req(t, a, member, "POST", "/api/password", object{"current": "incorrect", "password": "1"}); w.Code != 400 {
		t.Fatal("wrong current password accepted")
	}
	if sessionStatus(old) != 200 {
		t.Fatal("failed change invalidated session")
	}
	if w := req(t, a, member, "POST", "/api/password", object{"current": "test-password-123456", "password": "1"}); w.Code != 200 {
		t.Fatalf("short self change: %d", w.Code)
	}
	if sessionStatus(old) != 401 {
		t.Fatal("old session survived password change")
	}
	current := login("1")
	path := fmt.Sprintf("/api/users/%d", member.ID)
	// Empty on the edit endpoint means preserve, never assign an empty password.
	if w := req(t, a, owner, "PUT", path, userInput{Username: "member", Enabled: true}); w.Code != 200 {
		t.Fatalf("blank edit: %d", w.Code)
	}
	if sessionStatus(current) != 200 {
		t.Fatal("blank edit invalidated session")
	}
	login("1")
	if w := req(t, a, owner, "PUT", path, userInput{Username: "member", Password: "2", Enabled: true}); w.Code != 200 {
		t.Fatalf("admin short reset: %d", w.Code)
	}
	if sessionStatus(current) != 401 {
		t.Fatal("admin reset did not invalidate session")
	}
	login("2")
	w := req(t, a, Record{}, "POST", "/api/login", object{"username": "member", "password": "1"})
	if w.Code != 401 {
		t.Fatal("previous password still works")
	}
	record, err := a.store.record(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Public responses never include password hashes.
	w = req(t, a, owner, "GET", "/api/state", nil)
	var state map[string]any
	if json.Unmarshal(w.Body.Bytes(), &state) != nil {
		t.Fatal("invalid state")
	}
	if strings.Contains(w.Body.String(), string(record.Password)) {
		t.Fatal("password hash exposed")
	}
}
