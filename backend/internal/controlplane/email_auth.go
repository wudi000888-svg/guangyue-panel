package controlplane

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const emailGenericResponse = "如果邮箱符合条件，邮件将发送至该地址，请稍后查收。"

var errEmailToken = commerceFail(400, "邮箱验证链接无效、已使用或已过期")

func (a *App) emailLink(action, token string) (string, error) {
	base, err := url.Parse(a.cfg.PublicURL)
	if err != nil || base.Host == "" || base.User != nil || (base.Scheme != "https" && !(a.cfg.Dev && base.Scheme == "http")) {
		return "", errors.New("invalid public URL for email")
	}
	base.RawQuery = ""
	base.Fragment = "/email/" + action + "?token=" + url.QueryEscape(token)
	return base.String(), nil
}
func (s *Store) eligibleEmailUser(id int64) (Record, error) {
	actor, err := s.record(id)
	if err != nil {
		return actor, err
	}
	var archived int
	err = s.db.QueryRow("SELECT COUNT(*) FROM archived_users WHERE user_id=?", id).Scan(&archived)
	if err != nil {
		return actor, err
	}
	if !actor.Enabled || actor.Mount != nil || archived > 0 {
		return actor, errors.New("account unavailable")
	}
	return actor, nil
}
func (a *App) issueEmailToken(purpose, email string, user int64, passwordHash string) (string, error) {
	token := randomToken(32)
	now := time.Now().Unix()
	expires := now + 1200
	action := "verify"
	subject := "验证你的邮箱"
	message := "请在 20 分钟内打开下方链接，并在面板中确认验证邮箱。"
	if purpose == "reset" {
		action = "reset"
		subject = "重置你的登录密码"
		message = "请在 20 分钟内打开下方链接设置新密码。重置后所有已登录会话将失效。"
	}
	link, err := a.emailLink(action, token)
	if err != nil {
		return "", err
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO email_tokens(token_hash,user_id,email,purpose,password_hash,expires,created) VALUES(?,?,?,?,?,?,?)", digest(token), user, email, purpose, passwordHash, expires, now); err != nil {
		return "", err
	}
	id := "mail-" + randomToken(18)
	if err = a.store.insertEmail(tx, id, user, email, purpose, subject, message, link, expires, now); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func (a *App) emailPublicAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/email")
	settings, err := a.store.emailSettings()
	if err != nil {
		emailAPIError(w, err)
		return
	}
	if path == "/status" && r.Method == "GET" {
		jsonResponse(w, 200, object{"available": emailReady(settings), "registration_verification": settings.RegistrationVerification})
		return
	}
	if r.Method != "POST" {
		failure(w, 405, "不支持的请求方法")
		return
	}
	switch path {
	case "/registration", "/reset/request":
		var in struct {
			Email string `json:"email"`
		}
		if !decode(w, r, &in) {
			return
		}
		if path == "/registration" && !a.store.siteSettings().RegistrationEnabled {
			failure(w, 403, "注册功能未开放")
			return
		}
		email, e := emailAddress(in.Email)
		if e != nil {
			emailAPIError(w, e)
			return
		}
		if !emailReady(settings) {
			failure(w, 409, "邮件服务暂未启用，请联系管理员")
			return
		}
		if !a.store.emailRateAllowed(path, requestIP(r), email, time.Now().Unix()) {
			jsonResponse(w, 200, object{"ok": true, "message": emailGenericResponse})
			return
		}
		if path == "/registration" {
			var count int
			e = a.store.db.QueryRow("SELECT COUNT(*) FROM email_accounts WHERE email=?", email).Scan(&count)
			if e == nil && count == 0 {
				_, e = a.issueEmailToken("registration", email, 0, "")
			}
		} else {
			var user int64
			e = a.store.db.QueryRow("SELECT user_id FROM email_accounts WHERE email=?", email).Scan(&user)
			if e == nil {
				actor, userErr := a.store.eligibleEmailUser(user)
				if userErr == nil {
					_, e = a.issueEmailToken("reset", email, user, digest(string(actor.Password)))
				}
			}
		}
		// Identical public status/body for unknown, disabled, rate-limited and valid accounts.
		jsonResponse(w, 200, object{"ok": true, "message": emailGenericResponse})
	case "/verify":
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !a.rateAllowed("email-verify:" + requestIP(r)) {
			failure(w, 429, "验证尝试过多，请稍后重试")
			return
		}
		result, e := a.store.verifyEmailToken(in.Token, time.Now().Unix())
		if e != nil {
			emailAPIError(w, e)
			return
		}
		jsonResponse(w, 200, result)
	case "/reset/confirm":
		var in struct {
			Token           string `json:"token"`
			Password        string `json:"password"`
			ConfirmPassword string `json:"confirm_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !a.rateAllowed("email-reset:" + requestIP(r)) {
			failure(w, 429, "验证尝试过多，请稍后重试")
			return
		}
		if len(in.Password) < 8 || len(in.Password) > 72 || in.Password != in.ConfirmPassword {
			failure(w, 400, "密码需为 8–72 个字节且两次输入一致")
			return
		}
		if e := a.store.resetEmailPassword(in.Token, in.Password, time.Now().Unix()); e != nil {
			emailAPIError(w, e)
			return
		}
		jsonResponse(w, 200, object{"ok": true})
	default:
		failure(w, 404, "接口不存在")
	}
}
func (s *Store) verifyEmailToken(token string, now int64) (object, error) {
	if len(token) < 32 || len(token) > 128 {
		return nil, errEmailToken
	}
	var user, expires, consumed int64
	var email, purpose, fingerprint string
	err := s.db.QueryRow("SELECT user_id,email,purpose,password_hash,expires,consumed FROM email_tokens WHERE token_hash=?", digest(token)).Scan(&user, &email, &purpose, &fingerprint, &expires, &consumed)
	if err != nil || consumed != 0 || expires <= now || (purpose != "bind" && purpose != "registration") {
		return nil, errEmailToken
	}
	var actor Record
	if purpose == "bind" {
		actor, err = s.eligibleEmailUser(user)
		if err != nil || digest(string(actor.Password)) != fingerprint {
			return nil, errEmailToken
		}
	}
	proof := ""
	if purpose == "registration" {
		proof = randomToken(32)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE email_tokens SET consumed=? WHERE token_hash=? AND consumed=0 AND expires>?", now, digest(token), now)
	if err != nil {
		return nil, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return nil, errEmailToken
	}
	if purpose == "registration" {
		_, err = tx.Exec("INSERT INTO email_tokens(token_hash,user_id,email,purpose,expires,created) VALUES(?,0,?,'registration-proof',?,?)", digest(proof), email, now+600, now)
	} else {
		// Verify the password has not changed between the eligibility read and the transaction.
		// Lock the credential row as well: a simultaneous password reset must
		// either invalidate this token first or observe this completed binding.
		locked, lockErr := tx.Exec("UPDATE users SET password=password WHERE id=? AND password=?", user, actor.Password)
		if lockErr != nil {
			return nil, lockErr
		}
		changed, lockErr := locked.RowsAffected()
		if lockErr != nil || changed != 1 {
			return nil, errEmailToken
		}
		_, err = tx.Exec("INSERT INTO email_accounts(user_id,email,verified_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,verified_at=excluded.verified_at", user, email, now)
		if err == nil {
			_, err = tx.Exec("UPDATE email_tokens SET consumed=? WHERE user_id=? AND purpose IN ('bind','reset') AND consumed=0", now, user)
		}
	}
	if err != nil {
		return nil, errEmailToken
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	out := object{"ok": true, "purpose": purpose, "email": email}
	if proof != "" {
		out["registration_token"] = proof
	}
	return out, nil
}
func (s *Store) saveEmailRegistration(record *Record, email, proof string) error {
	settings, err := s.emailSettings()
	if err != nil {
		return err
	}
	email = strings.TrimSpace(email)
	if email == "" && !settings.RegistrationVerification {
		return s.save(record)
	}
	if !emailReady(settings) {
		return commerceFail(409, "邮件服务暂未启用，请联系管理员")
	}
	email, err = emailAddress(email)
	if err != nil {
		return err
	}
	if len(proof) < 32 || len(proof) > 128 {
		return errEmailToken
	}
	if record.Credentials.PublicToken == "" {
		record.Credentials.PublicToken = randomToken(32)
	}
	now := time.Now().Unix()
	credentials, err := s.vault.seal(record.Credentials)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE email_tokens SET consumed=? WHERE token_hash=? AND email=? AND purpose='registration-proof' AND consumed=0 AND expires>?", now, digest(proof), email, now)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errEmailToken
	}
	var id int64
	if err = tx.QueryRow("INSERT INTO users(username,doc,credentials,password,token_hash) VALUES(?,?,?,?,?) RETURNING id", record.Username, jsonBytes(record.User), credentials, record.Password, digest(record.Credentials.Token)).Scan(&id); err != nil {
		return commerceFail(409, "账号或邮箱已存在，请检查后重试")
	}
	if _, err = tx.Exec("INSERT INTO email_accounts(user_id,email,verified_at) VALUES(?,?,?)", id, email, now); err != nil {
		return commerceFail(409, "账号或邮箱已存在，请检查后重试")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	record.ID = id
	return nil
}
func (s *Store) resetEmailPassword(token, password string, now int64) error {
	if len(token) < 32 || len(token) > 128 {
		return errEmailToken
	}
	var user int64
	var email, fingerprint string
	err := s.db.QueryRow("SELECT user_id,email,password_hash FROM email_tokens WHERE token_hash=? AND purpose='reset' AND consumed=0 AND expires>?", digest(token), now).Scan(&user, &email, &fingerprint)
	if err != nil {
		return errEmailToken
	}
	actor, err := s.eligibleEmailUser(user)
	if err != nil || digest(string(actor.Password)) != fingerprint {
		return errEmailToken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE email_tokens SET consumed=? WHERE token_hash=? AND consumed=0 AND expires>?", now, digest(token), now)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errEmailToken
	}
	result, err = tx.Exec("UPDATE users SET password=? WHERE id=? AND password=? AND EXISTS(SELECT 1 FROM email_accounts WHERE user_id=? AND email=?)", hash, user, actor.Password, user, email)
	if err != nil {
		return err
	}
	n, _ = result.RowsAffected()
	if n != 1 {
		return errEmailToken
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", user); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE email_tokens SET consumed=? WHERE user_id=? AND consumed=0", now, user); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) accountEmailAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	settings, err := a.store.emailSettings()
	if err != nil {
		emailAPIError(w, err)
		return
	}
	if r.Method == "GET" {
		var email, pending string
		var verifiedAt int64
		err = a.store.db.QueryRow("SELECT email,verified_at FROM email_accounts WHERE user_id=?", actor.ID).Scan(&email, &verifiedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			emailAPIError(w, err)
			return
		}
		_ = a.store.db.QueryRow("SELECT email FROM email_tokens WHERE user_id=? AND purpose='bind' AND consumed=0 AND expires>? ORDER BY created DESC,token_hash DESC LIMIT 1", actor.ID, time.Now().Unix()).Scan(&pending)
		jsonResponse(w, 200, object{"email": email, "verified": email != "", "verified_at": verifiedAt, "pending_email": pending, "available": emailReady(settings)})
		return
	}
	if r.Method != "POST" && r.Method != "DELETE" {
		failure(w, 405, "不支持的请求方法")
		return
	}
	var in struct {
		Email           string `json:"email"`
		CurrentPassword string `json:"current_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !a.rateAllowed("email-account:" + requestIP(r)) {
		failure(w, 429, "验证尝试过多，请稍后重试")
		return
	}
	actor, err = a.store.eligibleEmailUser(actor.ID)
	if err != nil || bcrypt.CompareHashAndPassword(actor.Password, []byte(in.CurrentPassword)) != nil {
		failure(w, 403, "当前密码不正确")
		return
	}
	now := time.Now().Unix()
	if r.Method == "DELETE" {
		tx, err := a.store.db.Begin()
		if err != nil {
			emailAPIError(w, err)
			return
		}
		defer tx.Rollback()
		locked, lockErr := tx.Exec("UPDATE users SET password=password WHERE id=? AND password=?", actor.ID, actor.Password)
		if lockErr != nil {
			emailAPIError(w, lockErr)
			return
		}
		changed, lockErr := locked.RowsAffected()
		if lockErr != nil || changed != 1 {
			failure(w, 403, "当前密码不正确")
			return
		}
		if _, err = tx.Exec("DELETE FROM email_accounts WHERE user_id=?", actor.ID); err == nil {
			_, err = tx.Exec("UPDATE email_tokens SET consumed=? WHERE user_id=? AND consumed=0", now, actor.ID)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			emailAPIError(w, err)
			return
		}
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	email, err := emailAddress(in.Email)
	if err != nil {
		emailAPIError(w, err)
		return
	}
	if !emailReady(settings) {
		failure(w, 409, "邮件服务暂未启用，请联系管理员")
		return
	}
	if !a.store.emailRateAllowed("bind", requestIP(r), email, now) {
		failure(w, 429, "邮件请求过于频繁，请稍后重试")
		return
	}
	if _, err = a.issueEmailToken("bind", email, actor.ID, digest(string(actor.Password))); err != nil {
		emailAPIError(w, err)
		return
	}
	jsonResponse(w, 202, object{"ok": true, "message": emailGenericResponse})
}
