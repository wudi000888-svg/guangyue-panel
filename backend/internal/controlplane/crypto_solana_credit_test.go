package controlplane

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func cryptoSolanaDecodeCredit(t *testing.T, raw object, signature, owner string) []cryptoScannedTransfer {
	t.Helper()
	var tx cryptoSolanaRPCTransaction
	if e := json.Unmarshal(jsonBytes(raw), &tx); e != nil {
		t.Fatal(e)
	}
	credits, e := cryptoSolanaTransfers(&tx, signature, cryptoSolanaUSDC, owner, cryptoEVMBlock{Number: tx.Slot, Hash: cryptoSolanaFixtureHash(tx.Slot)})
	if e != nil {
		t.Fatal(e)
	}
	return credits
}

func TestCryptoSolanaCreditsOnlyActualOwnerNetGain(t *testing.T) {
	owner, _ := cryptoSolanaDerive(cryptoTestMnemonic, 0)
	for _, mode := range []string{"ordinary", "failed-cpi", "partly-failed-cpi", "internal-shuffle", "auxiliary-account"} {
		t.Run(mode, func(t *testing.T) {
			sig, raw := cryptoSolanaTestIncoming(owner, cryptoSolanaUSDC, "1000000", 1000, 1700000000)
			message := raw["transaction"].(object)["message"].(object)
			meta := raw["meta"].(object)
			ix := message["instructions"].([]object)[0]
			switch mode {
			case "failed-cpi":
				message["instructions"] = []object{{"programId": cryptoSolanaSystemProgram}}
				meta["innerInstructions"] = []object{{"index": 0, "instructions": []object{ix}}}
				meta["postTokenBalances"].([]object)[1]["uiTokenAmount"].(object)["amount"] = "0"
			case "partly-failed-cpi":
				meta["innerInstructions"] = []object{{"index": 0, "instructions": []object{ix}}}
			case "internal-shuffle":
				meta["preTokenBalances"].([]object)[0]["owner"] = owner
				meta["postTokenBalances"].([]object)[0]["owner"] = owner
				meta["postTokenBalances"].([]object)[0]["uiTokenAmount"].(object)["amount"] = "0"
			case "auxiliary-account":
				aux := cryptoSolanaFixtureHash(9001)
				message["accountKeys"].([]object)[1]["pubkey"] = aux
				ix["parsed"].(object)["info"].(object)["destination"] = aux
			}
			credits := cryptoSolanaDecodeCredit(t, raw, sig, owner)
			if mode == "failed-cpi" || mode == "internal-shuffle" {
				if len(credits) != 0 {
					t.Fatal("attempted CPI/shuffled own tokens credited", credits)
				}
				return
			}
			if len(credits) != 1 || credits[0].Log.Index != 2000000001 || credits[0].Log.Data != "0xf4240" {
				t.Fatal("credit is not stable actual owner net gain", credits)
			}
			if (mode == "ordinary") != (credits[0].ReviewReason == "") {
				t.Fatal("ambiguous/auxiliary account auto-credited", credits)
			}
		})
	}
}

func TestCryptoSolanaReviewedNetCreditNeverReinterpretedAsPaid(t *testing.T) {
	for _, mode := range []string{"ambiguous-cpi", "canonical-ata-delegate"} {
		t.Run(mode, func(t *testing.T) {
			a, admin, user, _, f := cryptoSolanaTestFixture(t)
			order := newOrder(t, a, user, commerceOffer(t, a, admin))
			invoice := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": "solana-usdc", "operation_id": randomToken(24)}), 201)
			invoice, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			f.slot = 1001
			sig, raw := cryptoSolanaTestIncoming(invoice.Address, cryptoSolanaUSDC, invoice.ExpectedAtoms, 1001, f.created+1)
			message := raw["transaction"].(object)["message"].(object)
			if mode == "ambiguous-cpi" {
				raw["meta"].(object)["innerInstructions"] = []object{{"index": 0, "instructions": message["instructions"]}}
			} else {
				delegate, _ := cryptoSolanaDerive(cryptoTestMnemonic, 12)
				f.delegates = map[string]string{invoice.Address: delegate}
			}
			f.incoming[sig] = raw
			f.tokens[invoice.Address] = map[string]string{cryptoSolanaUSDC: invoice.ExpectedAtoms}
			f.history[cryptoSolanaFixtureATA(invoice.Address, cryptoSolanaUSDC)] = []string{sig}
			f.mu.Unlock()
			if e := a.scanCryptoInvoice(context.Background(), invoice); e != nil {
				t.Fatal(e)
			}
			var state, atoms string
			if e := a.store.db.QueryRow("SELECT state,atoms FROM crypto_transfers WHERE invoice_id=?", invoice.ID).Scan(&state, &atoms); e != nil || state != "review_required" || atoms != invoice.ExpectedAtoms {
				t.Fatal("unproved credit became a payment", state, atoms, e)
			}
			// Changing the parser interpretation/permissions must not invent a
			// second receipt or promote a prior reviewed credit automatically.
			f.mu.Lock()
			raw["meta"].(object)["innerInstructions"] = []object{}
			f.delegates = nil
			f.mu.Unlock()
			if _, e := a.store.db.Exec("UPDATE crypto_invoice_chains SET next_block=?,finalized_hash='' WHERE invoice_id=? AND chain_id=101", invoice.NextBlock, invoice.ID); e != nil {
				t.Fatal(e)
			}
			if e := a.scanCryptoInvoice(context.Background(), invoice); e != nil {
				t.Fatal(e)
			}
			var count int
			if e := a.store.db.QueryRow("SELECT COUNT(*),MIN(state) FROM crypto_transfers WHERE invoice_id=?", invoice.ID).Scan(&count, &state); e != nil || count != 1 || state != "review_required" {
				t.Fatal("rescan changed credit representation", count, state, e)
			}
			if e := a.paymentWork(); e != nil {
				t.Fatal(e)
			}
			if e := a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", order.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal("reviewed credit fulfilled order", count, e)
			}
			if e := a.store.validateCommerce(true); e != nil {
				t.Fatal("net credit audit integrity", e)
			}
		})
	}
}

func TestCryptoSolanaFeePayerRequiresOrderAndStrictThreshold(t *testing.T) {
	a, admin, user, wallet, f := cryptoSolanaTestFixture(t)
	manual := cryptoSolanaTestAllocate(t, a, admin, wallet)
	allocation := cryptoSolanaTestOrderAllocate(t, a, admin, user, manual.Wallet)
	destination, _ := cryptoSolanaDerive(cryptoTestMnemonic, 10)
	f.mu.Lock()
	f.tokens[manual.Address.Address] = map[string]string{cryptoSolanaUSDC: "5000000"}
	f.mu.Unlock()
	for _, value := range []string{"99999", "100000", "100001"} {
		f.mu.Lock()
		f.tokens[allocation.Address.Address] = map[string]string{cryptoSolanaUSDC: value}
		f.mu.Unlock()
		preview, e := a.cryptoPreviewSweep(context.Background(), admin, wallet.ID, cryptoSweepInput{ChainID: "solana", AssetID: "solana-usdc", Destination: destination, MinAtoms: "1"})
		if e != nil {
			t.Fatal(e)
		}
		if preview.CanSubmit != (value == "100001") {
			t.Fatal("strict >0.1 gate not enforced", value, preview.CanSubmit)
		}
		if len(preview.Items) > 0 && (len(preview.Items) != 1 || preview.Items[0].AddressID != allocation.Address.ID) {
			t.Fatal("manual address received treasury fee allowance", preview.Items)
		}
		f.mu.Lock()
		sends := f.sends
		f.mu.Unlock()
		if sends != 0 {
			t.Fatal("preview sent a transaction")
		}
	}
	preview, e := a.cryptoPreviewSweep(context.Background(), admin, wallet.ID, cryptoSweepInput{ChainID: "solana", AssetID: "solana-usdc", Destination: destination, MinAtoms: "1"})
	if e != nil {
		t.Fatal(e)
	}
	job, e := a.cryptoCreateSweep(context.Background(), admin, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)})
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.tokens[allocation.Address.Address][cryptoSolanaUSDC] = "100000"
	f.mu.Unlock()
	if e = a.cryptoSweepWork(context.Background()); e == nil {
		t.Fatal("balance at exact threshold signed")
	}
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_solana_transactions WHERE job_id=?", job.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("threshold recheck signed", count, e)
	}
	if !cryptoAutoGasEligible(CryptoAsset{Decimals: 6}, big.NewInt(100001)) || cryptoAutoGasEligible(CryptoAsset{Decimals: 6}, big.NewInt(100000)) {
		t.Fatal("strict threshold math")
	}
}

func TestCryptoSolanaFeeSpikePausesUntilPasswordRetry(t *testing.T) {
	a, admin, user, wallet, f := cryptoSolanaTestFixture(t)
	allocation := cryptoSolanaTestOrderAllocate(t, a, admin, user, wallet)
	destination, _ := cryptoSolanaDerive(cryptoTestMnemonic, 10)
	f.mu.Lock()
	f.tokens[allocation.Address.Address] = map[string]string{cryptoSolanaUSDC: "2000000"}
	f.mu.Unlock()
	preview, e := a.cryptoPreviewSweep(context.Background(), admin, wallet.ID, cryptoSweepInput{ChainID: "solana", AssetID: "solana-usdc", Destination: destination, MinAtoms: "1"})
	if e != nil || !preview.CanSubmit {
		t.Fatal(preview, e)
	}
	job, e := a.cryptoCreateSweep(context.Background(), admin, wallet.ID, cryptoSweepInput{Quote: preview.Quote, OperationID: randomToken(24)})
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.feePerSignature = 10000
	f.mu.Unlock()
	if e = a.cryptoSweepWork(context.Background()); e == nil {
		t.Fatal("fee spike was silently accepted")
	}
	paused, e := a.store.cryptoSweepJob(job.ID)
	if e != nil || paused.State != "fee_review_required" || !paused.CanRetry {
		t.Fatal("fee spike did not require administrator review", paused, e)
	}
	if e = a.store.validateCryptoSweeps(true); e != nil {
		t.Fatal("paused intent integrity", e)
	}
	if e = a.cryptoRetrySweep(context.Background(), admin, job.ID, cryptoSweepInput{Password: commerceTestPassword, OperationID: randomToken(24)}); e == nil {
		t.Fatal("retry exceeded original fee approval")
	}
	f.mu.Lock()
	f.feePerSignature = 5000
	f.mu.Unlock()
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	sends := f.sends
	f.mu.Unlock()
	if sends != 0 {
		t.Fatal("fee decrease automatically resumed paused transfer")
	}
	if e = a.cryptoRetrySweep(context.Background(), admin, job.ID, cryptoSweepInput{Password: "wrong", OperationID: randomToken(24)}); e == nil {
		t.Fatal("fee pause bypassed password")
	}
	if e = a.cryptoRetrySweep(context.Background(), admin, job.ID, cryptoSweepInput{Password: commerceTestPassword, OperationID: randomToken(24)}); e != nil {
		t.Fatal("approved fee retry failed", e)
	}
	if e = a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_solana_transactions WHERE job_id=?", job.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("manual retry did not persist exactly one signature", count, e)
	}
}

func TestCryptoSolanaCreditAnchorUsesPostOwnerAndUnrelatedMintsDoNotBlock(t *testing.T) {
	owner, _ := cryptoSolanaDerive(cryptoTestMnemonic, 0)
	other, _ := cryptoSolanaDerive(cryptoTestMnemonic, 1)
	sig, raw := cryptoSolanaTestIncoming(owner, cryptoSolanaUSDC, "1000000", 1000, 1700000000)
	message := raw["transaction"].(object)["message"].(object)
	meta := raw["meta"].(object)
	keys := message["accountKeys"].([]object)
	keys = append(keys, object{"pubkey": cryptoSolanaFixtureHash(501)}, object{"pubkey": cryptoSolanaFixtureHash(502)})
	message["accountKeys"] = keys
	balance := func(index int, mint, who, amount string) object {
		return object{"accountIndex": index, "mint": mint, "owner": who, "programId": cryptoSolanaTokenProgram, "uiTokenAmount": object{"amount": amount, "decimals": 6}}
	}
	// A unrelated mint account can be closed/reinitialized in this transaction;
	// its metadata must not permanently block a canonical USDC payment.
	pre := meta["preTokenBalances"].([]object)
	post := meta["postTokenBalances"].([]object)
	meta["preTokenBalances"] = append(pre, balance(4, cryptoSolanaUSDT, other, "100"))
	meta["postTokenBalances"] = append(post, balance(4, cryptoSolanaFixtureHash(503), other, "100"))
	credits := cryptoSolanaDecodeCredit(t, raw, sig, owner)
	if len(credits) != 1 || credits[0].ReviewReason != "" {
		t.Fatal("unrelated account transition blocked canonical credit", credits)
	}
	// Owner-changing account 0 is not an identity anchor for its old owner.
	meta["preTokenBalances"] = []object{balance(0, cryptoSolanaUSDC, owner, "1")}
	meta["postTokenBalances"] = []object{balance(0, cryptoSolanaUSDC, other, "2"), balance(1, cryptoSolanaUSDC, owner, "1000001")}
	first := cryptoSolanaDecodeCredit(t, raw, sig, owner)
	second := cryptoSolanaDecodeCredit(t, raw, sig, other)
	if len(first) != 1 || len(second) != 1 || first[0].Log.Index == second[0].Log.Index {
		t.Fatal("owner change collided global receipt identities", first, second)
	}
}

func TestCryptoSolanaUnknownTransactionVersionsRemainUncredited(t *testing.T) {
	a, _, _, wallet, f := cryptoSolanaTestFixture(t)
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	chain, _ := settings.chain(cryptoSolanaChainID)
	c, e := newCryptoSolana(chain, true)
	if e != nil {
		t.Fatal(e)
	}
	sig, raw := cryptoSolanaTestIncoming(wallet.SolanaFirstAddress, cryptoSolanaUSDC, "1000000", 1000, f.created)
	for _, version := range []any{"legacy", 0, 1, 2, 3, nil} {
		f.mu.Lock()
		raw["version"] = version
		f.incoming[sig] = raw
		f.mu.Unlock()
		_, err := c.transaction(context.Background(), sig)
		accepted := version == "legacy" || version == 0 || version == 1
		if accepted != (err == nil) {
			t.Fatal("unsupported transaction version consumed", version, err)
		}
	}
	f.mu.Lock()
	f.malformed = "unsupported-version"
	f.mu.Unlock()
	if _, err := c.transaction(context.Background(), sig); err == nil || !strings.Contains(err.Error(), "交易版本") || strings.Contains(err.Error(), "diagnostic") {
		t.Fatal("unsupported version RPC error lost safe actionable message", err)
	}
}
