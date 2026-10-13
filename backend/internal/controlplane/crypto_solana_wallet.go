package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type cryptoSolanaPublic struct {
	First   string
	Funding cryptoFundingAddress
}
type cryptoSolanaPublicProof struct {
	WalletID    string             `json:"wallet_id"`
	ReceiveHash string             `json:"receive_hash"`
	Public      cryptoSolanaPublic `json:"public"`
}

func (s *Store) cryptoSolanaPublicByID(q cryptoWalletQuery, id string) (cryptoSolanaPublic, error) {
	var p cryptoSolanaPublic
	var sealed []byte
	var receiveHash string
	e := q.QueryRow("SELECT p.first_address,p.funding_address,p.funding_path,p.doc,h.receive_hash FROM crypto_solana_wallet_public p JOIN crypto_wallet_history h ON h.id=p.wallet_id WHERE p.wallet_id=?", id).Scan(&p.First, &p.Funding.Address, &p.Funding.Path, &sealed, &receiveHash)
	if e != nil {
		return p, e
	}
	var proof cryptoSolanaPublicProof
	if s.vault.open(sealed, &proof) != nil || proof.WalletID != id || proof.ReceiveHash != receiveHash || proof.Public != p || !cryptoSolanaAddress(p.First) || !cryptoSolanaAddress(p.Funding.Address) || p.Funding.Path != cryptoSolanaPath(cryptoSolanaFundingIndex) {
		return p, errors.New("Solana 钱包公开地址完整性校验失败")
	}
	return p, nil
}
func (s *Store) cryptoEnsureSolanaPublic(tx *persistence.Tx, w CryptoWallet) (cryptoSolanaPublic, error) {
	var p cryptoSolanaPublic
	if w.Mode != "hot" {
		return p, commerceFail(409, "Solana 地址全硬化派生，外部 EVM 公钥钱包不支持此网络")
	}
	secret, e := s.cryptoSecret(tx, w)
	if e != nil {
		return p, e
	}
	defer func() { secret.Mnemonic = "" }()
	p.First, e = cryptoSolanaDerive(secret.Mnemonic, 0)
	if e != nil {
		return p, e
	}
	p.Funding.Address, e = cryptoSolanaDerive(secret.Mnemonic, cryptoSolanaFundingIndex)
	if e != nil {
		return p, e
	}
	p.Funding.Path = cryptoSolanaPath(cryptoSolanaFundingIndex)
	receive, e := cryptoReceiveXPub(w.XPub, w.Path)
	if e != nil {
		return p, e
	}
	sealed, e := s.vault.seal(cryptoSolanaPublicProof{w.ID, digest(receive), p})
	if e != nil {
		return p, e
	}
	if _, e = tx.Exec("INSERT INTO crypto_solana_wallet_public(wallet_id,first_address,funding_address,funding_path,doc,created) VALUES(?,?,?,?,?,?) ON CONFLICT(wallet_id) DO NOTHING", w.ID, p.First, p.Funding.Address, p.Funding.Path, sealed, time.Now().Unix()); e != nil {
		return p, e
	}
	stored, e := s.cryptoSolanaPublicByID(tx, w.ID)
	if e != nil {
		return p, e
	}
	if stored != p {
		return p, errors.New("Solana 钱包公开派生记录校验失败")
	}
	return p, nil
}
func (s *Store) cryptoSolanaFundingAddress(ctx context.Context, w CryptoWallet) (cryptoFundingAddress, error) {
	p, e := s.cryptoSolanaPublicByID(s.db, w.ID)
	if e == nil {
		return p.Funding, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return p.Funding, e
	}
	tx, e := s.cryptoWalletTx(ctx)
	if e != nil {
		return p.Funding, e
	}
	defer tx.Rollback()
	p, e = s.cryptoEnsureSolanaPublic(tx, w)
	if e != nil {
		return p.Funding, e
	}
	return p.Funding, tx.Commit()
}
func (s *Store) cryptoWalletSolanaDTO(q cryptoWalletQuery, w CryptoWallet) (CryptoWallet, error) {
	if !cryptoWalletSupports(w, cryptoSolanaChainID) {
		return w, nil
	}
	if w.Mode != "hot" {
		return w, commerceFail(409, "Solana 需要热钱包助记词，外部 EVM 公钥无法派生")
	}
	p, e := s.cryptoSolanaPublicByID(q, w.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return w, nil
	}
	if e != nil {
		return w, e
	}
	w.SolanaFirstAddress = p.First
	w.SolanaFundingAddress = p.Funding.Address
	w.SolanaFundingPath = p.Funding.Path
	return w, nil
}
func (s *Store) cryptoWalletDerive(q cryptoWalletQuery, w CryptoWallet, index int64, sol bool) (string, string, error) {
	if index < 0 || index >= int64(cryptoSolanaFundingIndex) {
		return "", "", errors.New("收款地址索引已用尽")
	}
	if !sol {
		v, e := cryptoDeriveAddress(w.XPub, w.Path, uint32(index))
		return v, cryptoFullPath(w.Path, index), e
	}
	secret, e := s.cryptoSecret(q, w)
	if e != nil {
		return "", "", e
	}
	defer func() { secret.Mnemonic = "" }()
	address, e := cryptoSolanaDerive(secret.Mnemonic, uint32(index))
	return address, cryptoSolanaPath(uint32(index)), e
}
func (s *Store) cryptoCheckWalletAddress(q cryptoWalletQuery, w CryptoWallet, index int64, path, address string) error {
	if cryptoIsSolanaPath(path) {
		if e := s.cryptoSolanaAddressProof(q, w, index, path, address); e != nil {
			return e
		}
	}
	derived, canonical, e := s.cryptoWalletDerive(q, w, index, cryptoIsSolanaPath(path))
	if e != nil || derived != address || canonical != path {
		return errors.New("钱包地址与派生记录不一致")
	}
	return nil
}
func cryptoSolanaAddressIndex(path string) (uint32, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != "m" || parts[1] != "44'" || parts[2] != "501'" || parts[4] != "0'" || !strings.HasSuffix(parts[3], "'") {
		return 0, errors.New("Solana 签名路径无效")
	}
	n := strings.TrimSuffix(parts[3], "'")
	v, e := strconv.ParseUint(n, 10, 31)
	if e != nil || strconv.FormatUint(v, 10) != n {
		return 0, errors.New("Solana 签名索引无效")
	}
	return uint32(v), nil
}
func (a *App) cryptoSolanaSetupPreflight(ctx context.Context, chain CryptoChain, assets []CryptoAsset, wallet CryptoWallet) error {
	if wallet.Mode != "hot" {
		return commerceFail(409, "Solana 需要热钱包，外部 EVM 公钥无法派生")
	}
	c, e := newCryptoSolana(chain, a.cfg.Dev)
	if e != nil {
		return e
	}
	head, e := c.Finalized(ctx)
	if e != nil {
		return e
	}
	for _, asset := range assets {
		if asset.ChainID == cryptoSolanaChainID && asset.Enabled {
			if e = c.ValidateToken(ctx, asset.Contract, asset.Decimals, head.Number); e != nil {
				return e
			}
		}
	}
	_, e = a.store.cryptoSolanaFundingAddress(ctx, wallet)
	return e
}

type cryptoSolanaAddressProof struct {
	ReceiveHash string              `json:"receive_hash"`
	Address     CryptoWalletAddress `json:"address"`
}

func (s *Store) cryptoSealSolanaAddress(tx *persistence.Tx, w CryptoWallet, address CryptoWalletAddress) error {
	receive, e := cryptoReceiveXPub(w.XPub, w.Path)
	if e != nil {
		return e
	}
	doc, e := s.vault.seal(cryptoSolanaAddressProof{digest(receive), address})
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO crypto_solana_address_proofs(address_id,doc) VALUES(?,?) ON CONFLICT(address_id) DO NOTHING", address.ID, doc)
	return e
}
func (s *Store) cryptoSolanaAddressProof(q cryptoWalletQuery, w CryptoWallet, index int64, path, address string) error {
	receive, e := cryptoReceiveXPub(w.XPub, w.Path)
	if e != nil {
		return e
	}
	var doc []byte
	var id, wallet string
	e = q.QueryRow("SELECT a.id,a.wallet_id,p.doc FROM crypto_wallet_addresses a JOIN crypto_solana_address_proofs p ON p.address_id=a.id WHERE a.wallet_id IN ("+cryptoWalletOwnerScope+") AND a.address_index=? AND a.address=? AND a.path=?", w.ID, index, address, path).Scan(&id, &wallet, &doc)
	if e != nil {
		return errors.New("Solana 历史公开地址凭据缺失")
	}
	var proof cryptoSolanaAddressProof
	if s.vault.open(doc, &proof) != nil || proof.ReceiveHash != digest(receive) || proof.Address.ID != id || proof.Address.WalletID != wallet || proof.Address.Index != index || proof.Address.Path != path || proof.Address.Address != address || path != cryptoSolanaPath(uint32(index)) || !cryptoSolanaAddress(address) {
		return errors.New("Solana 历史公开地址凭据校验失败")
	}
	return nil
}
