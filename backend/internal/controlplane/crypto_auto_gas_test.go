package controlplane

import (
	"context"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"testing"
)

func TestCryptoAutoGasThresholdSixAndEighteenDecimals(t *testing.T) {
	for _, decimals := range []int{6, 18} {
		asset := CryptoAsset{Decimals: decimals}
		threshold := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals-1)), nil)
		for _, delta := range []int64{-100000, -1, 0, 1, 100000} {
			balance := new(big.Int).Add(threshold, big.NewInt(delta))
			if got := cryptoAutoGasEligible(asset, balance); got != (delta > 0) {
				t.Fatalf("decimals=%d balance=%s eligible=%v", decimals, balance, got)
			}
		}
		if cryptoAutoGasEligible(asset, new(big.Int)) || cryptoAutoGasEligible(asset, nil) {
			t.Fatal("empty balance eligible")
		}
	}
	if cryptoAutoGasEligible(CryptoAsset{Decimals: 0}, big.NewInt(1)) || cryptoAutoGasEligible(CryptoAsset{Decimals: 19}, big.NewInt(1)) {
		t.Fatal("invalid precision eligible")
	}
}

func TestCryptoAutoGasOrderAddressAndLiveBalanceGate(t *testing.T) {
	for _, mode := range []string{"zero", "below", "exact", "above", "manual", "manual_self_funded", "different_order_coin", "wrong_chain", "wrong_invoice_address", "wrong_order_user", "deleted_invoice"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, wallet, f := cryptoSweepFixture(t)
			input := cryptoSweepTestInput()
			f.mu.Lock()
			switch mode {
			case "zero":
				f.tokens[strings.ToLower(f.receive)] = big.NewInt(0)
			case "below":
				f.tokens[strings.ToLower(f.receive)] = big.NewInt(99999)
			case "exact":
				f.tokens[strings.ToLower(f.receive)] = big.NewInt(100000)
			case "above":
				f.tokens[strings.ToLower(f.receive)] = big.NewInt(100001)
			case "different_order_coin":
				input.AssetID = "ethereum-usdt"
				asset, _ := cryptoDefaultSettings().asset(input.AssetID)
				f.contract = asset.Contract
			}
			f.mu.Unlock()
			switch mode {
			case "manual", "manual_self_funded":
				allocation := cryptoTestAllocate(t, a, owner, wallet)
				input.AddressIDs = []string{allocation.Address.ID}
				f.mu.Lock()
				f.tokens[strings.ToLower(allocation.Address.Address)] = big.NewInt(2000000)
				if mode == "manual_self_funded" {
					f.native[strings.ToLower(allocation.Address.Address)] = big.NewInt(100000000)
				}
				f.mu.Unlock()
			case "wrong_chain":
				if _, err := a.store.db.Exec("UPDATE crypto_invoices SET chain_id=56"); err != nil {
					t.Fatal(err)
				}
			case "wrong_invoice_address":
				if _, err := a.store.db.Exec("UPDATE crypto_invoices SET address='0x3333333333333333333333333333333333333333'"); err != nil {
					t.Fatal(err)
				}
			case "wrong_order_user":
				if _, err := a.store.db.Exec("UPDATE crypto_invoices SET user_id=user_id+100"); err != nil {
					t.Fatal(err)
				}
			case "deleted_invoice":
				if _, err := a.store.db.Exec("DELETE FROM crypto_invoices"); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			want := mode == "above" || mode == "manual_self_funded" || mode == "different_order_coin"
			if preview.CanSubmit != want || len(preview.Items) != boolCount(want) {
				t.Fatalf("mode=%s preview=%+v", mode, preview)
			}
			if mode == "manual_self_funded" && preview.Items[0].TopupAtoms != "0" {
				t.Fatal("manual address received automatic gas")
			}
		})
	}
}

func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestCryptoAutoGasRechecksOrderAndTokenBalanceBeforeSigning(t *testing.T) {
	for _, mode := range []string{"empty", "exact_threshold", "less_than_approved", "wrong_chain", "removed_invoice", "changed_order_user"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, wallet, f := cryptoSweepFixture(t)
			preview := cryptoSweepTestPreview(t, a, owner, wallet)
			job := cryptoSweepTestCreate(t, a, owner, wallet, preview)
			switch mode {
			case "empty", "exact_threshold", "less_than_approved":
				amount := int64(0)
				if mode == "exact_threshold" {
					amount = 100000
				}
				if mode == "less_than_approved" {
					amount = 1999999
				}
				f.mu.Lock()
				f.tokens[strings.ToLower(f.receive)] = big.NewInt(amount)
				f.mu.Unlock()
			case "wrong_chain":
				if _, err := a.store.db.Exec("UPDATE crypto_invoices SET chain_id=56"); err != nil {
					t.Fatal(err)
				}
			case "removed_invoice":
				if _, err := a.store.db.Exec("DELETE FROM crypto_invoices"); err != nil {
					t.Fatal(err)
				}
			case "changed_order_user":
				if _, err := a.store.db.Exec("UPDATE crypto_invoices SET user_id=user_id+100"); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.cryptoSweepWork(context.Background()); err == nil || cryptoSweepTestCount(t, a) != 0 {
				t.Fatal("stale preview funded an ineligible address", err)
			}
			current, err := a.store.cryptoSweepJob(job.ID)
			if err != nil || current.State != "waiting" {
				t.Fatal(current, err)
			}
			if f.sends != 0 {
				t.Fatal("stale preview broadcast new gas")
			}
		})
	}
}

func TestCryptoAutoGasExactTopupAndDefaultBuffer(t *testing.T) {
	a, owner, wallet, f := cryptoSweepFixture(t)
	f.mu.Lock()
	f.native[strings.ToLower(f.receive)] = big.NewInt(1000000)
	f.mu.Unlock()
	preview := cryptoSweepTestPreview(t, a, owner, wallet)
	plan := preview.Items[0]
	if plan.GasLimit != 55000 || plan.GasPriceAtoms != "110" || plan.GasFeeAtoms != "6050000" || plan.TopupAtoms != "5050000" || plan.FundingGasLimit != 23100 {
		t.Fatal("buffer exceeds approved 10% price and gas limits", plan)
	}
	job := cryptoSweepTestCreate(t, a, owner, wallet, preview)
	// Incoming native funds after approval are deducted from the top-up.
	f.mu.Lock()
	f.native[strings.ToLower(f.receive)] = big.NewInt(2000000)
	f.mu.Unlock()
	if err := a.cryptoSweepWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	var amount, price string
	var gas uint64
	if err := a.store.db.QueryRow("SELECT amount_atoms,gas_price_atoms,gas_limit FROM crypto_chain_transactions WHERE job_id=? AND kind='gas'", job.ID).Scan(&amount, &price, &gas); err != nil {
		t.Fatal(err)
	}
	if amount != "4050000" || price != "110" || gas != 23100 {
		t.Fatal("top-up overfunded an existing native balance", amount, price, gas)
	}
}

func TestCryptoAutoGasRejectsWrongWalletAndUnapprovedAsset(t *testing.T) {
	a, _, wallet, _ := cryptoSweepFixture(t)
	var addressID string
	if err := a.store.db.QueryRow("SELECT address_id FROM crypto_invoices WHERE wallet_id=?", wallet.ID).Scan(&addressID); err != nil {
		t.Fatal(err)
	}
	asset, _ := cryptoDefaultSettings().asset("ethereum-usdc")
	for _, mutation := range []string{"wallet", "chain", "contract", "decimals", "unknown_asset"} {
		id, chain, copy := wallet.ID, int64(1), asset
		switch mutation {
		case "wallet":
			id = "missing-wallet"
		case "chain":
			copy.ChainID = 56
		case "contract":
			copy.Contract = "0x3333333333333333333333333333333333333333"
		case "decimals":
			copy.Decimals = 18
		case "unknown_asset":
			copy.ID = "unapproved-token"
		}
		if ok, err := cryptoAutoGasOrder(a.store.db, id, chain, addressID, copy); err != nil || ok {
			t.Fatal("unowned or unsupported asset got automatic gas", mutation, ok, err)
		}
	}
}

func TestCryptoSweepFeeSelectionAndSealedQuote(t *testing.T) {
	a, owner, wallet, _ := cryptoSweepFixture(t)
	for _, test := range []struct{ mode, custom, price, fee string }{{"economy", "", "100", "7810000"}, {"standard", "", "110", "8591000"}, {"priority", "", "120", "9372000"}, {"custom", "123", "123", "9606300"}} {
		in := cryptoSweepTestInput()
		in.FeeMode, in.GasPriceAtoms = test.mode, test.custom
		preview, err := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, in)
		if err != nil || !preview.CanSubmit || preview.FeeMode != test.mode || preview.GasPriceAtoms != test.price || preview.TotalGasAtoms != test.fee {
			t.Fatal(test, preview, err)
		}
		if len(preview.FeeOptions) != 3 || preview.FeeOptions[0].ID != "economy" || preview.FeeOptions[0].GasPriceAtoms != "100" || preview.FeeOptions[1].TotalGasAtoms != "8591000" {
			t.Fatal(preview.FeeOptions)
		}
		sealed, err := base64.StdEncoding.DecodeString(preview.Quote)
		if err != nil {
			t.Fatal(err)
		}
		var quote cryptoSweepQuote
		if err = a.store.vault.open(sealed, &quote); err != nil {
			t.Fatal(err)
		}
		if quote.Preview.FeeMode != test.mode || quote.Preview.GasPriceAtoms != test.price || quote.Preview.Items[0].GasPriceAtoms != test.price {
			t.Fatal("fee choice was not frozen", quote)
		}
	}
	for _, input := range []cryptoSweepInput{{FeeMode: "custom", GasPriceAtoms: "99"}, {FeeMode: "custom", GasPriceAtoms: "126"}, {FeeMode: "custom", GasPriceAtoms: "100.1"}, {FeeMode: "custom", GasPriceAtoms: "000100"}, {FeeMode: "custom"}, {FeeMode: "priority", GasPriceAtoms: "123"}, {FeeMode: "unsupported"}} {
		if _, _, err := cryptoSweepSelectFee(input, big.NewInt(100)); err == nil {
			t.Fatal("invalid custom fee accepted", input)
		}
	}
}

func TestCryptoEconomicFeeDoesNotDoubleApplyPriceBuffer(t *testing.T) {
	a, owner, wallet, _ := cryptoSweepFixture(t)
	in := cryptoSweepTestInput()
	in.FeeMode = "economy"
	preview, err := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cryptoSweepTestCreate(t, a, owner, wallet, preview)
	if err = a.cryptoSweepWork(context.Background()); err != nil || cryptoSweepTestCount(t, a) != 1 {
		t.Fatal("economic fee rejected unchanged unbuffered market price", err)
	}
}

func TestCryptoSweepFinancialReviewCannotRetry(t *testing.T) {
	a, owner, wallet, _ := cryptoSweepFixture(t)
	job := cryptoSweepTestCreate(t, a, owner, wallet, cryptoSweepTestPreview(t, a, owner, wallet))
	for _, state := range []string{"review_required", "failed"} {
		if _, err := a.store.db.Exec("UPDATE crypto_sweep_jobs SET state=? WHERE id=?", state, job.ID); err != nil {
			t.Fatal(err)
		}
		current, err := a.store.cryptoSweepJob(job.ID)
		if err != nil || current.CanRetry {
			t.Fatal("financial conflict exposes retry", current, err)
		}
		if err = a.cryptoRetrySweep(context.Background(), owner, job.ID, cryptoSweepInput{OperationID: randomToken(24), Password: commerceTestPassword}); err == nil || errors.Is(err, errCryptoGasReview) {
			t.Fatal("financial conflict accepted fee retry", err)
		}
	}
}

func TestCryptoSweepGasLimitIncreaseRequiresManualReview(t *testing.T) {
	for _, kind := range []string{"token", "funding", "native"} {
		t.Run(kind, func(t *testing.T) {
			a, owner, wallet, f := cryptoSweepFixture(t)
			in := cryptoSweepTestInput()
			if kind == "native" {
				in.AssetID = "native"
				f.mu.Lock()
				f.native[strings.ToLower(f.receive)] = big.NewInt(100000000)
				f.mu.Unlock()
			}
			preview, err := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, in)
			if err != nil || !preview.CanSubmit {
				t.Fatal(preview, err)
			}
			job := cryptoSweepTestCreate(t, a, owner, wallet, preview)
			f.mu.Lock()
			if kind == "token" {
				f.tokenGas = 55001
			} else {
				f.nativeGas = 23101
			}
			f.mu.Unlock()
			if err = a.cryptoSweepWork(context.Background()); !errors.Is(err, errCryptoGasReview) || cryptoSweepTestCount(t, a) != 0 {
				t.Fatal("unsigned operation did not pause for an estimate above approval", err)
			}
			current, err := a.store.cryptoSweepJob(job.ID)
			if err != nil || current.State != "fee_review_required" {
				t.Fatal(current, err)
			}
			f.mu.Lock()
			f.tokenGas, f.nativeGas = 0, 0
			f.mu.Unlock()
			if err = a.cryptoSweepWork(context.Background()); err != nil || cryptoSweepTestCount(t, a) != 0 {
				t.Fatal("lower estimate automatically resumed", err)
			}
			if err = a.cryptoRetrySweep(context.Background(), owner, job.ID, cryptoSweepInput{OperationID: randomToken(24), Password: commerceTestPassword}); err != nil {
				t.Fatal(err)
			}
			if err = a.cryptoSweepWork(context.Background()); err != nil || cryptoSweepTestCount(t, a) != 1 {
				t.Fatal("manual retry did not keep original limits", err)
			}
		})
	}
}
