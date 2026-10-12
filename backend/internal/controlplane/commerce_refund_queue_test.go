package controlplane

import (
	"testing"
	"time"
)

func refundQueueOffer(t *testing.T, a *App, owner Record, name string) Offer {
	t.Helper()
	p := Plan{
		Name:      name,
		Quota:     1 << 30,
		ValidDays: 30,
		Cycle:     "30d",
		Timezone:  "UTC",
		GroupIDs:  []string{legacyPrivateGroup},
		VLESS:     true,
		HY2:       true,
	}
	p = decoded[Plan](t, req(t, a, owner, "POST", "/api/plans", p), 200)
	return decoded[Offer](t, req(t, a, owner, "POST", "/api/commerce/offers", object{
		"plan_id": p.ID, "plan_version": p.Version, "price": "1000", "enabled": true,
	}), 200)
}

func completeRefundQueueOrder(t *testing.T, a *App, user Record, offer Offer) Order {
	t.Helper()
	o := newOrder(t, a, user, offer)
	orderDo(t, a, user, o, "confirm")
	if err := a.commerceWork(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	completed, err := a.store.order(o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "completed" {
		t.Fatalf("order did not complete: %+v", completed)
	}
	return completed
}

func TestRefundAllowsLaterPlanPolicyRevision(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	offer := commerceOffer(t, a, owner)
	fundWallet(t, a, owner, user, "3000")
	order := completeRefundQueueOrder(t, a, user, offer)

	// Editing a live package policy intentionally changes the authorization
	// revision, but must not make the paid order permanently non-refundable.
	u, err := a.store.record(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	u.Entitlement.Revision = randomToken(12)
	if err = a.store.save(&u); err != nil {
		t.Fatal(err)
	}

	requested := decoded[Order](t, req(t, a, user, "POST", "/api/commerce/orders/action", object{
		"id": order.ID, "action": "request_refund", "reason": "套餐策略已调整", "operation_id": randomToken(24),
	}), 200)
	if requested.RefundRequest == nil {
		t.Fatal("refund request was not created after policy revision")
	}
	approved := orderDo(t, a, owner, requested, "refund")
	if approved.State != "refunding" {
		t.Fatalf("refund approval did not enter processing: %s", approved.State)
	}
	if err = a.commerceWork(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	refunded, err := a.store.order(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refunded.State != "refunded" {
		t.Fatalf("refund did not complete after policy revision: %s", refunded.State)
	}
	wallet, err := a.store.wallet(user.ID)
	if err != nil || wallet.Available != 3000 || wallet.Held != 0 {
		t.Fatalf("refund did not restore wallet: %+v %v", wallet, err)
	}
}

func TestRefundQueuedMiddlePlanPreservesQueueOrder(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	aOffer := refundQueueOffer(t, a, owner, "Queue A")
	bOffer := refundQueueOffer(t, a, owner, "Queue B")
	cOffer := refundQueueOffer(t, a, owner, "Queue C")
	fundWallet(t, a, owner, user, "5000")
	aOrder := completeRefundQueueOrder(t, a, user, aOffer)
	bOrder := completeRefundQueueOrder(t, a, user, bOffer)
	cOrder := completeRefundQueueOrder(t, a, user, cOffer)

	active, err := a.store.record(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Entitlement == nil || active.Entitlement.OrderID != cOrder.ID || len(active.PlanQueue) != 2 {
		t.Fatalf("orders were not linked to active and queued entitlements: %+v", active)
	}
	if active.PlanQueue[0].OrderID != aOrder.ID || active.PlanQueue[1].OrderID != bOrder.ID {
		t.Fatalf("queue order was not preserved: %+v", active.PlanQueue)
	}

	requested := decoded[Order](t, req(t, a, user, "POST", "/api/commerce/orders/action", object{
		"id": bOrder.ID, "action": "request_refund", "reason": "退回中间套餐", "operation_id": randomToken(24),
	}), 200)
	approved := orderDo(t, a, owner, requested, "refund")
	if approved.State != "refunded" {
		t.Fatalf("queued refund approval did not complete wallet refund: %s", approved.State)
	}
	if err = a.commerceWork(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	refunded, err := a.store.order(bOrder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refunded.State != "refunded" {
		t.Fatalf("queued refund did not complete: %s", refunded.State)
	}
	active, err = a.store.record(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Entitlement == nil || active.Entitlement.OrderID != cOrder.ID || len(active.PlanQueue) != 1 || active.PlanQueue[0].OrderID != aOrder.ID {
		t.Fatalf("refunding middle queue item changed active or remaining queue: %+v", active)
	}
	wallet, err := a.store.wallet(user.ID)
	if err != nil || wallet.Available != 3000 || wallet.Held != 0 {
		t.Fatalf("queued refund did not return exactly one payment: %+v %v", wallet, err)
	}
}

func TestRefundActivePlanRestoresRemainingQueuedEntitlement(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	aOffer := refundQueueOffer(t, a, owner, "Remaining A")
	bOffer := refundQueueOffer(t, a, owner, "Remaining B")
	fundWallet(t, a, owner, user, "5000")
	aOrder := completeRefundQueueOrder(t, a, user, aOffer)
	before, err := a.store.record(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Leave a measurable amount of A's lifetime when B is purchased.
	before.Expires = time.Now().Unix() + 3600
	if err = a.store.save(&before); err != nil {
		t.Fatal(err)
	}
	bOrder := completeRefundQueueOrder(t, a, user, bOffer)
	requested := decoded[Order](t, req(t, a, user, "POST", "/api/commerce/orders/action", object{
		"id": bOrder.ID, "action": "request_refund", "operation_id": randomToken(24),
	}), 200)
	orderDo(t, a, owner, requested, "refund")
	if err = a.commerceWork(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	// The refund first revokes B. The normal cycle worker then restores A,
	// preserving the remaining lifetime captured when B was purchased.
	now := time.Now().Unix()
	if err = a.advanceQuotaPeriods(now); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceQuotaPeriods(now + 1); err != nil {
		t.Fatal(err)
	}
	restored, err := a.store.record(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Entitlement == nil || restored.Entitlement.OrderID != aOrder.ID || len(restored.PlanQueue) != 0 || restored.Expires <= now {
		t.Fatalf("remaining queued entitlement was not restored: %+v", restored)
	}
}

func TestRefundReturnsOnlyUnusedActiveQuota(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	offer := refundQueueOffer(t, a, owner, "Partial refund")
	fundWallet(t, a, owner, user, "5000")
	order := completeRefundQueueOrder(t, a, user, offer)
	current, err := a.store.record(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Meter.Upload = current.Meter.BaseUpload + current.Quota/2
	if err = a.store.save(&current); err != nil {
		t.Fatal(err)
	}
	requested := decoded[Order](t, req(t, a, user, "POST", "/api/commerce/orders/action", object{
		"id": order.ID, "action": "request_refund", "operation_id": randomToken(24),
	}), 200)
	orderDo(t, a, owner, requested, "refund")
	if err = a.commerceWork(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	wallet, err := a.store.wallet(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Available != 4500 {
		t.Fatalf("refund returned consumed quota: wallet=%+v", wallet)
	}
}
