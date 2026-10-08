package controlplane

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"
	"time"
)

func TestPaymentCents(t *testing.T) {
	for input, want := range map[string]int64{"1": 100, "1.2": 120, "0.01": 1, "1000000000.00": 100000000000} {
		got, err := paymentCents(input)
		if err != nil || got != want {
			t.Fatalf("paymentCents(%q)=%d,%v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0", "1.001", "-1", "1000000000.01"} {
		if _, err := paymentCents(input); err == nil {
			t.Fatalf("paymentCents(%q) accepted invalid amount", input)
		}
	}
}

func TestEpaySignatureVerification(t *testing.T) {
	provider := PaymentProvider{Code: "epay", Config: map[string]string{"secret": "test-secret", "merchant_id": "merchant"}}
	values := url.Values{"pid": {"merchant"}, "trade_no": {"T-1"}, "out_trade_no": {"GYO-1"}, "money": {"12.34"}, "trade_status": {"TRADE_SUCCESS"}}
	values.Set("sign", epaySign(values, provider.Config["secret"]))
	values.Set("sign_type", "MD5")
	verified, err := verifyEpay(provider, values)
	if err != nil || verified.MerchantRef != "GYO-1" || verified.Amount != 1234 || verified.Status != "trade_success" {
		t.Fatalf("verifyEpay()=%+v,%v", verified, err)
	}
	values.Set("money", "99.99")
	if _, err = verifyEpay(provider, values); err != nil {
		// The changed value must be rejected by the signature check.
		return
	}
	t.Fatal("verifyEpay accepted a tampered callback")
}

func TestJSONWebhookVerification(t *testing.T) {
	provider := PaymentProvider{Code: "webhook", Config: map[string]string{"secret": "test-secret"}}
	body := []byte(`{"event_id":"evt-1","merchant_ref":"GYO-1","amount":"12.34","currency":"CNY","status":"paid"}`)
	sum := hmac.New(sha256.New, []byte(provider.Config["secret"]))
	_, _ = sum.Write(body)
	signature := "sha256=" + hex.EncodeToString(sum.Sum(nil))
	verified, err := verifyJSONWebhook(provider, body, signature)
	if err != nil || verified.ExternalRef != "evt-1" || verified.Amount != 1234 || verified.Currency != "CNY" {
		t.Fatalf("verifyJSONWebhook()=%+v,%v", verified, err)
	}
	if _, err = verifyJSONWebhook(provider, body, "sha256=bad"); err == nil {
		t.Fatal("verifyJSONWebhook accepted an invalid signature")
	}
}

func TestStripeRefundSignatureAndMode(t *testing.T) {
	p := PaymentProvider{Code: "stripe", Config: map[string]string{"webhook_secret": "whsec_fixture", "mode": "test", "merchant_id": "acct_fixture"}}
	body := []byte(`{"id":"evt_refund","type":"refund.updated","livemode":false,"data":{"object":{"id":"re_fixture","payment_intent":"pi_fixture","amount":1234,"currency":"cny","status":"succeeded","metadata":{"attempt_id":"attempt_fixture"}}}}`)
	sig := stripeTestSignature(body, "whsec_fixture", time.Now().Unix())
	event, err := verifyStripe(p, body, sig)
	if err != nil || event.Status != "refund" || event.RefundID != "re_fixture" || event.RefundStatus != "succeeded" || event.ExternalRef != "pi_fixture" || event.Amount != 1234 || event.MerchantRef != "attempt_fixture" {
		t.Fatalf("refund=%+v err=%v", event, err)
	}
	changed := bytes.Replace(body, []byte(`"amount":1234`), []byte(`"amount":9999`), 1)
	if _, err = verifyStripe(p, changed, sig); err == nil {
		t.Fatal("tampered refund accepted")
	}
	p.Config["mode"] = "live"
	if _, err = verifyStripe(p, body, sig); err == nil {
		t.Fatal("test webhook crossed into live mode")
	}
}
