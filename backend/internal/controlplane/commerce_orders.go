package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type Offer struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	Enabled       bool   `json:"enabled"`
	Price         int64  `json:"price,string"`
	PurchaseLimit int    `json:"purchase_limit,omitempty"`
	Plan          Plan   `json:"plan"`
}
type Order struct {
	ID                string         `json:"id"`
	UserID            int64          `json:"user_id"`
	State             string         `json:"state"`
	Created           int64          `json:"created"`
	Updated           int64          `json:"updated"`
	Expires           int64          `json:"expires"`
	Offer             Offer          `json:"offer"`
	Action            string         `json:"action"`
	BeforeExpiry      int64          `json:"before_expiry"`
	BeforeRevision    string         `json:"before_revision"`
	AppliedRevision   string         `json:"applied_revision"`
	Expected          string         `json:"expected"`
	PreviousEnd       int64          `json:"previous_end"`
	Started           int64          `json:"started"`
	NextAttempt       int64          `json:"next_attempt"`
	Attempts          int            `json:"attempts"`
	Message           string         `json:"message"`
	RefundStatus      string         `json:"refund_status,omitempty"`
	RefundRequestID   string         `json:"refund_request_id,omitempty"`
	RefundRequest     *RefundRequest `json:"refund_request,omitempty"`
	RefundAmount      int64          `json:"refund_amount,string,omitempty"`
	RefundAmountSet   bool           `json:"refund_amount_set,omitempty"`
	PaymentAttemptID  string         `json:"payment_attempt_id,omitempty"`
	PaymentProviderID string         `json:"payment_provider_id,omitempty"`
}

func (s *Store) refundRequest(orderID string) (*RefundRequest, error) {
	var v RefundRequest
	err := s.db.QueryRow("SELECT id,order_id,user_id,state,revision,reason,actor_id,created,updated,decided_by,decided_at FROM commerce_refund_requests WHERE order_id=? ORDER BY created DESC,id DESC LIMIT 1", orderID).Scan(&v.ID, &v.OrderID, &v.UserID, &v.State, &v.Revision, &v.Reason, &v.ActorID, &v.Created, &v.Updated, &v.DecidedBy, &v.DecidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (a *App) enrichOrder(o *Order) error {
	r, err := a.store.refundRequest(o.ID)
	if err != nil {
		return err
	}
	o.RefundRequest = r
	if r != nil {
		o.RefundStatus = r.State
		o.RefundRequestID = r.ID
	}
	if o.State == "refund_requested" {
		o.State = "completed"
	}
	return nil
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
	if e == nil && v.State == "refund_requested" {
		// Migration 017 keeps the historical JSON document intact while moving
		// fulfillment back to completed. Normalize snapshots read by workers too.
		v.State = "completed"
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
	v := object{"entitlement": u.Entitlement, "quota": u.Quota, "expires": u.Expires, "enabled": u.Enabled, "vless": u.VLESS, "hy2": u.HY2, "period": period}
	if len(u.PlanQueue) > 0 {
		v["plan_queue"] = u.PlanQueue
	}
	if u.NodeGroupIDs != nil {
		v["node_group_ids"] = u.NodeGroupIDs
	}
	return digest(string(jsonBytes(v)))
}

// orderEntitlementPosition locates a purchase in the user's current
// entitlement chain. Authorization revisions are deliberately excluded from
// this lookup because administrators may change a live package's node policy
// after it was purchased. The purchase identity remains stable across those
// policy revisions.
func orderEntitlementPosition(u Record, orderID string) (active bool, queuedIndex int, found bool) {
	if orderID == "" {
		return false, -1, false
	}
	if u.Entitlement != nil && (u.Entitlement.OrderID == orderID || u.Entitlement.RenewalOrderID == orderID) {
		return true, -1, true
	}
	for i := range u.PlanQueue {
		id := u.PlanQueue[i].OrderID
		if id == "" && u.PlanQueue[i].Entitlement != nil {
			id = u.PlanQueue[i].Entitlement.OrderID
			if id == "" {
				id = u.PlanQueue[i].Entitlement.RenewalOrderID
			}
		}
		if id == orderID || u.PlanQueue[i].RenewalOrderID == orderID {
			return false, i, true
		}
	}
	return false, -1, false
}

func removeQueuedEntitlement(u *Record, index int) bool {
	if index < 0 || index >= len(u.PlanQueue) {
		return false
	}
	copy(u.PlanQueue[index:], u.PlanQueue[index+1:])
	u.PlanQueue[len(u.PlanQueue)-1] = domain.PlanSlot{}
	u.PlanQueue = u.PlanQueue[:len(u.PlanQueue)-1]
	return true
}

// refundableOrderAmount returns the portion of an order's price backed by
// unused active entitlement. Queued purchases have not been consumed yet and
// therefore remain fully refundable. A missing meter falls back to the full
// amount so an old record is never under-refunded silently.
func refundableOrderAmount(o Order, u Record, now int64) int64 {
	amount := o.Offer.Price
	if amount <= 0 {
		return 0
	}
	active, _, found := orderEntitlementPosition(u, o.ID)
	if !found || !active {
		return amount
	}
	if o.Offer.Plan.Quota > 0 && u.QuotaUsed() > 0 {
		used := min(o.Offer.Plan.Quota, max(int64(0), u.QuotaUsed()))
		return amount * (o.Offer.Plan.Quota - used) / o.Offer.Plan.Quota
	}
	if o.Offer.Plan.Quota == 0 && o.Offer.Plan.ValidDays > 0 && u.Entitlement != nil && u.Entitlement.AssignedAt > 0 {
		total := int64(o.Offer.Plan.ValidDays) * 86400
		left := max(int64(0), u.Expires-now)
		if total > 0 && left < total {
			return amount * left / total
		}
	}
	return amount
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
		ID            string `json:"id"`
		Version       int    `json:"version"`
		PlanID        string `json:"plan_id"`
		PlanVersion   int    `json:"plan_version"`
		Price         string `json:"price"`
		Enabled       bool   `json:"enabled"`
		PurchaseLimit int    `json:"purchase_limit"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	var e error
	price := int64(0)
	if strings.TrimSpace(in.Price) != "0" {
		price, e = moneyValue(in.Price)
		if e != nil {
			return e
		}
	}
	if in.PurchaseLimit < 0 || in.PurchaseLimit > 100000 {
		return commerceFail(400, "限购次数应为 0–100000，0 表示不限购")
	}
	p, e := a.store.plan(in.PlanID)
	if e != nil || p.Archived || p.Version != in.PlanVersion {
		return commerceFail(409, "套餐已变化或已归档")
	}
	if e = a.validatePlan(&p); e != nil {
		return e
	}
	p.Notes = ""
	o := Offer{ID: in.ID, Version: in.Version + 1, Enabled: in.Enabled, Price: price, PurchaseLimit: in.PurchaseLimit, Plan: p}
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

// countOfferPurchases counts orders that already reserve or have consumed a
// purchase slot. Cancelled, expired, failed and refunded orders release it.
// The commerce API serializes these checks with order creation and confirmation
// so two browser tabs cannot race past a per-user limit.
func (a *App) countOfferPurchases(user int64, offerID, exclude string) (int, error) {
	rows, err := a.store.db.Query("SELECT id,state,doc FROM commerce_orders WHERE user_id=?", user)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, state string
		var b []byte
		if err = rows.Scan(&id, &state, &b); err != nil {
			return 0, err
		}
		if id == exclude || state != "pending" && state != "provisioning" && state != "completed" && state != "refunding" {
			continue
		}
		var order Order
		if err = json.Unmarshal(b, &order); err != nil {
			return 0, err
		}
		if order.Offer.ID == offerID {
			count++
		}
	}
	return count, rows.Err()
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func (a *App) listOrders(w http.ResponseWriter, r *http.Request, actor Record) error {
	query := "SELECT doc FROM commerce_orders"
	args := []any{}
	clauses := []string{}
	// Deep links and status refreshes must find an order outside the first page.
	// Keep the same user scope below for exact reads as for ordinary listing.
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		clauses = append(clauses, "id=?")
		args = append(args, id)
	}
	// Do not use an artificial first-page cursor. Order IDs are opaque text and
	// the ordering of '~' relative to letters differs between SQLite and
	// PostgreSQL collations; the old sentinel could hide every order on a new
	// PostgreSQL installation.
	if before := strings.TrimSpace(r.URL.Query().Get("before")); before != "" {
		clauses = append(clauses, "id<?")
		args = append(args, before)
	}
	if actor.Role != "owner" || r.URL.Query().Get("all") != "1" {
		u, e := requestedUser(r, actor)
		if e != nil {
			return e
		}
		clauses = append(clauses, "user_id=?")
		args = append(args, u)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY id DESC LIMIT 50"
	rows, e := a.store.db.Query(query, args...)
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
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	// SQLite deliberately uses one connection. Close the order cursor before
	// loading each refund aggregate, otherwise the enrichment query waits for
	// the cursor forever. PostgreSQL also benefits from releasing the page
	// snapshot before the per-order lookups.
	if e = rows.Close(); e != nil {
		return e
	}
	out := make([]Order, 0, len(orders))
	for i := range orders {
		if e = a.enrichOrder(&orders[i]); e != nil {
			return e
		}
		out = append(out, orders[i])
	}
	jsonResponse(w, 200, object{"items": out})
	return nil
}
func (a *App) createOrder(w http.ResponseWriter, r *http.Request, actor Record) error {
	if e := a.requirePaymentModule(); e != nil {
		return e
	}
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
	offer, e := a.store.offer(in.OfferID)
	if e != nil || !offer.Enabled || offer.Version != in.OfferVersion {
		return commerceFail(409, "商品已变化或已下架")
	}
	p, e := a.store.plan(offer.Plan.ID)
	if e != nil || p.Archived {
		return commerceFail(409, "套餐已归档")
	}
	if offer.PurchaseLimit > 0 {
		count, e := a.countOfferPurchases(actor.ID, offer.ID, "")
		if e != nil {
			return e
		}
		if count >= offer.PurchaseLimit {
			return commerceFail(409, "该套餐已达到你的限购次数")
		}
	}
	if actor.Meter != nil && actor.Meter.PendingReset {
		return commerceFail(409, "旧周期正在结算，请稍后重试")
	}
	if pending, e := a.store.commercePending(actor.ID); e != nil {
		return e
	} else if pending {
		return commerceFail(409, "请先处理已有未完成订单")
	}
	now := time.Now().Unix()
	action := "purchase"
	if actor.Entitlement != nil && (actor.Expires == 0 || actor.Expires > now) && actor.Entitlement.PlanID == offer.Plan.ID && actor.Entitlement.Version == offer.Plan.Version {
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
