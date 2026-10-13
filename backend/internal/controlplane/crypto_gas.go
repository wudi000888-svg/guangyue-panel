package controlplane

import (
	"context"
	"math/big"
	"time"
)

const cryptoGasTTL = 4 * time.Second
const cryptoGasMaxEntries = 64

// A lightweight read-only market/funding snapshot. It does not read receiving
// addresses, allocate nonces, create jobs or authorize a transfer.
type CryptoGasSnapshot struct {
	WalletID                        string `json:"wallet_id"`
	ChainID                         string `json:"chain_id"`
	NativeSymbol                    string `json:"native_symbol"`
	GasPriceAtoms                   string `json:"gas_price_atoms"`
	ReferenceNativeTransferFeeAtoms string `json:"reference_native_transfer_fee_atoms"`
	GasCheckedAt                    int64  `json:"gas_checked_at"`
	ExpiresAt                       int64  `json:"expires_at"`
	GasError                        string `json:"gas_error"`
	FundingAddress                  string `json:"funding_address"`
	FundingBalanceAtoms             string `json:"funding_balance_atoms"`
	FundingCheckedAt                int64  `json:"funding_checked_at"`
	FundingError                    string `json:"funding_error"`
	BlockNumber                     uint64 `json:"block_number"`
	Finality                        string `json:"finality"`
	LastAttemptAt                   int64  `json:"last_attempt_at"`
}
type cryptoGasCacheEntry struct {
	fingerprint           string
	snapshot              CryptoGasSnapshot
	expires, timeAccessed time.Time
	running               bool
	done                  chan struct{}
}

func (a *App) cryptoWalletGas(ctx context.Context, id, chainID string) (CryptoGasSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Current authorization, live wallet and configuration always precede cache
	// lookup, including on a five-second UI poll after deletion or chain removal.
	wallet, e := cryptoWalletByID(a.store.db, id)
	if e != nil {
		return CryptoGasSnapshot{}, e
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return CryptoGasSnapshot{}, e
	}
	chain, e := cryptoSweepChain(settings, chainID)
	if e != nil {
		return CryptoGasSnapshot{}, e
	}
	if !cryptoWalletSupports(wallet, chain.ChainID) {
		return CryptoGasSnapshot{}, commerceFail(409, "此钱包未选择该网络")
	}
	fingerprint := digest(string(jsonBytes(object{"chain": chain, "xpub": wallet.XPub, "path": wallet.Path, "supported_chain_ids": wallet.SupportedChainIDs})))
	key := id + "|" + chainID
	a.cryptoGasMu.Lock()
	if a.cryptoGasSnapshots == nil {
		a.cryptoGasSnapshots = map[string]*cryptoGasCacheEntry{}
		a.cryptoGasSlots = make(chan struct{}, 4)
	}
	entry := a.cryptoGasSnapshots[key]
	if entry == nil || entry.fingerprint != fingerprint {
		entry = &cryptoGasCacheEntry{fingerprint: fingerprint}
		a.cryptoGasSnapshots[key] = entry
	}
	entry.timeAccessed = time.Now()
	if entry.running {
		done := entry.done
		a.cryptoGasMu.Unlock()
		select {
		case <-ctx.Done():
			return CryptoGasSnapshot{}, ctx.Err()
		case <-done:
		}
		a.cryptoGasMu.Lock()
		v := entry.snapshot
		a.cryptoGasMu.Unlock()
		return v, nil
	}
	if time.Now().Before(entry.expires) {
		v := entry.snapshot
		a.cryptoGasMu.Unlock()
		return v, nil
	}
	prior := entry.snapshot
	entry.running = true
	entry.done = make(chan struct{})
	slots := a.cryptoGasSlots
	a.cryptoGasMu.Unlock()
	v := a.cryptoRefreshGas(ctx, wallet, chain, prior, slots)
	a.cryptoGasMu.Lock()
	entry.snapshot = v
	entry.running = false
	entry.expires = time.Now().Add(cryptoGasTTL)
	if ctx.Err() != nil {
		entry.expires = time.Time{}
	}
	close(entry.done)
	for len(a.cryptoGasSnapshots) > cryptoGasMaxEntries {
		oldestKey := ""
		var oldest time.Time
		for k, x := range a.cryptoGasSnapshots {
			if k != key && !x.running && (oldestKey == "" || x.timeAccessed.Before(oldest)) {
				oldestKey = k
				oldest = x.timeAccessed
			}
		}
		if oldestKey == "" {
			break
		}
		delete(a.cryptoGasSnapshots, oldestKey)
	}
	a.cryptoGasMu.Unlock()
	return v, nil
}
func (a *App) cryptoRefreshGas(ctx context.Context, wallet CryptoWallet, chain CryptoChain, prior CryptoGasSnapshot, slots chan struct{}) CryptoGasSnapshot {
	out := prior
	out.WalletID = wallet.ID
	out.ChainID = chain.ID
	out.NativeSymbol = chain.NativeSymbol
	out.LastAttemptAt = time.Now().Unix()
	out.GasError = ""
	out.FundingError = ""
	fail := func(e error) CryptoGasSnapshot { out.GasError = e.Error(); out.FundingError = e.Error(); return out }
	c, e := newCryptoEVM(chain, a.cfg.Dev)
	if e != nil {
		return fail(e)
	}
	var block cryptoEVMBlock
	e = cryptoPortfolioRPCSlot(ctx, slots, func() error {
		if cryptoSweepAllowed(chain) == nil {
			block, e = c.Finalized(ctx)
			out.Finality = "finalized"
		} else {
			block, e = c.Latest(ctx)
			out.Finality = "latest"
		}
		return e
	})
	if e != nil {
		return fail(e)
	}
	out.BlockNumber = block.Number
	var price *big.Int
	e = cryptoPortfolioRPCSlot(ctx, slots, func() error { var err error; price, err = c.gasPrice(ctx); return err })
	if e != nil {
		out.GasError = e.Error()
	} else {
		out.GasPriceAtoms = price.String()
		out.ReferenceNativeTransferFeeAtoms = new(big.Int).Mul(price, big.NewInt(21000)).String()
		out.GasCheckedAt = time.Now().Unix()
		out.ExpiresAt = out.GasCheckedAt + 10
	}
	funding, e := cryptoFunding(wallet)
	if e != nil {
		out.FundingError = e.Error()
	} else if funding.Address != "" {
		out.FundingAddress = funding.Address
		var amount *big.Int
		e = cryptoPortfolioRPCSlot(ctx, slots, func() error {
			var err error
			amount, err = c.NativeBalance(ctx, funding.Address, block.Number)
			return err
		})
		if e != nil {
			out.FundingError = e.Error()
		} else {
			out.FundingBalanceAtoms = amount.String()
			out.FundingCheckedAt = time.Now().Unix()
		}
	}
	return out
}
