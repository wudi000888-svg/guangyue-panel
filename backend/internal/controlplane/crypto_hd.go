package controlplane

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/sha3"
)

const cryptoWalletCoreVersion = "4.8.4"
const cryptoHotPath = "m/44'/60'/0'"

// Ethereum-compatible networks use the same BIP44 coin type and address. The
// allocation namespace is the receiving branch, never a network or token.
var cryptoAccountPath = regexp.MustCompile(`^m/44'/60'/(0|[1-9][0-9]{0,9})'(/0)?$`)

func cryptoParsePath(path string) (uint32, bool, error) {
	match := cryptoAccountPath.FindStringSubmatch(path)
	if match == nil {
		return 0, false, errors.New("仅支持以太坊 BIP44 账户公钥或接收分支公钥")
	}
	account, err := strconv.ParseUint(match[1], 10, 31)
	if err != nil {
		return 0, false, errors.New("钱包账户编号超出范围")
	}
	return uint32(account), match[2] != "", nil
}

func cryptoPublicKey(xpub, path string) (*hdkeychain.ExtendedKey, bool, error) {
	account, branch, err := cryptoParsePath(path)
	if err != nil {
		return nil, false, err
	}
	// Explicit xpub only: never accept xprv, SLIP132, testnet, or a master key.
	if len(xpub) != 111 || !strings.HasPrefix(xpub, "xpub") {
		return nil, false, errors.New("请导入有效的 xpub 公钥，不能导入私钥或助记词")
	}
	key, err := hdkeychain.NewKeyFromString(xpub)
	if err != nil || key.IsPrivate() || !key.IsForNet(&chaincfg.MainNetParams) {
		return nil, false, errors.New("扩展公钥校验失败")
	}
	depth, child := uint8(3), account+hdkeychain.HardenedKeyStart
	if branch {
		depth, child = 4, 0
	}
	if key.Depth() != depth || key.ChildIndex() != child {
		key.Zero()
		return nil, false, errors.New("公钥深度或账户编号与派生路径不一致")
	}
	if _, err = key.ECPubKey(); err != nil {
		key.Zero()
		return nil, false, errors.New("扩展公钥曲线校验失败")
	}
	return key, branch, nil
}

func cryptoValidateXPub(xpub, path string) error {
	key, _, err := cryptoPublicKey(xpub, path)
	if err == nil {
		key.Zero()
	}
	return err
}

// The canonical receiving branch makes an account xpub and its /0 xpub share
// one unique identity, preventing overlapping address pools on the same site.
func cryptoReceiveXPub(xpub, path string) (string, error) {
	key, branch, err := cryptoPublicKey(xpub, path)
	if err != nil {
		return "", err
	}
	defer key.Zero()
	if branch {
		return key.String(), nil
	}
	receive, err := key.Derive(0)
	if err != nil {
		return "", errors.New("接收分支派生失败")
	}
	defer receive.Zero()
	return receive.String(), nil
}

func cryptoDeriveAddress(xpub, path string, index uint32) (string, error) {
	if index >= hdkeychain.HardenedKeyStart {
		return "", errors.New("接收地址索引已用尽")
	}
	key, branch, err := cryptoPublicKey(xpub, path)
	if err != nil {
		return "", err
	}
	defer key.Zero()
	if !branch {
		receive, err := key.Derive(0)
		if err != nil {
			return "", errors.New("接收分支派生失败")
		}
		defer receive.Zero()
		key = receive
	}
	child, err := key.Derive(index)
	if err != nil {
		// Do not silently skip an invalid child here: the allocator owns indexes.
		return "", errors.New("收款地址派生失败")
	}
	defer child.Zero()
	public, err := child.ECPubKey()
	if err != nil {
		return "", errors.New("收款地址公钥无效")
	}
	hash := sha3.NewLegacyKeccak256()
	hash.Write(public.SerializeUncompressed()[1:])
	address := hex.EncodeToString(hash.Sum(nil)[12:])
	// EIP55 checksum is independent of network; ERC20 contract and chain are
	// separate payment intent fields, not part of an HD address descriptor.
	hash.Reset()
	hash.Write([]byte(address))
	checksum := hash.Sum(nil)
	result := []byte(address)
	for i, c := range result {
		nibble := checksum[i/2] >> 4
		if i%2 == 1 {
			nibble = checksum[i/2] & 15
		}
		if c >= 'a' && c <= 'f' && nibble >= 8 {
			result[i] -= 'a' - 'A'
		}
	}
	return "0x" + string(result), nil
}

// Wallet Core creates the wallet in the browser. Independently validating its
// BIP39/BIP32 descriptor here prevents a corrupted client from persisting a seed
// that cannot recover the advertised addresses. No BIP39 passphrase is used.
func cryptoHDFromMnemonic(mnemonic string) (xpub, path, firstAddress string, err error) {
	words := strings.Fields(mnemonic)
	if len(mnemonic) > 400 || (len(words) != 12 && len(words) != 24) || strings.Join(words, " ") != mnemonic || !bip39.IsMnemonicValid(mnemonic) {
		return "", "", "", errors.New("助记词校验失败，仅支持无附加密码的标准英文助记词")
	}
	seed := bip39.NewSeed(mnemonic, "")
	defer func() {
		for i := range seed {
			seed[i] = 0
		}
	}()
	key, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return "", "", "", errors.New("钱包主密钥生成失败")
	}
	defer key.Zero()
	for _, child := range []uint32{44 + hdkeychain.HardenedKeyStart, 60 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart} {
		next, e := key.Derive(child)
		if e != nil {
			return "", "", "", errors.New("钱包账户派生失败")
		}
		defer next.Zero()
		key = next
	}
	public, err := key.Neuter()
	if err != nil {
		return "", "", "", errors.New("钱包公钥生成失败")
	}
	// Neuter shares some buffers with the private key. Serialize before any
	// deferred zeroing, and do not zero either until derivation is complete.
	defer public.Zero()
	xpub, path = public.String(), cryptoHotPath
	firstAddress, err = cryptoDeriveAddress(xpub, path, 0)
	return xpub, path, firstAddress, err
}
