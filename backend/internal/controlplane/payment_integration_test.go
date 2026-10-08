package controlplane

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func paymentTestMethod(t *testing.T, a *App, owner Record, channel string) PaymentProvider {
	t.Helper()
	w := req(t, a, owner, "POST", "/api/commerce/payment-methods", object{"code": "epay", "name": "测试支付 " + channel, "enabled": true, "base_url": "https://pay.example.com", "merchant_id": "42", "secret": "test-merchant-secret", "channel": channel, "password": commerceTestPassword})
	method := decoded[PaymentMethod](t, w, 200)
	p, e := a.store.paymentProvider(method.ID)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func paymentTestTopup(t *testing.T, a *App, user Record, p PaymentProvider, amount string) PaymentAttempt {
	t.Helper()
	w := req(t, a, user, "POST", "/api/commerce/wallet/topup", object{"amount": amount, "payment_method": p.ID, "operation_id": randomToken(24)})
	return decoded[struct {
		Attempt PaymentAttempt `json:"attempt"`
	}](t, w, 201).Attempt
}
func paymentTestEvent(attempt PaymentAttempt, external string) verifiedPaymentEvent {
	return verifiedPaymentEvent{EventID: external, MerchantRef: attempt.MerchantRef, ExternalRef: external, Amount: attempt.Amount, Currency: "CNY", Status: "paid"}
}
func paymentTestOrder(t *testing.T, a *App, owner, user Record, p PaymentProvider) (Order, PaymentAttempt) {
	t.Helper()
	offer := commerceOffer(t, a, owner)
	order := newOrder(t, a, user, offer)
	w := req(t, a, user, "POST", "/api/commerce/orders/pay", object{"order_id": order.ID, "payment_method": p.ID, "operation_id": randomToken(24)})
	return order, decoded[struct {
		Attempt PaymentAttempt `json:"attempt"`
	}](t, w, 201).Attempt
}
func paymentTestReceipt(t *testing.T, a *App, external string) PaymentReceipt {
	t.Helper()
	r, e := scanPaymentReceipt(a.store.db.QueryRow("SELECT "+paymentReceiptColumns+" FROM payment_receipts WHERE external_ref=?", external))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestPaymentTopupDedupAcrossInstancesAndMethods(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	p2 := paymentTestMethod(t, a, owner, "wxpay")
	first := paymentTestTopup(t, a, user, p, "1200")
	second := paymentTestTopup(t, a, user, p2, "1200")
	if first.AccountScope != second.AccountScope {
		t.Fatal("same merchant account should share receipt scope")
	}
	peer := testApp(t)
	peer.store = a.store
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app, provider, attempt := a, p, first
			if i%2 == 1 {
				app, provider, attempt = peer, p2, second
			}
			ev := paymentTestEvent(attempt, "same-real-transaction")
			ev.EventID = fmt.Sprintf("different-event-%d", i)
			errs <- app.recordPaymentEvent(provider, ev, jsonBytes(ev))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if e := a.paymentWork(); e != nil {
		t.Fatal(e)
	}
	wallet, e := a.store.wallet(user.ID)
	if e != nil || wallet.Available != 1200 || wallet.Held != 0 {
		t.Fatalf("wallet=%+v err=%v", wallet, e)
	}
	var n int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts").Scan(&n); e != nil || n != 1 {
		t.Fatalf("receipts %d: %v", n, e)
	}
	assertLedger(t, a)
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestPaymentLateMismatchAndManualRefund(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	for _, kind := range []string{"expired", "mismatch", "cancelled"} {
		at := paymentTestTopup(t, a, user, p, "1200")
		switch kind {
		case "expired":
			_, _ = a.store.db.Exec("UPDATE payment_attempts SET expires=? WHERE id=?", time.Now().Unix()-1, at.ID)
		case "cancelled":
			_, _ = a.store.db.Exec("UPDATE payment_attempts SET state='cancelled' WHERE id=?", at.ID)
		}
		ev := paymentTestEvent(at, "pay-"+kind)
		if kind == "mismatch" {
			ev.Amount = 1500
		}
		if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
			t.Fatal(e)
		}
		receipt := paymentTestReceipt(t, a, ev.ExternalRef)
		if receipt.State != "refund_required" || receipt.Amount != ev.Amount {
			t.Fatalf("bad receipt: %+v", receipt)
		}
		request := object{"receipt_id": receipt.ID, "action": "confirm_manual", "reason": "已核对商户后台退款完成", "external_ref": "refund-" + kind, "password": commerceTestPassword, "operation_id": randomToken(24)}
		if w := req(t, a, user, "POST", "/api/commerce/payment-refunds", request); w.Code != 403 {
			t.Fatalf("member refunded: %d", w.Code)
		}
		for i := 0; i < 2; i++ {
			if w := req(t, a, owner, "POST", "/api/commerce/payment-refunds", request); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if receipt = paymentTestReceipt(t, a, ev.ExternalRef); receipt.State != "refunded" {
			t.Fatal(receipt)
		}
	}
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 0 || wallet.Held != 0 {
		t.Fatal("external refunds must not credit wallet", wallet)
	}
}
func TestPaymentOrderRecoveryCancellationAndRefundTruth(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	order, at := paymentTestOrder(t, a, owner, user, p)
	// Stop activation after the durable callback commit, then recover without any callback retry.
	a.store.commerceErr = fmt.Errorf("temporarily recovering ledger")
	ev := paymentTestEvent(at, "order-real-payment")
	if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
		t.Fatal(e)
	}
	if r := paymentTestReceipt(t, a, ev.ExternalRef); r.State != "verified" {
		t.Fatal(r)
	}
	a.store.commerceErr = nil
	peer := testApp(t)
	peer.store = a.store
	if e := peer.paymentWork(); e != nil {
		t.Fatal(e)
	}
	current, _ := a.store.order(order.ID)
	if current.State != "provisioning" {
		t.Fatal(current)
	}
	if e := a.store.validateCommerce(true); e != nil {
		t.Fatal("external order has no wallet hold", e)
	}
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	current, _ = a.store.order(order.ID)
	if current.State != "completed" {
		t.Fatal(current)
	}
	orderDo(t, a, user, current, "request_refund")
	orderDo(t, a, owner, current, "refund")
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	current, _ = a.store.order(order.ID)
	if current.State != "refund_pending" {
		t.Fatalf("must wait for actual refund: %+v", current)
	}
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 0 || wallet.Held != 0 {
		t.Fatal(wallet)
	}
	receipt := paymentTestReceipt(t, a, ev.ExternalRef)
	if receipt.State != "refund_required" {
		t.Fatal(receipt)
	}
	w := req(t, a, owner, "POST", "/api/commerce/payment-refunds", object{"receipt_id": receipt.ID, "action": "confirm_manual", "reason": "后台确认成功", "external_ref": "merchant-refund-123", "password": commerceTestPassword, "operation_id": randomToken(24)})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ = a.store.order(order.ID)
	if current.State != "refunded" {
		t.Fatal(current)
	}
	// Cancelling a provider-funded provisioning order never releases nonexistent wallet holds.
	user, _ = a.store.record(user.ID)
	order, at = paymentTestOrder(t, a, owner, user, p)
	ev = paymentTestEvent(at, "cancel-real-payment")
	if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
		t.Fatal(e)
	}
	cancelled := orderDo(t, a, user, order, "cancel")
	if cancelled.State != "cancelled" {
		t.Fatal(cancelled)
	}
	if receipt = paymentTestReceipt(t, a, ev.ExternalRef); receipt.State != "refund_required" {
		t.Fatal(receipt)
	}
	assertLedger(t, a)
}
func TestPaymentProviderLifecycleAndSnapshot(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	at := paymentTestTopup(t, a, user, p, "100")
	save := object{"id": p.ID, "version": p.Version, "code": "epay", "name": "Renamed", "enabled": false, "base_url": "https://pay.example.com", "merchant_id": "43", "secret": "rotated-secret", "channel": "alipay", "password": commerceTestPassword}
	w := req(t, a, owner, "POST", "/api/commerce/payment-methods", save)
	m := decoded[PaymentMethod](t, w, 200)
	if strings.Contains(w.Body.String(), "rotated-secret") || !m.HasSecret {
		t.Fatal("write-only secret leaked or lost")
	}
	if w = req(t, a, owner, "POST", "/api/commerce/payment-methods", save); w.Code != 409 {
		t.Fatal("stale provider write succeeded", w.Code)
	}
	var stored []byte
	if e := a.store.db.QueryRow("SELECT doc FROM payment_providers WHERE id=?", p.ID).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(stored, []byte("rotated-secret")) {
		t.Fatal("secret not encrypted")
	}
	w = req(t, a, owner, "POST", "/api/commerce/payment-methods/delete", object{"id": p.ID, "version": m.Version, "password": commerceTestPassword})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ := a.store.paymentProvider(p.ID)
	if !current.Archived || current.Enabled {
		t.Fatal(current)
	}
	values := url.Values{"pid": {"42"}, "trade_no": {"rotation-payment"}, "out_trade_no": {at.MerchantRef}, "money": {"1.00"}, "trade_status": {"TRADE_SUCCESS"}, "sign_type": {"MD5"}}
	values.Set("sign", epaySign(values, p.Config["secret"]))
	w = req(t, a, Record{}, "GET", "/api/payments/webhook/"+p.ID+"?"+values.Encode(), nil)
	if w.Code != 200 || w.Body.String() != "success" {
		t.Fatal(w.Code, w.Body.String())
	}
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 100 {
		t.Fatal(wallet)
	}
}
func stripeTestSignature(body []byte, secret string, ts int64) string {
	stamp := strconv.FormatInt(ts, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
func TestStripeCheckoutWebhookTopupAndRefund(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	var checkoutRef string
	var amount int64
	refundKey := ""
	a.paymentClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.stripe.com" || r.Header.Get("Authorization") != "Bearer sk_test_fake_secret" {
			t.Errorf("unsafe request %s", r.URL.Host)
		}
		body := ""
		switch r.URL.Path {
		case "/v1/account":
			body = `{"id":"acct_fixture"}`
		case "/v1/checkout/sessions":
			if e := r.ParseForm(); e != nil {
				t.Fatal(e)
			}
			checkoutRef = r.Form.Get("client_reference_id")
			amount, _ = strconv.ParseInt(r.Form.Get("line_items[0][price_data][unit_amount]"), 10, 64)
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing checkout idempotency")
			}
			body = `{"id":"cs_fixture","url":"https://checkout.stripe.com/c/pay/cs_fixture"}`
		case "/v1/refunds":
			if e := r.ParseForm(); e != nil {
				t.Fatal(e)
			}
			refundKey = r.Header.Get("Idempotency-Key")
			body = fmt.Sprintf(`{"id":"re_fixture","status":"pending","payment_intent":"pi_late","currency":"cny","amount":%s}`, r.Form.Get("amount"))
		case "/v1/refunds/re_fixture":
			body = `{"id":"re_fixture","status":"succeeded","payment_intent":"pi_late","currency":"cny","amount":1200}`
		default:
			t.Errorf("unexpected Stripe path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	m := decoded[PaymentMethod](t, req(t, a, owner, "POST", "/api/commerce/payment-methods", object{"code": "stripe", "name": "Stripe fixture", "enabled": true, "secret": "sk_test_fake_secret", "webhook_secret": "whsec_fake", "password": commerceTestPassword}), 200)
	if m.MerchantID != "acct_fixture" || m.Mode != "test" {
		t.Fatal(m)
	}
	p, _ := a.store.paymentProvider(m.ID)
	at := paymentTestTopup(t, a, user, p, "1200")
	if checkoutRef != at.ID || amount != 1200 {
		t.Fatal("incorrect Stripe checkout arguments")
	}
	deliver := func(id, pi string, stamp int64) int {
		event := object{"id": id, "type": "checkout.session.completed", "livemode": false, "data": object{"object": object{"id": "cs_fixture", "client_reference_id": at.ID, "metadata": object{"attempt_id": at.ID}, "payment_intent": pi, "payment_status": "paid", "currency": "cny", "amount_total": 1200}}}
		body := jsonBytes(event)
		r := httptest.NewRequest("POST", "/api/payments/webhook/"+p.ID, bytes.NewReader(body))
		r.Header.Set("Stripe-Signature", stripeTestSignature(body, "whsec_fake", stamp))
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		return w.Code
	}
	if status := deliver("evt_old", "pi_once", time.Now().Unix()-600); status != 401 {
		t.Fatal("stale signature accepted", status)
	}
	for _, id := range []string{"evt_once", "evt_duplicate"} {
		if status := deliver(id, "pi_once", time.Now().Unix()); status != 200 {
			t.Fatal(status)
		}
	}
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 1200 {
		t.Fatal(wallet)
	}
	at = paymentTestTopup(t, a, user, p, "1200")
	_, _ = a.store.db.Exec("UPDATE payment_attempts SET expires=? WHERE id=?", time.Now().Unix()-1, at.ID)
	if status := deliver("evt_late", "pi_late", time.Now().Unix()); status != 200 {
		t.Fatal(status)
	}
	receipt := paymentTestReceipt(t, a, "pi_late")
	w := req(t, a, owner, "POST", "/api/commerce/payment-refunds", object{"receipt_id": receipt.ID, "action": "request", "reason": "过期付款原路退回", "password": commerceTestPassword, "operation_id": randomToken(24)})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := a.paymentRefundWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	receipt = paymentTestReceipt(t, a, "pi_late")
	if receipt.State != "refund_pending" || refundKey == "" {
		t.Fatal("pending refund falsely completed", receipt)
	}
	_, _ = a.store.db.Exec("UPDATE payment_refunds SET next_attempt=0 WHERE receipt_id=?", receipt.ID)
	if e := a.paymentRefundWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	receipt = paymentTestReceipt(t, a, "pi_late")
	if receipt.State != "refunded" {
		t.Fatal(receipt)
	}
	wallet, _ = a.store.wallet(user.ID)
	if wallet.Available != 1200 {
		t.Fatal("refund credited wallet", wallet)
	}
	assertLedger(t, a)
}
func TestEpayRejectsMissingStatusDuplicateFieldsAndMerchant(t *testing.T) {
	p := PaymentProvider{Code: "epay", Config: map[string]string{"secret": "fixture", "merchant_id": "42"}}
	base := url.Values{"pid": {"42"}, "out_trade_no": {"attempt"}, "trade_no": {"receipt"}, "money": {"1.00"}, "trade_status": {"TRADE_SUCCESS"}, "sign_type": {"MD5"}, "empty": {""}}
	for _, change := range []func(url.Values){func(v url.Values) { v.Del("trade_status") }, func(v url.Values) { v.Set("pid", "43") }, func(v url.Values) { v.Add("money", "2.00") }} {
		v := url.Values{}
		for k, values := range base {
			v[k] = append([]string{}, values...)
		}
		change(v)
		v.Set("sign", epaySign(v, "fixture"))
		if _, e := verifyEpay(p, v); e == nil {
			t.Fatal("invalid callback accepted")
		}
	}
}

func TestPaymentOrderRecoveryConcurrentAndAfterExpiry(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	order, at := paymentTestOrder(t, a, owner, user, p)
	a.store.commerceErr = fmt.Errorf("paused after receipt commit")
	ev := paymentTestEvent(at, "before-expiry")
	if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
		t.Fatal(e)
	}
	a.store.commerceErr = nil
	// Received before expiry, but the panel was down until after the deadline.
	current, _ := a.store.order(order.ID)
	current.Expires = time.Now().Unix() - 1
	receipt := paymentTestReceipt(t, a, ev.ExternalRef)
	_, e := a.store.db.Exec("UPDATE payment_receipts SET created=? WHERE id=?", current.Expires-2, receipt.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.persistOrder(current, "pending"); e != nil {
		t.Fatal(e)
	}
	if e = a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	peer := testApp(t)
	peer.store = a.store
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, app := range []*App{a, peer} {
		wg.Add(1)
		go func(app *App) { defer wg.Done(); errs <- app.paymentWork() }(app)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && e != errCommerceConflict {
			t.Fatal(e)
		}
	}
	current, _ = a.store.order(order.ID)
	if current.State != "provisioning" {
		t.Fatal(current)
	}
	receipt = paymentTestReceipt(t, a, ev.ExternalRef)
	if receipt.State != "applied" {
		t.Fatal("concurrent recovery turned a valid payment into a refund", receipt)
	}
	if e = a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	current, _ = a.store.order(order.ID)
	if current.State != "completed" {
		t.Fatal(current)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestPaymentCheckoutReplayAndEventIdentity(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	input := object{"amount": "100", "payment_method": p.ID, "operation_id": randomToken(24)}
	first := decoded[struct {
		Attempt PaymentAttempt `json:"attempt"`
	}](t, req(t, a, user, "POST", "/api/commerce/wallet/topup", input), 201).Attempt
	second := decoded[struct {
		Attempt PaymentAttempt `json:"attempt"`
	}](t, req(t, a, user, "POST", "/api/commerce/wallet/topup", input), 200).Attempt
	if first.ID != second.ID {
		t.Fatal("replayed checkout created another attempt")
	}
	input["amount"] = "200"
	if w := req(t, a, user, "POST", "/api/commerce/wallet/topup", input); w.Code != 409 {
		t.Fatal("operation was reused for a different amount", w.Code)
	}
	ev := paymentTestEvent(first, "receipt-first")
	ev.EventID = "same-event"
	if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
		t.Fatal(e)
	}
	other := paymentTestTopup(t, a, user, p, "100")
	ev.MerchantRef = other.MerchantRef
	ev.ExternalRef = "receipt-other"
	if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
		t.Fatal(e)
	}
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 100 {
		t.Fatal("duplicate event with changed payload credited twice", wallet)
	}
	other, _ = a.store.paymentAttempt(other.ID)
	if other.State != "awaiting_customer" {
		t.Fatal(other)
	}
}

func TestPaymentLegacyPaidRecoveryAndFailedActivation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	order, attempt := paymentTestOrder(t, a, owner, user, p)
	oldDoc, e := a.store.vault.seal(object{"channel": "alipay"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.store.db.Exec("UPDATE payment_attempts SET state='paid',external_ref='legacy-real-payment',account_scope='',doc=? WHERE id=?", oldDoc, attempt.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.paymentWork(); e != nil {
		t.Fatal(e)
	}
	current, _ := a.store.order(order.ID)
	if current.State != "provisioning" {
		t.Fatal("old paid attempts must recover on upgrade", current)
	}
	if e = a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	// The return error from orderAction is authoritative, never Recorder's default 200.
	current, _ = a.store.order(order.ID)
	if e = a.confirmExternalOrder(current.ID, user, attempt.ID); e == nil {
		t.Fatal("failed order activation was reported successful")
	}
	receipt := paymentTestReceipt(t, a, "legacy-real-payment")
	if receipt.State != "applied" {
		t.Fatal(receipt)
	}
}
func TestPaymentUnknownAndDuplicateChargeReview(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	unknown := verifiedPaymentEvent{EventID: "unknown-event", MerchantRef: "not-an-order", ExternalRef: "unknown-paid", Amount: 300, Currency: "CNY", Status: "paid"}
	if e := a.recordPaymentEvent(p, unknown, jsonBytes(unknown)); e != nil {
		t.Fatal(e)
	}
	if receipt := paymentTestReceipt(t, a, unknown.ExternalRef); receipt.State != "review_required" {
		t.Fatal("unknown real funds disappeared", receipt)
	}
	at := paymentTestTopup(t, a, user, p, "100")
	first := paymentTestEvent(at, "charge-first")
	second := paymentTestEvent(at, "charge-extra")
	for _, ev := range []verifiedPaymentEvent{first, second} {
		if e := a.recordPaymentEvent(p, ev, jsonBytes(ev)); e != nil {
			t.Fatal(e)
		}
	}
	receipt := paymentTestReceipt(t, a, second.ExternalRef)
	if receipt.State != "refund_required" {
		t.Fatal("extra charge should require refund", receipt)
	}
	if w := req(t, a, owner, "POST", "/api/commerce/payment-refunds", object{"receipt_id": receipt.ID, "action": "confirm_manual", "reason": "重复付款已原路退回", "external_ref": "extra-refund", "password": commerceTestPassword, "operation_id": randomToken(24)}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ := a.store.paymentAttempt(at.ID)
	if current.State != "completed" {
		t.Fatal("refunding a duplicate charge changed the original successful payment", current)
	}
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 100 {
		t.Fatal(wallet)
	}
}

func TestStripeDisabledDraftProvidesWebhookAddress(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	a.paymentClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"acct_draft"}`)), Header: http.Header{}}, nil
	})}
	method := decoded[PaymentMethod](t, req(t, a, owner, "POST", "/api/commerce/payment-methods", object{"code": "stripe", "name": "Draft", "enabled": false, "secret": "sk_test_fixture", "password": commerceTestPassword}), 200)
	if method.WebhookURL == "" || method.HasWebhookSecret || method.Enabled {
		t.Fatal("draft must expose its callback before an endpoint secret exists", method)
	}
	input := object{"id": method.ID, "version": method.Version, "code": "stripe", "name": "Draft", "enabled": true, "password": commerceTestPassword}
	if w := req(t, a, owner, "POST", "/api/commerce/payment-methods", input); w.Code != 400 {
		t.Fatal("enabled an unsigned Stripe endpoint", w.Code, w.Body.String())
	}
	input["webhook_secret"] = "whsec_fixture"
	if w := req(t, a, owner, "POST", "/api/commerce/payment-methods", input); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPaymentCallbacksBypassCountryGateButRequireSignature(t *testing.T) {
	for _, adapter := range []string{"epay", "stripe"} {
		t.Run(adapter, func(t *testing.T) {
			a := testApp(t)
			owner := testUser(t, a, "owner", "owner")
			user := testUser(t, a, "alice", "user")
			var p PaymentProvider
			if adapter == "epay" {
				p = paymentTestMethod(t, a, owner, "alipay")
			} else {
				a.paymentClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					body := `{"id":"acct_country"}`
					if r.URL.Path == "/v1/checkout/sessions" {
						body = `{"id":"cs_country","url":"https://checkout.stripe.com/c/pay/cs_country"}`
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
				})}
				method := decoded[PaymentMethod](t, req(t, a, owner, "POST", "/api/commerce/payment-methods", object{"code": "stripe", "name": "Country fixture", "enabled": true, "secret": "sk_test_fixture", "webhook_secret": "whsec_fixture", "password": commerceTestPassword}), 200)
				var err error
				p, err = a.store.paymentProvider(method.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			attempt := paymentTestTopup(t, a, user, p, "100")
			setCountryPolicy(t, a, true)
			if w := countryRequest(a, "/api/site", "8.8.8.8:4000", ""); w.Code != http.StatusForbidden {
				t.Fatal("fixture address must be blocked for panel visitors", w.Code)
			}
			deliver := func(valid bool) *httptest.ResponseRecorder {
				var request *http.Request
				if adapter == "epay" {
					values := url.Values{"pid": {"42"}, "out_trade_no": {attempt.MerchantRef}, "trade_no": {"country-epay"}, "money": {"1.00"}, "trade_status": {"TRADE_SUCCESS"}, "sign_type": {"MD5"}}
					signature := "invalid"
					if valid {
						signature = epaySign(values, p.Config["secret"])
					}
					values.Set("sign", signature)
					request = httptest.NewRequest("GET", "/api/payments/webhook/"+p.ID+"?"+values.Encode(), nil)
				} else {
					body := jsonBytes(object{"id": "evt_country", "type": "checkout.session.completed", "livemode": false, "data": object{"object": object{"id": "cs_country", "client_reference_id": attempt.ID, "metadata": object{"attempt_id": attempt.ID}, "payment_intent": "pi_country", "payment_status": "paid", "currency": "cny", "amount_total": 100}}})
					request = httptest.NewRequest("POST", "/api/payments/webhook/"+p.ID, bytes.NewReader(body))
					signature := "invalid"
					if valid {
						signature = stripeTestSignature(body, "whsec_fixture", time.Now().Unix())
					}
					request.Header.Set("Stripe-Signature", signature)
				}
				request.RemoteAddr = "8.8.8.8:4000"
				response := httptest.NewRecorder()
				a.routes().ServeHTTP(response, request)
				return response
			}
			if w := deliver(false); w.Code != http.StatusUnauthorized {
				t.Fatal("country exemption must not exempt signature verification", w.Code, w.Body.String())
			}
			wallet, _ := a.store.wallet(user.ID)
			if wallet.Available != 0 {
				t.Fatal("forged callback credited wallet", wallet)
			}
			for i := 0; i < 2; i++ {
				if w := deliver(true); w.Code != http.StatusOK {
					t.Fatal("verified payment was blocked by the visitor country policy", w.Code, w.Body.String())
				}
			}
			wallet, _ = a.store.wallet(user.ID)
			if wallet.Available != 100 {
				t.Fatal("verified payment must credit exactly once", wallet)
			}
			assertLedger(t, a)
		})
	}
}
