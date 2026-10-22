package controlplane

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

func (a *App) commerceWork(now int64) error {
	if a.store.commerceErr != nil {
		return a.store.commerceErr
	}
	if a.cfg.businessAgent() {
		return nil
	}
	rows, e := a.store.db.Query("SELECT doc FROM commerce_orders WHERE state IN ('pending','provisioning','refunding') ORDER BY created LIMIT 100")
	if e != nil {
		return e
	}
	orders := []Order{}
	for rows.Next() {
		var b []byte
		var o Order
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(b, &o); e != nil {
			rows.Close()
			return e
		}
		orders = append(orders, o)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	var problems []error
	for _, o := range orders {
		if o.NextAttempt > now {
			continue
		}
		if o.State == "pending" {
			// Crypto eligibility follows finalized block time, not RPC discovery time.
			if o.PaymentProviderID == "crypto" {
				continue
			}
			if o.Expires <= now {
				if o.PaymentAttemptID != "" {
					var state string
					if err := a.store.db.QueryRow("SELECT state FROM payment_attempts WHERE id=?", o.PaymentAttemptID).Scan(&state); err != nil {
						problems = append(problems, err)
						continue
					} else if state == "paid" {
						continue
					}
				}
				o.State = "expired"
				o.Message = "订单确认已超时"
				if o.PaymentAttemptID != "" {
					if _, e = a.store.db.Exec("UPDATE payment_attempts SET state='expired',updated=? WHERE id=? AND state IN ('created','awaiting_customer')", now, o.PaymentAttemptID); e != nil {
						problems = append(problems, e)
						continue
					}
				}
				if e = a.persistOrder(o, "pending"); e != nil {
					problems = append(problems, e)
				}
			}
			continue
		}
		if o.State == "provisioning" && o.PaymentProviderID == "crypto" {
			invoice, err := a.store.scanCryptoInvoice(a.store.db.QueryRow("SELECT "+cryptoInvoiceColumns+" FROM crypto_invoices WHERE attempt_id=?", o.PaymentAttemptID))
			if err == nil {
				err = a.store.cryptoChainAvailable(a.store.db, invoice.ChainID)
			}
			if err != nil {
				problems = append(problems, err)
				continue
			}
		}
		if o.State == "provisioning" && now-o.Started >= 1800 {
			e = a.failProvisioning(o, "开通超时，冻结余额已退回")
		} else {
			e = a.fulfilOrder(o, now)
		}
		if e != nil {
			problems = append(problems, e)
		}
		current, readErr := a.store.order(o.ID)
		if readErr != nil {
			problems = append(problems, readErr)
			continue
		}
		if current.State == o.State {
			current.Attempts++
			current.NextAttempt = now + min(int64(60), int64(1)<<min(current.Attempts, 6))
			if e != nil {
				current.Message = "处理暂未完成，系统将自动重试"
			} else {
				current.Message = "等待旧连接与业务站权益结算"
			}
			if e = a.persistOrder(current, current.State); e != nil {
				problems = append(problems, e)
			}
		}
	}
	return errors.Join(append(problems, a.commerceNotifications(now))...)
}
func (a *App) persistOrder(o Order, old string) error {
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = saveOrder(tx, o, old); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) failProvisioning(o Order, message string) error {
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", o.ID); e != nil {
		return e
	}
	// A provider-paid order has no wallet hold to release.  Keep the paid
	// attempt visible for an operator to refund through the provider instead of
	// silently crediting the user's internal balance.
	if o.PaymentAttemptID != "" {
		if _, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=? WHERE id=? AND state IN ('paid','completed')", time.Now().Unix(), o.PaymentAttemptID); e != nil {
			return e
		}
		if e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, "订单开通失败，需原路退款"); e != nil {
			return e
		}
		o.Message = message + "；在线支付待人工退款"
	} else if o.Offer.Price > 0 {
		if _, e = a.store.postMoney(tx, u.ID, 0, "release:"+o.ID, "release", o.ID, message, o.Offer.Price, -o.Offer.Price); e != nil {
			return e
		}
	}
	if u.Meter != nil && o.Action == "purchase" {
		u.Meter.PendingReset = false
	}
	if e = txUser(tx, u.User); e != nil {
		return e
	}
	o.State = "failed"
	if o.PaymentAttemptID == "" {
		o.Message = message
	}
	if e = saveOrder(tx, o, "provisioning"); e != nil {
		return e
	}
	if e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":failed", o.ID+" "+message); e != nil {
		return e
	}
	return tx.Commit()
}

// notifyCommerceOwners creates an inbox notification for every enabled owner.
// Refund requests are intentionally visible to administrators without changing
// the user's entitlement or issuing any money movement before approval.
func (a *App) notifyCommerceOwners(tx *persistence.Tx, body string, now int64) error {
	return a.notifyCommerceOwnersTitle(tx, "退款申请待审核", body, now)
}

func (a *App) notifyCommerceOwnersTitle(tx *persistence.Tx, title, body string, now int64) error {
	var messageID int64
	if err := tx.QueryRow("INSERT INTO messages(sender_id,sender_name,title,body,category,created) VALUES(NULL,'System',?,?, 'account',?) RETURNING id", title, body, now).Scan(&messageID); err != nil {
		return err
	}
	rows, err := tx.Query("SELECT id,doc FROM users")
	if err != nil {
		return err
	}
	owners := []int64{}
	for rows.Next() {
		var id int64
		var b []byte
		var user domain.User
		if err = rows.Scan(&id, &b); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(b, &user); err != nil {
			rows.Close()
			return err
		}
		if user.Role == "owner" && user.Enabled {
			owners = append(owners, id)
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, ownerID := range owners {
		if _, err = tx.Exec("INSERT INTO message_recipients(message_id,user_id) VALUES(?,?)", messageID, ownerID); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) fulfilOrder(o Order, now int64) error {
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	sites, e := a.store.businessSites()
	if e != nil {
		return e
	}
	barrier := o.Action == "purchase" || o.State == "refunding"
	if barrier {
		for _, s := range sites {
			if _, ok := s.Issued[u.ID]; ok || s.IssuedUnlimited[u.ID] {
				return nil
			}
			for _, g := range s.SentGrants {
				if g.UserID == u.ID {
					return nil
				}
			}
		}
		if e = a.reconcile(); e != nil {
			return e
		}
		if e = a.collect(); e != nil {
			return e
		}
		if e = a.settleHYRevocations(); e != nil {
			return e
		}
		u, e = a.store.record(u.ID)
		if e != nil {
			return e
		}
	}
	if o.State == "provisioning" {
		future := cloneCommerceUser(u)
		if o.Action == "purchase" {
			assignPlan(&future, o.Offer.Plan, now)
		} else {
			future.Expires = max(now, future.Expires) + int64(o.Offer.Plan.ValidDays)*86400
		}
		policyErr := a.validateEntitlementSites(future, "reset")
		if policyErr != nil {
			return a.failProvisioning(o, "业务站权益配置已变化，冻结余额已退回")
		}
	}
	var budgets map[string]int64
	if o.State == "provisioning" && o.Action == "purchase" {
		future := cloneCommerceUser(u)
		assignPlan(&future, o.Offer.Plan, now)
		budgets, e = a.purchaseSiteBudgets(future, sites)
		if e != nil {
			return e
		}
	}
	oldState := o.State
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if oldState == "provisioning" && o.PaymentProviderID == "crypto" {
		if e = cryptoLockOrderPayment(tx, o); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", o.ID); e != nil {
		return e
	}
	var storedState, storedAttempt string
	var lockedRaw []byte
	if e = tx.QueryRow("SELECT state,doc FROM commerce_orders WHERE id=?", o.ID).Scan(&storedState, &lockedRaw); e != nil {
		return e
	}
	var currentOrder Order
	if e = json.Unmarshal(lockedRaw, &currentOrder); e != nil {
		return e
	}
	storedAttempt = currentOrder.PaymentAttemptID
	if storedState != oldState || storedAttempt != o.PaymentAttemptID {
		return errCommerceConflict
	}
	if !u.Enabled && oldState == "provisioning" {
		o.State = "failed"
		o.Message = "账号已停用，冻结余额已退回"
		if u.Meter != nil {
			u.Meter.PendingReset = false
		}
		if o.PaymentAttemptID != "" {
			_, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=? WHERE id=? AND state IN ('paid','completed')", time.Now().Unix(), o.PaymentAttemptID)
			if e == nil {
				e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, "账号已停用，需原路退款")
			}
			o.Message += "；在线支付待人工退款"
		} else if o.Offer.Price > 0 {
			_, e = a.store.postMoney(tx, u.ID, 0, "release:"+o.ID, "release", o.ID, o.Message, o.Offer.Price, -o.Offer.Price)
		}
	} else if oldState == "refunding" {
		if u.Entitlement == nil || (u.Entitlement.OrderID != o.ID && u.Entitlement.RenewalOrderID != o.ID) {
			return commerceFail(409, "退款权益来源不匹配")
		}
		u.Meter.PendingReset = false
		if o.Action == "renew" {
			u.Expires = o.BeforeExpiry
			u.Entitlement.RenewalOrderID = ""
			u.Entitlement.Revision = serial("GYV")
		} else {
			u.Expires = now
		}
		o.State = "refunded"
		o.Message = "已退回站内余额"
		if o.PaymentAttemptID != "" {
			o.State = "refund_pending"
			o.Message = "权益已撤回，等待管理员执行原路退款"
			if _, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=?,message=? WHERE id=? AND state IN ('completed','paid')", time.Now().Unix(), o.Message, o.PaymentAttemptID); e != nil {
				return e
			}
			e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, o.Message)
		} else if o.RefundAmount > 0 {
			_, e = a.store.postMoney(tx, u.ID, 0, "refund:"+o.ID, "refund", o.ID, o.Message, o.RefundAmount, 0)
		}
	} else {
		if o.Action == "renew" {
			u.Expires = max(now, u.Expires) + int64(o.Offer.Plan.ValidDays)*86400
			u.Entitlement.RenewalOrderID = o.ID
			u.Entitlement.Revision = serial("GYV")
		} else {
			oldDoc := jsonBytes(u.User)
			oldPeriod := u.Meter.PeriodID
			pauseCurrentPlan(&u, now)
			assignPlan(&u, o.Offer.Plan, now, o.ID)
			u.Entitlement.OrderID = o.ID
			m := u.Meter
			m.PeriodID = serial("GYCYCLE")
			m.Start = now
			m.End, e = domain.NextPeriod(now, o.Offer.Plan.Cycle, o.Offer.Plan.Timezone)
			m.PendingReset = false
			m.BaseUpload, m.BaseDownload = m.Upload, m.Download
			m.RawBaseUpload, m.RawBaseDownload = u.Upload, u.Download
			m.UploadRemainder, m.DownloadRemainder = 0, 0
			if e == nil {
				_, e = tx.Exec("INSERT INTO quota_periods(user_id,period_id,doc) VALUES(?,?,?) ON CONFLICT(user_id,period_id) DO NOTHING", u.ID, oldPeriod, oldDoc)
			}
			// Old issued grants have been acknowledged above. Mounts derive fresh
			// grants from the new package on their next sync; legacy sites receive
			// a bounded share. Never copy an old unlimited or oversized budget.
			for i := range sites {
				s := &sites[i]
				grants := []BusinessGrant{}
				changed := false
				for _, g := range s.Grants {
					if g.UserID != u.ID {
						grants = append(grants, g)
						continue
					}
					changed = true
					if budget, keep := budgets[s.ID]; keep && s.Mount == nil {
						g.Budget, g.Quota, g.PeriodID = budget, 0, m.PeriodID
						if budget > 0 {
							g.Quota, e = domain.AddCounter(s.Usage[u.ID].total(), budget)
							if e != nil {
								return e
							}
						}
						grants = append(grants, g)
					}
				}
				if changed {
					s.Grants, s.Revision = grants, serial("GYV")
					b, err := a.store.vault.seal(s)
					if err != nil {
						return err
					}
					if _, e = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, s.ID); e != nil {
						return e
					}
				}
			}
		}
		if e == nil {
			o.AppliedRevision = u.Entitlement.Revision
			o.State = "completed"
			o.Message = "权益已开通，业务站按同步状态生效"
			if o.PaymentAttemptID != "" {
				_, e = tx.Exec("UPDATE payment_attempts SET state='completed',message='套餐已开通',updated=? WHERE id=? AND state='paid'", time.Now().Unix(), o.PaymentAttemptID)
			} else if o.Offer.Price > 0 {
				_, e = a.store.postMoney(tx, u.ID, 0, "capture:"+o.ID, "purchase", o.ID, o.Message, 0, -o.Offer.Price)
			}
		}
	}
	if e == nil {
		e = txUser(tx, u.User)
	}
	if e == nil {
		e = saveOrder(tx, o, oldState)
	}
	if e == nil {
		e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":"+o.State, o.ID+" "+o.Message)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
func (a *App) commerceNotifications(now int64) error {
	rows, e := a.store.db.Query("SELECT id,user_id,body FROM commerce_events WHERE notified=0 ORDER BY created LIMIT 20")
	if e != nil {
		return e
	}
	events := []struct {
		ID   string
		User int64
		Body string
	}{}
	for rows.Next() {
		var v struct {
			ID   string
			User int64
			Body string
		}
		if e = rows.Scan(&v.ID, &v.User, &v.Body); e != nil {
			rows.Close()
			return e
		}
		events = append(events, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, v := range events {
		tx, e := a.store.db.Begin()
		if e != nil {
			return e
		}
		var id int64
		e = tx.QueryRow("INSERT INTO messages(sender_id,sender_name,title,body,category,created) VALUES(NULL,'System',?,?, 'account',?) RETURNING id", "账户与服务通知", v.Body, now).Scan(&id)
		if e == nil {
			_, e = tx.Exec("INSERT INTO message_recipients(message_id,user_id) SELECT ?,id FROM users WHERE id=?", id, v.User)
		}
		if e == nil {
			_, e = tx.Exec("UPDATE commerce_events SET notified=1 WHERE id=? AND notified=0", v.ID)
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			return e
		}
	}
	if e = a.supportCleanup(now); e != nil {
		return e
	}
	_, e = a.store.db.Exec("DELETE FROM redeem_attempts WHERE bucket<?", now/600-144)
	return e
}

func cloneCommerceUser(u Record) Record {
	if u.Meter != nil {
		m := *u.Meter
		u.Meter = &m
	}
	u.Entitlement = cloneEntitlement(u.Entitlement)
	if len(u.PlanQueue) > 0 {
		u.PlanQueue = append([]domain.PlanSlot{}, u.PlanQueue...)
		for i := range u.PlanQueue {
			u.PlanQueue[i].Entitlement = cloneEntitlement(u.PlanQueue[i].Entitlement)
			u.PlanQueue[i].Meter = cloneMeter(u.PlanQueue[i].Meter)
		}
	}
	return u
}

// Compute fresh shares only after old remote authorizations have settled.
// Selling a plan is independent of reservations from the user's previous plan.
func (a *App) purchaseSiteBudgets(u Record, sites []BusinessSite) (map[string]int64, error) {
	eligible := map[string]bool{}
	shares := int64(0)
	allows := func(nodes []Node) bool {
		for _, n := range nodes {
			if n.Enabled && nodeGroupAllowed(u, n) && (n.Protocol == "vless" && u.VLESS || n.Protocol == "hy2" && u.HY2) {
				return true
			}
		}
		return false
	}
	local, err := a.store.nodes()
	if err != nil {
		return nil, err
	}
	if allows(local) {
		shares++
	}
	for _, s := range sites {
		if !s.Enabled || s.Removed || !a.businessOwnerEnabled(s) {
			continue
		}
		assigned := s.Mount != nil
		for _, g := range s.Grants {
			assigned = assigned || g.UserID == u.ID
		}
		if !assigned {
			continue
		}
		nodes, err := a.materializeBusinessNodes(s)
		if err != nil {
			return nil, err
		}
		if allows(nodes) {
			eligible[s.ID] = true
			shares++
		}
	}
	out := map[string]int64{}
	remaining := u.Quota
	for _, s := range sites {
		if !eligible[s.ID] || s.Mount != nil {
			continue
		}
		if u.Quota == 0 {
			out[s.ID] = 0
			continue
		}
		if remaining == 0 {
			continue
		}
		budget := min(remaining, max(int64(1), u.Quota/max(int64(1), shares)))
		out[s.ID] = budget
		remaining -= budget
	}
	return out, nil
}

func cryptoLockOrderPayment(tx *persistence.Tx, o Order) error {
	var chainID int64
	if e := tx.QueryRow("SELECT chain_id FROM crypto_invoices WHERE attempt_id=? AND order_id=?", o.PaymentAttemptID, o.ID).Scan(&chainID); e != nil {
		return e
	}
	return cryptoLockChain(tx, chainID)
}
