package controlplane

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

func TestCryptoSweepIntegrityAndSnapshot(t *testing.T) {
	a, owner, wallet, f := cryptoSweepFixture(t)
	p := cryptoSweepTestPreview(t, a, owner, wallet)
	j := cryptoSweepTestCreate(t, a, owner, wallet, p)
	check := func() {
		t.Helper()
		if e := a.store.validateCommerce(true); e != nil {
			t.Fatal(e)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := a.store.db.Exec(query, args...); e != nil {
			t.Fatal(e)
		}
	}
	reject := func(name string, corrupt, restore func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			corrupt()
			defer restore()
			if e := a.store.validateCommerce(true); e == nil {
				t.Fatal("corruption accepted")
			}
			if e := a.store.validateCryptoSweeps(false); e == nil {
				t.Fatal("startup accepted corrupted sweep")
			}
		})
		check()
	}
	check()
	plan := p.Items[0]
	reject("missing_item", func() {
		exec("DELETE FROM crypto_sweep_items WHERE job_id=?", j.ID)
	}, func() {
		exec("INSERT INTO crypto_sweep_items(id,job_id,address_id,address,path,amount_atoms,gas_needed_atoms,state) VALUES(?,?,?,?,?,?,?,'queued')", j.Items[0].ID, j.ID, plan.AddressID, plan.Address, plan.Path, plan.AmountAtoms, plan.TopupAtoms)
	})
	var fundPath string
	var fundCreated int64
	if e := a.store.db.QueryRow("SELECT path,created FROM crypto_funding_addresses WHERE wallet_id=?", wallet.ID).Scan(&fundPath, &fundCreated); e != nil {
		t.Fatal(e)
	}
	reject("missing_funding", func() { exec("DELETE FROM crypto_funding_addresses WHERE wallet_id=?", wallet.ID) }, func() {
		exec("INSERT INTO crypto_funding_addresses(wallet_id,address,path,created) VALUES(?,?,?,?)", wallet.ID, f.funding, fundPath, fundCreated)
	})
	reject("funding_derivation", func() {
		exec("UPDATE crypto_funding_addresses SET path=? WHERE wallet_id=?", wallet.Path+"/0/0", wallet.ID)
	}, func() {
		exec("UPDATE crypto_funding_addresses SET path=? WHERE wallet_id=?", fundPath, wallet.ID)
	})
	reject("job_destination", func() { exec("UPDATE crypto_sweep_jobs SET destination=? WHERE id=?", f.receive, j.ID) }, func() {
		exec("UPDATE crypto_sweep_jobs SET destination=? WHERE id=?", p.Destination, j.ID)
	})
	sealed, e := base64.StdEncoding.DecodeString(p.Quote)
	if e != nil {
		t.Fatal(e)
	}
	reject("sealed_quote", func() { exec("UPDATE crypto_sweep_jobs SET config=? WHERE id=?", []byte("damaged"), j.ID) }, func() {
		exec("UPDATE crypto_sweep_jobs SET config=? WHERE id=?", sealed, j.ID)
	})
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	check() // Signed and broadcast, not yet final: reservation must survive restart.
	var transactionID, state string
	var nonce int64
	if e = a.store.db.QueryRow("SELECT id,state,nonce FROM crypto_chain_transactions WHERE job_id=?", j.ID).Scan(&transactionID, &state, &nonce); e != nil {
		t.Fatal(e)
	}
	reject("forged_final_state", func() {
		exec("UPDATE crypto_chain_transactions SET state='confirmed',block_number=101,block_hash=? WHERE id=?", cryptoFixtureHash(101), transactionID)
	}, func() {
		exec("UPDATE crypto_chain_transactions SET state=?,block_number=0,block_hash='' WHERE id=?", state, transactionID)
	})
	reject("signed_nonce", func() { exec("UPDATE crypto_chain_transactions SET nonce=? WHERE id=?", nonce+1, transactionID) }, func() {
		exec("UPDATE crypto_chain_transactions SET nonce=? WHERE id=?", nonce, transactionID)
	})
	f.mine(true)
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	check() // Confirmed gas evidence and the second durable transfer.
	f.mine(true)
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	check()
	result, e := a.store.cryptoSweepJob(j.ID)
	if e != nil || result.State != "complete" {
		t.Fatal(result, e)
	}
	var proof []byte
	var blockHash string
	if e = a.store.db.QueryRow("SELECT receipt,block_hash FROM crypto_chain_transactions WHERE id=?", transactionID).Scan(&proof, &blockHash); e != nil {
		t.Fatal(e)
	}
	reject("missing_final_proof", func() { exec("UPDATE crypto_chain_transactions SET receipt=? WHERE id=?", []byte{}, transactionID) }, func() {
		exec("UPDATE crypto_chain_transactions SET receipt=? WHERE id=?", proof, transactionID)
	})
	reject("damaged_final_proof", func() {
		exec("UPDATE crypto_chain_transactions SET receipt=? WHERE id=?", []byte("damaged"), transactionID)
	}, func() {
		exec("UPDATE crypto_chain_transactions SET receipt=? WHERE id=?", proof, transactionID)
	})
	reject("forged_final_block", func() {
		exec("UPDATE crypto_chain_transactions SET block_hash=? WHERE id=?", cryptoFixtureHash(999), transactionID)
	}, func() {
		exec("UPDATE crypto_chain_transactions SET block_hash=? WHERE id=?", blockHash, transactionID)
	})
	// A portable snapshot retains sealed receipts and is valid under the same key.
	dir := t.TempDir()
	if e = a.backupSnapshot(dir); e != nil {
		t.Fatal(e)
	}
	snapshot, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer snapshot.db.Close()
	if e = snapshot.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
	target := testApp(t)
	if e = persistence.Copy(context.Background(), snapshot.db, target.store.db, false); e != nil {
		t.Fatal(e)
	}
	key, e := os.ReadFile(filepath.Join(dir, "master.key"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(target.cfg.StateDir, "master.key"), key, 0600); e != nil {
		t.Fatal(e)
	}
	target.store.vault, e = openVault(target.cfg.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	if e = target.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoSweepIntegrityLegacyWalletAndCancelledFailure(t *testing.T) {
	a, owner, wallet, f := cryptoSweepFixture(t)
	// An upgraded v0.36 wallet had no funding row, and must remain usable.
	if _, e := a.store.db.Exec("DELETE FROM crypto_funding_addresses WHERE wallet_id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	if e := a.store.validateCommerce(true); e != nil {
		t.Fatal("legacy wallet rejected", e)
	}
	p := cryptoSweepTestPreview(t, a, owner, wallet) // First use recreates funding.
	j := cryptoSweepTestCreate(t, a, owner, wallet, p)
	if e := a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.revert = true
	f.mine(true)
	if e := a.cryptoSweepWork(context.Background()); e == nil {
		t.Fatal("expected reverted transaction")
	}
	if e := a.store.validateCommerce(true); e != nil {
		t.Fatal("legitimate failure rejected", e)
	}
	if e := a.cryptoCancelSweep(context.Background(), owner, j.ID, cryptoSweepInput{OperationID: randomToken(24)}); e != nil {
		t.Fatal(e)
	}
	if e := a.store.validateCommerce(true); e != nil {
		t.Fatal("legitimate cancellation rejected", e)
	}
}
