package controlplane

import (
	"encoding/json"
	"errors"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"net/http"
	"time"
)

type Offer struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Enabled bool   `json:"enabled"`
	Price   int64  `json:"price,string"`
	Plan    Plan   `json:"plan"`
}
type Order struct {
	ID              string `json:"id"`
	UserID          int64  `json:"user_id"`
	State           string `json:"state"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
	Expires         int64  `json:"expires"`
	Offer           Offer  `json:"offer"`
	Action          string `json:"action"`
	BeforeExpiry    int64  `json:"before_expiry"`
	BeforeRevision  string `json:"before_revision"`
	AppliedRevision string `json:"applied_revision"`
	Expected        string `json:"expected"`
	PreviousEnd     int64  `json:"previous_end"`
	Started         int64  `json:"started"`
	NextAttempt     int64  `json:"next_attempt"`
	Attempts        int    `json:"attempts"`
	Message         string `json:"message"`
}

func (s *Store) offer(id string) (Offer, error) {
	var v Offer
	var b []byte
	e := s.db.QueryRow("SELECT doc FROM commerce_offers WHERE id=?", id).Scan(&b)
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return v, e
}
func (s *Store) order(id string) (Order, error) {
	var v Order
	var b []byte
	e := s.db.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", id).Scan(&b)
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return v, e
}
func saveOrder(tx *persistence.Tx, o Order, previous string) error {
	o.Updated = time.Now().Unix()
	r, e := tx.Exec("UPDATE commerce_orders SET state=?,updated=?,doc=? WHERE id=? AND state=?", o.State, o.Updated, jsonBytes(o), o.ID, previous)
	if e == nil {
		n, _ := r.RowsAffected()
		if n != 1 {
			e = errCommerceConflict
		}
	}
	return e
}
func userCommerceVersion(u Record) string {
	period := ""
	if u.Meter != nil {
		period = u.Meter.PeriodID
	}
	return digest(string(jsonBytes(object{"entitlement": u.Entitlement, "quota": u.Quota, "expires": u.Expires, "enabled": u.Enabled, "vless": u.VLESS, "hy2": u.HY2, "period": period})))
}
func (a *App) listOffers(w http.ResponseWriter, actor Record) error {
	offers, e := readDocuments[Offer](a.store, "commerce_offers")
	if e != nil {
		return e
	}
	out := []Offer{}
	for _, o := range offers {
		p, e := a.store.plan(o.Plan.ID)
		if actor.Role == "owner" || e == nil && !p.Archived && o.Enabled {
			o.Plan.Notes = ""
			out = append(out, o)
		}
	}
	jsonResponse(w, 200, object{"items": out})
	return nil
}
func (a *App) saveOffer(w http.ResponseWriter, r *http.Request, actor Record) error {
	if actor.Role != "owner" {
		return commerceFail(403, "需要管理员权限")
	}
	var in struct {
		ID          string `json:"id"`
		Version     int    `json:"version"`
		PlanID      string `json:"plan_id"`
		PlanVersion int    `json:"plan_version"`
		Price       string `json:"price"`
		Enabled     bool   `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	price, e := moneyValue(in.Price)
	if e != nil {
		return e
	}
	p, e := a.store.plan(in.PlanID)
	if e != nil || p.Archived || p.Version != in.PlanVersion {
		return commerceFail(409, "套餐已变化或已归档")
	}
	if e = a.validatePlan(&p); e != nil {
		return e
	}
	p.Notes = ""
	o := Offer{ID: in.ID, Version: in.Version + 1, Enabled: in.Enabled, Price: price, Plan: p}
	if in.ID == "" {
		var n int
		if e = a.store.db.QueryRow("SELECT COUNT(*) FROM commerce_offers").Scan(&n); e != nil {
			return e
		}
		if n >= 256 {
			return commerceFail(409, "商品最多 256 个")
		}
		o.ID = serial("GYP")
		o.Version = 1
		_, e = a.store.db.Exec("INSERT INTO commerce_offers(id,version,enabled,doc) VALUES(?,?,?,?)", o.ID, o.Version, boolInt(o.Enabled), jsonBytes(o))
	} else {
		res, err := a.store.db.Exec("UPDATE commerce_offers SET version=?,enabled=?,doc=? WHERE id=? AND version=?", o.Version, boolInt(o.Enabled), jsonBytes(o), o.ID, in.Version)
		e = err
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				e = errCommerceConflict
			}
		}
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 200, o)
	return nil
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func (a *App) listOrders(w http.ResponseWriter, r *http.Request, actor Record) error {
	query := "SELECT doc FROM commerce_orders WHERE id<?"
	args := []any{pageCursor(r)}
	if actor.Role != "owner" || r.URL.Query().Get("all") != "1" {
		u, e := requestedUser(r, actor)
		if e != nil {
			return e
		}
		query += " AND user_id=?"
		args = append(args, u)
	}
	query += " ORDER BY id DESC LIMIT 50"
	rows, e := a.store.db.Query(query, args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		var b []byte
		var o Order
		if e = rows.Scan(&b); e != nil {
			return e
		}
		if e = json.Unmarshal(b, &o); e != nil {
			return e
		}
		out = append(out, o)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	jsonResponse(w, 200, object{"items": out})
	return nil
}
func (a *App) createOrder(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		OfferID      string `json:"offer_id"`
		OfferVersion int    `json:"offer_version"`
		OperationID  string `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	settings, e := a.store.commerceSettings()
	if e != nil {
		return e
	}
	if !settings.Sales {
		return commerceFail(409, "套餐销售暂未开放")
	}
	offer, e := a.store.offer(in.OfferID)
	if e != nil || !offer.Enabled || offer.Version != in.OfferVersion {
		return commerceFail(409, "商品已变化或已下架")
	}
	p, e := a.store.plan(offer.Plan.ID)
	if e != nil || p.Archived {
		return commerceFail(409, "套餐已归档")
	}
	if actor.Meter != nil && actor.Meter.PendingReset {
		return commerceFail(409, "旧周期正在结算，请稍后重试")
	}
	now := time.Now().Unix()
	action := "purchase"
	if actor.Entitlement != nil && (actor.Expires == 0 || actor.Expires > now) {
		if actor.Entitlement.PlanID != offer.Plan.ID || actor.Entitlement.Version != offer.Plan.Version {
			return commerceFail(409, "首版仅支持当前套餐同版本续费")
		}
		if actor.Expires == 0 || offer.Plan.ValidDays == 0 {
			return commerceFail(409, "永久权益无需续费")
		}
		action = "renew"
	}
	next := cloneCommerceUser(actor)
	assignPlan(&next, offer.Plan, now)
	if e = a.validateEntitlementSites(next, "reset"); e != nil {
		return e
	}
	if action == "purchase" {
		if e = a.validatePurchaseBudgets(next); e != nil {
			return e
		}
	}
	o := Order{ID: serial("GYO"), UserID: actor.ID, State: "pending", Created: now, Updated: now, Expires: now + 900, Offer: offer, Action: action, BeforeExpiry: actor.Expires, Expected: userCommerceVersion(actor)}
	if actor.Entitlement != nil {
		o.BeforeRevision = actor.Entitlement.Revision
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO commerce_orders(id,user_id,state,created,updated,expires,doc) VALUES(?,?,?,?,?,?,?)", o.ID, o.UserID, o.State, now, now, o.Expires, jsonBytes(o)); e != nil {
		return commerceFail(409, "请先处理已有未完成订单")
	}
	if e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, o); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	jsonResponse(w, 201, o)
	return nil
}
func (a *App) orderAction(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID          string `json:"id"`
		Action      string `json:"action"`
		OperationID string `json:"operation_id"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if in.Action == "refund" {
		if _, e := a.commerceActor(actor, true, in.Password); e != nil {
			return e
		}
	}
	in.Password = ""
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	o, e := a.store.order(in.ID)
	if e != nil {
		return commerceFail(404, "订单不存在")
	}
	if actor.Role != "owner" && actor.ID != o.UserID {
		return commerceFail(404, "订单不存在")
	}
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	old := o.State
	now := time.Now().Unix()
	deltaAvailable, deltaHeld := int64(0), int64(0)
	kind := ""
	switch in.Action {
	case "confirm":
		if actor.ID != o.UserID {
			return commerceFail(403, "订单需由所属用户确认")
		}
		settings, e := a.store.commerceSettings()
		if e != nil {
			return e
		}
		offer, e := a.store.offer(o.Offer.ID)
		p, pe := a.store.plan(o.Offer.Plan.ID)
		if !settings.Sales || e != nil || !offer.Enabled || pe != nil || p.Archived {
			return commerceFail(409, "商品已暂停销售")
		}
		if o.State != "pending" || o.Expires <= now || !u.Enabled || o.Expected != userCommerceVersion(u) || u.Meter != nil && u.Meter.PendingReset {
			return commerceFail(409, "订单或权益已变化，请取消后重新下单")
		}
		future := cloneCommerceUser(u)
		assignPlan(&future, o.Offer.Plan, now)
		if o.Action == "renew" {
			future = u
			future.Expires += int64(o.Offer.Plan.ValidDays) * 86400
		}
		if e = a.validateEntitlementSites(future, "reset"); e != nil {
			return e
		}
		if o.Action == "renew" {
			if e = a.validateBusinessUserQuota(future); e != nil {
				return e
			}
		} else if e = a.validatePurchaseBudgets(future); e != nil {
			return e
		}
		u.InitMeter("legacy-"+businessUsageKey(u.ID), now)
		o.PreviousEnd = u.Meter.End
		if o.Action == "purchase" {
			u.Meter.PendingReset = true
		}
		o.Started = now
		o.State = "provisioning"
		o.Message = "余额已冻结，正在开通"
		deltaAvailable, deltaHeld, kind = -o.Offer.Price, o.Offer.Price, "hold"
	case "cancel":
		if old != "pending" && old != "provisioning" {
			return commerceFail(409, "订单无法取消")
		}
		o.State = "cancelled"
		o.Message = "订单已取消"
		if old == "provisioning" {
			deltaAvailable, deltaHeld, kind = o.Offer.Price, -o.Offer.Price, "release"
			if o.Action == "purchase" && u.Meter != nil {
				u.Meter.PendingReset = false
			}
		}
	case "refund":
		if old != "completed" || u.Entitlement == nil || u.Entitlement.Revision != o.AppliedRevision {
			return commerceFail(409, "订单存在后续权益变化，不能自动退款")
		}
		if pending, e := a.store.commercePending(u.ID); e != nil {
			return e
		} else if pending {
			return commerceFail(409, "请先处理用户的未完成订单")
		}
		u.Meter.PendingReset = true
		o.Started = now
		o.NextAttempt = 0
		o.State = "refunding"
		o.Message = "等待订单权益撤回后退回余额"
	default:
		return commerceFail(400, "订单操作无效")
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if kind != "" {
		if _, e = a.store.postMoney(tx, u.ID, actor.ID, kind+":"+o.ID, kind, o.ID, o.Message, deltaAvailable, deltaHeld); e != nil {
			return e
		}
	}
	if e = saveOrder(tx, o, old); e == nil {
		e = txUser(tx, u.User)
	}
	if e == nil {
		e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, o)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	a.status = "pending"
	jsonResponse(w, 200, o)
	return nil
}
func (a *App) commerceWork(now int64) error {
	if a.store.commerceErr != nil {
		return a.store.commerceErr
	}
	if a.cfg.businessAgent() {
		return nil
	}
	rows, e := a.store.db.Query("SELECT doc FROM commerce_orders WHERE state IN ('pending','provisioning','refunding') ORDER BY created LIMIT 100")
	if e != nil {
		return e
	}
	orders := []Order{}
	for rows.Next() {
		var b []byte
		var o Order
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(b, &o); e != nil {
			rows.Close()
			return e
		}
		orders = append(orders, o)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	var problems []error
	for _, o := range orders {
		if o.NextAttempt > now {
			continue
		}
		if o.State == "pending" {
			if o.Expires <= now {
				o.State = "expired"
				o.Message = "订单确认已超时"
				if e = a.persistOrder(o, "pending"); e != nil {
					problems = append(problems, e)
				}
			}
			continue
		}
		if o.State == "provisioning" && now-o.Started >= 1800 {
			e = a.failProvisioning(o, "开通超时，冻结余额已退回")
		} else {
			e = a.fulfilOrder(o, now)
		}
		if e != nil {
			problems = append(problems, e)
		}
		current, readErr := a.store.order(o.ID)
		if readErr != nil {
			problems = append(problems, readErr)
			continue
		}
		if current.State == o.State {
			current.Attempts++
			current.NextAttempt = now + min(int64(60), int64(1)<<min(current.Attempts, 6))
			if e != nil {
				current.Message = "处理暂未完成，系统将自动重试"
			} else {
				current.Message = "等待旧连接与业务站权益结算"
			}
			if e = a.persistOrder(current, current.State); e != nil {
				problems = append(problems, e)
			}
		}
	}
	return errors.Join(append(problems, a.commerceNotifications(now))...)
}
func (a *App) persistOrder(o Order, old string) error {
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = saveOrder(tx, o, old); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) failProvisioning(o Order, message string) error {
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = a.store.postMoney(tx, u.ID, 0, "release:"+o.ID, "release", o.ID, message, o.Offer.Price, -o.Offer.Price); e != nil {
		return e
	}
	if u.Meter != nil && o.Action == "purchase" {
		u.Meter.PendingReset = false
	}
	if e = txUser(tx, u.User); e != nil {
		return e
	}
	o.State = "failed"
	o.Message = message
	if e = saveOrder(tx, o, "provisioning"); e != nil {
		return e
	}
	if e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":failed", o.ID+" "+message); e != nil {
		return e
	}
	return tx.Commit()
}

func (a *App) fulfilOrder(o Order, now int64) error {
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	sites, e := a.store.businessSites()
	if e != nil {
		return e
	}
	barrier := o.Action == "purchase" || o.State == "refunding"
	if barrier {
		for _, s := range sites {
			if _, ok := s.Issued[u.ID]; ok || s.IssuedUnlimited[u.ID] {
				return nil
			}
			for _, g := range s.SentGrants {
				if g.UserID == u.ID {
					return nil
				}
			}
		}
		if e = a.reconcile(); e != nil {
			return e
		}
		if e = a.collect(); e != nil {
			return e
		}
		if e = a.settleHYRevocations(); e != nil {
			return e
		}
		u, e = a.store.record(u.ID)
		if e != nil {
			return e
		}
	}
	if o.State == "provisioning" {
		future := cloneCommerceUser(u)
		if o.Action == "purchase" {
			assignPlan(&future, o.Offer.Plan, now)
		} else {
			future.Expires = max(now, future.Expires) + int64(o.Offer.Plan.ValidDays)*86400
		}
		policyErr := a.validateEntitlementSites(future, "reset")
		if policyErr == nil && o.Action == "purchase" {
			policyErr = a.validatePurchaseBudgets(future)
		}
		if policyErr == nil && o.Action == "renew" {
			policyErr = a.validateBusinessUserQuota(future)
		}
		if policyErr != nil {
			return a.failProvisioning(o, "业务站权益配置已变化，冻结余额已退回")
		}
	}
	oldState := o.State
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if !u.Enabled && oldState == "provisioning" {
		o.State = "failed"
		o.Message = "账号已停用，冻结余额已退回"
		if u.Meter != nil {
			u.Meter.PendingReset = false
		}
		_, e = a.store.postMoney(tx, u.ID, 0, "release:"+o.ID, "release", o.ID, o.Message, o.Offer.Price, -o.Offer.Price)
	} else if oldState == "refunding" {
		if u.Entitlement == nil || u.Entitlement.Revision != o.AppliedRevision {
			return commerceFail(409, "退款权益版本不匹配")
		}
		u.Meter.PendingReset = false
		if o.Action == "renew" {
			u.Expires = o.BeforeExpiry
			u.Entitlement.Revision = serial("GYV")
		} else {
			u.Expires = now
		}
		o.State = "refunded"
		o.Message = "已退回站内余额"
		_, e = a.store.postMoney(tx, u.ID, 0, "refund:"+o.ID, "refund", o.ID, o.Message, o.Offer.Price, 0)
	} else {
		if o.Action == "renew" {
			u.Expires = max(now, u.Expires) + int64(o.Offer.Plan.ValidDays)*86400
			u.Entitlement.Revision = serial("GYV")
		} else {
			oldDoc := jsonBytes(u.User)
			oldPeriod := u.Meter.PeriodID
			assignPlan(&u, o.Offer.Plan, now)
			m := u.Meter
			m.PeriodID = serial("GYCYCLE")
			m.Start = now
			m.End, e = domain.NextPeriod(now, o.Offer.Plan.Cycle, o.Offer.Plan.Timezone)
			m.PendingReset = false
			m.BaseUpload, m.BaseDownload = m.Upload, m.Download
			m.RawBaseUpload, m.RawBaseDownload = u.Upload, u.Download
			m.UploadRemainder, m.DownloadRemainder = 0, 0
			if e == nil {
				_, e = tx.Exec("INSERT INTO quota_periods(user_id,period_id,doc) VALUES(?,?,?) ON CONFLICT(user_id,period_id) DO NOTHING", u.ID, oldPeriod, oldDoc)
			}
			// Existing business-site budget policy survives a new paid period, bounded
			// by the new plan's total quota. Remaining allocation uses current usage.
			for i := range sites {
				s := &sites[i]
				changed := false
				for j := range s.Grants {
					g := &s.Grants[j]
					if g.UserID != u.ID {
						continue
					}
					budget := g.Budget
					if budget == 0 && g.Quota > 0 {
						budget = g.Quota
					}
					if budget > 0 {
						g.Quota, e = domain.AddCounter(s.Usage[u.ID].total(), budget)
					} else {
						g.Quota = 0
					}
					g.Budget = budget
					g.PeriodID = m.PeriodID
					changed = true
					if e != nil {
						return e
					}
				}
				if changed {
					s.Revision = serial("GYV")
					b, err := a.store.vault.seal(s)
					if err != nil {
						return err
					}
					if _, e = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, s.ID); e != nil {
						return e
					}
				}
			}
		}
		if e == nil {
			o.AppliedRevision = u.Entitlement.Revision
			o.State = "completed"
			o.Message = "权益已开通，业务站按同步状态生效"
			_, e = a.store.postMoney(tx, u.ID, 0, "capture:"+o.ID, "purchase", o.ID, o.Message, 0, -o.Offer.Price)
		}
	}
	if e == nil {
		e = txUser(tx, u.User)
	}
	if e == nil {
		e = saveOrder(tx, o, oldState)
	}
	if e == nil {
		e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":"+o.State, o.ID+" "+o.Message)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
func (a *App) commerceNotifications(now int64) error {
	rows, e := a.store.db.Query("SELECT id,user_id,body FROM commerce_events WHERE notified=0 ORDER BY created LIMIT 20")
	if e != nil {
		return e
	}
	events := []struct {
		ID   string
		User int64
		Body string
	}{}
	for rows.Next() {
		var v struct {
			ID   string
			User int64
			Body string
		}
		if e = rows.Scan(&v.ID, &v.User, &v.Body); e != nil {
			rows.Close()
			return e
		}
		events = append(events, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, v := range events {
		tx, e := a.store.db.Begin()
		if e != nil {
			return e
		}
		var id int64
		e = tx.QueryRow("INSERT INTO messages(sender_id,sender_name,title,body,category,created) VALUES(NULL,'System',?,?, 'account',?) RETURNING id", "账户与服务通知", v.Body, now).Scan(&id)
		if e == nil {
			_, e = tx.Exec("INSERT INTO message_recipients(message_id,user_id) SELECT ?,id FROM users WHERE id=?", id, v.User)
		}
		if e == nil {
			_, e = tx.Exec("UPDATE commerce_events SET notified=1 WHERE id=? AND notified=0", v.ID)
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			return e
		}
	}
	if e = a.supportCleanup(now); e != nil {
		return e
	}
	_, e = a.store.db.Exec("DELETE FROM redeem_attempts WHERE bucket<?", now/600-144)
	return e
}

func cloneCommerceUser(u Record) Record {
	if u.Meter != nil {
		m := *u.Meter
		u.Meter = &m
	}
	if u.Entitlement != nil {
		p := *u.Entitlement
		u.Entitlement = &p
	}
	return u
}
func (a *App) validatePurchaseBudgets(u Record) error {
	if u.Quota == 0 {
		return nil
	}
	sites, e := a.store.businessSites()
	if e != nil {
		return e
	}
	remaining := u.Quota
	for _, s := range sites {
		for _, g := range s.Grants {
			if g.UserID != u.ID {
				continue
			}
			budget := g.Budget
			if budget == 0 && g.Quota > 0 {
				budget = g.Quota
			}
			if budget == 0 || budget > remaining {
				return commerceFail(409, "请先调整业务站预留额度，使其不超过套餐总额度")
			}
			remaining -= budget
		}
	}
	return nil
}
