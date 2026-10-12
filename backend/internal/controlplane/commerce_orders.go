package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"net/http"
	"strconv"
	"strings"
	"time"
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
func (a *App) orderAction(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID               string `json:"id"`
		Action           string `json:"action"`
		OperationID      string `json:"operation_id"`
		Password         string `json:"password"`
		PaymentAttemptID string `json:"payment_attempt_id"`
		Reason           string `json:"reason"`
		RefundRequestID  string `json:"refund_request_id"`
		RefundRevision   int64  `json:"refund_request_revision"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	// Normalize user supplied text before calculating the idempotency
	// fingerprint.  Retrying the same operation with surrounding whitespace
	// must replay the original response instead of being treated as a new
	// command.
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Action == "refund" || in.Action == "reject_refund" {
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
	if e = a.enrichOrder(&o); e != nil {
		return e
	}
	if actor.Role != "owner" && actor.ID != o.UserID {
		// Keep ordinary checkout actions opaque to other members, while refund
		// review actions explicitly report the permission failure so the member
		// UI can explain why the request was rejected.
		if in.Action == "request_refund" || in.Action == "cancel_refund" || in.Action == "reject_refund" {
			return commerceFail(403, "无权操作此订单")
		}
		return commerceFail(404, "订单不存在")
	}
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	old := o.State
	request, requestErr := a.store.refundRequest(o.ID)
	if requestErr != nil {
		return requestErr
	}
	if in.RefundRequestID != "" {
		if request == nil || request.ID != in.RefundRequestID || in.RefundRevision < 1 || request.Revision != in.RefundRevision {
			return commerceFail(409, "退款申请已变化，请刷新后重试")
		}
	}
	if request != nil && request.State == "requested" && (in.Action == "cancel_refund" || in.Action == "reject_refund" || in.Action == "refund") && (in.RefundRequestID == "" || in.RefundRevision < 1) {
		return commerceFail(409, "请提供当前退款申请编号和版本")
	}
	now := time.Now().Unix()
	deltaAvailable, deltaHeld := int64(0), int64(0)
	kind := ""
	queuedRefund := false
	queuedRefundIndex := -1
	queuedRenewalRefund := false
	switch in.Action {
	case "confirm":
		if o.PaymentAttemptID == "" {
			if e := a.requirePaymentModule(); e != nil {
				return e
			}
		}
		if actor.ID != o.UserID {
			return commerceFail(403, "订单需由所属用户确认")
		}
		offer, e := a.store.offer(o.Offer.ID)
		p, pe := a.store.plan(o.Offer.Plan.ID)
		if e != nil || !offer.Enabled || pe != nil || p.Archived {
			return commerceFail(409, "商品已暂停销售")
		}
		if offer.PurchaseLimit > 0 {
			count, ce := a.countOfferPurchases(u.ID, offer.ID, o.ID)
			if ce != nil {
				return ce
			}
			if count >= offer.PurchaseLimit {
				return commerceFail(409, "该套餐已达到你的限购次数")
			}
		}
		paidInTime := false
		if o.PaymentAttemptID != "" && in.PaymentAttemptID == o.PaymentAttemptID {
			var received int64
			if err := a.store.db.QueryRow("SELECT MIN(created) FROM payment_receipts WHERE attempt_id=? AND state IN ('verified','applied')", o.PaymentAttemptID).Scan(&received); err == nil {
				paidInTime = received > 0 && received < o.Expires
			}
		}
		if o.State != "pending" || o.Expires <= now && !paidInTime || !u.Enabled || o.Expected != userCommerceVersion(u) || u.Meter != nil && u.Meter.PendingReset {
			return commerceFail(409, "订单或权益已变化，请取消后重新下单")
		}
		// Online payments are activated only after a verified provider callback.
		// The client cannot turn an unpaid online order into a balance order by
		// calling this endpoint directly.
		onlinePayment := o.PaymentAttemptID != ""
		if onlinePayment {
			if strings.TrimSpace(in.PaymentAttemptID) != o.PaymentAttemptID {
				return commerceFail(409, "请先完成在线支付")
			}
			var paymentState string
			if e = a.store.db.QueryRow("SELECT state FROM payment_attempts WHERE id=? AND order_id=?", o.PaymentAttemptID, o.ID).Scan(&paymentState); e != nil || paymentState != "paid" {
				return commerceFail(409, "请先完成在线支付")
			}
		} else if strings.TrimSpace(in.PaymentAttemptID) != "" {
			return commerceFail(409, "订单支付方式无效")
		}
		future := cloneCommerceUser(u)
		assignPlan(&future, o.Offer.Plan, now, o.ID)
		if o.Action == "renew" {
			future = u
			future.Expires += int64(o.Offer.Plan.ValidDays) * 86400
		}
		if e = a.validateEntitlementSites(future, "reset"); e != nil {
			return e
		}
		u.InitMeter("legacy-"+businessUsageKey(u.ID), now)
		o.PreviousEnd = u.Meter.End
		if o.Action == "purchase" {
			u.Meter.PendingReset = true
		}
		o.Started = now
		o.State = "provisioning"
		if onlinePayment {
			o.Message = "在线支付已确认，正在开通"
		} else {
			o.Message = "余额已冻结，正在开通"
			deltaAvailable, deltaHeld, kind = -o.Offer.Price, o.Offer.Price, "hold"
		}
	case "cancel":
		if old != "pending" && old != "provisioning" {
			return commerceFail(409, "订单无法取消")
		}
		o.State = "cancelled"
		o.Message = "订单已取消"
		if old == "provisioning" {
			if o.PaymentAttemptID == "" {
				deltaAvailable, deltaHeld, kind = o.Offer.Price, -o.Offer.Price, "release"
			}
			if o.Action == "purchase" && u.Meter != nil {
				u.Meter.PendingReset = false
			}
		}
	case "request_refund":
		if actor.ID != o.UserID {
			return commerceFail(403, "退款申请需由所属用户提交")
		}
		if len([]rune(in.Reason)) > 500 {
			return commerceFail(400, "退款申请理由不能超过 500 个字符")
		}
		if old != "completed" {
			return commerceFail(409, "当前订单状态不可申请退款")
		}
		if _, _, found := orderEntitlementPosition(u, o.ID); !found {
			return commerceFail(409, "订单权益已被替换，请联系管理员核对后退款")
		}
		if request != nil && request.State == "requested" {
			return commerceFail(409, "该订单已有待处理退款申请")
		}
		o.State = "completed"
		o.Message = "退款申请已提交，等待管理员审核"
		o.RefundStatus = "requested"
		o.RefundRequestID = serial("GYRR")
		o.RefundRequest = &RefundRequest{ID: o.RefundRequestID, OrderID: o.ID, UserID: o.UserID, State: "requested", Revision: 1, Reason: in.Reason, ActorID: actor.ID, Created: now, Updated: now}
	case "cancel_refund":
		if actor.ID != o.UserID {
			return commerceFail(403, "退款申请需由所属用户取消")
		}
		if len([]rune(in.Reason)) > 500 {
			return commerceFail(400, "退款取消理由不能超过 500 个字符")
		}
		if old != "completed" || request == nil || request.State != "requested" {
			return commerceFail(409, "当前没有待处理的退款申请")
		}
		o.State = "completed"
		o.Message = "退款申请已取消"
		o.RefundStatus = "cancelled"
		updatedRequest := *request
		o.RefundRequest = &updatedRequest
		o.RefundRequest.State = "cancelled"
		o.RefundRequest.Revision++
	case "reject_refund":
		if actor.Role != "owner" {
			return commerceFail(403, "需要管理员权限")
		}
		if old != "completed" || request == nil || request.State != "requested" {
			return commerceFail(409, "当前没有待处理的退款申请")
		}
		if len([]rune(in.Reason)) > 500 {
			return commerceFail(400, "退款拒绝理由不能超过 500 个字符")
		}
		o.State = "completed"
		o.Message = "退款申请已拒绝"
		if in.Reason != "" {
			o.Message += "：" + in.Reason
		}
		o.RefundStatus = "rejected"
		updatedRequest := *request
		o.RefundRequest = &updatedRequest
		o.RefundRequest.State = "rejected"
		o.RefundRequest.Reason = in.Reason
		o.RefundRequest.Revision++
	case "refund":
		if old != "completed" && old != "refund_requested" {
			return commerceFail(409, "当前订单状态不可退款")
		}
		activeEntitlement, queuedIndex, found := orderEntitlementPosition(u, o.ID)
		if !found {
			return commerceFail(409, "订单权益已被替换，请联系管理员核对后退款")
		}
		if pending, e := a.store.commercePendingExcept(u.ID, o.ID); e != nil {
			return e
		} else if pending {
			return commerceFail(409, "请先处理用户的未完成订单")
		}
		o.RefundAmount = refundableOrderAmount(o, u, now)
		o.RefundAmountSet = true
		if !activeEntitlement {
			queuedRefund = true
			queuedRefundIndex = queuedIndex
			queuedRenewalRefund = o.Action == "renew" && queuedIndex >= 0 && queuedIndex < len(u.PlanQueue) && u.PlanQueue[queuedIndex].RenewalOrderID == o.ID
			o.State = "refunded"
			o.Message = "已移除排队权益并退回站内余额"
			if o.PaymentAttemptID != "" {
				o.State = "refund_pending"
				o.Message = "已移除排队权益，等待管理员执行原路退款"
			}
		} else {
			u.InitMeter("legacy-"+businessUsageKey(u.ID), now)
			u.Meter.PendingReset = true
			o.Started = now
			o.NextAttempt = 0
			o.State = "refunding"
			o.Message = "等待订单权益撤回后退回余额"
			if o.PaymentAttemptID != "" {
				o.Message = "等待订单权益撤回后原路退款"
			}
		}
		if request != nil && request.State == "requested" {
			o.RefundStatus = "approved"
			updatedRequest := *request
			o.RefundRequest = &updatedRequest
			o.RefundRequest.State = "approved"
			o.RefundRequest.Revision++
		}
	default:
		return commerceFail(400, "订单操作无效")
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if in.Action == "confirm" && o.PaymentProviderID == "crypto" {
		if e = cryptoLockOrderPayment(tx, o); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", o.ID); e != nil {
		return e
	}
	var lockedDoc []byte
	var lockedOrder Order
	if e = tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", o.ID).Scan(&lockedDoc); e != nil {
		return e
	}
	if e = json.Unmarshal(lockedDoc, &lockedOrder); e != nil {
		return e
	}
	if lockedOrder.State == "refund_requested" {
		lockedOrder.State = "completed"
	}
	if lockedOrder.State != old || lockedOrder.PaymentAttemptID != o.PaymentAttemptID || lockedOrder.PaymentProviderID != o.PaymentProviderID {
		return errCommerceConflict
	}
	if in.Action == "confirm" && o.PaymentAttemptID != "" {
		locked, err := tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=? AND state='paid'", o.PaymentAttemptID)
		if err != nil {
			return err
		}
		n, err := locked.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return commerceFail(409, "在线付款状态已变化，请刷新订单")
		}
	}
	if in.Action == "cancel" && o.PaymentAttemptID != "" {
		if _, e = tx.Exec("UPDATE payment_attempts SET state=CASE WHEN state='paid' THEN 'refund_required' ELSE 'cancelled' END,updated=? WHERE id=? AND state IN ('created','awaiting_customer','paid')", now, o.PaymentAttemptID); e != nil {
			return e
		}
		if e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, "订单已取消，需原路退款"); e != nil {
			return e
		}
		if old == "provisioning" {
			o.Message = "订单已取消；在线支付待人工退款"
		}
	}
	if queuedRefund {
		if queuedRenewalRefund {
			slot := &u.PlanQueue[queuedRefundIndex]
			slot.Expires = o.BeforeExpiry
			slot.RenewalOrderID = ""
			if slot.Entitlement != nil {
				slot.Entitlement.RenewalOrderID = ""
			}
		} else if !removeQueuedEntitlement(&u, queuedRefundIndex) {
			return commerceFail(409, "排队权益已变化，请刷新后重试")
		}
		if o.PaymentAttemptID != "" {
			if _, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=?,message=? WHERE id=? AND state IN ('completed','paid')", now, o.Message, o.PaymentAttemptID); e != nil {
				return e
			}
			if e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, o.Message); e != nil {
				return e
			}
		} else if o.Offer.Price > 0 {
			if _, e = a.store.postMoney(tx, u.ID, actor.ID, "refund:"+o.ID, "refund", o.ID, o.Message, o.RefundAmount, 0); e != nil {
				return e
			}
		}
	}
	if in.Action == "request_refund" {
		_, e = tx.Exec("INSERT INTO commerce_refund_requests(id,order_id,user_id,state,revision,reason,actor_id,created,updated) VALUES(?,?,?,?,?,?,?,?,?)", o.RefundRequestID, o.ID, o.UserID, "requested", 1, in.Reason, actor.ID, now, now)
	} else if (in.Action == "cancel_refund" || in.Action == "reject_refund" || in.Action == "refund") && request != nil && request.State == "requested" {
		newState := "approved"
		if in.Action == "cancel_refund" {
			newState = "cancelled"
		} else if in.Action == "reject_refund" {
			newState = "rejected"
		}
		result, updateErr := tx.Exec("UPDATE commerce_refund_requests SET state=?,reason=?,revision=revision+1,updated=?,decided_by=?,decided_at=? WHERE id=? AND state='requested' AND revision=?", newState, in.Reason, now, actor.ID, now, request.ID, request.Revision)
		e = updateErr
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				e = commerceFail(409, "退款申请已变化，请刷新后重试")
			}
		}
	}
	if e != nil {
		return e
	}
	if kind != "" && o.Offer.Price > 0 {
		if _, e = a.store.postMoney(tx, u.ID, actor.ID, kind+":"+o.ID, kind, o.ID, o.Message, deltaAvailable, deltaHeld); e != nil {
			return e
		}
	}
	if e = saveOrder(tx, o, old); e == nil {
		e = txUser(tx, u.User)
	}
	if e == nil {
		switch in.Action {
		case "request_refund":
			e = a.notifyCommerceOwners(tx, o.ID+" 用户 #"+strconv.FormatInt(o.UserID, 10)+" 已提交退款申请，请在订单中心审核。", now)
		case "cancel_refund":
			// Keep administrators informed when a pending review disappears.
			e = a.notifyCommerceOwnersTitle(tx, "退款申请已取消", o.ID+" 用户 #"+strconv.FormatInt(o.UserID, 10)+" 已取消退款申请。", now)
		case "reject_refund":
			message := "订单 " + o.ID + " 的退款申请已拒绝。"
			if in.Reason != "" {
				message += " 原因：" + in.Reason
			}
			// The member must be told about a decision even when they are not
			// looking at the order page.  Delivery is handled by the existing
			// commerce event worker after this transaction commits.
			e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":refund_rejected:"+in.OperationID, message)
		}
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
	if in.Action == "request_refund" || in.Action == "cancel_refund" || in.Action == "reject_refund" || in.Action == "refund" {
		target := o.ID
		if in.Action == "reject_refund" && in.Reason != "" {
			target += " reason=" + in.Reason
		}
		a.store.audit(actor.Username, "order-"+in.Action, target)
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
			// Crypto eligibility follows finalized block time, not RPC discovery time.
			if o.PaymentProviderID == "crypto" {
				continue
			}
			if o.Expires <= now {
				if o.PaymentAttemptID != "" {
					var state string
					if err := a.store.db.QueryRow("SELECT state FROM payment_attempts WHERE id=?", o.PaymentAttemptID).Scan(&state); err != nil {
						problems = append(problems, err)
						continue
					} else if state == "paid" {
						continue
					}
				}
				o.State = "expired"
				o.Message = "订单确认已超时"
				if o.PaymentAttemptID != "" {
					if _, e = a.store.db.Exec("UPDATE payment_attempts SET state='expired',updated=? WHERE id=? AND state IN ('created','awaiting_customer')", now, o.PaymentAttemptID); e != nil {
						problems = append(problems, e)
						continue
					}
				}
				if e = a.persistOrder(o, "pending"); e != nil {
					problems = append(problems, e)
				}
			}
			continue
		}
		if o.State == "provisioning" && o.PaymentProviderID == "crypto" {
			invoice, err := a.store.scanCryptoInvoice(a.store.db.QueryRow("SELECT "+cryptoInvoiceColumns+" FROM crypto_invoices WHERE attempt_id=?", o.PaymentAttemptID))
			if err == nil {
				err = a.store.cryptoChainAvailable(a.store.db, invoice.ChainID)
			}
			if err != nil {
				problems = append(problems, err)
				continue
			}
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
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", o.ID); e != nil {
		return e
	}
	// A provider-paid order has no wallet hold to release.  Keep the paid
	// attempt visible for an operator to refund through the provider instead of
	// silently crediting the user's internal balance.
	if o.PaymentAttemptID != "" {
		if _, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=? WHERE id=? AND state IN ('paid','completed')", time.Now().Unix(), o.PaymentAttemptID); e != nil {
			return e
		}
		if e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, "订单开通失败，需原路退款"); e != nil {
			return e
		}
		o.Message = message + "；在线支付待人工退款"
	} else if o.Offer.Price > 0 {
		if _, e = a.store.postMoney(tx, u.ID, 0, "release:"+o.ID, "release", o.ID, message, o.Offer.Price, -o.Offer.Price); e != nil {
			return e
		}
	}
	if u.Meter != nil && o.Action == "purchase" {
		u.Meter.PendingReset = false
	}
	if e = txUser(tx, u.User); e != nil {
		return e
	}
	o.State = "failed"
	if o.PaymentAttemptID == "" {
		o.Message = message
	}
	if e = saveOrder(tx, o, "provisioning"); e != nil {
		return e
	}
	if e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":failed", o.ID+" "+message); e != nil {
		return e
	}
	return tx.Commit()
}

// notifyCommerceOwners creates an inbox notification for every enabled owner.
// Refund requests are intentionally visible to administrators without changing
// the user's entitlement or issuing any money movement before approval.
func (a *App) notifyCommerceOwners(tx *persistence.Tx, body string, now int64) error {
	return a.notifyCommerceOwnersTitle(tx, "退款申请待审核", body, now)
}

func (a *App) notifyCommerceOwnersTitle(tx *persistence.Tx, title, body string, now int64) error {
	var messageID int64
	if err := tx.QueryRow("INSERT INTO messages(sender_id,sender_name,title,body,category,created) VALUES(NULL,'System',?,?, 'account',?) RETURNING id", title, body, now).Scan(&messageID); err != nil {
		return err
	}
	rows, err := tx.Query("SELECT id,doc FROM users")
	if err != nil {
		return err
	}
	owners := []int64{}
	for rows.Next() {
		var id int64
		var b []byte
		var user domain.User
		if err = rows.Scan(&id, &b); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(b, &user); err != nil {
			rows.Close()
			return err
		}
		if user.Role == "owner" && user.Enabled {
			owners = append(owners, id)
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, ownerID := range owners {
		if _, err = tx.Exec("INSERT INTO message_recipients(message_id,user_id) VALUES(?,?)", messageID, ownerID); err != nil {
			return err
		}
	}
	return nil
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
		if policyErr != nil {
			return a.failProvisioning(o, "业务站权益配置已变化，冻结余额已退回")
		}
	}
	var budgets map[string]int64
	if o.State == "provisioning" && o.Action == "purchase" {
		future := cloneCommerceUser(u)
		assignPlan(&future, o.Offer.Plan, now)
		budgets, e = a.purchaseSiteBudgets(future, sites)
		if e != nil {
			return e
		}
	}
	oldState := o.State
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if oldState == "provisioning" && o.PaymentProviderID == "crypto" {
		if e = cryptoLockOrderPayment(tx, o); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", o.ID); e != nil {
		return e
	}
	var storedState, storedAttempt string
	var lockedRaw []byte
	if e = tx.QueryRow("SELECT state,doc FROM commerce_orders WHERE id=?", o.ID).Scan(&storedState, &lockedRaw); e != nil {
		return e
	}
	var currentOrder Order
	if e = json.Unmarshal(lockedRaw, &currentOrder); e != nil {
		return e
	}
	storedAttempt = currentOrder.PaymentAttemptID
	if storedState != oldState || storedAttempt != o.PaymentAttemptID {
		return errCommerceConflict
	}
	if !u.Enabled && oldState == "provisioning" {
		o.State = "failed"
		o.Message = "账号已停用，冻结余额已退回"
		if u.Meter != nil {
			u.Meter.PendingReset = false
		}
		if o.PaymentAttemptID != "" {
			_, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=? WHERE id=? AND state IN ('paid','completed')", time.Now().Unix(), o.PaymentAttemptID)
			if e == nil {
				e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, "账号已停用，需原路退款")
			}
			o.Message += "；在线支付待人工退款"
		} else if o.Offer.Price > 0 {
			_, e = a.store.postMoney(tx, u.ID, 0, "release:"+o.ID, "release", o.ID, o.Message, o.Offer.Price, -o.Offer.Price)
		}
	} else if oldState == "refunding" {
		if u.Entitlement == nil || (u.Entitlement.OrderID != o.ID && u.Entitlement.RenewalOrderID != o.ID) {
			return commerceFail(409, "退款权益来源不匹配")
		}
		u.Meter.PendingReset = false
		if o.Action == "renew" {
			u.Expires = o.BeforeExpiry
			u.Entitlement.RenewalOrderID = ""
			u.Entitlement.Revision = serial("GYV")
		} else {
			u.Expires = now
		}
		o.State = "refunded"
		o.Message = "已退回站内余额"
		if o.PaymentAttemptID != "" {
			o.State = "refund_pending"
			o.Message = "权益已撤回，等待管理员执行原路退款"
			if _, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=?,message=? WHERE id=? AND state IN ('completed','paid')", time.Now().Unix(), o.Message, o.PaymentAttemptID); e != nil {
				return e
			}
			e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, o.Message)
		} else if o.RefundAmount > 0 {
			_, e = a.store.postMoney(tx, u.ID, 0, "refund:"+o.ID, "refund", o.ID, o.Message, o.RefundAmount, 0)
		}
	} else {
		if o.Action == "renew" {
			u.Expires = max(now, u.Expires) + int64(o.Offer.Plan.ValidDays)*86400
			u.Entitlement.RenewalOrderID = o.ID
			u.Entitlement.Revision = serial("GYV")
		} else {
			oldDoc := jsonBytes(u.User)
			oldPeriod := u.Meter.PeriodID
			pauseCurrentPlan(&u, now)
			assignPlan(&u, o.Offer.Plan, now, o.ID)
			u.Entitlement.OrderID = o.ID
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
			// Old issued grants have been acknowledged above. Mounts derive fresh
			// grants from the new package on their next sync; legacy sites receive
			// a bounded share. Never copy an old unlimited or oversized budget.
			for i := range sites {
				s := &sites[i]
				grants := []BusinessGrant{}
				changed := false
				for _, g := range s.Grants {
					if g.UserID != u.ID {
						grants = append(grants, g)
						continue
					}
					changed = true
					if budget, keep := budgets[s.ID]; keep && s.Mount == nil {
						g.Budget, g.Quota, g.PeriodID = budget, 0, m.PeriodID
						if budget > 0 {
							g.Quota, e = domain.AddCounter(s.Usage[u.ID].total(), budget)
							if e != nil {
								return e
							}
						}
						grants = append(grants, g)
					}
				}
				if changed {
					s.Grants, s.Revision = grants, serial("GYV")
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
			if o.PaymentAttemptID != "" {
				_, e = tx.Exec("UPDATE payment_attempts SET state='completed',message='套餐已开通',updated=? WHERE id=? AND state='paid'", time.Now().Unix(), o.PaymentAttemptID)
			} else if o.Offer.Price > 0 {
				_, e = a.store.postMoney(tx, u.ID, 0, "capture:"+o.ID, "purchase", o.ID, o.Message, 0, -o.Offer.Price)
			}
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
	u.Entitlement = cloneEntitlement(u.Entitlement)
	if len(u.PlanQueue) > 0 {
		u.PlanQueue = append([]domain.PlanSlot{}, u.PlanQueue...)
		for i := range u.PlanQueue {
			u.PlanQueue[i].Entitlement = cloneEntitlement(u.PlanQueue[i].Entitlement)
			u.PlanQueue[i].Meter = cloneMeter(u.PlanQueue[i].Meter)
		}
	}
	return u
}

// Compute fresh shares only after old remote authorizations have settled.
// Selling a plan is independent of reservations from the user's previous plan.
func (a *App) purchaseSiteBudgets(u Record, sites []BusinessSite) (map[string]int64, error) {
	eligible := map[string]bool{}
	shares := int64(0)
	allows := func(nodes []Node) bool {
		for _, n := range nodes {
			if n.Enabled && nodeGroupAllowed(u, n) && (n.Protocol == "vless" && u.VLESS || n.Protocol == "hy2" && u.HY2) {
				return true
			}
		}
		return false
	}
	local, err := a.store.nodes()
	if err != nil {
		return nil, err
	}
	if allows(local) {
		shares++
	}
	for _, s := range sites {
		if !s.Enabled || s.Removed || !a.businessOwnerEnabled(s) {
			continue
		}
		assigned := s.Mount != nil
		for _, g := range s.Grants {
			assigned = assigned || g.UserID == u.ID
		}
		if !assigned {
			continue
		}
		nodes, err := a.materializeBusinessNodes(s)
		if err != nil {
			return nil, err
		}
		if allows(nodes) {
			eligible[s.ID] = true
			shares++
		}
	}
	out := map[string]int64{}
	remaining := u.Quota
	for _, s := range sites {
		if !eligible[s.ID] || s.Mount != nil {
			continue
		}
		if u.Quota == 0 {
			out[s.ID] = 0
			continue
		}
		if remaining == 0 {
			continue
		}
		budget := min(remaining, max(int64(1), u.Quota/max(int64(1), shares)))
		out[s.ID] = budget
		remaining -= budget
	}
	return out, nil
}

func cryptoLockOrderPayment(tx *persistence.Tx, o Order) error {
	var chainID int64
	if e := tx.QueryRow("SELECT chain_id FROM crypto_invoices WHERE attempt_id=? AND order_id=?", o.PaymentAttemptID, o.ID).Scan(&chainID); e != nil {
		return e
	}
	return cryptoLockChain(tx, chainID)
}
