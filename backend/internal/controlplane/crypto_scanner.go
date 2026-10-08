package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type cryptoScannedTransfer struct {
	Log       cryptoEVMLog
	Timestamp int64
	Final     bool
}

func (a *App) runCryptoPaymentWorker(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = a.refreshCryptoAutoRates(ctx)
		_ = a.cryptoPaymentWork(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *App) cryptoPaymentWork(ctx context.Context) error {
	if a.cfg.businessAgent() || a.store.commerceErr != nil {
		return a.store.commerceErr
	}
	// Reserve most capacity for payable orders while retaining a permanent
	// fair history sweep for late/wrong-network funds after cancellation.
	invoices := []CryptoInvoice{}
	active := "state IN ('waiting','review_required') AND order_id IN (SELECT id FROM commerce_orders WHERE state='pending')"
	for _, selection := range []struct {
		where string
		limit int
	}{{active, 8}, {"NOT (" + active + ")", 2}} {
		rows, e := a.store.db.Query("SELECT "+cryptoInvoiceColumns+" FROM crypto_invoices WHERE "+selection.where+" ORDER BY last_scan,id LIMIT ?", selection.limit)
		if e != nil {
			return e
		}
		for rows.Next() {
			v, e := a.store.scanCryptoInvoice(rows)
			if e != nil {
				rows.Close()
				return e
			}
			invoices = append(invoices, v)
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return e
		}
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 3)
	results := make(chan error, len(invoices))
	for _, v := range invoices {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(v CryptoInvoice) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			e := a.scanCryptoInvoice(scanCtx, v)
			if e != nil {
				_, _ = a.store.db.Exec("UPDATE crypto_invoices SET last_scan=?,scan_error=? WHERE id=?", time.Now().Unix(), "网络核验暂未完成，款项记录保留并继续重试", v.ID)
			}
			results <- e
		}(v)
	}
	wg.Wait()
	close(results)
	var errs []error
	for e := range results {
		if e != nil {
			errs = append(errs, e)
		}
	}

	return errors.Join(errs...)
}
func (a *App) scanCryptoInvoice(ctx context.Context, v CryptoInvoice) error {
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	chains := []CryptoChain{v.Snapshot.Chain}
	for _, c := range settings.Chains {
		if c.ChainID == v.ChainID {
			if c.RPCURL != "" && c.RPCBackupURL != "" {
				chains[0] = c
			}
			continue
		}
		if c.RPCURL != "" && c.RPCBackupURL != "" && (c.ChainID == 1 || c.ChainID == 56) {
			chains = append(chains, c)
		}
	}
	for _, chain := range chains {
		if e := a.store.cryptoChainAvailable(a.store.db, chain.ChainID); e != nil {
			return e
		}
		chain.FinalityVerified = chain.ChainID == 1 || chain.ChainID == 56
		client, e := newCryptoEVM(chain, a.cfg.Dev)
		if e != nil {
			return e
		}
		final, e := client.Finalized(ctx)
		if e != nil {
			return e
		}
		if chain.ChainID == v.ChainID {
			if e = client.ValidateToken(ctx, v.Contract, v.Decimals, final.Number); e != nil {
				return e
			}
		}
		latest, e := client.Latest(ctx)
		if e != nil {
			return e
		}
		if final.Number > latest.Number || final.Number > 1<<63-2 || latest.Number > 1<<63-2 {
			return errors.New("invalid chain boundary")
		}
		var next int64
		var previousHash string
		e = a.store.db.QueryRow("SELECT next_block,finalized_hash FROM crypto_invoice_chains WHERE invoice_id=? AND chain_id=?", v.ID, chain.ChainID).Scan(&next, &previousHash)
		if errors.Is(e, sql.ErrNoRows) {
			next = v.StartBlock
			if chain.ChainID != v.ChainID {
				n, e := cryptoBlockAtTime(ctx, client, final, v.Created-120)
				if e != nil {
					return e
				}
				next = int64(n)
			}
			_, e = a.store.db.Exec("INSERT INTO crypto_invoice_chains(invoice_id,chain_id,next_block) VALUES(?,?,?) ON CONFLICT DO NOTHING", v.ID, chain.ChainID, next)
			if e != nil {
				return e
			}
			if e = a.store.db.QueryRow("SELECT next_block,finalized_hash FROM crypto_invoice_chains WHERE invoice_id=? AND chain_id=?", v.ID, chain.ChainID).Scan(&next, &previousHash); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		if previousHash != "" && next > 0 {
			b, e := client.block(ctx, cryptoHex(uint64(next-1)))
			if e != nil {
				return e
			}
			if b.Hash != previousHash {
				return a.store.freezeCryptoChain(chain.ChainID)
			}
		}
		if next > int64(latest.Number) {
			continue
		}
		to := min(uint64(next)+499, latest.Number)
		finalTo := min(to, final.Number)
		transfers := []cryptoScannedTransfer{}
		blocks := map[uint64]cryptoEVMBlock{}
		for _, asset := range cryptoDefaultSettings().Assets {
			if asset.ChainID != chain.ChainID {
				continue
			}
			var logs []cryptoEVMLog
			if uint64(next) <= finalTo {
				logs, e = client.ConfirmedTransfers(ctx, asset.Contract, v.Address, uint64(next), finalTo)
				if e != nil {
					return e
				}
			}
			if to > final.Number {
				unfinal, e := client.Transfers(ctx, asset.Contract, v.Address, max(uint64(next), final.Number+1), to)
				if e != nil {
					return e
				}
				logs = append(logs, unfinal...)
			}
			for _, log := range logs {
				b, ok := blocks[log.BlockNumber]
				if !ok {
					b, e = client.block(ctx, cryptoHex(log.BlockNumber))
					if e != nil {
						return e
					}
					blocks[log.BlockNumber] = b
				}
				if b.Hash != log.BlockHash || b.Timestamp > 1<<63-1 {
					return errors.New("transfer block mismatch")
				}
				transfers = append(transfers, cryptoScannedTransfer{Log: log, Timestamp: int64(b.Timestamp), Final: log.BlockNumber <= final.Number})
			}
		}
		nextAfter := next
		nextHash := previousHash
		if uint64(next) <= finalTo {
			nextAfter = int64(finalTo + 1)
			b, e := client.block(ctx, cryptoHex(finalTo))
			if e != nil {
				return e
			}
			nextHash = b.Hash
		}
		if e = a.applyCryptoScan(v.ID, chain.ChainID, next, to, nextAfter, nextHash, final, transfers); e != nil {
			if errors.Is(e, errCryptoFinality) {
				return a.store.freezeCryptoChain(chain.ChainID)
			}
			return e
		}
	}
	_, e = a.store.db.Exec("UPDATE crypto_invoices SET last_scan=?,scan_error='' WHERE id=?", time.Now().Unix(), v.ID)
	return e
}
func cryptoBlockAtTime(ctx context.Context, c *cryptoEVM, head cryptoEVMBlock, timestamp int64) (uint64, error) {
	lo, hi := uint64(0), head.Number
	for lo < hi {
		mid := lo + (hi-lo)/2
		b, e := c.block(ctx, cryptoHex(mid))
		if e != nil {
			return 0, e
		}
		if b.Timestamp < uint64(max(timestamp, 0)) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, nil
}
func (a *App) applyCryptoScan(id string, chainID, from int64, to uint64, next int64, hash string, final cryptoEVMBlock, transfers []cryptoScannedTransfer) error {
	v, e := a.store.cryptoInvoice(a.store.db, id)
	if e != nil {
		return e
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = cryptoLockChain(tx, chainID); e != nil {
		return e
	}
	// Every channel takes the order lock before payment rows; this is the shared
	// financial ownership boundary across independent application instances.
	if _, e = tx.Exec("UPDATE commerce_orders SET updated=updated WHERE id=?", v.OrderID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE crypto_invoices SET state=state WHERE id=?", id); e != nil {
		return e
	}
	v, e = a.store.cryptoInvoice(tx, id)
	if e != nil {
		return e
	}
	var cursor int64
	if e = tx.QueryRow("SELECT next_block FROM crypto_invoice_chains WHERE invoice_id=? AND chain_id=?", id, chainID).Scan(&cursor); e != nil {
		return e
	}
	if cursor != from {
		return nil
	}
	// Non-final events are observations, so a canonical rescan may orphan them.
	if _, e = tx.Exec("DELETE FROM crypto_transfer_observations WHERE invoice_id=? AND chain_id=? AND block_number>=? AND block_number<=?", id, chainID, from, int64(to)); e != nil {
		return e
	}
	for _, item := range transfers {
		l := item.Log
		atoms, e := cryptoHexInt(l.Data)
		if e != nil {
			return e
		}
		if atoms.Sign() == 0 {
			continue
		}
		state, reason := "observed", ""
		if item.Final {
			state = "confirmed"
			if chainID != v.ChainID || l.Address != v.Contract {
				state, reason = "review_required", "资产或网络与付款单不符"
			} else if item.Timestamp >= v.Expires {
				state, reason = "review_required", "付款截止后上链，需人工核对"
			} else if item.Timestamp < v.Created {
				state, reason = "review_required", "付款单创建前的链上转账，需人工核对"
			}
		}
		if len(l.Topics) != 3 || l.Topics[0] != cryptoTransferTopic || l.Topics[2] != "0x"+cryptoABIAddress(v.Address) || l.Index > 1<<63-1 {
			return errors.New("invalid transfer identity")
		}
		if !item.Final {
			var settled int
			if e = tx.QueryRow("SELECT COUNT(*) FROM crypto_transfers WHERE chain_id=? AND tx_hash=?", chainID, l.TxHash).Scan(&settled); e != nil {
				return e
			}
			if settled > 0 {
				continue
			}
			if _, e = tx.Exec("INSERT INTO crypto_transfer_observations(chain_id,tx_hash,log_index,block_hash,invoice_id,contract,atoms,block_number,block_time) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(chain_id,tx_hash,block_hash,log_index) DO UPDATE SET invoice_id=excluded.invoice_id,contract=excluded.contract,atoms=excluded.atoms,block_number=excluded.block_number,block_time=excluded.block_time", chainID, l.TxHash, int64(l.Index), l.BlockHash, id, l.Address, atoms.String(), int64(l.BlockNumber), item.Timestamp); e != nil {
				return e
			}
			continue
		}
		// A complete final receipt supersedes every old observation of this
		// transaction, even if its global log indices moved between recipients.
		if _, e = tx.Exec("DELETE FROM crypto_transfer_observations WHERE chain_id=? AND tx_hash=?", chainID, l.TxHash); e != nil {
			return e
		}
		sender := "0x" + l.Topics[1][len(l.Topics[1])-40:]
		var oldInvoice, oldAtoms, oldHash string
		read := tx.QueryRow("SELECT invoice_id,atoms,block_hash FROM crypto_transfers WHERE chain_id=? AND tx_hash=? AND log_index=?", chainID, l.TxHash, int64(l.Index)).Scan(&oldInvoice, &oldAtoms, &oldHash)
		if read != nil && !errors.Is(read, sql.ErrNoRows) {
			return read
		}
		if read == nil && (oldInvoice != id || oldAtoms != atoms.String() || oldHash != l.BlockHash) {
			return errCryptoFinality
		}
		now := time.Now().Unix()
		if _, e = tx.Exec("INSERT INTO crypto_transfers(chain_id,tx_hash,log_index,invoice_id,contract,sender,recipient,atoms,block_number,block_hash,block_time,state,reason,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(chain_id,tx_hash,log_index) DO UPDATE SET block_number=excluded.block_number,block_hash=excluded.block_hash,block_time=excluded.block_time,state=excluded.state,reason=excluded.reason,updated=excluded.updated", chainID, l.TxHash, int64(l.Index), id, l.Address, sender, strings.ToLower(v.Address), atoms.String(), int64(l.BlockNumber), l.BlockHash, item.Timestamp, state, reason, now, now); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE crypto_invoice_chains SET next_block=?,finalized_hash=? WHERE invoice_id=? AND chain_id=?", next, hash, id, chainID); e != nil {
		return e
	}
	if chainID == v.ChainID {
		if _, e = tx.Exec("UPDATE crypto_invoices SET next_block=?,finalized_block=? WHERE id=?", next, int64(final.Number), id); e != nil {
			return e
		}
	}
	if e = a.settleCryptoInvoice(tx, &v, chainID == v.ChainID && next > int64(final.Number) && int64(final.Timestamp) > v.Expires); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) settleCryptoInvoice(tx *persistence.Tx, v *CryptoInvoice, deadlineFinal bool) error {
	if e := a.store.cryptoInvoiceAmounts(tx, v); e != nil {
		return e
	}
	confirmed, e := cryptoAtoms(v.ConfirmedAtoms)
	if e != nil {
		return e
	}
	expected, e := cryptoAtoms(v.ExpectedAtoms)
	if e != nil {
		return e
	}
	var body []byte
	var o Order
	if e = tx.QueryRow("SELECT doc FROM commerce_orders WHERE id=?", v.OrderID).Scan(&body); e != nil {
		return e
	}
	if e = json.Unmarshal(body, &o); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE payment_attempts SET updated=updated WHERE id=?", v.AttemptID); e != nil {
		return e
	}
	attempt, e := scanPaymentAttempt(tx.QueryRow("SELECT "+paymentAttemptColumns+" FROM payment_attempts WHERE id=?", v.AttemptID))
	if e != nil {
		return e
	}
	if confirmed.Cmp(expected) >= 0 && (v.State == "waiting" || v.State == "review_required") && o.State == "pending" && o.PaymentAttemptID == v.AttemptID && attempt.State == "awaiting_customer" {
		var paidAt int64
		if e = tx.QueryRow("SELECT MAX(block_time) FROM crypto_transfers WHERE invoice_id=? AND chain_id=? AND contract=? AND state='confirmed' AND block_time<?", v.ID, v.ChainID, v.Contract, v.Expires).Scan(&paidAt); e != nil {
			return e
		}
		// One receipt represents this frozen invoice settlement. Every contributing
		// token transfer retains its global chain/tx/log identity and exact atoms.
		receipt := PaymentReceipt{ID: serial("GYR"), AccountScope: "evm_crypto", ProviderID: "crypto", ExternalRef: "invoice:" + v.ID, AttemptID: v.AttemptID, OrderID: v.OrderID, UserID: v.UserID, Amount: attempt.Amount, Currency: "CNY", State: "verified", Created: paidAt, Updated: time.Now().Unix()}
		if _, e = tx.Exec("INSERT INTO payment_receipts("+paymentReceiptColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", receipt.ID, receipt.AccountScope, receipt.ProviderID, receipt.ExternalRef, receipt.AttemptID, receipt.OrderID, receipt.UserID, receipt.Amount, receipt.Currency, receipt.State, "链上付款已最终确认", receipt.Created, receipt.Updated); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE payment_attempts SET state='paid',external_ref=?,updated=?,message='链上付款已最终确认' WHERE id=?", receipt.ExternalRef, time.Now().Unix(), v.AttemptID); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE crypto_invoices SET state='paid' WHERE id=?", v.ID); e != nil {
			return e
		}
		v.State = "paid"
	} else if v.State != "paid" && (confirmed.Sign() > 0 || v.ReceivedAtoms != "0") && (o.State != "pending" || o.PaymentAttemptID != v.AttemptID || v.State == "superseded" || v.State == "expired") {
		if _, e = tx.Exec("UPDATE crypto_invoices SET state='review_required' WHERE id=?", v.ID); e != nil {
			return e
		}
	}
	var review int
	if e = tx.QueryRow("SELECT COUNT(*) FROM crypto_transfers WHERE invoice_id=? AND state='review_required'", v.ID).Scan(&review); e != nil {
		return e
	}
	if review > 0 && v.State != "paid" {
		if _, e = tx.Exec("UPDATE crypto_invoices SET state='review_required' WHERE id=?", v.ID); e != nil {
			return e
		}
	}
	if deadlineFinal && (v.State == "waiting" || v.State == "review_required") && confirmed.Cmp(expected) < 0 && o.State == "pending" && o.PaymentAttemptID == v.AttemptID {
		o.State = "expired"
		o.Message = "加密货币付款窗口已结束，未足额款项已保留待核对"
		if e = saveOrder(tx, o, "pending"); e != nil {
			return e
		}
		state := "expired"
		if confirmed.Sign() > 0 || review > 0 {
			state = "review_required"
		}
		if _, e = tx.Exec("UPDATE crypto_invoices SET state=? WHERE id=?", state, v.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE payment_attempts SET state='expired',updated=?,message=? WHERE id=? AND state='awaiting_customer'", time.Now().Unix(), o.Message, v.AttemptID); e != nil {
			return e
		}
	}
	return nil
}

var errCryptoFinality = errors.New("最终确认历史发生变化，已暂停该网络资金操作，请管理员核对")

func (s *Store) cryptoChainAvailable(q cryptoWalletQuery, chainID int64) error {
	var frozen int
	e := q.QueryRow("SELECT frozen FROM crypto_chain_state WHERE chain_id=?", chainID).Scan(&frozen)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if frozen != 0 {
		return commerceFail(409, errCryptoFinality.Error())
	}
	return nil
}
func cryptoLockChain(tx *persistence.Tx, chainID int64) error {
	if _, e := tx.Exec("INSERT INTO crypto_chain_state(chain_id,updated) VALUES(?,?) ON CONFLICT DO NOTHING", chainID, time.Now().Unix()); e != nil {
		return e
	}
	if _, e := tx.Exec("UPDATE crypto_chain_state SET updated=updated WHERE chain_id=?", chainID); e != nil {
		return e
	}
	var frozen int
	if e := tx.QueryRow("SELECT frozen FROM crypto_chain_state WHERE chain_id=?", chainID).Scan(&frozen); e != nil {
		return e
	}
	if frozen != 0 {
		return errCryptoFinality
	}
	return nil
}
func (s *Store) freezeCryptoChain(chainID int64) error {
	_, e := s.db.Exec("INSERT INTO crypto_chain_state(chain_id,frozen,reason,updated) VALUES(?,1,?,?) ON CONFLICT(chain_id) DO UPDATE SET frozen=1,reason=excluded.reason,updated=excluded.updated", chainID, errCryptoFinality.Error(), time.Now().Unix())
	if e != nil {
		return e
	}
	return errCryptoFinality
}
