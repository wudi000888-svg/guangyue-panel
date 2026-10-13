package controlplane

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// Explicit, read-only diagnostics for public RPC operators. This is skipped in
// every normal/CI run and never reads production settings, keys or addresses.
// The wallet is the repository's public test fixture in a disposable database.
// No message is signed, no payment enabled and no transaction broadcast.
func TestCryptoSolanaBuiltInReadOnlyGasProbe(t *testing.T) {
	if os.Getenv("RUN_CRYPTO_NETWORK_PROBE") != "1" {
		t.Skip("explicit public RPC probe disabled")
	}
	a, _, wallet, _ := cryptoSweepFixture(t)
	if os.Getenv("CRYPTO_PROBE_PRODUCTION_TRANSPORT") == "1" {
		a.cfg.Dev = false
	}
	wallet.SupportedChainIDs = []int64{101}
	if _, err := a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[101]' WHERE id=?", wallet.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := a.store.cryptoWalletTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.cryptoEnsureSolanaPublic(tx, wallet); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	settings := cryptoDefaultSettings()
	settings.Revision = 2
	// Reproduce adding the network to a wallet without opening payments. The
	// Gas API must resolve a request-local public pair without persisting it.
	sealed, err := a.store.vault.seal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE crypto_payment_settings SET revision=2,doc=? WHERE id=1", sealed); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"cold", "warm"} {
		if label == "warm" {
			// Match the UI's real five-second cadence, not a burst against the
			// operators' free endpoints while bypassing our coalescing cache.
			time.Sleep(5 * time.Second)
		}
		// Force a fresh RPC market query without waiting on the cache TTL. Keep
		// the ordinary shared transport and read-only genesis cache for warm.
		a.cryptoGasMu.Lock()
		if entry := a.cryptoGasSnapshots[wallet.ID+"|solana"]; entry != nil {
			entry.expires = time.Time{}
		}
		a.cryptoGasMu.Unlock()
		started := time.Now()
		gas, err := a.cryptoWalletGas(context.Background(), wallet.ID, "solana")
		elapsed := time.Since(started)
		if err != nil || gas.GasError != "" || gas.FundingError != "" || gas.GasCheckedAt == 0 || gas.FundingCheckedAt == 0 || gas.ReferenceNativeTransferFeeAtoms != "5000" || gas.FundingBalanceAtoms == "" || elapsed >= 8*time.Second {
			t.Fatalf("%s readonly gas probe: elapsed=%s fee=%s gas_error=%s funding_error=%s error=%v", label, elapsed, gas.ReferenceNativeTransferFeeAtoms, gas.GasError, gas.FundingError, err)
		}
		t.Logf("%s SOL gas: elapsed=%s fee_lamports=%s finalized_slot=%d native_decimals=%d", label, elapsed, gas.ReferenceNativeTransferFeeAtoms, gas.BlockNumber, gas.NativeDecimals)
	}
	cryptoProbeGasHasNoFinancialEffects(t, a, sealed)
}

func TestCryptoPlasmaBuiltInReadOnlyGasProbe(t *testing.T) {
	if os.Getenv("RUN_CRYPTO_NETWORK_PROBE") != "1" {
		t.Skip("explicit public RPC probe disabled")
	}
	a, _, wallet, _ := cryptoSweepFixture(t)
	if os.Getenv("CRYPTO_PROBE_PRODUCTION_TRANSPORT") == "1" {
		a.cfg.Dev = false
	}
	if _, err := a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[9745]' WHERE id=?", wallet.ID); err != nil {
		t.Fatal(err)
	}
	settings := cryptoDefaultSettings()
	settings.Revision = 2
	sealed, err := a.store.vault.seal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE crypto_payment_settings SET revision=2,doc=? WHERE id=1", sealed); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"cold", "warm"} {
		if label == "warm" {
			time.Sleep(5 * time.Second)
		}
		a.cryptoGasMu.Lock()
		if entry := a.cryptoGasSnapshots[wallet.ID+"|plasma"]; entry != nil {
			entry.expires = time.Time{}
		}
		a.cryptoGasMu.Unlock()
		started := time.Now()
		gas, err := a.cryptoWalletGas(context.Background(), wallet.ID, "plasma")
		elapsed := time.Since(started)
		price, priceErr := cryptoAtoms(gas.GasPriceAtoms)
		fee, feeErr := cryptoAtoms(gas.ReferenceNativeTransferFeeAtoms)
		if err != nil || gas.GasError != "" || gas.FundingError != "" || gas.GasCheckedAt == 0 || gas.FundingCheckedAt == 0 || gas.FundingBalanceAtoms == "" || gas.BlockNumber == 0 || gas.Finality != "finalized" || priceErr != nil || feeErr != nil || price.Sign() <= 0 || fee.Sign() <= 0 || elapsed >= 8*time.Second {
			t.Fatalf("%s readonly gas probe: elapsed=%s fee=%s gas_error=%s funding_error=%s error=%v", label, elapsed, gas.ReferenceNativeTransferFeeAtoms, gas.GasError, gas.FundingError, err)
		}
		t.Logf("%s XPL gas: elapsed=%s gas_price_wei=%s native_transfer_fee_wei=%s finalized_block=%d", label, elapsed, gas.GasPriceAtoms, gas.ReferenceNativeTransferFeeAtoms, gas.BlockNumber)
	}
	cryptoProbeGasHasNoFinancialEffects(t, a, sealed)
}

func cryptoProbeGasHasNoFinancialEffects(t *testing.T, a *App, sealed []byte) {
	t.Helper()
	var after []byte
	if err := a.store.db.QueryRow("SELECT doc FROM crypto_payment_settings WHERE id=1").Scan(&after); err != nil || !bytes.Equal(sealed, after) {
		t.Fatal("read-only default RPCs changed payment settings", err)
	}
	for _, table := range []string{"crypto_sweep_jobs", "crypto_solana_transactions", "crypto_chain_transactions"} {
		var count int
		if err := a.store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("live probe created financial work", table, count, err)
		}
	}
}

func TestCryptoPlasmaBuiltInReadOnlyPreflightProbe(t *testing.T) {
	if os.Getenv("RUN_CRYPTO_NETWORK_PROBE") != "1" {
		t.Skip("explicit public RPC probe disabled")
	}
	settings := cryptoDefaultSettings()
	chain, _ := settings.chain(cryptoPlasmaChainID)
	rpc := cryptoBuiltinRPCs()[cryptoPlasmaChainID]
	chain.RPCURL, chain.RPCBackupURL, chain.FinalityVerified = rpc[0], rpc[1], true
	assets := []CryptoAsset{}
	for _, asset := range settings.Assets {
		if asset.ChainID == cryptoPlasmaChainID {
			// This is a local simulation argument, never payment configuration.
			asset.Enabled = true
			assets = append(assets, asset)
		}
	}
	a := testApp(t)
	if os.Getenv("CRYPTO_PROBE_PRODUCTION_TRANSPORT") == "1" {
		a.cfg.Dev = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	started := time.Now()
	if err := a.cryptoSetupPreflight(ctx, chain, assets, cryptoSetupProbeAddress); err != nil {
		t.Fatal("read-only Plasma preflight failed", err)
	}
	t.Logf("Plasma dual-source readonly preflight: elapsed=%s contracts=%d canonical_chain_id=%d", time.Since(started), len(assets), chain.ChainID)
}

func TestCryptoNativePricesBuiltInReadOnlyProbe(t *testing.T) {
	if os.Getenv("RUN_CRYPTO_NETWORK_PROBE") != "1" {
		t.Skip("explicit public market feed probe disabled")
	}
	// This uses the same production public-address transport as the service;
	// it has no database, wallet, keys or site configuration at all.
	a := &App{}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	started := time.Now()
	prices, err := a.fetchCryptoNativePrices(ctx)
	if err != nil {
		t.Fatal("production public native market feed failed", err)
	}
	now := time.Now().Unix()
	for _, symbol := range []string{"ETH", "BNB", "SOL", "XPL"} {
		price, ok := prices[symbol]
		usd, usdErr := cryptoRateNumber(price.USD)
		cny, cnyErr := cryptoRateNumber(price.CNY)
		if !ok || usdErr != nil || cnyErr != nil || usd.Sign() <= 0 || cny.Sign() <= 0 || price.Updated <= now-300 || price.Updated > now+120 || price.Expires <= now || price.Source != "CoinGecko" {
			t.Fatal("native market quote missing, zero or stale", symbol, price)
		}
		t.Logf("%s readonly public quote: usd=%s cny=%s updated_at=%d expires_at=%d source=%s", symbol, price.USD, price.CNY, price.Updated, price.Expires, price.Source)
	}
	t.Logf("production public native feed: elapsed=%s", time.Since(started))
}
