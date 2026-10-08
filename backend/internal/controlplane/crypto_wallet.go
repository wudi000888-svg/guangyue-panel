package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

const cryptoWalletPrefix = "/api/commerce/crypto/admin/"
const cryptoMaxIndex int64 = 1 << 31
const cryptoBackupMaxBytes = 8 << 20

// Limit expensive backup parsing, scrypt and serialization independently of the
// application mutex. This also bounds memory on Lite installations.
var cryptoWalletBackupSlots = make(chan struct{}, 1)

type CryptoWallet struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Mode              string  `json:"mode"`
	XPub              string  `json:"xpub"`
	Path              string  `json:"path"`
	FirstAddress      string  `json:"first_address"`
	Engine            string  `json:"engine"`
	EngineVersion     string  `json:"engine_version"`
	Enabled           bool    `json:"enabled"`
	Revision          int64   `json:"revision"`
	NextIndex         int64   `json:"next_index"`
	Created           int64   `json:"created"`
	BackupConfirmed   bool    `json:"backup_confirmed"`
	RecoveryRequired  bool    `json:"recovery_required"`
	SupportedChainIDs []int64 `json:"supported_chain_ids"`
	FundingAddress    string  `json:"funding_address"`
	FundingPath       string  `json:"funding_path"`
}

type CryptoWalletAddress struct {
	ID       string `json:"id"`
	WalletID string `json:"wallet_id"`
	Index    int64  `json:"index"`
	Path     string `json:"path"`
	Address  string `json:"address"`
	Label    string `json:"label"`
	Created  int64  `json:"created"`
}

type cryptoWalletSecret struct {
	WalletID   string `json:"wallet_id"`
	ReceiveKey string `json:"receive_key"`
	Mnemonic   string `json:"mnemonic"`
}

type cryptoWalletInput struct {
	Name              string             `json:"name"`
	Mnemonic          string             `json:"mnemonic"`
	XPub              string             `json:"xpub"`
	Path              string             `json:"path"`
	FirstAddress      string             `json:"first_address"`
	EngineVersion     string             `json:"engine_version"`
	Password          string             `json:"password"`
	OperationID       string             `json:"operation_id"`
	RiskAck           bool               `json:"risk_ack"`
	Revision          int64              `json:"revision"`
	Enabled           *bool              `json:"enabled"`
	Label             string             `json:"label"`
	BackupPassword    string             `json:"backup_password"`
	NextIndex         *int64             `json:"next_index"`
	RecoveryAck       bool               `json:"recovery_ack"`
	Backup            cryptoWalletBackup `json:"backup"`
	SupportedChainIDs []int64            `json:"supported_chain_ids"`
}

type cryptoWalletQuery interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

const cryptoWalletColumns = "id,name,mode,xpub,path,first_address,engine,engine_version,enabled,revision,next_index,created,backup_confirmed,recovery_required,supported_chain_ids"

type cryptoScanner interface{ Scan(...any) error }

func scanCryptoWallet(row cryptoScanner) (v CryptoWallet, err error) {
	var enabled, confirmed, recovery int
	var chains []byte
	err = row.Scan(&v.ID, &v.Name, &v.Mode, &v.XPub, &v.Path, &v.FirstAddress, &v.Engine, &v.EngineVersion, &enabled, &v.Revision, &v.NextIndex, &v.Created, &confirmed, &recovery, &chains)
	v.Enabled, v.BackupConfirmed, v.RecoveryRequired = enabled == 1, confirmed == 1, recovery == 1
	if err != nil {
		return
	}
	if err = json.Unmarshal(chains, &v.SupportedChainIDs); err != nil || v.SupportedChainIDs == nil {
		return v, errors.New("wallet network policy unavailable")
	}
	v.SupportedChainIDs, err = cryptoWalletChainIDs(v.SupportedChainIDs)
	if err == nil {
		v, err = cryptoWalletFundingDTO(v)
	}
	return
}
func cryptoWalletByID(q cryptoWalletQuery, id string) (CryptoWallet, error) {
	v, e := scanCryptoWallet(q.QueryRow("SELECT "+cryptoWalletColumns+" FROM crypto_wallets WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return v, commerceFail(404, "钱包不存在")
	}
	return v, e
}
func cryptoWalletAddresses(q cryptoWalletQuery, id string, limit int) ([]CryptoWalletAddress, error) {
	query := "SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE wallet_id=? ORDER BY address_index"
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
		var v CryptoWalletAddress
		if e = rows.Scan(&v.ID, &v.WalletID, &v.Index, &v.Path, &v.Address, &v.Label, &v.Created); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Every mutating wallet transaction locks the same database row before reading.
// This serializes independent controllers on both SQLite and PostgreSQL.
func (s *Store) cryptoWalletTx(ctx context.Context) (*persistence.Tx, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE crypto_wallet_state SET lock_version=lock_version+1 WHERE id=1"); e != nil {
		tx.Rollback()
		return nil, e
	}
	return tx, nil
}
func (s *Store) cryptoOperation(tx *persistence.Tx, actor int64, route string, in cryptoWalletInput) (string, json.RawMessage, error) {
	if !operationPattern.MatchString(in.OperationID) {
		return "", nil, commerceFail(400, "缺少有效操作标识")
	}
	var sealed, key []byte
	if e := tx.QueryRow("SELECT hmac_secret FROM crypto_wallet_state WHERE id=1").Scan(&sealed); e != nil {
		return "", nil, e
	}
	if len(sealed) == 0 {
		key = make([]byte, 32)
		if _, e := rand.Read(key); e != nil {
			return "", nil, e
		}
		var e error
		sealed, e = s.vault.seal(key)
		if e != nil {
			return "", nil, e
		}
		if _, e = tx.Exec("UPDATE crypto_wallet_state SET hmac_secret=? WHERE id=1", sealed); e != nil {
			return "", nil, e
		}
	} else if e := s.vault.open(sealed, &key); e != nil || len(key) != 32 {
		return "", nil, errors.New("wallet operation key unavailable")
	}
	defer clear(key)
	// Password reauthentication happens on every request, including replays. The
	// persisted fingerprint is keyed, so even low-entropy input cannot be guessed
	// from a database dump. Neither passwords nor mnemonic enter replay storage.
	in.Password = ""
	mac := hmac.New(sha256.New, key)
	mac.Write(jsonBytes([]any{route, in}))
	fingerprint := hex.EncodeToString(mac.Sum(nil))
	var user int64
	var previous string
	var response []byte
	e := tx.QueryRow("SELECT actor_id,fingerprint,response FROM crypto_wallet_operations WHERE id=?", in.OperationID).Scan(&user, &previous, &response)
	if errors.Is(e, sql.ErrNoRows) {
		return fingerprint, nil, nil
	}
	if e != nil {
		return "", nil, e
	}
	if user != actor || !hmac.Equal([]byte(previous), []byte(fingerprint)) {
		return "", nil, commerceFail(409, "操作标识已用于其他请求")
	}
	return fingerprint, json.RawMessage(response), nil
}
func cryptoSaveOperation(tx *persistence.Tx, actor int64, in cryptoWalletInput, hash string, response any) error {
	_, e := tx.Exec("INSERT INTO crypto_wallet_operations(id,actor_id,fingerprint,response,created) VALUES(?,?,?,?,?)", in.OperationID, actor, hash, jsonBytes(response), time.Now().Unix())
	return e
}

func (a *App) cryptoWalletAPIRoute(w http.ResponseWriter, r *http.Request, actor Record) bool {
	if !strings.HasPrefix(r.URL.Path, cryptoWalletPrefix) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	current, e := a.commerceActor(actor, false, "")
	if e == nil && current.Role != "owner" {
		e = commerceFail(403, "需要管理员权限")
	}
	if e != nil {
		commerceWriteError(w, e)
		return true
	}
	route := strings.TrimPrefix(r.URL.Path, cryptoWalletPrefix)
	if r.Method == http.MethodGet {
		e = a.cryptoWalletGet(w, r, route)
	} else if r.Method == http.MethodPost {
		if route == "wallets/restore" || strings.HasSuffix(route, "/backup") {
			wait := time.NewTimer(5 * time.Second)
			defer wait.Stop()
			select {
			case cryptoWalletBackupSlots <- struct{}{}:
				defer func() { <-cryptoWalletBackupSlots }()
			case <-wait.C:
				commerceWriteError(w, commerceFail(429, "其他钱包备份正在处理，请稍后重试"))
				return true
			case <-r.Context().Done():
				commerceWriteError(w, commerceFail(408, "备份操作等待超时，请重试"))
				return true
			}
		}
		var in cryptoWalletInput
		bodyLimit := int64(32 << 10)
		if route == "wallets/restore" {
			bodyLimit = cryptoBackupMaxBytes * 2
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(&in); err != nil {
			failure(w, 400, "钱包请求格式无效")
			return true
		}
		if err := d.Decode(new(any)); err != io.EOF {
			failure(w, 400, "钱包请求格式无效")
			return true
		}
		if _, e = a.commerceActor(current, true, in.Password); e == nil {
			if a.store.commerceErr != nil {
				e = commerceFail(409, "数据完整性校验失败，已阻止钱包操作")
			} else {
				e = a.cryptoWalletPost(w, r, current, route, in)
			}
		}
	} else {
		e = commerceFail(405, "方法不支持")
	}
	if e != nil {
		commerceWriteError(w, e)
	}
	return true
}
func (a *App) cryptoWalletGet(w http.ResponseWriter, r *http.Request, route string) error {
	if route == "wallets" {
		rows, e := a.store.db.Query("SELECT " + cryptoWalletColumns + " FROM crypto_wallets ORDER BY created DESC,id DESC")
		if e != nil {
			return e
		}
		defer rows.Close()
		items := []CryptoWallet{}
		for rows.Next() {
			v, e := scanCryptoWallet(rows)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		jsonResponse(w, 200, object{"items": items})
		return nil
	}
	p := strings.Split(route, "/")
	if len(p) != 3 || p[0] != "wallets" || p[2] != "addresses" {
		return commerceFail(404, "功能不存在")
	}
	v, e := cryptoWalletByID(a.store.db, p[1])
	if e != nil {
		return e
	}
	// All allocations are available through cursor pages; no silent truncation.
	after := int64(-1)
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || after < 0 {
			return commerceFail(400, "地址游标无效")
		}
	}
	rows, e := a.store.db.Query("SELECT id,wallet_id,address_index,path,address,label,created FROM crypto_wallet_addresses WHERE wallet_id=? AND address_index>? ORDER BY address_index LIMIT 201", v.ID, after)
	if e != nil {
		return e
	}
	defer rows.Close()
	items := []CryptoWalletAddress{}
	for rows.Next() {
		var x CryptoWalletAddress
		if e = rows.Scan(&x.ID, &x.WalletID, &x.Index, &x.Path, &x.Address, &x.Label, &x.Created); e != nil {
			return e
		}
		items = append(items, x)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	more := len(items) > 200
	if more {
		items = items[:200]
	}
	var next *int64
	if more {
		n := items[len(items)-1].Index
		next = &n
	}
	jsonResponse(w, 200, object{"wallet": v, "items": items, "next_after": next})
	return nil
}
func cryptoFullPath(path string, index int64) string {
	if len(strings.Split(path, "/")) == 4 {
		path += "/0"
	}
	return path + "/" + strconv.FormatInt(index, 10)
}
func cryptoBool(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (s *Store) cryptoSecret(q cryptoWalletQuery, v CryptoWallet) (cryptoWalletSecret, error) {
	var out cryptoWalletSecret
	if v.Mode != "hot" {
		return out, commerceFail(400, "观察钱包不保存助记词")
	}
	var blob []byte
	if e := q.QueryRow("SELECT secret FROM crypto_wallets WHERE id=?", v.ID).Scan(&blob); e != nil {
		return out, e
	}
	if len(blob) == 0 {
		return out, commerceFail(400, "观察钱包不保存助记词")
	}
	receive, e := cryptoReceiveXPub(v.XPub, v.Path)
	if e != nil {
		return out, e
	}
	if e = s.vault.open(blob, &out); e != nil || out.WalletID != v.ID || out.ReceiveKey != receive {
		return out, errors.New("wallet secret integrity failed")
	}
	return out, nil
}
func (a *App) cryptoWalletPost(w http.ResponseWriter, r *http.Request, actor Record, route string, in cryptoWalletInput) error {
	if strings.HasSuffix(route, "/backup") {
		return a.cryptoWalletExport(w, r, route, in)
	}
	if strings.HasSuffix(route, "/reveal") {
		p := strings.Split(route, "/")
		if len(p) != 3 || p[0] != "wallets" {
			return commerceFail(404, "功能不存在")
		}
		v, e := cryptoWalletByID(a.store.db, p[1])
		if e != nil {
			return e
		}
		secret, e := a.store.cryptoSecret(a.store.db, v)
		if e != nil {
			return e
		}
		a.store.audit(actor.Username, "crypto_wallet_reveal", v.ID)
		jsonResponse(w, 200, object{"mnemonic": secret.Mnemonic})
		return nil
	}
	var restore *cryptoWalletDump
	if route == "wallets/restore" {
		v, e := cryptoDecryptBackup(in.Backup, in.BackupPassword)
		if e != nil {
			return e
		}
		if e = cryptoValidateDump(v); e != nil {
			return e
		}
		restore = &v
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Label = strings.TrimSpace(in.Label)
	in.Mnemonic = strings.Join(strings.Fields(in.Mnemonic), " ")
	in.XPub = strings.TrimSpace(in.XPub)
	in.Path = strings.TrimSpace(in.Path)
	tx, e := a.store.cryptoWalletTx(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback()
	hash, replay, e := a.store.cryptoOperation(tx, actor.ID, route, in)
	if e != nil {
		return e
	}
	if replay != nil {
		if e = tx.Commit(); e != nil {
			return e
		}
		jsonResponse(w, 200, replay)
		return nil
	}
	var response any
	status := 200
	var target string
	switch route {
	case "wallets/hot", "wallets/xpub":
		v, e := a.cryptoWalletCreate(tx, route, in)
		if e != nil {
			return e
		}
		response, target, status = v, v.ID, 201
	case "wallets/restore":
		v, e := a.cryptoWalletRestore(tx, *restore)
		if e != nil {
			return e
		}
		response, target = v, v.ID
	default:
		p := strings.Split(route, "/")
		if len(p) < 3 || p[0] != "wallets" {
			return commerceFail(404, "功能不存在")
		}
		v, e := cryptoWalletByID(tx, p[1])
		if e != nil {
			return e
		}
		target = v.ID
		if v.Revision != in.Revision {
			return commerceFail(409, "钱包已变化，请刷新后重试")
		}
		switch strings.Join(p[2:], "/") {
		case "addresses":
			if v.RecoveryRequired {
				return commerceFail(409, "请先核对并确认恢复钱包的地址高水位")
			}
			if !v.Enabled {
				return commerceFail(409, "钱包已暂停，无法分配新地址")
			}
			if !v.BackupConfirmed {
				return commerceFail(409, "请先导出并确认钱包备份")
			}
			if v.NextIndex >= cryptoMaxIndex {
				return commerceFail(409, "钱包地址索引已用尽")
			}
			if utf8.RuneCountInString(in.Label) > 120 {
				return commerceFail(400, "地址备注不能超过120字")
			}
			addr, e := cryptoDeriveAddress(v.XPub, v.Path, uint32(v.NextIndex))
			if e != nil {
				return e
			}
			allocation := CryptoWalletAddress{ID: serial("GYA"), WalletID: v.ID, Index: v.NextIndex, Path: cryptoFullPath(v.Path, v.NextIndex), Address: addr, Label: in.Label, Created: time.Now().Unix()}
			if _, e = tx.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", allocation.ID, v.ID, allocation.Index, allocation.Path, allocation.Address, allocation.Label, allocation.Created); e != nil {
				return e
			}
			v.NextIndex++
			v.Revision++
			if _, e = tx.Exec("UPDATE crypto_wallets SET next_index=?,revision=? WHERE id=?", v.NextIndex, v.Revision, v.ID); e != nil {
				return e
			}
			response = object{"wallet": v, "address": allocation}
			status = 201
		case "recovery":
			if !v.RecoveryRequired {
				return commerceFail(409, "钱包无需恢复高水位确认")
			}
			if !in.RecoveryAck || in.NextIndex == nil || *in.NextIndex < v.NextIndex || *in.NextIndex > cryptoMaxIndex {
				return commerceFail(400, "请核对原面板最后分配索引，填写不低于备份的下一索引并确认")
			}
			skipped := *in.NextIndex - v.NextIndex
			v.NextIndex = *in.NextIndex
			v.RecoveryRequired = false
			v.Enabled = false
			v.Revision++
			if _, e = tx.Exec("UPDATE crypto_wallets SET next_index=?,reserved_indices=reserved_indices+?,recovery_required=0,enabled=0,revision=? WHERE id=?", v.NextIndex, skipped, v.Revision, v.ID); e != nil {
				return e
			}
			response = v
		case "status":
			if in.Enabled == nil {
				return commerceFail(400, "请选择钱包启用状态")
			}
			v.Enabled = *in.Enabled
			v.Revision++
			if _, e = tx.Exec("UPDATE crypto_wallets SET enabled=?,revision=? WHERE id=?", cryptoBool(v.Enabled), v.Revision, v.ID); e != nil {
				return e
			}
			response = v
		case "backup/confirm":
			var exported int64
			if e = tx.QueryRow("SELECT backup_exported FROM crypto_wallets WHERE id=?", v.ID).Scan(&exported); e != nil {
				return e
			}
			if v.Mode == "hot" && exported == 0 {
				return commerceFail(409, "请先下载加密备份，再确认已妥善保存")
			}
			v.BackupConfirmed = true
			v.Revision++
			if _, e = tx.Exec("UPDATE crypto_wallets SET backup_confirmed=1,revision=? WHERE id=?", v.Revision, v.ID); e != nil {
				return e
			}
			response = v
		default:
			return commerceFail(404, "功能不存在")
		}
	}
	if e = cryptoSaveOperation(tx, actor.ID, in, hash, response); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.store.audit(actor.Username, "crypto_wallet_"+strings.ReplaceAll(route, "/", "_"), target)
	jsonResponse(w, status, response)
	return nil
}
func (a *App) cryptoWalletCreate(tx *persistence.Tx, route string, in cryptoWalletInput) (CryptoWallet, error) {
	v := CryptoWallet{ID: serial("GYW"), Name: in.Name, XPub: in.XPub, Path: in.Path, Enabled: true, Revision: 1, Created: time.Now().Unix()}
	var chainError error
	v.SupportedChainIDs, chainError = cryptoWalletChainIDs(in.SupportedChainIDs)
	if chainError != nil {
		return v, chainError
	}
	if !validText(v.Name, 80) {
		return v, commerceFail(400, "钱包名称为1至80字")
	}
	if e := cryptoValidateXPub(v.XPub, v.Path); e != nil {
		return v, commerceFail(400, "扩展公钥或派生路径无效")
	}
	var e error
	v.FirstAddress, e = cryptoDeriveAddress(v.XPub, v.Path, 0)
	if e != nil {
		return v, e
	}
	receive, e := cryptoReceiveXPub(v.XPub, v.Path)
	if e != nil {
		return v, e
	}
	var existing string
	e = tx.QueryRow("SELECT id FROM crypto_wallets WHERE receive_key=? OR xpub=?", receive, v.XPub).Scan(&existing)
	if e == nil {
		return v, commerceFail(409, "该收款分支已存在，请使用已有钱包")
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	var secret []byte
	if route == "wallets/hot" {
		if !in.RiskAck || in.EngineVersion != "4.8.4" {
			return v, commerceFail(400, "请确认热钱包风险并使用受支持的钱包核心版本")
		}
		xpub, path, first, e := cryptoHDFromMnemonic(in.Mnemonic)
		if e != nil || xpub != v.XPub || path != v.Path || !strings.EqualFold(first, in.FirstAddress) || !strings.EqualFold(first, v.FirstAddress) {
			return v, commerceFail(400, "助记词、公钥和首地址校验不一致")
		}
		v.Mode, v.Engine, v.EngineVersion = "hot", "trust-wallet-core", in.EngineVersion
		secret, e = a.store.vault.seal(cryptoWalletSecret{WalletID: v.ID, ReceiveKey: receive, Mnemonic: in.Mnemonic})
		if e != nil {
			return v, e
		}
	} else {
		if in.Mnemonic != "" {
			return v, commerceFail(400, "观察钱包只能导入扩展公钥")
		}
		v.Mode, v.Engine, v.BackupConfirmed = "watch_only", "external-xpub", true
	}
	if e = cryptoInsertWallet(tx, v, receive, secret, 0); e != nil {
		return v, e
	}
	if v.Mode == "hot" {
		var f cryptoFundingAddress
		f, e = a.store.cryptoEnsureFunding(tx, v)
		v.FundingAddress, v.FundingPath = f.Address, f.Path
	}
	return v, e
}
func cryptoInsertWallet(tx *persistence.Tx, v CryptoWallet, receive string, secret []byte, exported int64) error {
	ids, e := cryptoWalletChainIDs(v.SupportedChainIDs)
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO crypto_wallets("+cryptoWalletColumns+",receive_key,secret,backup_exported) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.ID, v.Name, v.Mode, v.XPub, v.Path, v.FirstAddress, v.Engine, v.EngineVersion, cryptoBool(v.Enabled), v.Revision, v.NextIndex, v.Created, cryptoBool(v.BackupConfirmed), cryptoBool(v.RecoveryRequired), string(jsonBytes(ids)), receive, secret, exported)
	return e
}

func cryptoInvalidDump() error {
	return commerceFail(400, "钱包备份不完整或校验失败，请使用完整加密备份")
}
