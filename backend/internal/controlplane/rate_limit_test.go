package controlplane

import (
	"fmt"
	"testing"
)

func TestRateLimitFullTableDoesNotLockEveryoneOut(t *testing.T) {
	a := testApp(t)
	for i := range 4096 {
		if !a.rateAllowed(fmt.Sprintf("198.51.%d.%d", i/256, i%256)) {
			t.Fatal("distinct source refused while filling the table")
		}
	}
	if !a.rateAllowed("203.0.113.7") {
		t.Fatal("a full table locked out a new legitimate source")
	}
	if len(a.limits) > 4096 {
		t.Fatalf("table grew to %d", len(a.limits))
	}
}

func TestRateLimitGroupsIPv6By64AndStillLimits(t *testing.T) {
	a := testApp(t)
	for i := range 12 {
		if !a.rateAllowed(fmt.Sprintf("register:2001:db8:1:2::%x", i+1)) {
			t.Fatal("attempt refused before the limit")
		}
	}
	if a.rateAllowed("register:2001:db8:1:2:ffff::9") {
		t.Fatal("rotating addresses inside one /64 bypassed the limit")
	}
	if !a.rateAllowed("register:2001:db8:1:3::1") {
		t.Fatal("a different /64 was limited")
	}
	for key, want := range map[string]string{"10.0.0.1": "10.0.0.1", "login:::ffff:10.0.0.1": "login:10.0.0.1", "2001:db8::1": "2001:db8::/64", "email:x": "email:x"} {
		if got := rateLimitKey(key); got != want {
			t.Errorf("rateLimitKey(%q)=%q want %q", key, got, want)
		}
	}
}
