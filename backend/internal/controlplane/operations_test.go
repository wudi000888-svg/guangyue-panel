package controlplane

import (
	"context"
	"testing"
	"time"
)

func TestConfigurationRevisionChangesWithAuthorization(t *testing.T) {
	a := testApp(t)
	u := testUser(t, a, "revision", "user")
	if err := a.reconcile(); err != nil {
		t.Fatal(err)
	}
	// Credential provisioning is included before recording the stable baseline.
	if err := a.reconcile(); err != nil {
		t.Fatal(err)
	}
	first := a.store.coreRevision()
	if first.State != "applied" || first.Desired != first.Applied {
		t.Fatal("revision not applied")
	}
	u.Upload = 1
	if err := a.store.save(&u); err != nil {
		t.Fatal(err)
	}
	if err := a.reconcileIfNeeded(); err != nil {
		t.Fatal(err)
	}
	if got := a.store.coreRevision(); got.Desired != first.Desired {
		t.Fatal("usage counters changed config")
	}
	u.Quota = 1
	if err := a.store.save(&u); err != nil {
		t.Fatal(err)
	}
	if err := a.reconcileIfNeeded(); err != nil {
		t.Fatal(err)
	}
	if got := a.store.coreRevision(); got.Desired <= first.Desired || got.Applied != got.Desired {
		t.Fatal("quota revocation did not apply")
	}
	u.Quota = 0
	u.Expires = time.Now().Unix() - 1
	if err := a.store.save(&u); err != nil {
		t.Fatal(err)
	}
	before := a.store.coreRevision()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.publicContext = ctx
	if err := a.reconcile(); err == nil {
		t.Fatal("cancelled controller applied config")
	}
	if got := a.store.coreRevision(); got != before {
		t.Fatal("lost controller changed revision")
	}
}
