package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Public order ownership outlives the erased wallet's encrypted seed.
func (d *DB) migrateCryptoWalletHistory(ctx context.Context, tx *Tx) error {
	rows, e := tx.QueryContext(ctx, "SELECT id,receive_key,next_index,removed_at FROM crypto_wallets")
	if e != nil {
		return e
	}
	type owner struct {
		id, receive   string
		next, deleted int64
	}
	owners := []owner{}
	for rows.Next() {
		var x owner
		if e = rows.Scan(&x.id, &x.receive, &x.next, &x.deleted); e != nil {
			break
		}
		owners = append(owners, x)
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, x := range owners {
		hash := sha256.Sum256([]byte(x.receive))
		if _, e = tx.ExecContext(ctx, "INSERT INTO crypto_wallet_history(id,receive_hash,next_index,deleted_at) VALUES(?,?,?,?)", x.id, hex.EncodeToString(hash[:]), x.next, x.deleted); e != nil {
			return e
		}
	}
	if d.driver == "postgres" {
		for _, table := range []string{"crypto_wallet_addresses", "crypto_invoices", "crypto_funding_addresses", "crypto_sweep_jobs"} {
			if _, e = tx.ExecContext(ctx, "ALTER TABLE "+table+" DROP CONSTRAINT "+table+"_wallet_id_fkey"); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "ALTER TABLE "+table+" ADD CONSTRAINT "+table+"_wallet_id_fkey FOREIGN KEY(wallet_id) REFERENCES crypto_wallet_history(id)"); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "ALTER TABLE crypto_funding_addresses DROP CONSTRAINT crypto_funding_addresses_address_key")
		return e
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); e != nil {
		return e
	}
	tables := []struct{ name, definition, columns, indexes string }{
		{"crypto_wallet_addresses", `id TEXT PRIMARY KEY,wallet_id TEXT NOT NULL REFERENCES crypto_wallet_history(id),address_index INTEGER NOT NULL CHECK(address_index>=0 AND address_index<2147483648),path TEXT NOT NULL,address TEXT NOT NULL UNIQUE,label TEXT NOT NULL,created INTEGER NOT NULL,UNIQUE(wallet_id,address_index)`, "id,wallet_id,address_index,path,address,label,created", ""},
		{"crypto_invoices", `id TEXT PRIMARY KEY,order_id TEXT NOT NULL REFERENCES commerce_orders(id),user_id INTEGER NOT NULL,attempt_id TEXT NOT NULL UNIQUE REFERENCES payment_attempts(id),operation_id TEXT NOT NULL UNIQUE,fingerprint TEXT NOT NULL,wallet_id TEXT NOT NULL REFERENCES crypto_wallet_history(id),address_id TEXT NOT NULL UNIQUE REFERENCES crypto_wallet_addresses(id),chain_id INTEGER NOT NULL,asset_id TEXT NOT NULL,address TEXT NOT NULL UNIQUE,contract TEXT NOT NULL,expected_atoms TEXT NOT NULL,state TEXT NOT NULL,created INTEGER NOT NULL,expires INTEGER NOT NULL,start_block INTEGER NOT NULL,next_block INTEGER NOT NULL,last_scan INTEGER NOT NULL DEFAULT 0,finalized_block INTEGER NOT NULL DEFAULT 0,scan_error TEXT NOT NULL DEFAULT '',doc BLOB NOT NULL`, "id,order_id,user_id,attempt_id,operation_id,fingerprint,wallet_id,address_id,chain_id,asset_id,address,contract,expected_atoms,state,created,expires,start_block,next_block,last_scan,finalized_block,scan_error,doc", `CREATE INDEX crypto_invoice_order ON crypto_invoices(order_id,created);CREATE INDEX crypto_invoice_scan ON crypto_invoices(last_scan,id)`},
		{"crypto_funding_addresses", `wallet_id TEXT PRIMARY KEY REFERENCES crypto_wallet_history(id),address TEXT NOT NULL,path TEXT NOT NULL,created INTEGER NOT NULL`, "wallet_id,address,path,created", ""},
		{"crypto_sweep_jobs", `id TEXT PRIMARY KEY,actor_id INTEGER NOT NULL,operation_id TEXT NOT NULL UNIQUE,fingerprint TEXT NOT NULL,wallet_id TEXT NOT NULL REFERENCES crypto_wallet_history(id),chain_id INTEGER NOT NULL,asset_id TEXT NOT NULL,destination TEXT NOT NULL,min_atoms TEXT NOT NULL,max_gas_atoms TEXT NOT NULL,state TEXT NOT NULL,doc BLOB NOT NULL,config BLOB NOT NULL,error TEXT NOT NULL DEFAULT '',lease_token TEXT NOT NULL DEFAULT '',lease_until INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,updated INTEGER NOT NULL`, "id,actor_id,operation_id,fingerprint,wallet_id,chain_id,asset_id,destination,min_atoms,max_gas_atoms,state,doc,config,error,lease_token,lease_until,created,updated", `CREATE UNIQUE INDEX crypto_one_active_sweep ON crypto_sweep_jobs(wallet_id,chain_id) WHERE state NOT IN ('complete','cancelled');CREATE UNIQUE INDEX crypto_sweep_quote_once ON crypto_sweep_jobs(actor_id,fingerprint)`},
	}
	for _, table := range tables {
		for _, q := range []string{"CREATE TABLE " + table.name + "_permanent(" + table.definition + ")", "INSERT INTO " + table.name + "_permanent(" + table.columns + ") SELECT " + table.columns + " FROM " + table.name, "DROP TABLE " + table.name, "ALTER TABLE " + table.name + "_permanent RENAME TO " + table.name} {
			if _, e = tx.ExecContext(ctx, q); e != nil {
				return fmt.Errorf("%s: %w", table.name, e)
			}
		}
		for _, q := range strings.Split(table.indexes, ";") {
			if q != "" {
				if _, e = tx.ExecContext(ctx, q); e != nil {
					return e
				}
			}
		}
	}
	// Clearing SQLite's stale DDL-deferred counter is safe only after every
	// final parent reference has been explicitly checked. FK enforcement stays on.
	check, e := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	bad := check.Next()
	e = errors.Join(check.Err(), check.Close())
	if e != nil {
		return e
	}
	if bad {
		return errors.New("wallet history migration violated foreign keys")
	}
	_, e = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=OFF")
	return e
}
