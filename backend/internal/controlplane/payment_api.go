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
	if actor.Role != "owner" {
		enabled, e := a.paymentModuleEnabled()
		if e != nil {
			return e
		}
		if !enabled {
			jsonResponse(w, 200, object{"items": []PaymentMethod{}})
			return nil
		}
	}
	providers, err := a.store.paymentProviders(actor.Role != "owner")
	if err != nil {
		return err
	}
	methods := make([]PaymentMethod, 0, len(providers))
	for _, p := range providers {
		m := paymentMethod(p)
		if actor.Role == "owner" {
			m.WebhookURL = strings.TrimRight(a.cfg.PublicURL, "/") + "/api/payments/webhook/" + p.ID
		} else {
			m.BaseURL, m.MerchantID, m.Channel, m.AccountScope, m.Mode = "", "", "", "", ""
			m.HasSecret, m.HasWebhookSecret = false, false
		}
		methods = append(methods, m)
	}
	jsonResponse(w, 200, object{"items": methods})
	return nil
}
func (a *App) savePaymentMethod(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID            string `json:"id"`
		Version       int    `json:"version"`
		Code          string `json:"code"`
		Name          string `json:"name"`
		Enabled       bool   `json:"enabled"`
		BaseURL       string `json:"base_url"`
		MerchantID    string `json:"merchant_id"`
		Secret        string `json:"secret"`
		WebhookSecret string `json:"webhook_secret"`
		Channel       string `json:"channel"`
		Password      string `json:"password"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, err := a.commerceActor(actor, true, in.Password); err != nil {
		return err
	}
	if in.Enabled {
		if e := a.requirePaymentModule(); e != nil {
			return e
		}
	}
	in.Code = strings.ToLower(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.MerchantID = strings.TrimSpace(in.MerchantID)
	in.Channel = strings.TrimSpace(in.Channel)
	if in.Code != "epay" && in.Code != "stripe" && in.Code != "webhook" {
		return commerceFail(400, "暂不支持该支付适配器")
	}
	if !validText(in.Name, 80) || len(in.Secret) > 512 || len(in.WebhookSecret) > 512 || len(in.MerchantID) > 120 || len(in.Channel) > 40 {
		return commerceFail(400, "支付配置格式无效")
	}
	var old PaymentProvider
	var err error
	if in.ID != "" {
		old, err = a.store.paymentProvider(in.ID)
		if err != nil {
			return commerceFail(404, "支付方式不存在")
		}
		if old.Version != in.Version || old.Archived {
			return commerceFail(409, "支付方式已变化或已归档，请刷新后重试")
		}
		if old.Code != in.Code {
			return commerceFail(409, "已创建的支付方式不能更换适配器，请新增支付方式")
		}
	}
	if in.Secret == "" {
		in.Secret = old.Config["secret"]
	}
	if in.WebhookSecret == "" {
		in.WebhookSecret = old.Config["webhook_secret"]
	}
	p := PaymentProvider{ID: in.ID, Version: in.Version + 1, Code: in.Code, Name: in.Name, Enabled: in.Enabled, Config: map[string]string{"secret": strings.TrimSpace(in.Secret), "webhook_secret": strings.TrimSpace(in.WebhookSecret), "merchant_id": in.MerchantID, "base_url": in.BaseURL, "channel": in.Channel}}
	if p.Config["secret"] == "" {
		return commerceFail(400, "支付密钥不能为空")
	}
	switch p.Code {
	case "epay":
		if !paymentHTTPS(in.BaseURL) || validatePublicSourceURL(in.BaseURL) != nil {
			return commerceFail(400, "易支付网关必须使用公网 HTTPS 地址")
		}
		if in.MerchantID == "" {
			return commerceFail(400, "易支付商户号不能为空")
		}
		if in.Channel != "" && in.Channel != "alipay" && in.Channel != "wxpay" && in.Channel != "qqpay" {
			return commerceFail(400, "易支付渠道应为 alipay、wxpay 或 qqpay")
		}
	case "stripe":
		if !strings.HasPrefix(p.Config["secret"], "sk_test_") && !strings.HasPrefix(p.Config["secret"], "sk_live_") {
			return commerceFail(400, "请填写 Stripe 标准 Secret key（sk_test_ 或 sk_live_）")
		}
		if (p.Enabled || p.Config["webhook_secret"] != "") && !strings.HasPrefix(p.Config["webhook_secret"], "whsec_") {
			return commerceFail(400, "请填写 Stripe Webhook 签名密钥（whsec_）")
		}
		account := old.Config["merchant_id"]
		// Routine disable/rename remains possible during a provider outage.
		if old.ID == "" || p.Config["secret"] != old.Config["secret"] || account == "" || in.MerchantID != "" && in.MerchantID != account {
			var e error
			account, e = a.stripeAccount(r.Context(), p)
			if e != nil {
				return commerceFail(409, e.Error())
			}
		}
		if in.MerchantID != "" && account != in.MerchantID {
			return commerceFail(400, "Stripe 商户号与密钥不属于同一账号")
		}
		p.Config["merchant_id"] = account
		p.Config["base_url"] = ""
		p.Config["channel"] = ""
		p.Config["mode"] = "test"
		if strings.HasPrefix(p.Config["secret"], "sk_live_") {
			p.Config["mode"] = "live"
		}
	}
	p.Config["account_scope"] = paymentAccountScope(p)
	doc, err := a.store.vault.seal(providerDocument(p))
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if p.ID == "" {
		p.ID = serial("GYPAY")
		p.Version = 1
		_, err = a.store.db.Exec("INSERT INTO payment_providers(id,code,name,enabled,version,doc,created,updated) VALUES(?,?,?,?,?,?,?,?)", p.ID, p.Code, p.Name, boolInt(p.Enabled), p.Version, doc, now, now)
	} else {
		result, e := a.store.db.Exec("UPDATE payment_providers SET name=?,enabled=?,version=?,doc=?,updated=? WHERE id=? AND version=? AND archived=0", p.Name, boolInt(p.Enabled), p.Version, doc, now, p.ID, in.Version)
		err = e
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return commerceFail(409, "支付方式已被其他管理员修改，请刷新")
			}
		}
	}
	if err != nil {
		return err
	}
	a.store.audit(actor.Username, "payment-provider-save", p.ID)
	m := paymentMethod(p)
	m.WebhookURL = strings.TrimRight(a.cfg.PublicURL, "/") + "/api/payments/webhook/" + p.ID
	jsonResponse(w, 200, m)
	return nil
}
func (a *App) deletePaymentMethod(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID       string `json:"id"`
		Version  int    `json:"version"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, e := a.commerceActor(actor, true, in.Password); e != nil {
		return e
	}
	// Archive first even without history. This locks the provider against checkout
	// reservation; a provider referenced by any checkout or event is never erased.
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.Exec("UPDATE payment_providers SET archived=1,enabled=0,version=version+1,updated=? WHERE id=? AND version=? AND archived=0", time.Now().Unix(), in.ID, in.Version)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return commerceFail(409, "支付方式不存在、已归档或已变化")
	}
	var count int
	if e = tx.QueryRow("SELECT (SELECT COUNT(*) FROM payment_attempts WHERE provider_id=?)+(SELECT COUNT(*) FROM payment_events WHERE provider_id=?)", in.ID, in.ID).Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		if _, e = tx.Exec("DELETE FROM payment_providers WHERE id=? AND archived=1", in.ID); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.store.audit(actor.Username, "payment-provider-delete", in.ID)
	jsonResponse(w, 200, object{"ok": true, "archived": count > 0})
	return nil
}
func (a *App) checkPaymentMethod(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, e := a.commerceActor(actor, true, in.Password); e != nil {
		return e
	}
	p, e := a.store.paymentProvider(in.ID)
	if e != nil {
		return commerceFail(404, "支付方式不存在")
	}
	message := "配置已保存；尚需支付平台回调验证"
	ok := true
	if p.Code == "stripe" {
		account, err := a.stripeAccount(r.Context(), p)
		if err != nil {
			message = err.Error()
			ok = false
		} else if account != p.Config["merchant_id"] {
			message = "商户账号不匹配"
			ok = false
		} else {
			message = "Stripe 密钥和商户身份已验证；回调需在 Stripe 控制台发送测试事件验证"
		}
	} else if p.Code == "epay" {
		if !paymentHTTPS(p.Config["base_url"]) {
			ok = false
			message = "网关必须使用 HTTPS"
		} else {
			request, _ := http.NewRequestWithContext(r.Context(), "HEAD", p.Config["base_url"], nil)
			response, err := a.paymentHTTP().Do(request)
			if err != nil {
				ok = false
				message = "网关连接失败，请检查地址和服务器出站网络"
			} else {
				response.Body.Close()
				ok = response.StatusCode < 500
				message = "网关 HTTPS 可访问；此检查不代表商户密钥或支付通道已验证"
			}
		}
	}
	var last int64
	var pending, refunds int
	if e = a.store.db.QueryRow("SELECT COALESCE(MAX(created),0) FROM payment_events WHERE provider_id=? AND signature_valid=1", p.ID).Scan(&last); e != nil {
		return e
	}
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_attempts WHERE provider_id=? AND state IN ('created','awaiting_customer','paid')", p.ID).Scan(&pending); e != nil {
		return e
	}
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE provider_id=? AND state IN ('refund_required','review_required')", p.ID).Scan(&refunds); e != nil {
		return e
	}
	jsonResponse(w, 200, object{"ok": ok, "message": message, "checked_at": time.Now().Unix(), "last_event_at": last, "pending": pending, "refund_required": refunds})
	return nil
}

type paymentCreateInput struct {
	OrderID       string `json:"order_id,omitempty"`
	Amount        string `json:"amount,omitempty"`
	PaymentMethod string `json:"payment_method"`
	OperationID   string `json:"operation_id"`
}

func (a *App) createPayment(w http.ResponseWriter, r *http.Request, actor Record) error {
	return a.createCheckout(w, r, actor, "order")
}
func (a *App) createTopup(w http.ResponseWriter, r *http.Request, actor Record) error {
	return a.createCheckout(w, r, actor, "topup")
}
func (a *App) createCheckout(w http.ResponseWriter, r *http.Request, actor Record, purpose string) error {
	var in paymentCreateInput
	if !decode(w, r, &in) {
		return nil
	}
	input := object{"purpose": purpose, "input": in}
	if replay, e := a.store.commerceReplay(actor.ID, in.OperationID, input); e != nil {
		return e
	} else if replay != nil {
		var saved struct {
			Attempt PaymentAttempt `json:"attempt"`
		}
		if e = json.Unmarshal(replay, &saved); e != nil {
			return e
		}
		current, e := a.store.paymentAttempt(saved.Attempt.ID)
		if e != nil {
			return e
		}
		jsonResponse(w, 200, object{"attempt": current, "checkout_url": current.CheckoutURL, "type": "redirect"})
		return nil
	}
	if !paymentHTTPS(strings.TrimRight(a.cfg.PublicURL, "/")) {
		return commerceFail(409, "站点公开地址必须配置为 HTTPS，才能创建在线支付")
	}
	attempt, e := a.reservePayment(actor, purpose, in, input)
	if e != nil {
		return e
	}
	if attempt.State == "created" && attempt.Expires > time.Now().Unix() {
		p, doc, err := a.store.attemptProvider(attempt)
		if err != nil {
			return err
		}
		returnURL := strings.TrimRight(a.cfg.PublicURL, "/") + "/#/orders"
		if purpose == "topup" {
			returnURL = strings.TrimRight(a.cfg.PublicURL, "/") + "/#/wallet"
		}
		if p.Code == "stripe" {
			attempt.CheckoutURL, doc.SessionID, err = a.stripeCheckout(r.Context(), p, attempt, returnURL)
		} else {
			attempt.CheckoutURL, err = createPaymentCheckout(p, attempt, strings.TrimRight(a.cfg.PublicURL, "/")+"/api/payments/webhook/"+p.ID, returnURL)
		}
		if err != nil {
			_, _ = a.store.db.Exec("UPDATE payment_attempts SET message=?,updated=? WHERE id=? AND state='created'", err.Error(), time.Now().Unix(), attempt.ID)
			return commerceFail(409, err.Error()+"；使用原操作重试即可，不会重复创建支付")
		}
		if attempt.CheckoutURL == "" {
			return commerceFail(409, "该方式不支持在线收银台")
		}
		docBody, err := a.store.vault.seal(doc)
		if err != nil {
			return err
		}
		if _, err = a.store.db.Exec("UPDATE payment_attempts SET state='awaiting_customer',checkout_url=?,doc=?,message='等待完成在线支付',updated=? WHERE id=? AND state='created'", attempt.CheckoutURL, docBody, time.Now().Unix(), attempt.ID); err != nil {
			return err
		}
		attempt, e = a.store.paymentAttempt(attempt.ID)
		if e != nil {
			return e
		}
	}
	result := object{"attempt": attempt, "checkout_url": attempt.CheckoutURL, "type": "redirect"}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Persist the operation even when it reused another attempt of this order.
	response, e := a.store.vault.seal(result)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO commerce_requests(id,user_id,fingerprint,response,created) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING", in.OperationID, actor.ID, digest(string(jsonBytes(input))), response, time.Now().Unix()); e != nil {
		return e
	}
	var id int64
	var fingerprint string
	if e = tx.QueryRow("SELECT user_id,fingerprint FROM commerce_requests WHERE id=?", in.OperationID).Scan(&id, &fingerprint); e != nil {
		return e
	}
	if id != actor.ID || fingerprint != digest(string(jsonBytes(input))) {
		return commerceFail(409, "操作标识已用于其他请求")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonResponse(w, 201, result)
	return nil
}
func (a *App) reservePayment(actor Record, purpose string, in paymentCreateInput, input any) (PaymentAttempt, error) {
	var empty PaymentAttempt
	if !operationPattern.MatchString(in.OperationID) {
		return empty, commerceFail(400, "缺少有效操作标识")
	}
	existing, e := scanPaymentAttempt(a.store.db.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE idempotency_key=?", in.OperationID))
	if e == nil {
		_, doc, de := a.store.attemptProvider(existing)
		if de != nil {
			return empty, de
		}
		if existing.UserID != actor.ID || doc.Fingerprint != digest(string(jsonBytes(input))) {
			return empty, commerceFail(409, "操作标识已用于其他请求")
		}
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return empty, e
	}
	p, e := a.store.paymentProvider(in.PaymentMethod)
	if e != nil || !p.Enabled || p.Archived {
		return empty, commerceFail(409, "支付方式暂未开放")
	}
	if p.Code != "epay" && p.Code != "stripe" {
		return empty, commerceFail(409, "该方式不支持在线收银台")
	}
	now := time.Now().Unix()
	attempt := PaymentAttempt{ID: serial("GYA"), UserID: actor.ID, ProviderID: p.ID, State: "created", Currency: "CNY", Purpose: purpose, IdempotencyKey: in.OperationID, Created: now, Updated: now, Expires: now + 1800, AccountScope: paymentAccountScope(p)}
	attempt.MerchantRef = attempt.ID
	tx, e := a.store.db.Begin()
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	// Serialize removal/disable against checkout reservation without holding a DB
	// transaction while contacting an external payment service.
	lock, e := tx.Exec("UPDATE payment_providers SET updated=updated WHERE id=? AND enabled=1 AND archived=0 AND version=?", p.ID, p.Version)
	if e != nil {
		return empty, e
	}
	n, _ := lock.RowsAffected()
	if n != 1 {
		return empty, commerceFail(409, "支付方式已变化，请刷新后重试")
	}
	var order Order
	if purpose == "order" {
		lock, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=? AND user_id=? AND state='pending'", in.OrderID, actor.ID)
		if e != nil {
			return empty, e
		}
		n, _ = lock.RowsAffected()
		if n != 1 {
			return empty, commerceFail(409, "订单不存在、已失效或正在处理")
		}
		var b []byte
		if e = tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", in.OrderID).Scan(&b); e != nil {
			return empty, e
		}
		if e = json.Unmarshal(b, &order); e != nil {
			return empty, e
		}
		if order.Expires <= now || order.Offer.Price <= 0 {
			return empty, commerceFail(409, "订单已过期或无需在线支付")
		}
		if order.PaymentAttemptID != "" {
			old, err := scanPaymentAttempt(tx.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE id=?", order.PaymentAttemptID))
			if err != nil {
				return empty, err
			}
			if old.ProviderID != p.ID {
				return empty, commerceFail(409, "订单已选择其他支付方式，请取消后重新下单")
			}
			return old, tx.Commit()
		}
		attempt.OrderID = order.ID
		attempt.Amount = order.Offer.Price
		attempt.Expires = order.Expires
	} else {
		attempt.Amount, e = moneyValue(in.Amount)
		if e != nil {
			return empty, e
		}
	}
	doc, e := a.store.vault.seal(paymentAttemptDoc{Code: p.Code, Provider: providerDocument(p), Fingerprint: digest(string(jsonBytes(input)))})
	if e != nil {
		return empty, e
	}
	result, e := tx.Exec("INSERT INTO payment_attempts(id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,checkout_url,idempotency_key,created,updated,expires,doc,purpose,account_scope) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING", attempt.ID, attempt.OrderID, attempt.UserID, attempt.ProviderID, attempt.State, attempt.Amount, attempt.Currency, attempt.MerchantRef, "", attempt.IdempotencyKey, now, now, attempt.Expires, doc, purpose, attempt.AccountScope)
	if e != nil {
		return empty, e
	}
	n, _ = result.RowsAffected()
	if n == 0 {
		return empty, commerceFail(409, "同一支付请求正在处理，请使用原操作标识重试")
	}
	if purpose == "order" {
		order.PaymentAttemptID = attempt.ID
		order.PaymentProviderID = p.ID
		order.Message = "等待在线支付"
		if e = saveOrder(tx, order, "pending"); e != nil {
			return empty, e
		}
	}
	if e = tx.Commit(); e != nil {
		return empty, e
	}
	return attempt, nil
}
func (a *App) listPayments(w http.ResponseWriter, r *http.Request, actor Record) error {
	query := "SELECT " + paymentAttemptColumns + " FROM payment_attempts"
	args := []any{}
	if actor.Role != "owner" {
		query += " WHERE user_id=?"
		args = append(args, actor.ID)
	}
	query += " ORDER BY created DESC,id DESC LIMIT 100"
	rows, e := a.store.db.Query(query, args...)
	if e != nil {
		return e
	}
	items := []PaymentAttempt{}
	for rows.Next() {
		v, err := scanPaymentAttempt(rows)
		if err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for i := range items {
		var state string
		err := a.store.db.QueryRow("SELECT state FROM payment_refunds WHERE receipt_id IN (SELECT id FROM payment_receipts WHERE attempt_id=?) ORDER BY created DESC LIMIT 1", items[i].ID).Scan(&state)
		if err == nil {
			items[i].RefundStatus = state
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	jsonResponse(w, 200, object{"items": items})
	return nil
}
func (a *App) confirmExternalOrder(orderID string, user Record, attemptID string) error {
	body, _ := json.Marshal(object{"id": orderID, "action": "confirm", "operation_id": randomToken(24), "payment_attempt_id": attemptID})
	request := httptest.NewRequest(http.MethodPost, "/api/commerce/orders/action", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := a.orderAction(w, request, user); err != nil {
		return err
	}
	if w.Code < 200 || w.Code >= 300 {
		return errors.New("payment was verified but order activation is pending")
	}
	return nil
}

// Payment gateways have bounded but external latency. They must never hold the
// panel-wide mutex while other users read state or a webhook is being received.
func (a *App) paymentAPIRoute(w http.ResponseWriter, r *http.Request, actor Record) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/commerce/")
	handlers := map[string]func(http.ResponseWriter, *http.Request, Record) error{"payment-methods": a.savePaymentMethod, "payment-methods/delete": a.deletePaymentMethod, "payment-methods/check": a.checkPaymentMethod, "orders/pay": a.createPayment, "wallet/topup": a.createTopup, "payment-refunds": a.paymentRefundAction}
	handler, known := handlers[path]
	if !known && path != "payments" && path != "payment-receipts" {
		return false
	}
	a.mu.Lock()
	current, err := a.commerceActor(actor, false, "")
	integrityErr := a.store.commerceErr
	a.mu.Unlock()
	if err != nil {
		commerceWriteError(w, err)
		return true
	}
	if r.Method == "GET" {
		switch path {
		case "payment-methods":
			err = a.listPaymentMethods(w, current)
		case "payments":
			err = a.listPayments(w, r, current)
		case "payment-receipts":
			err = a.listPaymentReceipts(w, r, current)
		default:
			err = commerceFail(405, "方法不支持")
		}
	} else if r.Method == "POST" && known {
		if integrityErr != nil {
			err = commerceFail(409, "资金账目校验失败，已阻止写入")
		} else {
			err = handler(w, r, current)
		}
	} else {
		err = commerceFail(405, "方法不支持")
	}
	if err != nil {
		commerceWriteError(w, err)
	}
	return true
}
