package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type CryptoWalletDeletion struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

const cryptoWalletOwnerScope = "SELECT id FROM crypto_wallet_history WHERE receive_hash=(SELECT receive_hash FROM crypto_wallet_history WHERE id=?)"

func cryptoWalletSameDescriptor(q cryptoWalletQuery, a, b string) (bool, error) {
	var n int
	e := q.QueryRow("SELECT COUNT(*) FROM crypto_wallet_history a JOIN crypto_wallet_history b ON a.receive_hash=b.receive_hash WHERE a.id=? AND b.id=?", a, b).Scan(&n)
	return n == 1, e
}

func cryptoWalletReceiveAddresses(q cryptoWalletQuery, id string, limit int) ([]CryptoWalletAddress, error) {
	query := "SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE wallet_id IN (" + cryptoWalletOwnerScope + ") ORDER BY address_index"
	args := []any{id}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, e := q.Query(query, args...)
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
	return out, rows.Err()
}

func cryptoWalletNetworkAddresses(q cryptoWalletQuery, id string, sol bool, limit int) ([]CryptoWalletAddress, error) {
	query := "SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE wallet_id IN (" + cryptoWalletOwnerScope + ") AND " + cryptoWalletPathSQL("", sol) + " ORDER BY address_index"
	args := []any{id}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, e := q.Query(query, args...)
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
	return out, rows.Err()
}

func cryptoWalletDeleted(q cryptoWalletQuery, id string) (bool, error) {
	var n int
	e := q.QueryRow("SELECT COUNT(*) FROM crypto_wallet_history WHERE id=? AND deleted_at>0", id).Scan(&n)
	return n > 0, e
}
func cryptoWalletHighWater(q cryptoWalletQuery, receive string) (int64, error) {
	var n int64
	e := q.QueryRow("SELECT COALESCE(MAX(next_index),0) FROM crypto_wallet_history WHERE receive_hash=?", digest(receive)).Scan(&n)
	return n, e
}

func cryptoWalletDescriptorSeen(q cryptoWalletQuery, receive string) (bool, error) {
	var n int
	e := q.QueryRow("SELECT COUNT(*) FROM crypto_wallet_history WHERE receive_hash=?", digest(receive)).Scan(&n)
	return n > 0, e
}

func (s *Store) permanentlyDeleteCryptoWallet(tx *persistence.Tx, v CryptoWallet) error {
	var pending int
	if e := tx.QueryRow("SELECT COUNT(*) FROM crypto_sweep_jobs WHERE wallet_id=? AND state NOT IN ('complete','cancelled')", v.ID).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return commerceFail(409, "钱包仍有未结束的归集任务，请先完成或取消任务再永久删除")
	}
	if e := tx.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions t JOIN crypto_sweep_jobs j ON j.id=t.job_id WHERE j.wallet_id=? AND t.state NOT IN ('confirmed','reverted')", v.ID).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return commerceFail(409, "钱包仍有已签名交易等待核验，请先确认交易结果再永久删除")
	}
	if e := tx.QueryRow("SELECT COUNT(*) FROM crypto_solana_transactions t JOIN crypto_sweep_jobs j ON j.id=t.job_id WHERE j.wallet_id=? AND t.state NOT IN ('confirmed','reverted')", v.ID).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return commerceFail(409, "钱包仍有已签名 Solana 交易等待核验，请先确认交易结果再永久删除")
	}
	settings, e := s.cryptoPaymentSettingsFrom(tx)
	if e != nil {
		return e
	}
	if settings.WalletID == v.ID {
		settings.Enabled = false
		settings.WalletID = ""
		settings.Revision++
		sealed, e := s.vault.seal(settings)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE crypto_payment_settings SET revision=?,doc=? WHERE id=1", settings.Revision, sealed); e != nil {
			return e
		}
	}
	receive, e := cryptoReceiveXPub(v.XPub, v.Path)
	if e != nil {
		return e
	}
	var ownerHash string
	if e = tx.QueryRow("SELECT receive_hash FROM crypto_wallet_history WHERE id=?", v.ID).Scan(&ownerHash); e != nil || ownerHash != digest(receive) {
		return commerceFail(422, "钱包永久地址归属校验失败，已阻止删除密钥")
	}
	changed, e := tx.Exec("UPDATE crypto_wallet_history SET next_index=CASE WHEN next_index<? THEN ? ELSE next_index END,deleted_at=? WHERE id=?", v.NextIndex, v.NextIndex, time.Now().Unix(), v.ID)
	if e != nil {
		return e
	}
	count, e := changed.RowsAffected()
	if e != nil {
		return e
	}
	if count != 1 {
		return commerceFail(422, "钱包删除记录未能保存，已阻止删除密钥")
	}
	// Remove replayed wallet descriptors too. Operation fingerprints and the
	// permanent deletion result remain, so network retries cannot resurrect a
	// deleted wallet or cause a second deletion.
	rows, e := tx.Query("SELECT id,response FROM crypto_wallet_operations")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		var raw []byte
		if e = rows.Scan(&id, &raw); e != nil {
			break
		}
		var x struct {
			ID     string `json:"id"`
			Wallet struct {
				ID string `json:"id"`
			} `json:"wallet"`
		}
		if json.Unmarshal(raw, &x) == nil && (x.ID == v.ID || x.Wallet.ID == v.ID) {
			ids = append(ids, id)
		}
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = tx.Exec("UPDATE crypto_wallet_operations SET response=? WHERE id=?", jsonBytes(CryptoWalletDeletion{v.ID, true}), id); e != nil {
			return e
		}
	}
	// This deletes the only per-wallet private seed blob. The shared vault key
	// must remain for unrelated wallets, receipts and historical invoice proofs.
	_, e = tx.Exec("DELETE FROM crypto_wallets WHERE id=?", v.ID)
	return e
}

// Migration020 retargets public-history ownership first. This vault-aware
// startup step then clears selected payment configuration and really erases
// every wallet that older releases had merely hidden with removed_at.
func (s *Store) migratePermanentCryptoWalletDeletes() error {
	tx, e := s.cryptoWalletTx(context.Background())
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.Query("SELECT " + cryptoWalletColumns + " FROM crypto_wallets WHERE removed_at>0")
	if e != nil {
		return e
	}
	wallets := []CryptoWallet{}
	for rows.Next() {
		var v CryptoWallet
		v, e = scanCryptoWallet(rows)
		if e != nil {
			break
		}
		wallets = append(wallets, v)
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, v := range wallets {
		if e = s.permanentlyDeleteCryptoWallet(tx, v); e != nil {
			return e
		}
	}
	return tx.Commit()
}
