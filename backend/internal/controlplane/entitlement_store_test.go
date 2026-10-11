package controlplane

import "testing"

func TestSameNodeRatesDetectsZeroRateKeyMove(t *testing.T) {
	oldRates := map[string]int64{"legacy-private/node-a": 0}
	newRates := map[string]int64{"legacy-private/node-b": 0}
	if sameNodeRates(oldRates, newRates) {
		t.Fatalf("different zero-rate node keys were treated as equal: old=%v new=%v", oldRates, newRates)
	}
}
