package controlplane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

func TestLoadConfigAppliesBusinessAdoptionMarker(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfg := Config{Edition: "pro", Role: "controller", SiteID: "adopted", StateDir: dir, PublicURL: "https://site.example.com", VLESSHost: "vless.example.com", HY2Host: "hy.example.com", Database: persistence.Options{Driver: "postgres", DSN: "postgres://panel:secret@localhost/panel"}, RedisURL: "redis://:secret@localhost/1", Dev: true}
	if err := writeJSON(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	marker := businessAdoption{SiteID: "adopted", ControllerURL: "https://master.example.com", ConnectToken: "gye_" + strings.Repeat("A", 43)}
	if err := writeJSON(filepath.Join(dir, businessAdoptionFile), marker); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "business" || !got.BusinessAdopted || got.ControllerURL != marker.ControllerURL || got.ConnectToken != marker.ConnectToken {
		t.Fatalf("adoption marker not applied: %+v", got)
	}
	if got.DatabaseOptions().Driver != "postgres" {
		t.Fatalf("adopted Pro site lost PostgreSQL storage: %s", got.DatabaseOptions().Driver)
	}

	bad := marker
	bad.SiteID = "other"
	b, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(dir, businessAdoptionFile), b, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = loadConfig(configPath)
	if err != nil || got.Role != "controller" {
		t.Fatalf("invalid adoption marker changed role: role=%s err=%v", got.Role, err)
	}
}
