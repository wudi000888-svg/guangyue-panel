package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type PaymentRefund struct {
	ID          string `json:"id"`
	ReceiptID   string `json:"receipt_id"`
	ProviderID  string `json:"provider_id"`
	State       string `json:"state"`
	Amount      int64  `json:"amount,string"`
	Currency    string `json:"currency"`
	ExternalRef string `json:"external_ref"`
	Message     string `json:"message"`
}

func (a *App) paymentRefundAction(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ReceiptID   string `json:"receipt_id"`
		Action      string `json:"action"`
		ExternalRef string `json:"external_ref"`
		Reason      string `json:"reason"`
		Password    string `json:"password"`
		OperationID string `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, err := a.commerceActor(actor, true, in.Password); err != nil {
		return err
	}
	in.Password = ""
	in.Reason = strings.TrimSpace(in.Reason)
	in.ExternalRef = strings.TrimSpace(in.ExternalRef)
	if !validText(in.Reason, 300) {
		return commerceFail(400, "请填写退款原因或退款凭据说明")
	}
	if b, err := a.store.commerceReplay(actor.ID, in.OperationID, in); err != nil {
		return err
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	receipt, err := a.store.paymentReceipt(in.ReceiptID)
	if err != nil {
		return commerceFail(404, "收款记录不存在")
	}
	if receipt.State != "refund_required" && !(receipt.State == "review_required" && in.Action == "confirm_manual") {
		return commerceFail(409, "此收款尚不可退款或退款已在处理；已开通套餐应先在订单中审核退款并撤回权益")
	}
	p, err := a.store.paymentProvider(receipt.ProviderID)
	if err != nil {
		return err
	}
	if receipt.AttemptID != "" {
		attempt, e := a.store.paymentAttempt(receipt.AttemptID)
		if e != nil {
			return e
		}
		p, _, err = a.store.attemptProvider(attempt)
		if err != nil {
			return err
		}
	}
	if in.Action != "request" && in.Action != "confirm_manual" {
		return commerceFail(400, "退款操作无效")
	}
	// Token quantities and chain transfer evidence do not have the semantics of
	// the CNY platform refund ledger. A text/hash supplied by an administrator
	// cannot make a crypto transfer final, or complete a fiat refund record.
	if p.Code != "stripe" && p.Code != "epay" && p.Code != "webhook" {
		return commerceFail(409, "该渠道不能使用法币退款确认；加密货币退款需核对链上实际转出，不能仅填写交易哈希确认")
	}
	if in.Action == "request" && p.Code != "stripe" {
		return commerceFail(409, "易支付需先在商户后台完成原路退款，再填写真实退款凭据确认；系统不会转入站内余额")
	}
	if in.Action == "confirm_manual" && (p.Code == "stripe" && receipt.State != "review_required" || !validText(in.ExternalRef, 200)) {
		return commerceFail(400, "请填写易支付后台已完成退款的交易凭据；Stripe 使用原路退款接口")
	}
	now := time.Now().Unix()
	refund := PaymentRefund{ID: serial("GYRF"), ReceiptID: receipt.ID, ProviderID: p.ID, State: "queued", Amount: receipt.Amount, Currency: receipt.Currency, ExternalRef: in.ExternalRef, Message: "已进入原路退款队列"}
	if in.Action == "confirm_manual" {
		refund.State = "succeeded"
		refund.Message = "管理员已核对并确认商户后台退款"
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if receipt.OrderID != "" {
		if _, err = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", receipt.OrderID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=?", receipt.AttemptID); err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE payment_receipts SET state='refund_pending',updated=? WHERE id=? AND state=?", now, receipt.ID, receipt.State)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return commerceFail(409, "退款状态已变化，请刷新")
	}
	_, err = tx.Exec("INSERT INTO payment_refunds(id,receipt_id,account_scope,provider_id,state,amount,currency,external_ref,actor_id,reason,message,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", refund.ID, refund.ReceiptID, receipt.AccountScope, refund.ProviderID, refund.State, refund.Amount, refund.Currency, refund.ExternalRef, actor.ID, in.Reason, refund.Message, now, now)
	if err != nil {
		return err
	}
	if in.Action == "confirm_manual" {
		if err = a.finishPaymentRefund(tx, receipt, refund.ExternalRef); err != nil {
			return err
		}
	}
	if err = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, refund); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.store.audit(actor.Username, "payment-refund-"+in.Action, receipt.ID+" "+in.ExternalRef)
	jsonResponse(w, 200, refund)
	return nil
}
func (a *App) paymentRefundWork(ctx context.Context) error {
	rows, err := a.store.db.Query("SELECT id,receipt_id,provider_id,state,amount,currency,external_ref,message FROM payment_refunds WHERE state IN ('queued','processing','pending') AND next_attempt<=? ORDER BY created LIMIT 20", time.Now().Unix())
	if err != nil {
		return err
	}
	refunds := []PaymentRefund{}
	for rows.Next() {
		var v PaymentRefund
		if err = rows.Scan(&v.ID, &v.ReceiptID, &v.ProviderID, &v.State, &v.Amount, &v.Currency, &v.ExternalRef, &v.Message); err != nil {
			rows.Close()
			return err
		}
		refunds = append(refunds, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var problems []error
	for _, refund := range refunds {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := time.Now().Unix()
		claim, e := a.store.db.Exec("UPDATE payment_refunds SET state='processing',next_attempt=?,attempts=attempts+1,updated=? WHERE id=? AND state IN ('queued','processing','pending') AND next_attempt<=?", now+60, now, refund.ID, now)
		if e != nil {
			problems = append(problems, e)
			continue
		}
		n, _ := claim.RowsAffected()
		if n == 0 {
			continue
		}
		receipt, e := a.store.paymentReceipt(refund.ReceiptID)
		if e != nil {
			problems = append(problems, e)
			continue
		}
		attempt, e := a.store.paymentAttempt(receipt.AttemptID)
		if e != nil {
			problems = append(problems, e)
			continue
		}
		p, _, e := a.store.attemptProvider(attempt)
		if e != nil {
			problems = append(problems, e)
			continue
		}
		if p.Code != "stripe" {
			// Never dispatch a recovered non-Stripe responsibility to Stripe.
			// Keep the receipt unresolved rather than pretending funds were sent.
			_, e = a.store.db.Exec("UPDATE payment_refunds SET state='failed',message='此渠道不支持 Stripe 自动退款，请核对对应渠道的真实资金转出',updated=? WHERE id=? AND state='processing'", time.Now().Unix(), refund.ID)
			if e != nil {
				problems = append(problems, e)
			}
			continue
		}
		var remote stripeRefund
		if refund.ExternalRef != "" {
			e = a.stripeAPI(ctx, p, "GET", "/v1/refunds/"+url.PathEscape(refund.ExternalRef), "", nil, &remote)
		} else {
			values := url.Values{"payment_intent": {receipt.ExternalRef}, "amount": {strconv.FormatInt(receipt.Amount, 10)}, "metadata[attempt_id]": {attempt.ID}, "metadata[refund_id]": {refund.ID}}
			e = a.stripeAPI(ctx, p, "POST", "/v1/refunds", "gy-refund-"+refund.ID, values, &remote)
		}
		if e != nil {
			_, _ = a.store.db.Exec("UPDATE payment_refunds SET state='pending',message=?,updated=? WHERE id=? AND state='processing'", e.Error(), time.Now().Unix(), refund.ID)
			problems = append(problems, e)
			continue
		}
		event := verifiedPaymentEvent{ExternalRef: remote.PaymentIntent, Amount: remote.Amount, Currency: strings.ToUpper(remote.Currency), RefundID: remote.ID, RefundStatus: remote.Status}
		if e = a.recordStripeRefund(p, event); e != nil {
			problems = append(problems, e)
		}
	}
	return errors.Join(problems...)
}
func (a *App) recordStripeRefund(p PaymentProvider, event verifiedPaymentEvent) error {
	if event.RefundID == "" || event.ExternalRef == "" {
		return errors.New("invalid refund identity")
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	receipt, err := scanPaymentReceipt(tx.QueryRow("SELECT "+paymentReceiptColumns+" FROM payment_receipts WHERE account_scope=? AND external_ref=?", paymentAccountScope(p), event.ExternalRef))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if receipt.OrderID != "" {
		if _, err = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", receipt.OrderID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=?", receipt.AttemptID); err != nil {
		return err
	}
	var refundID, ref, state string
	err = tx.QueryRow("SELECT id,external_ref,state FROM payment_refunds WHERE receipt_id=?", receipt.ID).Scan(&refundID, &ref, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "succeeded" {
		return nil
	}
	if ref != "" && ref != event.RefundID || event.Amount != receipt.Amount || event.Currency != receipt.Currency {
		return errors.New("refund receipt mismatch")
	}
	next := "pending"
	message := "支付平台正在处理原路退款"
	if event.RefundStatus == "succeeded" {
		next = "succeeded"
		message = "支付平台已确认原路退款"
	} else if event.RefundStatus == "failed" || event.RefundStatus == "canceled" {
		next = "failed"
		message = "支付平台退款失败，请在商户后台核对；资金尚未退回"
	}
	result, err := tx.Exec("UPDATE payment_refunds SET state=?,external_ref=?,message=?,updated=?,next_attempt=? WHERE id=? AND state IN ('queued','processing','pending') AND (external_ref='' OR external_ref=?)", next, event.RefundID, message, time.Now().Unix(), time.Now().Unix()+60, refundID, event.RefundID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil
	}
	if next == "succeeded" {
		if err = a.finishPaymentRefund(tx, receipt, event.RefundID); err != nil {
			return err
		}
	} else if next == "failed" {
		if _, err = tx.Exec("UPDATE payment_receipts SET reason=?,updated=? WHERE id=?", message, time.Now().Unix(), receipt.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (a *App) finishPaymentRefund(tx *persistence.Tx, receipt PaymentReceipt, externalRef string) error {
	now := time.Now().Unix()
	if _, err := tx.Exec("UPDATE payment_receipts SET state='refunded',reason='已确认原路退款',updated=? WHERE id=? AND state='refund_pending'", now, receipt.ID); err != nil {
		return err
	}
	updated, err := tx.Exec("UPDATE payment_attempts SET state='refunded',message='已确认原路退款',updated=? WHERE id=? AND external_ref=? AND state='refund_required'", now, receipt.AttemptID, receipt.ExternalRef)
	if err != nil {
		return err
	}
	primary, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if receipt.OrderID != "" && primary == 1 {
		var body []byte
		err := tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", receipt.OrderID).Scan(&body)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var order Order
			if err = json.Unmarshal(body, &order); err != nil {
				return err
			}
			if order.PaymentAttemptID == receipt.AttemptID && (order.State == "refund_pending" || order.State == "failed" || order.State == "cancelled" || order.State == "expired") {
				old := order.State
				order.State = "refunded"
				order.Message = "已原路退款（支付平台交易凭据已记录）"
				if err = saveOrder(tx, order, old); err != nil {
					return err
				}
			}
		}
	}
	if receipt.UserID > 0 {
		return a.store.commerceEvent(tx, receipt.UserID, "refund:"+receipt.ID, "在线支付已原路退款；交易凭据 "+externalRef)
	}
	return nil
}
