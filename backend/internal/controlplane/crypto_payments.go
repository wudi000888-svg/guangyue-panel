package controlplane

import (
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type CryptoChain struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ChainID          int64  `json:"chain_id"`
	RPCURL           string `json:"rpc_url,omitempty"`
	RPCBackupURL     string `json:"rpc_backup_url,omitempty"`
	HasRPC           bool   `json:"has_rpc"`
	HasRPCBackup     bool   `json:"has_rpc_backup"`
	NativeSymbol     string `json:"native_symbol"`
	Enabled          bool   `json:"enabled"`
	FinalityVerified bool   `json:"finality_verified"`
	RPCSource        string `json:"rpc_source"`
}
type CryptoAsset struct {
	ID              string `json:"id"`
	ChainID         int64  `json:"chain_id"`
	Symbol          string `json:"symbol"`
	Name            string `json:"name"`
	Contract        string `json:"contract"`
	Decimals        int    `json:"decimals"`
	CNYPerToken     string `json:"cny_per_token"`
	RateUpdatedAt   int64  `json:"rate_updated_at"`
	RateExpiresAt   int64  `json:"rate_expires_at"`
	PaymentDecimals int    `json:"payment_decimals"`
	Enabled         bool   `json:"enabled"`
	RateSource      string `json:"rate_source"`
}
type CryptoPaymentSettings struct {
	Revision       int64         `json:"revision"`
	Enabled        bool          `json:"enabled"`
	WalletID       string        `json:"wallet_id"`
	InvoiceMinutes int           `json:"invoice_minutes"`
	Chains         []CryptoChain `json:"chains"`
	Assets         []CryptoAsset `json:"assets"`
	RateMode       string        `json:"rate_mode"`
	RateSource     string        `json:"rate_source"`
	RateCheckedAt  int64         `json:"rate_checked_at"`
	RateError      string        `json:"rate_error"`
	SetupCheckedAt int64         `json:"setup_checked_at"`
}

func cryptoDefaultSettings() CryptoPaymentSettings {
	s := CryptoPaymentSettings{InvoiceMinutes: 30, Chains: []CryptoChain{
		{ID: "ethereum", Name: "Ethereum", ChainID: 1, NativeSymbol: "ETH"},
		{ID: "bsc", Name: "BNB Smart Chain", ChainID: 56, NativeSymbol: "BNB"},
		{ID: "arbitrum", Name: "Arbitrum One", ChainID: 42161, NativeSymbol: "ETH"},
		{ID: "op", Name: "OP Mainnet", ChainID: 10, NativeSymbol: "ETH"},
		{ID: "base", Name: "Base", ChainID: 8453, NativeSymbol: "ETH"},
	}}
	add := func(id string, c int64, symbol, name, contract string, decimals int) {
		s.Assets = append(s.Assets, CryptoAsset{ID: id, ChainID: c, Symbol: symbol, Name: name, Contract: strings.ToLower(contract), Decimals: decimals, PaymentDecimals: 2})
	}
	add("ethereum-usdt", 1, "USDT", "USDT · Ethereum", "0xdAC17F958D2ee523a2206206994597C13D831ec7", 6)
	add("ethereum-usdc", 1, "USDC", "USDC · Ethereum", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", 6)
	add("bsc-usdt", 56, "USDT", "USDT · BNB Smart Chain · Binance-Peg", "0x55d398326f99059fF775485246999027B3197955", 18)
	add("bsc-usdc", 56, "USDC", "USDC · BNB Smart Chain · Binance-Peg", "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d", 18)
	add("arbitrum-usdc", 42161, "USDC", "USDC · Arbitrum One", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831", 6)
	add("arbitrum-usdt0", 42161, "USDT0", "USDT0 · Arbitrum One", "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9", 6)
	add("op-usdc", 10, "USDC", "USDC · OP Mainnet", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85", 6)
	add("op-usdt0", 10, "USDT0", "USDT0 · OP Mainnet", "0x01bFF41798a0BcF287b996046Ca68b395DbC1071", 6)
	add("base-usdc", 8453, "USDC", "USDC · Base", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", 6)
	return s
}
func (s *Store) cryptoPaymentSettings() (CryptoPaymentSettings, error) {
	v := cryptoDefaultSettings()
	var body []byte
	e := s.db.QueryRow("SELECT revision,doc FROM crypto_payment_settings WHERE id=1").Scan(&v.Revision, &body)
	if errors.Is(e, sql.ErrNoRows) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	e = s.vault.open(body, &v)
	return v, e
}
func cryptoPublicSettings(v CryptoPaymentSettings) CryptoPaymentSettings {
	if v.RateMode == "" {
		v.RateMode = "manual"
	}
	v.Chains = append([]CryptoChain(nil), v.Chains...)
	for i := range v.Chains {
		c := &v.Chains[i]
		c.HasRPC = c.RPCURL != ""
		c.HasRPCBackup = c.RPCBackupURL != ""
		c.RPCURL = ""
		c.RPCBackupURL = ""
		c.FinalityVerified = c.ChainID == 1 || c.ChainID == 56
		if c.RPCSource == "" && c.HasRPC {
			c.RPCSource = "custom"
		}
	}
	return v
}
func (s CryptoPaymentSettings) chain(id int64) (CryptoChain, bool) {
	for _, c := range s.Chains {
		if c.ChainID == id {
			return c, true
		}
	}
	return CryptoChain{}, false
}
func (s CryptoPaymentSettings) asset(id string) (CryptoAsset, bool) {
	for _, v := range s.Assets {
		if v.ID == id {
			return v, true
		}
	}
	return CryptoAsset{}, false
}

var cryptoRatePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,8})?$`)

func cryptoQuote(cents int64, v CryptoAsset) (string, error) {
	rate, ok := new(big.Rat).SetString(v.CNYPerToken)
	if cents <= 0 || cents > moneyLimit || !cryptoRatePattern.MatchString(v.CNYPerToken) || !ok || rate.Sign() <= 0 || v.Decimals < 0 || v.Decimals > 18 || v.PaymentDecimals < 0 || v.PaymentDecimals > v.Decimals {
		return "", commerceFail(400, "加密货币报价无效")
	}
	units := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(v.PaymentDecimals)), nil)
	numerator := new(big.Int).Mul(big.NewInt(cents), units)
	numerator.Mul(numerator, rate.Denom())
	denominator := new(big.Int).Mul(rate.Num(), big.NewInt(100))
	numerator.Add(numerator, new(big.Int).Sub(denominator, big.NewInt(1)))
	numerator.Div(numerator, denominator)
	numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(v.Decimals-v.PaymentDecimals)), nil))
	if numerator.Sign() <= 0 || numerator.BitLen() > 256 {
		return "", commerceFail(400, "报价数量超出范围")
	}
	return numerator.String(), nil
}
func cryptoChainReason(c CryptoChain) string {
	if c.ChainID != 1 && c.ChainID != 56 {
		return "此网络尚未完成 L1 最终性核验，暂不接受新付款"
	}
	if !c.Enabled {
		return "网络未启用"
	}
	if c.RPCURL == "" || c.RPCBackupURL == "" {
		return "请配置两个独立 RPC 来源"
	}
	return ""
}
func cryptoRPCURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 2048 {
		return false
	}
	// Reuse the public-address restrictions, retaining API-key query parameters.
	copy := *u
	copy.RawQuery = ""
	return paymentHTTPS(copy.String())
}
func (a *App) saveCryptoPaymentSettings(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in struct {
		CryptoPaymentSettings
		Password    string `json:"password"`
		OperationID string `json:"operation_id"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	if _, e := a.commerceActor(actor, true, in.Password); e != nil {
		return e
	}
	if in.Enabled {
		if e := a.requirePaymentModule(); e != nil {
			return e
		}
	}
	if !operationPattern.MatchString(in.OperationID) {
		return commerceFail(400, "缺少有效操作标识")
	}
	operation := cryptoWalletInput{OperationID: in.OperationID, Name: string(jsonBytes(in.CryptoPaymentSettings))}
	replayTx, e := a.store.cryptoWalletTx(r.Context())
	if e != nil {
		return e
	}
	_, replay, e := a.store.cryptoOperation(replayTx, actor.ID, "payment-settings", operation)
	replayTx.Rollback()
	if e != nil {
		return e
	}
	if replay != nil {
		jsonResponse(w, 200, replay)
		return nil
	}
	old, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	if in.Revision != old.Revision {
		return commerceFail(409, "支付设置已变化，请刷新")
	}
	v := in.CryptoPaymentSettings
	if v.RateMode == "" {
		v.RateMode = "manual"
	}
	if v.RateMode != "manual" && v.RateMode != "automatic" {
		return commerceFail(400, "请选择自动或手动汇率")
	}
	if v.RateMode == "automatic" && old.RateMode != "automatic" {
		return commerceFail(400, "请先使用一键启用完成自动收款检查")
	}
	v.SetupCheckedAt = old.SetupCheckedAt
	if v.WalletID != old.WalletID {
		v.SetupCheckedAt = 0
	}
	if v.RateMode == "automatic" {
		v.RateSource, v.RateCheckedAt, v.RateError = old.RateSource, old.RateCheckedAt, old.RateError
	} else {
		v.RateSource, v.RateCheckedAt, v.RateError = "管理员手动汇率", 0, ""
	}
	if v.InvoiceMinutes < 15 || v.InvoiceMinutes > 120 || len(v.Chains) != 5 || len(v.Assets) != 9 {
		return commerceFail(400, "支付设置无效")
	}
	defs := cryptoDefaultSettings()
	seen := map[string]bool{}
	for i := range v.Chains {
		c := &v.Chains[i]
		d, ok := defs.chain(c.ChainID)
		prev, _ := old.chain(c.ChainID)
		if !ok || seen[c.ID] || c.ID != d.ID {
			return commerceFail(400, "网络不在受支持清单")
		}
		seen[c.ID] = true
		c.Name, c.NativeSymbol, c.FinalityVerified = d.Name, d.NativeSymbol, c.ChainID == 1 || c.ChainID == 56
		c.RPCURL = strings.TrimSpace(c.RPCURL)
		c.RPCBackupURL = strings.TrimSpace(c.RPCBackupURL)
		if c.RPCURL == "" {
			c.RPCURL = prev.RPCURL
		}
		if c.RPCBackupURL == "" {
			c.RPCBackupURL = prev.RPCBackupURL
		}
		c.RPCSource = prev.RPCSource
		if c.RPCURL != prev.RPCURL || c.RPCBackupURL != prev.RPCBackupURL {
			c.RPCSource, v.SetupCheckedAt = "custom", 0
		}
		if c.RPCURL != "" && !cryptoRPCURL(c.RPCURL) || c.RPCBackupURL != "" && !cryptoRPCURL(c.RPCBackupURL) {
			return commerceFail(400, "RPC 必须使用公网 HTTPS 地址")
		}
		if c.RPCURL != "" && c.RPCBackupURL != "" {
			u, _ := url.Parse(c.RPCURL)
			b, _ := url.Parse(c.RPCBackupURL)
			if strings.EqualFold(u.Hostname(), b.Hostname()) {
				return commerceFail(400, "请使用两个独立 RPC 供应商")
			}
		}
		if c.Enabled && cryptoChainReason(*c) != "" {
			return commerceFail(400, cryptoChainReason(*c))
		}
	}
	seen = map[string]bool{}
	now := time.Now().Unix()
	for i := range v.Assets {
		p := &v.Assets[i]
		d, ok := defs.asset(p.ID)
		if !ok || seen[p.ID] || p.ChainID != d.ChainID || !strings.EqualFold(p.Contract, d.Contract) || p.Decimals != d.Decimals {
			return commerceFail(400, "资产合约或精度不在白名单")
		}
		seen[p.ID] = true
		p.Name, p.Symbol, p.Contract = d.Name, d.Symbol, d.Contract
		previous, _ := old.asset(p.ID)
		if v.RateMode == "automatic" {
			p.CNYPerToken, p.RateUpdatedAt, p.RateExpiresAt, p.RateSource = previous.CNYPerToken, previous.RateUpdatedAt, previous.RateExpiresAt, previous.RateSource
		} else {
			p.RateSource = "管理员手动汇率"
		}
		if p.CNYPerToken != previous.CNYPerToken || p.RateExpiresAt != previous.RateExpiresAt {
			p.RateUpdatedAt = now
		} else {
			p.RateUpdatedAt = previous.RateUpdatedAt
		}
		if p.Enabled && v.Enabled {
			if _, e = cryptoQuote(100, *p); e != nil {
				return e
			}
			if p.RateExpiresAt <= now || p.RateExpiresAt > now+30*86400 {
				return commerceFail(400, "汇率已过期或有效期超过30天，请先更新报价")
			}
		}
	}
	if v.Enabled {
		wallet, e := cryptoWalletByID(a.store.db, v.WalletID)
		if e != nil {
			return e
		}
		if e = cryptoWalletReady(wallet); e != nil {
			return e
		}
		for _, asset := range v.Assets {
			if asset.Enabled && !cryptoWalletSupports(wallet, asset.ChainID) {
				return commerceFail(400, "所选钱包未启用该收款网络")
			}
		}
	}
	v.Revision++
	sealed, e := a.store.vault.seal(v)
	if e != nil {
		return e
	}
	tx, e := a.store.cryptoWalletTx(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback()
	operationHash, replay, e := a.store.cryptoOperation(tx, actor.ID, "payment-settings", operation)
	if e != nil {
		return e
	}
	if replay != nil {
		if e = tx.Commit(); e != nil {
			return e
		}
		jsonResponse(w, 200, replay)
		return nil
	}
	var revision int64
	e = tx.QueryRow("SELECT revision FROM crypto_payment_settings WHERE id=1").Scan(&revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if revision != in.Revision {
		return commerceFail(409, "支付设置已变化，请刷新")
	}
	if _, e = tx.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,doc=excluded.doc", v.Revision, sealed); e != nil {
		return e
	}
	if e = cryptoSaveOperation(tx, actor.ID, operation, operationHash, cryptoPublicSettings(v)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.store.audit(actor.Username, "crypto-payment-settings", v.WalletID)
	jsonResponse(w, 200, cryptoPublicSettings(v))
	return nil
}
func cryptoWalletReady(v CryptoWallet) error {
	if !v.Enabled || v.RecoveryRequired {
		return commerceFail(409, "收款钱包已暂停或需要核对恢复索引")
	}
	if v.Mode == "hot" && !v.BackupConfirmed {
		return commerceFail(409, "请先备份并确认收款钱包")
	}
	if v.NextIndex >= cryptoMaxIndex {
		return commerceFail(409, "收款钱包地址索引已用尽")
	}
	return nil
}
func (a *App) cryptoOptions(w http.ResponseWriter) error {
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	enabled, e := a.paymentModuleEnabled()
	if e != nil {
		return e
	}
	s.Enabled = s.Enabled && enabled
	items := []object{}
	var wallet CryptoWallet
	if a.cfg.businessAgent() {
		s.Enabled = false
	}
	if s.Enabled {
		wallet, e = cryptoWalletByID(a.store.db, s.WalletID)
		if e != nil || cryptoWalletReady(wallet) != nil {
			s.Enabled = false
		}
	}
	for _, v := range s.Assets {
		if !v.Enabled {
			continue
		}
		c, ok := s.chain(v.ChainID)
		if !ok {
			continue
		}
		reason := cryptoChainReason(c)
		if s.Enabled && !cryptoWalletSupports(wallet, c.ChainID) {
			reason = "所选钱包未启用该收款网络"
		}
		if err := a.store.cryptoChainAvailable(a.store.db, c.ChainID); err != nil {
			reason = errCryptoFinality.Error()
		}
		if v.RateExpiresAt <= time.Now().Unix() {
			reason = "汇率已过期，正在等待有效报价"
		}
		items = append(items, object{"id": v.ID, "chain_id": v.ChainID, "chain_name": c.Name, "name": v.Name, "symbol": v.Symbol, "contract": v.Contract, "decimals": v.Decimals, "cny_per_token": v.CNYPerToken, "rate_updated_at": v.RateUpdatedAt, "rate_expires_at": v.RateExpiresAt, "payment_decimals": v.PaymentDecimals, "available": s.Enabled && reason == "", "unavailable_reason": reason})
	}
	jsonResponse(w, 200, object{"enabled": s.Enabled, "assets": items})
	return nil
}
func (a *App) cryptoPaymentAPIRoute(w http.ResponseWriter, r *http.Request, actor Record) bool {
	const prefix = "/api/commerce/crypto/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if path != "options" && path != "admin/settings" && path != "admin/setup" && path != "admin/setup/status" && path != "invoices" && !strings.HasPrefix(path, "invoices/") && path != "admin/invoices" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	actor, e := a.commerceActor(actor, false, "")
	if e == nil && strings.HasPrefix(path, "admin/") && actor.Role != "owner" {
		e = commerceFail(403, "需要管理员权限")
	}
	if e == nil && r.Method == "POST" && a.store.commerceErr != nil {
		e = commerceFail(409, "数据完整性校验失败，已暂停支付操作")
	}
	if e == nil {
		switch {
		case path == "admin/setup" && r.Method == "POST":
			e = a.cryptoSetupAPI(w, r, actor)
		case path == "admin/setup/status" && r.Method == "GET":
			e = a.cryptoSetupStatus(w)
		case path == "options" && r.Method == "GET":
			e = a.cryptoOptions(w)
		case path == "admin/settings" && r.Method == "GET":
			var v CryptoPaymentSettings
			v, e = a.store.cryptoPaymentSettings()
			if e == nil {
				jsonResponse(w, 200, cryptoPublicSettings(v))
			}
		case path == "admin/settings" && r.Method == "POST":
			e = a.saveCryptoPaymentSettings(w, r, actor)
		case path == "invoices" && r.Method == "POST":
			e = a.createCryptoInvoice(w, r, actor)
		case r.Method == "GET":
			e = a.getCryptoInvoices(w, r, actor, path)
		default:
			e = commerceFail(405, "方法不支持")
		}
	}
	if e != nil {
		commerceWriteError(w, e)
	}
	return true
}

// Retained in an encrypted invoice snapshot; RPC credentials never enter public DTOs.
type cryptoInvoiceSnapshot struct {
	Chain  CryptoChain `json:"chain"`
	Asset  CryptoAsset `json:"asset"`
	Amount int64       `json:"amount"`
}
