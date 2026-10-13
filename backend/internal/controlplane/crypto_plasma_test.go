package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCryptoNewNetworkCatalogAndLegacySettings(t *testing.T) {
	defaults := cryptoDefaultSettings()
	plasma, ok := defaults.chain(cryptoPlasmaChainID)
	if !ok || plasma.ID != "plasma" || plasma.NativeSymbol != "XPL" || cryptoNativeDecimals(plasma) != 18 || plasma.Enabled {
		t.Fatalf("invalid Plasma catalog: %+v", plasma)
	}
	for _, want := range []CryptoAsset{
		{ID: "plasma-usdt0", Contract: "0xb8ce59fc3717ada4c02eadf9682a9e934f625ebb", Symbol: "USDT0"},
		{ID: "plasma-usdc", Contract: "0x2d661c89d812261039af9764eceaaee884f5f67f", Symbol: "USDC"},
		{ID: "solana-usdt", Contract: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Symbol: "USDT"},
		{ID: "solana-usdc", Contract: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Symbol: "USDC"},
	} {
		asset, ok := defaults.asset(want.ID)
		if !ok || asset.Contract != want.Contract || asset.Symbol != want.Symbol || asset.Decimals != 6 || asset.Enabled {
			t.Fatalf("issuer contract/mint changed: %+v", asset)
		}
	}
	// Unset metadata on old EVM chains must remain absent from signed JSON.
	if raw := string(jsonBytes(defaults.Chains[0])); strings.Contains(raw, "family") || strings.Contains(raw, "native_decimals") {
		t.Fatal("new metadata changed legacy HMAC shape", raw)
	}
	if cryptoNormalizeContract(101, "ABCdef") != "ABCdef" || cryptoNormalizeContract(cryptoPlasmaChainID, "0xABCdef") != "0xabcdef" {
		t.Fatal("chain-specific address case was lost")
	}

	a := testApp(t)
	old := defaults
	old.Chains = append([]CryptoChain(nil), defaults.Chains[:5]...)
	old.Assets = append([]CryptoAsset(nil), defaults.Assets[:9]...)
	old.Enabled, old.Revision = true, 4
	old.Chains[0].Enabled, old.Chains[0].RPCURL, old.Chains[0].RPCBackupURL = true, "https://primary.example", "https://backup.example"
	old.Assets[1].Enabled, old.Assets[1].CNYPerToken = true, "7.1"
	sealed, err := a.store.vault.seal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,?,?)", old.Revision, sealed); err != nil {
		t.Fatal(err)
	}
	merged, err := a.store.cryptoPaymentSettings()
	if err != nil || merged.Revision != old.Revision || len(merged.Chains) != len(defaults.Chains) || len(merged.Assets) != len(defaults.Assets) || !merged.Chains[0].Enabled || merged.Assets[1].CNYPerToken != "7.1" {
		t.Fatal("old settings not safely expanded", err, merged)
	}
	for _, chainID := range []int64{cryptoPlasmaChainID, 101} {
		chain, _ := merged.chain(chainID)
		if chain.Enabled || chain.RPCURL != "" || chain.FinalityVerified {
			t.Fatal("reading upgrade enabled a new payment network", chain)
		}
	}
	var stored []byte
	if err = a.store.db.QueryRow("SELECT doc FROM crypto_payment_settings WHERE id=1").Scan(&stored); err != nil || !bytes.Equal(stored, sealed) {
		t.Fatal("read-only merge rewrote signed settings", err)
	}
}

func TestCryptoPlasmaPinnedGenesisAndFinalizedAgreement(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_genesis_on_both", "wrong_chain", "finalized_missing", "finalized_fork"} {
		t.Run(mode, func(t *testing.T) {
			answer := func(backup bool) func(string, []json.RawMessage) any {
				return func(method string, params []json.RawMessage) any {
					switch method {
					case "eth_chainId":
						if mode == "wrong_chain" {
							return "0x2612" // Plasma testnet must never pass.
						}
						return "0x2611"
					case "eth_getBlockByNumber":
						var tag string
						_ = json.Unmarshal(params[0], &tag)
						if tag == "finalized" && mode == "finalized_missing" {
							return errors.New("unsupported tag")
						}
						n := uint64(100)
						if tag == "finalized" && backup {
							n = 103 // Recheck the lower provider's fixed height.
						} else if strings.HasPrefix(tag, "0x") {
							n, _ = cryptoHexU64(tag)
						}
						hash, stamp := cryptoFixtureHash(n), "0x1"
						if n == 0 {
							hash, stamp = cryptoPlasmaGenesisHash, "0x0"
							if mode == "wrong_genesis_on_both" {
								hash = cryptoFixtureHash(0)
							}
						} else if backup && mode == "finalized_fork" {
							hash = cryptoFixtureHash(n + 500)
						}
						return object{"number": cryptoHex(n), "hash": hash, "timestamp": stamp}
					}
					return nil
				}
			}
			primary, backup := cryptoRPCAnswers(t, answer(false)), cryptoRPCAnswers(t, answer(true))
			chain, _ := cryptoDefaultSettings().chain(cryptoPlasmaChainID)
			chain.FinalityVerified, chain.RPCURL, chain.RPCBackupURL = true, primary.URL, backup.URL
			client, err := newCryptoEVM(chain, true)
			if err != nil {
				t.Fatal(err)
			}
			block, err := client.Finalized(context.Background())
			if mode == "valid" {
				if err != nil || block.Number != 100 || block.Hash != cryptoFixtureHash(100) {
					t.Fatal("valid dual-provider Plasma finality rejected", block, err)
				}
			} else if err == nil {
				t.Fatal("invalid Plasma identity/finality accepted", mode)
			}
		})
	}
	if _, err := newCryptoEVM(CryptoChain{ChainID: 101, Family: "solana"}, true); err == nil {
		t.Fatal("Solana was passed to an EVM RPC client")
	}
}

func TestCryptoPlasmaOneClickPreflight(t *testing.T) {
	a, owner, wallet, fixture := cryptoSweepFixture(t)
	if _, err := a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[9745]' WHERE id=?", wallet.ID); err != nil {
		t.Fatal(err)
	}
	fixture.chain = cryptoPlasmaChainID
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(raw, &request)
		if request.Method == "eth_getBlockByNumber" && len(request.Params) != 0 && request.Params[0] == "0x0" {
			jsonResponse(w, 200, object{"jsonrpc": "2.0", "id": 1, "result": object{"number": "0x0", "timestamp": "0x0", "hash": cryptoPlasmaGenesisHash}})
			return
		}
		if request.Method == "eth_getLogs" {
			jsonResponse(w, 200, object{"jsonrpc": "2.0", "id": 1, "result": []any{}})
			return
		}
		fixture.serve(w, r)
	})
	primary, backup := httptest.NewServer(handler), httptest.NewServer(handler)
	t.Cleanup(primary.Close)
	t.Cleanup(backup.Close)
	a.cryptoRateFetch = func(context.Context) (cryptoMarketRates, error) { return cryptoTestRates(), nil }
	in := cryptoSetupInput{WalletID: wallet.ID, ChainIDs: []int64{cryptoPlasmaChainID}, AssetIDs: []string{"plasma-usdt0", "plasma-usdc"}, OperationID: randomToken(24)}
	settings, err := a.setupCryptoPayments(context.Background(), owner, in, map[int64][2]string{cryptoPlasmaChainID: {primary.URL, backup.URL}})
	if err != nil {
		t.Fatal(err)
	}
	plasma, ok := settings.chain(cryptoPlasmaChainID)
	if !ok || !settings.Enabled || !plasma.Enabled || !plasma.FinalityVerified || !plasma.HasRPC || !plasma.HasRPCBackup || plasma.RPCURL != "" || settings.SetupCheckedAt == 0 {
		t.Fatal("Plasma setup did not validate and redact sources", settings)
	}
	for _, id := range in.AssetIDs {
		asset, _ := settings.asset(id)
		if !asset.Enabled || asset.RateExpiresAt == 0 || asset.CNYPerToken == "" {
			t.Fatal("Plasma setup lacked a valid stablecoin rate", asset)
		}
	}
	before, err := a.store.cryptoPaymentSettings()
	if err != nil {
		t.Fatal(err)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	t.Cleanup(broken.Close)
	in.OperationID = randomToken(24)
	if _, err = a.setupCryptoPayments(context.Background(), owner, in, map[int64][2]string{cryptoPlasmaChainID: {primary.URL, broken.URL}}); err == nil {
		t.Fatal("limited backup silently enabled Plasma payments")
	}
	after, err := a.store.cryptoPaymentSettings()
	if err != nil || before.Revision != after.Revision || !bytes.Equal(jsonBytes(before), jsonBytes(after)) {
		t.Fatal("failed dual-provider preflight changed live settings", err)
	}
}

func TestCryptoPlasmaGasTopupAndTokenSweep(t *testing.T) {
	a, owner, wallet, fixture := cryptoSweepFixture(t)
	if _, err := a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[9745]' WHERE id=?", wallet.ID); err != nil {
		t.Fatal(err)
	}
	fixture.chain = cryptoPlasmaChainID
	settings := cryptoDefaultSettings()
	settings.Enabled, settings.WalletID, settings.Revision = true, wallet.ID, 2
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(raw, &request)
		if request.Method == "eth_getBlockByNumber" && len(request.Params) != 0 && request.Params[0] == "0x0" {
			jsonResponse(w, 200, object{"jsonrpc": "2.0", "id": 1, "result": object{"number": "0x0", "timestamp": "0x0", "hash": cryptoPlasmaGenesisHash}})
			return
		}
		fixture.serve(w, r)
	})
	primary, backup := httptest.NewServer(handler), httptest.NewServer(handler)
	t.Cleanup(primary.Close)
	t.Cleanup(backup.Close)
	for i := range settings.Chains {
		chain := &settings.Chains[i]
		if chain.ChainID == cryptoPlasmaChainID {
			chain.Enabled, chain.FinalityVerified, chain.RPCURL, chain.RPCBackupURL = true, true, primary.URL, backup.URL
		}
	}
	for i := range settings.Assets {
		asset := &settings.Assets[i]
		if asset.ID == "plasma-usdc" {
			asset.Enabled = true
			fixture.contract = asset.Contract
		}
	}
	sealed, err := a.store.vault.seal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE crypto_payment_settings SET revision=2,doc=? WHERE id=1", sealed); err != nil {
		t.Fatal(err)
	}
	// Keep this fixture an actual, authenticated Plasma invoice rather than
	// relabeling an Ethereum invoice's SQL metadata without its sealed snapshot.
	var invoiceID string
	if err = a.store.db.QueryRow("SELECT id FROM crypto_invoices WHERE wallet_id=?", wallet.ID).Scan(&invoiceID); err != nil {
		t.Fatal(err)
	}
	invoice, err := a.store.cryptoInvoice(a.store.db, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	chain, _ := settings.chain(cryptoPlasmaChainID)
	asset, _ := settings.asset("plasma-usdc")
	asset.CNYPerToken, asset.RateUpdatedAt, asset.RateExpiresAt = invoice.Snapshot.Asset.CNYPerToken, invoice.Snapshot.Asset.RateUpdatedAt, invoice.Snapshot.Asset.RateExpiresAt
	invoice.Snapshot.Chain, invoice.Snapshot.Asset = chain, asset
	invoiceDoc, err := a.store.vault.seal(invoice.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE crypto_invoices SET chain_id=?,asset_id=?,contract=?,doc=? WHERE id=?", cryptoPlasmaChainID, asset.ID, asset.Contract, invoiceDoc, invoiceID); err != nil {
		t.Fatal(err)
	}
	input := cryptoSweepTestInput()
	input.ChainID, input.AssetID = "plasma", "plasma-usdc"
	preview, err := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, input)
	if err != nil || !preview.CanSubmit || len(preview.Items) != 1 || preview.Items[0].TopupAtoms == "0" {
		t.Fatalf("Plasma sweep preview failed: %+v %v", preview, err)
	}
	job := cryptoSweepTestCreate(t, a, owner, wallet, preview)
	if err = a.cryptoSweepWork(context.Background()); err != nil || cryptoSweepTestCount(t, a) != 1 {
		t.Fatal("Plasma gas topup failed", err)
	}
	fixture.mine(false)
	if err = a.cryptoSweepWork(context.Background()); err != nil || cryptoSweepTestCount(t, a) != 1 {
		t.Fatal("Plasma token sent before gas finality", err)
	}
	fixture.mu.Lock()
	fixture.final = fixture.latest
	fixture.mu.Unlock()
	if err = a.cryptoSweepWork(context.Background()); err != nil || cryptoSweepTestCount(t, a) != 2 {
		t.Fatal("Plasma token transfer failed", err)
	}
	fixture.mine(true)
	for range 2 {
		if err = a.cryptoSweepWork(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := a.store.cryptoSweepJob(job.ID)
	if err != nil || completed.State != "complete" || fixture.durabilityFailed {
		t.Fatal("Plasma did not finish a durable sweep", completed, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, transaction := range fixture.txs {
		if transaction.ChainId().Int64() != cryptoPlasmaChainID {
			t.Fatal("Plasma transaction signed for another chain")
		}
	}
	if fixture.balance(fixture.tokens, input.Destination).String() != "2000000" {
		t.Fatal("Plasma swept the wrong token amount")
	}
}
