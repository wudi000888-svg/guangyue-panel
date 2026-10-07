package geoip

import (
	"sync"
	"testing"
)

func TestBundledCountryDatabaseIPv4IPv6AndUnknown(t *testing.T) {
	for _, test := range []struct{ ip, country string }{{"8.8.8.8", "US"}, {"223.5.5.5", "CN"}, {"2001:4860:4860::8888", "CA"}, {"::ffff:8.8.8.8", "US"}, {"127.0.0.1", ""}, {"10.0.0.1", ""}, {"::1", ""}, {"invalid", ""}} {
		c, err := Lookup(test.ip)
		if err != nil || c.Code != test.country {
			t.Fatalf("%s: country %q, error %v", test.ip, c.Code, err)
		}
		if c.Code != "" && c.Name == "" {
			t.Fatal("country name missing")
		}
	}
}

func TestCountryReaderConcurrentLookups(t *testing.T) {
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 20 {
				c, err := Lookup("8.8.8.8")
				if err != nil || c.Code != "US" {
					t.Errorf("shared reader failed: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
}
