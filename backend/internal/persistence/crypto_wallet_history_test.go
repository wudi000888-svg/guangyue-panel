package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestCryptoWalletHistoryMigratesExistingReferences(t *testing.T) {
	raw, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	d := &DB{raw: raw, driver: "sqlite"}
	if _, e = d.Exec("PRAGMA foreign_keys=ON"); e != nil {
		t.Fatal(e)
	}
	testCryptoWalletHistoryLegacyFixture(t, d)
}

func TestPostgresCryptoWalletHistoryMigratesExistingReferences(t *testing.T) {
	d := postgresTestDB(t)
	if _, e := d.Exec("DROP SCHEMA " + d.schema + " CASCADE"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.Exec("CREATE SCHEMA " + d.schema); e != nil {
		t.Fatal(e)
	}
	testCryptoWalletHistoryLegacyFixture(t, d)
}

func testCryptoWalletHistoryLegacyFixture(t *testing.T, d *DB) {
	t.Helper()
	ctx := context.Background()
	if _, e := d.Exec("CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	entries, e := migrationFiles.ReadDir("migrations")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() >= "020" {
			continue
		}
		body, e := migrationFiles.ReadFile("migrations/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		text := string(body)
		if d.driver == "postgres" {
			text = strings.ReplaceAll(text, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
			text = strings.ReplaceAll(text, "audit (id INTEGER PRIMARY KEY,", "audit (id BIGSERIAL PRIMARY KEY,")
			text = strings.ReplaceAll(text, "INTEGER", "BIGINT")
			text = strings.ReplaceAll(text, "BLOB", "BYTEA")
		}
		for _, q := range strings.Split(text, ";") {
			if strings.TrimSpace(q) != "" {
				if _, e = d.Exec(q); e != nil {
					t.Fatal(entry.Name(), e)
				}
			}
		}
		sum := sha256.Sum256(body)
		if _, e = d.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", entry.Name(), hex.EncodeToString(sum[:])); e != nil {
			t.Fatal(e)
		}
	}
	queries := []string{
		`INSERT INTO crypto_wallets(id,name,mode,xpub,path,receive_key,first_address,engine,engine_version,enabled,revision,next_index,created,backup_confirmed,recovery_required,secret,removed_at) VALUES('old','wallet','hot','public-xpub','m/path','public-receive','receive','trust-wallet-core','4.8.4',0,1,1,1,1,0,x'01',2)`,
		`INSERT INTO crypto_wallet_addresses VALUES('address','old',0,'m/path/0/0','receive','label',1)`,
		`INSERT INTO commerce_orders VALUES('order',1,'pending',1,1,2,x'01')`,
		`INSERT INTO payment_attempts(id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,idempotency_key,created,updated,expires,doc) VALUES('attempt','order',1,'crypto','awaiting_customer',1,'CNY','order','key',1,1,2,x'01')`,
		`INSERT INTO crypto_invoices(id,order_id,user_id,attempt_id,operation_id,fingerprint,wallet_id,address_id,chain_id,asset_id,address,contract,expected_atoms,state,created,expires,start_block,next_block,doc) VALUES('invoice','order',1,'attempt','operation','fingerprint','old','address',1,'usdc','receive','contract','100','waiting',1,2,0,0,x'01')`,
		`INSERT INTO crypto_invoice_chains VALUES('invoice',1,0,'')`,
		`INSERT INTO crypto_transfers VALUES(1,'tx',0,'invoice','contract','sender','receive','100',1,'hash',1,'confirmed','',1,1)`,
		`INSERT INTO crypto_funding_addresses VALUES('old','funding','m/path/1/0',1)`,
		`INSERT INTO crypto_sweep_jobs(id,actor_id,operation_id,fingerprint,wallet_id,chain_id,asset_id,destination,min_atoms,max_gas_atoms,state,doc,config,created,updated) VALUES('job',1,'sweep-op','fp','old',1,'usdc','dest','1','100','cancelled',x'01',x'02',1,1)`,
		`INSERT INTO crypto_sweep_items(id,job_id,address_id,address,path,amount_atoms,gas_needed_atoms,state) VALUES('item','job','address','receive','m/path/0/0','100','1','queued')`,
	}
	for _, q := range queries {
		if d.driver == "postgres" {
			q = strings.ReplaceAll(q, "x'01'", "decode('01','hex')")
			q = strings.ReplaceAll(q, "x'02'", "decode('02','hex')")
		}
		if _, e = d.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if e = d.Migrate(ctx); e != nil {
		t.Fatal("populated legacy ownership migration", e)
	}
	var hash string
	var next, deleted int64
	if e = d.QueryRow("SELECT receive_hash,next_index,deleted_at FROM crypto_wallet_history WHERE id='old'").Scan(&hash, &next, &deleted); e != nil || len(hash) != 64 || next != 1 || deleted != 2 {
		t.Fatal(hash, next, deleted, e)
	}
	if _, e = d.Exec("DELETE FROM crypto_wallets WHERE id='old'"); e != nil {
		t.Fatal("foreign keys still retain erased wallet entity", e)
	}
	for _, table := range []string{"crypto_wallet_addresses", "crypto_invoices", "crypto_invoice_chains", "crypto_transfers", "crypto_funding_addresses", "crypto_sweep_jobs", "crypto_sweep_items"} {
		var count int
		if e = d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil || count != 1 {
			t.Fatal("historical records lost", table, count, e)
		}
	}
	if _, e = d.Exec(`INSERT INTO crypto_transfers VALUES(1,'tx',0,'invoice','contract','sender','receive','100',1,'hash',1,'confirmed','',1,1)`); e == nil {
		t.Fatal("on-chain duplicate identity protection lost")
	}
	if e = d.Migrate(ctx); e != nil {
		t.Fatal("migration not idempotent", e)
	}
	if d.driver == "postgres" {
		if _, e := d.Exec("DELETE FROM crypto_wallet_history WHERE id='old'"); e == nil {
			t.Fatal("historical ownership foreign keys missing")
		}
		return
	}
	rows, e := d.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("historical references broken")
	}
}
