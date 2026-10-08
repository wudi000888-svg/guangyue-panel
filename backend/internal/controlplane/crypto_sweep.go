package controlplane

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type CryptoBalanceToken struct {
	AssetID      string `json:"asset_id"`
	Symbol       string `json:"symbol"`
	Decimals     int    `json:"decimals"`
	BalanceAtoms string `json:"balance_atoms"`
}
type CryptoBalanceItem struct {
	AddressID   string               `json:"address_id"`
	OrderID     string               `json:"order_id"`
	InvoiceID   string               `json:"invoice_id"`
	UserID      int64                `json:"user_id"`
	Index       int64                `json:"index"`
	Address     string               `json:"address"`
	Path        string               `json:"path"`
	NativeAtoms string               `json:"native_atoms"`
	Tokens      []CryptoBalanceToken `json:"tokens"`
}
type CryptoWalletBalances struct {
	WalletID            string              `json:"wallet_id"`
	ChainID             string              `json:"chain_id"`
	NativeSymbol        string              `json:"native_symbol"`
	WatchOnly           bool                `json:"watch_only"`
	OperationsSupported bool                `json:"operations_supported"`
	UnavailableReason   string              `json:"unavailable_reason"`
	FundingAddress      string              `json:"funding_address"`
	FundingPath         string              `json:"funding_path"`
	FundingBalanceAtoms string              `json:"funding_balance_atoms"`
	CheckedAt           int64               `json:"checked_at"`
	BlockNumber         uint64              `json:"block_number"`
	Items               []CryptoBalanceItem `json:"items"`
	NextAfter           *int64              `json:"next_after"`
}
type CryptoSweepPlanItem struct {
	AddressID       string `json:"address_id"`
	Address         string `json:"address"`
	Path            string `json:"path"`
	AmountAtoms     string `json:"amount_atoms"`
	GasLimit        uint64 `json:"gas_limit,string"`
	GasPriceAtoms   string `json:"gas_price_atoms"`
	GasFeeAtoms     string `json:"gas_fee_atoms"`
	TopupAtoms      string `json:"topup_atoms"`
	FundingGasAtoms string `json:"funding_gas_atoms"`
	FundingGasLimit uint64 `json:"funding_gas_limit,string"`
}
type CryptoSweepPreview struct {
	WalletID              string                `json:"wallet_id"`
	ChainID               string                `json:"chain_id"`
	AssetID               string                `json:"asset_id"`
	Destination           string                `json:"destination"`
	MinAtoms              string                `json:"min_atoms"`
	MaxGasAtoms           string                `json:"max_gas_atoms"`
	CheckedAt             int64                 `json:"checked_at"`
	ExpiresAt             int64                 `json:"expires_at"`
	BlockNumber           uint64                `json:"block_number"`
	NativeSymbol          string                `json:"native_symbol"`
	FundingAddress        string                `json:"funding_address"`
	FundingBalanceAtoms   string                `json:"funding_balance_atoms"`
	TotalAtoms            string                `json:"total_atoms"`
	TotalTopupAtoms       string                `json:"total_topup_atoms"`
	TotalGasAtoms         string                `json:"total_gas_atoms"`
	Items                 []CryptoSweepPlanItem `json:"items"`
	Quote                 string                `json:"quote,omitempty"`
	CanSubmit             bool                  `json:"can_submit"`
	UnavailableReason     string                `json:"unavailable_reason"`
	RequiredFundingAtoms  string                `json:"required_funding_atoms"`
	FundingShortfallAtoms string                `json:"funding_shortfall_atoms"`
}
type cryptoSweepQuote struct {
	ActorID int64              `json:"actor_id"`
	Preview CryptoSweepPreview `json:"preview"`
	Chain   CryptoChain        `json:"chain"`
	Asset   CryptoAsset        `json:"asset"`
}
type cryptoSweepInput struct {
	ChainID     string   `json:"chain_id"`
	AssetID     string   `json:"asset_id"`
	Destination string   `json:"destination"`
	MinAtoms    string   `json:"min_atoms"`
	MaxGasAtoms string   `json:"max_gas_atoms"`
	AddressIDs  []string `json:"address_ids"`
	Quote       string   `json:"quote"`
	Password    string   `json:"password"`
	OperationID string   `json:"operation_id"`
}
type CryptoSweepItem struct {
	ID          string `json:"id"`
	Address     string `json:"address"`
	AmountAtoms string `json:"amount_atoms"`
	State       string `json:"state"`
	Error       string `json:"error"`
	GasTxHash   string `json:"gas_tx_hash"`
	SweepTxHash string `json:"sweep_tx_hash"`
}
type CryptoSweepJob struct {
	ID          string            `json:"id"`
	WalletID    string            `json:"wallet_id"`
	ChainID     int64             `json:"chain_id"`
	AssetID     string            `json:"asset_id"`
	Destination string            `json:"destination"`
	State       string            `json:"state"`
	Error       string            `json:"error"`
	Created     int64             `json:"created"`
	Updated     int64             `json:"updated"`
	Items       []CryptoSweepItem `json:"items"`
	CanCancel   bool              `json:"can_cancel"`
}

func cryptoSweepAllowed(c CryptoChain) error {
	if c.ChainID != 1 && c.ChainID != 56 {
		return commerceFail(409, "L1 最终性证据尚未验证，该网络暂不可自动提取")
	}
	if !c.FinalityVerified {
		return commerceFail(409, "尚未完成链最终性验证")
	}
	return nil
}
func cryptoSweepChain(s CryptoPaymentSettings, id string) (CryptoChain, error) {
	for _, c := range s.Chains {
		if c.ID == id {
			return c, nil
		}
	}
	return CryptoChain{}, commerceFail(400, "请选择已配置的网络")
}
func cryptoSweepWallet(v CryptoWallet) error {
	if v.Mode != "hot" {
		return commerceFail(409, "外部 xpub 钱包只能查看余额，请使用原钱包手动转出")
	}
	if v.RecoveryRequired {
		return commerceFail(409, "请先核对钱包恢复高水位")
	}
	if !v.BackupConfirmed {
		return commerceFail(409, "请先完成钱包备份确认")
	}
	return nil
}
func cryptoParallel(n int, work func(int) error) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if e := work(i); e != nil {
				errs <- e
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		return e
	}
	return nil
}

func (a *App) cryptoWalletBalance(ctx context.Context, id, chainID string, after int64) (CryptoWalletBalances, error) {
	out := CryptoWalletBalances{WalletID: id, ChainID: chainID, FundingBalanceAtoms: "0", Items: []CryptoBalanceItem{}}
	v, e := cryptoWalletByID(a.store.db, id)
	if e != nil {
		return out, e
	}
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return out, e
	}
	chain, e := cryptoSweepChain(s, chainID)
	if e != nil {
		return out, e
	}
	c, e := newCryptoEVM(chain, a.cfg.Dev)
	if e != nil {
		return out, e
	}
	block, e := c.Latest(ctx)
	if e != nil {
		return out, e
	}
	out.BlockNumber = block.Number
	out.CheckedAt = time.Now().Unix()
	out.NativeSymbol = chain.NativeSymbol
	out.WatchOnly = v.Mode != "hot"
	out.OperationsSupported = cryptoSweepAllowed(chain) == nil && !out.WatchOnly
	if x := cryptoSweepAllowed(chain); x != nil {
		out.UnavailableReason = x.Error()
	} else if out.WatchOnly {
		out.UnavailableReason = "外部 xpub 仅可查看余额，请使用原钱包提取"
	}
	if e := a.store.cryptoChainAvailable(a.store.db, chain.ChainID); e != nil {
		out.OperationsSupported = false
		out.UnavailableReason = e.Error()
	}
	f, e := a.store.cryptoFundingAddress(ctx, v)
	if e != nil {
		return out, e
	}
	out.FundingAddress, out.FundingPath = f.Address, f.Path
	if f.Address != "" {
		balance, e := c.NativeBalance(ctx, f.Address, block.Number)
		if e != nil {
			return out, e
		}
		out.FundingBalanceAtoms = balance.String()
	}
	rows, e := a.store.db.Query("SELECT a.id,a.address_index,a.address,a.path,COALESCE(i.order_id,''),COALESCE(i.id,''),COALESCE(i.user_id,0) FROM crypto_wallet_addresses a LEFT JOIN crypto_invoices i ON i.address_id=a.id WHERE a.wallet_id=? AND a.address_index>? ORDER BY a.address_index LIMIT 11", id, after)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var item CryptoBalanceItem
		if e = rows.Scan(&item.AddressID, &item.Index, &item.Address, &item.Path, &item.OrderID, &item.InvoiceID, &item.UserID); e != nil {
			rows.Close()
			return out, e
		}
		item.Tokens = []CryptoBalanceToken{}
		out.Items = append(out.Items, item)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return out, e
	}
	if len(out.Items) > 10 {
		out.Items = out.Items[:10]
		next := out.Items[9].Index
		out.NextAfter = &next
	}
	e = cryptoParallel(len(out.Items), func(i int) error {
		item := &out.Items[i]
		native, e := c.NativeBalance(ctx, item.Address, block.Number)
		if e != nil {
			return e
		}
		item.NativeAtoms = native.String()
		for _, asset := range s.Assets {
			if asset.ChainID != chain.ChainID {
				continue
			}
			balance, e := c.TokenBalance(ctx, asset.Contract, item.Address, block.Number)
			if e != nil {
				return e
			}
			item.Tokens = append(item.Tokens, CryptoBalanceToken{asset.ID, asset.Symbol, asset.Decimals, balance.String()})
		}
		return nil
	})
	return out, e
}
func (a *App) cryptoPreviewSweep(ctx context.Context, actor Record, id string, in cryptoSweepInput) (CryptoSweepPreview, error) {
	var out CryptoSweepPreview
	v, e := cryptoWalletByID(a.store.db, id)
	if e != nil {
		return out, e
	}
	if e = cryptoSweepWallet(v); e != nil {
		return out, e
	}
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return out, e
	}
	chain, e := cryptoSweepChain(s, in.ChainID)
	if e != nil {
		return out, e
	}
	if e = cryptoSweepAllowed(chain); e != nil {
		return out, e
	}
	asset, ok := s.asset(in.AssetID)
	if in.AssetID == "native" {
		asset = CryptoAsset{ID: "native", ChainID: chain.ChainID, Symbol: chain.NativeSymbol, Decimals: 18}
		ok = true
	}
	if !ok || asset.ChainID != chain.ChainID {
		return out, commerceFail(400, "所选资产与网络不一致")
	}
	minimum, e := cryptoAtoms(in.MinAtoms)
	if e != nil {
		return out, e
	}
	budget, e := cryptoAtoms(in.MaxGasAtoms)
	if e != nil || budget.Sign() <= 0 {
		return out, commerceFail(400, "请设置本次手续费预算上限")
	}
	if !common.IsHexAddress(in.Destination) || common.HexToAddress(in.Destination) == (common.Address{}) {
		return out, commerceFail(400, "提取目标地址无效")
	}
	dest := common.HexToAddress(in.Destination).Hex()
	for _, token := range s.Assets {
		if strings.EqualFold(dest, token.Contract) {
			return out, commerceFail(400, "提取目标不能是代币合约地址")
		}
	}
	if e = a.store.cryptoChainAvailable(a.store.db, chain.ChainID); e != nil {
		return out, e
	}
	c, e := newCryptoEVM(chain, a.cfg.Dev)
	if e != nil {
		return out, e
	}
	block, e := c.Finalized(ctx)
	if e != nil {
		return out, e
	}
	if asset.Contract != "" {
		if e = c.ValidateToken(ctx, asset.Contract, asset.Decimals, block.Number); e != nil {
			return out, e
		}
	}
	f, e := a.store.cryptoFundingAddress(ctx, v)
	if e != nil {
		return out, e
	}
	if strings.EqualFold(dest, f.Address) {
		return out, commerceFail(400, "提取目标不能是本钱包的 Gas 资金地址")
	}
	selected := map[string]bool{}
	for _, x := range in.AddressIDs {
		selected[x] = true
	}
	if len(selected) > 1000 {
		return out, commerceFail(400, "每次最多选择1000个地址")
	}
	var addresses []CryptoWalletAddress
	if len(selected) == 0 {
		addresses, e = cryptoWalletAddresses(a.store.db, id, 1001)
	} else {
		args := []any{id}
		marks := []string{}
		for x := range selected {
			args = append(args, x)
			marks = append(marks, "?")
		}
		rows, err := a.store.db.Query("SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE wallet_id=? AND id IN ("+strings.Join(marks, ",")+") ORDER BY address_index", args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var x CryptoWalletAddress
			if err = rows.Scan(&x.ID, &x.WalletID, &x.Index, &x.Path, &x.Address, &x.Label, &x.Created); err != nil {
				rows.Close()
				return out, err
			}
			addresses = append(addresses, x)
		}
		e = errors.Join(rows.Err(), rows.Close())
		if e == nil && len(addresses) != len(selected) {
			return out, commerceFail(400, "选择包含其他钱包或不存在的地址")
		}
	}
	if e != nil {
		return out, e
	}
	if len(addresses) > 1000 {
		return out, commerceFail(400, "地址较多，请在余额列表选择本次提取地址，每次最多1000个")
	}
	for _, x := range addresses {
		if strings.EqualFold(x.Address, dest) {
			return out, commerceFail(400, "提取目标不能是本次来源地址")
		}
	}
	price, e := c.gasPrice(ctx)
	if e != nil {
		return out, e
	}
	fundBalance, e := c.NativeBalance(ctx, f.Address, block.Number)
	if e != nil {
		return out, e
	}
	plans := make([]CryptoSweepPlanItem, len(addresses))
	e = cryptoParallel(len(addresses), func(i int) error {
		x := addresses[i]
		native, e := c.NativeBalance(ctx, x.Address, block.Number)
		if e != nil {
			return e
		}
		amount := new(big.Int).Set(native)
		to, data := dest, ""
		if asset.Contract != "" {
			amount, e = c.TokenBalance(ctx, asset.Contract, x.Address, block.Number)
			if e != nil {
				return e
			}
			to = asset.Contract
			data = cryptoTransferData(dest, amount)
		}
		if amount.Sign() <= 0 || amount.Cmp(minimum) < 0 {
			return nil
		}
		estimateValue := big.NewInt(0)
		if asset.Contract == "" {
			estimateValue = big.NewInt(1)
		}
		gas, e := c.estimateGas(ctx, x.Address, to, data, estimateValue)
		if e != nil {
			return e
		}
		fee := new(big.Int).Mul(new(big.Int).SetUint64(gas), price)
		topup := new(big.Int)
		fundFee := new(big.Int)
		fundGas := uint64(0)
		if asset.Contract == "" {
			amount.Sub(amount, fee)
			if amount.Sign() <= 0 || amount.Cmp(minimum) < 0 {
				return nil
			}
		} else if native.Cmp(fee) < 0 {
			topup.Sub(fee, native)
			fundGas, e = c.estimateGas(ctx, f.Address, x.Address, "", topup)
			if e != nil {
				return e
			}
			fundFee.Mul(new(big.Int).SetUint64(fundGas), price)
		}
		plans[i] = CryptoSweepPlanItem{AddressID: x.ID, Address: x.Address, Path: x.Path, AmountAtoms: amount.String(), GasLimit: gas, GasPriceAtoms: price.String(), GasFeeAtoms: fee.String(), TopupAtoms: topup.String(), FundingGasAtoms: fundFee.String(), FundingGasLimit: fundGas}
		return nil
	})
	if e != nil {
		return out, e
	}
	now := time.Now().Unix()
	out = CryptoSweepPreview{WalletID: id, ChainID: chain.ID, AssetID: asset.ID, Destination: dest, MinAtoms: minimum.String(), MaxGasAtoms: budget.String(), CheckedAt: now, ExpiresAt: now + 120, BlockNumber: block.Number, NativeSymbol: chain.NativeSymbol, FundingAddress: f.Address, FundingBalanceAtoms: fundBalance.String(), Items: []CryptoSweepPlanItem{}}
	total, topups, fees, fundFees := new(big.Int), new(big.Int), new(big.Int), new(big.Int)
	for _, x := range plans {
		if x.AddressID == "" {
			continue
		}
		out.Items = append(out.Items, x)
		amount, _ := cryptoAtoms(x.AmountAtoms)
		topup, _ := cryptoAtoms(x.TopupAtoms)
		fee, _ := cryptoAtoms(x.GasFeeAtoms)
		fundFee, _ := cryptoAtoms(x.FundingGasAtoms)
		total.Add(total, amount)
		topups.Add(topups, topup)
		fees.Add(fees, fee)
		fees.Add(fees, fundFee)
		fundFees.Add(fundFees, fundFee)
	}
	out.TotalAtoms, out.TotalTopupAtoms, out.TotalGasAtoms = total.String(), topups.String(), fees.String()
	requiredFunding := new(big.Int).Add(new(big.Int).Set(topups), fundFees)
	out.RequiredFundingAtoms = requiredFunding.String()
	shortfall := new(big.Int).Sub(requiredFunding, fundBalance)
	if shortfall.Sign() < 0 {
		shortfall.SetInt64(0)
	}
	out.FundingShortfallAtoms = shortfall.String()
	out.CanSubmit = true
	if len(out.Items) == 0 {
		out.CanSubmit = false
		out.UnavailableReason = "没有达到提取阈值且足以支付手续费的地址"
	}
	if fees.Cmp(budget) > 0 {
		out.CanSubmit = false
		out.UnavailableReason = "预计手续费超过本次预算，请调整阈值或预算后重新预览"
	}
	if requiredFunding.Cmp(fundBalance) > 0 {
		out.CanSubmit = false
		out.UnavailableReason = "Gas 资金地址余额不足，请充值对应网络的原生币并等待最终确认"
	}
	if !out.CanSubmit {
		return out, nil
	}
	quote := cryptoSweepQuote{ActorID: actor.ID, Preview: out, Chain: chain, Asset: asset}
	sealed, e := a.store.vault.seal(quote)
	if e != nil {
		return out, e
	}
	out.Quote = base64.StdEncoding.EncodeToString(sealed)
	return out, nil
}
func (a *App) cryptoCreateSweep(ctx context.Context, actor Record, wallet string, in cryptoSweepInput) (CryptoSweepJob, error) {
	var job CryptoSweepJob
	if !operationPattern.MatchString(in.OperationID) {
		return job, commerceFail(400, "缺少有效操作标识")
	}
	fingerprint := digest(wallet + "|" + in.Quote)
	sealed, e := base64.StdEncoding.DecodeString(in.Quote)
	var quote cryptoSweepQuote
	if e != nil || in.Quote != base64.StdEncoding.EncodeToString(sealed) || a.store.vault.open(sealed, &quote) != nil || quote.ActorID != actor.ID || quote.Preview.WalletID != wallet || len(quote.Preview.Items) == 0 || !quote.Preview.CanSubmit {
		return job, commerceFail(400, "提取预览无效，请重新预览")
	}
	tx, e := a.store.db.BeginTx(ctx, nil)
	if e != nil {
		return job, e
	}
	defer tx.Rollback()
	if e = cryptoLockChain(tx, quote.Chain.ChainID); e != nil {
		return job, e
	}
	if _, e = tx.Exec("UPDATE crypto_wallet_state SET lock_version=lock_version+1 WHERE id=1"); e != nil {
		return job, e
	}
	var id, fp string
	var owner int64
	e = tx.QueryRow("SELECT id,actor_id,fingerprint FROM crypto_sweep_jobs WHERE operation_id=?", in.OperationID).Scan(&id, &owner, &fp)
	if e == nil {
		if owner != actor.ID || fp != fingerprint {
			return job, commerceFail(409, "操作标识已用于其他提取请求")
		}
		if e = tx.Commit(); e != nil {
			return job, e
		}
		return a.store.cryptoSweepJob(id)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return job, e
	}
	// A quote authorizes exactly one batch, even if a client changes operation_id.
	e = tx.QueryRow("SELECT id FROM crypto_sweep_jobs WHERE actor_id=? AND fingerprint=?", actor.ID, fingerprint).Scan(&id)
	if e == nil {
		if e = tx.Commit(); e != nil {
			return job, e
		}
		return a.store.cryptoSweepJob(id)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return job, e
	}
	if quote.Preview.ExpiresAt < time.Now().Unix() {
		return job, commerceFail(409, "提取预览已过期，请重新核对手续费")
	}
	if e = cryptoSweepAllowed(quote.Chain); e != nil {
		return job, e
	}
	v, e := cryptoWalletByID(tx, wallet)
	if e != nil {
		return job, e
	}
	if e = cryptoSweepWallet(v); e != nil {
		return job, e
	}
	var active string
	e = tx.QueryRow("SELECT id FROM crypto_sweep_jobs WHERE wallet_id=? AND chain_id=? AND state NOT IN ('complete','cancelled')", wallet, quote.Chain.ChainID).Scan(&active)
	if e == nil {
		return job, commerceFail(409, "该钱包在此网络已有提取任务，请查看或继续原任务")
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return job, e
	}
	now := time.Now().Unix()
	if quote.Preview.ExpiresAt < now {
		return job, commerceFail(409, "提取预览已过期，请重新核对手续费")
	}
	job = CryptoSweepJob{ID: serial("GYS"), WalletID: wallet, ChainID: quote.Chain.ChainID, AssetID: quote.Asset.ID, Destination: quote.Preview.Destination, State: "queued", Created: now, Updated: now, Items: []CryptoSweepItem{}}
	_, e = tx.Exec("INSERT INTO crypto_sweep_jobs(id,actor_id,operation_id,fingerprint,wallet_id,chain_id,asset_id,destination,min_atoms,max_gas_atoms,state,doc,config,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", job.ID, actor.ID, in.OperationID, fingerprint, wallet, job.ChainID, job.AssetID, job.Destination, quote.Preview.MinAtoms, quote.Preview.MaxGasAtoms, job.State, jsonBytes(quote.Preview), sealed, now, now)
	if e != nil {
		return job, e
	}
	for _, x := range quote.Preview.Items {
		item := CryptoSweepItem{ID: serial("GYI"), Address: x.Address, AmountAtoms: x.AmountAtoms, State: "queued"}
		if _, e = tx.Exec("INSERT INTO crypto_sweep_items(id,job_id,address_id,address,path,amount_atoms,gas_needed_atoms,state) VALUES(?,?,?,?,?,?,?,?)", item.ID, job.ID, x.AddressID, x.Address, x.Path, x.AmountAtoms, x.TopupAtoms, item.State); e != nil {
			return job, e
		}
		job.Items = append(job.Items, item)
	}
	if e = tx.Commit(); e != nil {
		return job, e
	}
	a.store.audit(actor.Username, "crypto_sweep_create", job.ID)
	return job, nil
}
func (s *Store) cryptoSweepJob(id string) (CryptoSweepJob, error) {
	var v CryptoSweepJob
	e := s.db.QueryRow("SELECT id,wallet_id,chain_id,asset_id,destination,state,error,created,updated FROM crypto_sweep_jobs WHERE id=?", id).Scan(&v.ID, &v.WalletID, &v.ChainID, &v.AssetID, &v.Destination, &v.State, &v.Error, &v.Created, &v.Updated)
	if errors.Is(e, sql.ErrNoRows) {
		return v, commerceFail(404, "提取任务不存在")
	}
	if e != nil {
		return v, e
	}
	var pending int
	var lease int64
	if e = s.db.QueryRow("SELECT lease_until,(SELECT COUNT(*) FROM crypto_chain_transactions WHERE job_id=? AND state NOT IN ('confirmed','reverted')) FROM crypto_sweep_jobs WHERE id=?", id, id).Scan(&lease, &pending); e != nil {
		return v, e
	}
	v.CanCancel = v.State != "complete" && v.State != "cancelled" && lease <= time.Now().Unix() && pending == 0
	v.Items = []CryptoSweepItem{}
	rows, e := s.db.Query("SELECT i.id,i.address,i.amount_atoms,i.state,i.error,COALESCE((SELECT tx_hash FROM crypto_chain_transactions t WHERE t.item_id=i.id AND t.kind='gas'),''),COALESCE((SELECT tx_hash FROM crypto_chain_transactions t WHERE t.item_id=i.id AND t.kind='sweep'),'') FROM crypto_sweep_items i WHERE i.job_id=? ORDER BY i.id", id)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var x CryptoSweepItem
		if e = rows.Scan(&x.ID, &x.Address, &x.AmountAtoms, &x.State, &x.Error, &x.GasTxHash, &x.SweepTxHash); e != nil {
			return v, e
		}
		v.Items = append(v.Items, x)
	}
	return v, rows.Err()
}
func (a *App) cryptoListSweeps(w http.ResponseWriter, wallet string) error {
	query := "SELECT id FROM crypto_sweep_jobs"
	args := []any{}
	if wallet != "" {
		query += " WHERE wallet_id=?"
		args = append(args, wallet)
	}
	query += " ORDER BY created DESC,id DESC LIMIT 50"
	rows, e := a.store.db.Query(query, args...)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	jobs := []CryptoSweepJob{}
	for _, id := range ids {
		v, e := a.store.cryptoSweepJob(id)
		if e != nil {
			return e
		}
		jobs = append(jobs, v)
	}
	jsonResponse(w, 200, object{"items": jobs})
	return nil
}
func (a *App) cryptoSweepAPIRoute(w http.ResponseWriter, r *http.Request, actor Record) bool {
	if !strings.HasPrefix(r.URL.Path, cryptoWalletPrefix) {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, cryptoWalletPrefix)
	p := strings.Split(path, "/")
	matched := path == "sweeps" || (len(p) == 3 && p[0] == "sweeps" && (p[2] == "retry" || p[2] == "cancel")) || (len(p) >= 3 && p[0] == "wallets" && (p[2] == "balances" || p[2] == "sweeps"))
	if !matched {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if a.cfg.businessAgent() {
		commerceWriteError(w, commerceFail(403, "接入业务站不支持管理本地收款钱包，请前往主站操作"))
		return true
	}
	current, e := a.commerceActor(actor, false, "")
	if e == nil && current.Role != "owner" {
		e = commerceFail(403, "需要管理员权限")
	}
	if e != nil {
		commerceWriteError(w, e)
		return true
	}
	if len(p) == 3 && p[0] == "wallets" && p[2] == "balances" && r.Method == "GET" || len(p) == 4 && p[0] == "wallets" && p[2] == "sweeps" && p[3] == "preview" && r.Method == "POST" {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if r.Method == "GET" {
		switch {
		case path == "sweeps":
			e = a.cryptoListSweeps(w, r.URL.Query().Get("wallet_id"))
		case len(p) == 3 && p[2] == "balances":
			after := int64(-1)
			if raw := r.URL.Query().Get("after"); raw != "" {
				after, e = strconv.ParseInt(raw, 10, 64)
				if e != nil || after < 0 {
					e = commerceFail(400, "地址游标无效")
					break
				}
			}
			var v CryptoWalletBalances
			v, e = a.cryptoWalletBalance(ctx, p[1], r.URL.Query().Get("chain_id"), after)
			if e == nil {
				jsonResponse(w, 200, v)
			}
		default:
			e = commerceFail(404, "功能不存在")
		}
	} else if r.Method == "POST" {
		var in cryptoSweepInput
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
			e = commerceFail(400, "提取请求格式无效")
		} else if len(p) == 4 && p[0] == "wallets" && p[2] == "sweeps" && p[3] == "preview" {
			var v CryptoSweepPreview
			v, e = a.cryptoPreviewSweep(ctx, current, p[1], in)
			if e == nil {
				jsonResponse(w, 200, v)
			}
		} else if _, e = a.commerceActor(current, true, in.Password); e == nil {
			if a.store.commerceErr != nil {
				e = commerceFail(409, "资金完整性校验失败，已暂停提取")
			} else if len(p) == 3 && p[0] == "wallets" && p[2] == "sweeps" {
				var v CryptoSweepJob
				v, e = a.cryptoCreateSweep(ctx, current, p[1], in)
				if e == nil {
					jsonResponse(w, 201, v)
				}
			} else if len(p) == 3 && p[0] == "sweeps" && (p[2] == "retry" || p[2] == "cancel") {
				if p[2] == "cancel" {
					e = a.cryptoCancelSweep(ctx, current, p[1], in)
				} else {
					e = a.cryptoRetrySweep(ctx, current, p[1], in)
				}
				if e == nil {
					var v CryptoSweepJob
					v, e = a.store.cryptoSweepJob(p[1])
					if e == nil {
						jsonResponse(w, 200, v)
					}
				}
			} else {
				e = commerceFail(404, "功能不存在")
			}
		}
	} else {
		e = commerceFail(405, "方法不支持")
	}
	if e != nil {
		commerceWriteError(w, e)
	}
	return true
}
func (a *App) cryptoRetrySweep(ctx context.Context, actor Record, id string, in cryptoSweepInput) error {
	if !operationPattern.MatchString(in.OperationID) {
		return commerceFail(400, "缺少有效操作标识")
	}
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state string
	var lease int64
	if e = tx.QueryRow("SELECT state,lease_until FROM crypto_sweep_jobs WHERE id=?", id).Scan(&state, &lease); e != nil {
		return e
	}
	if lease > time.Now().Unix() {
		return commerceFail(409, "任务正在执行，请等待本次处理结束")
	}
	if state == "complete" || state == "cancelled" {
		return commerceFail(409, "任务已经结束")
	}
	if state == "failed" || state == "review_required" {
		return commerceFail(409, "交易失败或证据冲突，需要人工核对；不会自动重新补 Gas")
	}
	if _, e = tx.Exec("UPDATE crypto_sweep_jobs SET state='waiting',error='',lease_token='',lease_until=0,updated=? WHERE id=?", time.Now().Unix(), id); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.store.audit(actor.Username, "crypto_sweep_retry", id)
	return nil
}

func (a *App) cryptoCancelSweep(ctx context.Context, actor Record, id string, in cryptoSweepInput) error {
	if !operationPattern.MatchString(in.OperationID) {
		return commerceFail(400, "缺少有效操作标识")
	}
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state string
	var lease int64
	if e = tx.QueryRow("SELECT state,lease_until FROM crypto_sweep_jobs WHERE id=?", id).Scan(&state, &lease); e != nil {
		return e
	}
	if state == "cancelled" {
		return nil
	}
	if state == "complete" {
		return commerceFail(409, "任务已经完成")
	}
	if lease > time.Now().Unix() {
		return commerceFail(409, "任务正在执行，请稍后结束")
	}
	var pending int
	if e = tx.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions WHERE job_id=? AND state NOT IN ('confirmed','reverted')", id).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return commerceFail(409, "仍有已签名交易等待最终确认，不能结束或释放资金保留")
	}
	if _, e = tx.Exec("UPDATE crypto_sweep_jobs SET state='cancelled',lease_token='',lease_until=0,updated=? WHERE id=?", time.Now().Unix(), id); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.store.audit(actor.Username, "crypto_sweep_cancel", id)
	return nil
}
