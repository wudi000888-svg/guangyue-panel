package persistence

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func nowUnix() int64 { return time.Now().Unix() }

// Tables is ordered by foreign-key dependencies. schema_migrations is local to
// each dialect; its contents are checked by Migrate rather than copied blindly.
var Tables = []string{"users", "nodes", "ip_pool", "meta", "import_sources", "quality_geo_cache", "messages", "public_blacklist", "public_source_cache", "public_source_tasks", "public_custom_sources", "audit", "checkpoints", "revocations", "traffic", "sessions", "message_recipients", "tasks", "fleet_peers", "fleet_tokens", "core_revisions", "business_sites"}

// Copy replaces destination data in one transaction from a consistent source
// snapshot. Callers must stop all site controllers before migration or restore.
// requireEmpty guards migrations from accidentally replacing an existing site.
func Copy(ctx context.Context, src, dst *DB, requireEmpty bool) error {
	readOpts := &sql.TxOptions{ReadOnly: true}
	if src.driver == "postgres" {
		readOpts.Isolation = sql.LevelRepeatableRead
	}
	r, err := src.BeginTx(ctx, readOpts)
	if err != nil {
		return err
	}
	defer r.Rollback()
	w, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if requireEmpty {
		var count int
		for _, table := range Tables {
			if table == "meta" {
				continue
			}
			if err = w.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+dst.tableName(table)).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return errors.New("target site contains data; migration refused")
			}
		}
	}
	for i := len(Tables) - 1; i >= 0; i-- {
		if _, err = w.ExecContext(ctx, "DELETE FROM "+dst.tableName(Tables[i])); err != nil {
			return err
		}
	}
	for _, table := range Tables {
		rows, err := r.QueryContext(ctx, "SELECT * FROM "+src.tableName(table))
		if err != nil {
			return err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		quoted := make([]string, len(columns))
		placeholders := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
			placeholders[i] = "?"
		}
		q := "INSERT INTO " + dst.tableName(table) + "(" + strings.Join(quoted, ",") + ") VALUES(" + strings.Join(placeholders, ",") + ")"
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				rows.Close()
				return err
			}
			if _, err = w.ExecContext(ctx, q, values...); err != nil {
				rows.Close()
				return err
			}
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	if dst.driver == "postgres" {
		for _, table := range []string{"users", "messages", "audit"} {
			// Explicit IDs are preserved; advance sequences so future INSERTs work.
			q := "SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE(MAX(id),1), COUNT(*)>0) FROM " + dst.tableName(table)
			if _, err = w.ExecContext(ctx, q, dst.schema+"."+table); err != nil {
				return err
			}
		}
	}
	if err = r.Commit(); err != nil {
		return err
	}
	return w.Commit()
}

// Snapshot always produces a SQLite recovery artifact, including on Pro. This
// preserves a single portable backup format and does not require pg_dump tools.
func (d *DB) Snapshot(ctx context.Context, path string) error {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("snapshot target already exists or cannot be inspected")
	}
	if filepath.Base(path) != "panel.db" {
		return errors.New("snapshot must be named panel.db")
	}
	if d.driver == "sqlite" {
		_, err := d.ExecContext(ctx, "VACUUM INTO ?", path)
		return err
	}
	out, err := Open(ctx, filepath.Dir(path), Options{Driver: "sqlite"})
	if err != nil {
		return err
	}
	defer out.Close()
	if err = Copy(ctx, d, out, true); err != nil {
		return err
	}
	var busy, pages, done int
	if err = out.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &done); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("snapshot checkpoint busy")
	}
	return os.Chmod(path, 0600)
}
