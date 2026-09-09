package main

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Presentation settings do not touch protocol configuration or credentials.
type SiteSettings struct {
	PanelName     string `json:"panel_name"`
	Organization  string `json:"organization"`
	DefaultLocale string `json:"default_locale"`
	SupportEmail  string `json:"support_email"`
	LoginNotice   string `json:"login_notice"`
	Revision      string `json:"revision"`
}

func (s *Store) siteSettings() SiteSettings {
	v := SiteSettings{PanelName: "广月面板", Organization: "跨境电商工作区", DefaultLocale: "zh-CN"}
	_ = json.Unmarshal([]byte(s.meta("site_settings")), &v)
	return v
}
func (a *App) siteInfo(w http.ResponseWriter, r *http.Request) {
	v := a.store.siteSettings()
	v.Revision = ""
	jsonResponse(w, 200, v)
}
func (a *App) panelSettings(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.Method == "GET" {
		jsonResponse(w, 200, a.store.siteSettings())
		return
	}
	var v SiteSettings
	if !decode(w, r, &v) {
		return
	}
	v.PanelName = strings.TrimSpace(v.PanelName)
	v.Organization = strings.TrimSpace(v.Organization)
	v.SupportEmail = strings.TrimSpace(v.SupportEmail)
	v.LoginNotice = strings.TrimSpace(v.LoginNotice)
	if v.PanelName == "" || utf8.RuneCountInString(v.PanelName) > 40 || v.Organization == "" || utf8.RuneCountInString(v.Organization) > 60 || utf8.RuneCountInString(v.LoginNotice) > 500 || (v.DefaultLocale != "zh-CN" && v.DefaultLocale != "en") {
		failure(w, 400, "面板名称、组织名称或语言设置无效")
		return
	}
	if v.SupportEmail != "" {
		addr, e := mail.ParseAddress(v.SupportEmail)
		if e != nil || addr.Address != v.SupportEmail || len(v.SupportEmail) > 254 {
			failure(w, 400, "联系邮箱无效")
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store.siteSettings().Revision != v.Revision {
		failure(w, 409, "设置已被其他管理员修改，请重新加载")
		return
	}
	v.Revision = randomToken(12)
	b, _ := json.Marshal(v)
	if err := a.store.setMeta("site_settings", string(b)); err != nil {
		failure(w, 500, "保存系统设置失败")
		return
	}
	a.store.audit(actor.Username, "update-settings", "panel")
	jsonResponse(w, 200, v)
}

type PanelMessage struct {
	ID         int64  `json:"id"`
	Sender     string `json:"sender"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Category   string `json:"category"`
	Created    int64  `json:"created"`
	ReadAt     int64  `json:"read_at"`
	Recipients int    `json:"recipients"`
	ReadCount  int    `json:"read_count"`
}

func (s *Store) unreadMessages(user int64) int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM message_recipients WHERE user_id=? AND deleted=0 AND read_at=0", user).Scan(&n)
	return n
}
func (a *App) messages(w http.ResponseWriter, r *http.Request, actor Record) {
	path := strings.TrimPrefix(r.URL.Path, "/api/messages")
	if path == "" && r.Method == "GET" {
		a.listMessages(w, r, actor)
		return
	}
	if path == "" && r.Method == "POST" {
		if actor.Role != "owner" {
			failure(w, 403, "需要管理员权限")
			return
		}
		a.sendMessage(w, r, actor)
		return
	}
	if path == "/read-all" && r.Method == "POST" {
		_, err := a.store.db.Exec("UPDATE message_recipients SET read_at=? WHERE user_id=? AND deleted=0 AND read_at=0", time.Now().Unix(), actor.ID)
		if err != nil {
			failure(w, 500, "更新站内信失败")
			return
		}
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		failure(w, 404, "站内信不存在")
		return
	}
	var query string
	var args []any
	if len(parts) == 2 && parts[1] == "read" && r.Method == "POST" {
		query = "UPDATE message_recipients SET read_at=CASE WHEN read_at=0 THEN ? ELSE read_at END WHERE message_id=? AND user_id=? AND deleted=0"
		args = []any{time.Now().Unix(), id, actor.ID}
	} else if len(parts) == 1 && r.Method == "DELETE" {
		query = "UPDATE message_recipients SET deleted=1 WHERE message_id=? AND user_id=? AND deleted=0"
		args = []any{id, actor.ID}
	} else {
		failure(w, 404, "接口不存在")
		return
	}
	result, e := a.store.db.Exec(query, args...)
	if e != nil {
		failure(w, 500, "更新站内信失败")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		failure(w, 404, "站内信不存在")
		return
	}
	jsonResponse(w, 200, object{"ok": true})
}
func (a *App) listMessages(w http.ResponseWriter, r *http.Request, actor Record) {
	sent := r.URL.Query().Get("folder") == "sent"
	if sent && actor.Role != "owner" {
		failure(w, 403, "需要管理员权限")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	query := `SELECT m.id,m.sender_name,m.title,m.body,m.category,m.created,r.read_at,0,0 FROM messages m JOIN message_recipients r ON r.message_id=m.id WHERE r.user_id=? AND r.deleted=0 AND m.id<?`
	if sent {
		query = `SELECT m.id,m.sender_name,m.title,m.body,m.category,m.created,0,(SELECT COUNT(*) FROM message_recipients WHERE message_id=m.id),(SELECT COUNT(*) FROM message_recipients WHERE message_id=m.id AND read_at>0) FROM messages m WHERE m.sender_id=? AND m.id<?`
	}
	if !sent && r.URL.Query().Get("unread") == "1" {
		query += " AND r.read_at=0"
	}
	rows, err := a.store.db.Query(query+" ORDER BY m.id DESC LIMIT 31", actor.ID, before)
	if err != nil {
		failure(w, 500, "读取站内信失败")
		return
	}
	out := []PanelMessage{}
	for rows.Next() {
		var m PanelMessage
		if err = rows.Scan(&m.ID, &m.Sender, &m.Title, &m.Body, &m.Category, &m.Created, &m.ReadAt, &m.Recipients, &m.ReadCount); err != nil {
			break
		}
		out = append(out, m)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		failure(w, 500, "读取站内信失败")
		return
	}
	var next int64
	if len(out) > 30 {
		out = out[:30]
		next = out[29].ID
	}
	jsonResponse(w, 200, object{"items": out, "next_before": next, "unread": a.store.unreadMessages(actor.ID)})
}
func (a *App) sendMessage(w http.ResponseWriter, r *http.Request, actor Record) {
	var input struct {
		Title       string `json:"title"`
		Body        string `json:"body"`
		Category    string `json:"category"`
		RecipientID int64  `json:"recipient_id"`
		All         bool   `json:"all"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 120 || input.Body == "" || utf8.RuneCountInString(input.Body) > 8000 || (input.Category != "notice" && input.Category != "maintenance" && input.Category != "account") || (input.All && input.RecipientID != 0) || (!input.All && input.RecipientID <= 0) {
		failure(w, 400, "请填写有效的收件人、标题和正文")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		failure(w, 500, "发送站内信失败")
		return
	}
	defer tx.Rollback()
	var recent int
	err = tx.QueryRow("SELECT COUNT(*) FROM messages WHERE sender_id=? AND created>?", actor.ID, time.Now().Add(-time.Minute).Unix()).Scan(&recent)
	if err != nil {
		failure(w, 500, "发送站内信失败")
		return
	}
	if recent >= 10 {
		failure(w, 429, "发送过于频繁，请稍后再试")
		return
	}
	result, err := tx.Exec("INSERT INTO messages(sender_id,sender_name,title,body,category,created) VALUES(?,?,?,?,?,?)", actor.ID, actor.Username, input.Title, input.Body, input.Category, time.Now().Unix())
	if err != nil {
		failure(w, 500, "发送站内信失败")
		return
	}
	id, _ := result.LastInsertId()
	query := "INSERT INTO message_recipients(message_id,user_id) SELECT ?,id FROM users"
	args := []any{id}
	if !input.All {
		query += " WHERE id=?"
		args = append(args, input.RecipientID)
	}
	result, err = tx.Exec(query, args...)
	if err != nil {
		failure(w, 500, "发送站内信失败")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		failure(w, 404, "用户不存在")
		return
	}
	// Bound message storage; recipients are removed by FK cascade.
	_, err = tx.Exec("DELETE FROM messages WHERE id NOT IN (SELECT id FROM messages ORDER BY id DESC LIMIT 2000)")
	if err != nil || tx.Commit() != nil {
		failure(w, 500, "发送站内信失败")
		return
	}
	a.store.audit(actor.Username, "send-message", strconv.FormatInt(id, 10))
	jsonResponse(w, 201, object{"id": id, "recipients": n})
}
