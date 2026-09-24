package controlplane

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PaymentProvider is deliberately stored separately from commerce settings.
// One site can therefore expose several providers and a provider can have
// multiple payment attempts without changing the order snapshot.
type PaymentProvider struct {
	ID      string            `json:"id"`
	Code    string            `json:"code"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Version int               `json:"version"`
	Config  map[string]string `json:"-"`
}

type PaymentMethod struct {
	ID         string `json:"id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Version    int    `json:"version"`
	BaseURL    string `json:"base_url,omitempty"`
	MerchantID string `json:"merchant_id,omitempty"`
	Channel    string `json:"channel,omitempty"`
	Checkout   bool   `json:"checkout"`
}

type PaymentAttempt struct {
	ID             string `json:"id"`
	OrderID        string `json:"order_id"`
	UserID         int64  `json:"user_id"`
	ProviderID     string `json:"provider_id"`
	State          string `json:"state"`
	Amount         int64  `json:"amount,string"`
	Currency       string `json:"currency"`
	MerchantRef    string `json:"merchant_ref"`
	ExternalRef    string `json:"external_ref,omitempty"`
	CheckoutURL    string `json:"checkout_url,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	Created        int64  `json:"created"`
	Updated        int64  `json:"updated"`
	Expires        int64  `json:"expires"`
}

type paymentProviderDoc struct {
	Secret     string `json:"secret"`
	BaseURL    string `json:"base_url"`
	MerchantID string `json:"merchant_id"`
	Channel    string `json:"channel"`
}

type verifiedPaymentEvent struct {
	EventID     string
	MerchantRef string
	ExternalRef string
	Amount      int64
	Currency    string
	Status      string
}

func (s *Store) paymentProvider(id string) (PaymentProvider, error) {
	var p PaymentProvider
	var enabled, version int
	var body []byte
	err := s.db.QueryRow("SELECT id,code,name,enabled,version,doc FROM payment_providers WHERE id=?", id).Scan(&p.ID, &p.Code, &p.Name, &enabled, &version, &body)
	if err != nil {
		return p, err
	}
	p.Enabled = enabled != 0
	p.Version = version
	p.Config = map[string]string{}
	if len(body) > 0 {
		var doc paymentProviderDoc
		if err = s.vault.open(body, &doc); err != nil {
			return p, err
		}
		p.Config = map[string]string{"secret": doc.Secret, "base_url": doc.BaseURL, "merchant_id": doc.MerchantID, "channel": doc.Channel}
	}
	return p, nil
}

func (s *Store) paymentProviders(enabledOnly bool) ([]PaymentProvider, error) {
	query := "SELECT id,code,name,enabled,version,doc FROM payment_providers"
	if enabledOnly {
		query += " WHERE enabled=1"
	}
	query += " ORDER BY name,id"
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PaymentProvider
	for rows.Next() {
		var p PaymentProvider
		var enabled int
		var body []byte
		if err = rows.Scan(&p.ID, &p.Code, &p.Name, &enabled, &p.Version, &body); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		p.Config = map[string]string{}
		if len(body) > 0 {
			var doc paymentProviderDoc
			if err = s.vault.open(body, &doc); err != nil {
				return nil, err
			}
			p.Config = map[string]string{"secret": doc.Secret, "base_url": doc.BaseURL, "merchant_id": doc.MerchantID, "channel": doc.Channel}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) paymentAttemptByMerchant(providerID, merchantRef string) (PaymentAttempt, error) {
	var a PaymentAttempt
	var external, checkout, idempotency string
	err := s.db.QueryRow("SELECT id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,COALESCE(external_ref,''),checkout_url,idempotency_key,created,updated,expires FROM payment_attempts WHERE provider_id=? AND merchant_ref=? ORDER BY created DESC LIMIT 1", providerID, merchantRef).Scan(&a.ID, &a.OrderID, &a.UserID, &a.ProviderID, &a.State, &a.Amount, &a.Currency, &a.MerchantRef, &external, &checkout, &idempotency, &a.Created, &a.Updated, &a.Expires)
	a.ExternalRef, a.CheckoutURL, a.IdempotencyKey = external, checkout, idempotency
	return a, err
}

func paymentMethod(p PaymentProvider) PaymentMethod {
	return PaymentMethod{ID: p.ID, Code: p.Code, Name: p.Name, Enabled: p.Enabled, Version: p.Version, BaseURL: p.Config["base_url"], MerchantID: p.Config["merchant_id"], Channel: p.Config["channel"], Checkout: p.Code == "epay"}
}

func paymentCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("missing payment amount")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("invalid payment amount")
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 2 {
		return 0, errors.New("payment amount has too many decimals")
	}
	for _, c := range parts[0] + frac {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid payment amount")
		}
	}
	frac += strings.Repeat("0", 2-len(frac))
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > moneyLimit/100 {
		return 0, errors.New("payment amount exceeds limit")
	}
	n, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	amount := whole*100 + n
	if amount <= 0 || amount > moneyLimit {
		return 0, errors.New("payment amount exceeds limit")
	}
	return amount, nil
}

func epaySign(values url.Values, secret string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "sign" && key != "sign_type" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, key := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(values.Get(key))
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func createPaymentCheckout(p PaymentProvider, a PaymentAttempt, notifyURL, returnURL string) (string, error) {
	if p.Code != "epay" {
		return "", nil
	}
	base := strings.TrimRight(strings.TrimSpace(p.Config["base_url"]), "/")
	merchant := strings.TrimSpace(p.Config["merchant_id"])
	secret := p.Config["secret"]
	if base == "" || merchant == "" || secret == "" {
		return "", errors.New("payment provider is not configured")
	}
	values := url.Values{
		"money":        {fmt.Sprintf("%d.%02d", a.Amount/100, a.Amount%100)},
		"name":         {a.MerchantRef},
		"notify_url":   {notifyURL},
		"return_url":   {returnURL},
		"out_trade_no": {a.MerchantRef},
		"pid":          {merchant},
	}
	if channel := strings.TrimSpace(p.Config["channel"]); channel != "" {
		values.Set("type", channel)
	}
	values.Set("sign", epaySign(values, secret))
	values.Set("sign_type", "MD5")
	return base + "/submit.php?" + values.Encode(), nil
}

func verifyEpay(p PaymentProvider, values url.Values) (verifiedPaymentEvent, error) {
	var out verifiedPaymentEvent
	if values.Get("sign") == "" || !hmac.Equal([]byte(strings.ToLower(values.Get("sign"))), []byte(epaySign(values, p.Config["secret"]))) {
		return out, errors.New("payment signature verification failed")
	}
	status := strings.ToLower(strings.TrimSpace(values.Get("trade_status")))
	if status == "" {
		status = "success"
	}
	amount, err := paymentCents(values.Get("money"))
	if err != nil {
		return out, err
	}
	out.EventID = values.Get("trade_no")
	out.MerchantRef = values.Get("out_trade_no")
	out.ExternalRef = values.Get("trade_no")
	if out.EventID == "" {
		return out, errors.New("payment event reference is missing")
	}
	out.Amount = amount
	out.Currency = "CNY"
	out.Status = status
	return out, nil
}

func verifyJSONWebhook(p PaymentProvider, body []byte, signature string) (verifiedPaymentEvent, error) {
	var out verifiedPaymentEvent
	sum := hmac.New(sha256.New, []byte(p.Config["secret"]))
	_, _ = sum.Write(body)
	want := "sha256=" + hex.EncodeToString(sum.Sum(nil))
	if signature == "" || !hmac.Equal([]byte(signature), []byte(want)) {
		return out, errors.New("payment signature verification failed")
	}
	var input struct {
		EventID     string `json:"event_id"`
		MerchantRef string `json:"merchant_ref"`
		ExternalRef string `json:"external_ref"`
		Amount      string `json:"amount"`
		Currency    string `json:"currency"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return out, err
	}
	amount, err := paymentCents(input.Amount)
	if err != nil {
		return out, err
	}
	out.EventID, out.MerchantRef, out.ExternalRef = input.EventID, input.MerchantRef, input.ExternalRef
	out.Amount, out.Currency, out.Status = amount, strings.ToUpper(strings.TrimSpace(input.Currency)), strings.ToLower(strings.TrimSpace(input.Status))
	if out.Currency == "" {
		out.Currency = "CNY"
	}
	if out.EventID == "" {
		out.EventID = digest(string(body))
	}
	if out.ExternalRef == "" {
		out.ExternalRef = out.EventID
	}
	if out.MerchantRef == "" {
		return out, errors.New("payment order reference is missing")
	}
	return out, nil
}

func (a *App) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.PathValue("provider"))
	p, err := a.store.paymentProvider(providerID)
	// Disabling a method prevents new checkout attempts, but must not make an
	// already-created payment impossible to settle when the provider retries a
	// callback later.
	if err != nil {
		failure(w, 404, "支付方式不存在")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failure(w, 400, "支付回调格式无效")
		return
	}
	// GET callbacks carry the signed form in the query string.  Preserve it as
	// the event payload so different callbacks cannot collide on an empty-body
	// hash.
	eventBody := body
	if len(eventBody) == 0 && r.URL.RawQuery != "" {
		eventBody = []byte(r.URL.RawQuery)
	}
	var verified verifiedPaymentEvent
	if p.Code == "epay" {
		values := r.URL.Query()
		if r.Method == http.MethodPost {
			if form, e := url.ParseQuery(string(body)); e == nil {
				values = form
			}
		}
		verified, err = verifyEpay(p, values)
	} else {
		verified, err = verifyJSONWebhook(p, body, r.Header.Get("X-Guangyue-Signature"))
	}
	if err != nil {
		failure(w, 401, err.Error())
		return
	}
	if verified.Status != "success" && verified.Status != "paid" && verified.Status != "trade_success" {
		if p.Code == "epay" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("success"))
		} else {
			jsonResponse(w, 200, object{"ok": true, "ignored": true})
		}
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err = a.recordPaymentEvent(p, verified, eventBody); err != nil {
		failure(w, 409, "支付回调暂未完成，请稍后重试")
		return
	}
	if p.Code == "epay" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("success"))
	} else {
		jsonResponse(w, 200, object{"ok": true})
	}
}

func (a *App) recordPaymentEvent(p PaymentProvider, event verifiedPaymentEvent, body []byte) error {
	if event.MerchantRef == "" {
		return errors.New("payment order reference is missing")
	}
	attempt, err := a.store.paymentAttemptByMerchant(p.ID, event.MerchantRef)
	if err != nil {
		return err
	}
	if attempt.State == "refund_required" {
		_, err = a.store.db.Exec("UPDATE payment_events SET state='applied',processed=?,message=? WHERE provider_id=? AND external_id=? AND processed=0", time.Now().Unix(), "开通失败，已转人工退款", p.ID, event.EventID)
		return err
	}
	if attempt.State == "cancelled" || attempt.State == "expired" {
		return errors.New("payment attempt is no longer active")
	}
	if attempt.Amount != event.Amount || event.Currency != "" && event.Currency != attempt.Currency {
		return errors.New("payment amount or currency mismatch")
	}
	hash := digest(string(body))
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var processed int
	err = tx.QueryRow("SELECT processed FROM payment_events WHERE provider_id=? AND (external_id=? OR payload_hash=?)", p.ID, event.EventID, hash).Scan(&processed)
	if err == nil && processed != 0 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec("INSERT INTO payment_events(id,provider_id,external_id,payload_hash,signature_valid,state,order_id,attempt_id,body,created) VALUES(?,?,?,?,1,'verified',?,?,?,?)", serial("GYE"), p.ID, event.EventID, hash, attempt.OrderID, attempt.ID, body, time.Now().Unix())
		if err != nil {
			// A concurrent duplicate callback is safe; the other request will do
			// the activation and this request can return success.
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return nil
			}
			return err
		}
	}
	if _, err = tx.Exec("UPDATE payment_attempts SET state='paid',external_ref=?,updated=? WHERE id=? AND state IN ('created','awaiting_customer')", event.ExternalRef, time.Now().Unix(), attempt.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	user, err := a.store.record(attempt.UserID)
	if err != nil {
		return err
	}
	if err = a.confirmExternalOrder(attempt.OrderID, user, attempt.ID); err != nil {
		return err
	}
	_, err = a.store.db.Exec("UPDATE payment_events SET state='applied',processed=?,message=? WHERE provider_id=? AND payload_hash=?", time.Now().Unix(), "套餐开通已进入队列", p.ID, hash)
	return err
}
