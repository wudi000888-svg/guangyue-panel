package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
)

func TestCryptoSolanaWalletCoreVectors(t *testing.T) {
	for _, v := range []struct {
		index   uint32
		address string
	}{{0, "HAgk14JpMQLgt6rVgv7cBQFJWFto5Dqxi472uT3DKpqk"}, {1, "Hh8QwFUA6MtVu1qAoq12ucvFHNwCcVTV7hpWjeY1Hztb"}, {cryptoSolanaFundingIndex, "4js3iYAPReCCbyjcQi6hnmfy6gM4idGBRpTLtTJbkFRc"}} {
		got, e := cryptoSolanaDerive(cryptoTestMnemonic, v.index)
		if e != nil || got != v.address {
			t.Fatalf("Wallet Core 4.8.4/Solana mismatch index %d got=%s err=%v", v.index, got, e)
		}
	}
	if _, e := cryptoSolanaDerive(cryptoTestMnemonic, 1<<31); e == nil {
		t.Fatal("non-receiving index accepted")
	}
	if _, e := cryptoSolanaDerive("invalid seed", 0); e == nil {
		t.Fatal("invalid mnemonic accepted")
	}
	if cryptoSolanaAddress("0x9858EfFD232B4033E47d90003D41EC34EcaEda94") {
		t.Fatal("EVM address accepted on Solana")
	}
	var v any
	if e := cryptoSolanaSame(json.RawMessage(`{"value":9007199254740992}`), json.RawMessage(`{"value":9007199254740993}`), &v); e == nil {
		t.Fatal("distinct uint64 balances rounded to same JSON float")
	}
}

func cryptoSolanaFixtureHash(n uint64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("public-solana-test-block-%d", n)))
	return base58.Encode(h[:])
}

type cryptoSolanaFixture struct {
	mu                 sync.Mutex
	slot, height       uint64
	created            int64
	native             map[string]uint64
	tokens             map[string]map[string]string
	raw                map[string][]byte
	mined              map[string]bool
	incoming           map[string]object
	history            map[string][]string
	malformed, genesis string
	acceptedError      bool
	sends              int
	rent               uint64
	feePerSignature    uint64
	delegates          map[string]string
}

func (f *cryptoSolanaFixture) tokenAccount(owner, mint, amount string) object {
	value := object{"pubkey": cryptoSolanaFixtureATA(owner, mint), "account": object{"owner": cryptoSolanaTokenProgram, "lamports": f.rent, "data": object{"parsed": object{"type": "account", "info": object{"owner": owner, "mint": mint, "state": "initialized", "tokenAmount": object{"amount": amount, "decimals": 6}}}}}}
	if delegate := f.delegates[owner]; delegate != "" {
		value["account"].(object)["data"].(object)["parsed"].(object)["info"].(object)["delegate"] = delegate
	}
	return value
}
func cryptoSolanaFixtureATA(owner, mint string) string {
	a, e := cryptoSolanaATA(owner, mint)
	if e != nil {
		panic(e)
	}
	return a
}
func (f *cryptoSolanaFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(400)
		return
	}
	param := func(i int) string { var s string; _ = json.Unmarshal(req.Params[i], &s); return s }
	var result any
	switch req.Method {
	case "getGenesisHash":
		result = cryptoSolanaGenesis
		if f.genesis != "" {
			result = f.genesis
		}
	case "getSlot":
		result = f.slot
	case "getBlock":
		var slot uint64
		_ = json.Unmarshal(req.Params[0], &slot)
		result = object{"blockhash": cryptoSolanaFixtureHash(slot), "blockTime": f.created + int64(slot) - 1000, "blockHeight": f.height}
	case "getLatestBlockhash":
		result = object{"context": object{"slot": f.slot}, "value": object{"blockhash": cryptoSolanaFixtureHash(f.slot), "lastValidBlockHeight": f.height + 150}}
	case "isBlockhashValid":
		result = object{"context": object{"slot": f.slot}, "value": true}
	case "getFeeForMessage":
		message, e := base64.StdEncoding.DecodeString(param(0))
		if e != nil || len(message) < 3 {
			w.WriteHeader(400)
			return
		}
		fee := f.feePerSignature
		if fee == 0 {
			fee = 5000
		}
		result = object{"context": object{"slot": f.slot}, "value": uint64(message[0]) * fee}
	case "getMinimumBalanceForRentExemption":
		result = f.rent
	case "getBlockHeight":
		result = f.height
	case "getBalance":
		result = object{"context": object{"slot": f.slot}, "value": f.native[param(0)]}
		if f.malformed == "missing-balance" {
			result = object{"context": object{"slot": f.slot}}
		}
	case "getTokenAccountsByOwner":
		var filter struct {
			Mint string `json:"mint"`
		}
		_ = json.Unmarshal(req.Params[1], &filter)
		values := []object{}
		if amount, ok := f.tokens[param(0)][filter.Mint]; ok {
			values = append(values, f.tokenAccount(param(0), filter.Mint, amount))
		}
		result = object{"context": object{"slot": f.slot}, "value": values}
		if f.malformed == "missing-tokens" {
			result = object{"context": object{"slot": f.slot}}
		}
	case "getAccountInfo":
		address := param(0)
		var value any
		if address == cryptoSolanaUSDC || address == cryptoSolanaUSDT {
			value = object{"owner": cryptoSolanaTokenProgram, "data": object{"parsed": object{"type": "mint", "info": object{"decimals": 6, "isInitialized": true}}}}
		} else {
			for owner, tokens := range f.tokens {
				for mint, amount := range tokens {
					if cryptoSolanaFixtureATA(owner, mint) == address {
						value = f.tokenAccount(owner, mint, amount)["account"]
					}
				}
			}
		}
		result = object{"context": object{"slot": f.slot}, "value": value}
	case "getSignaturesForAddress":
		list := []object{}
		for _, sig := range f.history[param(0)] {
			v := f.incoming[sig]
			list = append(list, object{"signature": sig, "slot": v["slot"], "err": nil, "confirmationStatus": "finalized"})
		}
		result = list
	case "getTransaction":
		if f.malformed == "unsupported-version" {
			_ = json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "id": 1, "error": object{"code": -32015, "message": "untrusted provider diagnostic"}})
			return
		}
		sig := param(0)
		var encoding struct {
			Encoding string `json:"encoding"`
		}
		_ = json.Unmarshal(req.Params[1], &encoding)
		if v, ok := f.incoming[sig]; ok {
			result = v
		} else if raw, ok := f.raw[sig]; ok && f.mined[sig] {
			count := int(raw[132])
			pre, post := make([]uint64, count), make([]uint64, count)
			cost := uint64(10000)
			if bytes.Contains(raw, base58.Decode(cryptoSolanaATAProgram)) {
				cost += f.rent
			}
			pre[0] = 1000000000
			post[0] = pre[0] - cost
			result = object{"slot": f.slot, "blockTime": f.created + int64(f.slot) - 1000, "transaction": []string{base64.StdEncoding.EncodeToString(raw), "base64"}, "meta": object{"err": nil, "fee": 10000, "preBalances": pre, "postBalances": post}}
		}
		if f.malformed != "" && result != nil {
			v := object{}
			_ = json.Unmarshal(jsonBytes(result), &v)
			switch f.malformed {
			case "missing-meta":
				delete(v, "meta")
			case "null-meta":
				v["meta"] = nil
			case "missing-err":
				meta := v["meta"].(map[string]any)
				delete(meta, "err")
			case "missing-balances":
				meta := v["meta"].(map[string]any)
				delete(meta, "preBalances")
				delete(meta, "postBalances")
			case "missing-token-balances":
				meta := v["meta"].(map[string]any)
				delete(meta, "preTokenBalances")
				delete(meta, "postTokenBalances")
			case "missing-account-index":
				meta := v["meta"].(map[string]any)
				balances := meta["postTokenBalances"].([]any)
				delete(balances[len(balances)-1].(map[string]any), "accountIndex")
			case "missing-fee":
				meta := v["meta"].(map[string]any)
				delete(meta, "fee")
			}
			result = v
		}
	case "sendTransaction":
		raw, e := base64.StdEncoding.DecodeString(param(0))
		if e != nil || len(raw) < 130 || raw[0] != 2 {
			w.WriteHeader(400)
			return
		}
		sig := base58.Encode(raw[1:65])
		if _, ok := f.raw[sig]; !ok {
			f.raw[sig] = raw
		}
		f.sends++
		result = sig
		if f.acceptedError {
			w.WriteHeader(503)
			return
		}
	default:
		w.WriteHeader(400)
		return
	}
	_ = json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "id": 1, "result": result})
}
func cryptoSolanaTestFixture(t *testing.T) (*App, Record, Record, CryptoWallet, *cryptoSolanaFixture) {
	t.Helper()
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	wallet := cryptoSelectedWallet(t, a, owner, true, []int64{1, 56, cryptoSolanaChainID}, 201)
	wallet = cryptoTestConfirm(t, a, owner, wallet)
	f := &cryptoSolanaFixture{slot: 1000, height: 2000, created: time.Now().Unix(), native: map[string]uint64{wallet.SolanaFundingAddress: 1000000000}, tokens: map[string]map[string]string{}, raw: map[string][]byte{}, mined: map[string]bool{}, incoming: map[string]object{}, history: map[string][]string{}, rent: 2039280}
	first := httptest.NewServer(http.HandlerFunc(f.serve))
	second := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	settings := cryptoDefaultSettings()
	settings.Enabled = true
	settings.WalletID = wallet.ID
	settings.Revision = 1
	for i := range settings.Chains {
		if settings.Chains[i].ChainID == cryptoSolanaChainID {
			settings.Chains[i].Enabled = true
			settings.Chains[i].FinalityVerified = true
			settings.Chains[i].RPCURL = first.URL
			settings.Chains[i].RPCBackupURL = second.URL
		}
	}
	for i := range settings.Assets {
		if settings.Assets[i].ChainID == cryptoSolanaChainID {
			settings.Assets[i].Enabled = true
			settings.Assets[i].CNYPerToken = "7"
			settings.Assets[i].RateUpdatedAt = f.created
			settings.Assets[i].RateExpiresAt = f.created + 86400
		}
	}
	raw, e := a.store.vault.seal(settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,1,?)", raw); e != nil {
		t.Fatal(e)
	}
	return a, owner, user, wallet, f
}
func cryptoSolanaTestAllocate(t *testing.T, a *App, owner Record, wallet CryptoWallet) cryptoAllocationResult {
	t.Helper()
	return decoded[cryptoAllocationResult](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/addresses", object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "chain_id": "solana"}), 201)
}
func cryptoSolanaTestOrderAllocate(t *testing.T, a *App, owner, user Record, wallet CryptoWallet) cryptoAllocationResult {
	t.Helper()
	order := newOrder(t, a, user, commerceOffer(t, a, owner))
	invoice := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": "solana-usdc", "operation_id": randomToken(24)}), 201)
	invoice, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
	if e != nil {
		t.Fatal(e)
	}
	var row CryptoWalletAddress
	if e := a.store.db.QueryRow("SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE id=?", invoice.AddressID).Scan(&row.ID, &row.WalletID, &row.Index, &row.Path, &row.Address, &row.Label, &row.Created); e != nil {
		t.Fatal(e)
	}
	current, e := cryptoWalletByID(a.store.db, wallet.ID)
	if e != nil {
		t.Fatal(e)
	}
	if current, e = a.store.cryptoWalletSolanaDTO(a.store.db, current); e != nil {
		t.Fatal(e)
	}
	return cryptoAllocationResult{Wallet: current, Address: row}
}
func cryptoSolanaTestIncoming(owner, mint, amount string, slot uint64, timestamp int64) (string, object) {
	sender, _ := cryptoSolanaDerive(cryptoTestMnemonic, 11)
	source := cryptoSolanaFixtureATA(sender, mint)
	destination := cryptoSolanaFixtureATA(owner, mint)
	a := sha256.Sum256([]byte(owner + mint + amount))
	b := sha256.Sum256(a[:])
	sig := base58.Encode(append(a[:], b[:]...))
	balance := func(index int, who string) object {
		return object{"accountIndex": index, "mint": mint, "owner": who, "programId": cryptoSolanaTokenProgram, "uiTokenAmount": object{"amount": amount, "decimals": 6}}
	}
	return sig, object{"slot": slot, "blockTime": timestamp, "version": "legacy", "transaction": object{"signatures": []string{sig}, "message": object{"accountKeys": []object{{"pubkey": source}, {"pubkey": destination}, {"pubkey": sender, "signer": true}, {"pubkey": owner}}, "instructions": []object{{"programId": cryptoSolanaTokenProgram, "parsed": object{"type": "transferChecked", "info": object{"source": source, "destination": destination, "authority": sender, "mint": mint, "tokenAmount": object{"amount": amount, "decimals": 6}}}}}}}, "meta": object{"err": nil, "fee": 5000, "preTokenBalances": []object{balance(0, sender)}, "postTokenBalances": []object{balance(0, sender), balance(1, owner)}, "innerInstructions": []object{}}}
}
func TestCryptoSolanaInvoiceFinalityDedupAndOrderActivation(t *testing.T) {
	for _, v := range []struct {
		name    string
		version any
	}{{"legacy", "legacy"}, {"v0", 0}, {"v1", 1}} {
		t.Run(v.name, func(t *testing.T) { cryptoSolanaTestInvoiceActivation(t, v.version) })
	}
}
func cryptoSolanaTestInvoiceActivation(t *testing.T, version any) {
	t.Helper()
	a, owner, user, wallet, f := cryptoSolanaTestFixture(t)
	o := newOrder(t, a, user, commerceOffer(t, a, owner))
	in := object{"order_id": o.ID, "asset_id": "solana-usdc", "operation_id": randomToken(24)}
	invoice := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", in), 201)
	if !cryptoSolanaAddress(invoice.Address) || !strings.HasPrefix(invoice.QRURI, "solana:") || !strings.Contains(invoice.QRURI, "spl-token="+cryptoSolanaUSDC) {
		t.Fatal("wrong chain address/QR", invoice)
	}
	if got := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", in), 200); got.Address != invoice.Address {
		t.Fatal("invoice replay allocated another address")
	}
	f.mu.Lock()
	f.slot = 1001
	sig, transfer := cryptoSolanaTestIncoming(invoice.Address, cryptoSolanaUSDC, invoice.ExpectedAtoms, 1001, f.created+1)
	transfer["version"] = version
	f.incoming[sig] = transfer
	f.history[cryptoSolanaFixtureATA(invoice.Address, cryptoSolanaUSDC)] = []string{sig}
	f.tokens[invoice.Address] = map[string]string{cryptoSolanaUSDC: invoice.ExpectedAtoms}
	f.mu.Unlock()
	stored, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = a.scanCryptoInvoice(context.Background(), stored); e != nil {
			t.Fatal(e)
		}
	}
	for range 2 {
		if e = a.paymentWork(); e != nil {
			t.Fatal(e)
		}
		if e = a.commerceWork(time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	got, e := a.store.order(o.ID)
	if e != nil || got.State != "completed" {
		t.Fatal("Solana final payment did not activate plan", got.State, e)
	}
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", o.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate final payment", count, e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal("Solana persisted invoice integrity", e)
	}
	balances, e := a.cryptoWalletPortfolio(context.Background(), wallet.ID, "solana", -1, "funded")
	if e != nil || balances.NativeDecimals != 9 || balances.FundedAddresses != 1 || len(balances.Items) != 1 {
		t.Fatal("Solana funded address portfolio", balances, e)
	}
}
func TestCryptoSolanaSweepDurableRetryAndPermanentDeleteGuard(t *testing.T) {
	a, owner, user, wallet, f := cryptoSolanaTestFixture(t)
	allocation := cryptoSolanaTestOrderAllocate(t, a, owner, user, wallet)
	wallet = allocation.Wallet
	destination, _ := cryptoSolanaDerive(cryptoTestMnemonic, 10)
	f.mu.Lock()
	f.tokens[allocation.Address.Address] = map[string]string{cryptoSolanaUSDC: "2000000"}
	f.mu.Unlock()
	preview, e := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{ChainID: "solana", AssetID: "solana-usdc", Destination: destination, MinAtoms: "1"})
	if e != nil || !preview.CanSubmit || preview.NativeDecimals != 9 || preview.TotalRentAtoms == "0" || preview.Items[0].TopupAtoms != "0" {
		t.Fatal("Solana fee payer/rent preview", preview, e)
	}
	job, e := a.cryptoCreateSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)})
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.acceptedError = true
	f.mu.Unlock()
	if e = a.cryptoSweepWork(context.Background()); e == nil {
		t.Fatal("unknown send fixture should fail transiently")
	}
	if e = a.cryptoSweepWork(context.Background()); e == nil {
		t.Fatal("unknown resend fixture should fail transiently")
	}
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_solana_transactions WHERE job_id=?", job.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("unknown broadcast created another signature", count, e)
	}
	if e = a.cryptoCancelSweep(context.Background(), owner, job.ID, cryptoSweepInput{OperationID: randomToken(24)}); e == nil {
		t.Fatal("cancelled unconfirmed Solana signature")
	}
	if e = a.store.validateCryptoSweeps(true); e != nil {
		t.Fatal("signed Solana job integrity", e)
	}
	// Even a wrongly terminal job cannot allow destruction of its signing key.
	if _, e = a.store.db.Exec("UPDATE crypto_sweep_jobs SET state='complete' WHERE id=?", job.ID); e != nil {
		t.Fatal(e)
	}
	tx, e := a.store.cryptoWalletTx(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	e = a.store.permanentlyDeleteCryptoWallet(tx, wallet)
	tx.Rollback()
	if e == nil {
		t.Fatal("deleted wallet with unknown signed Solana transaction")
	}
	_, _ = a.store.db.Exec("UPDATE crypto_sweep_jobs SET state='waiting' WHERE id=?", job.ID)
	f.mu.Lock()
	f.acceptedError = false
	f.slot = 1001
	for sig := range f.raw {
		f.mined[sig] = true
	}
	f.mu.Unlock()
	for range 2 {
		if e = a.cryptoSweepWork(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	finished, e := a.store.cryptoSweepJob(job.ID)
	if e != nil || finished.State != "complete" || finished.Items[0].SweepTxHash == "" {
		t.Fatal("Solana batch did not complete", finished, e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal("confirmed Solana integrity", e)
	}
}
func TestCryptoSolanaMalformedRPCNeverMeansEmptyOrPaid(t *testing.T) {
	a, _, _, wallet, f := cryptoSolanaTestFixture(t)
	settings, _ := a.store.cryptoPaymentSettings()
	chain, _ := settings.chain(cryptoSolanaChainID)
	c, e := newCryptoSolana(chain, true)
	if e != nil {
		t.Fatal(e)
	}
	owner := wallet.SolanaFirstAddress
	sig, transaction := cryptoSolanaTestIncoming(owner, cryptoSolanaUSDC, "1000000", 1000, f.created)
	f.mu.Lock()
	f.incoming[sig] = transaction
	f.mu.Unlock()
	for _, mode := range []string{"missing-meta", "null-meta", "missing-err", "missing-fee", "missing-token-balances", "missing-account-index"} {
		t.Run(mode, func(t *testing.T) {
			f.mu.Lock()
			f.malformed = mode
			f.mu.Unlock()
			if _, e = c.transaction(context.Background(), sig); e == nil {
				t.Fatal("malformed execution evidence treated as paid")
			}
		})
	}
	f.mu.Lock()
	f.malformed = "missing-balance"
	f.mu.Unlock()
	if _, e = c.NativeBalance(context.Background(), owner, 1000); e == nil {
		t.Fatal("missing lamports treated as zero")
	}
	f.mu.Lock()
	f.malformed = "missing-tokens"
	f.mu.Unlock()
	if _, e = c.TokenBalance(context.Background(), cryptoSolanaUSDC, owner, 1000); e == nil {
		t.Fatal("missing token accounts treated as empty")
	}
	f.mu.Lock()
	f.malformed = ""
	f.genesis = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1"
	f.mu.Unlock()
	if e = c.Chain(context.Background()); e == nil {
		t.Fatal("testnet accepted as Solana mainnet")
	}
}
func TestCryptoSolanaSharedHistoryWatchOnlyReimport(t *testing.T) {
	a, owner, _, wallet, _ := cryptoSolanaTestFixture(t)
	allocation := cryptoSolanaTestAllocate(t, a, owner, wallet)
	wallet = allocation.Wallet
	deleted := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/delete", object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "delete_ack": true, "permanent_delete_ack": true})
	if deleted.Code != 200 {
		t.Fatal(deleted.Code, deleted.Body.String())
	}
	newWallet, _ := cryptoTestCreate(t, a, owner, false)
	if newWallet.NextIndex <= allocation.Address.Index {
		t.Fatal("Solana history high-water lost")
	}
	if e := a.store.validateCryptoWallets(true); e != nil {
		t.Fatal("public proof must authenticate Solana history without xpub seed", e)
	}
	if _, e := a.cryptoWalletPortfolio(context.Background(), newWallet.ID, "solana", -1, ""); e == nil {
		t.Fatal("external EVM xpub used as Solana derivation")
	}
}
func TestCryptoSolanaPublicReadOnlyFee(t *testing.T) {
	if os.Getenv("GY_CRYPTO_SOLANA_LIVE") != "1" {
		t.Skip("explicit read-only public RPC probe")
	}
	chain := CryptoChain{ID: "solana", ChainID: cryptoSolanaChainID, RPCURL: "https://api.mainnet-beta.solana.com", RPCBackupURL: "https://docs-demo.solana-mainnet.quiknode.pro/", NativeSymbol: "SOL"}
	c, e := newCryptoSolana(chain, false)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	address := "4js3iYAPReCCbyjcQi6hnmfy6gM4idGBRpTLtTJbkFRc"
	started := time.Now()
	var fee, balance *big.Int
	var e1, e2, e3 error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); e1 = c.chainCachedForGas(ctx) }()
	go func() { defer wg.Done(); fee, e2 = c.ReferenceFee(ctx, address) }()
	go func() { defer wg.Done(); balance, e3 = c.NativeBalance(ctx, address, 0) }()
	wg.Wait()
	if e1 != nil || e2 != nil || e3 != nil || fee == nil || fee.Sign() <= 0 || balance == nil {
		t.Fatalf("public finalized read probe elapsed %s auth=%v fee=%v balance=%v", time.Since(started), e1, e2, e3)
	}
	t.Logf("public mainnet pair authenticated; fee=%s lamports; fixture funding=%s lamports; elapsed=%s", fee, balance, time.Since(started))
}

func TestCryptoSolanaNetworkAdditionAndDTOs(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet := cryptoSelectedWallet(t, a, owner, true, []int64{56}, 201)
	in := object{"password": commerceTestPassword, "revision": wallet.Revision, "operation_id": randomToken(24), "enabled": true, "supported_chain_ids": []int64{56, 101}}
	added := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/status", in), 200)
	if added.SolanaFirstAddress == "" || added.SolanaFundingAddress == "" || added.NextIndex != 0 {
		t.Fatal("network addition did not initialize/hydrate public descriptor", added)
	}
	if got := decoded[CryptoWallet](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/status", in), 200); got.Revision != added.Revision || got.SolanaFundingAddress != added.SolanaFundingAddress {
		t.Fatal("network replay changed state")
	}
	bad := object{"password": commerceTestPassword, "revision": added.Revision, "operation_id": randomToken(24), "enabled": true, "supported_chain_ids": []int64{101}}
	if got := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/status", bad); got.Code != 400 {
		t.Fatal("existing BNB network was silently removed", got.Code)
	}
	bad["supported_chain_ids"] = []int64{56, 101}
	bad["password"] = "wrong"
	if got := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/status", bad); got.Code != 403 {
		t.Fatal("network changed without password")
	}
	bad["password"] = commerceTestPassword
	bad["revision"] = int64(1)
	if got := req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/status", bad); got.Code != 409 {
		t.Fatal("stale network change ignored CAS")
	}
	confirmed := cryptoTestConfirm(t, a, owner, added)
	if confirmed.SolanaFundingAddress != added.SolanaFundingAddress {
		t.Fatal("backup confirm lost Solana DTO")
	}
	allocation := cryptoSolanaTestAllocate(t, a, owner, confirmed)
	if allocation.Wallet.SolanaFirstAddress != added.SolanaFirstAddress {
		t.Fatal("allocation lost Solana DTO")
	}
	page := decoded[struct {
		Wallet CryptoWallet          `json:"wallet"`
		Items  []CryptoWalletAddress `json:"items"`
	}](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/addresses?chain_id=solana", nil), 200)
	if page.Wallet.SolanaFundingAddress == "" || len(page.Items) != 1 || !cryptoIsSolanaPath(page.Items[0].Path) {
		t.Fatal("address page lost public descriptor")
	}
	// A portable file is validated and re-encrypted under a genuinely fresh site vault.
	backup := cryptoTestExport(t, a, owner, allocation.Wallet)
	dump, e := cryptoDecryptBackup(backup, cryptoBackupTestPassword)
	if e != nil || cryptoValidateDump(dump) != nil {
		t.Fatal("Solana portable backup invalid", e)
	}
	other := testApp(t)
	otherOwner := testUser(t, other, "owner2", "owner")
	restored := decoded[CryptoWallet](t, req(t, other, otherOwner, "POST", cryptoWalletPrefix+"wallets/restore", object{"password": commerceTestPassword, "operation_id": randomToken(24), "backup_password": cryptoBackupTestPassword, "backup": backup}), 200)
	if restored.SolanaFundingAddress != added.SolanaFundingAddress || restored.NextIndex != allocation.Wallet.NextIndex || !restored.RecoveryRequired {
		t.Fatal("fresh-vault restore lost wallet path/high-water", restored)
	}
	if e = other.store.validateCryptoWallets(true); e != nil {
		t.Fatal("fresh-vault proof re-seal failed", e)
	}
	mergedApp := testApp(t)
	mergedOwner := testUser(t, mergedApp, "merge-owner", "owner")
	merged := cryptoSelectedWallet(t, mergedApp, mergedOwner, true, []int64{1, 56, 101}, 201)
	merged = cryptoTestConfirm(t, mergedApp, mergedOwner, merged)
	existingAllocation := cryptoSolanaTestAllocate(t, mergedApp, mergedOwner, merged)
	imported := decoded[CryptoWallet](t, req(t, mergedApp, mergedOwner, "POST", cryptoWalletPrefix+"wallets/restore", object{"password": commerceTestPassword, "operation_id": randomToken(24), "backup_password": cryptoBackupTestPassword, "backup": backup}), 200)
	if imported.ID != existingAllocation.Wallet.ID {
		t.Fatal("same-descriptor backup created duplicate wallet")
	}
	if e = mergedApp.store.validateCryptoWallets(true); e != nil {
		t.Fatal("merge used backup row identity instead of actual allocation", e)
	}

}
func TestCryptoSolanaRentBudgetAndExpiredSignature(t *testing.T) {
	for _, mode := range []string{"rent-increase", "ata-closed", "expired", "receipt-over-budget", "receipt-missing-balances"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, user, wallet, f := cryptoSolanaTestFixture(t)
			allocation := cryptoSolanaTestOrderAllocate(t, a, owner, user, wallet)
			wallet = allocation.Wallet
			destination, _ := cryptoSolanaDerive(cryptoTestMnemonic, 10)
			f.mu.Lock()
			f.tokens[allocation.Address.Address] = map[string]string{cryptoSolanaUSDC: "2000000"}
			if mode == "ata-closed" {
				f.tokens[destination] = map[string]string{cryptoSolanaUSDC: "0"}
			}
			f.mu.Unlock()
			preview, e := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{ChainID: "solana", AssetID: "solana-usdc", Destination: destination, MinAtoms: "1"})
			if e != nil || !preview.CanSubmit {
				t.Fatal(preview, e)
			}
			if mode == "ata-closed" {
				message, e := cryptoSolanaSweepMessage(cryptoSweepQuote{Preview: preview, Asset: CryptoAsset{Contract: cryptoSolanaUSDC}}, preview.Items[0], cryptoSolanaFixtureHash(1000))
				if e != nil || bytes.Contains(message, base58.Decode(cryptoSolanaATAProgram)) {
					t.Fatal("zero rent authorization unexpectedly permits ATA creation", e)
				}
			}
			job, e := a.cryptoCreateSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)})
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			if mode == "rent-increase" {
				f.rent *= 2
			}
			if mode == "ata-closed" {
				delete(f.tokens, destination)
			}
			f.mu.Unlock()
			if mode == "rent-increase" || mode == "ata-closed" {
				if e = a.cryptoSweepWork(context.Background()); e == nil {
					t.Fatal("unapproved ATA rent signed or broadcast")
				}
				var count int
				_ = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_solana_transactions WHERE job_id=?", job.ID).Scan(&count)
				if count != 0 {
					t.Fatal("unapproved rent signed a transaction")
				}
				return
			}
			if e = a.cryptoSweepWork(context.Background()); e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			if mode == "expired" {
				f.height = 3000
			} else {
				for sig := range f.raw {
					f.mined[sig] = true
				}
				if mode == "receipt-over-budget" {
					f.rent *= 2
				} else {
					f.malformed = "missing-balances"
				}
			}
			f.mu.Unlock()
			if e = a.cryptoSweepWork(context.Background()); e == nil {
				t.Fatal("invalid/over-budget/unconfirmed receipt completed")
			}
			var state string
			_ = a.store.db.QueryRow("SELECT state FROM crypto_solana_transactions WHERE job_id=?", job.ID).Scan(&state)
			if state == "confirmed" {
				t.Fatal("bad proof marked confirmed")
			}
			f.mu.Lock()
			sends := f.sends
			f.mu.Unlock()
			if mode == "expired" {
				if state != "expired_unknown" {
					t.Fatal(state)
				}
				_ = a.cryptoSweepWork(context.Background())
				f.mu.Lock()
				if f.sends != sends || len(f.raw) != 1 {
					t.Fatal("expired signature was replaced or resent")
				}
				f.mu.Unlock()
				if e = a.store.validateCryptoSweeps(true); e != nil {
					t.Fatal("durable unknown signature invalid", e)
				}
			}
		})
	}
}
func TestCryptoSolanaSelectedAddressBeyondFirstThousand(t *testing.T) {
	a, owner, _, wallet, _ := cryptoSolanaTestFixture(t)
	allocation := cryptoSolanaTestAllocate(t, a, owner, wallet)
	tx, e := a.store.cryptoWalletTx(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for i := int64(1); i < 1002; i++ {
		address := CryptoWalletAddress{ID: fmt.Sprintf("public-over-limit-%d", i), WalletID: wallet.ID, Index: i, Path: cryptoSolanaPath(uint32(i)), Address: cryptoSolanaFixtureHash(uint64(i + 9000)), Created: time.Now().Unix()}
		if _, e = tx.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", address.ID, address.WalletID, address.Index, address.Path, address.Address, "", address.Created); e != nil {
			t.Fatal(e)
		}
	}
	selected, e := cryptoSolanaSelectedAddresses(tx, wallet.ID, []string{"public-over-limit-1001"})
	if e != nil || len(selected) != 1 || selected[0].Index != 1001 {
		t.Fatal("selected later page address incorrectly rejected", selected, e)
	}
	if _, e = cryptoSolanaSelectedAddresses(tx, wallet.ID, nil); e == nil {
		t.Fatal("unbounded empty selection ignored batch limit")
	}
	if _, e = cryptoSolanaSelectedAddresses(tx, wallet.ID, []string{allocation.Address.ID, "wrong-wallet"}); e == nil {
		t.Fatal("foreign selected ID accepted")
	}
}

func TestCryptoSolanaOfficialSDKWireAndSignatures(t *testing.T) {
	// Independently serialized/signed with the official Solana JS SDKs. Only
	// public fixture keys/transactions are included; these addresses have no funds.
	bytesJSON, e := os.ReadFile("testdata/crypto-solana-web3-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var vectors struct {
		SDK, Source, Funding, Destination, Blockhash string
		USDCATA                                      string `json:"usdc_ata"`
		USDTATA                                      string `json:"usdt_ata"`
		Vectors                                      []struct{ Name, Message, Raw string }
	}
	if e = json.Unmarshal(bytesJSON, &vectors); e != nil {
		t.Fatal(e)
	}
	for _, mint := range []struct{ mint, address string }{{cryptoSolanaUSDC, vectors.USDCATA}, {cryptoSolanaUSDT, vectors.USDTATA}} {
		got, e := cryptoSolanaATA(vectors.Source, mint.mint)
		if e != nil || got != mint.address {
			t.Fatal("official SDK PDA/ATA mismatch", got, e)
		}
	}
	funding, e := cryptoSolanaKey(cryptoTestMnemonic, cryptoSolanaFundingIndex)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(funding)
	source, e := cryptoSolanaKey(cryptoTestMnemonic, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(source)
	for _, v := range vectors.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			var message []byte
			var e error
			if v.Name == "native" {
				message, e = cryptoSolanaNativeMessage(vectors.Funding, vectors.Source, vectors.Destination, vectors.Blockhash, 1)
			} else {
				message, e = cryptoSolanaTokenMessage(vectors.Funding, vectors.Source, vectors.Destination, cryptoSolanaUSDC, vectors.Blockhash, []cryptoSolanaTokenAccount{{vectors.USDCATA, "2000000"}}, v.Name == "usdc_create_ata")
			}
			if e != nil {
				t.Fatal(e)
			}
			if base64.StdEncoding.EncodeToString(message) != v.Message {
				t.Fatalf("%s official account metas/message mismatch got=%s want=%s", v.Name, base64.StdEncoding.EncodeToString(message), v.Message)
			}
			raw := []byte{2}
			raw = append(raw, ed25519.Sign(funding, message)...)
			raw = append(raw, ed25519.Sign(source, message)...)
			raw = append(raw, message...)
			if base64.StdEncoding.EncodeToString(raw) != v.Raw {
				t.Fatalf("%s official raw/signature mismatch", v.Name)
			}
		})
	}
	for _, create := range []bool{true, false} {
		for count := 1; count <= 20; count++ {
			accounts := []cryptoSolanaTokenAccount{{vectors.USDCATA, "1"}}
			for i := 1; i < count; i++ {
				accounts = append(accounts, cryptoSolanaTokenAccount{cryptoSolanaFixtureHash(uint64(i + 8000)), "1"})
			}
			message, e := cryptoSolanaTokenMessage(vectors.Funding, vectors.Source, vectors.Destination, cryptoSolanaUSDC, vectors.Blockhash, accounts, create)
			expected := 375 + 49*(count-1)
			if create {
				expected += 106
			}
			if expected > 1232 {
				if e == nil {
					t.Fatalf("over-packet-cap %d-byte signed transaction accepted", expected)
				}
			} else if e != nil || len(message)+129 != expected {
				t.Fatal("packet boundary mismatch", create, count, expected, len(message)+129, e)
			}
		}
	}
}

func TestCryptoSolanaWrongMintAndLatePaymentNeverActivate(t *testing.T) {
	for _, mode := range []string{"wrong-mint", "late"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, user, _, f := cryptoSolanaTestFixture(t)
			order := newOrder(t, a, user, commerceOffer(t, a, owner))
			invoice := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": "solana-usdc", "operation_id": randomToken(24)}), 201)
			mint := cryptoSolanaUSDC
			slot := uint64(1001)
			timestamp := f.created + 1
			if mode == "wrong-mint" {
				mint = cryptoSolanaUSDT
			} else {
				timestamp = invoice.Expires
				slot = uint64(timestamp-f.created) + 1000
			}
			signature, transfer := cryptoSolanaTestIncoming(invoice.Address, mint, invoice.ExpectedAtoms, slot, timestamp)
			f.mu.Lock()
			f.slot = slot
			f.tokens[invoice.Address] = map[string]string{mint: invoice.ExpectedAtoms}
			f.incoming[signature] = transfer
			f.history[cryptoSolanaFixtureATA(invoice.Address, mint)] = []string{signature}
			f.mu.Unlock()
			stored, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
			if e != nil {
				t.Fatal(e)
			}
			if e = a.scanCryptoInvoice(context.Background(), stored); e != nil {
				t.Fatal(e)
			}
			if e = a.paymentWork(); e != nil {
				t.Fatal(e)
			}
			if e = a.commerceWork(time.Now().Unix()); e != nil {
				t.Fatal(e)
			}
			got, e := a.store.order(order.ID)
			if e != nil || got.State == "completed" {
				t.Fatal("wrong asset/late transaction activated package", got.State, e)
			}
			var paid int
			if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", order.ID).Scan(&paid); e != nil || paid != 0 {
				t.Fatal("unexpected payment receipt", paid, e)
			}
			if e = a.store.validateCommerce(true); e != nil {
				t.Fatal("review payment integrity", e)
			}
		})
	}
}
func TestCryptoSolanaPendingRebroadcastAuthenticatesMainnet(t *testing.T) {
	a, owner, _, wallet, f := cryptoSolanaTestFixture(t)
	allocation := cryptoSolanaTestAllocate(t, a, owner, wallet)
	wallet = allocation.Wallet
	destination, _ := cryptoSolanaDerive(cryptoTestMnemonic, 10)
	f.mu.Lock()
	f.native[allocation.Address.Address] = 1000000
	f.mu.Unlock()
	preview, e := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{ChainID: "solana", AssetID: "native", Destination: destination, MinAtoms: "1"})
	if e != nil || !preview.CanSubmit {
		t.Fatal(preview, e)
	}
	_, e = a.cryptoCreateSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	before := f.sends
	f.genesis = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1"
	f.mu.Unlock()
	if e = a.cryptoSweepWork(context.Background()); e == nil {
		t.Fatal("pending signature retried against devnet RPC")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sends != before || len(f.raw) != 1 {
		t.Fatal("unauthenticated RPC received signed transaction")
	}
}
func TestCryptoSolanaRPCDiagnosticsDoNotChangeSafetyTuple(t *testing.T) {
	owner, _ := cryptoSolanaDerive(cryptoTestMnemonic, 0)
	_, transaction := cryptoSolanaTestIncoming(owner, cryptoSolanaUSDC, "1000000", 1000, time.Now().Unix())
	a := jsonBytes(transaction)
	var changed object
	_ = json.Unmarshal(a, &changed)
	changed["meta"].(map[string]any)["computeUnitsConsumed"] = float64(7321)
	changed["meta"].(map[string]any)["costUnits"] = float64(9650)
	var out cryptoSolanaRPCTransaction
	if e := cryptoSolanaTypedSame(a, jsonBytes(changed), &out); e != nil {
		t.Fatal("nonfinancial RPC diagnostics blocked valid finalized payment", e)
	}
	changed["meta"].(map[string]any)["fee"] = float64(10000)
	if e := cryptoSolanaTypedSame(a, jsonBytes(changed), &out); e == nil {
		t.Fatal("RPC fee disagreement ignored")
	}
}

func TestCryptoSolanaHeightBoundsBeforeSigning(t *testing.T) {
	a, _, _, _, f := cryptoSolanaTestFixture(t)
	settings, _ := a.store.cryptoPaymentSettings()
	chain, _ := settings.chain(101)
	client, e := newCryptoSolana(chain, true)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.height = 1 << 63
	f.mu.Unlock()
	if _, _, e = client.latestBlockhash(context.Background()); e == nil {
		t.Fatal("out-of-database-range lastValidBlockHeight accepted")
	}
}

// Read-only integration smoke check for the default providers' complete invoice
// preflight, including official mint ownership/precision and SPL account history.
func TestCryptoSolanaBuiltinReadOnlyPreflight(t *testing.T) {
	if os.Getenv("RUN_CRYPTO_NETWORK_PROBE") != "1" {
		t.Skip("explicit public preflight RPC probe disabled")
	}
	chain := CryptoChain{ID: "solana", ChainID: 101, RPCURL: "https://api.mainnet-beta.solana.com", RPCBackupURL: "https://docs-demo.solana-mainnet.quiknode.pro/", NativeSymbol: "SOL"}
	client, e := newCryptoSolana(chain, true)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	head, e := client.Finalized(ctx)
	if e != nil {
		t.Fatal("default pair canonical finalized block", e)
	}
	for _, mint := range []string{cryptoSolanaUSDC, cryptoSolanaUSDT} {
		if e = client.ValidateToken(ctx, mint, 6, head.Number); e != nil {
			t.Fatal("default pair official mint", e)
		}
		accounts, e := client.tokenAccounts(ctx, mint, "4js3iYAPReCCbyjcQi6hnmfy6gM4idGBRpTLtTJbkFRc", head.Number)
		if e != nil {
			t.Fatal("default pair owned token accounts", e)
		}
		ata, e := cryptoSolanaATA("4js3iYAPReCCbyjcQi6hnmfy6gM4idGBRpTLtTJbkFRc", mint)
		if e != nil {
			t.Fatal(e)
		}
		signatures, e := client.signatures(ctx, ata, head.Number, head.Number)
		if e != nil {
			t.Fatal("default pair finalized SPL history", e)
		}
		t.Logf("default mainnet mint ownership+6 decimals verified; mint=%s slot=%d fixture token_accounts=%d recent_signatures=%d", mint, head.Number, len(accounts), len(signatures))
	}
	if signature := os.Getenv("GY_CRYPTO_SOLANA_KNOWN_TX"); signature != "" {
		transaction, e := client.transaction(ctx, signature)
		if e != nil || transaction == nil {
			t.Fatal("default pair existing finalized transaction metadata", e)
		}
		block, e := client.block(ctx, transaction.Slot)
		if e != nil || transaction.BlockTime == nil || uint64(*transaction.BlockTime) != block.Timestamp {
			t.Fatal("existing finalized transaction canonical block", e)
		}
		t.Logf("default pair real finalized transaction safety tuple+block verified; version=%s slot=%d fee_lamports=%d", transaction.Version, transaction.Slot, transaction.Meta.Fee)
	}
}
