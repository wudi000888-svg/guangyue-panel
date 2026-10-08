package controlplane

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type cryptoChainTransaction struct {
	ID, JobID, ItemID, Kind string
	ChainID                 int64
	From, To, Token, Amount string
	Nonce                   uint64
	Hash                    string
	Raw                     []byte
	GasLimit                uint64
	GasPrice, State         string
}

// Authenticated final receipt evidence prevents edited state/block columns from
// turning a merely signed transaction into an apparent confirmed transfer.
type CryptoSweepReceiptProof struct {
	TransactionID string           `json:"transaction_id"`
	JobID         string           `json:"job_id"`
	ItemID        string           `json:"item_id"`
	ChainID       int64            `json:"chain_id"`
	Receipt       cryptoEVMReceipt `json:"receipt"`
}

const cryptoTxColumns = "id,job_id,item_id,kind,chain_id,from_address,to_address,token,amount_atoms,nonce,tx_hash,raw,gas_limit,gas_price_atoms,state"

func scanCryptoTx(row cryptoScanner) (cryptoChainTransaction, error) {
	var v cryptoChainTransaction
	e := row.Scan(&v.ID, &v.JobID, &v.ItemID, &v.Kind, &v.ChainID, &v.From, &v.To, &v.Token, &v.Amount, &v.Nonce, &v.Hash, &v.Raw, &v.GasLimit, &v.GasPrice, &v.State)
	return v, e
}

func cryptoValidateSignedIntent(v cryptoChainTransaction) (*types.Transaction, error) {
	var tx types.Transaction
	if tx.UnmarshalBinary(v.Raw) != nil || tx.Type() != types.LegacyTxType || tx.Hash().Hex() != v.Hash || tx.ChainId().Cmp(big.NewInt(v.ChainID)) != 0 || tx.Nonce() != v.Nonce || tx.Gas() != v.GasLimit || tx.GasPrice().String() != v.GasPrice {
		return nil, commerceFail(422, "持久化签名交易校验失败")
	}
	from, e := types.Sender(types.NewEIP155Signer(big.NewInt(v.ChainID)), &tx)
	if e != nil || !strings.EqualFold(from.Hex(), v.From) {
		return nil, commerceFail(422, "签名来源地址不一致")
	}
	expectedTo := v.To
	if v.Token != "" {
		expectedTo = v.Token
	}
	if tx.To() == nil || !strings.EqualFold(tx.To().Hex(), expectedTo) {
		return nil, commerceFail(422, "签名交易目标不一致")
	}
	amount, e := cryptoAtoms(v.Amount)
	if e != nil {
		return nil, e
	}
	if v.Token == "" {
		if tx.Value().Cmp(amount) != 0 || len(tx.Data()) != 0 {
			return nil, commerceFail(422, "原生币签名金额不一致")
		}
	} else if tx.Value().Sign() != 0 || "0x"+hex.EncodeToString(tx.Data()) != cryptoTransferData(v.To, amount) {
		return nil, commerceFail(422, "代币交易签名参数不一致")
	}
	return &tx, nil
}
func cryptoTransactionMatchesPlan(v cryptoChainTransaction, quote cryptoSweepQuote, plan CryptoSweepPlanItem) error {
	if v.ChainID != quote.Chain.ChainID || v.GasPrice != plan.GasPriceAtoms {
		return commerceFail(422, "签名交易不符合原批准网络或手续费")
	}
	if v.Kind == "gas" {
		amount, e := cryptoAtoms(v.Amount)
		approved, x := cryptoAtoms(plan.TopupAtoms)
		if e != nil || x != nil || amount.Sign() <= 0 || amount.Cmp(approved) > 0 || v.GasLimit != plan.FundingGasLimit || v.Token != "" || !strings.EqualFold(v.From, quote.Preview.FundingAddress) || !strings.EqualFold(v.To, plan.Address) {
			return commerceFail(422, "Gas 交易超出原批准补差计划")
		}
	} else if v.Kind != "sweep" || v.Amount != plan.AmountAtoms || v.GasLimit != plan.GasLimit || !strings.EqualFold(v.From, plan.Address) || !strings.EqualFold(v.To, quote.Preview.Destination) || !strings.EqualFold(v.Token, quote.Asset.Contract) {
		return commerceFail(422, "提取交易不符合原批准目标或金额")
	}
	return nil
}
func (c *cryptoEVM) validateSignedReceipt(ctx context.Context, v cryptoChainTransaction, r *cryptoEVMReceipt) error {
	tx, e := cryptoValidateSignedIntent(v)
	if e != nil {
		return e
	}
	expectedTo := v.To
	if v.Token != "" {
		expectedTo = v.Token
	}
	if !strings.EqualFold(r.From, v.From) || !strings.EqualFold(r.To, expectedTo) || !strings.EqualFold(r.TxHash, v.Hash) {
		return commerceFail(422, "交易凭据的来源或目标不一致")
	}
	amount, _ := cryptoAtoms(v.Amount)
	// Independently inspect both nodes' canonical transaction, including nonce.
	a, b, e := c.pair(ctx, "eth_getTransactionByHash", []any{v.Hash})
	if e != nil {
		return e
	}
	for _, raw := range []json.RawMessage{a, b} {
		var x struct {
			Hash        string `json:"hash"`
			From        string `json:"from"`
			To          string `json:"to"`
			Nonce       string `json:"nonce"`
			Value       string `json:"value"`
			Input       string `json:"input"`
			BlockHash   string `json:"blockHash"`
			BlockNumber string `json:"blockNumber"`
			ChainID     string `json:"chainId"`
		}
		if json.Unmarshal(raw, &x) != nil {
			return errors.New("交易查询格式无效")
		}
		n, ne := cryptoHexU64(x.Nonce)
		value, ve := cryptoHexInt(x.Value)
		bn, be := cryptoHexU64(x.BlockNumber)
		cid, ce := cryptoHexU64(x.ChainID)
		if ne != nil || ve != nil || be != nil || ce != nil || cid != uint64(v.ChainID) || n != v.Nonce || value.Cmp(tx.Value()) != 0 || !strings.EqualFold(x.Hash, v.Hash) || !strings.EqualFold(x.From, v.From) || !strings.EqualFold(x.To, expectedTo) || !strings.EqualFold(x.Input, "0x"+hex.EncodeToString(tx.Data())) || !strings.EqualFold(x.BlockHash, r.BlockHash) || bn != r.BlockNumber {
			return commerceFail(422, "RPC 交易金额、nonce、链或规范区块与签名记录不一致")
		}
	}
	if r.Status == 1 && v.Token != "" {
		count := 0
		for _, log := range r.Logs {
			if log.Address == strings.ToLower(v.Token) && len(log.Topics) == 3 && log.Topics[0] == cryptoTransferTopic && log.Topics[1] == "0x"+cryptoABIAddress(v.From) && log.Topics[2] == "0x"+cryptoABIAddress(v.To) {
				n, e := cryptoHexInt(log.Data)
				if e != nil || n.Cmp(amount) != 0 {
					return commerceFail(422, "实际代币转出金额与批准金额不一致")
				}
				count++
			}
		}
		if count != 1 {
			return commerceFail(422, "最终 receipt 缺少唯一匹配的代币转账")
		}
	}
	return nil
}
func (a *App) cryptoSweepClaim(ctx context.Context) (string, string, error) {
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return "", "", e
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	var id string
	e = tx.QueryRow("SELECT id FROM crypto_sweep_jobs WHERE state IN ('queued','running','waiting') AND lease_until<=? ORDER BY updated,id LIMIT 1", now).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return "", "", nil
	}
	if e != nil {
		return "", "", e
	}
	token := randomToken(24)
	_, e = tx.Exec("UPDATE crypto_sweep_jobs SET state='running',lease_token=?,lease_until=?,updated=? WHERE id=?", token, now+120, now, id)
	if e != nil {
		return "", "", e
	}
	return id, token, tx.Commit()
}
func cryptoCheckSweepLease(tx *persistence.Tx, id, token string) error {
	var count int
	if e := tx.QueryRow("SELECT COUNT(*) FROM crypto_sweep_jobs WHERE id=? AND lease_token=? AND lease_until>? AND state='running'", id, token, time.Now().Unix()).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return errors.New("提取任务执行权已变更")
	}
	return nil
}
func (a *App) cryptoSweepFinish(ctx context.Context, id, token, state, message string) error {
	_, e := a.store.db.ExecContext(ctx, "UPDATE crypto_sweep_jobs SET state=?,error=?,lease_token='',lease_until=0,updated=? WHERE id=? AND lease_token=?", state, message, time.Now().Unix(), id, token)
	return e
}

func (a *App) cryptoStageTransfer(ctx context.Context, id, lease, itemID, kind string, quote cryptoSweepQuote, plan CryptoSweepPlanItem, c *cryptoEVM) (cryptoChainTransaction, error) {
	var out cryptoChainTransaction
	// All network observation occurs before taking the database transaction.
	block, e := c.Finalized(ctx)
	if e != nil {
		return out, e
	}
	latest, e := c.Latest(ctx)
	if e != nil {
		return out, e
	}
	price, e := c.gasPrice(ctx)
	if e != nil {
		return out, e
	}
	approvedPrice, e := cryptoAtoms(plan.GasPriceAtoms)
	if e != nil {
		return out, e
	}
	if price.Cmp(approvedPrice) > 0 {
		return out, commerceFail(409, "实时 Gas 价格超过已批准报价，任务保留原交易并等待价格恢复")
	}
	from, path, to, token := plan.Address, plan.Path, quote.Preview.Destination, quote.Asset.Contract
	amount, e := cryptoAtoms(plan.AmountAtoms)
	if e != nil {
		return out, e
	}
	gas := plan.GasLimit
	v, e := cryptoWalletByID(a.store.db, quote.Preview.WalletID)
	if e != nil {
		return out, e
	}
	if e = cryptoSweepWallet(v); e != nil {
		return out, e
	}
	funding, e := a.store.cryptoFundingAddress(ctx, v)
	if e != nil {
		return out, e
	}
	if kind == "gas" {
		balance, e := c.NativeBalance(ctx, plan.Address, latest.Number)
		if e != nil {
			return out, e
		}
		fee, e := cryptoAtoms(plan.GasFeeAtoms)
		if e != nil {
			return out, e
		}
		amount = new(big.Int).Sub(fee, balance)
		if amount.Sign() <= 0 {
			return out, nil
		}
		approved, e := cryptoAtoms(plan.TopupAtoms)
		if e != nil {
			return out, e
		}
		if amount.Cmp(approved) > 0 {
			return out, commerceFail(409, "所需 Gas 补差超过已批准额度，请核对地址余额")
		}
		from, path, to, token = funding.Address, funding.Path, plan.Address, ""
		gas = plan.FundingGasLimit
	} else {
		if token != "" {
			if e = c.ValidateToken(ctx, token, quote.Asset.Decimals, block.Number); e != nil {
				return out, e
			}
			balance, e := c.TokenBalance(ctx, token, from, block.Number)
			if e != nil {
				return out, e
			}
			if balance.Cmp(amount) < 0 {
				return out, commerceFail(409, "地址代币余额低于批准数量，已暂停而不增加转出金额")
			}
		}
	}
	data := ""
	txTo := to
	value := new(big.Int).Set(amount)
	if token != "" {
		data = cryptoTransferData(to, amount)
		txTo = token
		value.SetInt64(0)
	}
	estimate, e := c.estimateGas(ctx, from, txTo, data, value)
	if e != nil {
		return out, e
	}
	if estimate > gas || gas == 0 {
		return out, commerceFail(409, "实际 Gas 用量超过批准上限，任务等待核对")
	}
	fee := new(big.Int).Mul(new(big.Int).SetUint64(gas), approvedPrice)
	required := new(big.Int).Add(new(big.Int).Set(value), fee)
	balance, e := c.NativeBalance(ctx, from, latest.Number)
	if e != nil {
		return out, e
	}
	if balance.Cmp(required) < 0 {
		return out, commerceFail(409, "原生币余额不足，任务等待 Gas 确认；不会再次补入已签名交易")
	}
	nonce, e := c.PendingNonce(ctx, from)
	if e != nil {
		return out, e
	}
	tx, e := a.store.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = cryptoLockChain(tx, quote.Chain.ChainID); e != nil {
		return out, e
	}
	if _, e = tx.Exec("UPDATE crypto_wallet_state SET lock_version=lock_version+1 WHERE id=1"); e != nil {
		return out, e
	}
	if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
		return out, e
	}
	existing, e := scanCryptoTx(tx.QueryRow("SELECT "+cryptoTxColumns+" FROM crypto_chain_transactions WHERE job_id=? AND item_id=? AND kind=?", id, itemID, kind))
	if e == nil {
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	// Reserve every nonce in durable storage. An unknown broadcast is never
	// replaced by a new nonce; independent workers can only replay the same raw.
	var last int64
	if e = tx.QueryRow("SELECT COALESCE(MAX(nonce),-1) FROM crypto_chain_transactions WHERE chain_id=? AND lower(from_address)=lower(?)", quote.Chain.ChainID, from).Scan(&last); e != nil {
		return out, e
	}
	if last >= 0 && nonce <= uint64(last) {
		nonce = uint64(last) + 1
	}
	if nonce > 1<<63-1 {
		return out, errors.New("nonce 索引超出范围")
	}
	// Pending outgoing transactions reserve both value and their maximum fee.
	rows, e := tx.Query("SELECT amount_atoms,gas_limit,gas_price_atoms,token FROM crypto_chain_transactions WHERE chain_id=? AND lower(from_address)=lower(?) AND state NOT IN ('confirmed','reverted')", quote.Chain.ChainID, from)
	if e != nil {
		return out, e
	}
	reserved := new(big.Int)
	for rows.Next() {
		var atoms, price, tok string
		var gas uint64
		if e = rows.Scan(&atoms, &gas, &price, &tok); e != nil {
			rows.Close()
			return out, e
		}
		p, pe := cryptoAtoms(price)
		n, ne := cryptoAtoms(atoms)
		if pe != nil || ne != nil {
			rows.Close()
			return out, errors.New("交易保留金额无效")
		}
		reserved.Add(reserved, new(big.Int).Mul(new(big.Int).SetUint64(gas), p))
		if tok == "" {
			reserved.Add(reserved, n)
		}
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return out, e
	}
	if new(big.Int).Add(required, reserved).Cmp(balance) > 0 {
		return out, commerceFail(409, "Gas 余额已为其他待确认交易保留，请等待原交易确认")
	}
	current, e := cryptoWalletByID(tx, v.ID)
	if e != nil {
		return out, e
	}
	if e = cryptoSweepWallet(current); e != nil {
		return out, e
	}
	secret, e := a.store.cryptoSecret(tx, current)
	if e != nil {
		return out, e
	}
	bytesData, e := hex.DecodeString(strings.TrimPrefix(data, "0x"))
	if e != nil {
		return out, e
	}
	signed, e := cryptoSignTransfer(secret.Mnemonic, path, quote.Chain.ChainID, nonce, gas, approvedPrice, value, txTo, bytesData)
	secret.Mnemonic = ""
	if e != nil {
		return out, e
	}
	if !strings.EqualFold(signed.From, from) {
		return out, errors.New("签名路径与持久来源地址不一致")
	}
	raw, e := hex.DecodeString(strings.TrimPrefix(signed.Raw, "0x"))
	if e != nil {
		return out, e
	}
	now := time.Now().Unix()
	out = cryptoChainTransaction{ID: serial("GYT"), JobID: id, ItemID: itemID, Kind: kind, ChainID: quote.Chain.ChainID, From: from, To: to, Token: token, Amount: amount.String(), Nonce: nonce, Hash: signed.Hash, Raw: raw, GasLimit: gas, GasPrice: approvedPrice.String(), State: "signed"}
	_, e = tx.Exec("INSERT INTO crypto_chain_transactions("+cryptoTxColumns+",created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", out.ID, out.JobID, out.ItemID, out.Kind, out.ChainID, out.From, out.To, out.Token, out.Amount, out.Nonce, out.Hash, out.Raw, out.GasLimit, out.GasPrice, out.State, now, now)
	if e != nil {
		return out, e
	}
	if _, e = tx.Exec("UPDATE crypto_sweep_items SET state=?,error='' WHERE id=?", kind+"_pending", itemID); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (a *App) cryptoProcessTransaction(ctx context.Context, id, lease string, v cryptoChainTransaction, c *cryptoEVM) (bool, error) {
	if _, e := cryptoValidateSignedIntent(v); e != nil {
		return false, e
	}
	if e := a.store.cryptoChainAvailable(a.store.db, v.ChainID); e != nil {
		return false, e
	}
	if v.State == "confirmed" || v.State == "reverted" {
		if e := a.store.cryptoCheckStoredSweepReceipt(v); e != nil {
			return false, commerceFail(422, "已结算交易缺少有效最终凭据，已停止提取")
		}
		if v.State == "confirmed" {
			return true, nil
		}
		return false, commerceFail(422, "交易已执行失败，不会自动创建新的资金交易")
	}
	r, e := c.FinalReceipt(ctx, v.Hash)
	if e != nil {
		if errors.Is(e, errCryptoFinality) {
			_ = a.store.freezeCryptoChain(v.ChainID)
			return false, commerceFail(422, errCryptoFinality.Error())
		}
		return false, e
	}
	if r == nil {
		message := "等待链最终确认"
		if e = a.store.cryptoChainAvailable(a.store.db, v.ChainID); e != nil {
			return false, e
		}
		if e = c.Broadcast(ctx, "0x"+hex.EncodeToString(v.Raw), v.Hash); e != nil {
			message = e.Error()
		}
		tx, e := a.store.cryptoWalletTx(ctx)
		if e != nil {
			return false, e
		}
		defer tx.Rollback()
		if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
			return false, e
		}
		if _, e = tx.Exec("UPDATE crypto_chain_transactions SET state='pending',error=?,updated=? WHERE id=?", message, time.Now().Unix(), v.ID); e != nil {
			return false, e
		}
		return false, tx.Commit()
	}
	if e = c.validateSignedReceipt(ctx, v, r); e != nil {
		var ce commerceError
		if errors.As(e, &ce) && ce.status == 422 {
			_ = a.store.freezeCryptoChain(v.ChainID)
		}
		return false, e
	}
	state := "confirmed"
	if r.Status != 1 {
		state = "reverted"
	}
	proof, e := a.store.vault.seal(CryptoSweepReceiptProof{TransactionID: v.ID, JobID: v.JobID, ItemID: v.ItemID, ChainID: v.ChainID, Receipt: *r})
	if e != nil {
		return false, e
	}
	tx, e := a.store.cryptoWalletTx(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if e = cryptoCheckSweepLease(tx, id, lease); e != nil {
		return false, e
	}
	if _, e = tx.Exec("UPDATE crypto_chain_transactions SET state=?,error='',block_number=?,block_hash=?,receipt=?,updated=? WHERE id=?", state, r.BlockNumber, r.BlockHash, proof, time.Now().Unix(), v.ID); e != nil {
		return false, e
	}
	if state == "reverted" {
		if _, e = tx.Exec("UPDATE crypto_sweep_items SET state='failed',error='链上交易执行失败，未再次补 Gas 或转账' WHERE id=?", v.ItemID); e != nil {
			return false, e
		}
	} else if v.Kind == "sweep" {
		if _, e = tx.Exec("UPDATE crypto_sweep_items SET state='complete',error='' WHERE id=?", v.ItemID); e != nil {
			return false, e
		}
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	if state == "reverted" {
		return false, commerceFail(422, "链上交易已失败，任务保留凭据，需要人工核对")
	}
	return true, nil
}
func (a *App) cryptoSweepStep(ctx context.Context, id, lease string) error {
	var encrypted []byte
	if e := a.store.db.QueryRow("SELECT config FROM crypto_sweep_jobs WHERE id=?", id).Scan(&encrypted); e != nil {
		return e
	}
	var quote cryptoSweepQuote
	if e := a.store.vault.open(encrypted, &quote); e != nil {
		return errors.New("提取任务配置解密失败")
	}
	if e := a.store.cryptoChainAvailable(a.store.db, quote.Chain.ChainID); e != nil {
		return e
	}
	if e := cryptoSweepAllowed(quote.Chain); e != nil {
		return e
	}
	var itemCount int
	if e := a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_sweep_items WHERE job_id=?", id).Scan(&itemCount); e != nil {
		return e
	}
	if itemCount != len(quote.Preview.Items) || itemCount == 0 {
		return commerceFail(422, "提取任务明细与原批准计划不完整，已停止提取")
	}
	// A repaired RPC configuration may resume the same immutable signed tasks.
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	if current, ok := settings.chain(quote.Chain.ChainID); ok && current.RPCURL != "" && current.RPCBackupURL != "" {
		quote.Chain.RPCURL, quote.Chain.RPCBackupURL = current.RPCURL, current.RPCBackupURL
	}
	c, e := newCryptoEVM(quote.Chain, a.cfg.Dev)
	if e != nil {
		return e
	}
	var itemID, addressID, state string
	e = a.store.db.QueryRow("SELECT i.id,i.address_id,i.state FROM crypto_sweep_items i WHERE i.job_id=? AND i.state<>'complete' ORDER BY COALESCE((SELECT MAX(updated) FROM crypto_chain_transactions t WHERE t.item_id=i.id),0),i.id LIMIT 1", id).Scan(&itemID, &addressID, &state)
	if errors.Is(e, sql.ErrNoRows) {
		return a.cryptoCompleteSweep(ctx, id, lease, quote)
	}
	if e != nil {
		return e
	}
	if state == "failed" {
		return commerceFail(422, "任务含失败交易，需要人工核对")
	}
	var plan CryptoSweepPlanItem
	for _, x := range quote.Preview.Items {
		if x.AddressID == addressID {
			plan = x
			break
		}
	}
	if plan.AddressID == "" {
		return errors.New("任务来源地址不在批准预览中")
	}
	process := func(v cryptoChainTransaction) (bool, error) {
		if e := cryptoTransactionMatchesPlan(v, quote, plan); e != nil {
			return false, e
		}
		return a.cryptoProcessTransaction(ctx, id, lease, v, c)
	}
	sweep, e := scanCryptoTx(a.store.db.QueryRow("SELECT "+cryptoTxColumns+" FROM crypto_chain_transactions WHERE item_id=? AND kind='sweep'", itemID))
	if e == nil {
		_, e = process(sweep)
		return e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	gas, e := scanCryptoTx(a.store.db.QueryRow("SELECT "+cryptoTxColumns+" FROM crypto_chain_transactions WHERE item_id=? AND kind='gas'", itemID))
	if e == nil {
		confirmed, e := process(gas)
		if e != nil {
			return e
		}
		if !confirmed {
			return nil
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	} else if plan.TopupAtoms != "0" {
		gas, e = a.cryptoStageTransfer(ctx, id, lease, itemID, "gas", quote, plan, c)
		if e != nil {
			return e
		}
		if gas.ID != "" {
			_, e = process(gas)
			return e
		}
	}
	sweep, e = a.cryptoStageTransfer(ctx, id, lease, itemID, "sweep", quote, plan, c)
	if e != nil {
		return e
	}
	_, e = process(sweep)
	return e
}
func (a *App) cryptoSweepWork(ctx context.Context) error {
	if a.cfg.businessAgent() {
		return nil
	}
	if a.store.commerceErr != nil {
		return errors.New("资金完整性校验失败")
	}
	id, lease, e := a.cryptoSweepClaim(ctx)
	if e != nil || id == "" {
		return e
	}
	work, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	e = a.cryptoSweepStep(work, id, lease)
	state, message := "waiting", ""
	if e != nil {
		message = e.Error()
		var ce commerceError
		if errors.As(e, &ce) && ce.status == 422 {
			state = "failed"
		}
	}
	// Release only our own fenced lease. A completed step has already cleared it.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if end := a.cryptoSweepFinish(finishCtx, id, lease, state, message); e == nil {
		return end
	}
	return e
}
func (a *App) runCryptoSweepWorker(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = a.cryptoSweepWork(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) cryptoCheckStoredSweepReceipt(v cryptoChainTransaction) error {
	var block uint64
	var hash string
	var sealed []byte
	if e := s.db.QueryRow("SELECT block_number,block_hash,receipt FROM crypto_chain_transactions WHERE id=?", v.ID).Scan(&block, &hash, &sealed); e != nil {
		return e
	}
	return s.validateCryptoSweepReceipt(v, block, hash, sealed)
}
func (a *App) cryptoCompleteSweep(ctx context.Context, id, lease string, quote cryptoSweepQuote) error {
	// Completion requires each originally approved item and its authenticated
	// final transaction proof. Empty or edited detail rows cannot finish a job.
	rows, e := a.store.db.Query("SELECT id,address_id FROM crypto_sweep_items WHERE job_id=? AND state='complete'", id)
	if e != nil {
		return e
	}
	items := map[string]string{}
	for rows.Next() {
		var item, address string
		if e = rows.Scan(&item, &address); e != nil {
			rows.Close()
			return e
		}
		items[item] = address
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	if len(items) != len(quote.Preview.Items) {
		return commerceFail(422, "提取任务尚缺少已完成明细")
	}
	plans := map[string]CryptoSweepPlanItem{}
	for _, plan := range quote.Preview.Items {
		plans[plan.AddressID] = plan
	}
	for item, address := range items {
		plan, ok := plans[address]
		if !ok {
			return commerceFail(422, "提取完成明细不在批准计划内")
		}
		delete(plans, address)
		v, e := scanCryptoTx(a.store.db.QueryRow("SELECT "+cryptoTxColumns+" FROM crypto_chain_transactions WHERE job_id=? AND item_id=? AND kind='sweep'", id, item))
		if e != nil {
			return commerceFail(422, "提取完成明细缺少签名交易")
		}
		if v.State != "confirmed" {
			return commerceFail(422, "提取交易尚未完成最终确认")
		}
		if _, e = cryptoValidateSignedIntent(v); e != nil {
			return e
		}
		if e = cryptoTransactionMatchesPlan(v, quote, plan); e != nil {
			return e
		}
		if e = a.store.cryptoCheckStoredSweepReceipt(v); e != nil {
			return commerceFail(422, "提取完成凭据无效，已停止完成任务")
		}
	}
	return a.cryptoSweepFinish(ctx, id, lease, "complete", "")
}
