package persistence

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

var solanaSnapshotProof = []byte{0, 1, 2, 255, 42}

// A deleted wallet's public descriptors, address proofs and signed transfer
// receipts remain linked to its permanent history. No live wallet or seed is
// needed to exercise these recovery foreign keys and byte-preservation rules.
func solanaSnapshotFixture(t *testing.T, db *DB) {
	t.Helper()
	queries := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO crypto_wallet_history(id,receive_hash,next_index,deleted_at) VALUES('solar-wallet','1111111111111111111111111111111111111111111111111111111111111111',1,1)", nil},
		{"INSERT INTO crypto_solana_wallet_public(wallet_id,first_address,funding_address,funding_path,doc,created) VALUES('solar-wallet','public-first','public-funding','m/44''/501''/2147483647''/0''',?,1)", []any{solanaSnapshotProof}},
		{"INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES('solar-address','solar-wallet',0,'m/44''/501''/0''/0''','public-first','order history',1)", nil},
		{"INSERT INTO crypto_solana_address_proofs(address_id,doc) VALUES('solar-address',?)", []any{solanaSnapshotProof}},
		{"INSERT INTO crypto_sweep_jobs(id,actor_id,operation_id,fingerprint,wallet_id,chain_id,asset_id,destination,min_atoms,max_gas_atoms,state,doc,config,created,updated) VALUES('solar-job',1,'solar-operation','fingerprint','solar-wallet',101,'solana-usdc','public-destination','1','10000','complete',?,?,1,2)", []any{solanaSnapshotProof, solanaSnapshotProof}},
		{"INSERT INTO crypto_sweep_items(id,job_id,address_id,address,path,amount_atoms,gas_needed_atoms,state) VALUES('solar-item','solar-job','solar-address','public-first','m/44''/501''/0''/0''','1000000','0','complete')", nil},
		{"INSERT INTO crypto_solana_transactions(id,job_id,item_id,chain_id,signature,raw,message,intent,last_valid_height,state,receipt,created,updated) VALUES('solar-transaction','solar-job','solar-item',101,'public-signature',?,?,?,434000000,'confirmed',?,1,2)", []any{solanaSnapshotProof, solanaSnapshotProof, solanaSnapshotProof, solanaSnapshotProof}},
	}
	for _, query := range queries {
		if _, err := db.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSolanaSnapshot(t *testing.T, db *DB) {
	t.Helper()
	for _, query := range []string{
		"SELECT doc FROM crypto_solana_wallet_public WHERE wallet_id='solar-wallet'",
		"SELECT doc FROM crypto_solana_address_proofs WHERE address_id='solar-address'",
		"SELECT raw FROM crypto_solana_transactions WHERE id='solar-transaction'",
		"SELECT message FROM crypto_solana_transactions WHERE id='solar-transaction'",
		"SELECT intent FROM crypto_solana_transactions WHERE id='solar-transaction'",
		"SELECT receipt FROM crypto_solana_transactions WHERE id='solar-transaction'",
	} {
		var blob []byte
		if err := db.QueryRow(query).Scan(&blob); err != nil || !bytes.Equal(blob, solanaSnapshotProof) {
			t.Fatal("public proof/signed transaction changed or disappeared", query, blob, err)
		}
	}
	var deleted, height, chain int64
	var signature, state string
	if err := db.QueryRow("SELECT deleted_at FROM crypto_wallet_history WHERE id='solar-wallet'").Scan(&deleted); err != nil || deleted != 1 {
		t.Fatal("permanent wallet deletion lost", deleted, err)
	}
	if err := db.QueryRow("SELECT chain_id,signature,last_valid_height,state FROM crypto_solana_transactions WHERE id='solar-transaction'").Scan(&chain, &signature, &height, &state); err != nil || chain != 101 || signature != "public-signature" || height != 434000000 || state != "confirmed" {
		t.Fatal("signed transfer identity or expiry changed", chain, signature, height, state, err)
	}
	var liveWallets int
	if err := db.QueryRow("SELECT COUNT(*) FROM crypto_wallets").Scan(&liveWallets); err != nil || liveWallets != 0 {
		t.Fatal("snapshot resurrected a deleted wallet", liveWallets, err)
	}
}

func TestCryptoSolanaSnapshotCopiesDeletedWalletHistory(t *testing.T) {
	src, dst := sqliteTestDB(t), sqliteTestDB(t)
	solanaSnapshotFixture(t, src)
	ctx := context.Background()
	if err := Copy(ctx, src, dst, true); err != nil {
		t.Fatal("dependency-ordered SQLite Solana copy failed", err)
	}
	assertSolanaSnapshot(t, dst)
	if _, err := dst.Exec("UPDATE crypto_solana_wallet_public SET doc=?", []byte("changed")); err != nil {
		t.Fatal(err)
	}
	// Restore over populated data exercises reverse deletion dependencies too.
	if err := Copy(ctx, src, dst, false); err != nil {
		t.Fatal("dependency-ordered Solana restore failed", err)
	}
	assertSolanaSnapshot(t, dst)
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := src.Snapshot(ctx, path); err != nil {
		t.Fatal("SQLite Solana portable export failed", err)
	}
	portable, err := Open(ctx, filepath.Dir(path), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer portable.Close()
	assertSolanaSnapshot(t, portable)
	if err = Copy(ctx, portable, dst, false); err != nil {
		t.Fatal("SQLite Solana portable import failed", err)
	}
	assertSolanaSnapshot(t, dst)
}

func TestPostgresCryptoSolanaSnapshotRoundTrip(t *testing.T) {
	pg, restored := postgresTestDB(t), postgresTestDB(t)
	src := sqliteTestDB(t)
	solanaSnapshotFixture(t, src)
	ctx := context.Background()
	if err := Copy(ctx, src, pg, true); err != nil {
		t.Fatal("SQLite to fresh PostgreSQL lost Solana records", err)
	}
	assertSolanaSnapshot(t, pg)
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := pg.Snapshot(ctx, path); err != nil {
		t.Fatal("PostgreSQL Solana portable snapshot failed", err)
	}
	portable, err := Open(ctx, filepath.Dir(path), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer portable.Close()
	assertSolanaSnapshot(t, portable)
	if err = Copy(ctx, portable, restored, true); err != nil {
		t.Fatal("portable snapshot to fresh PostgreSQL lost Solana records", err)
	}
	assertSolanaSnapshot(t, restored)
	if err = Copy(ctx, portable, restored, false); err != nil {
		t.Fatal("PostgreSQL replacement lost Solana dependencies", err)
	}
	assertSolanaSnapshot(t, restored)
}
