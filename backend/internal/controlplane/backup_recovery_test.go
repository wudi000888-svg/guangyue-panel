package controlplane

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type stalledBackupWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
}

func (w *stalledBackupWriter) Header() http.Header { return w.header }
func (w *stalledBackupWriter) WriteHeader(int)     {}
func (w *stalledBackupWriter) Write(b []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(b), nil
}
func TestSlowBackupDoesNotLockControlPlane(t *testing.T) {
	a := testApp(t)
	w := &stalledBackupWriter{http.Header{}, make(chan struct{}, 1), make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); a.backup(w, httptest.NewRequest("GET", "/api/backup", nil), Record{}) }()
	defer func() { close(w.release); <-done }()
	select {
	case <-w.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("backup did not start")
	}
	if !a.mu.TryLock() {
		t.Fatal("network write holds the application lock")
	}
	a.mu.Unlock()
}
func TestRestoreRejectsCorruptEncryptedCollectionsBeforeReplacingLiveFiles(t *testing.T) {
	for _, table := range []string{"nodes", "ip_pool", "import_sources", "public_custom_sources"} {
		t.Run(table, func(t *testing.T) {
			a := testApp(t)
			testUser(t, a, "owner", "owner")
			stage := t.TempDir()
			if err := a.backupSnapshot(stage); err != nil {
				t.Fatal(err)
			}
			s, err := openStore(stage)
			if err != nil {
				t.Fatal(err)
			}
			statement := "INSERT OR REPLACE INTO " + table + "(id,doc) VALUES('corrupt',x'0000')"
			if strings.Contains(table, "sources") {
				statement = "INSERT INTO " + table + "(id,url_hash,doc) VALUES('corrupt','unique',x'0000')"
			}
			if _, err = s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err = s.db.Close(); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err = writeBackup(&archive, stage); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "invalid.tar.gz")
			if err = os.WriteFile(path, archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			a.store.db.Close()
			before, _ := os.ReadFile(filepath.Join(a.cfg.StateDir, "panel.db"))
			if err = restoreBackup(a.cfg, path); err == nil {
				t.Fatal("accepted corrupt encrypted collection")
			}
			after, _ := os.ReadFile(filepath.Join(a.cfg.StateDir, "panel.db"))
			if !bytes.Equal(before, after) {
				t.Fatal("rejected backup modified live database")
			}
			dirs, _ := filepath.Glob(filepath.Join(a.cfg.StateDir, "before-restore-*"))
			if len(dirs) != 0 {
				t.Fatal("live-file replacement began before validation")
			}
		})
	}
}
func TestRestoreRollsBackEveryRenameFailure(t *testing.T) {
	names := []string{"panel.db", "panel.db-wal", "panel.db-shm", "master.key", "xray.json", "hy2.json", runtimeModeFile, runtimeCommitFile}
	for fail := 1; fail <= 14; fail++ {
		live, stage, previous := t.TempDir(), t.TempDir(), t.TempDir()
		for _, n := range names {
			os.WriteFile(filepath.Join(live, n), []byte("old "+n), 0600)
			os.WriteFile(filepath.Join(stage, n), []byte("new "+n), 0600)
		}
		calls := 0
		rename := func(from, to string) error {
			calls++
			if calls == fail {
				return errors.New("injected rename failure")
			}
			return os.Rename(from, to)
		}
		if err := installRestore(live, stage, previous, rename); err == nil {
			t.Fatalf("failure %d unexpectedly succeeded", fail)
		}
		for _, n := range names {
			b, err := os.ReadFile(filepath.Join(live, n))
			if err != nil || string(b) != "old "+n {
				t.Fatalf("failure %d lost %s: %v", fail, n, err)
			}
		}
	}
}
func TestRestorePreservesNoLogsModeAndRegeneratesConfigs(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	if _, _, err := a.store.changeRuntimeMode(runtimeNoLogs, a.store.runtime.Revision); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, owner, "GET", "/api/backup", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	path := filepath.Join(t.TempDir(), "backup.tar.gz")
	os.WriteFile(path, w.Body.Bytes(), 0600)
	a.store.db.Close()
	if err := restoreBackup(a.cfg, path); err != nil {
		t.Fatal(err)
	}
	mode, ok := runtimeFileState(a.cfg.StateDir)
	if !ok || mode.Mode != runtimeNoLogs {
		t.Fatal("restore enabled logging")
	}
	for _, n := range []string{"xray.json", "hy2.json"} {
		if info, err := os.Stat(filepath.Join(a.cfg.StateDir, n)); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("missing or exposed core configuration", n, err)
		}
	}
}

func TestRestoreRejectsInvalidDNSBeforeInstalling(t *testing.T) {
	a := testApp(t)
	stage := t.TempDir()
	if err := a.backupSnapshot(stage); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.saveNode(Node{ID: "invalid-dns", Protocol: "vless", Enabled: true, Exit: "direct", DNS: &NodeDNS{Mode: "invalid"}}); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	if err = prepareRestore(a.cfg, stage); err == nil {
		t.Fatal("invalid DNS accepted for restore")
	}
}
