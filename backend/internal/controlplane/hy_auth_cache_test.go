package controlplane

import (
	"testing"
	"time"
)

func TestHYAuthCacheRechecksCurrentRecord(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "alice", "user")
	node := Node{ID: "hy2-main", Protocol: "hy2"}
	password := hyNodePassword(u, node)
	if _, ok := a.hyAuthenticate(password); !ok {
		t.Fatal("valid HY2 credential rejected")
	}
	built := a.hyAuthCache.table.Load()
	if built == nil || len(built.entries) == 0 {
		t.Fatal("auth table was not built")
	}
	// Disabling the user must take effect on the next handshake even though
	// the cached table still lists the credential.
	u.Enabled = false
	if err := a.store.save(&u); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.hyAuthenticate(password); ok {
		t.Fatal("disabled user authenticated from the cache")
	}
	if a.hyAuthCache.table.Load() != built && time.Since(built.built) < hyAuthRebuildInterval {
		t.Fatal("rebuild was not rate limited")
	}
	// A user added later is found once the rate-limit window has passed.
	bob := testUser(t, a, "bob", "user")
	a.hyAuthCache.table.Store(&hyAuthTable{built: time.Now().Add(-2 * hyAuthRebuildInterval), entries: built.entries})
	if id, ok := a.hyAuthenticate(hyNodePassword(bob, node)); !ok || id != hyNodeIdentity(bob, node) {
		t.Fatal("new user not found after rebuild")
	}
	if _, ok := a.hyAuthenticate("not-a-password"); ok {
		t.Fatal("unknown password accepted")
	}
}
