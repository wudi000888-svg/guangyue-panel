package controlplane

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// PaymentProvider is deliberately stored separately from commerce settings.
// One site can therefore expose several providers and a provider can have
// multiple payment attempts without changing the order snapshot.
type PaymentProvider struct {
	ID       string            `json:"id"`
	Code     string            `json:"code"`
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	Archived bool              `json:"archived"`
	Version  int               `json:"version"`
	Config   map[string]string `json:"-"`
}

type PaymentMethod struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	Version          int    `json:"version"`
	BaseURL          string `json:"base_url,omitempty"`
	MerchantID       string `json:"merchant_id,omitempty"`
	Channel          string `json:"channel,omitempty"`
	Checkout         bool   `json:"checkout"`
	Archived         bool   `json:"archived"`
	HasSecret        bool   `json:"has_secret"`
	HasWebhookSecret bool   `json:"has_webhook_secret"`
	WebhookURL       string `json:"webhook_url,omitempty"`
	AccountScope     string `json:"account_scope,omitempty"`
	Mode             string `json:"mode,omitempty"`
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
	Purpose        string `json:"purpose"`
	Message        string `json:"message"`
	AccountScope   string `json:"-"`
	RefundStatus   string `json:"refund_status,omitempty"`
}

type paymentProviderDoc struct {
	Secret        string `json:"secret"`
	BaseURL       string `json:"base_url"`
	MerchantID    string `json:"merchant_id"`
	Channel       string `json:"channel"`
	WebhookSecret string `json:"webhook_secret"`
	AccountScope  string `json:"account_scope"`
	Mode          string `json:"mode"`
}

type verifiedPaymentEvent struct {
	EventID      string
	MerchantRef  string
	ExternalRef  string
	Amount       int64
	Currency     string
	Status       string
	RefundID     string
	RefundStatus string
	ReviewReason string
}

func (s *Store) paymentProvider(id string) (PaymentProvider, error) {
	var p PaymentProvider
	var enabled, version int
	var body []byte
	err := s.db.QueryRow("SELECT id,code,name,enabled,version,doc,archived FROM payment_providers WHERE id=?", id).Scan(&p.ID, &p.Code, &p.Name, &enabled, &version, &body, &p.Archived)
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
		p.Config = map[string]string{"secret": doc.Secret, "base_url": doc.BaseURL, "merchant_id": doc.MerchantID, "channel": doc.Channel, "webhook_secret": doc.WebhookSecret, "account_scope": doc.AccountScope, "mode": doc.Mode}
	}
	return p, nil
}

func (s *Store) paymentProviders(enabledOnly bool) ([]PaymentProvider, error) {
	query := "SELECT id,code,name,enabled,version,doc,archived FROM payment_providers"
	if enabledOnly {
		query += " WHERE enabled=1 AND archived=0"
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
		if err = rows.Scan(&p.ID, &p.Code, &p.Name, &enabled, &p.Version, &body, &p.Archived); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		p.Config = map[string]string{}
		if len(body) > 0 {
			var doc paymentProviderDoc
			if err = s.vault.open(body, &doc); err != nil {
				return nil, err
			}
			p.Config = map[string]string{"secret": doc.Secret, "base_url": doc.BaseURL, "merchant_id": doc.MerchantID, "channel": doc.Channel, "webhook_secret": doc.WebhookSecret, "account_scope": doc.AccountScope, "mode": doc.Mode}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const paymentAttemptColumns = "id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,COALESCE(external_ref,''),checkout_url,idempotency_key,created,updated,expires,purpose,message,account_scope"

type paymentScanner interface{ Scan(...any) error }

func scanPaymentAttempt(row paymentScanner) (PaymentAttempt, error) {
	var a PaymentAttempt
	err := row.Scan(&a.ID, &a.OrderID, &a.UserID, &a.ProviderID, &a.State, &a.Amount, &a.Currency, &a.MerchantRef, &a.ExternalRef, &a.CheckoutURL, &a.IdempotencyKey, &a.Created, &a.Updated, &a.Expires, &a.Purpose, &a.Message, &a.AccountScope)
	return a, err
}
func (s *Store) paymentAttempt(id string) (PaymentAttempt, error) {
	return scanPaymentAttempt(s.db.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE id=?", id))
}
func (s *Store) paymentAttemptByMerchant(providerID, merchantRef string) (PaymentAttempt, error) {
	return scanPaymentAttempt(s.db.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE provider_id=? AND merchant_ref=? ORDER BY created DESC,id DESC LIMIT 1", providerID, merchantRef))
}

type paymentAttemptDoc struct {
	Provider    paymentProviderDoc `json:"provider"`
	Code        string             `json:"code"`
	Fingerprint string             `json:"fingerprint"`
	SessionID   string             `json:"session_id,omitempty"`
}

func providerDocument(p PaymentProvider) paymentProviderDoc {
	return paymentProviderDoc{Secret: p.Config["secret"], BaseURL: p.Config["base_url"], MerchantID: p.Config["merchant_id"], Channel: p.Config["channel"], WebhookSecret: p.Config["webhook_secret"], AccountScope: p.Config["account_scope"], Mode: p.Config["mode"]}
}
func (s *Store) attemptProvider(attempt PaymentAttempt) (PaymentProvider, paymentAttemptDoc, error) {
	var doc paymentAttemptDoc
	var body []byte
	err := s.db.QueryRow("SELECT doc FROM payment_attempts WHERE id=?", attempt.ID).Scan(&body)
	if err == nil {
		err = s.vault.open(body, &doc)
	}
	if err != nil {
		return PaymentProvider{}, doc, err
	}
	if doc.Code == "" {
		p, e := s.paymentProvider(attempt.ProviderID)
		return p, doc, e
	}
	p := PaymentProvider{ID: attempt.ProviderID, Code: doc.Code, Config: map[string]string{"secret": doc.Provider.Secret, "base_url": doc.Provider.BaseURL, "merchant_id": doc.Provider.MerchantID, "channel": doc.Provider.Channel, "webhook_secret": doc.Provider.WebhookSecret, "account_scope": doc.Provider.AccountScope, "mode": doc.Provider.Mode}}
	return p, doc, nil
}
func paymentAccountScope(p PaymentProvider) string {
	if scope := p.Config["account_scope"]; scope != "" {
		return scope
	}
	base, _ := url.Parse(strings.ToLower(strings.TrimSpace(p.Config["base_url"])))
	origin := ""
	if base != nil {
		origin = base.Hostname()
	}
	if p.Code == "stripe" {
		origin = "stripe"
	}
	merchant := strings.TrimSpace(p.Config["merchant_id"])
	if p.Code == "epay" {
		if id, err := strconv.ParseUint(merchant, 10, 64); err == nil {
			merchant = strconv.FormatUint(id, 10)
		}
	}
	return digest(p.Code + ":" + origin + ":" + merchant)
}
func paymentMethod(p PaymentProvider) PaymentMethod {
	return PaymentMethod{ID: p.ID, Code: p.Code, Name: p.Name, Enabled: p.Enabled, Version: p.Version, BaseURL: p.Config["base_url"], MerchantID: p.Config["merchant_id"], Channel: p.Config["channel"], Checkout: p.Code == "epay" || p.Code == "stripe", Archived: p.Archived, HasSecret: p.Config["secret"] != "", HasWebhookSecret: p.Config["webhook_secret"] != "", AccountScope: paymentAccountScope(p), Mode: p.Config["mode"]}
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
		if key != "sign" && key != "sign_type" && values.Get(key) != "" {
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
	label := "光月 · 套餐购买"
	if a.Purpose == "topup" {
		label = "光月 · 余额充值"
	}
	values := url.Values{
		"money":        {fmt.Sprintf("%d.%02d", a.Amount/100, a.Amount%100)},
		"name":         {label},
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
	for _, v := range values {
		if len(v) != 1 {
			return out, errors.New("ambiguous payment parameters")
		}
	}
	if p.Config["secret"] == "" || values.Get("pid") != p.Config["merchant_id"] || strings.ToUpper(values.Get("sign_type")) != "MD5" {
		return out, errors.New("payment merchant or signature type mismatch")
	}
	if values.Get("sign") == "" || !hmac.Equal([]byte(strings.ToLower(values.Get("sign"))), []byte(epaySign(values, p.Config["secret"]))) {
		return out, errors.New("payment signature verification failed")
	}
	status := strings.ToLower(strings.TrimSpace(values.Get("trade_status")))
	if status == "" {
		return out, errors.New("payment status is missing")
	}
	amount, err := paymentCents(values.Get("money"))
	if err != nil {
		return out, err
	}
	out.EventID = values.Get("trade_no")
	out.MerchantRef = values.Get("out_trade_no")
	out.ExternalRef = values.Get("trade_no")
	if out.EventID == "" || out.MerchantRef == "" || len(out.EventID) > 200 || len(out.MerchantRef) > 200 {
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
