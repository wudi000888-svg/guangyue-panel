package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
)

type cryptoSolanaRPCInstruction struct {
	ProgramID string `json:"programId"`
	Parsed    struct {
		Type string `json:"type"`
		Info struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
			Authority   string `json:"authority"`
			Mint        string `json:"mint"`
			Amount      string `json:"amount"`
			TokenAmount struct {
				Amount   string `json:"amount"`
				Decimals int    `json:"decimals"`
			} `json:"tokenAmount"`
		} `json:"info"`
	} `json:"parsed"`
}
type cryptoSolanaRPCTransaction struct {
	Slot        uint64          `json:"slot"`
	BlockTime   *int64          `json:"blockTime"`
	Version     json.RawMessage `json:"version"`
	Transaction struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			AccountKeys []struct {
				Pubkey string `json:"pubkey"`
				Signer bool   `json:"signer"`
			} `json:"accountKeys"`
			Instructions []cryptoSolanaRPCInstruction `json:"instructions"`
		} `json:"message"`
	} `json:"transaction"`
	Meta *struct {
		Err   json.RawMessage `json:"err"`
		Fee   uint64          `json:"fee"`
		Inner []struct {
			Index        int                          `json:"index"`
			Instructions []cryptoSolanaRPCInstruction `json:"instructions"`
		} `json:"innerInstructions"`
		Pre        []cryptoSolanaRPCTokenBalance `json:"preTokenBalances"`
		Post       []cryptoSolanaRPCTokenBalance `json:"postTokenBalances"`
		PreNative  []uint64                      `json:"preBalances"`
		PostNative []uint64                      `json:"postBalances"`
	} `json:"meta"`
}
type cryptoSolanaRPCTokenBalance struct {
	Index   int    `json:"accountIndex"`
	Mint    string `json:"mint"`
	Owner   string `json:"owner"`
	Program string `json:"programId"`
	Amount  struct {
		Amount   string `json:"amount"`
		Decimals int    `json:"decimals"`
	} `json:"uiTokenAmount"`
}

func (v *cryptoSolanaRPCTokenBalance) UnmarshalJSON(raw []byte) error {
	type plain cryptoSolanaRPCTokenBalance
	var value plain
	var presence struct {
		Index *int `json:"accountIndex"`
	}
	if json.Unmarshal(raw, &value) != nil || json.Unmarshal(raw, &presence) != nil || presence.Index == nil {
		return errors.New("Solana 代币余额账户索引凭据缺失")
	}
	*v = cryptoSolanaRPCTokenBalance(value)
	return nil
}

func (c *cryptoSolana) transaction(ctx context.Context, signature string) (*cryptoSolanaRPCTransaction, error) {
	if !cryptoSolanaSignature(signature) {
		return nil, errors.New("Solana 交易签名无效")
	}
	// Agave v1 jsonParsed uses the same ordered UiParsedMessage account keys,
	// parsed instructions and canonical token-balance metadata as legacy/v0.
	// Only this receipt reader admits v1; signed outgoing envelopes stay legacy.
	a, b, e := c.pair(ctx, "getTransaction", []any{signature, object{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 1}})
	if e != nil {
		var method cryptoRPCMethodError
		if errors.As(e, &method) && method.Code == -32015 {
			return nil, commerceFail(409, "Solana 交易版本暂不支持，款项保留待人工核对")
		}
		return nil, e
	}
	if string(a) == "null" && string(b) == "null" {
		return nil, nil
	}
	var v cryptoSolanaRPCTransaction
	if e = cryptoSolanaTypedSame(a, b, &v); e != nil {
		return nil, e
	}
	if v.Meta == nil || len(v.Meta.Err) == 0 || v.Meta.Fee == 0 || v.Meta.Pre == nil || v.Meta.Post == nil || v.Transaction.Message.Instructions == nil {
		return nil, errors.New("Solana 交易执行凭据缺失")
	}
	if len(v.Transaction.Signatures) == 0 || v.Transaction.Signatures[0] != signature || v.BlockTime == nil || *v.BlockTime <= 0 || v.Slot == 0 || len(v.Transaction.Message.AccountKeys) == 0 || len(v.Transaction.Message.AccountKeys) > 256 {
		return nil, errors.New("Solana 最终交易数据无效")
	}
	var version any
	if json.Unmarshal(v.Version, &version) != nil || (version != "legacy" && version != float64(0) && version != float64(1)) {
		return nil, errors.New("Solana 交易版本尚不支持，款项保留待核对")
	}
	return &v, nil
}
func (c *cryptoSolana) signatures(ctx context.Context, address string, start, head uint64) ([]string, error) {
	all := []string{}
	before := ""
	seen := map[string]bool{}
	for page := 0; page < 8; page++ {
		options := object{"commitment": "finalized", "limit": 200, "minContextSlot": head}
		if before != "" {
			options["before"] = before
		}
		a, b, e := c.pair(ctx, "getSignaturesForAddress", []any{address, options})
		if e != nil {
			return nil, e
		}
		if string(a) == "null" || string(b) == "null" {
			return nil, errors.New("Solana 交易历史响应缺失")
		}
		var list []struct {
			Signature          string          `json:"signature"`
			Slot               uint64          `json:"slot"`
			Err                json.RawMessage `json:"err"`
			ConfirmationStatus string          `json:"confirmationStatus"`
		}
		if e = cryptoSolanaSame(a, b, &list); e != nil {
			return nil, e
		}
		if len(list) > 200 {
			return nil, errors.New("Solana 交易历史超限")
		}
		old := false
		for _, v := range list {
			if !cryptoSolanaSignature(v.Signature) || seen[v.Signature] || v.ConfirmationStatus != "finalized" {
				return nil, errors.New("Solana 交易历史核验失败")
			}
			seen[v.Signature] = true
			before = v.Signature
			if v.Slot < start {
				old = true
				continue
			}
			if v.Slot > head {
				continue
			}
			if len(v.Err) > 0 && string(v.Err) != "null" {
				continue
			}
			all = append(all, v.Signature)
		}
		if len(list) < 200 || old {
			return all, nil
		}
	}
	return nil, errors.New("Solana 交易历史较多，本轮未完成核验，游标保持不变")
}

// A parsed CPI transfer can be present even when the caller catches its error.
// Only the actual owner-level token balance delta is a credit, regardless of
// how many attempted inner instructions the RPC parser displays.
func cryptoSolanaTransfers(v *cryptoSolanaRPCTransaction, signature, mint, owner string, block cryptoEVMBlock) ([]cryptoScannedTransfer, error) {
	if v == nil || v.Meta == nil || len(v.Meta.Err) == 0 || v.Meta.Pre == nil || v.Meta.Post == nil || v.Transaction.Message.Instructions == nil {
		return nil, errors.New("Solana 代币执行余额凭据缺失")
	}
	if string(v.Meta.Err) != "null" {
		return nil, nil
	}
	keys := v.Transaction.Message.AccountKeys
	seenKeys := map[string]bool{}
	for _, key := range keys {
		if !cryptoSolanaAddress(key.Pubkey) || seenKeys[key.Pubkey] {
			return nil, errors.New("Solana 交易账户身份无效")
		}
		seenKeys[key.Pubkey] = true
	}
	pre, post := map[int]cryptoSolanaRPCTokenBalance{}, map[int]cryptoSolanaRPCTokenBalance{}
	decode := func(list []cryptoSolanaRPCTokenBalance, target map[int]cryptoSolanaRPCTokenBalance) error {
		for _, x := range list {
			if x.Index < 0 || x.Index >= len(keys) || !cryptoSolanaAddress(x.Mint) || !cryptoSolanaAddress(x.Owner) || (x.Program != "" && x.Program != cryptoSolanaTokenProgram) {
				// Metadata for a different token program is irrelevant to our
				// canonical SPL mints, but cannot supply evidence for them.
				if x.Mint != mint {
					continue
				}
				return errors.New("Solana 收款代币账户身份凭据无效")
			}
			if _, duplicate := target[x.Index]; duplicate {
				return errors.New("Solana 代币余额账户索引重复")
			}
			if x.Mint == mint {
				n, e := cryptoAtoms(x.Amount.Amount)
				if e != nil || !n.IsUint64() || x.Amount.Decimals != 6 {
					return errors.New("Solana 收款代币余额或精度无效")
				}
			}
			target[x.Index] = x
		}
		return nil
	}
	if e := decode(v.Meta.Pre, pre); e != nil {
		return nil, e
	}
	if e := decode(v.Meta.Post, post); e != nil {
		return nil, e
	}
	before, after := new(big.Int), new(big.Int)
	anchor := len(keys)
	ownedAccounts := map[string]bool{}
	changedOwnerMint := false
	for index, x := range pre {
		if y, ok := post[index]; ok && y.Mint != x.Mint {
			if (x.Mint == mint && x.Owner == owner) || (y.Mint == mint && y.Owner == owner) {
				changedOwnerMint = true
			}
		}
		if x.Mint == mint && x.Owner == owner {
			n, _ := cryptoAtoms(x.Amount.Amount)
			before.Add(before, n)
			ownedAccounts[keys[index].Pubkey] = true
		}
	}
	for index, x := range post {
		if x.Mint == mint && x.Owner == owner {
			n, _ := cryptoAtoms(x.Amount.Amount)
			after.Add(after, n)
			ownedAccounts[keys[index].Pubkey] = true
			if index < anchor {
				anchor = index
			}
		}
	}
	net := new(big.Int).Sub(after, before)
	if net.Sign() <= 0 {
		return nil, nil
	}
	if !net.IsUint64() || anchor == len(keys) {
		return nil, errors.New("Solana 收款净增余额无效")
	}
	ata, e := cryptoSolanaATA(owner, mint)
	if e != nil {
		return nil, e
	}
	reason, sender := "", owner
	if changedOwnerMint {
		reason = "收款代币账户在交易内变更币种，需人工核对"
	}
	for account := range ownedAccounts {
		if account != ata {
			reason = "款项进入辅助代币账户，需核对外部授权后确认"
		}
	}
	for index, x := range post {
		if x.Mint != mint || x.Owner != owner {
			continue
		}
		if old, ok := pre[index]; ok && old.Owner != owner {
			reason = "代币账户所有权在交易内变更，需人工核对"
		}
	}
	parsed := new(big.Int)
	accounts := map[string]cryptoSolanaRPCTokenBalance{}
	for index, x := range pre {
		accounts[keys[index].Pubkey] = x
	}
	for index, x := range post {
		accounts[keys[index].Pubkey] = x
	}
	inspect := func(ix cryptoSolanaRPCInstruction) {
		if ix.ProgramID != cryptoSolanaTokenProgram || (ix.Parsed.Type != "transfer" && ix.Parsed.Type != "transferChecked") {
			return
		}
		info := ix.Parsed.Info
		destination, ok := accounts[info.Destination]
		if !ok || destination.Owner != owner || destination.Mint != mint {
			return
		}
		source, ok := accounts[info.Source]
		if !ok || source.Mint != mint || !cryptoSolanaAddress(source.Owner) {
			reason = "付款来源代币账户凭据不足，需人工核对"
			return
		}
		// Internal shuffles are already excluded by owner-level netting.
		if source.Owner == owner {
			return
		}
		amount := info.Amount
		if ix.Parsed.Type == "transferChecked" {
			if info.Mint != mint || info.TokenAmount.Decimals != 6 {
				reason = "转账解析币种或精度不匹配，需人工核对"
				return
			}
			amount = info.TokenAmount.Amount
		}
		n, e := cryptoAtoms(amount)
		if e != nil || !n.IsUint64() {
			reason = "转账解析金额无效，需人工核对"
			return
		}
		parsed.Add(parsed, n)
		sender = source.Owner
	}
	if len(v.Transaction.Message.Instructions) > 1000 {
		return nil, errors.New("Solana 外层指令过多")
	}
	for _, ix := range v.Transaction.Message.Instructions {
		inspect(ix)
	}
	seen := map[int]bool{}
	for _, inner := range v.Meta.Inner {
		if inner.Index < 0 || inner.Index >= len(v.Transaction.Message.Instructions) || seen[inner.Index] || len(inner.Instructions) > 1000 {
			return nil, errors.New("Solana 内层指令身份无效")
		}
		seen[inner.Index] = true
		for _, ix := range inner.Instructions {
			inspect(ix)
		}
	}
	if parsed.Cmp(net) != 0 {
		reason = "实际到账净额与转账指令不一致，需人工核对"
	}
	// Use one stable identity for both automatic and reviewed credits. A later
	// parser interpretation cannot add a second receipt under a different ID.
	index := uint64(2000000000 + anchor)
	return []cryptoScannedTransfer{{Log: cryptoEVMLog{Address: mint, Topics: []string{"solana_transfer", sender, owner}, Data: "0x" + net.Text(16), BlockNumber: v.Slot, BlockHash: block.Hash, TxHash: signature, Index: index}, Timestamp: *v.BlockTime, Final: true, ReviewReason: reason}}, nil
}

// Imported wallets may have old delegations even on their canonical ATA.
// Automatic fulfilment is allowed only while the current receiving account has
// no delegate and no external close authority, confirmed by both RPC sources.
func (c *cryptoSolana) receivingAuthority(ctx context.Context, owner, mint string, slot uint64) (string, error) {
	ata, e := cryptoSolanaATA(owner, mint)
	if e != nil {
		return "", e
	}
	raw, e := c.account(ctx, ata, slot)
	if e != nil {
		return "", e
	}
	var account struct {
		Owner string `json:"owner"`
		Data  struct {
			Parsed struct {
				Type string `json:"type"`
				Info struct {
					Owner          string `json:"owner"`
					Mint           string `json:"mint"`
					State          string `json:"state"`
					Delegate       string `json:"delegate"`
					CloseAuthority string `json:"closeAuthority"`
				} `json:"info"`
			} `json:"parsed"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &account) != nil || account.Owner != cryptoSolanaTokenProgram || account.Data.Parsed.Type != "account" || account.Data.Parsed.Info.Owner != owner || account.Data.Parsed.Info.Mint != mint || account.Data.Parsed.Info.State != "initialized" {
		return "当前收款代币账户权限无法核验，需人工确认", nil
	}
	info := account.Data.Parsed.Info
	if info.Delegate != "" || (info.CloseAuthority != "" && info.CloseAuthority != owner) {
		return "收款代币账户存在外部授权，需人工核对", nil
	}
	return "", nil
}
func (a *App) scanCryptoSolanaInvoice(ctx context.Context, v CryptoInvoice, settings CryptoPaymentSettings) error {
	chain := v.Snapshot.Chain
	if current, ok := settings.chain(cryptoSolanaChainID); ok && current.RPCURL != "" && current.RPCBackupURL != "" {
		chain = current
	}
	if e := a.store.cryptoChainAvailable(a.store.db, cryptoSolanaChainID); e != nil {
		return e
	}
	c, e := newCryptoSolana(chain, a.cfg.Dev)
	if e != nil {
		return e
	}
	head, e := c.Finalized(ctx)
	if e != nil {
		return e
	}
	if e = c.ValidateToken(ctx, v.Contract, v.Decimals, head.Number); e != nil {
		return e
	}
	var next int64
	var previous string
	e = a.store.db.QueryRow("SELECT next_block,finalized_hash FROM crypto_invoice_chains WHERE invoice_id=? AND chain_id=?", v.ID, cryptoSolanaChainID).Scan(&next, &previous)
	if errors.Is(e, sql.ErrNoRows) {
		next = v.StartBlock
		_, e = a.store.db.Exec("INSERT INTO crypto_invoice_chains(invoice_id,chain_id,next_block) VALUES(?,?,?) ON CONFLICT DO NOTHING", v.ID, cryptoSolanaChainID, next)
		if e != nil {
			return e
		}
		if e = a.store.db.QueryRow("SELECT next_block,finalized_hash FROM crypto_invoice_chains WHERE invoice_id=? AND chain_id=?", v.ID, cryptoSolanaChainID).Scan(&next, &previous); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if next < 0 || head.Number > 1<<63-2 {
		return errors.New("Solana 交易游标无效")
	}
	if previous != "" && next > 0 {
		block, e := c.block(ctx, uint64(next-1))
		if e != nil {
			return e
		}
		if block.Hash != previous {
			return a.store.freezeCryptoChain(cryptoSolanaChainID)
		}
	}
	if uint64(next) > head.Number {
		return nil
	}
	transfers := []cryptoScannedTransfer{}
	seen := map[string]bool{}
	blocks := map[uint64]cryptoEVMBlock{}
	for _, mint := range []string{cryptoSolanaUSDC, cryptoSolanaUSDT} {
		ata, e := cryptoSolanaATA(v.Address, mint)
		if e != nil {
			return e
		}
		tokenAccounts, e := c.tokenAccounts(ctx, mint, v.Address, head.Number)
		if e != nil {
			return e
		}
		accountList := []string{ata}
		for _, account := range tokenAccounts {
			if account.Address != ata {
				accountList = append(accountList, account.Address)
			}
		}
		signatures := []string{}
		seenSignatures := map[string]bool{}
		for _, account := range accountList {
			list, err := c.signatures(ctx, account, uint64(next), head.Number)
			if err != nil {
				return err
			}
			for _, sig := range list {
				if !seenSignatures[sig] {
					signatures = append(signatures, sig)
					seenSignatures[sig] = true
				}
			}
		}
		for _, sig := range signatures {
			transaction, e := c.transaction(ctx, sig)
			if e != nil {
				return e
			}
			if transaction == nil {
				return errors.New("Solana 最终交易历史暂不可用，游标保持不变")
			}
			if transaction.Slot < uint64(next) || transaction.Slot > head.Number {
				return errors.New("Solana 交易槽位越界")
			}
			block, ok := blocks[transaction.Slot]
			if !ok {
				block, e = c.block(ctx, transaction.Slot)
				if e != nil {
					return e
				}
				blocks[transaction.Slot] = block
			}
			if uint64(*transaction.BlockTime) != block.Timestamp {
				return errors.New("Solana 交易时间与最终区块不一致")
			}
			events, e := cryptoSolanaTransfers(transaction, sig, mint, v.Address, block)
			if e != nil {
				return e
			}
			for _, event := range events {
				if event.ReviewReason == "" {
					reason, err := c.receivingAuthority(ctx, v.Address, mint, head.Number)
					if err != nil {
						return err
					}
					event.ReviewReason = reason
				}
				id := sig + "/" + new(big.Int).SetUint64(event.Log.Index).String()
				if !seen[id] {
					transfers = append(transfers, event)
					seen[id] = true
				}
			}
		}
	}
	sort.Slice(transfers, func(i, j int) bool {
		if transfers[i].Log.BlockNumber != transfers[j].Log.BlockNumber {
			return transfers[i].Log.BlockNumber < transfers[j].Log.BlockNumber
		}
		if transfers[i].Log.TxHash != transfers[j].Log.TxHash {
			return transfers[i].Log.TxHash < transfers[j].Log.TxHash
		}
		return transfers[i].Log.Index < transfers[j].Log.Index
	})
	return a.applyCryptoScan(v.ID, cryptoSolanaChainID, next, head.Number, int64(head.Number)+1, head.Hash, head, transfers)
}
