package controlplane

import (
	"context"
	"encoding/json"
	"errors"
)

// Read under the same lock as allocation and restore. Close each rows cursor
// before querying secrets, including on SQLite's single-connection pool.
func (s *Store) validateCryptoWallets(deep bool) error {
	tx, e := s.cryptoWalletTx(context.Background())
	if e != nil {
		return e
	}
	defer tx.Rollback()
	fail := errors.New("crypto wallet integrity failed")
	for _, q := range []string{
		"SELECT COUNT(*) FROM crypto_wallet_state WHERE id=1",
		"SELECT COUNT(*) FROM crypto_wallet_addresses a LEFT JOIN crypto_wallets w ON w.id=a.wallet_id WHERE w.id IS NULL",
		"SELECT COUNT(*) FROM crypto_wallets w WHERE w.next_index<>w.reserved_indices+(SELECT COUNT(*) FROM crypto_wallet_addresses a WHERE a.wallet_id=w.id) OR w.next_index<=(SELECT COALESCE(MAX(a.address_index),-1) FROM crypto_wallet_addresses a WHERE a.wallet_id=w.id)",
	} {
		var n int64
		if e = tx.QueryRow(q).Scan(&n); e != nil {
			return e
		}
		if q == "SELECT COUNT(*) FROM crypto_wallet_state WHERE id=1" {
			if n != 1 {
				return fail
			}
		} else if n != 0 {
			return fail
		}
	}
	rows, e := tx.Query("SELECT " + cryptoWalletColumns + " FROM crypto_wallets")
	if e != nil {
		return e
	}
	wallets := []CryptoWallet{}
	for rows.Next() {
		v, e := scanCryptoWallet(rows)
		if e != nil {
			rows.Close()
			return e
		}
		wallets = append(wallets, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	if len(wallets) > 0 {
		var sealed, key []byte
		if e = tx.QueryRow("SELECT hmac_secret FROM crypto_wallet_state WHERE id=1").Scan(&sealed); e != nil {
			return e
		}
		if e = s.vault.open(sealed, &key); e != nil || len(key) != 32 {
			return fail
		}
		clear(key)
	}
	for _, v := range wallets {
		receive, e := cryptoReceiveXPub(v.XPub, v.Path)
		if e != nil {
			return fail
		}
		first, e := cryptoDeriveAddress(v.XPub, v.Path, 0)
		if e != nil || first != v.FirstAddress || !validText(v.Name, 80) || v.Revision < 1 || v.NextIndex < 0 || v.NextIndex > cryptoMaxIndex {
			return fail
		}
		var stored string
		var secret []byte
		if e = tx.QueryRow("SELECT receive_key,secret FROM crypto_wallets WHERE id=?", v.ID).Scan(&stored, &secret); e != nil {
			return e
		}
		if stored != receive {
			return fail
		}
		switch v.Mode {
		case "hot":
			if v.Engine != "trust-wallet-core" || v.EngineVersion != "4.8.4" || len(secret) == 0 {
				return fail
			}
			if deep {
				value, e := s.cryptoSecret(tx, v)
				if e != nil {
					return fail
				}
				xp, path, addr, e := cryptoHDFromMnemonic(value.Mnemonic)
				if e != nil || xp != v.XPub || path != v.Path || addr != first {
					return fail
				}
			}
		case "watch_only":
			if len(secret) != 0 || v.Engine != "external-xpub" {
				return fail
			}
		default:
			return fail
		}
		if !deep {
			continue
		}
		rows, e = tx.Query("SELECT address_index,path,address FROM crypto_wallet_addresses WHERE wallet_id=? ORDER BY address_index", v.ID)
		if e != nil {
			return e
		}
		previous := int64(-1)
		for rows.Next() {
			var n int64
			var path, addr string
			if e = rows.Scan(&n, &path, &addr); e != nil {
				rows.Close()
				return e
			}
			want, e := cryptoDeriveAddress(v.XPub, v.Path, uint32(n))
			if e != nil || n <= previous || n < 0 || n >= v.NextIndex || path != cryptoFullPath(v.Path, n) || addr != want {
				rows.Close()
				return fail
			}
			previous = n
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return e
		}
	}
	if deep {
		rows, e = tx.Query("SELECT fingerprint,response FROM crypto_wallet_operations")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var hash string
			var raw []byte
			if e = rows.Scan(&hash, &raw); e != nil {
				return e
			}
			var response map[string]json.RawMessage
			if len(hash) != 64 || json.Unmarshal(raw, &response) != nil {
				return fail
			}
			for _, key := range []string{"mnemonic", "password", "backup_password", "secret"} {
				if _, ok := response[key]; ok {
					return fail
				}
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
	}
	return nil
}
