package controlplane

import (
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) validateCryptoPayments(deep bool) error {
	fail := errors.New("crypto payment integrity failed")
	for _, query := range []string{
		"SELECT COUNT(*) FROM crypto_invoices i LEFT JOIN commerce_orders o ON o.id=i.order_id LEFT JOIN payment_attempts p ON p.id=i.attempt_id LEFT JOIN crypto_wallet_addresses a ON a.id=i.address_id WHERE o.id IS NULL OR p.id IS NULL OR a.id IS NULL OR i.user_id<>o.user_id OR p.order_id<>i.order_id OR p.user_id<>i.user_id OR p.provider_id<>'crypto' OR a.wallet_id<>i.wallet_id OR LOWER(a.address)<>LOWER(i.address)",
		"SELECT COUNT(*) FROM crypto_transfers t LEFT JOIN crypto_invoices i ON i.id=t.invoice_id WHERE i.id IS NULL OR t.recipient<>LOWER(i.address) OR t.state NOT IN ('confirmed','review_required') OR t.block_number<0 OR t.block_time<0",
		"SELECT COUNT(*) FROM crypto_invoices i WHERE i.state='paid' AND NOT EXISTS (SELECT 1 FROM payment_receipts r WHERE r.attempt_id=i.attempt_id AND r.order_id=i.order_id AND r.account_scope='evm_crypto' AND r.external_ref='invoice:'||i.id)",
		"SELECT COUNT(*) FROM crypto_invoice_chains c LEFT JOIN crypto_invoices i ON i.id=c.invoice_id WHERE i.id IS NULL OR c.next_block<0",
	} {
		var n int
		if e := s.db.QueryRow(query).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return fail
		}
	}
	if !deep {
		return nil
	}
	rows, e := s.db.Query("SELECT " + cryptoInvoiceColumns + " FROM crypto_invoices")
	if e != nil {
		return e
	}
	invoices := []CryptoInvoice{}
	for rows.Next() {
		v, e := s.scanCryptoInvoice(rows)
		if e != nil {
			rows.Close()
			return e
		}
		invoices = append(invoices, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	settings, e := s.cryptoPaymentSettings()
	if e != nil {
		return e
	}
	var storedRevision int64
	e = s.db.QueryRow("SELECT revision FROM crypto_payment_settings WHERE id=1").Scan(&storedRevision)
	if e == nil && storedRevision != settings.Revision {
		return fail
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	defs := cryptoDefaultSettings()
	for _, v := range invoices {
		asset, ok := defs.asset(v.AssetID)
		q, e := cryptoQuote(v.Snapshot.Amount, v.Snapshot.Asset)
		if !ok || e != nil || q != v.ExpectedAtoms || v.ChainID != asset.ChainID || v.Contract != asset.Contract || v.Snapshot.Asset.ID != asset.ID || v.Snapshot.Asset.Decimals != asset.Decimals || v.Snapshot.Asset.Contract != asset.Contract || v.Snapshot.Chain.ChainID != v.ChainID || v.Created <= 0 || v.Expires <= v.Created || v.StartBlock < 0 || v.NextBlock < v.StartBlock {
			return fail
		}
		attempt, e := s.paymentAttempt(v.AttemptID)
		if e != nil {
			return e
		}
		if attempt.Amount != v.Snapshot.Amount || attempt.AccountScope != "evm_crypto" || attempt.Expires != v.Expires {
			return fail
		}
	}
	rows, e = s.db.Query("SELECT chain_id,contract,atoms,tx_hash,block_hash FROM crypto_transfers UNION ALL SELECT chain_id,contract,atoms,tx_hash,block_hash FROM crypto_transfer_observations")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var chain int64
		var contract, atoms, txhash, blockhash string
		if e = rows.Scan(&chain, &contract, &atoms, &txhash, &blockhash); e != nil {
			return e
		}
		if _, e = cryptoAtoms(atoms); e != nil || !cryptoHash(txhash) || !cryptoHash(blockhash) || contract != strings.ToLower(contract) {
			return fail
		}
		valid := false
		for _, asset := range defs.Assets {
			if chain == asset.ChainID && contract == asset.Contract {
				valid = true
				break
			}
		}
		if !valid {
			return fail
		}
	}
	return rows.Err()
}
