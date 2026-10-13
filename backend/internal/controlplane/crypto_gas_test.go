package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"
)

func cryptoGasRead(t *testing.T, a *App, owner Record, wallet CryptoWallet) CryptoGasSnapshot {
	t.Helper()
	w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/gas?chain_id=ethereum", nil)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("gas must be HTTP no-store")
	}
	return decoded[CryptoGasSnapshot](t, w, 200)
}
func TestCryptoGasRefreshIsLightweightAndCannotMoveFunds(t *testing.T) {
	a, owner, wallet, f := cryptoPortfolioFixture(t, 25)
	v := cryptoGasRead(t, a, owner, wallet)
	if v.WalletID != wallet.ID || v.ChainID != "ethereum" || v.GasPriceAtoms != "120" || v.ReferenceNativeTransferFeeAtoms != "2520000" || v.FundingBalanceAtoms != "1000000000000" || v.ExpiresAt-v.GasCheckedAt > 10 || v.ExpiresAt <= v.GasCheckedAt {
		t.Fatalf("bad gas snapshot: %+v", v)
	}
	f.mu.Lock()
	calls := f.calls
	tags := append([]string{}, f.tags...)
	f.mu.Unlock()
	if calls > 16 || len(tags) != 2 {
		t.Fatalf("gas read scanned receiving addresses: calls=%d balance reads=%d", calls, len(tags))
	}
	cryptoGasRead(t, a, owner, wallet)
	f.mu.Lock()
	cached := f.calls
	f.mu.Unlock()
	if cached != calls {
		t.Fatal("cache did not coalesce immediate read")
	}
	a.cryptoGasMu.Lock()
	for _, e := range a.cryptoGasSnapshots {
		e.expires = time.Time{}
	}
	a.cryptoGasMu.Unlock()
	f.base.mu.Lock()
	f.base.price = 200
	f.base.mu.Unlock()
	next := cryptoGasRead(t, a, owner, wallet)
	if next.GasPriceAtoms != "240" {
		t.Fatalf("gas reused 30 second portfolio cache: %+v", next)
	}
	for _, table := range []string{"crypto_sweep_jobs", "crypto_chain_transactions"} {
		var count int
		err := a.store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil || count != 0 {
			t.Fatalf("gas mutated %s: count %d err %v", table, count, err)
		}
	}
	f.base.mu.Lock()
	sends := f.base.sends
	f.base.mu.Unlock()
	if sends != 0 {
		t.Fatal("gas query broadcast a transaction")
	}
}
func TestCryptoGasOutageRetainsEstimateButNeverInventsZero(t *testing.T) {
	a, owner, wallet, f := cryptoPortfolioFixture(t, 1)
	first := cryptoGasRead(t, a, owner, wallet)
	a.cryptoGasMu.Lock()
	for _, e := range a.cryptoGasSnapshots {
		e.expires = time.Time{}
	}
	a.cryptoGasMu.Unlock()
	f.mu.Lock()
	f.failGas = true
	f.mu.Unlock()
	failed := cryptoGasRead(t, a, owner, wallet)
	if failed.GasError == "" || failed.GasPriceAtoms != first.GasPriceAtoms || failed.GasCheckedAt != first.GasCheckedAt {
		t.Fatalf("lost old gas estimate: %+v", failed)
	}
	a.cryptoGasMu.Lock()
	a.cryptoGasSnapshots = nil
	a.cryptoGasMu.Unlock()
	unknown := cryptoGasRead(t, a, owner, wallet)
	if unknown.GasError == "" || unknown.GasPriceAtoms != "" || unknown.GasCheckedAt != 0 {
		t.Fatalf("unknown gas became zero: %+v", unknown)
	}
}
func TestCryptoGasAuthorizationAndWalletScopePrecedeCache(t *testing.T) {
	a, owner, wallet, f := cryptoPortfolioFixture(t, 1)
	cryptoGasRead(t, a, owner, wallet)
	if _, err := a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[1]' WHERE id=?", wallet.ID); err != nil {
		t.Fatal(err)
	}
	member := testUser(t, a, "member", "user")
	w := req(t, a, member, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/gas?chain_id=ethereum", nil)
	if w.Code != 403 {
		t.Fatalf("member read gas: %d", w.Code)
	}
	_, err := a.cryptoWalletGas(context.Background(), wallet.ID, "bsc")
	if err == nil {
		t.Fatal("gas expanded wallet network scope")
	}
	f.mu.Lock()
	before := f.calls
	f.mu.Unlock()
	wallet, err = cryptoWalletByID(a.store.db, wallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	deleted := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": "gas-test-permanent-delete", "delete_ack": true, "permanent_delete_ack": true})
	if deleted.Code != 200 {
		t.Fatalf("delete failed: %d %s", deleted.Code, deleted.Body.String())
	}
	_, err = a.cryptoWalletGas(context.Background(), wallet.ID, "ethereum")
	if err == nil || !strings.Contains(err.Error(), "钱包") {
		t.Fatalf("deleted wallet served cached gas: %v", err)
	}
	f.mu.Lock()
	after := f.calls
	f.mu.Unlock()
	if before != after {
		t.Fatal("invalid wallet used RPC")
	}
}

func TestCryptoGasCoalescesInFlightAndCancelsWaitingReader(t *testing.T) {
	a, _, wallet, f := cryptoPortfolioFixture(t, 1)
	f.mu.Lock()
	f.started = make(chan struct{})
	f.release = make(chan struct{})
	started, release := f.started, f.release
	f.mu.Unlock()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	done := make(chan CryptoGasSnapshot, 1)
	go func() { value, _ := a.cryptoWalletGas(context.Background(), wallet.ID, "ethereum"); done <- value }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("gas funding read did not start")
	}
	// Both independent RPC legs must have arrived before recording the count;
	// their normal scheduling must not be mistaken for a second reader's work.
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		arrived := len(f.tags) == 2
		f.mu.Unlock()
		if arrived {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second funding RPC did not arrive")
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	before := f.calls
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.cryptoWalletGas(ctx, wallet.ID, "ethereum"); err == nil {
		t.Fatal("cancelled waiter was not cancelled")
	}
	f.mu.Lock()
	after := f.calls
	f.mu.Unlock()
	if before != after {
		t.Fatal("waiting reader started overlapping RPC")
	}
	close(release)
	released = true
	select {
	case value := <-done:
		if value.GasError != "" || value.GasPriceAtoms != "120" {
			t.Fatalf("first reader lost successful result: %+v", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first reader did not finish")
	}
}
