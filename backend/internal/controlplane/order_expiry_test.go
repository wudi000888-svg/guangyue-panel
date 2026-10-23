package controlplane

import (
	"errors"
	"testing"
	"time"
)

// If the order moved on concurrently, expiring it must not expire its
// payment attempt either: both change together or not at all.
func TestOrderExpiryIsAtomicWithItsAttempt(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	p := paymentTestMethod(t, a, owner, "alipay")
	at := paymentTestTopup(t, a, user, p, "100")
	now := time.Now().Unix()
	o := Order{ID: "GYOexpiry", UserID: user.ID, State: "pending", Expires: now - 1, PaymentAttemptID: at.ID}
	if _, e := a.store.db.Exec("INSERT INTO commerce_orders(id,user_id,state,created,updated,expires,doc) VALUES(?,?,?,?,?,?,?)", o.ID, o.UserID, "provisioning", now, now, o.Expires, jsonBytes(o)); e != nil {
		t.Fatal(e)
	}
	// o still says pending, but the stored order is already provisioning.
	if e := a.expirePendingOrder(o, now); !errors.Is(e, errCommerceConflict) {
		t.Fatalf("stale expiry was not rejected: %v", e)
	}
	attempt, e := a.store.paymentAttempt(at.ID)
	if e != nil {
		t.Fatal(e)
	}
	if attempt.State == "expired" {
		t.Fatal("attempt expired although the order did not")
	}
	if _, e = a.store.db.Exec("UPDATE commerce_orders SET state='pending' WHERE id=?", o.ID); e != nil {
		t.Fatal(e)
	}
	if e = a.expirePendingOrder(o, now); e != nil {
		t.Fatal(e)
	}
	if attempt, _ = a.store.paymentAttempt(at.ID); attempt.State != "expired" {
		t.Fatal("attempt not expired with its order", attempt.State)
	}
}
