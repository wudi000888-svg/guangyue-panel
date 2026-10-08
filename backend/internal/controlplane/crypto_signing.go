package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type cryptoFundingAddress struct {
	Address string `json:"address"`
	Path    string `json:"path"`
}

// Funding uses the change branch, never any /0 receiving index. Only public
// derivation is needed to display the fixed address; viewing it never decrypts a seed.
func cryptoFunding(v CryptoWallet) (cryptoFundingAddress, error) {
	if v.Mode != "hot" {
		return cryptoFundingAddress{}, nil
	}
	key, branch, e := cryptoPublicKey(v.XPub, v.Path)
	if e != nil || branch {
		return cryptoFundingAddress{}, errors.New("热钱包账户公钥无效")
	}
	defer key.Zero()
	change, e := key.Derive(1)
	if e != nil {
		return cryptoFundingAddress{}, e
	}
	defer change.Zero()
	child, e := change.Derive(0)
	if e != nil {
		return cryptoFundingAddress{}, e
	}
	defer child.Zero()
	pub, e := child.ECPubKey()
	if e != nil {
		return cryptoFundingAddress{}, e
	}
	return cryptoFundingAddress{Address: ethcrypto.PubkeyToAddress(*pub.ToECDSA()).Hex(), Path: v.Path + "/1/0"}, nil
}
func (s *Store) cryptoEnsureFunding(tx *persistence.Tx, v CryptoWallet) (cryptoFundingAddress, error) {
	f, e := cryptoFunding(v)
	if e != nil || f.Address == "" {
		return f, e
	}
	if _, e = tx.Exec("INSERT INTO crypto_funding_addresses(wallet_id,address,path,created) VALUES(?,?,?,?) ON CONFLICT(wallet_id) DO NOTHING", v.ID, f.Address, f.Path, time.Now().Unix()); e != nil {
		return f, e
	}
	var stored cryptoFundingAddress
	if e = tx.QueryRow("SELECT address,path FROM crypto_funding_addresses WHERE wallet_id=?", v.ID).Scan(&stored.Address, &stored.Path); e != nil {
		return f, e
	}
	if stored != f {
		return f, errors.New("Gas 资金地址完整性校验失败")
	}
	return f, nil
}
func (s *Store) cryptoFundingAddress(ctx context.Context, v CryptoWallet) (cryptoFundingAddress, error) {
	tx, e := s.cryptoWalletTx(ctx)
	if e != nil {
		return cryptoFundingAddress{}, e
	}
	defer tx.Rollback()
	f, e := s.cryptoEnsureFunding(tx, v)
	if e != nil {
		return f, e
	}
	return f, tx.Commit()
}

type cryptoSignedTransfer struct {
	Raw  string
	Hash string
	From string
}

func cryptoSignTransfer(mnemonic, path string, chainID int64, nonce, gas uint64, gasPrice, value *big.Int, to string, data []byte) (cryptoSignedTransfer, error) {
	var out cryptoSignedTransfer
	if chainID != 1 && chainID != 56 {
		return out, errors.New("该网络尚不支持经过验证的自动提取")
	}
	if gasPrice == nil || value == nil || !common.IsHexAddress(to) || common.HexToAddress(to) == (common.Address{}) || gas == 0 || gas > 1000000 || gasPrice.Sign() <= 0 || value.Sign() < 0 {
		return out, errors.New("签名交易参数无效")
	}
	// Never accept arbitrary derivation paths from an HTTP request. Callers pass
	// stored /0/i paths or the independently derived /1/0 funding path only.
	parts := strings.Split(path, "/")
	if len(parts) != 6 || parts[0] != "m" || parts[1] != "44'" || parts[2] != "60'" || parts[3] != "0'" || (parts[4] != "0" && parts[4] != "1") {
		return out, errors.New("签名路径不在热钱包范围内")
	}
	index, e := cryptoAtoms(parts[5])
	if e != nil || !index.IsUint64() || index.Uint64() >= uint64(cryptoMaxIndex) || (parts[4] == "1" && index.Sign() != 0) {
		return out, errors.New("签名路径索引无效")
	}
	if _, _, _, e = cryptoHDFromMnemonic(mnemonic); e != nil {
		return out, e
	}
	seed := bip39.NewSeed(mnemonic, "")
	defer clear(seed)
	key, e := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if e != nil {
		return out, e
	}
	defer key.Zero()
	change := uint32(0)
	if parts[4] == "1" {
		change = 1
	}
	for _, i := range []uint32{44 + hdkeychain.HardenedKeyStart, 60 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, change, uint32(index.Uint64())} {
		next, e := key.Derive(i)
		if e != nil {
			return out, e
		}
		defer next.Zero()
		key = next
	}
	private, e := key.ECPrivKey()
	if e != nil {
		return out, e
	}
	defer private.Zero()
	ecdsa := private.ToECDSA()
	defer ecdsa.D.SetInt64(0)
	address := common.HexToAddress(to)
	tx := types.NewTx(&types.LegacyTx{Nonce: nonce, To: &address, Value: new(big.Int).Set(value), Gas: gas, GasPrice: new(big.Int).Set(gasPrice), Data: data})
	signed, e := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), ecdsa)
	if e != nil {
		return out, errors.New("交易签名失败")
	}
	raw, e := signed.MarshalBinary()
	if e != nil {
		return out, e
	}
	out.Raw = "0x" + hex.EncodeToString(raw)
	out.Hash = signed.Hash().Hex()
	out.From = ethcrypto.PubkeyToAddress(ecdsa.PublicKey).Hex()
	return out, nil
}
func (c *cryptoEVM) gasPrice(ctx context.Context) (*big.Int, error) {
	a, b, e := c.pair(ctx, "eth_gasPrice", []any{})
	if e != nil {
		return nil, e
	}
	var x, y string
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return nil, commerceFail(409, "Gas 报价格式无效")
	}
	p, e := cryptoHexInt(x)
	if e != nil {
		return nil, e
	}
	q, e := cryptoHexInt(y)
	if e != nil {
		return nil, e
	}
	if p.Sign() <= 0 || q.Sign() <= 0 {
		return nil, commerceFail(409, "Gas 报价无效")
	}
	lo, hi := p, q
	if p.Cmp(q) > 0 {
		lo, hi = q, p
	}
	if hi.Cmp(new(big.Int).Mul(lo, big.NewInt(2))) > 0 {
		return nil, commerceFail(409, "两个 RPC 的 Gas 价格分歧过大")
	}
	return new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(hi, big.NewInt(120)), big.NewInt(99)), big.NewInt(100)), nil
}
func (c *cryptoEVM) estimateGas(ctx context.Context, from, to, data string, value *big.Int) (uint64, error) {
	call := object{"from": from, "to": to, "value": "0x" + value.Text(16)}
	if data != "" {
		call["data"] = data
	}
	// First try a zero-price estimate: EVM execution still checks contract logic,
	// while a zero native balance does not prevent a token transfer simulation.
	call["gasPrice"] = "0x0"
	a, b, e := c.pair(ctx, "eth_estimateGas", []any{call, "latest"})
	if e != nil {
		// Only sender native balance changes; token storage/code remain canonical.
		delete(call, "gasPrice")
		a, b, e = c.pair(ctx, "eth_estimateGas", []any{call, "latest", object{from: object{"balance": "0x" + strings.Repeat("f", 64)}}})
	}
	if e != nil {
		return 0, commerceFail(409, "RPC 无法执行真实 Gas 估算，请配置支持零费率或状态覆盖估算的 RPC")
	}
	var x, y string
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return 0, commerceFail(409, "Gas 数量无效")
	}
	p, e := cryptoHexU64(x)
	if e != nil {
		return 0, e
	}
	q, e := cryptoHexU64(y)
	if e != nil {
		return 0, e
	}
	n := max(p, q)
	if n < 21000 || n > 800000 {
		return 0, commerceFail(409, "Gas 估算超出支持范围")
	}
	return (n*120 + 99) / 100, nil
}
