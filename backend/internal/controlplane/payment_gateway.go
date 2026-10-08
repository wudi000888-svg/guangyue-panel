package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var paymentHTTPClient = &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: publicSourceDial, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (a *App) paymentHTTP() *http.Client {
	if a.paymentClient != nil {
		return a.paymentClient
	}
	return paymentHTTPClient
}
func paymentHTTPS(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 1024 {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
		return false
	}
	if net.ParseIP(h) != nil && !publicIP(h) {
		return false
	}
	return true
}
func (a *App) stripeAPI(ctx context.Context, p PaymentProvider, method, path, key string, values url.Values, out any) error {
	// The gateway host is never configurable: a merchant key must only reach Stripe.
	if !strings.HasPrefix(path, "/v1/") {
		return errors.New("invalid Stripe API path")
	}
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.stripe.com"+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+p.Config["secret"])
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response, e := a.paymentHTTP().Do(req)
	if e != nil {
		return errors.New("Stripe 连接失败，请检查服务器出站网络")
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil {
		return errors.New("Stripe 响应读取失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Stripe 请求未完成（HTTP %d），请检查密钥、权限和商户配置", response.StatusCode)
	}
	if e = json.Unmarshal(b, out); e != nil {
		return errors.New("Stripe 响应格式无效")
	}
	return nil
}
func (a *App) stripeAccount(ctx context.Context, p PaymentProvider) (string, error) {
	var account struct {
		ID string `json:"id"`
	}
	if e := a.stripeAPI(ctx, p, "GET", "/v1/account", "", nil, &account); e != nil {
		return "", e
	}
	if !strings.HasPrefix(account.ID, "acct_") || len(account.ID) > 100 {
		return "", errors.New("Stripe 商户身份无效")
	}
	return account.ID, nil
}

type stripeCheckoutSession struct {
	ID                string            `json:"id"`
	URL               string            `json:"url"`
	ClientReferenceID string            `json:"client_reference_id"`
	PaymentIntent     string            `json:"payment_intent"`
	PaymentStatus     string            `json:"payment_status"`
	Currency          string            `json:"currency"`
	AmountTotal       int64             `json:"amount_total"`
	Metadata          map[string]string `json:"metadata"`
}

func (a *App) stripeCheckout(ctx context.Context, p PaymentProvider, attempt PaymentAttempt, returnURL string) (string, string, error) {
	label := "光月 · 套餐购买"
	if attempt.Purpose == "topup" {
		label = "光月 · 余额充值"
	}
	values := url.Values{"mode": {"payment"}, "client_reference_id": {attempt.MerchantRef}, "metadata[attempt_id]": {attempt.ID}, "payment_intent_data[metadata][attempt_id]": {attempt.ID}, "line_items[0][price_data][currency]": {"cny"}, "line_items[0][price_data][unit_amount]": {strconv.FormatInt(attempt.Amount, 10)}, "line_items[0][price_data][product_data][name]": {label}, "line_items[0][quantity]": {"1"}, "success_url": {returnURL}, "cancel_url": {returnURL}}
	var session stripeCheckoutSession
	if e := a.stripeAPI(ctx, p, "POST", "/v1/checkout/sessions", "gy-checkout-"+attempt.ID, values, &session); e != nil {
		return "", "", e
	}
	u, e := url.Parse(session.URL)
	if e != nil || u.Scheme != "https" || u.Hostname() != "checkout.stripe.com" || u.User != nil || !strings.HasPrefix(session.ID, "cs_") {
		return "", "", errors.New("Stripe 未返回有效的安全收银台")
	}
	return session.URL, session.ID, nil
}
func verifyStripeSignature(body []byte, header, secret string, now int64) error {
	if secret == "" {
		return errors.New("Stripe webhook signing secret is missing")
	}
	var timestamp string
	signatures := []string{}
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "t" {
			if timestamp != "" {
				return errors.New("ambiguous Stripe signature timestamp")
			}
			timestamp = kv[1]
		}
		if kv[0] == "v1" {
			signatures = append(signatures, kv[1])
		}
	}
	ts, e := strconv.ParseInt(timestamp, 10, 64)
	if e != nil || ts < now-300 || ts > now+300 {
		return errors.New("Stripe webhook timestamp is outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, sig := range signatures {
		b, e := hex.DecodeString(sig)
		if e == nil && hmac.Equal(b, want) {
			return nil
		}
	}
	return errors.New("payment signature verification failed")
}

type stripeEvent struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Livemode bool   `json:"livemode"`
	Account  string `json:"account"`
	Data     struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}
type stripeRefund struct {
	ID            string            `json:"id"`
	PaymentIntent string            `json:"payment_intent"`
	Amount        int64             `json:"amount"`
	Currency      string            `json:"currency"`
	Status        string            `json:"status"`
	Metadata      map[string]string `json:"metadata"`
}

func verifyStripe(p PaymentProvider, body []byte, header string) (verifiedPaymentEvent, error) {
	var out verifiedPaymentEvent
	if e := verifyStripeSignature(body, header, p.Config["webhook_secret"], time.Now().Unix()); e != nil {
		return out, e
	}
	var ev stripeEvent
	if e := json.Unmarshal(body, &ev); e != nil {
		return out, e
	}
	if ev.ID == "" || len(ev.ID) > 200 {
		return out, errors.New("missing Stripe event id")
	}
	if (p.Config["mode"] == "live") != ev.Livemode {
		return out, errors.New("Stripe test/live mode mismatch")
	}
	if ev.Account != "" && ev.Account != p.Config["merchant_id"] {
		return out, errors.New("Stripe merchant mismatch")
	}
	out.EventID = ev.ID
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var session stripeCheckoutSession
		if e := json.Unmarshal(ev.Data.Object, &session); e != nil {
			return out, e
		}
		if session.PaymentStatus != "paid" {
			return out, nil
		}
		if !strings.HasPrefix(session.PaymentIntent, "pi_") {
			return out, errors.New("Stripe payment identity missing")
		}
		if session.ClientReferenceID == "" || session.Metadata["attempt_id"] != session.ClientReferenceID {
			session.ClientReferenceID = ""
			out.ReviewReason = "Stripe 已确认到账，但订单标识不匹配，请人工核对并原路退款"
		}
		if session.AmountTotal <= 0 || session.AmountTotal > moneyLimit {
			return out, errors.New("invalid Stripe amount")
		}
		out.MerchantRef = session.ClientReferenceID
		out.ExternalRef = session.PaymentIntent
		out.Amount = session.AmountTotal
		out.Currency = strings.ToUpper(session.Currency)
		out.Status = "paid"
	case "refund.created", "refund.updated", "refund.failed":
		var refund stripeRefund
		if e := json.Unmarshal(ev.Data.Object, &refund); e != nil {
			return out, e
		}
		out.ExternalRef = refund.PaymentIntent
		out.Amount = refund.Amount
		out.Currency = strings.ToUpper(refund.Currency)
		out.Status = "refund"
		out.RefundID = refund.ID
		out.RefundStatus = refund.Status
		out.MerchantRef = refund.Metadata["attempt_id"]
	}
	return out, nil
}

// This untrusted value only selects immutable verification credentials. All
// effects require a successful signature check and the exact attempt identity.
func paymentMerchantHint(p PaymentProvider, body []byte, query url.Values) string {
	switch p.Code {
	case "epay":
		return query.Get("out_trade_no")
	case "stripe":
		var ev stripeEvent
		_ = json.Unmarshal(body, &ev)
		var obj struct {
			ClientReferenceID string            `json:"client_reference_id"`
			Metadata          map[string]string `json:"metadata"`
		}
		_ = json.Unmarshal(ev.Data.Object, &obj)
		if obj.ClientReferenceID != "" {
			return obj.ClientReferenceID
		}
		return obj.Metadata["attempt_id"]
	default:
		var obj struct {
			MerchantRef string `json:"merchant_ref"`
		}
		_ = json.Unmarshal(body, &obj)
		return obj.MerchantRef
	}
}
