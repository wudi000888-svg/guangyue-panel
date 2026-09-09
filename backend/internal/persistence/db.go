// Package persistence owns SQL dialects, connection budgets and versioned schemas.
// Business packages use parameterized portable SQL; infrastructure errors never
// include connection strings in user-facing responses.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Options struct {
	Driver         string `json:"driver"`
	DSN            string `json:"dsn,omitempty"`
	SiteID         string `json:"site_id"`
	MaxConnections int    `json:"max_connections,omitempty"`
}

var validSite = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func ValidSite(id string) bool { return validSite.MatchString(id) }

type DB struct {
	raw            *sql.DB
	driver, schema string
}

func Open(ctx context.Context, dir string, opts Options) (_ *DB, err error) {
	if opts.Driver == "" {
		opts.Driver = "sqlite"
	}
	if opts.SiteID == "" {
		opts.SiteID = "default"
	}
	if !ValidSite(opts.SiteID) {
		return nil, errors.New("invalid site ID")
	}
	d := &DB{driver: opts.Driver, schema: "gy_" + opts.SiteID}
	switch opts.Driver {
	case "sqlite":
		d.raw, err = sql.Open("sqlite", filepath.Join(dir, "panel.db"))
		if err != nil {
			return nil, err
		}
		d.raw.SetMaxOpenConns(1)
		d.raw.SetMaxIdleConns(1)
		_, err = d.raw.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA cache_size=-2048; PRAGMA foreign_keys=ON;`)
	case "postgres":
		var cfg *pgx.ConnConfig
		cfg, err = pgx.ParseConfig(opts.DSN)
		if err != nil || opts.DSN == "" {
			return nil, errors.New("invalid PostgreSQL connection configuration")
		}
		cfg.RuntimeParams["search_path"] = pgx.Identifier{d.schema}.Sanitize()
		cfg.RuntimeParams["statement_timeout"] = "15000"
		cfg.RuntimeParams["lock_timeout"] = "5000"
		cfg.RuntimeParams["application_name"] = "guangyue:" + opts.SiteID
		cfg.ConnectTimeout = 5 * time.Second
		d.raw = stdlib.OpenDB(*cfg)
		n := opts.MaxConnections
		if n == 0 {
			n = 8
		}
		if n < 2 || n > 64 {
			_ = d.raw.Close()
			return nil, errors.New("PostgreSQL max_connections must be 2..64")
		}
		d.raw.SetMaxOpenConns(n)
		d.raw.SetMaxIdleConns(2)
		d.raw.SetConnMaxIdleTime(5 * time.Minute)
		d.raw.SetConnMaxLifetime(30 * time.Minute)
		_, err = d.raw.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{d.schema}.Sanitize())
	default:
		return nil, errors.New("unsupported database driver")
	}
	if err == nil {
		err = d.Migrate(ctx)
	}
	if err != nil {
		_ = d.raw.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Driver() string                        { return d.driver }
func (d *DB) Schema() string                        { return d.schema }
func (d *DB) Close() error                          { return d.raw.Close() }
func (d *DB) PingContext(ctx context.Context) error { return d.raw.PingContext(ctx) }
func (d *DB) Stats() sql.DBStats                    { return d.raw.Stats() }
func (d *DB) Bind(query string) string {
	if d.driver == "postgres" {
		return Rebind(query)
	}
	return query
}
func (d *DB) Exec(q string, a ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), q, a...)
}
func (d *DB) Query(q string, a ...any) (*sql.Rows, error) {
	return d.QueryContext(context.Background(), q, a...)
}
func (d *DB) QueryRow(q string, a ...any) *sql.Row {
	return d.QueryRowContext(context.Background(), q, a...)
}
func (d *DB) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return d.raw.ExecContext(ctx, d.Bind(q), a...)
}
func (d *DB) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return d.raw.QueryContext(ctx, d.Bind(q), a...)
}
func (d *DB) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	return d.raw.QueryRowContext(ctx, d.Bind(q), a...)
}
func (d *DB) Begin() (*Tx, error) { return d.BeginTx(context.Background(), nil) }
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	t, err := d.raw.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{raw: t, db: d}, nil
}

type Tx struct {
	raw *sql.Tx
	db  *DB
}

func (t *Tx) Exec(q string, a ...any) (sql.Result, error) {
	return t.ExecContext(context.Background(), q, a...)
}
func (t *Tx) Query(q string, a ...any) (*sql.Rows, error) {
	return t.QueryContext(context.Background(), q, a...)
}
func (t *Tx) QueryRow(q string, a ...any) *sql.Row {
	return t.QueryRowContext(context.Background(), q, a...)
}
func (t *Tx) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return t.raw.ExecContext(ctx, t.db.Bind(q), a...)
}
func (t *Tx) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return t.raw.QueryContext(ctx, t.db.Bind(q), a...)
}
func (t *Tx) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	return t.raw.QueryRowContext(ctx, t.db.Bind(q), a...)
}
func (t *Tx) Commit() error   { return t.raw.Commit() }
func (t *Tx) Rollback() error { return t.raw.Rollback() }

// Rebind only changes placeholders outside SQL strings, identifiers and comments.
// Queries are developer-owned, never user-supplied SQL. PostgreSQL dollar-quoted
// literals are also preserved so migrations can contain function bodies.
func Rebind(q string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); {
		start := i
		switch {
		case q[i] == '\'' || q[i] == '"':
			quote := q[i]
			i++
			for i < len(q) {
				if q[i] == quote {
					i++
					if i < len(q) && q[i] == quote {
						i++
						continue
					}
					break
				}
				i++
			}
		case strings.HasPrefix(q[i:], "--"):
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case strings.HasPrefix(q[i:], "/*"):
			i += 2
			depth := 1
			for i < len(q) && depth > 0 {
				if strings.HasPrefix(q[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(q[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
		case q[i] == '$':
			j := i + 1
			for j < len(q) && ((q[j] >= 'a' && q[j] <= 'z') || (q[j] >= 'A' && q[j] <= 'Z') || q[j] == '_' || (j > i+1 && q[j] >= '0' && q[j] <= '9')) {
				j++
			}
			if j < len(q) && q[j] == '$' {
				tag := q[i : j+1]
				if k := strings.Index(q[j+1:], tag); k >= 0 {
					i = j + 1 + k + len(tag)
				} else {
					i++
				}
			} else {
				i++
			}
		case q[i] == '?':
			n++
			b.WriteString("$" + strconv.Itoa(n))
			i++
			continue
		default:
			i++
		}
		b.WriteString(q[start:i])
	}
	return b.String()
}

// ControllerLease prevents two controllers from applying one site's configuration.
// This uses a dedicated connection; a lost connection is a terminal lease loss,
// never silently reacquired while the old controller may still be running.
type ControllerLease struct {
	conn *sql.Conn
	site string
}

func (d *DB) AcquireController(ctx context.Context) (*ControllerLease, error) {
	if d.driver != "postgres" {
		return nil, nil
	}
	conn, err := d.raw.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", d.schema+":controller").Scan(&ok)
	if err != nil || !ok {
		_ = conn.Close()
		return nil, errors.New("site already has a controller or database unavailable")
	}
	return &ControllerLease{conn: conn, site: d.schema}, nil
}
func (l *ControllerLease) Check(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return l.conn.PingContext(ctx)
}
func (l *ControllerLease) Close() error {
	if l == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", l.site+":controller")
	return errors.Join(err, l.conn.Close())
}

func (d *DB) tableName(name string) string {
	// Only call with the static Tables registry below, never request input.
	return fmt.Sprintf(`"%s"`, name)
}
