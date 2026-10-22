package controlplane

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) orderAction(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		ID               string `json:"id"`
		Action           string `json:"action"`
		OperationID      string `json:"operation_id"`
		Password         string `json:"password"`
		PaymentAttemptID string `json:"payment_attempt_id"`
		Reason           string `json:"reason"`
		RefundRequestID  string `json:"refund_request_id"`
		RefundRevision   int64  `json:"refund_request_revision"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	// Normalize user supplied text before calculating the idempotency
	// fingerprint.  Retrying the same operation with surrounding whitespace
	// must replay the original response instead of being treated as a new
	// command.
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Action == "refund" || in.Action == "reject_refund" {
		if _, e := a.commerceActor(actor, true, in.Password); e != nil {
			return e
		}
	}
	in.Password = ""
	if b, e := a.store.commerceReplay(actor.ID, in.OperationID, in); e != nil {
		return e
	} else if b != nil {
		jsonResponse(w, 200, json.RawMessage(b))
		return nil
	}
	o, e := a.store.order(in.ID)
	if e != nil {
		return commerceFail(404, "订单不存在")
	}
	if e = a.enrichOrder(&o); e != nil {
		return e
	}
	if actor.Role != "owner" && actor.ID != o.UserID {
		// Keep ordinary checkout actions opaque to other members, while refund
		// review actions explicitly report the permission failure so the member
		// UI can explain why the request was rejected.
		if in.Action == "request_refund" || in.Action == "cancel_refund" || in.Action == "reject_refund" {
			return commerceFail(403, "无权操作此订单")
		}
		return commerceFail(404, "订单不存在")
	}
	u, e := a.store.record(o.UserID)
	if e != nil {
		return e
	}
	old := o.State
	request, requestErr := a.store.refundRequest(o.ID)
	if requestErr != nil {
		return requestErr
	}
	if in.RefundRequestID != "" {
		if request == nil || request.ID != in.RefundRequestID || in.RefundRevision < 1 || request.Revision != in.RefundRevision {
			return commerceFail(409, "退款申请已变化，请刷新后重试")
		}
	}
	if request != nil && request.State == "requested" && (in.Action == "cancel_refund" || in.Action == "reject_refund" || in.Action == "refund") && (in.RefundRequestID == "" || in.RefundRevision < 1) {
		return commerceFail(409, "请提供当前退款申请编号和版本")
	}
	now := time.Now().Unix()
	deltaAvailable, deltaHeld := int64(0), int64(0)
	kind := ""
	queuedRefund := false
	queuedRefundIndex := -1
	queuedRenewalRefund := false
	switch in.Action {
	case "confirm":
		if o.PaymentAttemptID == "" {
			if e := a.requirePaymentModule(); e != nil {
				return e
			}
		}
		if actor.ID != o.UserID {
			return commerceFail(403, "订单需由所属用户确认")
		}
		offer, e := a.store.offer(o.Offer.ID)
		p, pe := a.store.plan(o.Offer.Plan.ID)
		if e != nil || !offer.Enabled || pe != nil || p.Archived {
			return commerceFail(409, "商品已暂停销售")
		}
		if offer.PurchaseLimit > 0 {
			count, ce := a.countOfferPurchases(u.ID, offer.ID, o.ID)
			if ce != nil {
				return ce
			}
			if count >= offer.PurchaseLimit {
				return commerceFail(409, "该套餐已达到你的限购次数")
			}
		}
		paidInTime := false
		if o.PaymentAttemptID != "" && in.PaymentAttemptID == o.PaymentAttemptID {
			var received int64
			if err := a.store.db.QueryRow("SELECT MIN(created) FROM payment_receipts WHERE attempt_id=? AND state IN ('verified','applied')", o.PaymentAttemptID).Scan(&received); err == nil {
				paidInTime = received > 0 && received < o.Expires
			}
		}
		if o.State != "pending" || o.Expires <= now && !paidInTime || !u.Enabled || o.Expected != userCommerceVersion(u) || u.Meter != nil && u.Meter.PendingReset {
			return commerceFail(409, "订单或权益已变化，请取消后重新下单")
		}
		// Online payments are activated only after a verified provider callback.
		// The client cannot turn an unpaid online order into a balance order by
		// calling this endpoint directly.
		onlinePayment := o.PaymentAttemptID != ""
		if onlinePayment {
			if strings.TrimSpace(in.PaymentAttemptID) != o.PaymentAttemptID {
				return commerceFail(409, "请先完成在线支付")
			}
			var paymentState string
			if e = a.store.db.QueryRow("SELECT state FROM payment_attempts WHERE id=? AND order_id=?", o.PaymentAttemptID, o.ID).Scan(&paymentState); e != nil || paymentState != "paid" {
				return commerceFail(409, "请先完成在线支付")
			}
		} else if strings.TrimSpace(in.PaymentAttemptID) != "" {
			return commerceFail(409, "订单支付方式无效")
		}
		future := cloneCommerceUser(u)
		assignPlan(&future, o.Offer.Plan, now, o.ID)
		if o.Action == "renew" {
			future = u
			future.Expires += int64(o.Offer.Plan.ValidDays) * 86400
		}
		if e = a.validateEntitlementSites(future, "reset"); e != nil {
			return e
		}
		u.InitMeter("legacy-"+businessUsageKey(u.ID), now)
		o.PreviousEnd = u.Meter.End
		if o.Action == "purchase" {
			u.Meter.PendingReset = true
		}
		o.Started = now
		o.State = "provisioning"
		if onlinePayment {
			o.Message = "在线支付已确认，正在开通"
		} else {
			o.Message = "余额已冻结，正在开通"
			deltaAvailable, deltaHeld, kind = -o.Offer.Price, o.Offer.Price, "hold"
		}
	case "cancel":
		if old != "pending" && old != "provisioning" {
			return commerceFail(409, "订单无法取消")
		}
		o.State = "cancelled"
		o.Message = "订单已取消"
		if old == "provisioning" {
			if o.PaymentAttemptID == "" {
				deltaAvailable, deltaHeld, kind = o.Offer.Price, -o.Offer.Price, "release"
			}
			if o.Action == "purchase" && u.Meter != nil {
				u.Meter.PendingReset = false
			}
		}
	case "request_refund":
		if actor.ID != o.UserID {
			return commerceFail(403, "退款申请需由所属用户提交")
		}
		if len([]rune(in.Reason)) > 500 {
			return commerceFail(400, "退款申请理由不能超过 500 个字符")
		}
		if old != "completed" {
			return commerceFail(409, "当前订单状态不可申请退款")
		}
		if _, _, found := orderEntitlementPosition(u, o.ID); !found {
			return commerceFail(409, "订单权益已被替换，请联系管理员核对后退款")
		}
		if request != nil && request.State == "requested" {
			return commerceFail(409, "该订单已有待处理退款申请")
		}
		o.State = "completed"
		o.Message = "退款申请已提交，等待管理员审核"
		o.RefundStatus = "requested"
		o.RefundRequestID = serial("GYRR")
		o.RefundRequest = &RefundRequest{ID: o.RefundRequestID, OrderID: o.ID, UserID: o.UserID, State: "requested", Revision: 1, Reason: in.Reason, ActorID: actor.ID, Created: now, Updated: now}
	case "cancel_refund":
		if actor.ID != o.UserID {
			return commerceFail(403, "退款申请需由所属用户取消")
		}
		if len([]rune(in.Reason)) > 500 {
			return commerceFail(400, "退款取消理由不能超过 500 个字符")
		}
		if old != "completed" || request == nil || request.State != "requested" {
			return commerceFail(409, "当前没有待处理的退款申请")
		}
		o.State = "completed"
		o.Message = "退款申请已取消"
		o.RefundStatus = "cancelled"
		updatedRequest := *request
		o.RefundRequest = &updatedRequest
		o.RefundRequest.State = "cancelled"
		o.RefundRequest.Revision++
	case "reject_refund":
		if actor.Role != "owner" {
			return commerceFail(403, "需要管理员权限")
		}
		if old != "completed" || request == nil || request.State != "requested" {
			return commerceFail(409, "当前没有待处理的退款申请")
		}
		if len([]rune(in.Reason)) > 500 {
			return commerceFail(400, "退款拒绝理由不能超过 500 个字符")
		}
		o.State = "completed"
		o.Message = "退款申请已拒绝"
		if in.Reason != "" {
			o.Message += "：" + in.Reason
		}
		o.RefundStatus = "rejected"
		updatedRequest := *request
		o.RefundRequest = &updatedRequest
		o.RefundRequest.State = "rejected"
		o.RefundRequest.Reason = in.Reason
		o.RefundRequest.Revision++
	case "refund":
		if old != "completed" && old != "refund_requested" {
			return commerceFail(409, "当前订单状态不可退款")
		}
		activeEntitlement, queuedIndex, found := orderEntitlementPosition(u, o.ID)
		if !found {
			return commerceFail(409, "订单权益已被替换，请联系管理员核对后退款")
		}
		if pending, e := a.store.commercePendingExcept(u.ID, o.ID); e != nil {
			return e
		} else if pending {
			return commerceFail(409, "请先处理用户的未完成订单")
		}
		o.RefundAmount = refundableOrderAmount(o, u, now)
		o.RefundAmountSet = true
		if !activeEntitlement {
			queuedRefund = true
			queuedRefundIndex = queuedIndex
			queuedRenewalRefund = o.Action == "renew" && queuedIndex >= 0 && queuedIndex < len(u.PlanQueue) && u.PlanQueue[queuedIndex].RenewalOrderID == o.ID
			o.State = "refunded"
			o.Message = "已移除排队权益并退回站内余额"
			if o.PaymentAttemptID != "" {
				o.State = "refund_pending"
				o.Message = "已移除排队权益，等待管理员执行原路退款"
			}
		} else {
			u.InitMeter("legacy-"+businessUsageKey(u.ID), now)
			u.Meter.PendingReset = true
			o.Started = now
			o.NextAttempt = 0
			o.State = "refunding"
			o.Message = "等待订单权益撤回后退回余额"
			if o.PaymentAttemptID != "" {
				o.Message = "等待订单权益撤回后原路退款"
			}
		}
		if request != nil && request.State == "requested" {
			o.RefundStatus = "approved"
			updatedRequest := *request
			o.RefundRequest = &updatedRequest
			o.RefundRequest.State = "approved"
			o.RefundRequest.Revision++
		}
	default:
		return commerceFail(400, "订单操作无效")
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if in.Action == "confirm" && o.PaymentProviderID == "crypto" {
		if e = cryptoLockOrderPayment(tx, o); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", o.ID); e != nil {
		return e
	}
	var lockedDoc []byte
	var lockedOrder Order
	if e = tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", o.ID).Scan(&lockedDoc); e != nil {
		return e
	}
	if e = json.Unmarshal(lockedDoc, &lockedOrder); e != nil {
		return e
	}
	if lockedOrder.State == "refund_requested" {
		lockedOrder.State = "completed"
	}
	if lockedOrder.State != old || lockedOrder.PaymentAttemptID != o.PaymentAttemptID || lockedOrder.PaymentProviderID != o.PaymentProviderID {
		return errCommerceConflict
	}
	if in.Action == "confirm" && o.PaymentAttemptID != "" {
		locked, err := tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=? AND state='paid'", o.PaymentAttemptID)
		if err != nil {
			return err
		}
		n, err := locked.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return commerceFail(409, "在线付款状态已变化，请刷新订单")
		}
	}
	if in.Action == "cancel" && o.PaymentAttemptID != "" {
		if _, e = tx.Exec("UPDATE payment_attempts SET state=CASE WHEN state='paid' THEN 'refund_required' ELSE 'cancelled' END,updated=? WHERE id=? AND state IN ('created','awaiting_customer','paid')", now, o.PaymentAttemptID); e != nil {
			return e
		}
		if e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, "订单已取消，需原路退款"); e != nil {
			return e
		}
		if old == "provisioning" {
			o.Message = "订单已取消；在线支付待人工退款"
		}
	}
	if queuedRefund {
		if queuedRenewalRefund {
			slot := &u.PlanQueue[queuedRefundIndex]
			slot.Expires = o.BeforeExpiry
			slot.RenewalOrderID = ""
			if slot.Entitlement != nil {
				slot.Entitlement.RenewalOrderID = ""
			}
		} else if !removeQueuedEntitlement(&u, queuedRefundIndex) {
			return commerceFail(409, "排队权益已变化，请刷新后重试")
		}
		if o.PaymentAttemptID != "" {
			if _, e = tx.Exec("UPDATE payment_attempts SET state='refund_required',updated=?,message=? WHERE id=? AND state IN ('completed','paid')", now, o.Message, o.PaymentAttemptID); e != nil {
				return e
			}
			if e = paymentMarkReceiptRefund(tx, o.PaymentAttemptID, o.Message); e != nil {
				return e
			}
		} else if o.Offer.Price > 0 {
			if _, e = a.store.postMoney(tx, u.ID, actor.ID, "refund:"+o.ID, "refund", o.ID, o.Message, o.RefundAmount, 0); e != nil {
				return e
			}
		}
	}
	if in.Action == "request_refund" {
		_, e = tx.Exec("INSERT INTO commerce_refund_requests(id,order_id,user_id,state,revision,reason,actor_id,created,updated) VALUES(?,?,?,?,?,?,?,?,?)", o.RefundRequestID, o.ID, o.UserID, "requested", 1, in.Reason, actor.ID, now, now)
	} else if (in.Action == "cancel_refund" || in.Action == "reject_refund" || in.Action == "refund") && request != nil && request.State == "requested" {
		newState := "approved"
		if in.Action == "cancel_refund" {
			newState = "cancelled"
		} else if in.Action == "reject_refund" {
			newState = "rejected"
		}
		result, updateErr := tx.Exec("UPDATE commerce_refund_requests SET state=?,reason=?,revision=revision+1,updated=?,decided_by=?,decided_at=? WHERE id=? AND state='requested' AND revision=?", newState, in.Reason, now, actor.ID, now, request.ID, request.Revision)
		e = updateErr
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				e = commerceFail(409, "退款申请已变化，请刷新后重试")
			}
		}
	}
	if e != nil {
		return e
	}
	if kind != "" && o.Offer.Price > 0 {
		if _, e = a.store.postMoney(tx, u.ID, actor.ID, kind+":"+o.ID, kind, o.ID, o.Message, deltaAvailable, deltaHeld); e != nil {
			return e
		}
	}
	if e = saveOrder(tx, o, old); e == nil {
		e = txUser(tx, u.User)
	}
	if e == nil {
		switch in.Action {
		case "request_refund":
			e = a.notifyCommerceOwners(tx, o.ID+" 用户 #"+strconv.FormatInt(o.UserID, 10)+" 已提交退款申请，请在订单中心审核。", now)
		case "cancel_refund":
			// Keep administrators informed when a pending review disappears.
			e = a.notifyCommerceOwnersTitle(tx, "退款申请已取消", o.ID+" 用户 #"+strconv.FormatInt(o.UserID, 10)+" 已取消退款申请。", now)
		case "reject_refund":
			message := "订单 " + o.ID + " 的退款申请已拒绝。"
			if in.Reason != "" {
				message += " 原因：" + in.Reason
			}
			// The member must be told about a decision even when they are not
			// looking at the order page.  Delivery is handled by the existing
			// commerce event worker after this transaction commits.
			e = a.store.commerceEvent(tx, u.ID, "order:"+o.ID+":refund_rejected:"+in.OperationID, message)
		}
	}
	if e == nil {
		e = a.store.saveCommerceRequest(tx, actor.ID, in.OperationID, in, o)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return e
	}
	if in.Action == "request_refund" || in.Action == "cancel_refund" || in.Action == "reject_refund" || in.Action == "refund" {
		target := o.ID
		if in.Action == "reject_refund" && in.Reason != "" {
			target += " reason=" + in.Reason
		}
		a.store.audit(actor.Username, "order-"+in.Action, target)
	}
	a.status = "pending"
	jsonResponse(w, 200, o)
	return nil
}
