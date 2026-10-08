package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"sort"
	"time"
)

// Public endpoints published by their operators, never two aliases of one
// provider. Every installation verifies both before atomically enabling them.
// Sources: https://mevblocker.io/ https://bsc.publicnode.com/
// https://drpc.org/chainlist/ethereum-mainnet-rpc
// https://docs.blockrazor.io/transaction-submission/rpc/bsc/bsc-rpc-endpoint
func cryptoBuiltinRPCs() map[int64][2]string {
	return map[int64][2]string{
		1:  {"https://eth.drpc.org", "https://rpc.mevblocker.io"},
		56: {"https://bsc-rpc.publicnode.com", "https://bsc.blockrazor.xyz"},
	}
}

// Synthetic public address used only for zero-value execution simulation.
// There is no signer or private key for this address in the application.
const cryptoSetupProbeAddress = "0x6f560dd8488b071e8e7ba47a98f15669f6970324"

type cryptoSetupInput struct {
	WalletID    string   `json:"wallet_id"`
	ChainIDs    []int64  `json:"chain_ids"`
	AssetIDs    []string `json:"asset_ids"`
	Password    string   `json:"password"`
	OperationID string   `json:"operation_id"`
}

func (a *App) cryptoSetupAPI(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in cryptoSetupInput
	if !decode(w, r, &in) {
		return nil
	}
	if _, e := a.commerceActor(actor, true, in.Password); e != nil {
		return e
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(40 * time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	settings, e := a.setupCryptoPayments(ctx, actor, in, cryptoBuiltinRPCs())
	if e != nil {
		return e
	}
	jsonResponse(w, 200, settings)
	return nil
}

func (a *App) setupCryptoPayments(ctx context.Context, actor Record, in cryptoSetupInput, defaults map[int64][2]string) (CryptoPaymentSettings, error) {
	var out CryptoPaymentSettings
	if actor.Role != "owner" || a.cfg.businessAgent() {
		return out, commerceFail(403, "此站点角色不能启用独立收款")
	}
	if a.store.commerceErr != nil {
		return out, commerceFail(409, "数据完整性校验失败，已暂停支付操作")
	}
	if e := a.requirePaymentModule(); e != nil {
		return out, e
	}
	if !operationPattern.MatchString(in.OperationID) || in.WalletID == "" || len(in.ChainIDs) == 0 || len(in.ChainIDs) > 2 || len(in.AssetIDs) == 0 || len(in.AssetIDs) > 4 {
		return out, commerceFail(400, "请选择钱包、网络和收款币种")
	}
	in.ChainIDs = append([]int64(nil), in.ChainIDs...)
	sort.Slice(in.ChainIDs, func(i, j int) bool { return in.ChainIDs[i] < in.ChainIDs[j] })
	in.AssetIDs = append([]string(nil), in.AssetIDs...)
	sort.Strings(in.AssetIDs)
	op := cryptoWalletInput{OperationID: in.OperationID, Name: string(jsonBytes(object{"wallet_id": in.WalletID, "chain_ids": in.ChainIDs, "asset_ids": in.AssetIDs}))}
	replayTx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return out, e
	}
	_, replay, e := a.store.cryptoOperation(replayTx, actor.ID, "payment-setup", op)
	replayTx.Rollback()
	if e != nil {
		return out, e
	}
	if replay != nil {
		// Public operation responses omit RPC secrets. Replays return that exact
		// response, and never run preflight or overwrite a later settings edit.
		if e = jsonUnmarshalCryptoSettings(replay, &out); e != nil {
			return out, e
		}
		return out, nil
	}
	old, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return out, e
	}
	wallet, e := cryptoWalletByID(a.store.db, in.WalletID)
	if e != nil {
		return out, e
	}
	if e = cryptoWalletReady(wallet); e != nil {
		return out, e
	}
	out = cryptoDefaultSettings()
	out.Revision = old.Revision
	out.Enabled = true
	out.WalletID = wallet.ID
	if old.InvoiceMinutes >= 15 && old.InvoiceMinutes <= 120 {
		out.InvoiceMinutes = old.InvoiceMinutes
	}
	selected := map[int64]bool{}
	for _, id := range in.ChainIDs {
		if selected[id] || (id != 1 && id != 56) {
			return out, commerceFail(400, "一键启用目前只支持 Ethereum 和 BNB Smart Chain")
		}
		if !cryptoWalletSupports(wallet, id) {
			return out, commerceFail(400, "所选钱包未启用该收款网络")
		}
		selected[id] = true
	}
	assets := map[string]bool{}
	counts := map[int64]int{}
	for _, id := range in.AssetIDs {
		asset, ok := out.asset(id)
		if !ok || assets[id] || !selected[asset.ChainID] {
			return out, commerceFail(400, "收款币种必须属于所选网络且不能重复")
		}
		assets[id] = true
		counts[asset.ChainID]++
	}
	for id := range selected {
		if counts[id] == 0 {
			return out, commerceFail(400, "每个网络至少选择一种收款币种")
		}
	}
	for i := range out.Chains {
		chain := &out.Chains[i]
		if !selected[chain.ChainID] {
			continue
		}
		rpc, ok := defaults[chain.ChainID]
		if !ok {
			return out, commerceFail(400, "所选网络没有可用默认服务")
		}
		chain.RPCURL, chain.RPCBackupURL, chain.RPCSource, chain.Enabled, chain.FinalityVerified = rpc[0], rpc[1], "built_in", true, true
		if e = a.store.cryptoChainAvailable(a.store.db, chain.ChainID); e != nil {
			return out, e
		}
	}
	for i := range out.Assets {
		out.Assets[i].Enabled = assets[out.Assets[i].ID]
	}
	rates, e := a.fetchCryptoRates(ctx)
	if e != nil {
		return out, commerceFail(409, e.Error()+"；原配置保持不变")
	}
	if e = cryptoApplyMarketRates(&out, rates); e != nil {
		return out, commerceFail(409, e.Error())
	}
	// Bounded parallel checks save time without holding a panel/database lock.
	checks := make(chan error, len(selected))
	for _, chain := range out.Chains {
		if !chain.Enabled {
			continue
		}
		go func(chain CryptoChain) { checks <- a.cryptoSetupPreflight(ctx, chain, out.Assets, wallet.FirstAddress) }(chain)
	}
	var checkError error
	for range selected {
		if e := <-checks; e != nil && checkError == nil {
			checkError = e
		}
	}
	if checkError != nil {
		return out, commerceFail(409, "收款预检未通过："+checkError.Error()+"；原配置保持不变")
	}
	// Chain -> wallet is the same lock order as invoice allocation and signing.
	tx, e := a.store.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	for _, id := range in.ChainIDs {
		if e = cryptoLockChain(tx, id); e != nil {
			return out, e
		}
	}
	if _, e = tx.Exec("UPDATE crypto_wallet_state SET lock_version=lock_version+1 WHERE id=1"); e != nil {
		return out, e
	}
	hash, replay, e := a.store.cryptoOperation(tx, actor.ID, "payment-setup", op)
	if e != nil {
		return out, e
	}
	if replay != nil {
		if e = tx.Commit(); e != nil {
			return out, e
		}
		e = jsonUnmarshalCryptoSettings(replay, &out)
		return out, e
	}
	var revision int64
	if e = tx.QueryRow("SELECT revision FROM crypto_payment_settings WHERE id=1").Scan(&revision); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if revision != old.Revision {
		return out, commerceFail(409, "支付设置已变化，请重试；原配置未覆盖")
	}
	wallet, e = cryptoWalletByID(tx, in.WalletID)
	if e != nil {
		return out, e
	}
	if e = cryptoWalletReady(wallet); e != nil {
		return out, e
	}
	for _, id := range in.ChainIDs {
		if !cryptoWalletSupports(wallet, id) {
			return out, commerceFail(409, "钱包网络设置已变化，请重新选择")
		}
	}
	var policy string
	if _, e = tx.Exec("UPDATE meta SET value=value WHERE key='payment_module_enabled'"); e != nil {
		return out, e
	}
	e = tx.QueryRow("SELECT value FROM meta WHERE key='payment_module_enabled'").Scan(&policy)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if policy == "false" || (policy == "" && a.cfg.Role == "business") {
		return out, commerceFail(403, "本站支付模块已关闭")
	}
	for _, asset := range out.Assets {
		if asset.Enabled && asset.RateExpiresAt <= time.Now().Unix() {
			return out, commerceFail(409, "预检期间报价已过期，请重试")
		}
	}
	out.Revision++
	out.SetupCheckedAt = time.Now().Unix()
	sealed, e := a.store.vault.seal(out)
	if e != nil {
		return out, e
	}
	if _, e = tx.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,doc=excluded.doc", out.Revision, sealed); e != nil {
		return out, e
	}
	if e = cryptoSaveOperation(tx, actor.ID, op, hash, cryptoPublicSettings(out)); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	a.store.audit(actor.Username, "crypto-payment-setup", wallet.ID)
	return cryptoPublicSettings(out), nil
}

func (a *App) cryptoSetupPreflight(ctx context.Context, chain CryptoChain, assets []CryptoAsset, address string) error {
	c, e := newCryptoEVM(chain, a.cfg.Dev)
	if e != nil {
		return e
	}
	head, e := c.Finalized(ctx)
	if e != nil {
		return e
	}
	now := time.Now().Unix()
	if head.Number == 0 || head.Timestamp > uint64(now+120) || head.Timestamp < uint64(now-1800) {
		return errors.New(chain.Name + " 的最终区块尚未同步")
	}
	if _, e = c.NativeBalance(ctx, address, head.Number); e != nil {
		return e
	}
	if _, e = c.PendingNonce(ctx, address); e != nil {
		return e
	}
	if _, e = c.gasPrice(ctx); e != nil {
		return e
	}
	if _, e = c.estimateGas(ctx, address, "0x2222222222222222222222222222222222222222", "", big.NewInt(0)); e != nil {
		return e
	}
	for _, asset := range assets {
		if !asset.Enabled || asset.ChainID != chain.ChainID {
			continue
		}
		if e = c.ValidateToken(ctx, asset.Contract, asset.Decimals, head.Number); e != nil {
			return e
		}
		if _, e = c.TokenBalance(ctx, asset.Contract, address, head.Number); e != nil {
			return e
		}
		if _, e = c.Transfers(ctx, asset.Contract, address, head.Number, head.Number); e != nil {
			return e
		}
		if _, e = c.estimateGas(ctx, cryptoSetupProbeAddress, asset.Contract, cryptoTransferData("0x2222222222222222222222222222222222222222", big.NewInt(0)), big.NewInt(0)); e != nil {
			return e
		}
	}
	return nil
}

func jsonUnmarshalCryptoSettings(raw any, out *CryptoPaymentSettings) error {
	return json.Unmarshal(jsonBytes(raw), out)
}

func (a *App) cryptoSetupStatus(w http.ResponseWriter) error {
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	ready, message := true, "最近收款检查已通过"
	if !s.Enabled || s.SetupCheckedAt == 0 {
		ready, message = false, "尚未完成一键启用检查"
	}
	if enabled, e := a.paymentModuleEnabled(); e != nil {
		return e
	} else if !enabled {
		ready, message = false, "本站支付模块已关闭"
	}
	wallet, e := cryptoWalletByID(a.store.db, s.WalletID)
	if e != nil || cryptoWalletReady(wallet) != nil {
		ready, message = false, "请先选择可用的钱包并完成备份确认"
	}
	enabledAssets := 0
	for _, asset := range s.Assets {
		if !asset.Enabled {
			continue
		}
		enabledAssets++
		chain, ok := s.chain(asset.ChainID)
		if !ok || cryptoChainReason(chain) != "" || !cryptoWalletSupports(wallet, asset.ChainID) {
			ready, message = false, "收款网络尚未就绪"
		}
		if asset.RateExpiresAt <= time.Now().Unix() {
			ready, message = false, "收款报价已过期，请更新报价"
		}
		if e = a.store.cryptoChainAvailable(a.store.db, asset.ChainID); e != nil {
			ready, message = false, errCryptoFinality.Error()
		}
	}
	if enabledAssets == 0 {
		ready, message = false, "请至少启用一种收款币种"
	}
	if a.store.commerceErr != nil {
		ready, message = false, "数据完整性校验失败，已暂停支付操作"
	}
	jsonResponse(w, 200, struct {
		CryptoPaymentSettings
		Readiness object `json:"readiness"`
	}{cryptoPublicSettings(s), object{"ready": ready, "checked_at": s.SetupCheckedAt, "message": message}})
	return nil
}
