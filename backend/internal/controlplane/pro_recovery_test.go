package controlplane

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPostgresMigrationRestoreAndRollback(t *testing.T) {
	dsn := os.Getenv("GY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL integration not configured")
	}
	lite := testApp(t)
	user := testUser(t, lite, "restore-owner", "owner")
	cfg := lite.cfg
	cfg.Edition = "pro"
	cfg.SiteID = "restore_" + randomToken(8)
	cfg.SiteID = "restore_" + digest(cfg.SiteID)[:16]
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = dsn
	cfg.StateDir = t.TempDir()
	key, err := os.ReadFile(filepath.Join(lite.cfg.StateDir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cfg.StateDir, "master.key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	dst, err := openConfiguredStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = dst.db.Exec("DROP SCHEMA " + dst.db.Schema() + " CASCADE"); dst.db.Close() })
	if err = importSQLiteSite(cfg, dst, filepath.Join(lite.cfg.StateDir, "panel.db")); err != nil {
		t.Fatal(err)
	}
	migrated, err := dst.record(user.ID)
	if err != nil || migrated.Credentials.Token != user.Credentials.Token || !bytes.Equal(migrated.Password, user.Password) {
		t.Fatal("migration changed authentication")
	}
	if err = importSQLiteSite(cfg, dst, filepath.Join(lite.cfg.StateDir, "panel.db")); err == nil {
		t.Fatal("nonempty migration accepted")
	}
	app := &App{cfg: cfg, store: dst}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err = offlineSnapshot(cfg, dst, archive); err != nil {
		t.Fatal(err)
	}
	user.Upload = 98765
	if err = dst.save(&user); err != nil {
		t.Fatal(err)
	}
	// Inject failure after file replacement has begun: both DB and key must recover.
	stage := t.TempDir()
	if err = extractBackup(archive, stage); err != nil {
		t.Fatal(err)
	}
	if err = prepareRestore(cfg, stage); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = restorePostgresWithRename(cfg, stage, func(a, b string) error {
		calls++
		if calls == 5 {
			return errors.New("injected file failure")
		}
		return os.Rename(a, b)
	})
	if err == nil {
		t.Fatal("restore failure was ignored")
	}
	current, err := dst.record(user.ID)
	if err != nil || current.Upload != 98765 {
		t.Fatal("failed restore lost current DB")
	}
	actualKey, _ := os.ReadFile(filepath.Join(cfg.StateDir, "master.key"))
	if !bytes.Equal(key, actualKey) {
		t.Fatal("failed restore changed key")
	}
	if err = restoreBackup(cfg, archive); err != nil {
		t.Fatal(err)
	}
	current, err = dst.record(user.ID)
	if err != nil || current.Upload != 0 || current.Credentials.Token != user.Credentials.Token {
		t.Fatal("successful restore changed credentials or counters")
	}
	// Snapshot export is portable even after restoring a Pro site.
	if err = app.backupSnapshot(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err = dst.db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
