package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type cryptoSolanaIntent struct {
	ID, JobID, ItemID, WalletID, AddressID, Signature, Hash, FeeAtoms, RentAtoms string
	LastValidHeight                                                              uint64
	Message                                                                      []byte
}
type cryptoSolanaStoredTx struct {
	ID, JobID, ItemID, Signature, State string
	Raw, Message, Intent, Receipt       []byte
	Height                              uint64
}

const cryptoSolanaTxColumns = "id,job_id,item_id,signature,raw,message,intent,last_valid_height,state,receipt"

func scanCryptoSolanaTx(row cryptoScanner) (cryptoSolanaStoredTx, error) {
	var v cryptoSolanaStoredTx
	e := row.Scan(&v.ID, &v.JobID, &v.ItemID, &v.Signature, &v.Raw, &v.Message, &v.Intent, &v.Height, &v.State, &v.Receipt)
	return v, e
}

type cryptoSolanaFinalProof struct {
	TransactionID, JobID, ItemID, Signature, BlockHash string
	Slot                                               uint64
	Time                                               int64
	Failed                                             bool
	Raw                                                []byte
	Fee                                                uint64
	FundingBefore                                      uint64
	FundingAfter                                       uint64
	BalanceEvidence                                    bool
}

func (s *Store) cryptoValidateSolanaTx(v cryptoSolanaStoredTx, quote cryptoSweepQuote, plan CryptoSweepPlanItem) ([]byte, error) {
	var intent cryptoSolanaIntent
	var raw []byte
	if s.vault.open(v.Intent, &intent) != nil || s.vault.open(v.Raw, &raw) != nil {
		return nil, errCryptoSweepIntegrity
	}
	fail := func() ([]byte, error) { clear(raw); return nil, errCryptoSweepIntegrity }
	if v.ID != intent.ID || v.JobID != intent.JobID || v.ItemID != intent.ItemID || v.Signature != intent.Signature || v.Height != intent.LastValidHeight || quote.Preview.WalletID != intent.WalletID || plan.AddressID != intent.AddressID || plan.GasFeeAtoms != intent.FeeAtoms || plan.RentAtoms != intent.RentAtoms || !bytes.Equal(v.Message, intent.Message) || !cryptoSolanaSignature(v.Signature) || v.Height == 0 {
		return fail()
	}
	message, e := cryptoSolanaSweepMessage(quote, plan, intent.Hash)
	if e != nil || !bytes.Equal(message, v.Message) || len(raw) != 129+len(message) || raw[0] != 2 || !bytes.Equal(raw[129:], message) || base58.Encode(raw[1:65]) != v.Signature {
		return fail()
	}
	if !ed25519.Verify(ed25519.PublicKey(base58.Decode(quote.Preview.FundingAddress)), message, raw[1:65]) || !ed25519.Verify(ed25519.PublicKey(base58.Decode(plan.Address)), message, raw[65:129]) {
		return fail()
	}
	switch v.State {
	case "signed", "pending", "expired_unknown":
		if len(v.Receipt) != 0 {
			return fail()
		}
	case "confirmed", "reverted":
		var proof cryptoSolanaFinalProof
		if s.vault.open(v.Receipt, &proof) != nil || proof.TransactionID != v.ID || proof.JobID != v.JobID || proof.ItemID != v.ItemID || proof.Signature != v.Signature || !cryptoSolanaAddress(proof.BlockHash) || proof.Slot == 0 || proof.Time <= 0 || !bytes.Equal(proof.Raw, raw) || (v.State == "reverted") != proof.Failed {
			return fail()
		}
		fee, e := cryptoAtoms(plan.GasFeeAtoms)
		if e != nil || fee.Cmp(new(big.Int).SetUint64(proof.Fee)) < 0 {
			return fail()
		}
		rent, e := cryptoAtoms(plan.RentAtoms)
		if e != nil || !proof.BalanceEvidence || proof.FundingBefore < proof.FundingAfter {
			return fail()
		}
		cost := new(big.Int).SetUint64(proof.FundingBefore - proof.FundingAfter)
		if cost.Cmp(new(big.Int).SetUint64(proof.Fee)) < 0 || cost.Cmp(new(big.Int).Add(fee, rent)) > 0 {
			return fail()
		}
	default:
		return fail()
	}
	return raw, nil
}
func (a *App) cryptoStageSolanaTransfer(ctx context.Context, id, lease, itemID string, quote cryptoSweepQuote, plan CryptoSweepPlanItem, c *cryptoSolana) (cryptoSolanaStoredTx, error) {
	var out cryptoSolanaStoredTx
	head, e := c.Finalized(ctx)
	if e != nil {
		return out, e
	}
	hash, height, e := c.latestBlockhash(ctx)
	if e != nil {
		return out, e
	}
	message, e := cryptoSolanaSweepMessage(quote, plan, hash)
	if e != nil {
		return out, e
	}
	fee, e := c.Fee(ctx, message)
	if e != nil {
		return out, e
	}
	if e = c.approvedSweepCosts(ctx, quote, plan, fee, head.Number); e != nil {
		return out, e
	}
	rent, e := cryptoAtoms(plan.RentAtoms)
	if e != nil {
		return out, e
	}
	required := new(big.Int).Add(fee, rent)
	fundingBalance, e := c.NativeBalance(ctx, quote.Preview.FundingAddress, head.Number)
	if e != nil {
		return out, e
	}
	if fundingBalance.Cmp(required) < 0 {
		return out, commerceFail(409, "Solana Gas 资金余额不足，等待充值确认")
	}
	if quote.Asset.Contract == "" {
		balance, e := c.NativeBalance(ctx, plan.Address, head.Number)
		if e != nil {
			return out, e
		}
		amount, e := cryptoAtoms(plan.AmountAtoms)
		if e != nil || balance.Cmp(amount) < 0 {
			return out, commerceFail(409, "SOL 余额低于批准数量")
		}
	} else {
		if e = c.ValidateToken(ctx, quote.Asset.Contract, 6, head.Number); e != nil {
			return out, e
		}
		current, e := c.tokenAccounts(ctx, quote.Asset.Contract, plan.Address, head.Number)
		if e != nil {
			return out, e
		}
		balances := map[string]string{}
		currentTotal := new(big.Int)
		for _, x := range current {
			balances[x.Address] = x.Amount
			n, err := cryptoAtoms(x.Amount)
			if err != nil {
				return out, err
			}
			currentTotal.Add(currentTotal, n)
		}
		if !cryptoAutoGasEligible(quote.Asset, currentTotal) {
			return out, commerceFail(409, "订单收款地址稳定币余额需严格超过 0.1，已暂停新签名")
		}
		for _, x := range plan.SolanaSourceTokens {
			n, e := cryptoAtoms(balances[x.Address])
			amount, e2 := cryptoAtoms(x.Amount)
			if e != nil || e2 != nil || n.Cmp(amount) < 0 {
				return out, commerceFail(409, "Solana 来源代币余额低于批准数量")
			}
		}
	}
	tx, e := a.store.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = cryptoLockChain(tx, cryptoSolanaChainID); e != nil {
		return out, e
	}
	if _, e = tx.Exec("UPDATE crypto_wallet_state SET lock_version=lock_version+1 WHERE id=1"); e != nil {
		return out, e
	}
	if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
		return out, e
	}
	existing, e := scanCryptoSolanaTx(tx.QueryRow("SELECT "+cryptoSolanaTxColumns+" FROM crypto_solana_transactions WHERE item_id=?", itemID))
	if e == nil {
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	wallet, e := cryptoWalletByID(tx, quote.Preview.WalletID)
	if e != nil {
		return out, e
	}
	if e = cryptoSweepWallet(wallet); e != nil {
		return out, e
	}
	if !cryptoWalletSupports(wallet, cryptoSolanaChainID) {
		return out, commerceFail(409, "钱包未启用 Solana")
	}
	if quote.Asset.Contract != "" {
		allowed, err := cryptoAutoGasOrder(tx, wallet.ID, cryptoSolanaChainID, plan.AddressID, quote.Asset)
		if err != nil {
			return out, err
		}
		if !allowed {
			return out, commerceFail(409, "仅真实订单在本网络的收款地址可以使用 Gas 资金归集稳定币")
		}
	}
	if e = a.store.cryptoCheckWalletAddress(tx, wallet, cryptoSolanaAddressIndexMust(plan.Path), plan.Path, plan.Address); e != nil {
		return out, commerceFail(422, "Solana 来源地址派生记录不一致")
	}
	public, e := a.store.cryptoSolanaPublicByID(tx, wallet.ID)
	if e != nil || public.Funding.Address != quote.Preview.FundingAddress {
		return out, commerceFail(422, "Solana Gas 资金地址不一致")
	}
	secret, e := a.store.cryptoSecret(tx, wallet)
	if e != nil {
		return out, e
	}
	defer func() { secret.Mnemonic = "" }()
	sourceIndex, e := cryptoSolanaAddressIndex(plan.Path)
	if e != nil || sourceIndex == cryptoSolanaFundingIndex {
		return out, commerceFail(422, "Solana 收款路径无效")
	}
	fundingKey, e := cryptoSolanaKey(secret.Mnemonic, cryptoSolanaFundingIndex)
	if e != nil {
		return out, e
	}
	defer clear(fundingKey)
	sourceKey, e := cryptoSolanaKey(secret.Mnemonic, sourceIndex)
	if e != nil {
		return out, e
	}
	defer clear(sourceKey)
	if base58.Encode(fundingKey.Public().(ed25519.PublicKey)) != quote.Preview.FundingAddress || base58.Encode(sourceKey.Public().(ed25519.PublicKey)) != plan.Address {
		return out, commerceFail(422, "Solana 签名密钥与批准地址不一致")
	}
	raw := []byte{2}
	raw = append(raw, ed25519.Sign(fundingKey, message)...)
	raw = append(raw, ed25519.Sign(sourceKey, message)...)
	raw = append(raw, message...)
	defer clear(raw)
	out = cryptoSolanaStoredTx{ID: serial("GYT"), JobID: id, ItemID: itemID, Signature: base58.Encode(raw[1:65]), Message: message, Height: height, State: "signed"}
	out.Raw, e = a.store.vault.seal(raw)
	if e != nil {
		return out, e
	}
	out.Intent, e = a.store.vault.seal(cryptoSolanaIntent{out.ID, id, itemID, wallet.ID, plan.AddressID, out.Signature, hash, plan.GasFeeAtoms, plan.RentAtoms, height, message})
	if e != nil {
		return out, e
	}
	now := time.Now().Unix()
	if _, e = tx.Exec("INSERT INTO crypto_solana_transactions(id,job_id,item_id,chain_id,signature,raw,message,intent,last_valid_height,state,created,updated) VALUES(?,?,?,101,?,?,?,?,?,?,?,?)", out.ID, id, itemID, out.Signature, out.Raw, out.Message, out.Intent, out.Height, out.State, now, now); e != nil {
		return out, e
	}
	if _, e = tx.Exec("UPDATE crypto_sweep_items SET state='sweep_pending',error='' WHERE id=?", itemID); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func cryptoSolanaAddressIndexMust(path string) int64 {
	index, e := cryptoSolanaAddressIndex(path)
	if e != nil {
		return -1
	}
	return int64(index)
}
func (c *cryptoSolana) finalTransaction(ctx context.Context, signature string) (*cryptoSolanaFinalProof, error) {
	a, b, e := c.pair(ctx, "getTransaction", []any{signature, object{"encoding": "base64", "commitment": "finalized", "maxSupportedTransactionVersion": 0}})
	if e != nil {
		return nil, e
	}
	if string(a) == "null" && string(b) == "null" {
		return nil, nil
	}
	var v struct {
		Slot        uint64   `json:"slot"`
		Time        *int64   `json:"blockTime"`
		Transaction []string `json:"transaction"`
		Meta        *struct {
			Err  json.RawMessage `json:"err"`
			Fee  uint64          `json:"fee"`
			Pre  []uint64        `json:"preBalances"`
			Post []uint64        `json:"postBalances"`
		} `json:"meta"`
	}
	if e = cryptoSolanaTypedSame(a, b, &v); e != nil {
		return nil, e
	}
	if v.Meta == nil || len(v.Meta.Err) == 0 || v.Meta.Fee == 0 {
		return nil, commerceFail(422, "Solana 最终交易执行凭据缺失")
	}
	if v.Time == nil || *v.Time <= 0 || v.Slot == 0 || len(v.Transaction) != 2 || v.Transaction[1] != "base64" {
		return nil, commerceFail(422, "Solana 最终交易格式无效")
	}
	raw, e := base64.StdEncoding.DecodeString(v.Transaction[0])
	if e != nil || len(raw) < 130 || raw[0] != 2 || base58.Encode(raw[1:65]) != signature {
		return nil, commerceFail(422, "Solana 最终交易签名不一致")
	}
	if len(raw) < 133 || raw[132] > 127 || len(v.Meta.Pre) != int(raw[132]) || len(v.Meta.Post) != int(raw[132]) || len(v.Meta.Pre) == 0 || v.Meta.Pre[0] < v.Meta.Post[0] {
		return nil, commerceFail(422, "Solana 最终交易资金扣款凭据缺失或无效")
	}
	head, e := c.Finalized(ctx)
	if e != nil {
		return nil, e
	}
	if v.Slot > head.Number {
		return nil, nil
	}
	block, e := c.block(ctx, v.Slot)
	if e != nil {
		return nil, e
	}
	if uint64(*v.Time) != block.Timestamp {
		return nil, commerceFail(422, "Solana 最终交易区块时间不一致")
	}
	return &cryptoSolanaFinalProof{Signature: signature, BlockHash: block.Hash, Slot: v.Slot, Time: *v.Time, Failed: len(v.Meta.Err) > 0 && string(v.Meta.Err) != "null", Raw: raw, Fee: v.Meta.Fee, FundingBefore: v.Meta.Pre[0], FundingAfter: v.Meta.Post[0], BalanceEvidence: true}, nil
}
func (c *cryptoSolana) blockHeight(ctx context.Context) (uint64, error) {
	a, b, e := c.pair(ctx, "getBlockHeight", []any{object{"commitment": "finalized"}})
	if e != nil {
		return 0, e
	}
	var x, y uint64
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return 0, errors.New("Solana 区块高度无效")
	}
	return min(x, y), nil
}
func (c *cryptoSolana) Broadcast(ctx context.Context, raw []byte, signature string) error {
	var returned string
	e := c.rpc(ctx, c.chain.RPCURL, "sendTransaction", []any{base64.StdEncoding.EncodeToString(raw), object{"encoding": "base64", "skipPreflight": false, "preflightCommitment": "finalized", "maxRetries": 2}}, &returned)
	if e != nil {
		return e
	}
	if returned != signature {
		return commerceFail(422, "Solana 广播返回的签名与原签名不一致")
	}
	return nil
}
func (a *App) cryptoProcessSolanaTransfer(ctx context.Context, id, lease string, v cryptoSolanaStoredTx, quote cryptoSweepQuote, plan CryptoSweepPlanItem, c *cryptoSolana) error {
	if e := a.store.cryptoChainAvailable(a.store.db, cryptoSolanaChainID); e != nil {
		return e
	}
	if e := c.Chain(ctx); e != nil {
		return e
	}
	raw, e := a.store.cryptoValidateSolanaTx(v, quote, plan)
	if e != nil {
		return commerceFail(422, "Solana 持久化签名与批准计划不一致")
	}
	defer clear(raw)
	if v.State == "confirmed" {
		return nil
	}
	if v.State == "reverted" {
		return commerceFail(422, "Solana 交易已失败，需要人工核对")
	}
	proof, e := c.finalTransaction(ctx, v.Signature)
	if e != nil {
		return e
	}
	if proof == nil {
		if v.State == "expired_unknown" {
			return commerceFail(409, "Solana 交易结果未确认，持续核对原签名，不会重签或再次转账")
		}
		height, e := c.blockHeight(ctx)
		if e != nil {
			return e
		}
		if height > v.Height {
			tx, e := a.store.cryptoWalletTx(ctx)
			if e != nil {
				return e
			}
			defer tx.Rollback()
			if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
				return e
			}
			if _, e = tx.Exec("UPDATE crypto_solana_transactions SET state='expired_unknown',error='交易有效期已结束但执行结果未知，禁止换哈希重签',updated=? WHERE id=?", time.Now().Unix(), v.ID); e != nil {
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			return commerceFail(409, "Solana 交易已过期且结果未确认，请人工核对；不会自动重签或再次转账")
		}
		// Check our fenced lease and immutable signed bytes immediately before send.
		tx, e := a.store.cryptoWalletTx(ctx)
		if e != nil {
			return e
		}
		if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
			tx.Rollback()
			return e
		}
		current, e := scanCryptoSolanaTx(tx.QueryRow("SELECT "+cryptoSolanaTxColumns+" FROM crypto_solana_transactions WHERE id=?", v.ID))
		if e != nil || current.Signature != v.Signature || current.State == "confirmed" || current.State == "reverted" {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		if e = a.store.cryptoChainAvailable(a.store.db, cryptoSolanaChainID); e != nil {
			return e
		}
		if e = c.Broadcast(ctx, raw, v.Signature); e != nil {
			return e
		}
		_, e = a.store.db.ExecContext(ctx, "UPDATE crypto_solana_transactions SET state='pending',updated=? WHERE id=? AND state='signed'", time.Now().Unix(), v.ID)
		return e
	}
	if !bytes.Equal(proof.Raw, raw) {
		return commerceFail(422, "Solana 链上交易与原签名内容不一致")
	}
	approved, e := cryptoAtoms(plan.GasFeeAtoms)
	if e != nil || approved.Cmp(new(big.Int).SetUint64(proof.Fee)) < 0 {
		return commerceFail(422, "Solana 实际手续费超过批准额度")
	}
	rent, e := cryptoAtoms(plan.RentAtoms)
	if e != nil || !proof.BalanceEvidence || proof.FundingBefore < proof.FundingAfter {
		return commerceFail(422, "Solana 实际扣款无法核验")
	}
	cost := new(big.Int).SetUint64(proof.FundingBefore - proof.FundingAfter)
	if cost.Cmp(new(big.Int).SetUint64(proof.Fee)) < 0 || cost.Cmp(new(big.Int).Add(approved, rent)) > 0 {
		return commerceFail(422, "Solana 实际手续费及账户租金超过批准预算")
	}

	proof.TransactionID = v.ID
	proof.JobID = id
	proof.ItemID = v.ItemID
	sealed, e := a.store.vault.seal(proof)
	if e != nil {
		return e
	}
	state, itemState := "confirmed", "complete"
	if proof.Failed {
		state, itemState = "reverted", "failed"
	}
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE crypto_solana_transactions SET state=?,receipt=?,error='',updated=? WHERE id=?", state, sealed, time.Now().Unix(), v.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE crypto_sweep_items SET state=?,error='' WHERE id=?", itemState, v.ItemID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if proof.Failed {
		return commerceFail(422, "Solana 链上交易执行失败，不会重复转账")
	}
	return nil
}
func (a *App) cryptoSolanaSweepStep(ctx context.Context, id, lease string, quote cryptoSweepQuote) error {
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	if current, ok := settings.chain(cryptoSolanaChainID); ok && current.RPCURL != "" && current.RPCBackupURL != "" {
		quote.Chain.RPCURL = current.RPCURL
		quote.Chain.RPCBackupURL = current.RPCBackupURL
	}
	c, e := newCryptoSolana(quote.Chain, a.cfg.Dev)
	if e != nil {
		return e
	}
	var itemID, addressID, state string
	e = a.store.db.QueryRow("SELECT id,address_id,state FROM crypto_sweep_items WHERE job_id=? AND state<>'complete' ORDER BY id LIMIT 1", id).Scan(&itemID, &addressID, &state)
	if errors.Is(e, sql.ErrNoRows) {
		return a.cryptoCompleteSolanaSweep(ctx, id, lease, quote)
	}
	if e != nil {
		return e
	}
	if state == "failed" {
		return commerceFail(422, "Solana 任务含失败交易，需人工核对")
	}
	var plan CryptoSweepPlanItem
	for _, x := range quote.Preview.Items {
		if x.AddressID == addressID {
			plan = x
			break
		}
	}
	if plan.AddressID == "" {
		return commerceFail(422, "Solana 来源地址不在批准计划")
	}
	v, e := scanCryptoSolanaTx(a.store.db.QueryRow("SELECT "+cryptoSolanaTxColumns+" FROM crypto_solana_transactions WHERE item_id=?", itemID))
	if errors.Is(e, sql.ErrNoRows) {
		v, e = a.cryptoStageSolanaTransfer(ctx, id, lease, itemID, quote, plan, c)
	}
	if e != nil {
		return e
	}
	return a.cryptoProcessSolanaTransfer(ctx, id, lease, v, quote, plan, c)
}
func (a *App) cryptoCompleteSolanaSweep(ctx context.Context, id, lease string, quote cryptoSweepQuote) error {
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
		return e
	}
	if e = a.store.validateCryptoSolanaJob(tx, quote, id, "complete"); e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE crypto_sweep_jobs SET state='complete',error='',lease_token='',lease_until=0,updated=? WHERE id=? AND lease_token=?", time.Now().Unix(), id, lease)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) validateCryptoSolanaJob(tx *persistence.Tx, quote cryptoSweepQuote, id, state string) error {
	p := quote.Preview
	if quote.Chain.ChainID != cryptoSolanaChainID || p.NativeDecimals != 9 || p.FeeModel != "solana" || quote.Asset.Decimals != 6 && quote.Asset.Contract != "" || quote.Asset.Contract == "" && quote.Asset.Decimals != 9 || !cryptoSolanaAddress(p.Destination) || !cryptoSolanaAddress(p.FundingAddress) {
		return errCryptoSweepIntegrity
	}
	minimum, e := cryptoAtoms(p.MinAtoms)
	if e != nil {
		return errCryptoSweepIntegrity
	}
	budget, e := cryptoAtoms(p.MaxGasAtoms)
	if e != nil || budget.Sign() <= 0 {
		return errCryptoSweepIntegrity
	}
	sum, fees, rents := new(big.Int), new(big.Int), new(big.Int)
	plans := map[string]CryptoSweepPlanItem{}
	for _, plan := range p.Items {
		amount, e := cryptoAtoms(plan.AmountAtoms)
		fee, e2 := cryptoAtoms(plan.GasFeeAtoms)
		rent, e3 := cryptoAtoms(plan.RentAtoms)
		if e != nil || e2 != nil || e3 != nil || !amount.IsUint64() || amount.Sign() <= 0 || amount.Cmp(minimum) < 0 || fee.Sign() <= 0 || plan.TopupAtoms != "0" || plan.FundingGasAtoms != "0" || plan.FundingGasLimit != 0 || plan.GasLimit != 1 || plan.GasPriceAtoms != "0" || !cryptoIsSolanaPath(plan.Path) || plan.Address == p.Destination || plan.Address == p.FundingAddress {
			return errCryptoSweepIntegrity
		}
		if _, dup := plans[plan.AddressID]; dup {
			return errCryptoSweepIntegrity
		}
		plans[plan.AddressID] = plan
		sum.Add(sum, amount)
		fees.Add(fees, fee)
		rents.Add(rents, rent)
		if quote.Asset.Contract != "" {
			tokens := new(big.Int)
			seen := map[string]bool{}
			for _, account := range plan.SolanaSourceTokens {
				n, e := cryptoAtoms(account.Amount)
				if e != nil || !n.IsUint64() || n.Sign() <= 0 || !cryptoSolanaAddress(account.Address) || seen[account.Address] {
					return errCryptoSweepIntegrity
				}
				seen[account.Address] = true
				tokens.Add(tokens, n)
			}
			if tokens.Cmp(amount) != 0 || len(plan.SolanaSourceTokens) == 0 || len(plan.SolanaSourceTokens) > 20 {
				return errCryptoSweepIntegrity
			}
		} else if len(plan.SolanaSourceTokens) != 0 || rent.Sign() != 0 {
			return errCryptoSweepIntegrity
		}
	}
	required := new(big.Int).Add(fees, rents)
	if sum.String() != p.TotalAtoms || fees.String() != p.TotalGasAtoms || rents.String() != p.TotalRentAtoms || required.String() != p.RequiredFundingAtoms || required.Cmp(budget) > 0 || p.TotalTopupAtoms != "0" {
		return errCryptoSweepIntegrity
	}
	rows, e := tx.Query("SELECT id,address_id,address,path,amount_atoms,gas_needed_atoms,state FROM crypto_sweep_items WHERE job_id=?", id)
	if e != nil {
		return e
	}
	type item struct{ id, addressID, address, path, amount, gas, state string }
	items := []item{}
	for rows.Next() {
		var x item
		if e = rows.Scan(&x.id, &x.addressID, &x.address, &x.path, &x.amount, &x.gas, &x.state); e != nil {
			rows.Close()
			return e
		}
		items = append(items, x)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	if len(items) != len(plans) {
		return errCryptoSweepIntegrity
	}
	wallet := CryptoWallet{ID: p.WalletID}
	var xp, path string
	if e = tx.QueryRow("SELECT xpub,path FROM crypto_wallets WHERE id=?", wallet.ID).Scan(&xp, &path); e == nil {
		wallet.XPub = xp
		wallet.Path = path
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	for _, item := range items {
		plan, ok := plans[item.addressID]
		if !ok || plan.Address != item.address || plan.Path != item.path || plan.AmountAtoms != item.amount || item.gas != "0" {
			return errCryptoSweepIntegrity
		}
		delete(plans, item.addressID)
		var address, walletID, path string
		var index int64
		if e = tx.QueryRow("SELECT wallet_id,address_index,path,address FROM crypto_wallet_addresses WHERE id=?", item.addressID).Scan(&walletID, &index, &path, &address); e != nil {
			return e
		}
		owns, e := cryptoWalletSameDescriptor(tx, walletID, p.WalletID)
		if e != nil || !owns || address != item.address || path != item.path {
			return errCryptoSweepIntegrity
		}
		if wallet.XPub != "" {
			if e = s.cryptoSolanaAddressProof(tx, wallet, index, path, address); e != nil {
				return errCryptoSweepIntegrity
			}
		}
		v, e := scanCryptoSolanaTx(tx.QueryRow("SELECT "+cryptoSolanaTxColumns+" FROM crypto_solana_transactions WHERE item_id=?", item.id))
		if errors.Is(e, sql.ErrNoRows) {
			if item.state != "queued" || state == "complete" {
				return errCryptoSweepIntegrity
			}
			continue
		}
		if e != nil {
			return e
		}
		if v.JobID != id || v.ItemID != item.id {
			return errCryptoSweepIntegrity
		}
		raw, e := s.cryptoValidateSolanaTx(v, quote, plan)
		clear(raw)
		if e != nil {
			return e
		}
		switch item.state {
		case "sweep_pending":
			if v.State != "signed" && v.State != "pending" && v.State != "expired_unknown" {
				return errCryptoSweepIntegrity
			}
		case "complete":
			if v.State != "confirmed" {
				return errCryptoSweepIntegrity
			}
		case "failed":
			if v.State != "reverted" {
				return errCryptoSweepIntegrity
			}
		default:
			return errCryptoSweepIntegrity
		}
		if state == "complete" && item.state != "complete" || state == "queued" && item.state != "queued" || state == "cancelled" && v.State != "confirmed" && v.State != "reverted" {
			return errCryptoSweepIntegrity
		}
	}
	var wrong int
	if e = tx.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions WHERE job_id=?", id).Scan(&wrong); e != nil {
		return e
	}
	if wrong != 0 {
		return errCryptoSweepIntegrity
	}
	return nil
}
