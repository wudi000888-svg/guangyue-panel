package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"time"
)

// CoinGecko's keyless feed refreshes about once per minute. Gas RPC requests
// remain five-second reads; this quote is only a display estimate, never an
// order exchange rate, transfer budget, signing input or settlement authority.
// https://docs.coingecko.com/reference/simple-price
const cryptoNativePricesURL = "https://api.coingecko.com/api/v3/simple/price?ids=ethereum,binancecoin,solana,plasma&vs_currencies=usd,cny&include_last_updated_at=true&precision=8"

type cryptoNativePrice struct {
	USD, CNY, Source string
	Updated, Expires int64
}

func cryptoParseNativePrices(raw []byte, now int64) (map[string]cryptoNativePrice, error) {
	var body map[string]struct {
		USD, CNY json.Number
		Updated  int64 `json:"last_updated_at"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, errors.New("原生币行情响应无效")
	}
	out := map[string]cryptoNativePrice{}
	for _, coin := range []struct{ id, symbol string }{{"ethereum", "ETH"}, {"binancecoin", "BNB"}, {"solana", "SOL"}, {"plasma", "XPL"}} {
		v, ok := body[coin.id]
		// Never invent prices for missing assets, turn malformed responses into
		// zero, or combine provider USD with a fixed or unrelated CNY exchange rate.
		if !ok || v.Updated <= now-300 || v.Updated > now+120 {
			continue
		}
		usd, e := cryptoRateNumber(v.USD.String())
		cny, c := cryptoRateNumber(v.CNY.String())
		if e != nil || c != nil || !cryptoRateBound(usd, "0.00000001", "10000000") || !cryptoRateBound(cny, "0.00000001", "1000000000") {
			continue
		}
		x := new(big.Rat).Quo(cny, usd)
		if !cryptoRateBound(x, "3", "20") {
			continue
		}
		out[coin.symbol] = cryptoNativePrice{USD: cryptoRateDecimal(usd), CNY: cryptoRateDecimal(cny), Source: "CoinGecko", Updated: v.Updated, Expires: min(now+300, v.Updated+300)}
	}
	if len(out) == 0 {
		return nil, errors.New("原生币行情已过期或暂不可用")
	}
	return out, nil
}

func (a *App) fetchCryptoNativePrices(ctx context.Context) (map[string]cryptoNativePrice, error) {
	if a.cryptoNativePriceFetch != nil {
		return a.cryptoNativePriceFetch(ctx)
	}
	client := &http.Client{Transport: cryptoProductionRPCTransport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("行情来源不允许重定向") }}
	raw, _, e := cryptoRateGET(ctx, client, cryptoNativePricesURL)
	if e != nil {
		return nil, errors.New("原生币参考行情暂不可用")
	}
	return cryptoParseNativePrices(raw, time.Now().Unix())
}

// The asynchronous single-flight refresh is bounded to six seconds and at
// most once a minute per process, regardless of visible wallet/network count.
// Stale metadata stays explicit; no failure is passed off as a fresh price.
func (a *App) cryptoNativePrice(symbol string) (cryptoNativePrice, string) {
	now := time.Now()
	a.cryptoNativePriceMu.Lock()
	if !a.cryptoNativePriceBusy && !now.Before(a.cryptoNativePriceNext) {
		a.cryptoNativePriceBusy = true
		a.cryptoNativePriceNext = now.Add(time.Minute)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			values, e := a.fetchCryptoNativePrices(ctx)
			a.cryptoNativePriceMu.Lock()
			defer a.cryptoNativePriceMu.Unlock()
			a.cryptoNativePriceBusy = false
			if e != nil {
				a.cryptoNativePriceError = "原生币参考行情暂不可用，保留上次行情时间"
				return
			}
			if a.cryptoNativePrices == nil {
				a.cryptoNativePrices = map[string]cryptoNativePrice{}
			}
			for k, v := range values {
				a.cryptoNativePrices[k] = v
			}
			a.cryptoNativePriceError = ""
		}()
	}
	v, ok := a.cryptoNativePrices[symbol]
	err := a.cryptoNativePriceError
	if !ok {
		if a.cryptoNativePriceBusy {
			err = "正在获取原生币参考行情"
		} else if err == "" {
			err = "此原生币参考行情暂不可用"
		}
	} else if v.Expires <= now.Unix() {
		err = "原生币参考行情已过期，等待重新查询"
	}
	a.cryptoNativePriceMu.Unlock()
	return v, err
}

func (a *App) cryptoDecorateGasFiat(v *CryptoGasSnapshot, chain CryptoChain) {
	v.NativeDecimals = cryptoNativeDecimals(chain)
	v.FeeModel = "evm"
	if chain.ChainID == 101 {
		v.FeeModel = "solana"
	}
	price, e := a.cryptoNativePrice(chain.NativeSymbol)
	v.NativePriceUSD, v.NativePriceCNY = price.USD, price.CNY
	v.PriceCheckedAt, v.PriceExpiresAt = price.Updated, price.Expires
	v.FiatSource, v.FiatError = price.Source, e
}

func (a *App) cryptoDecorateSweepFiat(v *CryptoSweepPreview) {
	v.NativeDecimals = 18
	v.FeeModel = "evm"
	if v.ChainID == "solana" {
		v.NativeDecimals, v.FeeModel = 9, "solana"
	}
	price, e := a.cryptoNativePrice(v.NativeSymbol)
	v.NativePriceUSD, v.NativePriceCNY = price.USD, price.CNY
	v.PriceCheckedAt, v.PriceExpiresAt = price.Updated, price.Expires
	v.FiatSource, v.FiatError = price.Source, e
}
