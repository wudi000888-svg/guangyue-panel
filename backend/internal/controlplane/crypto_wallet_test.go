package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

const cryptoBackupTestPassword = "fixture-backup-password-2026"

func cryptoTestCreate(t *testing.T, a *App, owner Record, hot bool) (CryptoWallet, object) {
	t.Helper()
	xp, path, first, e := cryptoHDFromMnemonic(cryptoTestMnemonic)
	if e != nil {
		t.Fatal(e)
	}
	in := object{"name": "收款钱包", "xpub": xp, "path": path, "password": commerceTestPassword, "operation_id": randomToken(24)}
	route := "wallets/xpub"
	if hot {
		route = "wallets/hot"
		in["mnemonic"] = cryptoTestMnemonic
		in["first_address"] = first
		in["engine_version"] = "4.8.4"
		in["risk_ack"] = true
	}
	v := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+route, in), 201)
	return v, in
}
func cryptoTestExport(t *testing.T, a *App, owner Record, v CryptoWallet) cryptoWalletBackup {
	t.Helper()
	return decoded[cryptoWalletBackup](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/backup", object{"password": commerceTestPassword, "backup_password": cryptoBackupTestPassword}), 200)
}
func cryptoTestConfirm(t *testing.T, a *App, owner Record, v CryptoWallet) CryptoWallet {
	t.Helper()
	cryptoTestExport(t, a, owner, v)
	return decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/backup/confirm", object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24)}), 200)
}

type cryptoAllocationResult struct {
	Wallet  CryptoWallet        `json:"wallet"`
	Address CryptoWalletAddress `json:"address"`
}

func cryptoTestAllocate(t *testing.T, a *App, owner Record, v CryptoWallet) cryptoAllocationResult {
	t.Helper()
	return decoded[cryptoAllocationResult](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24), "label": "订单测试"}), 201)
}
func cryptoTestCurrent(t *testing.T, a *App, id string) CryptoWallet {
	t.Helper()
	v, e := cryptoWalletByID(a.store.db, id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestCryptoWalletPermissionsSecretsAndBackup(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	v, in := cryptoTestCreate(t, a, owner, true)
	if v.BackupConfirmed || v.NextIndex != 0 || v.Mode != "hot" {
		t.Fatal(v)
	}
	for _, endpoint := range []string{"wallets", "wallets/" + v.ID + "/addresses"} {
		if w := req(t, a, user, "GET", cryptoWalletPrefix+endpoint, nil); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/reveal", object{"password": "wrong"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/backup/confirm", object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal(w.Code)
	}
	replay := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/hot", in)
	if replay.Code != 200 || strings.Contains(replay.Body.String(), cryptoTestMnemonic) {
		t.Fatal(replay.Code, replay.Body.String())
	}
	for _, endpoint := range []string{"wallets", "wallets/" + v.ID + "/addresses"} {
		w := req(t, a, owner, "GET", cryptoWalletPrefix+endpoint, nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), cryptoTestMnemonic) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	backup := cryptoTestExport(t, a, owner, v)
	if strings.Contains(backup.Data, cryptoTestMnemonic) {
		t.Fatal("plain secret in backup")
	}
	dump, e := cryptoDecryptBackup(backup, cryptoBackupTestPassword)
	if e != nil || dump.Mnemonic != cryptoTestMnemonic {
		t.Fatal(e)
	}
	if _, e = cryptoDecryptBackup(backup, "incorrect-backup-password"); e == nil {
		t.Fatal("wrong password accepted")
	}
	raw, _ := base64.StdEncoding.DecodeString(backup.Data)
	var box cryptoBackupCipher
	json.Unmarshal(raw, &box)
	box.Ciphertext[0] ^= 1
	tampered := cryptoWalletBackup{Format: backup.Format, Data: base64.StdEncoding.EncodeToString(jsonBytes(box))}
	if _, e = cryptoDecryptBackup(tampered, cryptoBackupTestPassword); e == nil {
		t.Fatal("tampered backup accepted")
	}
	v = decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/backup/confirm", object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24)}), 200)
	result := cryptoTestAllocate(t, a, owner, v)
	if result.Address.Index != 0 || result.Address.Address != v.FirstAddress {
		t.Fatal(result)
	}
	reveal := decoded[struct {
		Mnemonic string `json:"mnemonic"`
	}](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/reveal", object{"password": commerceTestPassword}), 200)
	if reveal.Mnemonic != cryptoTestMnemonic {
		t.Fatal("reveal mismatch")
	}
	for _, query := range []string{"SELECT secret FROM crypto_wallets", "SELECT response FROM crypto_wallet_operations", "SELECT fingerprint FROM crypto_wallet_operations", "SELECT action || target FROM audit"} {
		rows, e := a.store.db.Query(query)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				t.Fatal(e)
			}
			if bytes.Contains(b, []byte(cryptoTestMnemonic)) || bytes.Contains(b, []byte(commerceTestPassword)) || bytes.Contains(b, []byte(cryptoBackupTestPassword)) {
				t.Fatal("secret persisted in public storage")
			}
		}
		rows.Close()
	}
	if e = a.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}
func TestCryptoWalletCanonicalDedupAndValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v, in := cryptoTestCreate(t, a, owner, true)
	branch, e := cryptoReceiveXPub(v.XPub, v.Path)
	if e != nil {
		t.Fatal(e)
	}
	duplicate := object{"name": "duplicate", "xpub": branch, "path": v.Path + "/0", "password": commerceTestPassword, "operation_id": randomToken(24)}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/xpub", duplicate); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	in["name"] = "changed"
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/hot", in); w.Code != 409 {
		t.Fatal("conflicting operation id", w.Code)
	}
	b := testApp(t)
	other := testUser(t, b, "owner", "owner")
	in["operation_id"] = randomToken(24)
	in["first_address"] = "0x0000000000000000000000000000000000000000"
	if w := req(t, b, other, "POST", cryptoWalletPrefix+"wallets/hot", in); w.Code != 400 {
		t.Fatal(w.Code)
	}
	in["first_address"] = v.FirstAddress
	in["mnemonic"] = "invalid mnemonic"
	if w := req(t, b, other, "POST", cryptoWalletPrefix+"wallets/hot", in); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func cryptoIndependentApp(t *testing.T, a *App) *App {
	t.Helper()
	cfg := Config{StateDir: a.cfg.StateDir}
	if a.store.db.Driver() == "postgres" {
		cfg.Edition = "pro"
		cfg.SiteID = strings.TrimPrefix(a.store.db.Schema(), "gy_")
		cfg.Database = persistence.Options{Driver: "postgres", DSN: os.Getenv("GY_TEST_POSTGRES_DSN")}
	}
	s, e := openConfiguredStore(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.db.Close() })
	return &App{store: s, cfg: a.cfg}
}
func TestCryptoWalletIndependentInstancesReplayAndRestart(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v, _ := cryptoTestCreate(t, a, owner, false)
	b := cryptoIndependentApp(t, a)
	in := object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24), "label": "same request"}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, app := range []*App{a, b} {
		wg.Add(1)
		go func(app *App) {
			defer wg.Done()
			results <- req(t, app, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", in)
		}(app)
	}
	wg.Wait()
	close(results)
	id := ""
	for w := range results {
		if w.Code != 200 && w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var r cryptoAllocationResult
		json.Unmarshal(w.Body.Bytes(), &r)
		if id != "" && r.Address.ID != id {
			t.Fatal("duplicate allocation")
		}
		id = r.Address.ID
	}
	v = cryptoTestCurrent(t, a, v.ID)
	if v.NextIndex != 1 {
		t.Fatal(v)
	}
	restarted := cryptoIndependentApp(t, a)
	result := cryptoTestAllocate(t, restarted, owner, v)
	if result.Address.Index != 1 {
		t.Fatal("restart reused index")
	}
	paused := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/status", object{"password": commerceTestPassword, "revision": result.Wallet.Revision, "operation_id": randomToken(24), "enabled": false}), 200)
	if w := req(t, b, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", object{"password": commerceTestPassword, "revision": paused.Revision, "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if e := restarted.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
}
func TestCryptoWalletRestoreHighWaterAndRecoveryGate(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v, _ := cryptoTestCreate(t, a, owner, true)
	v = cryptoTestConfirm(t, a, owner, v)
	v = cryptoTestAllocate(t, a, owner, v).Wallet
	backup := cryptoTestExport(t, a, owner, v)
	v = cryptoTestAllocate(t, a, owner, v).Wallet
	in := object{"password": commerceTestPassword, "backup_password": cryptoBackupTestPassword, "backup": backup, "operation_id": randomToken(24)}
	restored := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/restore", in), 200)
	if restored.NextIndex != 2 || restored.RecoveryRequired {
		t.Fatal("old backup rewound live state", restored)
	}
	if r := cryptoTestAllocate(t, a, owner, restored); r.Address.Index != 2 {
		t.Fatal(r)
	}
	b := testApp(t)
	ownerB := testUser(t, b, "owner", "owner")
	in["operation_id"] = randomToken(24)
	restored = decoded[CryptoWallet](t, req(t, b, ownerB, "POST", cryptoWalletPrefix+"wallets/restore", in), 200)
	if !restored.RecoveryRequired || restored.Enabled || !restored.BackupConfirmed || restored.NextIndex != 1 {
		t.Fatal(restored)
	}
	enabled := decoded[CryptoWallet](t, req(t, b, ownerB, "POST", cryptoWalletPrefix+"wallets/"+restored.ID+"/status", object{"password": commerceTestPassword, "revision": restored.Revision, "operation_id": randomToken(24), "enabled": true}), 200)
	if w := req(t, b, ownerB, "POST", cryptoWalletPrefix+"wallets/"+restored.ID+"/addresses", object{"password": commerceTestPassword, "revision": enabled.Revision, "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal("recovery gate bypass", w.Code)
	}
	recovery := object{"password": commerceTestPassword, "revision": enabled.Revision, "operation_id": randomToken(24), "next_index": 0, "recovery_ack": true}
	if w := req(t, b, ownerB, "POST", cryptoWalletPrefix+"wallets/"+restored.ID+"/recovery", recovery); w.Code != 400 {
		t.Fatal(w.Code)
	}
	recovery["next_index"] = 10
	restored = decoded[CryptoWallet](t, req(t, b, ownerB, "POST", cryptoWalletPrefix+"wallets/"+restored.ID+"/recovery", recovery), 200)
	if restored.RecoveryRequired || restored.Enabled || restored.NextIndex != 10 {
		t.Fatal(restored)
	}
	restored = decoded[CryptoWallet](t, req(t, b, ownerB, "POST", cryptoWalletPrefix+"wallets/"+restored.ID+"/status", object{"password": commerceTestPassword, "revision": restored.Revision, "operation_id": randomToken(24), "enabled": true}), 200)
	if r := cryptoTestAllocate(t, b, ownerB, restored); r.Address.Index != 10 {
		t.Fatal("high water reused", r)
	}
	if e := b.store.validateCryptoWallets(true); e != nil {
		t.Fatal(e)
	}
	freshBackup := cryptoTestExport(t, b, ownerB, restored)
	dump, e := cryptoDecryptBackup(freshBackup, cryptoBackupTestPassword)
	if e != nil {
		t.Fatal(e)
	}
	if e = cryptoValidateDump(dump); e != nil {
		t.Fatal("burned indices invalidated backup", e)
	}
}
func TestCryptoWalletSnapshotIntegrityAndPagination(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v, _ := cryptoTestCreate(t, a, owner, true)
	v = cryptoTestConfirm(t, a, owner, v)
	// Populate allocations transactionally, then exercise paging and integrity
	// without hundreds of unrelated session writes/password hashes.
	tx, e := a.store.cryptoWalletTx(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for i := int64(0); i < 205; i++ {
		address, e := cryptoDeriveAddress(v.XPub, v.Path, uint32(i))
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", fmt.Sprintf("fixture-%d", i), v.ID, i, cryptoFullPath(v.Path, i), address, "fixture", v.Created)
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx.Exec("UPDATE crypto_wallets SET next_index=205,revision=revision+1 WHERE id=?", v.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	type page struct {
		Items []CryptoWalletAddress `json:"items"`
		Next  *int64                `json:"next_after"`
	}
	p := decoded[page](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", nil), 200)
	if len(p.Items) != 200 || p.Next == nil || *p.Next != 199 {
		t.Fatal("first page", p.Next, len(p.Items))
	}
	p = decoded[page](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses?after=199", nil), 200)
	if len(p.Items) != 5 || p.Next != nil {
		t.Fatal("second page")
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
	if e = snapshot.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
	current, e := cryptoWalletByID(snapshot.db, v.ID)
	if e != nil || current.NextIndex != 205 {
		t.Fatal(e, current)
	}
	secret, e := snapshot.cryptoSecret(snapshot.db, current)
	if e != nil || secret.Mnemonic != cryptoTestMnemonic {
		t.Fatal("snapshot secret lost", e)
	}
	// Portable PostgreSQL -> SQLite -> another database copies the singleton,
	// operation fingerprints, encrypted keys and allocations as one unit.
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
	if _, e = target.store.db.Exec("DELETE FROM crypto_wallet_addresses WHERE wallet_id=? AND address_index=204", v.ID); e != nil {
		t.Fatal(e)
	}
	if e = target.store.validateCommerce(true); e == nil {
		t.Fatal("missing allocation accepted")
	}
	// Restore the valid source before checking independent address corruption.
	if e = persistence.Copy(context.Background(), snapshot.db, target.store.db, false); e != nil {
		t.Fatal(e)
	}
	if _, e = snapshot.db.Exec("UPDATE crypto_wallets SET secret=? WHERE id=?", []byte("corrupted"), v.ID); e != nil {
		t.Fatal(e)
	}
	if e = snapshot.validateCommerce(true); e == nil {
		t.Fatal("corrupt seed accepted")
	}
	if _, e = target.store.db.Exec("UPDATE crypto_wallet_addresses SET address=? WHERE wallet_id=? AND address_index=1", "0x0000000000000000000000000000000000000000", v.ID); e != nil {
		t.Fatal(e)
	}
	if e = target.store.validateCommerce(true); e == nil {
		t.Fatal("corrupt address accepted")
	}
}

func TestCryptoWalletAllocationRollbackAndStaleRevision(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v, _ := cryptoTestCreate(t, a, owner, false)
	if _, e := a.store.db.Exec("CREATE TRIGGER crypto_operation_failure BEFORE INSERT ON crypto_wallet_operations BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); e != nil {
		t.Fatal(e)
	}
	in := object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24)}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", in); w.Code != 409 {
		t.Fatal(w.Code)
	}
	current := cryptoTestCurrent(t, a, v.ID)
	if current.NextIndex != 0 || current.Revision != v.Revision {
		t.Fatal("failure did not roll back", current)
	}
	var count int
	if e := a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_wallet_addresses").Scan(&count); e != nil || count != 0 {
		t.Fatal("orphan allocation", e, count)
	}
	if _, e := a.store.db.Exec("DROP TRIGGER crypto_operation_failure"); e != nil {
		t.Fatal(e)
	}
	result := decoded[cryptoAllocationResult](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/addresses", in), 201)
	if result.Address.Index != 0 {
		t.Fatal(result)
	}
	stale := object{"password": commerceTestPassword, "revision": v.Revision, "operation_id": randomToken(24), "enabled": false}
	if w := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+v.ID+"/status", stale); w.Code != 409 {
		t.Fatal("stale write accepted", w.Code)
	}
}
