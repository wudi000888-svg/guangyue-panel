package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"io"
	"net/http"
	"strings"
	"time"
)

type Ticket struct {
	ID       string `json:"id"`
	UserID   int64  `json:"user_id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	State    string `json:"state"`
	Priority string `json:"priority"`
	OrderID  string `json:"order_id"`
	SiteID   string `json:"site_id"`
	NodeID   string `json:"node_id"`
	Resource string `json:"resource"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
	Closed   int64  `json:"closed"`
	Revision string `json:"revision"`
}
type TicketReply struct {
	ID          string   `json:"id"`
	TicketID    string   `json:"ticket_id"`
	UserID      int64    `json:"user_id"`
	Sender      string   `json:"sender"`
	Body        string   `json:"body"`
	Internal    bool     `json:"internal"`
	Attachments []string `json:"attachments"`
	Created     int64    `json:"created"`
}

func (s *Store) ticket(id string) (Ticket, error) {
	var v Ticket
	var b []byte
	e := s.db.QueryRow("SELECT doc FROM support_tickets WHERE id=?", id).Scan(&b)
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return v, e
}
func (s *Store) visibleTicket(id string, actor Record) (Ticket, error) {
	v, e := s.ticket(id)
	if e != nil || actor.Role != "owner" && v.UserID != actor.ID {
		return v, commerceFail(404, "工单不存在")
	}
	return v, nil
}
func (a *App) supportAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	path := strings.TrimPrefix(r.URL.Path, "/api/support/")
	// Image decoding never holds the control-plane lock.
	if path == "upload" && r.Method == "POST" {
		if e := a.uploadTicketImage(w, r, actor); e != nil {
			commerceWriteError(w, e)
		}
		return
	}
	if path == "attachment" && r.Method == "GET" {
		if e := a.supportGet(w, r, actor, path); e != nil {
			commerceWriteError(w, e)
		}
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	actor, e := a.commerceActor(actor, false, "")
	if e != nil {
		commerceWriteError(w, e)
		return
	}
	if r.Method == "GET" {
		e = a.supportGet(w, r, actor, path)
	} else if r.Method == "POST" {
		switch path {
		case "tickets":
			e = a.createTicket(w, r, actor)
		case "reply":
			e = a.replyTicket(w, r, actor)
		case "state":
			e = a.changeTicket(w, r, actor)
		default:
			e = commerceFail(404, "功能不存在")
		}
	} else {
		e = commerceFail(405, "方法不支持")
	}
	if e != nil {
		commerceWriteError(w, e)
	}
}
func (a *App) supportGet(w http.ResponseWriter, r *http.Request, actor Record, path string) error {
	switch path {
	case "resources":
		out := []object{}
		nodes, e := a.store.nodes()
		if e != nil {
			return e
		}
		for _, n := range nodes {
			if actor.Role == "owner" || supportNodeAllowed(actor, n) {
				out = append(out, object{"site_id": a.cfg.siteID(), "node_id": n.ID, "name": n.Name})
			}
		}
		sites, e := a.store.businessSites()
		if e != nil {
			return e
		}
		for _, site := range sites {
			assigned := actor.Role == "owner"
			for _, g := range site.Grants {
				if g.UserID == actor.ID {
					assigned = true
				}
			}
			if !assigned {
				continue
			}
			ns, e := a.materializeBusinessNodes(site)
			if e != nil {
				return e
			}
			for _, n := range ns {
				if actor.Role == "owner" || supportNodeAllowed(actor, n) {
					out = append(out, object{"site_id": site.ID, "node_id": n.ID, "name": site.Name + " / " + n.Name})
				}
			}
		}
		jsonResponse(w, 200, object{"items": out})
		return nil
	case "tickets":
		query := "SELECT doc FROM support_tickets WHERE id<?"
		args := []any{pageCursor(r)}
		if actor.Role != "owner" {
			query += " AND user_id=?"
			args = append(args, actor.ID)
		}
		query += " ORDER BY id DESC LIMIT 50"
		rows, e := a.store.db.Query(query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []Ticket{}
		for rows.Next() {
			var b []byte
			var t Ticket
			if e = rows.Scan(&b); e != nil {
				return e
			}
			if e = json.Unmarshal(b, &t); e != nil {
				return e
			}
			out = append(out, t)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		jsonResponse(w, 200, object{"items": out})
	case "ticket":
		t, e := a.store.visibleTicket(r.URL.Query().Get("id"), actor)
		if e != nil {
			return e
		}
		query := "SELECT doc FROM support_replies WHERE ticket_id=? AND id<?"
		args := []any{t.ID, pageCursor(r)}
		if actor.Role != "owner" {
			query += " AND internal=0"
		}
		query += " ORDER BY id DESC LIMIT 50"
		rows, e := a.store.db.Query(query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []TicketReply{}
		for rows.Next() {
			var b []byte
			var v TicketReply
			if e = rows.Scan(&b); e != nil {
				return e
			}
			if e = json.Unmarshal(b, &v); e != nil {
				return e
			}
			out = append(out, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		jsonResponse(w, 200, object{"ticket": t, "replies": out})
	case "attachment":
		var owner, internal, expires int64
		var ticket, mime string
		var b []byte
		e := a.store.db.QueryRow("SELECT user_id,ticket_id,internal,expires,mime,body FROM support_attachments WHERE id=?", r.URL.Query().Get("id")).Scan(&owner, &ticket, &internal, &expires, &mime, &b)
		if e != nil {
			return commerceFail(404, "图片不存在或已过期")
		}
		if ticket == "" {
			if owner != actor.ID || expires <= time.Now().Unix() {
				return commerceFail(404, "图片不存在或已过期")
			}
		} else {
			if _, e = a.store.visibleTicket(ticket, actor); e != nil {
				return e
			}
			if internal != 0 && actor.Role != "owner" {
				return commerceFail(404, "图片不存在或已过期")
			}
		}
		var body []byte
		if e = a.store.vault.open(b, &body); e != nil {
			return e
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Content-Disposition", "inline")
		w.WriteHeader(200)
		_, e = w.Write(body)
		return e
	default:
		return commerceFail(404, "功能不存在")
	}
	return nil
}
func (a *App) ticketSettings() error {
	s, e := a.store.commerceSettings()
	if e != nil {
		return e
	}
	if !s.Tickets {
		return commerceFail(409, "工单提交暂未开放")
	}
	return nil
}
func (a *App) ticketResource(u Record, siteID, nodeID string) (string, error) {
	if siteID == "" && nodeID == "" {
		return "", nil
	}
	if nodeID == "" {
		return "", commerceFail(400, "请选择关联节点")
	}
	if siteID == "" || siteID == a.cfg.siteID() {
		nodes, e := a.store.nodes()
		var n Node
		found := false
		for _, v := range nodes {
			if v.ID == nodeID {
				n = v
				found = true
				break
			}
		}
		if e != nil || !found || u.Role != "owner" && !supportNodeAllowed(u, n) {
			return "", commerceFail(403, "无权关联此节点")
		}
		return n.Name, nil
	}
	sites, e := a.store.businessSites()
	if e != nil {
		return "", e
	}
	for _, s := range sites {
		if s.ID != siteID {
			continue
		}
		allowed := u.Role == "owner"
		for _, g := range s.Grants {
			if g.UserID == u.ID {
				allowed = true
			}
		}
		if !allowed {
			break
		}
		nodes, e := a.materializeBusinessNodes(s)
		if e != nil {
			return "", e
		}
		for _, n := range nodes {
			if n.ID == nodeID && (u.Role == "owner" || supportNodeAllowed(u, n)) {
				return s.Name + " / " + n.Name, nil
			}
		}
	}
	return "", commerceFail(403, "无权关联此节点")
}
func bindTicketImages(tx *persistence.Tx, ids []string, actor int64, ticket, reply string, internal bool) error {
	if len(ids) > 3 {
		return commerceFail(400, "每次最多上传 3 张图片")
	}
	var total int64
	if e := tx.QueryRow("SELECT COALESCE(SUM(size),0) FROM support_attachments WHERE ticket_id=?", ticket).Scan(&total); e != nil {
		return e
	}
	var added int64
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return commerceFail(400, "图片不能重复绑定")
		}
		seen[id] = true
		var size int64
		e := tx.QueryRow("SELECT size FROM support_attachments WHERE id=? AND user_id=? AND ticket_id='' AND expires>?", id, actor, time.Now().Unix()).Scan(&size)
		if e != nil {
			return commerceFail(400, "图片不存在、已过期或不属于当前用户")
		}
		added += size
		if added > 6<<20 || total+added > 20<<20 {
			return commerceFail(409, "工单图片容量已达上限")
		}
		res, e := tx.Exec("UPDATE support_attachments SET ticket_id=?,reply_id=?,internal=?,expires=0 WHERE id=? AND user_id=? AND ticket_id=''", ticket, reply, boolInt(internal), id, actor)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errCommerceConflict
		}
	}
	return nil
}
func (a *App) createTicket(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		Title       string   `json:"title"`
		Category    string   `json:"category"`
		Body        string   `json:"body"`
		OrderID     string   `json:"order_id"`
		SiteID      string   `json:"site_id"`
		NodeID      string   `json:"node_id"`
		Attachments []string `json:"attachments"`
		OperationID string   `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if e := a.ticketSettings(); e != nil {
		return e
	}
	if !validText(in.Title, 120) || !validText(in.Body, 4000) || !map[string]bool{"account": true, "order": true, "node": true, "usage": true, "other": true}[in.Category] {
		return commerceFail(400, "请填写有效的标题、分类和正文")
	}
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	if in.OrderID != "" {
		o, e := a.store.order(in.OrderID)
		if e != nil || actor.Role != "owner" && o.UserID != actor.ID {
			return commerceFail(403, "无权关联此订单")
		}
	}
	resource, e := a.ticketResource(actor, in.SiteID, in.NodeID)
	if e != nil {
		return e
	}
	now := time.Now().Unix()
	var open, today int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM support_tickets WHERE user_id=? AND state NOT IN ('closed','resolved')", actor.ID).Scan(&open); e != nil {
		return e
	}
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM support_tickets WHERE user_id=? AND created>?", actor.ID, now-86400).Scan(&today); e != nil {
		return e
	}
	if actor.Role != "owner" && (open >= 5 || today >= 10) {
		return commerceFail(429, "工单数量已达上限，请先处理已有工单")
	}
	t := Ticket{ID: serial("GYT"), UserID: actor.ID, Title: in.Title, Category: in.Category, State: "open", Priority: "normal", OrderID: in.OrderID, SiteID: in.SiteID, NodeID: in.NodeID, Resource: resource, Created: now, Updated: now, Revision: serial("GYV")}
	reply := TicketReply{ID: serial("GYM"), TicketID: t.ID, UserID: actor.ID, Sender: actor.Username, Body: in.Body, Attachments: in.Attachments, Created: now}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO support_tickets(id,user_id,state,created,updated,doc) VALUES(?,?,?,?,?,?)", t.ID, t.UserID, t.State, now, now, jsonBytes(t)); e != nil {
		return e
	}
	if e = insertTicketReply(tx, reply); e == nil {
		e = bindTicketImages(tx, in.Attachments, actor.ID, t.ID, reply.ID, false)
	}
	if e == nil {
		e = a.ticketNotify(tx, actor, t, reply.ID, "收到新工单："+t.ID)
	}
	if e == nil {
		e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, t)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 201, t)
	return nil
}
func insertTicketReply(tx *persistence.Tx, v TicketReply) error {
	_, e := tx.Exec("INSERT INTO support_replies(id,ticket_id,user_id,internal,created,doc) VALUES(?,?,?,?,?,?)", v.ID, v.TicketID, v.UserID, boolInt(v.Internal), v.Created, jsonBytes(v))
	return e
}
func (a *App) ticketNotify(tx *persistence.Tx, actor Record, t Ticket, key, body string) error {
	if actor.Role == "owner" {
		return a.store.commerceEvent(tx, t.UserID, "ticket:"+key, body)
	}
	rows, e := tx.Query("SELECT id,doc FROM users")
	if e != nil {
		return e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		var b []byte
		var u User
		if e = rows.Scan(&id, &b); e != nil {
			rows.Close()
			return e
		}
		if json.Unmarshal(b, &u) == nil && u.Enabled && u.Role == "owner" {
			ids = append(ids, id)
		}
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, id := range ids {
		if e = a.store.commerceEvent(tx, id, "ticket:"+key+":"+businessUsageKey(id), body); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) replyTicket(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID          string   `json:"id"`
		Body        string   `json:"body"`
		Internal    bool     `json:"internal"`
		Attachments []string `json:"attachments"`
		OperationID string   `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if e := a.ticketSettings(); e != nil {
		return e
	}
	if !validText(in.Body, 4000) || in.Internal && actor.Role != "owner" {
		return commerceFail(400, "回复无效")
	}
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	t, e := a.store.visibleTicket(in.ID, actor)
	if e != nil {
		return e
	}
	if t.State == "closed" || t.State == "resolved" {
		return commerceFail(409, "请先重新打开工单")
	}
	now := time.Now().Unix()
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM support_replies WHERE user_id=? AND created>?", actor.ID, now-3600).Scan(&count); e != nil {
		return e
	}
	if actor.Role != "owner" && count >= 20 {
		return commerceFail(429, "回复过于频繁，请稍后再试")
	}
	reply := TicketReply{ID: serial("GYM"), TicketID: t.ID, UserID: actor.ID, Sender: actor.Username, Body: in.Body, Internal: in.Internal, Attachments: in.Attachments, Created: now}
	previous := t.Revision
	t.Updated = now
	t.Revision = serial("GYV")
	if !in.Internal {
		if actor.Role == "owner" {
			t.State = "waiting_user"
		} else {
			t.State = "open"
		}
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = insertTicketReply(tx, reply); e == nil {
		e = bindTicketImages(tx, in.Attachments, actor.ID, t.ID, reply.ID, in.Internal)
	}
	if e == nil {
		e = saveTicket(tx, t, previous)
	}
	if e == nil && !in.Internal {
		e = a.ticketNotify(tx, actor, t, reply.ID, "工单收到回复："+t.ID)
	}
	if e == nil {
		e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, t)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 200, t)
	return nil
}
func saveTicket(tx *persistence.Tx, t Ticket, previous string) error {
	var b []byte
	if e := tx.QueryRow("SELECT doc FROM support_tickets WHERE id=?", t.ID).Scan(&b); e != nil {
		return e
	}
	var old Ticket
	if e := json.Unmarshal(b, &old); e != nil {
		return e
	}
	if old.Revision != previous {
		return errCommerceConflict
	}
	r, e := tx.Exec("UPDATE support_tickets SET state=?,updated=?,doc=? WHERE id=? AND doc=?", t.State, t.Updated, jsonBytes(t), t.ID, b)
	if e == nil {
		n, _ := r.RowsAffected()
		if n != 1 {
			e = errCommerceConflict
		}
	}
	return e
}
func (a *App) changeTicket(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		Priority string `json:"priority"`
		Revision string `json:"revision"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	t, e := a.store.visibleTicket(in.ID, actor)
	if e != nil {
		return e
	}
	if t.Revision != in.Revision {
		return errCommerceConflict
	}
	now := time.Now().Unix()
	if in.State == t.State && (in.Priority == "" || in.Priority == t.Priority) {
		jsonResponse(w, 200, t)
		return nil
	}
	if actor.Role != "owner" && in.State == "open" && t.Closed > 0 {
		var n int
		if e = a.store.db.QueryRow("SELECT COUNT(*) FROM support_tickets WHERE user_id=? AND state NOT IN ('closed','resolved')", actor.ID).Scan(&n); e != nil {
			return e
		}
		if n >= 5 {
			return commerceFail(429, "工单数量已达上限，请先处理已有工单")
		}
	}
	previousClosed := t.Closed
	if actor.Role != "owner" {
		if in.State != "closed" && in.State != "open" || in.Priority != "" && in.Priority != t.Priority {
			return commerceFail(403, "无权修改此状态")
		}
		if in.State == "open" && (t.Closed == 0 || now-t.Closed > 7*86400) {
			return commerceFail(409, "请新建工单并关联原工单号")
		}
	} else if !map[string]bool{"open": true, "processing": true, "waiting_user": true, "resolved": true, "closed": true}[in.State] {
		return commerceFail(400, "工单状态无效")
	}
	if in.Priority != "" {
		if in.Priority != "normal" && in.Priority != "urgent" {
			return commerceFail(400, "优先级无效")
		}
		t.Priority = in.Priority
	}
	t.State = in.State
	t.Closed = 0
	if t.State == "closed" || t.State == "resolved" {
		t.Closed = previousClosed
		if t.Closed == 0 {
			t.Closed = now
		}
	}
	t.Updated = now
	t.Revision = serial("GYV")
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = saveTicket(tx, t, in.Revision); e == nil {
		e = a.ticketNotify(tx, actor, t, t.Revision, "工单状态已更新："+t.ID)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 200, t)
	return nil
}

var ticketImageSlots = make(chan struct{}, 1)

func (a *App) uploadTicketImage(w http.ResponseWriter, r *http.Request, actor Record) error {
	select {
	case ticketImageSlots <- struct{}{}:
		defer func() { <-ticketImageSlots }()
	default:
		return commerceFail(429, "图片处理中，请稍后重试")
	}
	// Authenticate before consuming the body; recheck before committing the image.
	a.mu.Lock()
	actor, e := a.commerceActor(actor, false, "")
	if e == nil {
		e = a.ticketSettings()
	}
	if e == nil {
		e = a.ticketUploadPreflight(actor.ID)
	}
	a.mu.Unlock()
	if e != nil {
		return e
	}
	var in struct {
		Name string `json:"name"`
		MIME string `json:"mime"`
		Data string `json:"data"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
		return commerceFail(413, "图片请求过大或格式无效")
	}
	ext := ""
	lower := strings.ToLower(in.Name)
	if strings.HasSuffix(lower, ".png") {
		ext = "png"
	} else if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
		ext = "jpeg"
	}
	if ext == "" || len(in.Name) > 200 || strings.ContainsAny(in.Name, "/\\") || in.MIME != "image/"+ext {
		return commerceFail(400, "仅允许 PNG、JPG、JPEG 图片")
	}
	raw, e := base64.StdEncoding.DecodeString(in.Data)
	in.Data = ""
	if e != nil || len(raw) == 0 || len(raw) > 2<<20 {
		return commerceFail(413, "每张图片最大 2 MiB")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	body, e := a.processTicketImage(ctx, raw, ext)
	if e != nil {
		return commerceFail(400, "图片无效、尺寸过大或处理超时")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, e = a.commerceActor(actor, false, ""); e != nil {
		return e
	}
	if e = a.ticketSettings(); e != nil {
		return e
	}
	var userBytes, total, uploads int64
	if e = a.store.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM support_attachments WHERE user_id=?", actor.ID).Scan(&userBytes); e != nil {
		return e
	}
	if e = a.store.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM support_attachments").Scan(&total); e != nil {
		return e
	}
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM support_attachments WHERE user_id=? AND created>?", actor.ID, time.Now().Unix()-3600).Scan(&uploads); e != nil {
		return e
	}
	budget := int64(1 << 30)
	if a.cfg.controller() {
		budget = 5 << 30
	}
	size := int64(len(body))
	if userBytes+size > 100<<20 || total+size > budget || uploads >= 30 {
		return commerceFail(409, "图片容量或上传次数已达上限")
	}
	if !ticketDiskAvailable(a.cfg.StateDir) {
		return commerceFail(507, "磁盘空间不足，请提交文字工单")
	}
	sealed, e := a.store.vault.seal(body)
	if e != nil {
		return e
	}
	id := serial("GYA")
	_, e = a.store.db.Exec("INSERT INTO support_attachments(id,user_id,size,mime,created,expires,body) VALUES(?,?,?,?,?,?,?)", id, actor.ID, size, in.MIME, time.Now().Unix(), time.Now().Unix()+900, sealed)
	if e != nil {
		return e
	}
	jsonResponse(w, 201, object{"id": id, "size": size, "mime": in.MIME})
	return nil
}
func (a *App) supportCleanup(now int64) error {
	// SQLite/PG hold encrypted image bytes, so snapshots atomically include their
	// ownership and content. Deletion and restoration cannot leave public objects.
	_, e := a.store.db.Exec("DELETE FROM support_attachments WHERE id IN (SELECT id FROM support_attachments WHERE ticket_id='' AND expires<? LIMIT 20)", now)
	if e != nil {
		return e
	}
	rows, e := a.store.db.Query("SELECT id,doc FROM support_tickets WHERE state IN ('closed','resolved') AND updated<? AND EXISTS (SELECT 1 FROM support_attachments WHERE ticket_id=support_tickets.id) LIMIT 20", now-90*86400)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		var b []byte
		var t Ticket
		if e = rows.Scan(&id, &b); e != nil {
			rows.Close()
			return e
		}
		if json.Unmarshal(b, &t) == nil && t.Closed > 0 && t.Closed < now-90*86400 {
			ids = append(ids, id)
		}
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = a.store.db.Exec("DELETE FROM support_attachments WHERE ticket_id=?", id); e != nil {
			return e
		}
	}
	_, e = a.store.db.Exec("DELETE FROM support_upload_attempts WHERE bucket<?", now/3600-24)
	return e
}

func supportNodeAllowed(u Record, n Node) bool {
	return u.Enabled && nodeGroupAllowed(u, n) && (n.Protocol == "vless" && u.VLESS || n.Protocol == "hy2" && u.HY2)
}

// Count attempts before decoding, including malformed images, to bound resource use.
func (a *App) ticketUploadPreflight(user int64) error {
	var attempts int
	e := a.store.db.QueryRow("INSERT INTO support_upload_attempts(user_id,bucket,attempts) VALUES(?,?,1) ON CONFLICT(user_id,bucket) DO UPDATE SET attempts=support_upload_attempts.attempts+1 RETURNING attempts", user, time.Now().Unix()/3600).Scan(&attempts)
	if e != nil {
		return e
	}
	if attempts > 30 {
		return commerceFail(429, "图片上传尝试已达每小时上限")
	}
	var used, total int64
	if e = a.store.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM support_attachments WHERE user_id=?", user).Scan(&used); e != nil {
		return e
	}
	if e = a.store.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM support_attachments").Scan(&total); e != nil {
		return e
	}
	budget := int64(1 << 30)
	if a.cfg.controller() {
		budget = 5 << 30
	}
	if used >= 100<<20 || total >= budget {
		return commerceFail(409, "图片容量或上传次数已达上限")
	}
	if !ticketDiskAvailable(a.cfg.StateDir) {
		return commerceFail(507, "磁盘空间不足，请提交文字工单")
	}
	return nil
}
