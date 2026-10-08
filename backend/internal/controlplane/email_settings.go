package controlplane

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type EmailSettings struct {
	Enabled                  bool   `json:"enabled"`
	Host                     string `json:"host"`
	Port                     int    `json:"port"`
	TLSMode                  string `json:"tls_mode"`
	Username                 string `json:"username"`
	Password                 string `json:"password,omitempty"`
	HasPassword              bool   `json:"has_password"`
	FromEmail                string `json:"from_email"`
	FromName                 string `json:"from_name"`
	RegistrationVerification bool   `json:"registration_verification"`
	OrderNotifications       bool   `json:"order_notifications"`
	Version                  int64  `json:"version"`
	EventsAfter              int64  `json:"-"`
}

// The encrypted document includes the notification starting point, never exposed to clients.
type emailSettingsDoc struct {
	Settings    EmailSettings `json:"settings"`
	EventsAfter int64         `json:"events_after"`
}

func (s *Store) emailSettings() (EmailSettings, error) {
	out := EmailSettings{Port: 587, TLSMode: "starttls"}
	var b []byte
	var version int64
	err := s.db.QueryRow("SELECT version,doc FROM email_settings WHERE id=1").Scan(&version, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var doc emailSettingsDoc
	if err = s.vault.open(b, &doc); err != nil {
		return out, err
	}
	out = doc.Settings
	out.Version = version
	out.EventsAfter = doc.EventsAfter
	out.HasPassword = out.Password != ""
	return out, nil
}
func emailPublicSettings(v EmailSettings) EmailSettings {
	v.HasPassword = v.Password != ""
	v.Password = ""
	return v
}
func emailAddress(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value || len(value) > 254 || strings.ContainsAny(value, "\r\n") || !strings.Contains(value, "@") {
		return "", commerceFail(400, "请输入有效的邮箱地址")
	}
	for _, r := range value {
		if r > 127 {
			return "", commerceFail(400, "邮箱地址需使用 ASCII 字符")
		}
	}
	return value, nil
}
func emailReady(v EmailSettings) bool {
	return v.Enabled && v.Host != "" && v.Port > 0 && v.Username != "" && v.Password != "" && v.FromEmail != "" && (v.TLSMode == "tls" || v.TLSMode == "starttls")
}
func (s *Store) saveEmailSettings(v EmailSettings) (EmailSettings, error) {
	old, err := s.emailSettings()
	if err != nil {
		return v, err
	}
	if old.Version != v.Version {
		return v, commerceFail(409, "邮件设置已更新，请重新加载")
	}
	v.Host = strings.TrimSpace(v.Host)
	v.Username = strings.TrimSpace(v.Username)
	v.FromName = strings.TrimSpace(v.FromName)
	if v.Password == "" {
		v.Password = old.Password
	}
	v.HasPassword = v.Password != ""
	if len(v.Host) > 253 || strings.ContainsAny(v.Host, " /\\\r\n\t:@") && net.ParseIP(v.Host) == nil || len(v.Username) > 320 || strings.ContainsAny(v.Username, "\r\n") || len(v.Password) > 4096 || utf8.RuneCountInString(v.FromName) > 80 || strings.ContainsAny(v.FromName, "\r\n") {
		return v, commerceFail(400, "SMTP 主机、账户或发件人设置无效")
	}
	if v.Port < 1 || v.Port > 65535 || (v.TLSMode != "tls" && v.TLSMode != "starttls") {
		return v, commerceFail(400, "请选择有效的 SMTP 端口和 TLS 模式")
	}
	if v.FromEmail != "" {
		v.FromEmail, err = emailAddress(v.FromEmail)
		if err != nil {
			return v, err
		}
	}
	if v.Enabled && !emailReady(v) {
		return v, commerceFail(400, "启用邮件前请完整配置 SMTP 主机、账户、密码和发件邮箱")
	}
	if v.RegistrationVerification && !emailReady(v) {
		return v, commerceFail(400, "启用注册邮箱验证前必须配置并启用 SMTP")
	}
	v.EventsAfter = old.EventsAfter
	if v.OrderNotifications && !old.OrderNotifications {
		v.EventsAfter = time.Now().Unix()
	}
	v.Version++
	b, err := s.vault.seal(emailSettingsDoc{v, v.EventsAfter})
	if err != nil {
		return v, err
	}
	var result sql.Result
	if old.Version == 0 {
		result, err = s.db.Exec("INSERT INTO email_settings(id,version,doc) VALUES(1,?,?) ON CONFLICT(id) DO NOTHING", v.Version, b)
	} else {
		result, err = s.db.Exec("UPDATE email_settings SET version=?,doc=? WHERE id=1 AND version=?", v.Version, b, old.Version)
	}
	if err != nil {
		return v, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return v, err
	}
	if n != 1 {
		return v, commerceFail(409, "邮件设置已更新，请重新加载")
	}
	return v, nil
}
func emailAPIError(w http.ResponseWriter, err error) {
	var ce commerceError
	if errors.As(err, &ce) {
		failure(w, ce.status, ce.message)
	} else {
		failure(w, 500, "邮件操作暂时失败，请稍后重试")
	}
}
func (a *App) emailAdminAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.Role != "owner" {
		failure(w, 403, "需要管理员权限")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/email")
	if path == "/settings" && r.Method == "GET" {
		v, e := a.store.emailSettings()
		if e != nil {
			emailAPIError(w, e)
			return
		}
		jsonResponse(w, 200, emailPublicSettings(v))
		return
	}
	if path == "/settings" && r.Method == "PUT" {
		var in struct {
			EmailSettings
			AdminPassword string `json:"admin_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if _, e := a.commerceActor(actor, true, in.AdminPassword); e != nil {
			emailAPIError(w, e)
			return
		}
		v, e := a.store.saveEmailSettings(in.EmailSettings)
		if e != nil {
			emailAPIError(w, e)
			return
		}
		a.store.audit(actor.Username, "email-settings", "smtp")
		jsonResponse(w, 200, emailPublicSettings(v))
		return
	}
	if path == "/deliveries" && r.Method == "GET" {
		items, e := a.store.emailDeliveries()
		if e != nil {
			emailAPIError(w, e)
			return
		}
		jsonResponse(w, 200, object{"items": items})
		return
	}
	if r.Method == "POST" && (path == "/test" || path == "/deliveries/retry") {
		var in struct {
			To            string `json:"to"`
			ID            string `json:"id"`
			AdminPassword string `json:"admin_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if _, e := a.commerceActor(actor, true, in.AdminPassword); e != nil {
			emailAPIError(w, e)
			return
		}
		settings, e := a.store.emailSettings()
		if e != nil {
			emailAPIError(w, e)
			return
		}
		if !emailReady(settings) {
			failure(w, 409, "请先配置并启用 SMTP")
			return
		}
		if path == "/test" {
			to, e := emailAddress(in.To)
			if e != nil {
				emailAPIError(w, e)
				return
			}
			if !a.store.emailRateAllowed("test", requestIP(r), to, time.Now().Unix()) {
				failure(w, 429, "邮件请求过于频繁，请稍后重试")
				return
			}
			id, e := a.store.queueEmail(0, to, "test", "邮件接入测试", "SMTP 配置测试邮件。收到此邮件表示服务器已完成投递。", "", 0)
			if e != nil {
				emailAPIError(w, e)
				return
			}
			jsonResponse(w, 202, object{"delivery_id": id})
			return
		}
		if e = a.store.retryEmail(in.ID, time.Now().Unix()); e != nil {
			emailAPIError(w, e)
			return
		}
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	failure(w, 404, "接口不存在")
}
func (s *Store) emailRateAllowed(purpose, ip, address string, now int64) bool {
	// Persistent per-source and per-target counters survive process restarts.
	for _, entry := range []struct {
		key   string
		limit int
	}{{"ip:" + purpose + ":" + ip, 20}, {"address:" + address, 5}} {
		var count int
		err := s.db.QueryRow("INSERT INTO email_rate_limits(key,bucket,count) VALUES(?,?,1) ON CONFLICT(key,bucket) DO UPDATE SET count=email_rate_limits.count+1 RETURNING count", digest(entry.key), now/600).Scan(&count)
		if err != nil || count > entry.limit {
			return false
		}
	}
	return true
}
func emailEndpoint(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
