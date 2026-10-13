package controlplane

import (
	"context"
	"math/big"
	"strconv"
	"sync"
	"time"
)

// Portfolio uses this narrowly scoped read adapter; transaction signing and
// finality continue through their chain-specific implementations.
type cryptoBalanceClient struct {
	evm *cryptoEVM
	sol *cryptoSolana
}

func newCryptoBalanceClient(c CryptoChain, dev bool) (*cryptoBalanceClient, error) {
	if c.ChainID == cryptoSolanaChainID {
		s, e := newCryptoSolana(c, dev)
		return &cryptoBalanceClient{sol: s}, e
	}
	e, e2 := newCryptoEVM(c, dev)
	return &cryptoBalanceClient{evm: e}, e2
}
func (c *cryptoBalanceClient) Finalized(ctx context.Context) (cryptoEVMBlock, error) {
	if c.sol != nil {
		return c.sol.Finalized(ctx)
	}
	return c.evm.Finalized(ctx)
}
func (c *cryptoBalanceClient) Latest(ctx context.Context) (cryptoEVMBlock, error) {
	if c.sol != nil {
		return c.sol.Finalized(ctx)
	}
	return c.evm.Latest(ctx)
}
func (c *cryptoBalanceClient) block(ctx context.Context, tag string) (cryptoEVMBlock, error) {
	if c.sol != nil {
		n, e := strconv.ParseUint(tag[2:], 16, 64)
		if e != nil {
			return cryptoEVMBlock{}, e
		}
		return c.sol.block(ctx, n)
	}
	return c.evm.block(ctx, tag)
}
func (c *cryptoBalanceClient) NativeBalance(ctx context.Context, address string, block uint64) (*big.Int, error) {
	if c.sol != nil {
		return c.sol.NativeBalance(ctx, address, block)
	}
	return c.evm.NativeBalance(ctx, address, block)
}
func (c *cryptoBalanceClient) TokenBalance(ctx context.Context, mint, address string, block uint64) (*big.Int, error) {
	if c.sol != nil {
		return c.sol.TokenBalance(ctx, mint, address, block)
	}
	return c.evm.TokenBalance(ctx, mint, address, block)
}
func (c *cryptoBalanceClient) gasPrice(ctx context.Context) (*big.Int, error) {
	if c.sol != nil {
		return new(big.Int), nil
	}
	return c.evm.gasPrice(ctx)
}
func (c *cryptoBalanceClient) referenceFee(ctx context.Context, address string, price *big.Int) (*big.Int, error) {
	if c.sol != nil {
		return c.sol.ReferenceFee(ctx, address)
	}
	return new(big.Int).Mul(price, big.NewInt(21000)), nil
}
func cryptoWalletPathSQL(alias string, sol bool) string {
	prefix := alias
	if prefix != "" {
		prefix += "."
	}
	if sol {
		return prefix + "path LIKE 'm/44''/501''/%'"
	}
	return prefix + "path NOT LIKE 'm/44''/501''/%'"
}
func (a *App) cryptoSolanaGas(ctx context.Context, w CryptoWallet, chain CryptoChain, prior CryptoGasSnapshot, slots chan struct{}) CryptoGasSnapshot {
	out := prior
	out.WalletID = w.ID
	out.ChainID = chain.ID
	out.NativeSymbol = "SOL"
	out.NativeDecimals = 9
	out.FeeModel = "solana"
	out.LastAttemptAt = time.Now().Unix()
	out.GasError = ""
	out.FundingError = ""
	out.GasPriceAtoms = "0"
	out.Finality = "finalized"
	fail := func(e error) CryptoGasSnapshot { out.GasError = e.Error(); out.FundingError = e.Error(); return out }
	c, e := newCryptoSolana(chain, a.cfg.Dev)
	if e != nil {
		return fail(e)
	}
	p, e := a.store.cryptoSolanaPublicByID(a.store.db, w.ID)
	if e != nil {
		return fail(commerceFail(409, "Solana 钱包资金地址尚未完成初始化"))
	}
	out.FundingAddress = p.Funding.Address
	// Genesis authentication, fee calculation and finalized funding balance run
	// independently. FeeForMessage proves the selected recent hash is valid at
	// BOTH sources, avoiding an extra isBlockhashValid round on this read-only API.
	var authErr, feeErr, balanceErr error
	var fee, balance *big.Int
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		authErr = cryptoPortfolioRPCSlot(ctx, slots, func() error { return c.chainCachedForGas(ctx) })
	}()
	go func() {
		defer wg.Done()
		feeErr = cryptoPortfolioRPCSlot(ctx, slots, func() error { var err error; fee, err = c.ReferenceFee(ctx, p.Funding.Address); return err })
	}()
	go func() {
		defer wg.Done()
		balanceErr = cryptoPortfolioRPCSlot(ctx, slots, func() error { var err error; balance, err = c.NativeBalance(ctx, p.Funding.Address, 0); return err })
	}()
	wg.Wait()
	out.BlockNumber = c.observedSlot.Load()
	if authErr != nil {
		return fail(authErr)
	}
	if feeErr != nil {
		out.GasError = feeErr.Error()
	} else {
		out.ReferenceNativeTransferFeeAtoms = fee.String()
		out.GasCheckedAt = time.Now().Unix()
		out.ExpiresAt = out.GasCheckedAt + 10
	}
	if balanceErr != nil {
		out.FundingError = balanceErr.Error()
	} else {
		out.FundingBalanceAtoms = balance.String()
		out.FundingCheckedAt = time.Now().Unix()
	}
	return out
}
func (c *cryptoBalanceClient) ValidateToken(ctx context.Context, mint string, decimals int, block uint64) error {
	if c.sol != nil {
		return c.sol.ValidateToken(ctx, mint, decimals, block)
	}
	return c.evm.ValidateToken(ctx, mint, decimals, block)
}
