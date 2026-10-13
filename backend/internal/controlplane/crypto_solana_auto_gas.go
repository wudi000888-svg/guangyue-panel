package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
)

// A changed market quote does not grant a new spending authorization. Pause
// before producing a signature and require an explicit administrator retry.
func (c *cryptoSolana) approvedSweepCosts(ctx context.Context, quote cryptoSweepQuote, plan CryptoSweepPlanItem, fee *big.Int, slot uint64) error {
	approved, e := cryptoAtoms(plan.GasFeeAtoms)
	if e != nil {
		return e
	}
	if fee.Cmp(approved) > 0 {
		return fmt.Errorf("%w：Solana 实时手续费超过原批准报价", errCryptoGasReview)
	}
	if quote.Asset.Contract == "" {
		return nil
	}
	rent, e := cryptoAtoms(plan.RentAtoms)
	if e != nil {
		return e
	}
	target, e := cryptoSolanaATA(quote.Preview.Destination, quote.Asset.Contract)
	if e != nil {
		return e
	}
	account, e := c.account(ctx, target, slot)
	if e != nil {
		return e
	}
	if string(account) == "null" {
		needed, e := c.rent(ctx)
		if e != nil {
			return e
		}
		if rent.Cmp(needed) < 0 {
			return fmt.Errorf("%w：Solana 目标账户创建租金超过原批准额度", errCryptoGasReview)
		}
		return nil
	}
	var info struct {
		Owner string `json:"owner"`
		Data  struct {
			Parsed struct {
				Info struct {
					Mint  string `json:"mint"`
					Owner string `json:"owner"`
					State string `json:"state"`
				} `json:"info"`
			} `json:"parsed"`
		} `json:"data"`
	}
	if json.Unmarshal(account, &info) != nil || info.Owner != cryptoSolanaTokenProgram || info.Data.Parsed.Info.Owner != quote.Preview.Destination || info.Data.Parsed.Info.Mint != quote.Asset.Contract || info.Data.Parsed.Info.State != "initialized" {
		return commerceFail(409, "Solana 目标代币账户无法接收提取")
	}
	return nil
}

func (a *App) cryptoCheckSolanaRetryFee(ctx context.Context, job string, quote cryptoSweepQuote) error {
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	current, e := cryptoSweepChain(settings, quote.Chain.ID)
	if e != nil || current.ChainID != cryptoSolanaChainID {
		return commerceFail(409, "原 Solana 归集网络配置已变化")
	}
	quote.Chain.RPCURL, quote.Chain.RPCBackupURL = current.RPCURL, current.RPCBackupURL
	c, e := newCryptoSolana(quote.Chain, a.cfg.Dev)
	if e != nil {
		return e
	}
	head, e := c.Finalized(ctx)
	if e != nil {
		return e
	}
	hash, _, e := c.latestBlockhash(ctx)
	if e != nil {
		return e
	}
	for _, plan := range quote.Preview.Items {
		var signed int
		if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_solana_transactions t JOIN crypto_sweep_items i ON i.id=t.item_id WHERE t.job_id=? AND i.address_id=?", job, plan.AddressID).Scan(&signed); e != nil {
			return e
		}
		// Submitted transactions keep their original signature, including unknown
		// receipts; a retry must never create a replacement blockhash for them.
		if signed > 0 {
			continue
		}
		if quote.Asset.Contract != "" {
			allowed, e := cryptoAutoGasOrder(a.store.db, quote.Preview.WalletID, cryptoSolanaChainID, plan.AddressID, quote.Asset)
			if e != nil {
				return e
			}
			if !allowed {
				return commerceFail(409, "该收款地址不属于本网络的真实订单")
			}
			balance, e := c.TokenBalance(ctx, quote.Asset.Contract, plan.Address, head.Number)
			if e != nil {
				return e
			}
			if !cryptoAutoGasEligible(quote.Asset, balance) {
				return commerceFail(409, "订单收款地址稳定币余额需严格超过 0.1")
			}
		}
		message, e := cryptoSolanaSweepMessage(quote, plan, hash)
		if e != nil {
			return e
		}
		fee, e := c.Fee(ctx, message)
		if e != nil {
			return e
		}
		if e = c.approvedSweepCosts(ctx, quote, plan, fee, head.Number); e != nil {
			return e
		}
	}
	return nil
}
