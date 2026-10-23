package controlplane

import (
	"testing"
	"time"
)

// Subscription clients must not queue behind core reconciliation, which holds
// the control lock for seconds while restarting cores.
func TestSubscriptionServedWhileControlLockHeld(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	a.mu.Lock()
	defer a.mu.Unlock()
	done := make(chan int, 1)
	go func() { done <- req(t, a, Record{}, "GET", "/sub/"+u.Credentials.Token, nil).Code }()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("subscription status %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscription blocked on the control lock")
	}
}
