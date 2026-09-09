package controlplane

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicBaselineAcceptsFullSampleFromLargerSpeedFile(t *testing.T) {
	sample := strings.Repeat("x", 256<<10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			_, _ = io.WriteString(w, "8.8.8.8")
			return
		}
		_, _ = io.WriteString(w, sample)
	}))
	defer server.Close()
	ips, speed, err := publicHealthyTargets(context.Background(), []string{server.URL + "/ip"}, []string{server.URL + "/sample"})
	if err != nil || len(ips) != 1 || speed != server.URL+"/sample" {
		t.Fatal("a healthy speed file exceeding the bounded connectivity sample was rejected")
	}
}

func TestPublicBaselineRejectsShortInterruptedAndUnverifiedIdentityResponses(t *testing.T) {
	full := strings.Repeat("x", 1024)
	cases := []struct {
		name     string
		response qualityPage
		identity bool
		want     bool
	}{
		{"full sample", qualityPage{code: 200, body: full}, false, true},
		{"explicit sample limit", qualityPage{code: 200, body: full, truncated: true, err: errQualityByteBudget}, false, true},
		{"short body", qualityPage{code: 200, body: full[:1023]}, false, false},
		{"short interrupted read", qualityPage{code: 200, body: full[:500], err: io.ErrUnexpectedEOF}, false, false},
		{"interrupted read at sample boundary", qualityPage{code: 200, body: full, err: io.ErrUnexpectedEOF}, false, false},
		{"truncated interrupted response", qualityPage{code: 200, body: full, truncated: true, err: io.ErrUnexpectedEOF}, false, false},
		{"status denied", qualityPage{code: 403, body: full, truncated: true, err: errQualityByteBudget}, false, false},
		{"unmarked error", qualityPage{code: 200, body: full, err: errQualityByteBudget}, false, false},
		{"verified identity", qualityPage{code: 200, body: "8.8.8.8"}, true, true},
		{"private identity", qualityPage{code: 200, body: "127.0.0.1"}, true, false},
		{"truncated identity", qualityPage{code: 200, body: "ip=8.8.8.8\n" + full, truncated: true, err: errQualityByteBudget}, true, false},
		{"interrupted identity", qualityPage{code: 200, body: "8.8.8.8", err: io.ErrUnexpectedEOF}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicBaselineResponseOK(tc.response, tc.identity); got != tc.want {
				t.Fatalf("baseline result %t, want %t", got, tc.want)
			}
		})
	}
}
