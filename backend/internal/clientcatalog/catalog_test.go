package clientcatalog

import (
	"bytes"
	"os"
	"testing"
)

func TestCatalogMatchesFrontendAndPlatformIndices(t *testing.T) {
	source, err := os.ReadFile("../../../frontend/src/data/clients.json")
	if err != nil || !bytes.Equal(source, data) {
		t.Fatal("client catalog drift: run python3 scripts/sync-client-catalog.py", err)
	}
	for _, test := range []struct {
		id             string
		index          int
		platform, arch string
	}{
		{"clash-verge", 0, "Windows", "x64"},
		{"clash-verge", 2, "macOS", "Apple Silicon"},
		{"flclash", 4, "Windows", "ARM64"},
		{"flclash", 5, "macOS", "Apple Silicon"},
		{"v2rayn", 3, "macOS", "Intel"},
		{"v2rayng", 0, "Android", "ARM64"},
	} {
		item, ok := Lookup(test.id, test.index)
		if !ok || item.Platform != test.platform || item.Arch != test.arch {
			t.Fatalf("wrong platform/index: %+v = %+v", test, item)
		}
	}
	for _, index := range []int{-1, 1000} {
		if _, ok := Lookup("clash-verge", index); ok {
			t.Fatal("invalid index accepted")
		}
	}
	if _, ok := Lookup("missing", 0); ok {
		t.Fatal("unknown client accepted")
	}
}
