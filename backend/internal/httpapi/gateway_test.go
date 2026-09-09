package httpapi

import (
	"net"
	"testing"
)

func TestGatewayScopeAndForbiddenSurfaces(t *testing.T) {
	for _, path := range []string{"/api/backup", "/api/fleet/tokens", "/api/fleet-gateway", "/api/password", "/api/logout", "https://evil.test/api/state", "//evil.test/api/state", "/api/nodes/../backup", "/api/nodes/%2e%2e/backup", "/api/nodes%5c..%5cbackup"} {
		if AllowedGateway("GET", path, "manage") {
			t.Errorf("accepted %s", path)
		}
	}
	if AllowedGateway("DELETE", "/api/nodes/one", "read") {
		t.Fatal("read token allowed mutation")
	}
	if !AllowedGateway("POST", "/api/nodes", "manage") || !AllowedGateway("GET", "/api/operations", "read") {
		t.Fatal("valid route denied")
	}
	for _, address := range []string{"http://site.test", "https://user:secret@site.test", "https://site.test/path", "https://site.test?token=x"} {
		if ValidateEndpoint(address, false) == nil {
			t.Error("accepted invalid endpoint")
		}
	}
}

func TestMonitoringScopeDoesNotExportCredentials(t *testing.T) {
	for _, path := range []string{"/api/state", "/api/subscription", "/api/ips", "/api/users", "/api/tasks"} {
		if AllowedGateway("GET", path, "read") {
			t.Fatal("monitoring exported credential surface", path)
		}
	}
	for _, addr := range []string{"100.64.0.1", "198.18.0.1", "192.0.2.1", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "fc00::1", "2002:7f00:1::"} {
		if publicAddress(net.ParseIP(addr)) {
			t.Fatal("accepted reserved destination", addr)
		}
	}
	if !publicAddress(net.ParseIP("1.1.1.1")) {
		t.Fatal("public IP rejected")
	}
}
