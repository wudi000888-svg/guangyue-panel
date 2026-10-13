package controlplane

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

func cryptoSolanaTokenMessage(funding, source, destination, mint, hash string, accounts []cryptoSolanaTokenAccount, createATA bool) ([]byte, error) {
	if len(accounts) == 0 || len(accounts) > 20 {
		return nil, errors.New("Solana 来源代币账户数量超限")
	}
	ata, e := cryptoSolanaATA(destination, mint)
	if e != nil {
		return nil, e
	}
	instructions := []cryptoSolanaInstruction{}
	if createATA {
		instructions = append(instructions, cryptoSolanaInstruction{cryptoSolanaATAProgram, []cryptoSolanaMeta{{funding, true}, {ata, true}, {destination, false}, {mint, false}, {cryptoSolanaSystemProgram, false}, {cryptoSolanaTokenProgram, false}}, []byte{1}})
	}
	seen := map[string]bool{}
	for _, account := range accounts {
		n, e := cryptoAtoms(account.Amount)
		if e != nil || !n.IsUint64() || n.Sign() <= 0 || !cryptoSolanaAddress(account.Address) || seen[account.Address] {
			return nil, errors.New("Solana 来源代币金额或地址无效")
		}
		seen[account.Address] = true
		data := make([]byte, 10)
		data[0] = 12
		binary.LittleEndian.PutUint64(data[1:9], n.Uint64())
		data[9] = 6
		instructions = append(instructions, cryptoSolanaInstruction{cryptoSolanaTokenProgram, []cryptoSolanaMeta{{account.Address, true}, {mint, false}, {ata, true}, {source, false}}, data})
	}
	message, e := cryptoSolanaMessage(funding, source, hash, instructions)
	if e != nil {
		return nil, e
	}
	if len(message)+129 > 1232 {
		return nil, commerceFail(400, "Solana 来源代币账户较多，签名交易超过1232字节，请使用原钱包分批提取")
	}
	return message, nil
}
func (c *cryptoSolana) rent(ctx context.Context) (*big.Int, error) {
	a, b, e := c.pair(ctx, "getMinimumBalanceForRentExemption", []any{165, object{"commitment": "finalized"}})
	if e != nil {
		return nil, e
	}
	var x, y uint64
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x == 0 || x != y {
		return nil, errors.New("Solana 代币账户租金预估无法核验")
	}
	return new(big.Int).SetUint64(x), nil
}
func cryptoSolanaSweepMessage(quote cryptoSweepQuote, plan CryptoSweepPlanItem, hash string) ([]byte, error) {
	amount, e := cryptoAtoms(plan.AmountAtoms)
	if e != nil || !amount.IsUint64() {
		return nil, errors.New("Solana 提取金额无效")
	}
	if quote.Asset.Contract == "" {
		return cryptoSolanaNativeMessage(quote.Preview.FundingAddress, plan.Address, quote.Preview.Destination, hash, amount.Uint64())
	}
	return cryptoSolanaTokenMessage(quote.Preview.FundingAddress, plan.Address, quote.Preview.Destination, quote.Asset.Contract, hash, plan.SolanaSourceTokens, plan.RentAtoms != "0")
}
func (a *App) cryptoPreviewSolanaSweep(ctx context.Context, actor Record, w CryptoWallet, s CryptoPaymentSettings, chain CryptoChain, in cryptoSweepInput) (CryptoSweepPreview, error) {
	out := CryptoSweepPreview{WalletID: w.ID, ChainID: chain.ID, NativeSymbol: "SOL", NativeDecimals: 9, FeeModel: "solana", Items: []CryptoSweepPlanItem{}, TotalTopupAtoms: "0", TotalRentAtoms: "0"}
	if (in.FeeMode != "" && in.FeeMode != "network") || in.GasPriceAtoms != "" {
		return out, commerceFail(400, "Solana 采用网络基础签名手续费，不支持 EVM Gas 费率选项")
	}
	asset, ok := s.asset(in.AssetID)
	if in.AssetID == "native" {
		asset = CryptoAsset{ID: "native", ChainID: cryptoSolanaChainID, Symbol: "SOL", Decimals: 9}
		ok = true
	}
	if !ok || asset.ChainID != cryptoSolanaChainID {
		return out, commerceFail(400, "所选资产与网络不一致")
	}
	if !cryptoSolanaAddress(in.Destination) || in.Destination == cryptoSolanaSystemProgram || in.Destination == cryptoSolanaTokenProgram || in.Destination == cryptoSolanaATAProgram || in.Destination == cryptoSolanaUSDC || in.Destination == cryptoSolanaUSDT {
		return out, commerceFail(400, "Solana 提取目标地址无效或为程序地址")
	}
	minimum, e := cryptoAtoms(in.MinAtoms)
	if e != nil || !minimum.IsUint64() {
		return out, commerceFail(400, "Solana 最小提取数量无效")
	}
	budget := new(big.Int)
	automatic := strings.TrimSpace(in.MaxGasAtoms) == ""
	if !automatic {
		budget, e = cryptoAtoms(in.MaxGasAtoms)
		if e != nil || budget.Sign() <= 0 || !budget.IsUint64() {
			return out, commerceFail(400, "Solana 手续费预算无效")
		}
	}
	if e = a.store.cryptoChainAvailable(a.store.db, cryptoSolanaChainID); e != nil {
		return out, e
	}
	c, e := newCryptoSolana(chain, a.cfg.Dev)
	if e != nil {
		return out, e
	}
	head, e := c.Finalized(ctx)
	if e != nil {
		return out, e
	}
	if asset.Contract != "" {
		if e = c.ValidateToken(ctx, asset.Contract, asset.Decimals, head.Number); e != nil {
			return out, e
		}
	}
	funding, e := a.store.cryptoSolanaFundingAddress(ctx, w)
	if e != nil {
		return out, e
	}
	if in.Destination == funding.Address {
		return out, commerceFail(400, "提取目标不能是本钱包的 Gas 资金地址")
	}
	fundBalance, e := c.NativeBalance(ctx, funding.Address, head.Number)
	if e != nil {
		return out, e
	}
	hash, _, e := c.latestBlockhash(ctx)
	if e != nil {
		return out, e
	}
	addresses, e := cryptoSolanaSelectedAddresses(a.store.db, w.ID, in.AddressIDs)
	if e != nil {
		return out, e
	}
	rent := new(big.Int)
	if asset.Contract != "" {
		ata, err := cryptoSolanaATA(in.Destination, asset.Contract)
		if err != nil {
			return out, err
		}
		raw, err := c.account(ctx, ata, head.Number)
		if err != nil {
			return out, err
		}
		if string(raw) == "null" {
			rent, err = c.rent(ctx)
			if err != nil {
				return out, err
			}
		} else {
			var data struct {
				Owner string `json:"owner"`
				Data  struct {
					Parsed struct {
						Info struct {
							Owner string `json:"owner"`
							Mint  string `json:"mint"`
						} `json:"info"`
					} `json:"parsed"`
				} `json:"data"`
			}
			if json.Unmarshal(raw, &data) != nil || data.Owner != cryptoSolanaTokenProgram || data.Data.Parsed.Info.Owner != in.Destination || data.Data.Parsed.Info.Mint != asset.Contract {
				return out, commerceFail(409, "Solana 目标代币账户无效")
			}
		}
	}
	total, fees := new(big.Int), new(big.Int)
	for _, x := range addresses {
		if asset.Contract != "" {
			allowed, err := cryptoAutoGasOrder(a.store.db, w.ID, cryptoSolanaChainID, x.ID, asset)
			if err != nil {
				return out, err
			}
			if !allowed {
				continue
			}
		}
		if e = a.store.cryptoCheckWalletAddress(a.store.db, w, x.Index, x.Path, x.Address); e != nil {
			return out, commerceFail(422, "历史收款地址与钱包派生记录不一致")
		}
		if x.Address == in.Destination {
			return out, commerceFail(400, "提取目标不能是来源收款地址")
		}
		amount := new(big.Int)
		sourceTokens := []cryptoSolanaTokenAccount{}
		if asset.Contract == "" {
			amount, e = c.NativeBalance(ctx, x.Address, head.Number)
		} else {
			var accounts []cryptoSolanaTokenAccount
			accounts, e = c.tokenAccounts(ctx, asset.Contract, x.Address, head.Number)
			for _, account := range accounts {
				n, err := cryptoAtoms(account.Amount)
				if err != nil {
					return out, err
				}
				if n.Sign() > 0 {
					sourceTokens = append(sourceTokens, account)
					amount.Add(amount, n)
				}
			}
		}
		if e != nil {
			return out, e
		}
		if asset.Contract != "" && !cryptoAutoGasEligible(asset, amount) {
			continue
		}
		if amount.Sign() == 0 || amount.Cmp(minimum) < 0 {
			continue
		}
		if !amount.IsUint64() {
			return out, commerceFail(409, "Solana 提取数量超限")
		}
		plan := CryptoSweepPlanItem{AddressID: x.ID, Address: x.Address, Path: x.Path, AmountAtoms: amount.String(), GasLimit: 1, GasPriceAtoms: "0", TopupAtoms: "0", FundingGasAtoms: "0", FundingGasLimit: 0, RentAtoms: "0", SolanaSourceTokens: sourceTokens}
		if len(out.Items) == 0 && rent.Sign() > 0 {
			plan.RentAtoms = rent.String()
		}
		quote := cryptoSweepQuote{Chain: chain, Asset: asset, Preview: CryptoSweepPreview{FundingAddress: funding.Address, Destination: in.Destination}}
		message, err := cryptoSolanaSweepMessage(quote, plan, hash)
		if err != nil {
			return out, err
		}
		fee, err := c.Fee(ctx, message)
		if err != nil {
			return out, err
		}
		plan.GasFeeAtoms = fee.String()
		total.Add(total, amount)
		fees.Add(fees, fee)
		out.Items = append(out.Items, plan)
	}
	now := time.Now().Unix()
	out.AssetID = asset.ID
	out.Destination = in.Destination
	out.MinAtoms = minimum.String()
	out.CheckedAt = now
	out.ExpiresAt = now + 120
	out.BlockNumber = head.Number
	out.FundingAddress = funding.Address
	out.FundingBalanceAtoms = fundBalance.String()
	out.TotalAtoms = total.String()
	out.TotalGasAtoms = fees.String()
	if len(out.Items) > 0 {
		out.TotalRentAtoms = rent.String()
	}
	if len(out.Items) == 0 {
		rent.SetInt64(0)
	}
	required := new(big.Int).Add(fees, rent)
	out.RequiredFundingAtoms = required.String()
	short := new(big.Int).Sub(required, fundBalance)
	if short.Sign() < 0 {
		short.SetInt64(0)
	}
	out.FundingShortfallAtoms = short.String()
	if automatic {
		budget.Set(required)
	}
	out.MaxGasAtoms = budget.String()
	out.CanSubmit = len(out.Items) > 0
	switch {
	case len(out.Items) == 0:
		out.UnavailableReason = "没有达到提取条件的地址；稳定币仅归集真实订单收款地址，余额须超过 0.1"
	case required.Cmp(budget) > 0:
		out.CanSubmit = false
		out.UnavailableReason = "预计手续费及账户租金超过本次预算"
	case required.Cmp(fundBalance) > 0:
		out.CanSubmit = false
		out.UnavailableReason = "Gas 资金地址余额不足，请充值 SOL 并等待最终确认"
	}
	if !out.CanSubmit {
		return out, nil
	}
	sealed, e := a.store.vault.seal(cryptoSweepQuote{ActorID: actor.ID, Preview: out, Chain: chain, Asset: asset})
	if e != nil {
		return out, e
	}
	out.Quote = base64.StdEncoding.EncodeToString(sealed)
	return out, nil
}

func cryptoSolanaSelectedAddresses(q cryptoWalletQuery, id string, ids []string) ([]CryptoWalletAddress, error) {
	if len(ids) == 0 {
		all, e := cryptoWalletNetworkAddresses(q, id, true, 1001)
		if e != nil {
			return nil, e
		}
		if len(all) > 1000 {
			return nil, commerceFail(400, "地址较多，请分批提取")
		}
		return all, nil
	}
	selected := map[string]bool{}
	for _, v := range ids {
		selected[v] = true
	}
	if len(selected) > 1000 {
		return nil, commerceFail(400, "每次最多选择1000个地址")
	}
	args := []any{id}
	marks := []string{}
	for key := range selected {
		args = append(args, key)
		marks = append(marks, "?")
	}
	rows, e := q.Query("SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE wallet_id IN ("+cryptoWalletOwnerScope+") AND "+cryptoWalletPathSQL("", true)+" AND id IN ("+strings.Join(marks, ",")+") ORDER BY address_index", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []CryptoWalletAddress{}
	for rows.Next() {
		var x CryptoWalletAddress
		if e = rows.Scan(&x.ID, &x.WalletID, &x.Index, &x.Path, &x.Address, &x.Label, &x.Created); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	if e = rows.Err(); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if len(out) != len(selected) {
		return nil, commerceFail(400, "选择包含其他钱包或不同网络地址")
	}
	return out, nil
}
