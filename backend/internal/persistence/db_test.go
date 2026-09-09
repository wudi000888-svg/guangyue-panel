package persistence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sqliteTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func postgresTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("GY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GY_TEST_POSTGRES_DSN not configured")
	}
	site := fmt.Sprintf("test_%x", time.Now().UnixNano())
	d, err := Open(context.Background(), t.TempDir(), Options{Driver: "postgres", DSN: dsn, SiteID: site})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.raw.Exec("DROP SCHEMA " + d.schema + " CASCADE"); d.Close() })
	return d
}
func TestRebindSQLBoundaries(t *testing.T) {
	q := `SELECT ?, '?', "?", 'it''s ?' /* ? /* nested ? */ */ , $$?$$, $body$?$body$, ? -- ?` + "\n,?"
	want := `SELECT $1, '?', "?", 'it''s ?' /* ? /* nested ? */ */ , $$?$$, $body$?$body$, $2 -- ?` + "\n,$3"
	if got := Rebind(q); got != want {
		t.Fatalf("got %s", got)
	}
}
func TestMigrationTamperAndIdempotence(t *testing.T) {
	d := sqliteTestDB(t)
	ctx := context.Background()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE schema_migrations SET checksum='tampered'"); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatal("modified migration accepted", err)
	}
}
func TestPostgresRoundTripAndIsolation(t *testing.T) {
	pg := postgresTestDB(t)
	src := sqliteTestDB(t)
	ctx := context.Background()
	if _, err := src.Exec("INSERT INTO users(id,username,doc,credentials,password,token_hash) VALUES(?,?,?,?,?,?)", int64(42), "fixture", []byte(`{}`), []byte{1, 2, 3}, []byte{4}, "tokenhash"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec("INSERT INTO sessions VALUES(?,?,?)", "session", 42, int64(9000000000)); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec("INSERT INTO audit(id,at,actor,action,target) VALUES(?,?,?,?,?)", 19, int64(9000000000), "fixture", "copy", "target"); err != nil {
		t.Fatal(err)
	}
	if err := Copy(ctx, src, pg, true); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pg.QueryRow("INSERT INTO users(username,doc,credentials,password,token_hash) VALUES(?,?,?,?,?) RETURNING id", "next", []byte(`{}`), []byte{8}, []byte{9}, "next-hash").Scan(&id); err != nil || id <= 42 {
		t.Fatal("sequence not preserved", id, err)
	}
	if _, err := pg.Exec("INSERT INTO sessions VALUES(?,?,?)", "orphan", 9999, 0); err == nil {
		t.Fatal("foreign key not enforced")
	}
	if err := Copy(ctx, src, pg, true); err == nil {
		t.Fatal("nonempty migration destination accepted")
	}
	other := postgresTestDB(t)
	var count int
	if err := other.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil || count != 0 {
		t.Fatal("site data leaked", count, err)
	}
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := pg.Snapshot(ctx, path); err != nil {
		t.Fatal(err)
	}
	back, err := Open(ctx, filepath.Dir(path), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	var blob []byte
	var expires int64
	if err := back.QueryRow("SELECT credentials FROM users WHERE id=42").Scan(&blob); err != nil || string(blob) != string([]byte{1, 2, 3}) {
		t.Fatal("binary data changed", err)
	}
	if err := back.QueryRow("SELECT expires FROM sessions WHERE user_id=42").Scan(&expires); err != nil || expires != 9000000000 {
		t.Fatal("64 bit value changed", err)
	}
	if err := Copy(ctx, back, other, true); err != nil {
		t.Fatal(err)
	}
	if err := other.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil || count != 2 {
		t.Fatal("restore count", count, err)
	}
}
func TestPostgresSingleController(t *testing.T) {
	d := postgresTestDB(t)
	ctx := context.Background()
	one, err := d.AcquireController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	if second, err := d.AcquireController(ctx); err == nil {
		second.Close()
		t.Fatal("duplicate controller accepted")
	}
	if err := one.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownMigrationIsRejectedEvenWithSameCount(t *testing.T) {
	d := sqliteTestDB(t)
	if _, err := d.Exec("UPDATE schema_migrations SET version='999_future.sql' WHERE version='002_operations.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(context.Background()); err == nil {
		t.Fatal("unknown schema accepted")
	}
}
