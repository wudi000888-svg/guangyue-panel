package controlplane

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

func (a *App) listPaymentMethods(w http.ResponseWriter, actor Record) error {
	providers, err := a.store.paymentProviders(actor.Role != "owner")
	if err != nil {
		return err
	}
	methods := make([]PaymentMethod, 0, len(providers))
	for _, provider := range providers {
		methods = append(methods, paymentMethod(provider))
	}
	jsonResponse(w, 200, object{"items": methods})
	return nil
}

func (a *App) savePaymentMethod(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID         string `json:"id"`
		Version    int    `json:"version"`
		Code       string `json:"code"`
		Name       string `json:"name"`
		Enabled    bool   `json:"enabled"`
		BaseURL    string `json:"base_url"`
		MerchantID string `json:"merchant_id"`
		Secret     string `json:"secret"`
		Channel    string `json:"channel"`
		Password   string `json:"password"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, err := a.commerceActor(actor, true, in.Password); err != nil {
		return err
	}
	in.Code = strings.ToLower(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if in.Code != "epay" && in.Code != "webhook" {
		return commerceFail(400, "暂不支持该支付适配器")
	}
	if !validText(in.Name, 80) {
		return commerceFail(400, "支付方式名称无效")
	}
	if in.Code == "epay" && !validWebhookURL(strings.TrimSpace(in.BaseURL)) {
		return commerceFail(400, "易支付网关地址必须是 http 或 https 地址")
	}
	if len(strings.TrimSpace(in.Secret)) > 256 || len(strings.TrimSpace(in.MerchantID)) > 120 || len(in.Channel) > 40 {
		return commerceFail(400, "支付配置长度无效")
	}
	var old PaymentProvider
	var err error
	if in.ID != "" {
		old, err = a.store.paymentProvider(in.ID)
		if err != nil {
			return commerceFail(404, "支付方式不存在")
		}
		if in.Version != old.Version {
			return commerceFail(409, "支付方式已变化，请刷新后重试")
		}
	}
	if strings.TrimSpace(in.Secret) == "" {
		in.Secret = old.Config["secret"]
	}
	if in.Code == "epay" && (strings.TrimSpace(in.MerchantID) == "" || strings.TrimSpace(in.Secret) == "") {
		return commerceFail(400, "易支付商户号和密钥不能为空")
	}
	if in.Code == "webhook" && strings.TrimSpace(in.Secret) == "" {
		return commerceFail(400, "Webhook 签名密钥不能为空")
	}
	doc, err := a.store.vault.seal(paymentProviderDoc{Secret: strings.TrimSpace(in.Secret), BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), MerchantID: strings.TrimSpace(in.MerchantID), Channel: strings.TrimSpace(in.Channel)})
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	p := PaymentProvider{ID: in.ID, Code: in.Code, Name: in.Name, Enabled: in.Enabled, Version: in.Version + 1}
	if p.ID == "" {
		p.ID = serial("GYPAY")
		p.Version = 1
		_, err = a.store.db.Exec("INSERT INTO payment_providers(id,code,name,enabled,version,doc,created,updated) VALUES(?,?,?,?,?,?,?,?)", p.ID, p.Code, p.Name, boolInt(p.Enabled), p.Version, doc, now, now)
	} else {
		_, err = a.store.db.Exec("UPDATE payment_providers SET code=?,name=?,enabled=?,version=?,doc=?,updated=? WHERE id=? AND version=?", p.Code, p.Name, boolInt(p.Enabled), p.Version, doc, now, p.ID, in.Version)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return commerceFail(409, "支付方式编码已存在")
		}
		return err
	}
	a.store.audit(actor.Username, "payment-provider-save", p.ID)
	jsonResponse(w, 200, paymentMethod(p))
	return nil
}

func (a *App) createPayment(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		OrderID       string `json:"order_id"`
		PaymentMethod string `json:"payment_method"`
		OperationID   string `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if b, err := a.store.commerceReplay(actor.ID, in.OperationID, in); err != nil {
		return err
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	o, err := a.store.order(in.OrderID)
	if err != nil || o.UserID != actor.ID {
		return commerceFail(404, "订单不存在")
	}
	if o.State != "pending" || o.Expires <= time.Now().Unix() {
		return commerceFail(409, "订单已失效或正在处理")
	}
	if o.Offer.Price <= 0 {
		return commerceFail(409, "零元套餐无需在线支付")
	}
	p, err := a.store.paymentProvider(strings.TrimSpace(in.PaymentMethod))
	if err != nil || !p.Enabled {
		return commerceFail(409, "支付方式不可用")
	}
	var existing PaymentAttempt
	if err = a.store.db.QueryRow("SELECT id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,COALESCE(external_ref,''),checkout_url,idempotency_key,created,updated,expires FROM payment_attempts WHERE order_id=? AND provider_id=? AND state IN ('created','awaiting_customer','paid') ORDER BY created DESC LIMIT 1", o.ID, p.ID).Scan(&existing.ID, &existing.OrderID, &existing.UserID, &existing.ProviderID, &existing.State, &existing.Amount, &existing.Currency, &existing.MerchantRef, &existing.ExternalRef, &existing.CheckoutURL, &existing.IdempotencyKey, &existing.Created, &existing.Updated, &existing.Expires); err == nil {
		result := object{"attempt": existing, "checkout_url": existing.CheckoutURL, "type": "redirect"}
		tx, txErr := a.store.db.Begin()
		if txErr == nil {
			txErr = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, result)
		}
		if txErr == nil {
			txErr = tx.Commit()
		}
		if txErr != nil {
			return txErr
		}
		jsonResponse(w, 200, result)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().Unix()
	attempt := PaymentAttempt{ID: serial("GYA"), OrderID: o.ID, UserID: actor.ID, ProviderID: p.ID, State: "awaiting_customer", Amount: o.Offer.Price, Currency: "CNY", MerchantRef: o.ID, IdempotencyKey: in.OperationID, Created: now, Updated: now, Expires: o.Expires}
	if !validWebhookURL(strings.TrimRight(a.cfg.PublicURL, "/")) {
		return commerceFail(409, "站点公开地址未配置，暂时无法创建在线支付")
	}
	attempt.CheckoutURL, err = createPaymentCheckout(p, attempt, strings.TrimRight(a.cfg.PublicURL, "/")+"/api/payments/webhook/"+p.ID, strings.TrimRight(a.cfg.PublicURL, "/")+"/#/orders")
	if err != nil {
		return commerceFail(409, err.Error())
	}
	if attempt.CheckoutURL == "" {
		return commerceFail(409, "该支付方式暂不支持在线下单")
	}
	doc, err := a.store.vault.seal(object{"channel": p.Config["channel"]})
	if err != nil {
		return err
	}
	result := object{"attempt": attempt, "checkout_url": attempt.CheckoutURL, "type": "redirect"}
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO payment_attempts(id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,checkout_url,idempotency_key,created,updated,expires,doc) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", attempt.ID, attempt.OrderID, attempt.UserID, attempt.ProviderID, attempt.State, attempt.Amount, attempt.Currency, attempt.MerchantRef, attempt.CheckoutURL, attempt.IdempotencyKey, attempt.Created, attempt.Updated, attempt.Expires, doc); err != nil {
		return err
	}
	o.PaymentAttemptID = attempt.ID
	o.PaymentProviderID = p.ID
	o.Message = "等待在线支付"
	if err = saveOrder(tx, o, "pending"); err != nil {
		return err
	}
	if err = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, result); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	jsonResponse(w, 201, result)
	return nil
}

func (a *App) confirmExternalOrder(orderID string, user Record, attemptID string) error {
	body, _ := json.Marshal(object{"id": orderID, "action": "confirm", "operation_id": serial("GYOP"), "payment_attempt_id": attemptID})
	req := httptest.NewRequest(http.MethodPost, "/api/commerce/orders/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.orderAction(w, req, user)
	if w.Code < 200 || w.Code >= 300 {
		return errors.New("payment was verified but order activation is pending")
	}
	return nil
}
