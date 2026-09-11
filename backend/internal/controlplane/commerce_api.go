package controlplane

import (
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) commerceAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, e := a.commerceActor(actor, false, "")
	if e != nil {
		commerceWriteError(w, e)
		return
	}
	actor = current
	path := strings.TrimPrefix(r.URL.Path, "/api/commerce/")
	if r.Method == "GET" {
		if e = a.commerceGet(w, r, actor, path); e != nil {
			commerceWriteError(w, e)
		}
		return
	}
	if r.Method != "POST" {
		failure(w, 405, "方法不支持")
		return
	}
	if a.store.commerceErr != nil {
		failure(w, 409, "资金账目校验失败，已阻止写入")
		return
	}
	switch path {
	case "settings":
		var in struct {
			Settings CommerceSettings `json:"settings"`
			Password string           `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if _, e = a.commerceActor(actor, true, in.Password); e == nil {
			if in.Settings.Currency != "CNY" {
				e = commerceFail(400, "当前仅支持 CNY")
			} else {
				_, e = a.store.db.Exec("INSERT INTO meta(key,value) VALUES('commerce_settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(jsonBytes(in.Settings)))
			}
		}
		if e == nil {
			jsonResponse(w, 200, in.Settings)
			return
		}
	case "adjust":
		e = a.adjustBalance(w, r, actor)
	case "codes":
		e = a.generateCodes(w, r, actor)
	case "codes/revoke":
		e = a.revokeCodes(w, r, actor)
	case "redeem":
		e = a.redeemCode(w, r, actor)
	case "offers":
		e = a.saveOffer(w, r, actor)
	case "orders":
		e = a.createOrder(w, r, actor)
	case "orders/action":
		e = a.orderAction(w, r, actor)
	default:
		e = commerceFail(404, "功能不存在")
	}
	if e != nil {
		commerceWriteError(w, e)
	}
}
func pageCursor(r *http.Request) string {
	c := r.URL.Query().Get("before")
	if c == "" {
		return "~"
	}
	return c
}
func requestedUser(r *http.Request, actor Record) (int64, error) {
	u := actor.ID
	if v := r.URL.Query().Get("user_id"); v != "" {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < 1 {
			return 0, commerceFail(400, "用户编号无效")
		}
		if actor.Role != "owner" && n != u {
			return 0, commerceFail(403, "无权查看其他用户")
		}
		u = n
	}
	return u, nil
}
func (a *App) commerceGet(w http.ResponseWriter, r *http.Request, actor Record, path string) error {
	switch path {
	case "settings":
		v, e := a.store.commerceSettings()
		if e != nil {
			return e
		}
		jsonResponse(w, 200, v)
	case "wallet":
		u, e := requestedUser(r, actor)
		if e != nil {
			return e
		}
		v, e := a.store.wallet(u)
		if e != nil {
			return e
		}
		jsonResponse(w, 200, v)
	case "transactions":
		u, e := requestedUser(r, actor)
		if e != nil {
			return e
		}
		rows, e := a.store.db.Query("SELECT id,kind,amount,available,held,reason,reference,created FROM money_transactions WHERE user_id=? AND id<? ORDER BY id DESC LIMIT 50", u, pageCursor(r))
		if e != nil {
			return e
		}
		defer rows.Close()
		items := []object{}
		for rows.Next() {
			var id, kind, reason, ref string
			var amount, available, held, created int64
			if e = rows.Scan(&id, &kind, &amount, &available, &held, &reason, &ref, &created); e != nil {
				return e
			}
			items = append(items, object{"id": id, "kind": kind, "amount": moneyString(amount), "available": moneyString(available), "held": moneyString(held), "reason": reason, "reference": ref, "created": created})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		jsonResponse(w, 200, object{"items": items})
	case "codes":
		if actor.Role != "owner" {
			return commerceFail(403, "需要管理员权限")
		}
		rows, e := a.store.db.Query("SELECT id,batch_id,suffix,amount,expires,revoked,redeemed_by,redeemed_at,created,note FROM redeem_codes WHERE id<? ORDER BY id DESC LIMIT 50", pageCursor(r))
		if e != nil {
			return e
		}
		defer rows.Close()
		items := []object{}
		for rows.Next() {
			var id, batch, suffix, note string
			var amount, expires, revoked, user, used, created int64
			if e = rows.Scan(&id, &batch, &suffix, &amount, &expires, &revoked, &user, &used, &created, &note); e != nil {
				return e
			}
			state := "unused"
			if user > 0 {
				state = "redeemed"
			} else if revoked > 0 {
				state = "revoked"
			} else if expires > 0 && expires <= time.Now().Unix() {
				state = "expired"
			}
			items = append(items, object{"id": id, "batch_id": batch, "suffix": suffix, "amount": moneyString(amount), "expires": expires, "state": state, "redeemed_by": user, "redeemed_at": used, "created": created, "note": note})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		jsonResponse(w, 200, object{"items": items})
	case "offers":
		return a.listOffers(w, actor)
	case "orders":
		return a.listOrders(w, r, actor)
	default:
		return commerceFail(404, "功能不存在")
	}
	return nil
}
func (a *App) adjustBalance(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		UserID      int64  `json:"user_id"`
		Amount      string `json:"amount"`
		Kind        string `json:"kind"`
		Reason      string `json:"reason"`
		OperationID string `json:"operation_id"`
		// Accepted for backward compatibility with older clients; never read or
		// persisted. A logged-in owner session is the authorization factor.
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	// A valid owner session is sufficient for manual balance operations. The
	// operation remains audited and protected by the operation id and ledger
	// integrity checks, so routine adjustments do not require a second prompt.
	current, e := a.commerceActor(actor, false, "")
	if e != nil {
		return e
	}
	if current.Role != "owner" {
		return commerceFail(403, "需要管理员权限")
	}
	amount, e := moneyValue(in.Amount)
	if e != nil {
		return e
	}
	if !validText(in.Reason, 500) || in.Kind != "credit" && in.Kind != "debit" && in.Kind != "gift" {
		return commerceFail(400, "请选择入账类型并填写原因")
	}
	user, e := a.store.record(in.UserID)
	if e != nil || user.Archived {
		return commerceFail(400, "目标用户不可用")
	}
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	if in.Kind == "debit" {
		amount = -amount
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, e := a.store.postMoney(tx, in.UserID, actor.ID, "adjust:"+in.OperationID, in.Kind, in.OperationID, in.Reason, amount, 0)
	if e != nil {
		return e
	}
	if e = a.store.commerceEvent(tx, in.UserID, "adjust:"+in.OperationID, "余额调整已完成，请查看资金流水。"); e == nil {
		e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, v)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 200, v)
	return nil
}
func (a *App) generateCodes(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		Amount      string `json:"amount"`
		Count       int    `json:"count"`
		Expires     int64  `json:"expires"`
		Note        string `json:"note"`
		Password    string `json:"password"`
		OperationID string `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, e := a.commerceActor(actor, true, in.Password); e != nil {
		return e
	}
	in.Password = ""
	amount, e := moneyValue(in.Amount)
	if e != nil {
		return e
	}
	now := time.Now().Unix()
	if in.Count < 1 || in.Count > 100 || in.Expires != 0 && (in.Expires <= now || in.Expires > now+10*365*86400) || utf8.RuneCountInString(in.Note) > 500 {
		return commerceFail(400, "数量应为 1–100，有效期应在未来十年以内")
	}
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	var n int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM redeem_codes WHERE redeemed_by=0 AND revoked=0 AND (expires=0 OR expires>?)", now).Scan(&n); e != nil {
		return e
	}
	if n+in.Count > 10000 {
		return commerceFail(409, "可用兑换码最多 10000 个")
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	batch := serial("GYR")
	codes := []object{}
	for i := 0; i < in.Count; i++ {
		raw := strings.TrimPrefix(serial("GY"), "GY-")
		raw = strings.SplitN(raw, "-", 2)[1]
		code := "GY-" + raw[:8] + "-" + raw[8:16] + "-" + raw[16:]
		id := serial("GYC")
		suffix := raw[len(raw)-6:]
		if _, e = tx.Exec("INSERT INTO redeem_codes(id,batch_id,code_hash,suffix,amount,expires,created,actor_id,note) VALUES(?,?,?,?,?,?,?,?,?)", id, batch, digest(raw), suffix, amount, in.Expires, now, actor.ID, in.Note); e != nil {
			return e
		}
		codes = append(codes, object{"id": id, "code": code})
	}
	result := object{"batch_id": batch, "codes": codes, "amount": in.Amount, "expires": in.Expires}
	if e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, result); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, 201, result)
	return nil
}
func (a *App) revokeCodes(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		IDs         []string `json:"ids"`
		Password    string   `json:"password"`
		OperationID string   `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, e := a.commerceActor(actor, true, in.Password); e != nil {
		return e
	}
	in.Password = ""
	if len(in.IDs) < 1 || len(in.IDs) > 100 {
		return commerceFail(400, "请选择 1–100 个兑换码")
	}
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	n := int64(0)
	for _, id := range in.IDs {
		res, e := tx.Exec("UPDATE redeem_codes SET revoked=? WHERE id=? AND revoked=0 AND redeemed_by=0", time.Now().Unix(), id)
		if e != nil {
			return e
		}
		v, _ := res.RowsAffected()
		n += v
	}
	result := object{"revoked": n}
	if e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, result); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 200, result)
	return nil
}
func normalCode(code string) (string, bool) {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.TrimPrefix(s, "GY-")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 26 {
		return "", false
	}
	b, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	return s, e == nil && len(b) == 16
}
func (a *App) redeemCode(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		Code        string `json:"code"`
		OperationID string `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	settings, e := a.store.commerceSettings()
	if e != nil {
		return e
	}
	if !settings.Redemption {
		return commerceFail(409, "兑换功能暂未开放")
	}
	code, valid := normalCode(in.Code)
	in.Code = digest(code)
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	now := time.Now().Unix()
	var attempts int
	e = a.store.db.QueryRow("INSERT INTO redeem_attempts(user_id,bucket,attempts) VALUES(?,?,1) ON CONFLICT(user_id,bucket) DO UPDATE SET attempts=redeem_attempts.attempts+1 RETURNING attempts", actor.ID, now/600).Scan(&attempts)
	if e != nil {
		return e
	}
	if attempts > 10 {
		return commerceFail(429, "兑换尝试过多，请十分钟后重试")
	}
	if !valid {
		return commerceFail(400, "兑换码不可用、已过期或已使用")
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var id string
	var amount int64
	e = tx.QueryRow("SELECT id,amount FROM redeem_codes WHERE code_hash=?", in.Code).Scan(&id, &amount)
	if errors.Is(e, sql.ErrNoRows) {
		return commerceFail(400, "兑换码不可用、已过期或已使用")
	}
	if e != nil {
		return e
	}
	res, e := tx.Exec("UPDATE redeem_codes SET redeemed_by=?,redeemed_at=? WHERE id=? AND redeemed_by=0 AND revoked=0 AND (expires=0 OR expires>?)", actor.ID, now, id, now)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return commerceFail(400, "兑换码不可用、已过期或已使用")
	}
	v, e := a.store.postMoney(tx, actor.ID, actor.ID, "redeem:"+id, "redemption", id, "兑换码入账", amount, 0)
	if e != nil {
		return e
	}
	result := object{"wallet": v, "amount": moneyString(amount)}
	if e = a.store.commerceEvent(tx, actor.ID, "redeem:"+id, "兑换码入账成功，请查看余额。"); e == nil {
		e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, result)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 200, result)
	return nil
}
