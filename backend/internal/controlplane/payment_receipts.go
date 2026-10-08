package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type PaymentReceipt struct {
	ID           string `json:"id"`
	AccountScope string `json:"-"`
	ProviderID   string `json:"provider_id"`
	ExternalRef  string `json:"external_ref"`
	AttemptID    string `json:"attempt_id"`
	OrderID      string `json:"order_id"`
	UserID       int64  `json:"user_id"`
	Amount       int64  `json:"amount,string"`
	Currency     string `json:"currency"`
	State        string `json:"state"`
	Reason       string `json:"reason"`
	Created      int64  `json:"created"`
	Updated      int64  `json:"updated"`
}

const paymentReceiptColumns = "id,account_scope,provider_id,external_ref,attempt_id,order_id,user_id,amount,currency,state,reason,created,updated"

func scanPaymentReceipt(row paymentScanner) (PaymentReceipt, error) {
	var v PaymentReceipt
	err := row.Scan(&v.ID, &v.AccountScope, &v.ProviderID, &v.ExternalRef, &v.AttemptID, &v.OrderID, &v.UserID, &v.Amount, &v.Currency, &v.State, &v.Reason, &v.Created, &v.Updated)
	return v, err
}
func (s *Store) paymentReceipt(id string) (PaymentReceipt, error) {
	return scanPaymentReceipt(s.db.QueryRow("SELECT "+paymentReceiptColumns+" FROM payment_receipts WHERE id=?", id))
}
func (a *App) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.paymentProvider(strings.TrimSpace(r.PathValue("provider")))
	if err != nil {
		failure(w, 404, "支付方式不存在")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failure(w, 400, "支付回调格式无效")
		return
	}
	values := r.URL.Query()
	eventBody := body
	if p.Code == "epay" {
		if r.Method == "POST" {
			values, err = url.ParseQuery(string(body))
			if err != nil {
				failure(w, 400, "支付回调格式无效")
				return
			}
		} else {
			eventBody = []byte(r.URL.RawQuery)
		}
	}
	hint := paymentMerchantHint(p, body, values)
	if hint != "" {
		if attempt, e := a.store.paymentAttemptByMerchant(p.ID, hint); e == nil {
			saved, _, se := a.store.attemptProvider(attempt)
			if se != nil {
				failure(w, 503, "支付配置暂不可用")
				return
			}
			p = saved
		} else if !errors.Is(e, sql.ErrNoRows) {
			failure(w, 503, "支付记录暂不可用")
			return
		}
	}
	var event verifiedPaymentEvent
	switch p.Code {
	case "epay":
		event, err = verifyEpay(p, values)
	case "stripe":
		event, err = verifyStripe(p, body, r.Header.Get("Stripe-Signature"))
	default:
		event, err = verifyJSONWebhook(p, body, r.Header.Get("X-Guangyue-Signature"))
	}
	if err != nil {
		failure(w, 401, "支付签名或回调内容验证失败")
		return
	}
	if event.Status == "refund" {
		err = a.recordStripeRefund(p, event)
	} else if event.Status == "paid" || event.Status == "success" || event.Status == "trade_success" {
		err = a.recordPaymentEvent(p, event, eventBody)
	}
	if err != nil {
		failure(w, 503, "支付回调暂未完成，请稍后重试")
		return
	}
	if p.Code == "epay" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("success"))
	} else {
		jsonResponse(w, 200, object{"ok": true})
	}
}

// A provider event and a financial receipt are different identities. Callback
// retries and distinct events for one upstream transaction share one receipt,
// including when two checkout methods use the same merchant account.
func (a *App) recordPaymentEvent(p PaymentProvider, event verifiedPaymentEvent, body []byte) error {
	if event.ExternalRef == "" || len(event.ExternalRef) > 200 || event.Amount <= 0 || event.Amount > moneyLimit {
		return errors.New("invalid payment receipt")
	}
	now := time.Now().Unix()
	scope := paymentAccountScope(p)
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	eventInsert, err := tx.Exec("INSERT INTO payment_events(id,provider_id,external_id,payload_hash,signature_valid,state,order_id,attempt_id,body,created) VALUES(?,?,?,?,1,'verified','','',?,?) ON CONFLICT DO NOTHING", serial("GYE"), p.ID, event.EventID, digest(string(body)), body, now)
	if err != nil {
		return err
	}
	if count, e := eventInsert.RowsAffected(); e != nil {
		return e
	} else if count == 0 {
		return tx.Commit()
	}

	receipt := PaymentReceipt{ID: serial("GYR"), AccountScope: scope, ProviderID: p.ID, ExternalRef: event.ExternalRef, Amount: event.Amount, Currency: event.Currency, State: "verified", Created: now, Updated: now}
	if receipt.Currency == "" {
		receipt.Currency = "CNY"
	}
	attempt, readErr := scanPaymentAttempt(tx.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE provider_id=? AND merchant_ref=? ORDER BY created DESC,id DESC LIMIT 1", p.ID, event.MerchantRef))
	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return readErr
	}
	if readErr == nil {
		receipt.AttemptID, receipt.OrderID, receipt.UserID = attempt.ID, attempt.OrderID, attempt.UserID
	} else {
		receipt.State = "review_required"
		receipt.Reason = "已收到付款，但未找到对应支付，请人工核对商户账单"
		if event.ReviewReason != "" {
			receipt.Reason = event.ReviewReason
		}
	}
	inserted, err := tx.Exec("INSERT INTO payment_receipts("+paymentReceiptColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_scope,external_ref) DO NOTHING", receipt.ID, scope, p.ID, event.ExternalRef, receipt.AttemptID, receipt.OrderID, receipt.UserID, receipt.Amount, receipt.Currency, receipt.State, receipt.Reason, now, now)
	if err != nil {
		return err
	}
	n, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		existing, e := scanPaymentReceipt(tx.QueryRow("SELECT "+paymentReceiptColumns+" FROM payment_receipts WHERE account_scope=? AND external_ref=?", scope, event.ExternalRef))
		if e != nil {
			return e
		}
		message := "重复支付回调，未重复入账"
		if existing.AttemptID != receipt.AttemptID || existing.Amount != event.Amount || existing.Currency != receipt.Currency {
			message = "交易号已归属其他收款记录，禁止重复入账"
		}
		_, err = tx.Exec("UPDATE payment_events SET state='duplicate',processed=?,message=? WHERE provider_id=? AND external_id=? AND processed=0", now, message, p.ID, event.EventID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if readErr == nil {
		// Lock before re-reading; App.mu is deliberately not a correctness boundary.
		if _, err = tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=?", attempt.ID); err != nil {
			return err
		}
		attempt, err = scanPaymentAttempt(tx.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE id=?", attempt.ID))
		if err != nil {
			return err
		}
		reason := ""
		if attempt.AccountScope != "" && attempt.AccountScope != scope {
			reason = "商户账号与支付快照不匹配"
		} else if attempt.Amount != event.Amount || attempt.Currency != receipt.Currency {
			reason = "实收金额或币种与订单不符，需原路退款"
		} else if attempt.State != "created" && attempt.State != "awaiting_customer" {
			reason = "付款到达时支付已结束，需核对并原路退款"
		} else if attempt.Expires <= now {
			reason = "付款到达时支付已过期，需原路退款"
		}
		if attempt.ExternalRef != "" {
			reason = "该支付已关联其他到账交易，多付金额需原路退款"
		}
		state := "paid"
		message := "付款已确认，正在处理"
		if reason != "" {
			message = reason
			state = "refund_required"
			receipt.State = state
			receipt.Reason = reason
		}
		if attempt.ExternalRef == "" {
			if reason != "" && attempt.OrderID != "" {
				var body []byte
				e := tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", attempt.OrderID).Scan(&body)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				if e == nil {
					var order Order
					if e = json.Unmarshal(body, &order); e != nil {
						return e
					}
					if order.State == "pending" && order.PaymentAttemptID == attempt.ID {
						order.State = "failed"
						order.Message = reason
						if e = saveOrder(tx, order, "pending"); e != nil {
							return e
						}
					}
				}
			}
			_, err = tx.Exec("UPDATE payment_attempts SET state=?,external_ref=?,updated=?,message=? WHERE id=? AND (external_ref IS NULL OR external_ref='')", state, event.ExternalRef, now, message, attempt.ID)
			if err != nil {
				return err
			}
		}
		if _, err = tx.Exec("UPDATE payment_receipts SET state=?,reason=? WHERE id=?", receipt.State, receipt.Reason, receipt.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE payment_events SET state='recorded',processed=?,order_id=?,attempt_id=?,message=? WHERE provider_id=? AND external_id=?", now, receipt.OrderID, receipt.AttemptID, "已记录唯一收款，后台将自动处理", p.ID, event.EventID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Crash recovery uses the same durable rows; acknowledging a verified callback
	// never depends on a successful entitlement activation in this HTTP request.
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.reconcilePaymentReceipt(receipt.ID)
	return nil
}
func (a *App) paymentRefundRequired(id, reason string) error {
	receipt, err := a.store.paymentReceipt(id)
	if err != nil {
		return err
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// All order transitions lock the attempt first. Recheck under that lock so
	// another process accepting this payment cannot be undone by a stale 409.
	if _, err = tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=?", receipt.AttemptID); err != nil {
		return err
	}
	attempt, err := scanPaymentAttempt(tx.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE id=?", receipt.AttemptID))
	if err != nil {
		return err
	}
	var order Order
	if receipt.OrderID != "" {
		var body []byte
		err = tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", receipt.OrderID).Scan(&body)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err = json.Unmarshal(body, &order); err != nil {
				return err
			}
		}
		if order.PaymentAttemptID == attempt.ID && (order.State == "provisioning" || order.State == "completed") && attempt.State != "refund_required" {
			_, err = tx.Exec("UPDATE payment_receipts SET state='applied',reason='付款已确认，套餐进入开通流程',updated=? WHERE id=? AND state='verified'", time.Now().Unix(), id)
			if err != nil {
				return err
			}
			return tx.Commit()
		}
	}
	result, err := tx.Exec("UPDATE payment_receipts SET state='refund_required',reason=?,updated=? WHERE id=? AND state='verified'", reason, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil
	}
	if _, err = tx.Exec("UPDATE payment_attempts SET state='refund_required',message=?,updated=? WHERE id=? AND external_ref=? AND state='paid'", reason, time.Now().Unix(), receipt.AttemptID, receipt.ExternalRef); err != nil {
		return err
	}
	if order.ID != "" && order.State == "pending" && order.PaymentAttemptID == attempt.ID {
		order.State = "failed"
		order.Message = reason
		if err = saveOrder(tx, order, "pending"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (a *App) reconcilePaymentReceipt(id string) error {
	receipt, err := a.store.paymentReceipt(id)
	if err != nil || receipt.State != "verified" {
		return err
	}
	attempt, err := a.store.paymentAttempt(receipt.AttemptID)
	if err != nil {
		return err
	}
	if attempt.State == "refund_required" || attempt.State == "cancelled" || attempt.State == "expired" {
		return a.paymentRefundRequired(id, "订单未能开通，已收到的款项需原路退款")
	}
	if a.store.commerceErr != nil {
		return a.store.commerceErr
	}
	user, err := a.store.record(attempt.UserID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !user.Enabled {
		return a.paymentRefundRequired(id, "用户已停用或删除，需原路退款")
	}
	if err != nil {
		return err
	}
	if attempt.Purpose == "topup" {
		err = a.creditPaymentTopup(receipt, attempt)
		var ce commerceError
		if errors.As(err, &ce) && strings.Contains(ce.message, "余额超出限制") {
			return a.paymentRefundRequired(id, "充值后余额将超出上限，需原路退款")
		}
		return err
	}
	order, err := a.store.order(attempt.OrderID)
	if errors.Is(err, sql.ErrNoRows) {
		return a.paymentRefundRequired(id, "对应订单不存在，需原路退款")
	}
	if err != nil {
		return err
	}
	if order.PaymentAttemptID != attempt.ID {
		return a.paymentRefundRequired(id, "订单使用了其他支付，当前收款需原路退款")
	}
	switch order.State {
	case "pending":
		if err = a.confirmExternalOrder(order.ID, user, attempt.ID); err != nil {
			current, readErr := a.store.order(order.ID)
			if readErr != nil {
				return readErr
			}
			if current.PaymentAttemptID == attempt.ID && (current.State == "provisioning" || current.State == "completed") {
				break
			}
			var ce commerceError
			if errors.As(err, &ce) && (ce.status == 400 || ce.status == 403 || ce.status == 404 || ce.status == 409) {
				return a.paymentRefundRequired(id, "付款已确认，但订单无法开通："+ce.message)
			}
			return err
		}
	case "provisioning", "completed":
	default:
		return a.paymentRefundRequired(id, "订单已取消、过期或开通失败，需原路退款")
	}
	_, err = a.store.db.Exec("UPDATE payment_receipts SET state='applied',reason='付款已确认，套餐进入开通流程',updated=? WHERE id=? AND state='verified'", time.Now().Unix(), id)
	return err
}
func (a *App) creditPaymentTopup(receipt PaymentReceipt, attempt PaymentAttempt) error {
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	locked, err := tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=? AND state IN ('paid','completed')", attempt.ID)
	if err != nil {
		return err
	}
	n, _ := locked.RowsAffected()
	if n != 1 {
		return errCommerceConflict
	}
	result, err := tx.Exec("UPDATE payment_receipts SET state='applied',reason='余额充值已到账',updated=? WHERE id=? AND state='verified'", time.Now().Unix(), receipt.ID)
	if err != nil {
		return err
	}
	n, _ = result.RowsAffected()
	if n == 0 {
		return nil
	}
	if _, err = a.store.postMoney(tx, attempt.UserID, 0, "payment:"+receipt.ID, "online_topup", receipt.ID, "在线余额充值", receipt.Amount, 0); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE payment_attempts SET state='completed',message='余额充值已到账',updated=? WHERE id=? AND state='paid' AND external_ref=?", time.Now().Unix(), attempt.ID, receipt.ExternalRef); err != nil {
		return err
	}
	if err = a.store.commerceEvent(tx, attempt.UserID, "payment:"+receipt.ID, "在线充值已到账"); err != nil {
		return err
	}
	return tx.Commit()
}
func paymentMarkReceiptRefund(tx *persistence.Tx, attemptID, reason string) error {
	_, err := tx.Exec("UPDATE payment_receipts SET state='refund_required',reason=?,updated=? WHERE attempt_id=? AND state IN ('verified','applied')", reason, time.Now().Unix(), attemptID)
	return err
}
func (a *App) paymentWork() error {
	if a.cfg.businessAgent() {
		return nil
	}
	if err := a.recoverLegacyPaymentReceipts(); err != nil {
		return err
	}

	rows, err := a.store.db.Query("SELECT id FROM payment_receipts WHERE state='verified' ORDER BY updated LIMIT 100")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if e := a.reconcilePaymentReceipt(id); e != nil {
			errs = append(errs, e)
		}
	}
	// Older releases and order cancellation may have marked the attempt first.
	_, err = a.store.db.Exec("UPDATE payment_receipts SET state='refund_required',reason='订单未完成或退款审核已通过，等待原路退款',updated=? WHERE state IN ('verified','applied') AND attempt_id IN (SELECT id FROM payment_attempts WHERE state='refund_required')", time.Now().Unix())
	if err != nil {
		errs = append(errs, err)
	}
	_, err = a.store.db.Exec("UPDATE payment_attempts SET state='expired',message='支付已过期，晚到付款将转人工退款',updated=? WHERE state IN ('created','awaiting_customer') AND expires<=?", time.Now().Unix(), time.Now().Unix())
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
func (a *App) runPaymentWorker(ctx context.Context) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		_ = a.paymentWork()
		a.mu.Unlock()
		_ = a.paymentRefundWork(ctx)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (a *App) listPaymentReceipts(w http.ResponseWriter, r *http.Request, actor Record) error {
	if actor.Role != "owner" {
		return commerceFail(403, "需要管理员权限")
	}
	rows, err := a.store.db.Query("SELECT " + paymentReceiptColumns + " FROM payment_receipts ORDER BY created DESC,id DESC LIMIT 200")
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []PaymentReceipt{}
	for rows.Next() {
		v, e := scanPaymentReceipt(rows)
		if e != nil {
			return e
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	jsonResponse(w, 200, object{"items": items})
	return nil
}

// Upgrades may already contain verified attempts from the earlier payment core.
// Bring those funds into the same restartable receipt pipeline once, without
// granting anything from unsigned pending attempts.
func (a *App) recoverLegacyPaymentReceipts() error {
	rows, err := a.store.db.Query("SELECT " + paymentAttemptColumns + " FROM payment_attempts WHERE account_scope='' AND external_ref IS NOT NULL AND external_ref<>'' AND state IN ('paid','completed','refund_required','refunded') ORDER BY created LIMIT 100")
	if err != nil {
		return err
	}
	attempts := []PaymentAttempt{}
	for rows.Next() {
		attempt, e := scanPaymentAttempt(rows)
		if e != nil {
			rows.Close()
			return e
		}
		attempts = append(attempts, attempt)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, attempt := range attempts {
		p, doc, e := a.store.attemptProvider(attempt)
		if e != nil {
			return e
		}
		scope := paymentAccountScope(p)
		doc.Code = p.Code
		doc.Provider = providerDocument(p)
		doc.Provider.AccountScope = scope
		body, e := a.store.vault.seal(doc)
		if e != nil {
			return e
		}
		tx, e := a.store.db.Begin()
		if e != nil {
			return e
		}
		result, e := tx.Exec("UPDATE payment_attempts SET account_scope=?,doc=? WHERE id=? AND account_scope=''", scope, body, attempt.ID)
		if e != nil {
			tx.Rollback()
			return e
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			tx.Rollback()
			continue
		}
		state := attempt.State
		if state == "paid" {
			state = "verified"
		}
		if state == "completed" {
			state = "applied"
		}
		_, e = tx.Exec("INSERT INTO payment_receipts("+paymentReceiptColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_scope,external_ref) DO NOTHING", "legacy-"+attempt.ID, scope, p.ID, attempt.ExternalRef, attempt.ID, attempt.OrderID, attempt.UserID, attempt.Amount, attempt.Currency, state, "升级后恢复已验证的在线收款", attempt.Updated, time.Now().Unix())
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
