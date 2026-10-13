package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Only new, unsigned operations may enter fee_review_required. Existing signed
// envelopes retain their frozen parameters and reconciliation path.
var errCryptoGasReview = commerceFail(409, "手续费上涨，需要管理员确认后继续；超出原报价时请结束未签名任务并重新预览")

func cryptoAutoGasThreshold(asset CryptoAsset) *big.Int {
	if asset.Decimals < 1 || asset.Decimals > 18 {
		return nil
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(asset.Decimals-1)), nil)
}

func (a *App) cryptoCheckEVMRetryFee(ctx context.Context, job string, quote cryptoSweepQuote) error {
	settings, err := a.store.cryptoPaymentSettings()
	if err != nil {
		return err
	}
	current, err := cryptoSweepChain(settings, quote.Chain.ID)
	if err != nil || current.ChainID != quote.Chain.ChainID {
		return commerceFail(409, "原归集网络配置已变化")
	}
	quote.Chain.RPCURL, quote.Chain.RPCBackupURL = current.RPCURL, current.RPCBackupURL
	client, err := newCryptoEVM(quote.Chain, a.cfg.Dev)
	if err != nil {
		return err
	}
	if _, err = client.Finalized(ctx); err != nil {
		return err
	}
	price, err := client.rawGasPrice(ctx)
	if err != nil {
		return err
	}
	for _, plan := range quote.Preview.Items {
		var signed int
		if err = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions WHERE job_id=? AND kind='sweep' AND from_address=?", job, plan.Address).Scan(&signed); err != nil {
			return err
		}
		if signed > 0 {
			continue
		}
		approved, err := cryptoAtoms(plan.GasPriceAtoms)
		if err != nil {
			return err
		}
		if price.Cmp(approved) > 0 {
			return fmt.Errorf("%w：实时 Gas 价格仍超过原批准报价", errCryptoGasReview)
		}
		amount, err := cryptoAtoms(plan.AmountAtoms)
		if err != nil {
			return err
		}
		to, data, value := quote.Preview.Destination, "", amount
		if quote.Asset.Contract != "" {
			to = quote.Asset.Contract
			data = cryptoTransferData(quote.Preview.Destination, amount)
			value = big.NewInt(0)
		}
		estimate, err := client.rawEstimateGas(ctx, plan.Address, to, data, value)
		if err != nil {
			return err
		}
		if estimate > plan.GasLimit {
			return fmt.Errorf("%w：实时 Gas 用量仍超过原批准上限", errCryptoGasReview)
		}
		if plan.TopupAtoms != "0" {
			var signedGas int
			if err = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions WHERE job_id=? AND kind='gas' AND to_address=?", job, plan.Address).Scan(&signedGas); err != nil {
				return err
			}
			if signedGas == 0 {
				topup, err := cryptoAtoms(plan.TopupAtoms)
				if err != nil {
					return err
				}
				fundEstimate, err := client.rawEstimateGas(ctx, quote.Preview.FundingAddress, plan.Address, "", topup)
				if err != nil {
					return err
				}
				if fundEstimate > plan.FundingGasLimit {
					return fmt.Errorf("%w：补 Gas 交易用量仍超过原批准上限", errCryptoGasReview)
				}
			}
		}
	}
	return nil
}

func cryptoAutoGasEligible(asset CryptoAsset, balance *big.Int) bool {
	threshold := cryptoAutoGasThreshold(asset)
	return threshold != nil && balance != nil && balance.Cmp(threshold) > 0
}

// A receiving address must belong to an actual order/invoice on this network.
// Its order need not be marked paid: the verified token balance is evidence of
// receipt. Wrong-token payments may be recovered under administrator approval
// without using their amount to activate the original order.
func cryptoAutoGasOrder(q cryptoWalletQuery, wallet string, chain int64, addressID string, asset CryptoAsset) (bool, error) {
	approved, ok := cryptoDefaultSettings().asset(asset.ID)
	if !ok || asset.ChainID != chain || approved.ChainID != chain || approved.Contract != asset.Contract || approved.Decimals != asset.Decimals || (approved.Symbol != "USDT" && approved.Symbol != "USDC" && approved.Symbol != "USDT0") {
		return false, nil
	}
	var addressWallet, address, invoiceWallet, invoiceAddress string
	var invoiceChain, invoiceUser, orderUser int64
	err := q.QueryRow("SELECT a.wallet_id,a.address,i.wallet_id,i.address,i.chain_id,i.user_id,o.user_id FROM crypto_wallet_addresses a JOIN crypto_invoices i ON i.address_id=a.id JOIN commerce_orders o ON o.id=i.order_id WHERE a.id=?", addressID).Scan(&addressWallet, &address, &invoiceWallet, &invoiceAddress, &invoiceChain, &invoiceUser, &orderUser)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if addressWallet != invoiceWallet || invoiceChain != chain || invoiceUser != orderUser || (chain == cryptoSolanaChainID && address != invoiceAddress) || (chain != cryptoSolanaChainID && !strings.EqualFold(address, invoiceAddress)) {
		return false, nil
	}
	return cryptoWalletSameDescriptor(q, addressWallet, wallet)
}
