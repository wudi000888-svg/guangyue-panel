package controlplane

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testEmailSettings(t *testing.T, a *App, verification bool) EmailSettings {
	t.Helper()
	v, err := a.store.saveEmailSettings(EmailSettings{Enabled: true, Host: "smtp.example.test", Port: 587, TLSMode: "starttls", Username: "fixture-user", Password: "smtp-fixture-secret", FromEmail: "sender@example.test", RegistrationVerification: verification, OrderNotifications: true})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func queuedEmailToken(t *testing.T, a *App, kind string) string {
	t.Helper()
	var body []byte
	if err := a.store.db.QueryRow("SELECT body FROM email_outbox WHERE kind=? ORDER BY created DESC,id DESC LIMIT 1", kind).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var msg emailMessage
	if err := a.store.vault.open(body, &msg); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(msg.Text, "\n") {
		if strings.HasPrefix(line, "https://") {
			link, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.SplitN(link.Fragment, "?", 2)
			if len(parts) == 2 {
				values, _ := url.ParseQuery(parts[1])
				if token := values.Get("token"); token != "" {
					return token
				}
			}
		}
	}
	t.Fatal("verification link missing from encrypted message")
	return ""
}
func verifiedEmail(t *testing.T, a *App, actor Record, email string) {
	t.Helper()
	w := req(t, a, actor, "POST", "/api/account/email", object{"email": email, "current_password": "test-password-123456"})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	token := queuedEmailToken(t, a, "bind")
	decoded[object](t, req(t, a, Record{}, "POST", "/api/email/verify", object{"token": token}), 200)
}
func TestEmailSettingsPermissionsCASAndWriteOnlyPassword(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	for _, actor := range []Record{{}, member} {
		if code := req(t, a, actor, "GET", "/api/email/settings", nil).Code; code != 401 && code != 403 {
			t.Fatal("mail settings visible without owner", code)
		}
	}
	in := object{"enabled": true, "host": "smtp.example.test", "port": 587, "tls_mode": "starttls", "username": "fixture-user", "password": "smtp-fixture-secret", "from_email": "sender@example.test", "version": 0, "admin_password": "wrong"}
	if req(t, a, owner, "PUT", "/api/email/settings", in).Code != 403 {
		t.Fatal("mail changes require reauthentication")
	}
	in["admin_password"] = "test-password-123456"
	response := req(t, a, owner, "PUT", "/api/email/settings", in)
	saved := decoded[EmailSettings](t, response, 200)
	if !saved.HasPassword || saved.Password != "" || strings.Contains(response.Body.String(), "smtp-fixture-secret") {
		t.Fatal("SMTP secret exposed")
	}
	var ciphertext []byte
	if err := a.store.db.QueryRow("SELECT doc FROM email_settings WHERE id=1").Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("smtp-fixture-secret")) {
		t.Fatal("SMTP secret not encrypted")
	}
	if req(t, a, owner, "PUT", "/api/email/settings", in).Code != 409 {
		t.Fatal("stale SMTP config accepted")
	}
	in["version"] = saved.Version
	in["password"] = ""
	saved = decoded[EmailSettings](t, req(t, a, owner, "PUT", "/api/email/settings", in), 200)
	actual, err := a.store.emailSettings()
	if err != nil || actual.Password != "smtp-fixture-secret" || !saved.HasPassword {
		t.Fatal("blank password did not retain secret")
	}
	in["version"] = saved.Version
	in["enabled"] = false
	in["registration_verification"] = true
	if req(t, a, owner, "PUT", "/api/email/settings", in).Code != 400 {
		t.Fatal("registration verification allowed without enabled SMTP")
	}
}
func TestEmailBindingVerificationAndResetOneTime(t *testing.T) {
	a := testApp(t)
	testEmailSettings(t, a, false)
	user := testUser(t, a, "alice", "user")
	other := testUser(t, a, "other", "user")
	if req(t, a, user, "POST", "/api/account/email", object{"email": "alice@example.test", "current_password": "incorrect"}).Code != 403 {
		t.Fatal("binding bypassed current password")
	}
	decoded[object](t, req(t, a, user, "POST", "/api/account/email", object{"email": "Alice@EXAMPLE.TEST", "current_password": "test-password-123456"}), 202)
	token := queuedEmailToken(t, a, "bind")
	before := decoded[object](t, req(t, a, user, "GET", "/api/account/email", nil), 200)
	if before["verified"].(bool) || before["pending_email"] != "alice@example.test" {
		t.Fatal("email bound before verification")
	}
	_ = req(t, a, Record{}, "GET", "/api/email/verify?token="+token, nil)
	result := decoded[object](t, req(t, a, Record{}, "POST", "/api/email/verify", object{"token": token}), 200)
	if result["purpose"] != "bind" {
		t.Fatal("wrong verification purpose")
	}
	if req(t, a, Record{}, "POST", "/api/email/verify", object{"token": token}).Code != 400 {
		t.Fatal("binding token replay")
	}
	info := decoded[object](t, req(t, a, user, "GET", "/api/account/email", nil), 200)
	if !info["verified"].(bool) || info["email"] != "alice@example.test" {
		t.Fatal("binding not persisted")
	}
	otherInfo := decoded[object](t, req(t, a, other, "GET", "/api/account/email", nil), 200)
	if otherInfo["email"] != "" {
		t.Fatal("cross-user email leaked")
	}
	unknown := req(t, a, Record{}, "POST", "/api/email/reset/request", object{"email": "unknown@example.test"})
	known := req(t, a, Record{}, "POST", "/api/email/reset/request", object{"email": "alice@example.test"})
	if known.Code != 200 || unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatal("password reset enumerates accounts")
	}
	resetToken := queuedEmailToken(t, a, "reset")
	if strings.Contains(known.Body.String(), resetToken) {
		t.Fatal("reset token leaked in API")
	}
	if _, err := a.store.db.Exec("INSERT INTO sessions(token_hash,user_id,expires) VALUES(?,?,?)", digest("active-session"), user.ID, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	input := object{"token": resetToken, "password": "a-new-test-password", "confirm_password": "a-new-test-password"}
	decoded[object](t, req(t, a, Record{}, "POST", "/api/email/reset/confirm", input), 200)
	refreshed, err := a.store.record(user.ID)
	if err != nil || bcrypt.CompareHashAndPassword(refreshed.Password, []byte("a-new-test-password")) != nil {
		t.Fatal("password reset not applied")
	}
	var sessions int
	if err = a.store.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id=?", user.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("password reset left active sessions")
	}
	if req(t, a, Record{}, "POST", "/api/email/reset/confirm", input).Code != 400 {
		t.Fatal("reset token replay accepted")
	}
}
func TestEmailRegistrationProofAtomicityAndRequiredEmail(t *testing.T) {
	a := testApp(t)
	testEmailSettings(t, a, true)
	site := a.store.siteSettings()
	site.RegistrationEnabled = true
	if err := a.store.setMeta("site_settings", string(jsonBytes(site))); err != nil {
		t.Fatal(err)
	}
	input := object{"username": "alice", "password": "test-password-123456", "confirm_password": "test-password-123456"}
	if req(t, a, Record{}, "POST", "/api/register", input).Code != 400 {
		t.Fatal("required email omitted")
	}
	decoded[object](t, req(t, a, Record{}, "POST", "/api/email/registration", object{"email": "new@example.test"}), 200)
	token := queuedEmailToken(t, a, "registration")
	proof := decoded[object](t, req(t, a, Record{}, "POST", "/api/email/verify", object{"token": token}), 200)
	input["email"] = "different@example.test"
	input["email_token"] = proof["registration_token"]
	if req(t, a, Record{}, "POST", "/api/register", input).Code != 400 {
		t.Fatal("registration proof not bound to email")
	}
	input["email"] = "new@example.test"
	result := req(t, a, Record{}, "POST", "/api/register", input)
	if result.Code != 201 {
		t.Fatal(result.Code, result.Body.String())
	}
	input["username"] = "replay"
	if req(t, a, Record{}, "POST", "/api/register", input).Code != 400 {
		t.Fatal("registration proof reused")
	}
	var users int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil || users != 1 {
		t.Fatal("invalid registration left an account behind")
	}
	var user int64
	if err := a.store.db.QueryRow("SELECT user_id FROM email_accounts WHERE email='new@example.test'").Scan(&user); err != nil {
		t.Fatal(err)
	}
	record, err := a.store.record(user)
	if err != nil || record.Credentials.PublicToken == "" {
		t.Fatal("registered account missing credentials")
	}
}
func TestEmailTokenExpiryRevocationAndRateLimits(t *testing.T) {
	a := testApp(t)
	testEmailSettings(t, a, false)
	user := testUser(t, a, "alice", "user")
	verifiedEmail(t, a, user, "alice@example.test")
	if _, err := a.issueEmailToken("reset", "alice@example.test", user.ID, digest(string(user.Password))); err != nil {
		t.Fatal(err)
	}
	token := queuedEmailToken(t, a, "reset")
	if _, err := a.store.db.Exec("UPDATE email_tokens SET expires=? WHERE token_hash=?", time.Now().Unix()-1, digest(token)); err != nil {
		t.Fatal(err)
	}
	if a.store.resetEmailPassword(token, "new-password", time.Now().Unix()) == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := a.store.db.Exec("DELETE FROM email_outbox WHERE kind='reset'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issueEmailToken("reset", "alice@example.test", user.ID, digest(string(user.Password))); err != nil {
		t.Fatal(err)
	}
	token = queuedEmailToken(t, a, "reset")
	decoded[object](t, req(t, a, user, "DELETE", "/api/account/email", object{"current_password": "test-password-123456"}), 200)
	if a.store.resetEmailPassword(token, "new-password", time.Now().Unix()) == nil {
		t.Fatal("unbinding did not invalidate reset")
	}
	for i := 0; i < 5; i++ {
		if !a.store.emailRateAllowed("test", "ip-a", "target@example.test", 6000) {
			t.Fatal("premature throttle")
		}
	}
	if a.store.emailRateAllowed("test", "ip-b", "target@example.test", 6000) {
		t.Fatal("target throttle bypassed by another IP")
	}
	var tokenHash string
	if err := a.store.db.QueryRow("SELECT token_hash FROM email_tokens LIMIT 1").Scan(&tokenHash); err != nil || tokenHash == token {
		t.Fatal("raw verification token stored")
	}
	data, _ := json.Marshal(emailPublicSettings(EmailSettings{Password: "must-not-leak"}))
	if bytes.Contains(data, []byte("must-not-leak")) {
		t.Fatal("public config exposes secret")
	}
}
