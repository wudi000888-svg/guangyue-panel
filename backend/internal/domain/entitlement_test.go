package domain

import "testing"

func TestWeightedBytesPartitionInvariant(t *testing.T) {
	for _, rate := range []int64{0, 500, 1000, 1500, 2000, 99990, 100000} {
		want, rem, err := WeightedBytes(12345, rate, 0)
		if err != nil {
			t.Fatal(err)
		}
		var got, residue int64
		for i := 0; i < 12345; i++ {
			cost, next, e := WeightedBytes(1, rate, residue)
			if e != nil {
				t.Fatal(e)
			}
			got += cost
			residue = next
		}
		if got != want || rem != residue {
			t.Fatalf("rate=%d partition changed quota: %d/%d %d/%d", rate, got, want, residue, rem)
		}
	}
	if _, _, err := WeightedBytes(CounterLimit, 100000, 0); err == nil {
		t.Fatal("overflow accepted")
	}
	if got, _, err := WeightedBytes(CounterLimit, 1000, 0); err != nil || got != CounterLimit {
		t.Fatal("valid large counter rejected")
	}
}
func TestQuotaUsesWeightedPeriodAndValidity(t *testing.T) {
	u := User{Enabled: true, Quota: 10, Upload: 100}
	u.InitMeter("one", 1)
	u.Meter.Upload = 5
	if !u.Active() {
		t.Fatal("raw traffic depleted weighted quota")
	}
	u.Meter.PendingReset = true
	if u.Active() {
		t.Fatal("pending reset still authorized")
	}
	u.Meter.PendingReset = false
	u.Meter.Upload = 10
	if u.Active() {
		t.Fatal("weighted quota bypass")
	}
}
