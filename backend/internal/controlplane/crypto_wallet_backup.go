package controlplane

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"golang.org/x/crypto/scrypt"
)

const cryptoBackupFormat = "guangyue-crypto-wallet-v1"

type cryptoWalletBackup struct {
	Format string `json:"format"`
	Data   string `json:"data"`
}
type cryptoWalletDump struct {
	Version         int                   `json:"version"`
	ReservedIndices int64                 `json:"reserved_indices"`
	Exported        int64                 `json:"exported"`
	Wallet          CryptoWallet          `json:"wallet"`
	Mnemonic        string                `json:"mnemonic,omitempty"`
	Addresses       []CryptoWalletAddress `json:"addresses"`
}
type cryptoBackupCipher struct {
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func cryptoBackupAEAD(password string, salt []byte) (cipher.AEAD, error) {
	if utf8.RuneCountInString(password) < 12 || len(password) > 1024 {
		return nil, commerceFail(400, "备份密码至少12个字符，且不能超过1024字节")
	}
	key, e := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if e != nil {
		return nil, e
	}
	defer clear(key)
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func cryptoEncryptBackup(v cryptoWalletDump, password string) (cryptoWalletBackup, error) {
	out := cryptoWalletBackup{Format: cryptoBackupFormat}
	plain, e := json.Marshal(v)
	if e != nil {
		return out, e
	}
	defer clear(plain)
	if len(plain) > cryptoBackupMaxBytes {
		return out, commerceFail(409, "钱包备份过大，请使用完整面板备份")
	}
	box := cryptoBackupCipher{Salt: make([]byte, 32), Nonce: make([]byte, 12)}
	if _, e = rand.Read(box.Salt); e != nil {
		return out, e
	}
	if _, e = rand.Read(box.Nonce); e != nil {
		return out, e
	}
	a, e := cryptoBackupAEAD(password, box.Salt)
	if e != nil {
		return out, e
	}
	box.Ciphertext = a.Seal(nil, box.Nonce, plain, []byte(cryptoBackupFormat))
	out.Data = base64.StdEncoding.EncodeToString(jsonBytes(box))
	return out, nil
}
func cryptoDecryptBackup(backup cryptoWalletBackup, password string) (cryptoWalletDump, error) {
	var v cryptoWalletDump
	if backup.Format != cryptoBackupFormat || len(backup.Data) > cryptoBackupMaxBytes*2 {
		return v, cryptoInvalidDump()
	}
	raw, e := base64.StdEncoding.DecodeString(backup.Data)
	if e != nil {
		return v, cryptoInvalidDump()
	}
	var box cryptoBackupCipher
	if e = json.Unmarshal(raw, &box); e != nil || len(box.Salt) != 32 || len(box.Nonce) != 12 || len(box.Ciphertext) < 16 || len(box.Ciphertext) > cryptoBackupMaxBytes+16 {
		return v, cryptoInvalidDump()
	}
	a, e := cryptoBackupAEAD(password, box.Salt)
	if e != nil {
		return v, e
	}
	plain, e := a.Open(nil, box.Nonce, box.Ciphertext, []byte(cryptoBackupFormat))
	if e != nil {
		return v, commerceFail(400, "备份密码错误或文件已损坏")
	}
	defer clear(plain)
	if e = json.Unmarshal(plain, &v); e != nil {
		return v, cryptoInvalidDump()
	}
	return v, nil
}
func cryptoValidateDump(v cryptoWalletDump) error {
	w := v.Wallet
	if v.Version != 1 || v.Exported <= 0 || w.ID == "" || len(w.ID) > 100 || !validText(w.Name, 80) || w.Created <= 0 || w.Revision < 1 || w.NextIndex < 0 || w.NextIndex > cryptoMaxIndex || v.Addresses == nil || v.ReservedIndices < 0 || v.ReservedIndices > cryptoMaxIndex || int64(len(v.Addresses))+v.ReservedIndices != w.NextIndex {
		return cryptoInvalidDump()
	}
	if e := cryptoValidateXPub(w.XPub, w.Path); e != nil {
		return cryptoInvalidDump()
	}
	first, e := cryptoDeriveAddress(w.XPub, w.Path, 0)
	if e != nil || first != w.FirstAddress {
		return cryptoInvalidDump()
	}
	switch w.Mode {
	case "hot":
		if w.Engine != "trust-wallet-core" || w.EngineVersion != "4.8.4" {
			return cryptoInvalidDump()
		}
		xpub, path, address, e := cryptoHDFromMnemonic(v.Mnemonic)
		if e != nil || xpub != w.XPub || path != w.Path || address != w.FirstAddress {
			return cryptoInvalidDump()
		}
	case "watch_only":
		if v.Mnemonic != "" || w.Engine != "external-xpub" {
			return cryptoInvalidDump()
		}
	default:
		return cryptoInvalidDump()
	}
	seen := make(map[string]bool, len(v.Addresses))
	previous := int64(-1)
	for _, row := range v.Addresses {
		if row.ID == "" || seen[row.ID] || row.WalletID != w.ID || row.Index <= previous || row.Index >= w.NextIndex || row.Path != cryptoFullPath(w.Path, row.Index) || row.Created <= 0 || len(row.Label) > 480 {
			return cryptoInvalidDump()
		}
		address, e := cryptoDeriveAddress(w.XPub, w.Path, uint32(row.Index))
		if e != nil || address != row.Address {
			return cryptoInvalidDump()
		}
		seen[row.ID] = true
		previous = row.Index
	}
	return nil
}
func (a *App) cryptoWalletExport(w http.ResponseWriter, r *http.Request, route string, in cryptoWalletInput) error {
	p := strings.Split(route, "/")
	if len(p) != 3 || p[0] != "wallets" {
		return commerceFail(404, "功能不存在")
	}
	if utf8.RuneCountInString(in.BackupPassword) < 12 || len(in.BackupPassword) > 1024 {
		return commerceFail(400, "请设置至少12个字符的独立备份密码")
	}
	if in.BackupPassword == in.Password {
		return commerceFail(400, "请使用与管理员登录密码不同的备份密码")
	}
	tx, e := a.store.cryptoWalletTx(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, e := cryptoWalletByID(tx, p[1])
	if e != nil {
		return e
	}
	addresses, e := cryptoWalletAddresses(tx, v.ID, 30001)
	if e != nil {
		return e
	}
	if len(addresses) > 30000 {
		return commerceFail(409, "钱包地址记录过多，请使用完整面板备份")
	}
	dump := cryptoWalletDump{Version: 1, Exported: time.Now().Unix(), Wallet: v, Addresses: addresses}
	if e = tx.QueryRow("SELECT reserved_indices FROM crypto_wallets WHERE id=?", v.ID).Scan(&dump.ReservedIndices); e != nil {
		return e
	}
	if v.Mode == "hot" {
		secret, e := a.store.cryptoSecret(tx, v)
		if e != nil {
			return e
		}
		dump.Mnemonic = secret.Mnemonic
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	backup, e := cryptoEncryptBackup(dump, in.BackupPassword)
	if e != nil {
		return e
	}
	// Record successful encryption only after it has finished outside the DB
	// transaction. Do not lower the export checkpoint if another export won.
	mark, e := a.store.cryptoWalletTx(r.Context())
	if e != nil {
		return e
	}
	defer mark.Rollback()
	if _, e = mark.Exec("UPDATE crypto_wallets SET backup_exported=CASE WHEN backup_exported<? THEN ? ELSE backup_exported END WHERE id=?", v.Revision, v.Revision, v.ID); e != nil {
		return e
	}
	if e = mark.Commit(); e != nil {
		return e
	}
	jsonResponse(w, 200, backup)
	return nil
}
func (a *App) cryptoWalletRestore(tx *persistence.Tx, dump cryptoWalletDump) (CryptoWallet, error) {
	v := dump.Wallet
	receive, e := cryptoReceiveXPub(v.XPub, v.Path)
	if e != nil {
		return v, e
	}
	var found string
	e = tx.QueryRow("SELECT id FROM crypto_wallets WHERE receive_key=?", receive).Scan(&found)
	exists := e == nil
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	if exists {
		current, e := cryptoWalletByID(tx, found)
		if e != nil {
			return v, e
		}
		if current.Mode != v.Mode {
			return v, commerceFail(409, "已有钱包的模式不同，不能覆盖")
		}
		// Retain the current descriptor and all allocations made after this backup.
		current.NextIndex = max(current.NextIndex, v.NextIndex)
		current.Revision++
		current.BackupConfirmed = true
		v = current
		if v.Mode == "hot" {
			secret, e := a.store.vault.seal(cryptoWalletSecret{WalletID: v.ID, ReceiveKey: receive, Mnemonic: dump.Mnemonic})
			if e != nil {
				return v, e
			}
			if _, e = tx.Exec("UPDATE crypto_wallets SET secret=? WHERE id=?", secret, v.ID); e != nil {
				return v, e
			}
		}
		if _, e = tx.Exec("UPDATE crypto_wallets SET next_index=?,revision=?,backup_confirmed=1 WHERE id=?", v.NextIndex, v.Revision, v.ID); e != nil {
			return v, e
		}
	} else {
		// A restored copy starts paused, preventing immediate allocation from a
		// second panel until the administrator explicitly takes over this wallet.
		v.Enabled = false
		v.RecoveryRequired = true
		v.BackupConfirmed = true
		v.Revision++
		var secret []byte
		if v.Mode == "hot" {
			secret, e = a.store.vault.seal(cryptoWalletSecret{WalletID: v.ID, ReceiveKey: receive, Mnemonic: dump.Mnemonic})
			if e != nil {
				return v, e
			}
		}
		if e = cryptoInsertWallet(tx, v, receive, secret, v.Revision); e != nil {
			return v, commerceFail(409, "钱包标识冲突，无法恢复")
		}
	}
	for _, row := range dump.Addresses {
		canonicalPath := cryptoFullPath(v.Path, row.Index)
		var address, path string
		e = tx.QueryRow("SELECT address,path FROM crypto_wallet_addresses WHERE wallet_id=? AND address_index=?", v.ID, row.Index).Scan(&address, &path)
		if e == nil {
			if address != row.Address || path != canonicalPath {
				return v, cryptoInvalidDump()
			}
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return v, e
		}
		if _, e = tx.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", row.ID, v.ID, row.Index, canonicalPath, row.Address, row.Label, row.Created); e != nil {
			return v, e
		}
	}
	if _, e = tx.Exec("UPDATE crypto_wallets SET reserved_indices=next_index-(SELECT COUNT(*) FROM crypto_wallet_addresses WHERE wallet_id=?) WHERE id=?", v.ID, v.ID); e != nil {
		return v, e
	}
	return v, nil
}
