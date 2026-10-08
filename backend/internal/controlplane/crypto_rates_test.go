package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func cryptoTestRates() cryptoMarketRates {
	now := time.Now().Unix()
	return cryptoMarketRates{Source: "CoinGecko + Coinbase", Updated: now, Expires: now + 300, CNY: map[string]string{"USDT": "7.12345678", "USDC": "7.1"}}
}

func TestCryptoRatesSourcesBoundsFreshnessAndFallback(t *testing.T) {
	now := time.Now().Unix()
	gecko := fmt.Sprintf(`{"tether":{"cny":7.12,"usd":0.9999,"last_updated_at":%d},"usd-coin":{"cny":7.12,"usd":1,"last_updated_at":%d}}`, now, now)
	coinbase := `{"data":{"currency":"USD","rates":{"CNY":"7.12","USDT":"1.00010001","USDC":"1"}}}`
	for _, tc := range []struct {
		name, g, c, date, age string
		gc, cc                int
		wantErr               bool
		source                string
	}{
		{"both", gecko, coinbase, "", "", 200, 200, false, "CoinGecko + Coinbase"},
		{"gecko_quota", `{}`, coinbase, "", "", 429, 200, false, "Coinbase"},
		{"coinbase_unavailable", gecko, `{}`, "", "", 200, 503, false, "CoinGecko"},
		{"both_down", `{}`, `{}`, "", "", 429, 503, true, ""},
		{"stale_gecko", `{"tether":{"cny":7,"usd":1,"last_updated_at":1}}`, coinbase, "", "", 200, 200, false, "Coinbase"},
		{"stale_coinbase", `{}`, coinbase, time.Unix(now-700, 0).UTC().Format(http.TimeFormat), "", 503, 200, true, ""},
		{"stale_cache", `{}`, coinbase, "", "600", 503, 200, true, ""},
		{"depeg", fmt.Sprintf(`{"tether":{"cny":7,"usd":0.8,"last_updated_at":%d},"usd-coin":{"cny":7,"usd":1,"last_updated_at":%d}}`, now, now), coinbase, "", "", 200, 200, true, ""},
		{"stale_and_depeg", fmt.Sprintf(`{"tether":{"cny":7,"usd":1,"last_updated_at":1},"usd-coin":{"cny":7,"usd":0.8,"last_updated_at":%d}}`, now), coinbase, "", "", 200, 200, true, ""},
		{"missing_and_depeg", fmt.Sprintf(`{"usd-coin":{"cny":7,"usd":0.8,"last_updated_at":%d}}`, now), coinbase, "", "", 200, 200, true, ""},
		{"disagree", gecko, `{"data":{"currency":"USD","rates":{"CNY":"8","USDT":"1","USDC":"1"}}}`, "", "", 200, 200, true, ""},
		{"invalid_base", gecko, `{"data":{"currency":"EUR","rates":{"CNY":"7","USDT":"1","USDC":"1"}}}`, "", "", 200, 200, false, "CoinGecko"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/gecko" {
					w.WriteHeader(tc.gc)
					_, _ = w.Write([]byte(tc.g))
					return
				}
				date := tc.date
				if date == "" {
					date = time.Unix(now, 0).UTC().Format(http.TimeFormat)
				}
				w.Header().Set("Date", date)
				if tc.age != "" {
					w.Header().Set("Age", tc.age)
				}
				w.WriteHeader(tc.cc)
				_, _ = w.Write([]byte(tc.c))
			}))
			defer server.Close()
			v, e := cryptoFetchMarketRates(context.Background(), server.Client(), server.URL+"/gecko", server.URL+"/coinbase", now)
			if (e != nil) != tc.wantErr {
				t.Fatalf("unexpected result %v %+v", e, v)
			}
			if !tc.wantErr && (v.Source != tc.source || v.Expires <= now || v.Expires > now+900) {
				t.Fatalf("invalid metadata %+v", v)
			}
			if tc.name == "both" && v.CNY["USDT"] != "7.11928800" && v.CNY["USDT"] != "7.119288" {
				t.Fatalf("decimal quote changed: %s", v.CNY["USDT"])
			}
		})
	}
}

func TestCryptoRatesRefreshExpiryAndConcurrentEdit(t *testing.T) {
	a, _, _, _ := cryptoSweepFixture(t)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	if e = cryptoApplyMarketRates(&s, cryptoTestRates()); e != nil {
		t.Fatal(e)
	}
	save := func(s CryptoPaymentSettings) {
		t.Helper()
		raw, e := a.store.vault.seal(s)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET revision=?,doc=? WHERE id=1", s.Revision, raw); e != nil {
			t.Fatal(e)
		}
	}
	for i := range s.Assets {
		if s.Assets[i].Enabled {
			s.Assets[i].RateExpiresAt = time.Now().Unix() + 100
		}
	}
	save(s)
	a.cryptoRateFetch = func(context.Context) (cryptoMarketRates, error) {
		return cryptoMarketRates{}, errors.New("fixture unavailable")
	}
	if e = a.refreshCryptoAutoRates(context.Background()); e == nil {
		t.Fatal("expected provider failure")
	}
	after, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	oldAsset, _ := s.asset("ethereum-usdc")
	asset, _ := after.asset("ethereum-usdc")
	if asset.CNYPerToken != oldAsset.CNYPerToken || asset.RateExpiresAt != oldAsset.RateExpiresAt || after.RateError == "" {
		t.Fatal("valid quote lost during outage")
	}
	a.cryptoRateNextAttempt = time.Time{}
	a.cryptoRateFetch = func(context.Context) (cryptoMarketRates, error) { return cryptoMarketRates{}, errCryptoRateUnsafe }
	if e = a.refreshCryptoAutoRates(context.Background()); !errors.Is(e, errCryptoRateUnsafe) {
		t.Fatal(e)
	}
	after, e = a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	asset, _ = after.asset("ethereum-usdc")
	if asset.RateExpiresAt > time.Now().Unix() {
		t.Fatal("unsafe market remained payable")
	}
	a.cryptoRateNextAttempt = time.Time{}
	a.cryptoRateFetch = func(context.Context) (cryptoMarketRates, error) {
		current, e := a.store.cryptoPaymentSettings()
		if e != nil {
			t.Fatal(e)
		}
		current.Revision++
		current.RateMode = "manual"
		current.Enabled = false
		save(current)
		return cryptoTestRates(), nil
	}
	if e = a.refreshCryptoAutoRates(context.Background()); e != nil {
		t.Fatal(e)
	}
	after, e = a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	if after.RateMode != "manual" || after.Enabled {
		t.Fatal("background quote overwrote explicit settings")
	}
}

func TestCryptoRatesInvoiceSnapshotExpiryAndWalletScope(t *testing.T) {
	a, owner, user, wallet, _ := cryptoPaymentFixture(t)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	if e = cryptoApplyMarketRates(&s, cryptoTestRates()); e != nil {
		t.Fatal(e)
	}
	save := func(s CryptoPaymentSettings) {
		t.Helper()
		raw, e := a.store.vault.seal(s)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET revision=?,doc=? WHERE id=1", s.Revision, raw); e != nil {
			t.Fatal(e)
		}
	}
	save(s)
	order, invoice := cryptoPaymentInvoice(t, a, owner, user)
	if invoice.RateSource != "CoinGecko + Coinbase" || invoice.CNYPerToken != "7.1" {
		t.Fatal("invoice did not freeze automatic quote", invoice)
	}
	index := cryptoTestCurrent(t, a, wallet.ID).NextIndex
	for i := range s.Assets {
		if s.Assets[i].Enabled {
			s.Assets[i].RateExpiresAt = time.Now().Unix() - 1
		}
	}
	s.Revision++
	save(s)
	a.cryptoRateFetch = func(context.Context) (cryptoMarketRates, error) {
		return cryptoMarketRates{}, errors.New("fixture offline")
	}
	w := req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)})
	if w.Code != 409 || cryptoTestCurrent(t, a, wallet.ID).NextIndex != index {
		t.Fatal("expired auto quote allocated an address", w.Code, w.Body.String())
	}
	frozen, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
	if e != nil {
		t.Fatal(e)
	}
	if frozen.CNYPerToken != invoice.CNYPerToken || frozen.RateExpiresAt != invoice.RateExpiresAt || frozen.ExpectedAtoms != invoice.ExpectedAtoms {
		t.Fatal("historical frozen invoice was repriced")
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
	s, e = a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	if e = cryptoApplyMarketRates(&s, cryptoTestRates()); e != nil {
		t.Fatal(e)
	}
	s.Revision++
	save(s)
	if _, e = a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[56]' WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	other := testUser(t, a, "other-rate-user", "user")
	newOrder := newOrder(t, a, other, commerceOffer(t, a, owner))
	w = req(t, a, other, "POST", "/api/commerce/crypto/invoices", object{"order_id": newOrder.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)})
	if w.Code != 409 || cryptoTestCurrent(t, a, wallet.ID).NextIndex != index {
		t.Fatal("wallet scope allocated wrong-network invoice", w.Code, w.Body.String())
	}
}
