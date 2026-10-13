package controlplane

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func cryptoWalletRemoveInput(v CryptoWallet) object {
	return object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24), "delete_ack": true}
}

func TestCryptoWalletRemovalAuthorizationCASAndReplay(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	v, _ := cryptoTestCreate(t, a, owner, false)
	path := cryptoWalletPrefix + "wallets/" + v.ID + "/delete"
	in := cryptoWalletRemoveInput(v)
	if w := req(t, a, member, "POST", path, in); w.Code != 403 {
		t.Fatal("member removed wallet", w.Code)
	}
	in["password"] = "wrong"
	if w := req(t, a, owner, "POST", path, in); w.Code != 403 {
		t.Fatal("password not reauthenticated", w.Code)
	}
	in["password"] = commerceTestPassword
	in["delete_ack"] = false
	if w := req(t, a, owner, "POST", path, in); w.Code != 400 {
		t.Fatal("missing acknowledgement", w.Code)
	}
	in["delete_ack"] = true
	in["revision"] = v.Revision + 1
	if w := req(t, a, owner, "POST", path, in); w.Code != 409 {
		t.Fatal("stale removal accepted", w.Code)
	}
	in["revision"] = v.Revision
	removed := decoded[CryptoWallet](t, req(t, a, owner, "POST", path, in), 200)
	if removed.RemovedAt <= 0 || removed.Enabled || removed.Revision != v.Revision+1 {
		t.Fatal("invalid tombstone", removed)
	}
	replay := decoded[CryptoWallet](t, req(t, a, owner, "POST", path, in), 200)
	if replay.RemovedAt != removed.RemovedAt || replay.Revision != removed.Revision {
		t.Fatal("replay deleted twice", replay)
	}
	list := decoded[struct{ Items []CryptoWallet }](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets", nil), 200)
	if len(list.Items) != 0 {
		t.Fatal("removed wallet visible by default")
	}
	list = decoded[struct{ Items []CryptoWallet }](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets?deleted=true", nil), 200)
	if len(list.Items) != 1 || list.Items[0].ID != v.ID {
		t.Fatal("removed wallet missing")
	}
	if w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets?deleted=typo", nil); w.Code != 400 {
		t.Fatal("invalid filter", w.Code)
	}
	if w := req(t, a, owner, "POST", path, cryptoWalletRemoveInput(removed)); w.Code != 409 {
		t.Fatal("second distinct removal accepted", w.Code)
	}
	in["password"] = "wrong"
	if w := req(t, a, owner, "POST", path, in); w.Code != 403 {
		t.Fatal("replay bypassed password", w.Code)
	}
	if e := a.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletRemovalDisablesPaymentsAndGatesNewIntents(t *testing.T) {
	a, owner, wallet, _ := cryptoSweepFixture(t)
	addresses, e := cryptoWalletAddresses(a.store.db, wallet.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	secret, e := a.store.cryptoSecret(a.store.db, wallet)
	if e != nil {
		t.Fatal(e)
	}
	preview := cryptoSweepTestPreview(t, a, owner, wallet)
	removed := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", cryptoWalletRemoveInput(wallet)), 200)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil || s.Enabled || s.Revision != 2 {
		t.Fatal("active config not disabled atomically", s, e)
	}
	if cryptoWalletReady(removed) == nil || cryptoSweepWallet(removed) == nil {
		t.Fatal("removed wallet accepts new intents")
	}
	for _, action := range []string{"addresses", "status"} {
		in := object{"password": commerceTestPassword, "revision": removed.Revision, "operation_id": randomToken(24), "enabled": true}
		if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/"+action, in); w.Code != 409 {
			t.Fatal("removed action accepted", action, w.Code, w.Body.String())
		}
	}
	if _, e = a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, cryptoSweepTestInput()); e == nil {
		t.Fatal("removed preview allowed")
	}
	if _, e = a.cryptoCreateSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)}); e == nil {
		t.Fatal("pre-removal quote authorized new sweep")
	}
	current, e := cryptoWalletByID(a.store.db, wallet.ID)
	if e != nil || current.NextIndex != wallet.NextIndex {
		t.Fatal("removed high water changed", current, e)
	}
	got, e := cryptoWalletAddresses(a.store.db, wallet.ID, 0)
	if e != nil || len(got) != len(addresses) || got[0] != addresses[0] {
		t.Fatal("permanent address removed", got, e)
	}
	after, e := a.store.cryptoSecret(a.store.db, current)
	if e != nil || after.Mnemonic != secret.Mnemonic {
		t.Fatal("encrypted key lost", e)
	}
	if w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/addresses", nil); w.Code != 200 {
		t.Fatal("history inaccessible")
	}
	restoreIn := object{"password": commerceTestPassword, "revision": removed.Revision, "operation_id": randomToken(24)}
	restored := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/undelete", restoreIn), 200)
	if restored.RemovedAt != 0 || restored.Enabled || restored.NextIndex != wallet.NextIndex || restored.Revision != removed.Revision+1 {
		t.Fatal("restore did not stay paused", restored)
	}
	if replay := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/undelete", restoreIn), 200); replay.Revision != restored.Revision {
		t.Fatal("restore replay changed state")
	}
	s, e = a.store.cryptoPaymentSettings()
	if e != nil || s.Enabled {
		t.Fatal("undelete silently resumed payment", e)
	}
}

func TestCryptoWalletRemovalRejectsPendingSweepsAndPreservesInvoices(t *testing.T) {
	t.Run("pending sweep", func(t *testing.T) {
		a, owner, wallet, _ := cryptoSweepFixture(t)
		job := cryptoSweepTestCreate(t, a, owner, wallet, cryptoSweepTestPreview(t, a, owner, wallet))
		in := cryptoWalletRemoveInput(wallet)
		if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", in); w.Code != 409 {
			t.Fatal("pending sweep allowed deletion", w.Code)
		}
		if current := cryptoTestCurrent(t, a, wallet.ID); current.RemovedAt != 0 || !current.Enabled {
			t.Fatal("rejected removal changed wallet")
		}
		if e := a.cryptoCancelSweep(context.Background(), owner, job.ID, cryptoSweepInput{OperationID: randomToken(24)}); e != nil {
			t.Fatal(e)
		}
		decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", in), 200)
	})
	t.Run("invoice settlement", func(t *testing.T) {
		a, owner, user, wallet, f := cryptoPaymentFixture(t)
		order, invoice := cryptoPaymentInvoice(t, a, owner, user)
		wallet = cryptoTestCurrent(t, a, wallet.ID)
		removed := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", cryptoWalletRemoveInput(wallet)), 200)
		if removed.NextIndex != 1 {
			t.Fatal("invoice address highwater lost")
		}
		f.add(invoice, 101, invoice.ExpectedAtoms)
		f.mu.Lock()
		f.latest = 102
		f.final = 102
		f.mu.Unlock()
		internal, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
			t.Fatal("historical invoice stopped settlement", e)
		}
		for range 2 {
			if e = a.paymentWork(); e != nil {
				t.Fatal(e)
			}
			if e = a.commerceWork(time.Now().Unix()); e != nil {
				t.Fatal(e)
			}
		}
		paid, e := a.store.order(order.ID)
		if e != nil || paid.State != "completed" {
			t.Fatal("removed-wallet invoice not paid", paid.State, e)
		}
		if e = a.store.validateCommerce(true); e != nil {
			t.Fatal(e)
		}
		newOrder := newOrder(t, a, user, commerceOffer(t, a, owner))
		if w := req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": newOrder.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}); w.Code != 409 {
			t.Fatal("new invoice accepted after removal", w.Code, w.Body.String())
		}
		settings, e := a.store.cryptoPaymentSettings()
		if e != nil {
			t.Fatal(e)
		}
		settings.Chains[0].RPCURL = "https://one.example.com/rpc"
		settings.Chains[0].RPCBackupURL = "https://two.example.com/rpc"
		var enableInput object
		if e = json.Unmarshal(jsonBytes(settings), &enableInput); e != nil {
			t.Fatal(e)
		}
		enableInput["enabled"] = true
		enableInput["password"] = commerceTestPassword
		enableInput["operation_id"] = randomToken(24)
		if w := req(t, a, owner, "POST", "/api/commerce/crypto/admin/settings", enableInput); w.Code != 409 {
			t.Fatal("removed wallet reactivated through settings", w.Code, w.Body.String())
		}
	})
}

func TestCryptoWalletRemovalBackupRestoreAndSnapshotPreserveTombstone(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, true)
	wallet = cryptoTestConfirm(t, a, owner, wallet)
	wallet = cryptoTestAllocate(t, a, owner, wallet).Wallet
	oldBackup := cryptoTestExport(t, a, owner, wallet)
	removed := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", cryptoWalletRemoveInput(wallet)), 200)
	restored := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/restore", object{"password": commerceTestPassword, "operation_id": randomToken(24), "backup_password": cryptoBackupTestPassword, "backup": oldBackup}), 200)
	if restored.RemovedAt != removed.RemovedAt || restored.Enabled || restored.NextIndex != 1 {
		t.Fatal("old backup resurrected removed wallet", restored)
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/reveal", object{"password": commerceTestPassword}); w.Code != 409 {
		t.Fatal("removed seed reveal allowed", w.Code)
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/backup", object{"password": commerceTestPassword, "backup_password": cryptoBackupTestPassword}); w.Code != 409 {
		t.Fatal("removed wallet backup allowed", w.Code)
	}
	addresses, e := cryptoWalletAddresses(a.store.db, wallet.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	newBackup, e := cryptoEncryptBackup(cryptoWalletDump{Version: 1, Exported: wallet.Created, Wallet: restored, Addresses: addresses, Mnemonic: cryptoTestMnemonic}, cryptoBackupTestPassword)
	if e != nil {
		t.Fatal(e)
	}
	dump, e := cryptoDecryptBackup(newBackup, cryptoBackupTestPassword)
	if e != nil || dump.Wallet.RemovedAt != removed.RemovedAt {
		t.Fatal("backup omitted tombstone", e)
	}
	b := testApp(t)
	other := testUser(t, b, "owner", "owner")
	imported := decoded[CryptoWallet](t, req(t, b, other, "POST", cryptoWalletPrefix+"wallets/restore", object{"password": commerceTestPassword, "operation_id": randomToken(24), "backup_password": cryptoBackupTestPassword, "backup": newBackup}), 200)
	if imported.RemovedAt != removed.RemovedAt || imported.Enabled {
		t.Fatal("import resurrected removed wallet", imported)
	}
	dir := t.TempDir()
	if e = a.backupSnapshot(dir); e != nil {
		t.Fatal(e)
	}
	snapshot, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer snapshot.db.Close()
	saved, e := cryptoWalletByID(snapshot.db, wallet.ID)
	if e != nil || saved.RemovedAt != removed.RemovedAt || saved.NextIndex != 1 {
		t.Fatal("snapshot lost removal", saved, e)
	}
	if e = snapshot.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletConcurrentAllocationAndRemoval(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, false)
	b := cryptoIndependentApp(t, a)
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i, app := range []*App{a, b} {
		wg.Add(1)
		go func(i int, app *App) {
			defer wg.Done()
			action := "delete"
			in := cryptoWalletRemoveInput(wallet)
			if i == 1 {
				action = "addresses"
				in = object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24)}
			}
			responses <- req(t, app, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/"+action, in)
		}(i, app)
	}
	wg.Wait()
	close(responses)
	successes := 0
	for w := range responses {
		if w.Code == 200 || w.Code == 201 {
			successes++
		} else if w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal("invalid result")
		}
	}
	if successes != 1 {
		t.Fatal("concurrent stale operations both succeeded", successes)
	}
	current := cryptoTestCurrent(t, a, wallet.ID)
	rows, e := cryptoWalletAddresses(a.store.db, wallet.ID, 0)
	if e != nil || current.NextIndex != int64(len(rows)) {
		t.Fatal("concurrent highwater corrupt", current, e)
	}
	if e = a.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletRemovalMigrationPreservesExistingWallet(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, false)
	wallet = cryptoTestAllocate(t, a, owner, wallet).Wallet
	for _, statement := range []string{"DELETE FROM schema_migrations WHERE version='019_crypto_wallet_removal.sql'", "DROP INDEX crypto_wallets_removed_created", "ALTER TABLE crypto_wallets DROP COLUMN removed_at"} {
		if _, e := a.store.db.Exec(statement); e != nil {
			t.Fatal(e)
		}
	}
	if e := a.store.db.Migrate(context.Background()); e != nil {
		t.Fatal("existing installation upgrade", e)
	}
	current, e := cryptoWalletByID(a.store.db, wallet.ID)
	if e != nil || current.RemovedAt != 0 || !current.Enabled || current.NextIndex != 1 || current.XPub != wallet.XPub {
		t.Fatal("upgrade changed wallet identity/state", current, e)
	}
	if e = a.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletRemovalIntegrityRejectsIllicitReenable(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, false)
	decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", cryptoWalletRemoveInput(wallet)), 200)
	if _, e := a.store.db.Exec("UPDATE crypto_wallets SET enabled=1 WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	if e := a.store.validateCryptoWallets(false); e == nil {
		t.Fatal("tampered removed wallet enabled silently")
	}
}
