package controlplane

import (
	"fmt"
	"testing"
	"time"
)

// Users whose periods end together (the 1st of the month) are reset in one
// pass that shares a single reconcile, not one reconcile per user.
func TestQuotaPeriodsResetManyUsersInOnePass(t *testing.T) {
	a := testApp(t)
	now := time.Now().Unix()
	ids := []int64{}
	for i := range 40 {
		u := testUser(t, a, fmt.Sprintf("member%02d", i), "user")
		u.InitMeter("period-old", now-30*86400)
		u.Meter.End = now - 1
		if err := a.store.save(&u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	revisions := func() int {
		var desired int
		_ = a.store.db.QueryRow("SELECT COALESCE(MAX(desired),0) FROM core_revisions").Scan(&desired)
		return desired
	}
	before := revisions()
	if err := a.advanceQuotaPeriods(now); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		u, err := a.store.record(id)
		if err != nil {
			t.Fatal(err)
		}
		if u.Meter == nil || u.Meter.PendingReset || u.Meter.PeriodID == "period-old" {
			t.Fatalf("user %d not reset: %+v", id, u.Meter)
		}
		var archived int
		_ = a.store.db.QueryRow("SELECT COUNT(*) FROM quota_periods WHERE user_id=? AND period_id='period-old'", id).Scan(&archived)
		if archived != 1 {
			t.Fatalf("user %d old period not archived", id)
		}
	}
	if changed := revisions() - before; changed > 1 {
		t.Fatalf("reset applied %d core revisions; want at most 1", changed)
	}
}
