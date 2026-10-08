package controlplane

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestCryptoReceiptCannotUseFiatRefundConfirmation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	at := paymentTestTopup(t, a, user, p, "1200")
	if _, err := a.store.db.Exec("UPDATE payment_attempts SET state='cancelled' WHERE id=?", at.ID); err != nil {
		t.Fatal(err)
	}
	ev := paymentTestEvent(at, "crypto-refund-boundary-fixture")
	if err := a.recordPaymentEvent(p, ev, jsonBytes(ev)); err != nil {
		t.Fatal(err)
	}
	receipt := paymentTestReceipt(t, a, ev.ExternalRef)
	// The boundary applies to immutable payment snapshots, not the provider's
	// current display name or a client-supplied asset/currency field.
	doc, err := a.store.vault.seal(paymentAttemptDoc{Code: "evm_crypto", Provider: providerDocument(p)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE payment_attempts SET doc=? WHERE id=?", doc, at.ID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"request", "confirm_manual"} {
		in := object{"receipt_id": receipt.ID, "action": action, "external_ref": "0x-unverified-hash", "reason": "已手动转出", "password": commerceTestPassword, "operation_id": randomToken(24)}
		w := req(t, a, owner, "POST", "/api/commerce/payment-refunds", in)
		if w.Code != http.StatusConflict {
			t.Fatalf("crypto accepted fiat refund %s: %d %s", action, w.Code, w.Body.String())
		}
	}
	current := paymentTestReceipt(t, a, ev.ExternalRef)
	if current.State != "refund_required" {
		t.Fatalf("unverified chain transfer closed receipt: %+v", current)
	}
	var count int
	if err = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_refunds WHERE receipt_id=?", receipt.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected refund %d: %v", count, err)
	}
	wallet, err := a.store.wallet(user.ID)
	if err != nil || wallet.Available != 0 || wallet.Held != 0 {
		t.Fatalf("crypto refund credited CNY wallet: %+v %v", wallet, err)
	}
}

func TestRecoveredNonStripeRefundNeverCallsStripe(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	at := paymentTestTopup(t, a, user, p, "1200")
	if _, err := a.store.db.Exec("UPDATE payment_attempts SET state='cancelled' WHERE id=?", at.ID); err != nil {
		t.Fatal(err)
	}
	ev := paymentTestEvent(at, "recovered-refund-fixture")
	if err := a.recordPaymentEvent(p, ev, jsonBytes(ev)); err != nil {
		t.Fatal(err)
	}
	receipt := paymentTestReceipt(t, a, ev.ExternalRef)
	now := time.Now().Unix()
	if _, err := a.store.db.Exec("INSERT INTO payment_refunds(id,receipt_id,account_scope,provider_id,state,amount,currency,actor_id,reason,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "recovered-nonstripe", receipt.ID, receipt.AccountScope, p.ID, "queued", receipt.Amount, receipt.Currency, owner.ID, "legacy fixture", now, now); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.paymentClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected external refund request")
	})}
	if err := a.paymentRefundWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := a.store.db.QueryRow("SELECT state FROM payment_refunds WHERE id='recovered-nonstripe'").Scan(&state); err != nil || state != "failed" || calls != 0 {
		t.Fatalf("non-Stripe dispatched refund: state=%s calls=%d err=%v", state, calls, err)
	}
	if current := paymentTestReceipt(t, a, ev.ExternalRef); current.State == "refunded" {
		t.Fatal("unperformed refund marked complete")
	}
}

func TestOldPaymentCannotClaimReplacedOrderIntent(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	order, first := paymentTestOrder(t, a, owner, user, p)
	secondID := serial("GYAP")
	if _, err := a.store.db.Exec("INSERT INTO payment_attempts(id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,checkout_url,idempotency_key,created,updated,expires,doc,purpose,account_scope) SELECT ?,order_id,user_id,provider_id,'awaiting_customer',amount,currency,?,'',?,created,updated,expires,doc,purpose,account_scope FROM payment_attempts WHERE id=?", secondID, secondID, randomToken(24), first.ID); err != nil {
		t.Fatal(err)
	}
	current, err := a.store.order(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.PaymentAttemptID = secondID
	tx, err := a.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveOrder(tx, current, "pending"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ev := paymentTestEvent(first, "late-old-intent-payment")
	if err = a.recordPaymentEvent(p, ev, jsonBytes(ev)); err != nil {
		t.Fatal(err)
	}
	if receipt := paymentTestReceipt(t, a, ev.ExternalRef); receipt.State != "refund_required" {
		t.Fatalf("old intent claimed replaced order: %+v", receipt)
	}
	current, err = a.store.order(order.ID)
	if err != nil || current.State != "pending" || current.PaymentAttemptID != secondID {
		t.Fatalf("old callback changed active order: %+v %v", current, err)
	}
	if err = a.paymentWork(); err != nil {
		t.Fatal(err)
	}
	current, err = a.store.order(order.ID)
	if err != nil || current.State != "pending" {
		t.Fatalf("old callback activated order after recovery: %+v %v", current, err)
	}
}
