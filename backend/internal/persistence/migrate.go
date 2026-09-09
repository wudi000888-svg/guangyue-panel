package persistence

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func (d *DB) Migrate(ctx context.Context) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if d.driver == "postgres" {
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", d.schema+":migrations"); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at BIGINT NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if !known[name] {
			rows.Close()
			return errors.New("database schema is newer than this application; restore the matching release")
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, name := range names {
		b, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		checksum := hex.EncodeToString(sum[:])
		var old string
		rows, err := tx.QueryContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=?", name)
		if err != nil {
			return err
		}
		found := rows.Next()
		if found {
			err = rows.Scan(&old)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		if found {
			if old != checksum {
				return fmt.Errorf("migration %s checksum mismatch", name)
			}
			continue
		}
		sqlText := string(b)
		if d.driver == "postgres" {
			sqlText = strings.ReplaceAll(sqlText, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
			sqlText = strings.ReplaceAll(sqlText, "audit (id INTEGER PRIMARY KEY,", "audit (id BIGSERIAL PRIMARY KEY,")
			sqlText = strings.ReplaceAll(sqlText, "INTEGER", "BIGINT")
			sqlText = strings.ReplaceAll(sqlText, "BLOB", "BYTEA")
		}
		// Migration files contain statements, not triggers or function bodies.
		for _, statement := range strings.Split(sqlText, ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,checksum,applied_at) VALUES(?,?,?)", name, checksum, nowUnix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
