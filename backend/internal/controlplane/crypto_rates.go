package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const cryptoCoinGeckoURL = "https://api.coingecko.com/api/v3/simple/price?ids=tether,usd-coin&vs_currencies=cny,usd&include_last_updated_at=true&precision=8"
const cryptoCoinbaseURL = "https://api.coinbase.com/v2/exchange-rates?currency=USD"

var errCryptoRateUnsafe = errors.New("行情超出稳定币安全范围或两个来源分歧过大，暂停新报价")

type cryptoMarketRates struct {
	Source           string
	Updated, Expires int64
	CNY              map[string]string
}

func cryptoRateNumber(raw string) (*big.Rat, error) {
	// Providers may use more decimals than the persisted quote. Never pass
	// financial quantities through float64, exponent notation or fractions.
	if raw == "" || len(raw) > 64 || strings.Count(raw, ".") > 1 {
		return nil, errCryptoRateUnsafe
	}
	for _, r := range raw {
		if (r < '0' || r > '9') && r != '.' {
			return nil, errCryptoRateUnsafe
		}
	}
	v, ok := new(big.Rat).SetString(raw)
	if !ok || v.Sign() <= 0 {
		return nil, errCryptoRateUnsafe
	}
	return v, nil
}
func cryptoRateBound(v *big.Rat, low, high string) bool {
	l, _ := new(big.Rat).SetString(low)
	h, _ := new(big.Rat).SetString(high)
	return v.Cmp(l) >= 0 && v.Cmp(h) <= 0
}
func cryptoRateDecimal(v *big.Rat) string {
	// Round CNY/token down, so rounding never undercharges the order.
	n := new(big.Int).Mul(v.Num(), big.NewInt(100000000))
	n.Quo(n, v.Denom())
	digits := n.String()
	for len(digits) < 9 {
		digits = "0" + digits
	}
	return strings.TrimRight(strings.TrimRight(digits[:len(digits)-8]+"."+digits[len(digits)-8:], "0"), ".")
}
func cryptoRateFresh(at, now int64) bool { return at > now-600 && at <= now+120 }

func cryptoRateGET(ctx context.Context, client *http.Client, address string) ([]byte, http.Header, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, nil, e
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "Guangyue-Panel/crypto-rates")
	response, e := client.Do(r)
	if e != nil {
		return nil, nil, errors.New("自动行情来源暂时不可用")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, errors.New("自动行情来源暂时不可用")
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 128<<10+1))
	if e != nil || len(raw) > 128<<10 {
		return nil, nil, errors.New("自动行情响应无效")
	}
	return raw, response.Header, nil
}
func cryptoCoinGeckoRates(raw []byte, now int64) (cryptoMarketRates, error) {
	var body map[string]struct {
		CNY     json.Number `json:"cny"`
		USD     json.Number `json:"usd"`
		Updated int64       `json:"last_updated_at"`
	}
	out := cryptoMarketRates{Source: "CoinGecko", Updated: now, CNY: map[string]string{}}
	if json.Unmarshal(raw, &body) != nil {
		return out, errors.New("自动行情响应无效")
	}
	var incomplete error
	for _, coin := range []struct{ id, symbol string }{{"tether", "USDT"}, {"usd-coin", "USDC"}} {
		v, ok := body[coin.id]
		if !ok || !cryptoRateFresh(v.Updated, now) {
			incomplete = errors.New("自动行情已过期")
			continue
		}
		cny, e := cryptoRateNumber(v.CNY.String())
		usd, u := cryptoRateNumber(v.USD.String())
		if e != nil || u != nil || !cryptoRateBound(cny, "3", "20") || !cryptoRateBound(usd, "0.95", "1.05") {
			return out, errCryptoRateUnsafe
		}
		out.CNY[coin.symbol] = cryptoRateDecimal(cny)
		out.Updated = min(out.Updated, v.Updated)
	}
	if incomplete != nil {
		return out, incomplete
	}
	out.Expires = min(now+600, out.Updated+900)
	return out, nil
}
func cryptoCoinbaseRates(raw []byte, headers http.Header, now int64) (cryptoMarketRates, error) {
	var body struct {
		Data struct {
			Currency string            `json:"currency"`
			Rates    map[string]string `json:"rates"`
		} `json:"data"`
	}
	out := cryptoMarketRates{Source: "Coinbase", CNY: map[string]string{}}
	date, e := http.ParseTime(headers.Get("Date"))
	// Coinbase does not provide an asset update timestamp. Bind the quote to
	// the HTTP response date, cap it to 5 minutes, and account for the cache Age.
	if e != nil || !cryptoRateFresh(date.Unix(), now) {
		return out, errors.New("自动行情已过期")
	}
	if rawAge := headers.Get("Age"); rawAge != "" {
		age, e := strconv.ParseInt(rawAge, 10, 64)
		if e != nil || age < 0 || age >= 300 {
			return out, errors.New("自动行情已过期")
		}
		date = time.Unix(min(date.Unix(), now-age), 0)
	}
	if json.Unmarshal(raw, &body) != nil || body.Data.Currency != "USD" {
		return out, errors.New("自动行情响应无效")
	}
	cny, e := cryptoRateNumber(body.Data.Rates["CNY"])
	if e != nil || !cryptoRateBound(cny, "3", "20") {
		return out, errCryptoRateUnsafe
	}
	for _, symbol := range []string{"USDT", "USDC"} {
		perUSD, e := cryptoRateNumber(body.Data.Rates[symbol])
		if e != nil {
			return out, e
		}
		usd := new(big.Rat).Inv(perUSD)
		if !cryptoRateBound(usd, "0.95", "1.05") {
			return out, errCryptoRateUnsafe
		}
		out.CNY[symbol] = cryptoRateDecimal(new(big.Rat).Mul(cny, usd))
	}
	out.Updated, out.Expires = min(date.Unix(), now), min(date.Unix(), now)+300
	if out.Expires <= now {
		return out, errors.New("自动行情已过期")
	}
	return out, nil
}
func cryptoFetchMarketRates(ctx context.Context, client *http.Client, geckoURL, coinbaseURL string, now int64) (cryptoMarketRates, error) {
	type result struct {
		rates cryptoMarketRates
		err   error
	}
	results := make(chan result, 2)
	for index, address := range []string{geckoURL, coinbaseURL} {
		go func(index int, address string) {
			raw, headers, e := cryptoRateGET(ctx, client, address)
			var value cryptoMarketRates
			if e == nil {
				if index == 0 {
					value, e = cryptoCoinGeckoRates(raw, now)
				} else {
					value, e = cryptoCoinbaseRates(raw, headers, now)
				}
			}
			results <- result{value, e}
		}(index, address)
	}
	x, y := <-results, <-results
	if errors.Is(x.err, errCryptoRateUnsafe) || errors.Is(y.err, errCryptoRateUnsafe) {
		return cryptoMarketRates{}, errCryptoRateUnsafe
	}
	if x.err != nil && y.err != nil {
		return cryptoMarketRates{}, errors.New("自动行情来源暂时不可用，已有有效报价保留至到期")
	}
	if x.err != nil {
		return y.rates, nil
	}
	if y.err != nil {
		return x.rates, nil
	}
	out := cryptoMarketRates{Source: "CoinGecko + Coinbase", Updated: min(x.rates.Updated, y.rates.Updated), Expires: min(x.rates.Expires, y.rates.Expires), CNY: map[string]string{}}
	for _, symbol := range []string{"USDT", "USDC"} {
		a, _ := cryptoRateNumber(x.rates.CNY[symbol])
		b, _ := cryptoRateNumber(y.rates.CNY[symbol])
		low, high := a, b
		if low.Cmp(high) > 0 {
			low, high = high, low
		}
		if new(big.Rat).Quo(high, low).Cmp(big.NewRat(102, 100)) > 0 {
			return cryptoMarketRates{}, errCryptoRateUnsafe
		}
		out.CNY[symbol] = cryptoRateDecimal(low)
	}
	return out, nil
}
func (a *App) fetchCryptoRates(ctx context.Context) (cryptoMarketRates, error) {
	if a.cryptoRateFetch != nil {
		return a.cryptoRateFetch(ctx)
	}
	client := &http.Client{Transport: cryptoProductionRPCTransport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("行情来源不允许重定向") }}
	return cryptoFetchMarketRates(ctx, client, cryptoCoinGeckoURL, cryptoCoinbaseURL, time.Now().Unix())
}
func cryptoApplyMarketRates(settings *CryptoPaymentSettings, rates cryptoMarketRates) error {
	now := time.Now().Unix()
	if rates.Source == "" || !cryptoRateFresh(rates.Updated, now) || rates.Expires <= now || rates.Expires > now+900 {
		return errors.New("自动行情已过期")
	}
	for i := range settings.Assets {
		asset := &settings.Assets[i]
		if asset.ChainID != 1 && asset.ChainID != 56 {
			continue
		}
		rate, e := cryptoRateNumber(rates.CNY[asset.Symbol])
		if e != nil || !cryptoRateBound(rate, "3", "20") {
			return errCryptoRateUnsafe
		}
		asset.CNYPerToken, asset.RateSource, asset.RateUpdatedAt, asset.RateExpiresAt = cryptoRateDecimal(rate), rates.Source, rates.Updated, rates.Expires
	}
	settings.RateMode, settings.RateSource, settings.RateCheckedAt, settings.RateError = "automatic", rates.Source, now, ""
	return nil
}
func (a *App) refreshCryptoAutoRates(ctx context.Context) error {
	if a.cfg.businessAgent() || a.store.commerceErr != nil || !a.cryptoRateMu.TryLock() {
		return nil
	}
	defer a.cryptoRateMu.Unlock()
	if time.Now().Before(a.cryptoRateNextAttempt) {
		return nil
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	if settings.RateMode != "automatic" || !settings.Enabled {
		return nil
	}
	due := false
	for _, asset := range settings.Assets {
		if asset.Enabled && asset.RateExpiresAt <= time.Now().Unix()+150 {
			due = true
		}
	}
	if !due {
		return nil
	}
	a.cryptoRateNextAttempt = time.Now().Add(time.Minute)
	work, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	rates, rateErr := a.fetchCryptoRates(work)
	if rateErr == nil {
		rateErr = cryptoApplyMarketRates(&settings, rates)
	}
	if rateErr != nil {
		settings.RateError = rateErr.Error()
		if errors.Is(rateErr, errCryptoRateUnsafe) {
			for i := range settings.Assets {
				settings.Assets[i].RateExpiresAt = min(settings.Assets[i].RateExpiresAt, time.Now().Unix())
			}
		}
	}
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var revision int64
	if e = tx.QueryRow("SELECT revision FROM crypto_payment_settings WHERE id=1").Scan(&revision); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if revision != settings.Revision {
		return nil
	} // A concurrent explicit edit wins.
	settings.Revision++
	sealed, e := a.store.vault.seal(settings)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE crypto_payment_settings SET revision=?,doc=? WHERE id=1", settings.Revision, sealed); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	return rateErr
}
