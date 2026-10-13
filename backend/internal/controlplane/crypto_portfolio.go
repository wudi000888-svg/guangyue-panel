package controlplane

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"sync"
	"time"
)

const cryptoPortfolioTTL = 30 * time.Second
const cryptoPortfolioPageSize = 20
const cryptoPortfolioChunkSize = 100
const cryptoPortfolioMaxEntries = 32
const cryptoPortfolioMaxFrozenAge = 10 * time.Minute

type CryptoPortfolioAddress struct {
	CryptoBalanceItem
	Label         string `json:"label"`
	CheckedAt     int64  `json:"checked_at"`
	LastSuccessAt int64  `json:"last_success_at"`
	BlockNumber   uint64 `json:"block_number"`
	Error         string `json:"error"`
	Stale         bool   `json:"stale"`
	Funded        bool   `json:"funded"`
}

// NativeBalanceAtoms contains receiving addresses only. The independently
// derived funding/change address must never be counted twice in that total.
// Token totals are exact integer atoms, including retained last-known balances
// when a row fails. Complete=false means they are not a current full snapshot.
type CryptoWalletPortfolio struct {
	WalletID                        string                   `json:"wallet_id"`
	ChainID                         string                   `json:"chain_id"`
	NativeSymbol                    string                   `json:"native_symbol"`
	WatchOnly                       bool                     `json:"watch_only"`
	OperationsSupported             bool                     `json:"operations_supported"`
	UnavailableReason               string                   `json:"unavailable_reason"`
	FundingAddress                  string                   `json:"funding_address"`
	FundingPath                     string                   `json:"funding_path"`
	FundingBalanceAtoms             string                   `json:"funding_balance_atoms"`
	FundingCheckedAt                int64                    `json:"funding_checked_at"`
	FundingError                    string                   `json:"funding_error"`
	NativeBalanceAtoms              string                   `json:"native_balance_atoms"`
	Tokens                          []CryptoBalanceToken     `json:"tokens"`
	TotalAddresses                  int                      `json:"total_addresses"`
	CheckedAddresses                int                      `json:"checked_addresses"`
	FundedAddresses                 int                      `json:"funded_addresses"`
	EmptyAddresses                  int                      `json:"empty_addresses"`
	FailedAddresses                 int                      `json:"failed_addresses"`
	Complete                        bool                     `json:"complete"`
	Stale                           bool                     `json:"stale"`
	Refreshing                      bool                     `json:"refreshing"`
	CheckedAt                       int64                    `json:"checked_at"`
	LastAttemptAt                   int64                    `json:"last_attempt_at"`
	BlockNumber                     uint64                   `json:"block_number"`
	BlockHash                       string                   `json:"block_hash"`
	Finality                        string                   `json:"finality"`
	GasPriceAtoms                   string                   `json:"gas_price_atoms"`
	ReferenceNativeTransferFeeAtoms string                   `json:"reference_native_transfer_fee_atoms"`
	GasCheckedAt                    int64                    `json:"gas_checked_at"`
	GasError                        string                   `json:"gas_error"`
	Errors                          []string                 `json:"errors"`
	Items                           []CryptoPortfolioAddress `json:"items"`
	NextAfter                       *int64                   `json:"next_after"`
	Filter                          string                   `json:"filter"`
	// Request-private incremental scan state, never serialized to clients.
	scanAfter int64
	resumable bool
}

type cryptoPortfolioCacheEntry struct {
	fingerprint string
	allocations string
	snapshot    CryptoWalletPortfolio
	expires     time.Time
	running     bool
	done        chan struct{}
	progress    int
	accessed    time.Time
}

func cloneCryptoPortfolio(v CryptoWalletPortfolio) CryptoWalletPortfolio {
	v.Tokens = append([]CryptoBalanceToken{}, v.Tokens...)
	v.Errors = append([]string{}, v.Errors...)
	v.Items = append([]CryptoPortfolioAddress{}, v.Items...)
	for i := range v.Items {
		v.Items[i].Tokens = append([]CryptoBalanceToken{}, v.Items[i].Tokens...)
	}
	return v
}

func cryptoPortfolioPage(v CryptoWalletPortfolio, after int64, filter string) CryptoWalletPortfolio {
	v = cloneCryptoPortfolio(v)
	all := v.Items
	v.Items = []CryptoPortfolioAddress{}
	v.NextAfter = nil
	v.Filter = filter
	for _, x := range all {
		if x.Index <= after || filter == "funded" && (x.Error != "" || !x.Funded) || filter == "empty" && (x.Error != "" || x.LastSuccessAt == 0 || x.Funded) || filter == "failed" && x.Error == "" {
			continue
		}
		if len(v.Items) == cryptoPortfolioPageSize {
			next := v.Items[len(v.Items)-1].Index
			v.NextAfter = &next
			break
		}
		v.Items = append(v.Items, x)
	}
	return v
}

func (a *App) cryptoPortfolioAddressChunk(ctx context.Context, wallet string, after, high int64) ([]CryptoPortfolioAddress, error) {
	rows, e := a.store.db.QueryContext(ctx, "SELECT a.id,a.address_index,a.address,a.path,a.label,COALESCE(i.order_id,''),COALESCE(i.id,''),COALESCE(i.user_id,0) FROM crypto_wallet_addresses a LEFT JOIN crypto_invoices i ON i.address_id=a.id WHERE a.wallet_id IN ("+cryptoWalletOwnerScope+") AND a.address_index>? AND a.address_index<=? ORDER BY a.address_index LIMIT ?", wallet, after, high, cryptoPortfolioChunkSize)
	if e != nil {
		return nil, e
	}
	out := []CryptoPortfolioAddress{}
	for rows.Next() {
		var x CryptoPortfolioAddress
		if e = rows.Scan(&x.AddressID, &x.Index, &x.Address, &x.Path, &x.Label, &x.OrderID, &x.InvoiceID, &x.UserID); e != nil {
			break
		}
		x.Tokens = []CryptoBalanceToken{}
		out = append(out, x)
	}
	return out, errors.Join(e, rows.Err(), rows.Close())
}

func cryptoPortfolioRPCSlot(ctx context.Context, slots chan struct{}, work func() error) error {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-slots }()
	return work()
}

func (a *App) cryptoWalletPortfolio(ctx context.Context, id, chainID string, after int64, filter string) (CryptoWalletPortfolio, error) {
	if filter == "" {
		filter = "all"
	}
	if filter != "all" && filter != "funded" && filter != "empty" && filter != "failed" {
		return CryptoWalletPortfolio{}, commerceFail(400, "余额地址筛选无效")
	}
	wallet, e := cryptoWalletByID(a.store.db, id)
	if e != nil {
		return CryptoWalletPortfolio{}, e
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return CryptoWalletPortfolio{}, e
	}
	chain, e := cryptoSweepChain(settings, chainID)
	if e != nil {
		return CryptoWalletPortfolio{}, e
	}
	if !cryptoWalletSupports(wallet, chain.ChainID) {
		return CryptoWalletPortfolio{}, commerceFail(409, "此钱包未选择该网络")
	}
	var count int
	var high int64
	if e = a.store.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MAX(address_index),-1) FROM crypto_wallet_addresses WHERE wallet_id IN ("+cryptoWalletOwnerScope+")", id).Scan(&count, &high); e != nil {
		return CryptoWalletPortfolio{}, e
	}
	assets := []CryptoAsset{}
	for _, asset := range settings.Assets {
		if asset.ChainID == chain.ChainID {
			assets = append(assets, asset)
		}
	}
	// Re-read authorization/config before looking at the cache. A removed chain,
	// wallet policy change, new allocation or RPC/asset change invalidates it.
	configWallet := wallet
	configWallet.NextIndex = 0
	configWallet.Revision = 0
	compatibleAssets := []object{}
	for _, asset := range assets {
		compatibleAssets = append(compatibleAssets, object{"id": asset.ID, "chain_id": asset.ChainID, "symbol": asset.Symbol, "contract": asset.Contract, "decimals": asset.Decimals})
	}
	fingerprint := digest(string(jsonBytes(object{"wallet": configWallet, "chain": chain, "assets": compatibleAssets})))
	allocations := digest(string(jsonBytes(object{"count": count, "high": high})))
	key := id + "|" + chainID
	a.cryptoPortfolioMu.Lock()
	if a.cryptoPortfolios == nil {
		a.cryptoPortfolios = map[string]*cryptoPortfolioCacheEntry{}
		a.cryptoPortfolioSlots = make(chan struct{}, 4)
	}
	entry := a.cryptoPortfolios[key]
	if entry == nil || entry.fingerprint != fingerprint {
		entry = &cryptoPortfolioCacheEntry{fingerprint: fingerprint}
		a.cryptoPortfolios[key] = entry
	}
	entry.accessed = time.Now()
	if entry.allocations != allocations {
		entry.expires = time.Time{}
	}
	if entry.running {
		if entry.snapshot.WalletID != "" {
			v := cloneCryptoPortfolio(entry.snapshot)
			v.Refreshing = true
			v.Complete = false
			v.Stale = true
			v.CheckedAddresses = entry.progress
			a.cryptoPortfolioMu.Unlock()
			return cryptoPortfolioPage(v, after, filter), nil
		}
		done := entry.done
		a.cryptoPortfolioMu.Unlock()
		select {
		case <-ctx.Done():
			return CryptoWalletPortfolio{}, ctx.Err()
		case <-done:
		}
		a.cryptoPortfolioMu.Lock()
		v := cloneCryptoPortfolio(entry.snapshot)
		a.cryptoPortfolioMu.Unlock()
		return cryptoPortfolioPage(v, after, filter), nil
	}
	if time.Now().Before(entry.expires) {
		v := cloneCryptoPortfolio(entry.snapshot)
		a.cryptoPortfolioMu.Unlock()
		return cryptoPortfolioPage(v, after, filter), nil
	}
	previous := cloneCryptoPortfolio(entry.snapshot)
	entry.allocations = allocations
	entry.running = true
	entry.progress = 0
	if previous.resumable && time.Since(time.Unix(previous.CheckedAt, 0)) <= cryptoPortfolioMaxFrozenAge {
		entry.progress = previous.CheckedAddresses
	}
	entry.done = make(chan struct{})
	slots := a.cryptoPortfolioSlots
	a.cryptoPortfolioMu.Unlock()
	// Request-owned work: no detached worker survives cancellation or server
	// shutdown. Four workers, shared across wallet/chain refreshes, bound RPC load.
	v := a.cryptoRefreshPortfolio(ctx, wallet, chain, assets, count, high, previous, slots, func(n int) { a.cryptoPortfolioMu.Lock(); entry.progress = n; a.cryptoPortfolioMu.Unlock() })
	a.cryptoPortfolioMu.Lock()
	entry.snapshot = v
	entry.running = false
	entry.expires = time.Now().Add(cryptoPortfolioTTL)
	if !v.Complete {
		entry.expires = time.Now().Add(5 * time.Second)
	}
	if ctx.Err() != nil {
		entry.expires = time.Time{}
	}
	close(entry.done)
	// Bound memory across many wallets/networks with least-recently-used
	// eviction. Active coalescing entries stay until their request finishes.
	for len(a.cryptoPortfolios) > cryptoPortfolioMaxEntries {
		oldestKey := ""
		var oldest time.Time
		for k, x := range a.cryptoPortfolios {
			if k != key && !x.running && (oldestKey == "" || x.accessed.Before(oldest)) {
				oldestKey, oldest = k, x.accessed
			}
		}
		if oldestKey == "" {
			break
		}
		delete(a.cryptoPortfolios, oldestKey)
	}
	a.cryptoPortfolioMu.Unlock()
	return cryptoPortfolioPage(v, after, filter), nil
}

func (a *App) cryptoRefreshPortfolio(ctx context.Context, wallet CryptoWallet, chain CryptoChain, assets []CryptoAsset, count int, high int64, prior CryptoWalletPortfolio, slots chan struct{}, progress func(int)) CryptoWalletPortfolio {
	out := prior
	out.WalletID = wallet.ID
	out.ChainID = chain.ID
	out.NativeSymbol = chain.NativeSymbol
	out.WatchOnly = wallet.Mode != "hot"
	out.TotalAddresses = count
	out.CheckedAddresses = 0
	out.Refreshing = false
	out.Complete = false
	out.Stale = false
	out.Errors = []string{}
	out.LastAttemptAt = time.Now().Unix()
	out.FundingError = ""
	out.GasError = ""
	out.OperationsSupported = cryptoSweepAllowed(chain) == nil && !out.WatchOnly && cryptoSweepWallet(wallet) == nil
	out.UnavailableReason = ""
	if e := cryptoSweepAllowed(chain); e != nil {
		out.UnavailableReason = e.Error()
	} else if out.WatchOnly {
		out.UnavailableReason = "外部 xpub 仅可查看余额，请使用原钱包提取"
	} else if e := cryptoSweepWallet(wallet); e != nil {
		out.UnavailableReason = e.Error()
	}
	if e := a.store.cryptoChainAvailable(a.store.db, chain.ChainID); e != nil {
		out.OperationsSupported = false
		out.UnavailableReason = e.Error()
	}
	funding, fundingErr := cryptoFunding(wallet)
	out.FundingAddress, out.FundingPath = funding.Address, funding.Path
	failedBoundary := func(e error) CryptoWalletPortfolio {
		out.Stale = true
		out.Errors = append(out.Errors, e.Error())
		out.FailedAddresses = count
		out.FundedAddresses = 0
		out.EmptyAddresses = 0
		if out.Items == nil {
			out.Items = []CryptoPortfolioAddress{}
		}
		if len(out.Tokens) == 0 {
			out.Tokens = []CryptoBalanceToken{}
			for _, asset := range assets {
				out.Tokens = append(out.Tokens, CryptoBalanceToken{AssetID: asset.ID, Symbol: asset.Symbol, Decimals: asset.Decimals})
			}
		}
		for i := range out.Items {
			out.Items[i].Error = e.Error()
			out.Items[i].Stale = true
		}
		// RPC outage does not hide the allocated/order addresses or imply that
		// they are empty. Metadata is a cheap, chunked local DB read only.
		known := map[string]bool{}
		for _, x := range out.Items {
			known[x.AddressID] = true
		}
		for cursor := int64(-1); cursor < high && ctx.Err() == nil; {
			chunk, readErr := a.cryptoPortfolioAddressChunk(ctx, wallet.ID, cursor, high)
			if readErr != nil || len(chunk) == 0 {
				break
			}
			cursor = chunk[len(chunk)-1].Index
			for _, x := range chunk {
				if !known[x.AddressID] {
					x.Error = e.Error()
					x.Stale = true
					out.Items = append(out.Items, x)
				}
			}
		}
		sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Index < out.Items[j].Index })
		return out
	}
	c, e := newCryptoEVM(chain, a.cfg.Dev)
	if e != nil {
		return failedBoundary(e)
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
		return failedBoundary(e)
	}
	continuing := prior.resumable && prior.BlockHash != ""
	resuming := continuing && time.Since(time.Unix(prior.CheckedAt, 0)) <= cryptoPortfolioMaxFrozenAge
	if resuming {
		// Frozen snapshots are resumed only after both RPCs independently
		// confirm the same canonical hash and a boundary at or beyond it. This
		// is also necessary for read-only L2 latest snapshots after a reorg.
		if block.Number < prior.BlockNumber {
			return failedBoundary(commerceFail(409, "RPC 区块进度回退，请等待网络恢复"))
		}
		var frozen cryptoEVMBlock
		verifyErr := cryptoPortfolioRPCSlot(ctx, slots, func() error {
			var verifyErr error
			frozen, verifyErr = c.block(ctx, cryptoHex(prior.BlockNumber))
			return verifyErr
		})
		if verifyErr != nil {
			return failedBoundary(verifyErr)
		}
		if frozen.Number != prior.BlockNumber || frozen.Hash != prior.BlockHash {
			out.resumable = false
			return failedBoundary(commerceFail(409, "钱包余额快照区块已变化，将重新核验全部地址"))
		}
		block = frozen
	} else {
		out.scanAfter = -1
	}
	out.resumable = true
	out.BlockNumber = block.Number
	out.BlockHash = block.Hash
	checkedNow := time.Now().Unix()
	if !resuming {
		out.CheckedAt = checkedNow
	}
	if fundingErr != nil {
		out.FundingError = fundingErr.Error()
		out.Errors = append(out.Errors, fundingErr.Error())
	} else {
		if funding.Address != "" {
			var amount *big.Int
			e := cryptoPortfolioRPCSlot(ctx, slots, func() error {
				var readErr error
				amount, readErr = c.NativeBalance(ctx, funding.Address, block.Number)
				return readErr
			})
			if e != nil {
				out.FundingError = e.Error()
				out.Errors = append(out.Errors, e.Error())
			} else {
				out.FundingBalanceAtoms = amount.String()
				out.FundingCheckedAt = checkedNow
			}
		}
	}
	var price *big.Int
	e = cryptoPortfolioRPCSlot(ctx, slots, func() error { var priceErr error; price, priceErr = c.gasPrice(ctx); return priceErr })
	if e != nil {
		out.GasError = e.Error()
		out.Errors = append(out.Errors, e.Error())
	} else {
		out.GasPriceAtoms = price.String()
		out.ReferenceNativeTransferFeeAtoms = new(big.Int).Mul(price, big.NewInt(21000)).String()
		out.GasCheckedAt = checkedNow
	}
	old := map[string]CryptoPortfolioAddress{}
	verified := map[string]bool{}
	for _, x := range prior.Items {
		old[x.AddressID] = x
		if resuming && x.BlockNumber == block.Number && x.LastSuccessAt != 0 && x.Error == "" {
			verified[x.AddressID] = true
		}
	}
	out.Items = []CryptoPortfolioAddress{}
	start := int64(-1)
	// An expired frozen cycle advances the block but keeps its tail cursor.
	// Restarting index0 here would permanently starve later order addresses.
	if continuing && prior.scanAfter < high {
		start = prior.scanAfter
	}
	fullTraversal := start == -1 && high < 0
	for cursor := start; cursor < high; {
		if ctx.Err() != nil {
			out.Errors = append(out.Errors, "余额刷新已取消或超时")
			break
		}
		chunk, e := a.cryptoPortfolioAddressChunk(ctx, wallet.ID, cursor, high)
		if e != nil {
			out.Errors = append(out.Errors, "地址读取未完成")
			break
		}
		for i := range chunk {
			x := &chunk[i]
			if previous, ok := old[x.AddressID]; ok {
				x.NativeAtoms = previous.NativeAtoms
				x.Tokens = previous.Tokens
				x.LastSuccessAt = previous.LastSuccessAt
				x.BlockNumber = previous.BlockNumber
				x.Funded = previous.Funded
				x.Error = previous.Error
				x.Stale = previous.Stale
			}
			if x.Tokens == nil {
				x.Tokens = []CryptoBalanceToken{}
			}
		}
		if len(chunk) == 0 {
			break
		}
		cursor = chunk[len(chunk)-1].Index
		var wg sync.WaitGroup
		tasks := make(chan int)
		for worker := 0; worker < 4; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range tasks {
					if resuming && chunk[i].Error == "" && chunk[i].LastSuccessAt != 0 && chunk[i].BlockNumber == block.Number {
						continue
					}
					select {
					case slots <- struct{}{}:
					case <-ctx.Done():
						chunk[i].Error = "余额刷新已取消或超时"
						chunk[i].Stale = true
						continue
					}
					x := &chunk[i]
					x.CheckedAt = checkedNow
					native, e := c.NativeBalance(ctx, x.Address, block.Number)
					tokens := []CryptoBalanceToken{}
					if e == nil {
						for _, asset := range assets {
							var amount *big.Int
							amount, e = c.TokenBalance(ctx, asset.Contract, x.Address, block.Number)
							if e != nil {
								break
							}
							tokens = append(tokens, CryptoBalanceToken{asset.ID, asset.Symbol, asset.Decimals, amount.String()})
						}
					}
					<-slots
					if e != nil {
						x.Error = e.Error()
						x.Stale = true
						continue
					}
					x.NativeAtoms = native.String()
					x.Tokens = tokens
					x.LastSuccessAt = checkedNow
					x.BlockNumber = block.Number
					x.Error = ""
					x.Stale = false
					x.Funded = native.Sign() > 0
					for _, token := range tokens {
						if token.BalanceAtoms != "0" {
							x.Funded = true
						}
					}
				}
			}()
		}
		for i := range chunk {
			tasks <- i
		}
		close(tasks)
		wg.Wait()
		out.Items = append(out.Items, chunk...)
		out.scanAfter = cursor
		if start == -1 && cursor >= high {
			fullTraversal = true
		}
		for _, x := range chunk {
			if x.Error == "" && x.LastSuccessAt != 0 {
				verified[x.AddressID] = true
			} else {
				delete(verified, x.AddressID)
			}
		}
		progress(len(verified))
	}
	// A timeout or failed DB chunk must retain every previously known row,
	// including balances not reached in this attempt; never silently drop assets.
	present := map[string]bool{}
	for _, x := range out.Items {
		present[x.AddressID] = true
	}
	for _, x := range prior.Items {
		if !present[x.AddressID] && x.Index <= high {
			if !resuming || x.BlockNumber != block.Number || x.Error != "" {
				x.Error = "本次余额刷新未完成"
				x.Stale = true
			}
			out.Items = append(out.Items, x)
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Index < out.Items[j].Index })
	cryptoPortfolioAggregate(&out, assets)
	out.Complete = out.CheckedAddresses == count && out.FundingError == "" && len(out.Items) == count && len(out.Errors) == 0
	out.Stale = !out.Complete
	// Keep a frozen boundary only while a traversal was interrupted. A full
	// pass with one permanently failing address/Gas provider must not freeze
	// every healthy address at that historical block indefinitely.
	out.resumable = !out.Complete && (!fullTraversal || ctx.Err() != nil)
	return out
}

func cryptoPortfolioAggregate(out *CryptoWalletPortfolio, assets []CryptoAsset) {
	native := new(big.Int)
	totals := map[string]*big.Int{}
	for _, asset := range assets {
		totals[asset.ID] = new(big.Int)
	}
	out.CheckedAddresses = 0
	out.FundedAddresses = 0
	out.EmptyAddresses = 0
	out.FailedAddresses = 0
	known := 0
	for _, x := range out.Items {
		if x.Error != "" {
			out.FailedAddresses++
		} else if x.LastSuccessAt != 0 {
			out.CheckedAddresses++
			if x.Funded {
				out.FundedAddresses++
			} else {
				out.EmptyAddresses++
			}
		}
		if x.LastSuccessAt == 0 {
			continue
		}
		known++
		if n, ok := new(big.Int).SetString(x.NativeAtoms, 10); ok {
			native.Add(native, n)
		}
		for _, token := range x.Tokens {
			if sum := totals[token.AssetID]; sum != nil {
				if n, ok := new(big.Int).SetString(token.BalanceAtoms, 10); ok {
					sum.Add(sum, n)
				}
			}
		}
	}
	if len(out.Items) < out.TotalAddresses {
		out.FailedAddresses += out.TotalAddresses - len(out.Items)
	}
	out.NativeBalanceAtoms = native.String()
	out.Tokens = []CryptoBalanceToken{}
	for _, asset := range assets {
		amount := totals[asset.ID].String()
		if known == 0 && out.TotalAddresses > 0 {
			amount = ""
		}
		out.Tokens = append(out.Tokens, CryptoBalanceToken{asset.ID, asset.Symbol, asset.Decimals, amount})
	}
	if known == 0 && out.TotalAddresses > 0 {
		out.NativeBalanceAtoms = ""
	}
	if out.FailedAddresses > 0 {
		out.Errors = append(out.Errors, "部分地址余额刷新失败，合计包含上次成功值或尚未核验的地址")
	}
}
