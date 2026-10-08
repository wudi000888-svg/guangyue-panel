package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type CryptoTransfer struct {
	ChainID     int64  `json:"chain_id"`
	TxHash      string `json:"tx_hash"`
	LogIndex    int64  `json:"log_index"`
	Contract    string `json:"contract"`
	Atoms       string `json:"atoms"`
	BlockNumber int64  `json:"block_number"`
	BlockTime   int64  `json:"block_time"`
	State       string `json:"state"`
	Reason      string `json:"reason"`
}

type CryptoInvoice struct {
	Transfers       []CryptoTransfer      `json:"transfers,omitempty"`
	ID              string                `json:"id"`
	OrderID         string                `json:"order_id"`
	AttemptID       string                `json:"attempt_id"`
	ChainID         int64                 `json:"chain_id"`
	ChainName       string                `json:"chain_name"`
	AssetID         string                `json:"asset_id"`
	Symbol          string                `json:"symbol"`
	AssetName       string                `json:"asset_name"`
	Contract        string                `json:"contract"`
	Decimals        int                   `json:"decimals"`
	PaymentDecimals int                   `json:"payment_decimals"`
	Address         string                `json:"address"`
	ExpectedAtoms   string                `json:"expected_atoms"`
	ReceivedAtoms   string                `json:"received_atoms"`
	ConfirmedAtoms  string                `json:"confirmed_atoms"`
	RemainingAtoms  string                `json:"remaining_atoms"`
	OverpaidAtoms   string                `json:"overpaid_atoms"`
	State           string                `json:"state"`
	Message         string                `json:"message"`
	Created         int64                 `json:"created"`
	Expires         int64                 `json:"expires"`
	RateSource      string                `json:"rate_source"`
	CNYPerToken     string                `json:"cny_per_token"`
	RateUpdatedAt   int64                 `json:"rate_updated_at"`
	RateExpiresAt   int64                 `json:"rate_expires_at"`
	QRURI           string                `json:"qr_uri"`
	OrderState      string                `json:"order_state"`
	ReceiptState    string                `json:"receipt_state"`
	ScanError       string                `json:"scan_error"`
	LastScan        int64                 `json:"last_scan"`
	FinalizedBlock  int64                 `json:"finalized_block"`
	UserID          int64                 `json:"-"`
	WalletID        string                `json:"-"`
	AddressID       string                `json:"-"`
	StartBlock      int64                 `json:"-"`
	NextBlock       int64                 `json:"-"`
	Snapshot        cryptoInvoiceSnapshot `json:"-"`
}

const cryptoInvoiceColumns = "id,order_id,user_id,attempt_id,wallet_id,address_id,chain_id,asset_id,address,contract,expected_atoms,state,created,expires,start_block,next_block,last_scan,finalized_block,scan_error,doc"

func (s *Store) scanCryptoInvoice(row cryptoScanner) (CryptoInvoice, error) {
	var v CryptoInvoice
	var body []byte
	e := row.Scan(&v.ID, &v.OrderID, &v.UserID, &v.AttemptID, &v.WalletID, &v.AddressID, &v.ChainID, &v.AssetID, &v.Address, &v.Contract, &v.ExpectedAtoms, &v.State, &v.Created, &v.Expires, &v.StartBlock, &v.NextBlock, &v.LastScan, &v.FinalizedBlock, &v.ScanError, &body)
	if e != nil {
		return v, e
	}
	e = s.vault.open(body, &v.Snapshot)
	if e != nil {
		return v, e
	}
	c, p := v.Snapshot.Chain, v.Snapshot.Asset
	v.ChainName = c.Name
	v.Symbol = p.Symbol
	v.AssetName = p.Name
	v.Decimals = p.Decimals
	v.PaymentDecimals = p.PaymentDecimals
	v.CNYPerToken = p.CNYPerToken
	v.RateUpdatedAt = p.RateUpdatedAt
	v.RateExpiresAt = p.RateExpiresAt
	v.RateSource = "管理员手动汇率"
	v.QRURI = "ethereum:" + v.Contract + "@" + strconv.FormatInt(v.ChainID, 10) + "/transfer?address=" + v.Address + "&uint256=" + v.ExpectedAtoms
	return v, nil
}
func (s *Store) cryptoInvoice(q cryptoWalletQuery, id string) (CryptoInvoice, error) {
	return s.scanCryptoInvoice(q.QueryRow("SELECT "+cryptoInvoiceColumns+" FROM crypto_invoices WHERE id=?", id))
}
func (s *Store) cryptoInvoiceAmounts(q cryptoWalletQuery, v *CryptoInvoice) error {
	rows, e := q.Query("SELECT atoms,state,block_time,chain_id,contract FROM crypto_transfers WHERE invoice_id=? UNION ALL SELECT atoms,'observed',block_time,chain_id,contract FROM crypto_transfer_observations WHERE invoice_id=?", v.ID, v.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	received, confirmed := new(big.Int), new(big.Int)
	for rows.Next() {
		var atoms, state, contract string
		var timestamp, chain int64
		if e = rows.Scan(&atoms, &state, &timestamp, &chain, &contract); e != nil {
			return e
		}
		n, e := cryptoAtoms(atoms)
		if e != nil {
			return e
		}
		if chain != v.ChainID || contract != v.Contract || state == "orphaned" {
			continue
		}
		received.Add(received, n)
		if state == "confirmed" && timestamp < v.Expires {
			confirmed.Add(confirmed, n)
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	expected, e := cryptoAtoms(v.ExpectedAtoms)
	if e != nil {
		return e
	}
	remaining := new(big.Int).Sub(expected, confirmed)
	if remaining.Sign() < 0 {
		remaining.SetInt64(0)
	}
	over := new(big.Int).Sub(confirmed, expected)
	if over.Sign() < 0 {
		over.SetInt64(0)
	}
	v.ReceivedAtoms, v.ConfirmedAtoms, v.RemainingAtoms, v.OverpaidAtoms = received.String(), confirmed.String(), remaining.String(), over.String()
	return nil
}
func (s *Store) decorateCryptoInvoice(v CryptoInvoice) (CryptoInvoice, error) {
	if e := s.cryptoInvoiceAmounts(s.db, &v); e != nil {
		return v, e
	}
	o, e := s.order(v.OrderID)
	if e != nil {
		return v, e
	}
	v.OrderState = o.State
	e = s.db.QueryRow("SELECT state FROM payment_receipts WHERE attempt_id=? ORDER BY created LIMIT 1", v.AttemptID).Scan(&v.ReceiptState)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	if v.State == "waiting" {
		if v.ReceivedAtoms != "0" {
			v.State = "confirming"
		}
		if v.ConfirmedAtoms != "0" && v.ReceivedAtoms == v.ConfirmedAtoms {
			v.State = "partial"
		}
		if time.Now().Unix() > v.Expires && v.ReceivedAtoms == "0" {
			v.State = "expired"
		}
	}
	if v.State == "partial" {
		v.QRURI = "ethereum:" + v.Contract + "@" + strconv.FormatInt(v.ChainID, 10) + "/transfer?address=" + v.Address + "&uint256=" + v.RemainingAtoms
	}
	switch v.State {
	case "waiting":
		v.Message = "等待付款，请核对网络和应到账数量"
	case "confirming":
		v.Message = "已发现转账，正在等待网络最终确认"
	case "partial":
		v.Message = "已收到部分款项，请在截止前补足"
	case "paid":
		v.Message = "链上付款已最终确认，正在处理套餐"
	case "expired":
		v.Message = "付款时间已结束，请勿继续转账；已上链款项仍会核对"
	case "superseded":
		v.Message = "已切换付款方式，请勿向旧地址继续付款"
	case "review_required":
		v.Message = "已记录链上款项，请联系管理员核对"
	}
	if v.OrderState == "completed" && v.ReceiptState == "applied" {
		v.Message = "付款已确认，套餐已开通"
	}
	return v, nil
}
func (a *App) getCryptoInvoices(w http.ResponseWriter, r *http.Request, actor Record, path string) error {
	if strings.HasPrefix(path, "invoices/") {
		id := strings.TrimPrefix(path, "invoices/")
		v, e := a.store.cryptoInvoice(a.store.db, id)
		if e != nil {
			return commerceFail(404, "付款单不存在")
		}
		if actor.Role != "owner" && v.UserID != actor.ID {
			return commerceFail(404, "付款单不存在")
		}
		v, e = a.store.decorateCryptoInvoice(v)
		if e != nil {
			return e
		}
		v.Transfers, e = a.store.cryptoInvoiceTransfers(v.ID)
		if e != nil {
			return e
		}
		jsonResponse(w, 200, v)
		return nil
	}
	query := "SELECT " + cryptoInvoiceColumns + " FROM crypto_invoices"
	args := []any{}
	if path != "admin/invoices" {
		query += " WHERE order_id=?"
		args = append(args, r.URL.Query().Get("order_id"))
		if actor.Role != "owner" {
			query += " AND user_id=?"
			args = append(args, actor.ID)
		}
	}
	query += " ORDER BY created DESC,id DESC LIMIT 200"
	rows, e := a.store.db.Query(query, args...)
	if e != nil {
		return e
	}
	items := []CryptoInvoice{}
	for rows.Next() {
		v, e := a.store.scanCryptoInvoice(rows)
		if e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for i := range items {
		items[i], e = a.store.decorateCryptoInvoice(items[i])
		if e != nil {
			return e
		}
	}
	jsonResponse(w, 200, object{"items": items})
	return nil
}

type cryptoInvoiceInput struct {
	OrderID     string `json:"order_id"`
	AssetID     string `json:"asset_id"`
	OperationID string `json:"operation_id"`
}

func (s *Store) cryptoInvoiceReplay(q cryptoWalletQuery, actor int64, in cryptoInvoiceInput) (CryptoInvoice, bool, error) {
	var id, hash string
	var user int64
	e := q.QueryRow("SELECT id,user_id,fingerprint FROM crypto_invoices WHERE operation_id=?", in.OperationID).Scan(&id, &user, &hash)
	if errors.Is(e, sql.ErrNoRows) {
		return CryptoInvoice{}, false, nil
	}
	if e != nil {
		return CryptoInvoice{}, false, e
	}
	if actor != user || hash != digest(string(jsonBytes(in))) {
		return CryptoInvoice{}, false, commerceFail(409, "操作标识已用于其他请求")
	}
	v, e := s.cryptoInvoice(q, id)
	return v, true, e
}
func (a *App) createCryptoInvoice(w http.ResponseWriter, r *http.Request, actor Record) error {
	var in cryptoInvoiceInput
	if !decode(w, r, &in) {
		return nil
	}
	if !operationPattern.MatchString(in.OperationID) {
		return commerceFail(400, "缺少有效操作标识")
	}
	if v, found, e := a.store.cryptoInvoiceReplay(a.store.db, actor.ID, in); e != nil {
		return e
	} else if found {
		v, e = a.store.decorateCryptoInvoice(v)
		if e != nil {
			return e
		}
		jsonResponse(w, 200, v)
		return nil
	}
	if a.cfg.businessAgent() {
		return commerceFail(409, "此节点角色不提供独立收款服务")
	}
	existingOrder, e := a.store.order(in.OrderID)
	if e != nil || existingOrder.UserID != actor.ID || existingOrder.State != "pending" || existingOrder.Expires <= time.Now().Unix() || existingOrder.Offer.Price <= 0 {
		return commerceFail(409, "订单不存在、已过期或无需付款")
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	asset, ok := settings.asset(in.AssetID)
	chain, cok := settings.chain(asset.ChainID)
	now := time.Now().Unix()
	if !settings.Enabled || !ok || !cok || !asset.Enabled {
		return commerceFail(409, "加密货币支付尚未开放")
	}
	if reason := cryptoChainReason(chain); reason != "" {
		return commerceFail(409, reason)
	}
	if asset.RateExpiresAt <= now {
		return commerceFail(409, "手动汇率已过期，请管理员更新")
	}
	if e = a.store.cryptoChainAvailable(a.store.db, chain.ChainID); e != nil {
		return e
	}
	// Validate both RPCs and finalized token metadata before reserving any address.
	client, e := newCryptoEVM(chain, a.cfg.Dev)
	if e != nil {
		return commerceFail(409, e.Error())
	}
	head, e := client.Finalized(r.Context())
	if e != nil {
		return commerceFail(409, e.Error())
	}
	if head.Number > 1<<63-2 || head.Timestamp > uint64(time.Now().Unix()+120) {
		return commerceFail(409, "RPC 最终区块信息无效")
	}
	if e = client.ValidateToken(r.Context(), asset.Contract, asset.Decimals, head.Number); e != nil {
		return commerceFail(409, e.Error())
	}
	tx, e := a.store.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = cryptoLockChain(tx, chain.ChainID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE crypto_wallet_state SET lock_version=lock_version+1 WHERE id=1"); e != nil {
		return e
	}
	if v, found, e := a.store.cryptoInvoiceReplay(tx, actor.ID, in); e != nil {
		return e
	} else if found {
		if e = tx.Commit(); e != nil {
			return e
		}
		v, e = a.store.decorateCryptoInvoice(v)
		if e != nil {
			return e
		}
		jsonResponse(w, 200, v)
		return nil
	}
	if e = cryptoLockChain(tx, chain.ChainID); e != nil {
		return e
	}
	var revision int64
	if e = tx.QueryRow("SELECT revision FROM crypto_payment_settings WHERE id=1").Scan(&revision); e != nil {
		return e
	}
	if asset.RateExpiresAt <= time.Now().Unix() {
		return commerceFail(409, "手动汇率已过期，请管理员更新")
	}
	if revision != settings.Revision {
		return commerceFail(409, "支付设置已变化，请重试")
	}
	result, e := tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=? AND user_id=? AND state='pending'", in.OrderID, actor.ID)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return commerceFail(409, "订单不存在、已结束或无权付款")
	}
	var raw []byte
	var order Order
	if e = tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", in.OrderID).Scan(&raw); e != nil {
		return e
	}
	if e = json.Unmarshal(raw, &order); e != nil {
		return e
	}
	if order.Offer.Price <= 0 || order.Expires <= now {
		return commerceFail(409, "订单已过期或无需付款")
	}
	var invoiceCount int
	if e = tx.QueryRow("SELECT COUNT(*) FROM crypto_invoices WHERE order_id=?", order.ID).Scan(&invoiceCount); e != nil {
		return e
	}
	if invoiceCount >= 12 {
		return commerceFail(409, "该订单付款方式切换次数已达上限，请处理现有付款单")
	}
	if order.PaymentAttemptID != "" {
		var existingAsset string
		if e = tx.QueryRow("SELECT asset_id FROM crypto_invoices WHERE attempt_id=?", order.PaymentAttemptID).Scan(&existingAsset); e == nil && existingAsset == in.AssetID {
			return commerceFail(409, "该资产已有付款单，请使用已生成的收款地址")
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if order.PaymentProviderID != "crypto" {
			return commerceFail(409, "订单已选择其他支付方式，请先处理原付款")
		}
		var count int
		if e = tx.QueryRow("SELECT (SELECT COUNT(*) FROM crypto_transfers t JOIN crypto_invoices i ON i.id=t.invoice_id WHERE i.order_id=?)+(SELECT COUNT(*) FROM crypto_transfer_observations t JOIN crypto_invoices i ON i.id=t.invoice_id WHERE i.order_id=?)", order.ID, order.ID).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return commerceFail(409, "原付款地址已发现转账，请等待确认或联系管理员")
		}
		var state string
		if e = tx.QueryRow("SELECT state FROM payment_attempts WHERE id=?", order.PaymentAttemptID).Scan(&state); e != nil {
			return e
		}
		if state != "awaiting_customer" {
			return commerceFail(409, "原支付已进入处理，请刷新订单")
		}
		if _, e = tx.Exec("UPDATE crypto_invoices SET state='superseded' WHERE attempt_id=? AND state='waiting'", order.PaymentAttemptID); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE payment_attempts SET state='cancelled',message='已切换加密货币付款方式',updated=? WHERE id=? AND state='awaiting_customer'", now, order.PaymentAttemptID); e != nil {
			return e
		}
	} else {
		order.Expires = now + int64(settings.InvoiceMinutes)*60
	}
	wallet, e := cryptoWalletByID(tx, settings.WalletID)
	if e != nil {
		return e
	}
	if e = cryptoWalletReady(wallet); e != nil {
		return e
	}
	atoms, e := cryptoQuote(order.Offer.Price, asset)
	if e != nil {
		return e
	}
	address, e := cryptoDeriveAddress(wallet.XPub, wallet.Path, uint32(wallet.NextIndex))
	if e != nil {
		return e
	}
	allocation := CryptoWalletAddress{ID: serial("GYA"), WalletID: wallet.ID, Index: wallet.NextIndex, Path: cryptoFullPath(wallet.Path, wallet.NextIndex), Address: address, Label: "订单 " + order.ID, Created: now}
	if _, e = tx.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", allocation.ID, allocation.WalletID, allocation.Index, allocation.Path, allocation.Address, allocation.Label, now); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE crypto_wallets SET next_index=next_index+1,revision=revision+1 WHERE id=?", wallet.ID); e != nil {
		return e
	}
	attempt := PaymentAttempt{ID: serial("GYP"), OrderID: order.ID, UserID: actor.ID, ProviderID: "crypto", State: "awaiting_customer", Amount: order.Offer.Price, Currency: "CNY", IdempotencyKey: in.OperationID, Purpose: "order", Created: now, Updated: now, Expires: order.Expires, AccountScope: "evm_crypto"}
	attempt.MerchantRef = attempt.ID
	doc, e := a.store.vault.seal(paymentProviderDoc{})
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO payment_providers(id,code,name,enabled,version,doc,created,updated) VALUES('crypto','evm_crypto','加密货币',0,1,?,?,?) ON CONFLICT(id) DO NOTHING", doc, now, now); e != nil {
		return e
	}
	doc, e = a.store.vault.seal(paymentAttemptDoc{Code: "evm_crypto", Provider: paymentProviderDoc{AccountScope: "evm_crypto"}, Fingerprint: digest(string(jsonBytes(in)))})
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO payment_attempts(id,order_id,user_id,provider_id,state,amount,currency,merchant_ref,idempotency_key,created,updated,expires,doc,purpose,account_scope) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", attempt.ID, attempt.OrderID, attempt.UserID, attempt.ProviderID, attempt.State, attempt.Amount, attempt.Currency, attempt.MerchantRef, attempt.IdempotencyKey, now, now, attempt.Expires, doc, attempt.Purpose, attempt.AccountScope); e != nil {
		return e
	}
	v := CryptoInvoice{ID: serial("GYI"), OrderID: order.ID, UserID: actor.ID, AttemptID: attempt.ID, WalletID: wallet.ID, AddressID: allocation.ID, ChainID: chain.ChainID, AssetID: asset.ID, Address: address, Contract: asset.Contract, ExpectedAtoms: atoms, State: "waiting", Created: now, Expires: order.Expires, StartBlock: int64(head.Number), NextBlock: int64(head.Number), Snapshot: cryptoInvoiceSnapshot{Chain: chain, Asset: asset, Amount: order.Offer.Price}}
	doc, e = a.store.vault.seal(v.Snapshot)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO crypto_invoices("+cryptoInvoiceColumns+",operation_id,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.ID, v.OrderID, v.UserID, v.AttemptID, v.WalletID, v.AddressID, v.ChainID, v.AssetID, v.Address, v.Contract, v.ExpectedAtoms, v.State, v.Created, v.Expires, v.StartBlock, v.NextBlock, 0, 0, "", doc, in.OperationID, digest(string(jsonBytes(in)))); e != nil {
		return e
	}
	order.PaymentAttemptID = attempt.ID
	order.PaymentProviderID = "crypto"
	order.Message = "等待加密货币付款"
	if e = saveOrder(tx, order, "pending"); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE commerce_orders SET expires=? WHERE id=?", order.Expires, order.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	v, e = a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil {
		return e
	}
	v, e = a.store.decorateCryptoInvoice(v)
	if e != nil {
		return e
	}
	jsonResponse(w, 201, v)
	return nil
}

func (s *Store) cryptoInvoiceTransfers(id string) ([]CryptoTransfer, error) {
	rows, e := s.db.Query("SELECT chain_id,tx_hash,log_index,contract,atoms,block_number,block_time,state,reason FROM crypto_transfers WHERE invoice_id=? UNION ALL SELECT chain_id,tx_hash,log_index,contract,atoms,block_number,block_time,'observed','等待网络最终确认' FROM crypto_transfer_observations WHERE invoice_id=? ORDER BY block_time DESC,log_index DESC LIMIT 100", id, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []CryptoTransfer{}
	for rows.Next() {
		var v CryptoTransfer
		if e = rows.Scan(&v.ChainID, &v.TxHash, &v.LogIndex, &v.Contract, &v.Atoms, &v.BlockNumber, &v.BlockTime, &v.State, &v.Reason); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
