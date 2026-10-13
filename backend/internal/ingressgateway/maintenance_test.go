package ingressgateway

import "testing"

func TestMaintenanceExposesOnlyVersionHealth(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allow        bool
	}{
		{"GET", "/api/health", true}, {"POST", "/api/health", false},
		{"POST", "/api/orders", false}, {"GET", "/api/subscription", false}, {"GET", "/api/health/", false},
	} {
		if allowDuringMaintenance(tc.method, tc.path) != tc.allow {
			t.Fatalf("unexpected maintenance access: %s %s", tc.method, tc.path)
		}
	}
}
