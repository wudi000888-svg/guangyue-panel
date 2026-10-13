package controlplane

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func cryptoWalletRemoveInput(v CryptoWallet) object {
	return object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24), "delete_ack": true, "permanent_delete_ack": true}
}
func cryptoWalletDeleteTest(t *testing.T, a *App, owner Record, v CryptoWallet) CryptoWalletDeletion {
	t.Helper()
	return decoded[CryptoWalletDeletion](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/delete", cryptoWalletRemoveInput(v)), 200)
}

func TestCryptoWalletPermanentDeletionRejectsLegacyConsentWithoutMutation(t *testing.T) {
	a, owner, wallet, _ := cryptoSweepFixture(t)
	path := cryptoWalletPrefix + "wallets/" + wallet.ID + "/delete"
	var seed []byte
	if e := a.store.db.QueryRow("SELECT secret FROM crypto_wallets WHERE id=?", wallet.ID).Scan(&seed); e != nil || len(seed) == 0 {
		t.Fatal("hot wallet seed unavailable", e)
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil || !settings.Enabled || settings.WalletID != wallet.ID {
		t.Fatal("collection fixture unavailable", e)
	}
	type historyState struct {
		hash          string
		next, deleted int64
	}
	history := func() historyState {
		t.Helper()
		var out historyState
		if e := a.store.db.QueryRow("SELECT receive_hash,next_index,deleted_at FROM crypto_wallet_history WHERE id=?", wallet.ID).Scan(&out.hash, &out.next, &out.deleted); e != nil {
			t.Fatal(e)
		}
		return out
	}
	before := history()
	in := object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "delete_ack": true}
	for _, payload := range []object{in, {"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "delete_ack": false, "permanent_delete_ack": true}} {
		w := req(t, a, owner, "POST", path, payload)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "刷新") || !strings.Contains(w.Body.String(), "永久") {
			t.Fatal("legacy or incomplete consent accepted", w.Code, w.Body.String())
		}
		var retainedSeed []byte
		if e = a.store.db.QueryRow("SELECT secret FROM crypto_wallets WHERE id=?", wallet.ID).Scan(&retainedSeed); e != nil || !bytes.Equal(seed, retainedSeed) {
			t.Fatal("unconfirmed deletion changed encrypted seed", e)
		}
		currentSettings, e := a.store.cryptoPaymentSettings()
		if e != nil || !reflect.DeepEqual(settings, currentSettings) || !reflect.DeepEqual(wallet, cryptoTestCurrent(t, a, wallet.ID)) || before != history() {
			t.Fatal("unconfirmed deletion changed wallet, settings or history", e)
		}
	}
	in["permanent_delete_ack"] = true
	deleted := decoded[CryptoWalletDeletion](t, req(t, a, owner, "POST", path, in), 200)
	if deleted.ID != wallet.ID || !deleted.Deleted {
		t.Fatal("confirmed deletion failed", deleted)
	}
	if replay := decoded[CryptoWalletDeletion](t, req(t, a, owner, "POST", path, in), 200); replay != deleted {
		t.Fatal("confirmed deletion is not idempotent")
	}
	delete(in, "permanent_delete_ack")
	if w := req(t, a, owner, "POST", path, in); w.Code != 400 {
		t.Fatal("legacy consent bypassed acknowledgement through replay", w.Code)
	}
}

func TestCryptoWalletLegacyOperationReceiptReplayAndPermanentDeletion(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, payload := cryptoTestCreate(t, a, owner, false)
	var in cryptoWalletInput
	if e := json.Unmarshal(jsonBytes(payload), &in); e != nil {
		t.Fatal(e)
	}
	in.Password = ""
	// v0.39.17 serialized neither of the new intent fields. Preserve the
	// original struct field order when reconstructing its keyed receipt.
	legacy := bytes.ReplaceAll(jsonBytes(in), []byte(`,"import":false`), nil)
	legacy = bytes.ReplaceAll(legacy, []byte(`,"permanent_delete_ack":false`), nil)
	if bytes.Contains(legacy, []byte(`"import"`)) || bytes.Contains(legacy, []byte(`"permanent_delete_ack"`)) {
		t.Fatal("fixture does not represent the legacy request shape")
	}
	var sealed, key []byte
	if e := a.store.db.QueryRow("SELECT hmac_secret FROM crypto_wallet_state WHERE id=1").Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	if e := a.store.vault.open(sealed, &key); e != nil || len(key) != 32 {
		t.Fatal("operation key unavailable", e)
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	mac.Write(jsonBytes([]any{"wallets/xpub", json.RawMessage(legacy)}))
	if _, e := a.store.db.Exec("UPDATE crypto_wallet_operations SET fingerprint=? WHERE id=?", hex.EncodeToString(mac.Sum(nil)), in.OperationID); e != nil {
		t.Fatal(e)
	}
	replay := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/xpub", payload), 200)
	if replay.ID != wallet.ID || replay.Revision != wallet.Revision {
		t.Fatal("legacy replay mutated the active wallet")
	}
	cryptoWalletDeleteTest(t, a, owner, wallet)
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/xpub", payload); w.Code != 410 || strings.Contains(w.Body.String(), wallet.XPub) {
		t.Fatal("legacy replay did not honor permanent deletion", w.Code)
	}
}

func TestCryptoWalletRemovalAuthorizationCASAndReplay(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	v, createInput := cryptoTestCreate(t, a, owner, false)
	path := cryptoWalletPrefix + "wallets/" + v.ID + "/delete"
	in := cryptoWalletRemoveInput(v)
	if w := req(t, a, member, "POST", path, in); w.Code != 403 {
		t.Fatal("member deleted wallet", w.Code)
	}
	in["password"] = "wrong"
	if w := req(t, a, owner, "POST", path, in); w.Code != 403 {
		t.Fatal("password bypass", w.Code)
	}
	in["password"] = commerceTestPassword
	in["delete_ack"] = false
	if w := req(t, a, owner, "POST", path, in); w.Code != 400 {
		t.Fatal("missing permanent acknowledgement", w.Code)
	}
	in["delete_ack"] = true
	in["revision"] = v.Revision + 1
	if w := req(t, a, owner, "POST", path, in); w.Code != 409 {
		t.Fatal("CAS bypass", w.Code)
	}
	in["revision"] = v.Revision
	deleted := decoded[CryptoWalletDeletion](t, req(t, a, owner, "POST", path, in), 200)
	if deleted.ID != v.ID || !deleted.Deleted {
		t.Fatal(deleted)
	}
	if replay := decoded[CryptoWalletDeletion](t, req(t, a, owner, "POST", path, in), 200); replay != deleted {
		t.Fatal("non-idempotent deletion")
	}
	var count int
	if e := a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_wallets WHERE id=?", v.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("entity/seed retained", count, e)
	}
	list := decoded[struct{ Items []CryptoWallet }](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets", nil), 200)
	if len(list.Items) != 0 {
		t.Fatal("deleted wallet listed")
	}
	if w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets?deleted=true", nil); w.Code != 400 {
		t.Fatal("recoverable list exposed")
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/undelete", in); w.Code != 404 {
		t.Fatal("undelete available")
	}
	old := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/xpub", createInput)
	if old.Code != 410 || strings.Contains(old.Body.String(), v.XPub) {
		t.Fatal("old replay exposed descriptor", old.Code)
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
	preview := cryptoSweepTestPreview(t, a, owner, wallet)
	cryptoWalletDeleteTest(t, a, owner, wallet)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil || s.Enabled || s.WalletID != "" || s.Revision != 2 {
		t.Fatal("collection config not cleared", s, e)
	}
	for _, action := range []string{"addresses", "status", "reveal", "undelete", "backup"} {
		in := object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "enabled": true, "backup_password": cryptoBackupTestPassword}
		if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/"+action, in); w.Code != 404 {
			t.Fatal("deleted action available", action, w.Code, w.Body.String())
		}
	}
	if _, e = a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, cryptoSweepTestInput()); e == nil {
		t.Fatal("deleted preview")
	}
	if _, e = a.cryptoCreateSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)}); e == nil {
		t.Fatal("old quote created sweep")
	}
	history, e := cryptoWalletAddresses(a.store.db, wallet.ID, 0)
	if e != nil || len(history) != len(addresses) || history[0] != addresses[0] {
		t.Fatal("address occupancy lost", e)
	}
	if e = a.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletRemovalRejectsPendingSweepsAndPreservesInvoices(t *testing.T) {
	t.Run("pending sweep", func(t *testing.T) {
		a, owner, wallet, _ := cryptoSweepFixture(t)
		job := cryptoSweepTestCreate(t, a, owner, wallet, cryptoSweepTestPreview(t, a, owner, wallet))
		if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", cryptoWalletRemoveInput(wallet)); w.Code != 409 {
			t.Fatal("in-flight job deleted", w.Code)
		}
		if e := a.cryptoCancelSweep(context.Background(), owner, job.ID, cryptoSweepInput{OperationID: randomToken(24)}); e != nil {
			t.Fatal(e)
		}
		cryptoWalletDeleteTest(t, a, owner, wallet)
		if e := a.store.validateCryptoSweeps(true); e != nil {
			t.Fatal("archived proof failed", e)
		}
	})
	t.Run("invoice settlement", func(t *testing.T) {
		a, owner, user, wallet, f := cryptoPaymentFixture(t)
		order, invoice := cryptoPaymentInvoice(t, a, owner, user)
		wallet = cryptoTestCurrent(t, a, wallet.ID)
		cryptoWalletDeleteTest(t, a, owner, wallet)
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
			t.Fatal(e)
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
			t.Fatal(paid.State, e)
		}
		if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
			t.Fatal(e)
		}
		var n int
		if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_transfers WHERE invoice_id=?", invoice.ID).Scan(&n); e != nil || n != 1 {
			t.Fatal("dedup lost", n, e)
		}
		if e = a.store.validateCommerce(true); e != nil {
			t.Fatal(e)
		}
	})
}

func TestCryptoWalletRemovalBackupRestoreAndSnapshotPreserveTombstone(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, input := cryptoTestCreate(t, a, owner, true)
	wallet = cryptoTestConfirm(t, a, owner, wallet)
	allocation := cryptoTestAllocate(t, a, owner, wallet)
	wallet = allocation.Wallet
	backup := cryptoTestExport(t, a, owner, wallet)
	cryptoWalletDeleteTest(t, a, owner, wallet)
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/restore", object{"password": commerceTestPassword, "operation_id": randomToken(24), "backup_password": cryptoBackupTestPassword, "backup": backup}); w.Code != 409 {
		t.Fatal("old backup revived original ID", w.Code, w.Body.String())
	}
	input["operation_id"] = randomToken(24)
	input["import"] = true
	newWallet := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/hot", input), 201)
	if newWallet.ID == wallet.ID || newWallet.NextIndex != 1 || newWallet.Enabled || !newWallet.RecoveryRequired || newWallet.FundingAddress != wallet.FundingAddress {
		t.Fatal("import bypassed highwater/pause", newWallet)
	}
	newWallet = cryptoTestConfirm(t, a, owner, newWallet)
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+newWallet.ID+"/addresses", object{"password": commerceTestPassword, "revision": newWallet.Revision, "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal("unreviewed import allocated")
	}
	newWallet = decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+newWallet.ID+"/recovery", object{"password": commerceTestPassword, "revision": newWallet.Revision, "operation_id": randomToken(24), "next_index": 1, "recovery_ack": true}), 200)
	newWallet = decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+newWallet.ID+"/status", object{"password": commerceTestPassword, "revision": newWallet.Revision, "operation_id": randomToken(24), "enabled": true}), 200)
	next := cryptoTestAllocate(t, a, owner, newWallet)
	if next.Address.Index != 1 || next.Address.Address == allocation.Address.Address {
		t.Fatal("old address reused")
	}
	dir := t.TempDir()
	if e := a.backupSnapshot(dir); e != nil {
		t.Fatal(e)
	}
	saved, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer saved.db.Close()
	if _, e = cryptoWalletByID(saved.db, wallet.ID); e == nil {
		t.Fatal("snapshot revived old wallet")
	}
	if e = saved.validateCommerce(true); e != nil {
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
	success := 0
	for w := range responses {
		if w.Code == 200 || w.Code == 201 {
			success++
		} else if w.Code != 409 && w.Code != 404 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if success != 1 {
		t.Fatal("two stale operations won")
	}
	if e := a.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletRemovalMigrationPreservesExistingWallet(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, true)
	wallet = cryptoTestConfirm(t, a, owner, wallet)
	wallet = cryptoTestAllocate(t, a, owner, wallet).Wallet
	if _, e := a.store.db.Exec("UPDATE crypto_wallets SET enabled=0,removed_at=? WHERE id=?", time.Now().Unix(), wallet.ID); e != nil {
		t.Fatal(e)
	}
	reopened := cryptoIndependentApp(t, a)
	if _, e := cryptoWalletByID(reopened.store.db, wallet.ID); e == nil {
		t.Fatal("legacy soft-delete seed not erased")
	}
	if e := reopened.store.migratePermanentCryptoWalletDeletes(); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := reopened.store.db.QueryRow("SELECT COUNT(*) FROM crypto_wallet_addresses WHERE wallet_id=?", wallet.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("legacy occupancy lost", count, e)
	}
	if e := reopened.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletRemovalIntegrityRejectsIllicitReenable(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, input := cryptoTestCreate(t, a, owner, true)
	cryptoWalletDeleteTest(t, a, owner, wallet)
	input["operation_id"] = randomToken(24)
	// A known descriptor must stay paused even when an old client omits the
	// explicit import flag and its previous wallet had no allocated addresses.
	input["import"] = false
	fresh := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/hot", input), 201)
	if fresh.Enabled || !fresh.RecoveryRequired {
		t.Fatal("seed import skipped recovery")
	}
	var hash string
	if e := a.store.db.QueryRow("SELECT receive_hash FROM crypto_wallet_history WHERE id=?", wallet.ID).Scan(&hash); e != nil || len(hash) != 64 || hash == wallet.XPub {
		t.Fatal("tombstone kept reusable descriptor", e)
	}
}

func TestCryptoWalletPermanentImportCanUseHistoricalFundsWithScopeIsolation(t *testing.T) {
	a, owner, old, _ := cryptoSweepFixture(t)
	addresses, e := cryptoWalletAddresses(a.store.db, old.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	cryptoWalletDeleteTest(t, a, owner, old)
	in := object{"name": "Explicit import", "password": commerceTestPassword, "mnemonic": cryptoTestMnemonic, "xpub": old.XPub, "path": old.Path, "first_address": old.FirstAddress, "engine_version": "4.8.4", "risk_ack": true, "import": true, "operation_id": randomToken(24)}
	fresh := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/hot", in), 201)
	fresh = cryptoTestConfirm(t, a, owner, fresh)
	fresh = decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+fresh.ID+"/recovery", object{"password": commerceTestPassword, "revision": fresh.Revision, "operation_id": randomToken(24), "next_index": old.NextIndex, "recovery_ack": true}), 200)
	portfolio, e := a.cryptoWalletPortfolio(context.Background(), fresh.ID, "ethereum", -1, "funded")
	if e != nil || portfolio.TotalAddresses != 1 || len(portfolio.Items) != 1 || portfolio.Items[0].AddressID != addresses[0].ID || portfolio.Tokens[1].BalanceAtoms != "2000000" {
		t.Fatal("import hid historical funds", portfolio, e)
	}
	input := cryptoSweepTestInput()
	input.AddressIDs = []string{addresses[0].ID}
	preview, e := a.cryptoPreviewSweep(context.Background(), owner, fresh.ID, input)
	if e != nil || !preview.CanSubmit || len(preview.Items) != 1 {
		t.Fatal("historical address cannot be withdrawn", preview, e)
	}
	job := cryptoSweepTestCreate(t, a, owner, fresh, preview)
	if job.WalletID != fresh.ID {
		t.Fatal("new signer ownership wrong")
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal("same descriptor signed plan invalid", e)
	}
	xpub, path, _, e := cryptoHDFromMnemonic("legal winner thank year wave sausage worth useful legal winner thank yellow")
	if e != nil {
		t.Fatal(e)
	}
	other := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/xpub", object{"name": "Other descriptor", "password": commerceTestPassword, "xpub": xpub, "path": path, "operation_id": randomToken(24)}), 201)
	other = cryptoTestAllocate(t, a, owner, other).Wallet
	otherAddresses, e := cryptoWalletAddresses(a.store.db, other.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	input.AddressIDs = []string{otherAddresses[0].ID}
	if _, e = a.cryptoPreviewSweep(context.Background(), owner, fresh.ID, input); e == nil {
		t.Fatal("cross-descriptor address accepted")
	}
	if _, e = a.store.db.Exec("UPDATE crypto_wallet_addresses SET path=? WHERE id=?", "m/44'/60'/0'/0/99", addresses[0].ID); e != nil {
		t.Fatal(e)
	}
	input.AddressIDs = []string{addresses[0].ID}
	if _, e = a.cryptoPreviewSweep(context.Background(), owner, fresh.ID, input); e == nil {
		t.Fatal("tampered historical path got Gas approval")
	}
	if e = a.store.validateCryptoWallets(true); e == nil {
		t.Fatal("tampered historical derivation ignored")
	}
}

func TestCryptoWalletPermanentDeletionRequiresDurableTombstone(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, false)
	// A watch-only wallet with no allocations has no child FK yet. Simulate
	// external corruption: seed/entity deletion must fail if it cannot record
	// the irreversible backup/high-water tombstone first.
	if _, e := a.store.db.Exec("DELETE FROM crypto_wallet_history WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", cryptoWalletRemoveInput(wallet)); w.Code != 422 {
		t.Fatal("missing tombstone allowed deletion", w.Code, w.Body.String())
	}
	if current, e := cryptoWalletByID(a.store.db, wallet.ID); e != nil || current.ID != wallet.ID {
		t.Fatal("failed invariant still erased entity", e)
	}
}

func TestCryptoWalletForeignMnemonicImportStartsPaused(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	xpub, path, first, e := cryptoHDFromMnemonic(cryptoTestMnemonic)
	if e != nil {
		t.Fatal(e)
	}
	input := object{"name": "Foreign seed", "password": commerceTestPassword, "mnemonic": cryptoTestMnemonic, "xpub": xpub, "path": path, "first_address": first, "engine_version": "4.8.4", "risk_ack": true, "import": true, "operation_id": randomToken(24)}
	wallet := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/hot", input), 201)
	if wallet.Enabled || !wallet.RecoveryRequired || wallet.NextIndex != 0 {
		t.Fatal("foreign import implicitly trusted unused indices", wallet)
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/status", object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "enabled": true}); w.Code != 409 {
		t.Fatal("unreviewed seed import enabled", w.Code)
	}
}
