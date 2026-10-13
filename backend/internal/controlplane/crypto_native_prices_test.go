package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCryptoNativePricesFreshnessPrecisionAndChainIdentity(t *testing.T) {
	now := time.Now().Unix()
	input := fmt.Sprintf(`{"ethereum":{"usd":2512.27394915,"cny":16816.15690604,"last_updated_at":%d},"binancecoin":{"usd":741.72324984,"cny":4964.79874516,"last_updated_at":%d},"solana":{"usd":106.92065258,"cny":715.68408011,"last_updated_at":%d},"plasma":{"usd":0.08146879,"cny":0.54531951,"last_updated_at":%d}}`, now, now, now, now)
	prices, e := cryptoParseNativePrices([]byte(input), now)
	if e != nil || len(prices) != 4 {
		t.Fatalf("prices %v %v", prices, e)
	}
	for symbol, expected := range map[string]string{"ETH": "2512.27394915", "BNB": "741.72324984", "SOL": "106.92065258", "XPL": "0.08146879"} {
		v := prices[symbol]
		if v.USD != expected || v.CNY == "" || v.Source != "CoinGecko" || v.Updated != now || v.Expires != now+300 {
			t.Fatalf("incorrect price metadata %s %+v", symbol, v)
		}
	}
}

func TestCryptoNativePricesRejectMissingExpiredAndFabricatedQuotes(t *testing.T) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name, usd, cny string
		at             int64
	}{
		{"stale", "100", "670", now - 300},
		{"future", "100", "670", now + 121},
		{"zero", "0", "670", now},
		{"negative", "-100", "670", now},
		{"exponent", "1e2", "670", now},
		{"missing_cny", "100", "0", now},
		{"invalid_conversion", "100", "100", now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"solana":{"usd":%s,"cny":%s,"last_updated_at":%d}}`, tc.usd, tc.cny, tc.at)
			prices, e := cryptoParseNativePrices([]byte(body), now)
			if e == nil || len(prices) != 0 {
				t.Fatal("invalid price must not become a zero or fresh estimate", prices, e)
			}
		})
	}
	// A missing Plasma price does not invent an XPL/USD rate or prevent a valid
	// ETH fee estimate. Each network's market value is independent.
	prices, e := cryptoParseNativePrices([]byte(fmt.Sprintf(`{"ethereum":{"usd":100,"cny":670,"last_updated_at":%d}}`, now)), now)
	if e != nil || len(prices) != 1 || prices["ETH"].USD != "100" {
		t.Fatal(prices, e)
	}
}

func TestCryptoNativePricesDoNotBlockGasAndSingleFlightAcrossWallets(t *testing.T) {
	a := &App{}
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	a.cryptoNativePriceFetch = func(context.Context) (map[string]cryptoNativePrice, error) {
		calls.Add(1)
		close(started)
		<-release
		now := time.Now().Unix()
		return map[string]cryptoNativePrice{"SOL": {USD: "106.92065258", CNY: "715.68408011", Source: "fixture", Updated: now, Expires: now + 300}}, nil
	}
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			before := time.Now()
			v, e := a.cryptoNativePrice("SOL")
			if time.Since(before) > time.Second || v.USD != "" || e == "" {
				t.Error("pending market request blocked or fabricated a price", v, e)
			}
		})
	}
	wg.Wait()
	<-started
	if calls.Load() != 1 {
		t.Fatal("market feed multiplied by wallet count", calls.Load())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		v, e := a.cryptoNativePrice("SOL")
		if v.USD != "" {
			if e != "" || v.Source != "fixture" || v.CNY != "715.68408011" {
				t.Fatal(v, e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("asynchronous price never became available")
		}
		time.Sleep(time.Millisecond)
	}
	for range 10 {
		a.cryptoNativePrice("SOL")
	}
	if calls.Load() != 1 {
		t.Fatal("five-second gas polls bypassed one-minute market cache")
	}
}

func TestCryptoNativePricesOutageRetainsOriginalTimeAndMarksExpired(t *testing.T) {
	now := time.Now().Unix()
	a := &App{cryptoNativePrices: map[string]cryptoNativePrice{"ETH": {USD: "2512.27394915", CNY: "16816.15690604", Source: "fixture", Updated: now - 400, Expires: now - 100}}}
	finished := make(chan struct{})
	a.cryptoNativePriceFetch = func(context.Context) (map[string]cryptoNativePrice, error) {
		defer close(finished)
		return nil, errors.New("fixture offline")
	}
	v, e := a.cryptoNativePrice("ETH")
	if e == "" || v.Updated != now-400 || v.Expires != now-100 {
		t.Fatal("expired price presented as new", v, e)
	}
	<-finished
	deadline := time.Now().Add(time.Second)
	for {
		a.cryptoNativePriceMu.Lock()
		busy := a.cryptoNativePriceBusy
		a.cryptoNativePriceMu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("market refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	v, e = a.cryptoNativePrice("ETH")
	if e == "" || v.USD != "2512.27394915" || v.CNY != "16816.15690604" || v.Updated != now-400 || v.Expires != now-100 {
		t.Fatal("market failure erased history or refreshed its timestamp", v, e)
	}
}
