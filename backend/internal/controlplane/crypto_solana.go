package controlplane

// Solana is not an EVM chain: public keys and transaction signatures are case
// sensitive base58 values, amounts are lamports, and every payment is finalized.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/tyler-smith/go-bip39"
)

const cryptoSolanaChainID int64 = 101
const cryptoSolanaGenesis = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
const cryptoSolanaUSDC = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
const cryptoSolanaUSDT = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
const cryptoSolanaTokenProgram = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
const cryptoSolanaATAProgram = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
const cryptoSolanaSystemProgram = "11111111111111111111111111111111"
const cryptoSolanaFundingIndex = uint32(2147483647)

func cryptoSolanaAddress(v string) bool {
	return len(v) >= 32 && len(v) <= 44 && len(base58.Decode(v)) == 32 && base58.Encode(base58.Decode(v)) == v
}
func cryptoSolanaSignature(v string) bool {
	return len(v) >= 85 && len(v) <= 88 && len(base58.Decode(v)) == 64 && base58.Encode(base58.Decode(v)) == v
}
func cryptoSolanaPath(index uint32) string {
	return "m/44'/501'/" + strconv.FormatUint(uint64(index), 10) + "'/0'"
}
func cryptoIsSolanaPath(v string) bool { return strings.HasPrefix(v, "m/44'/501'/") }

// SLIP-0010 ed25519 uses hardened children only. An Ethereum xpub cannot derive
// these keys; the encrypted hot-wallet mnemonic is required for allocation.
func cryptoSolanaKey(mnemonic string, index uint32) (ed25519.PrivateKey, error) {
	if index >= 1<<31 || !bip39.IsMnemonicValid(mnemonic) {
		return nil, errors.New("Solana 助记词或派生索引无效")
	}
	seed := bip39.NewSeed(mnemonic, "")
	defer clear(seed)
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	key := mac.Sum(nil)
	defer clear(key)
	for _, i := range []uint32{44, 501, index, 0} {
		data := make([]byte, 37)
		copy(data[1:33], key[:32])
		binary.BigEndian.PutUint32(data[33:], i|1<<31)
		mac = hmac.New(sha512.New, key[32:])
		mac.Write(data)
		next := mac.Sum(nil)
		clear(data)
		clear(key)
		key = next
		defer clear(next)
	}
	return ed25519.NewKeyFromSeed(key[:32]), nil
}
func cryptoSolanaDerive(mnemonic string, index uint32) (string, error) {
	k, e := cryptoSolanaKey(mnemonic, index)
	if e != nil {
		return "", e
	}
	defer clear(k)
	return base58.Encode(k.Public().(ed25519.PublicKey)), nil
}

// Edwards decompression determines whether a candidate PDA lies on curve.
func cryptoSolanaOnCurve(raw []byte) bool {
	if len(raw) != 32 {
		return false
	}
	buf := append([]byte(nil), raw...)
	sign := buf[31] >> 7
	buf[31] &= 127
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	y := new(big.Int).SetBytes(buf)
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	if y.Cmp(p) >= 0 {
		return false
	}
	d := new(big.Int).ModInverse(big.NewInt(121666), p)
	d.Mul(d, big.NewInt(-121665))
	d.Mod(d, p)
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, p)
	num := new(big.Int).Sub(y2, big.NewInt(1))
	den := new(big.Int).Mul(d, y2)
	den.Add(den, big.NewInt(1))
	den = new(big.Int).ModInverse(den, p)
	if den == nil {
		return false
	}
	x2 := new(big.Int).Mul(num, den)
	x2.Mod(x2, p)
	if x2.Sign() == 0 {
		return sign == 0
	}
	exponent := new(big.Int).Rsh(new(big.Int).Sub(p, big.NewInt(1)), 1)
	return new(big.Int).Exp(x2, exponent, p).Cmp(big.NewInt(1)) == 0
}
func cryptoSolanaATA(owner, mint string) (string, error) {
	if !cryptoSolanaAddress(owner) || !cryptoSolanaAddress(mint) {
		return "", errors.New("Solana 地址无效")
	}
	for bump := 255; bump >= 0; bump-- {
		h := sha256.New()
		h.Write(base58.Decode(owner))
		h.Write(base58.Decode(cryptoSolanaTokenProgram))
		h.Write(base58.Decode(mint))
		h.Write([]byte{byte(bump)})
		h.Write(base58.Decode(cryptoSolanaATAProgram))
		h.Write([]byte("ProgramDerivedAddress"))
		raw := h.Sum(nil)
		if !cryptoSolanaOnCurve(raw) {
			return base58.Encode(raw), nil
		}
	}
	return "", errors.New("Solana 代币账户派生失败")
}

type cryptoSolana struct {
	*cryptoEVM
	observedSlot atomic.Uint64
}

func newCryptoSolana(chain CryptoChain, dev bool) (*cryptoSolana, error) {
	if chain.ChainID != cryptoSolanaChainID {
		return nil, errors.New("Solana 网络编号无效")
	}
	c, e := newCryptoRPCClient(chain, dev)
	if e != nil {
		return nil, e
	}
	return &cryptoSolana{cryptoEVM: c}, nil
}

var cryptoSolanaGenesisCache sync.Map

func (c *cryptoSolana) observeSlot(n uint64) {
	if n == 0 {
		return
	}
	for {
		old := c.observedSlot.Load()
		if old != 0 && old <= n {
			return
		}
		if c.observedSlot.CompareAndSwap(old, n) {
			return
		}
	}
}
func (c *cryptoSolana) Chain(ctx context.Context) error {
	a, b, e := c.pair(ctx, "getGenesisHash", []any{})
	if e != nil {
		return e
	}
	for _, raw := range []json.RawMessage{a, b} {
		var hash string
		if json.Unmarshal(raw, &hash) != nil || hash != cryptoSolanaGenesis {
			return commerceFail(409, "Solana RPC 必须连接主网，已阻止资金操作")
		}
	}
	return nil
}
func (c *cryptoSolana) chainCachedForGas(ctx context.Context) error {
	key := digest(c.chain.RPCURL + "|" + c.chain.RPCBackupURL)
	if until, ok := cryptoSolanaGenesisCache.Load(key); ok && time.Now().Before(until.(time.Time)) {
		return nil
	}
	if e := c.Chain(ctx); e != nil {
		return e
	}
	cryptoSolanaGenesisCache.Store(key, time.Now().Add(15*time.Second))
	return nil
}
func cryptoSolanaSame(a, b json.RawMessage, out any) error {
	var x, y any
	dx := json.NewDecoder(bytes.NewReader(a))
	dx.UseNumber()
	dy := json.NewDecoder(bytes.NewReader(b))
	dy.UseNumber()
	if dx.Decode(&x) != nil || dy.Decode(&y) != nil || string(jsonBytes(x)) != string(jsonBytes(y)) {
		return commerceFail(409, "两个 Solana RPC 的最终确认数据不一致")
	}
	return json.Unmarshal(a, out)
}

// Compare the complete consumed safety tuple, not provider-added diagnostics.
// Every integer is kept exact, including raw nested RPC execution errors.
func cryptoSolanaTypedSame(a, b json.RawMessage, out any) error {
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("Solana 响应类型无效")
	}
	other := reflect.New(value.Elem().Type()).Interface()
	normalize := func(raw json.RawMessage, target any) ([]byte, error) {
		if e := json.Unmarshal(raw, target); e != nil {
			return nil, e
		}
		var normalized any
		decoder := json.NewDecoder(bytes.NewReader(jsonBytes(target)))
		decoder.UseNumber()
		if e := decoder.Decode(&normalized); e != nil {
			return nil, e
		}
		return jsonBytes(normalized), nil
	}
	x, e := normalize(a, out)
	if e != nil {
		return e
	}
	y, e := normalize(b, other)
	if e != nil {
		return e
	}
	if !bytes.Equal(x, y) {
		return commerceFail(409, "两个 Solana RPC 的最终交易安全数据不一致")
	}
	return nil
}

func (c *cryptoSolana) Finalized(ctx context.Context) (cryptoEVMBlock, error) {
	if e := c.Chain(ctx); e != nil {
		return cryptoEVMBlock{}, e
	}
	a, b, e := c.pair(ctx, "getSlot", []any{object{"commitment": "finalized"}})
	if e != nil {
		return cryptoEVMBlock{}, e
	}
	var x, y uint64
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return cryptoEVMBlock{}, errors.New("Solana 最终槽位无效")
	}
	slot := min(x, y)
	return c.block(ctx, slot)
}
func (c *cryptoSolana) block(ctx context.Context, slot uint64) (cryptoEVMBlock, error) {
	a, b, e := c.pair(ctx, "getBlock", []any{slot, object{"commitment": "finalized", "transactionDetails": "none", "rewards": false, "maxSupportedTransactionVersion": 0}})
	if e != nil {
		return cryptoEVMBlock{}, e
	}
	var out struct {
		Blockhash string `json:"blockhash"`
		BlockTime *int64 `json:"blockTime"`
	}
	if e = cryptoSolanaSame(a, b, &out); e != nil || !cryptoSolanaAddress(out.Blockhash) || out.BlockTime == nil || *out.BlockTime <= 0 {
		return cryptoEVMBlock{}, commerceFail(409, "Solana 最终区块核验失败")
	}
	return cryptoEVMBlock{Number: slot, Hash: out.Blockhash, Timestamp: uint64(*out.BlockTime)}, nil
}
func (c *cryptoSolana) account(ctx context.Context, address string, slot uint64) (json.RawMessage, error) {
	if !cryptoSolanaAddress(address) {
		return nil, errors.New("Solana 地址无效")
	}
	a, b, e := c.pair(ctx, "getAccountInfo", []any{address, object{"encoding": "jsonParsed", "commitment": "finalized", "minContextSlot": slot}})
	if e != nil {
		return nil, e
	}
	var x, y struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x.Context.Slot < slot || y.Context.Slot < slot {
		return nil, errors.New("Solana 账户快照过旧")
	}
	var v any
	if e = cryptoSolanaSame(x.Value, y.Value, &v); e != nil {
		return nil, e
	}
	return x.Value, nil
}
func (c *cryptoSolana) NativeBalance(ctx context.Context, address string, slot uint64) (*big.Int, error) {
	if !cryptoSolanaAddress(address) {
		return nil, errors.New("Solana 余额地址无效")
	}
	a, b, e := c.pair(ctx, "getBalance", []any{address, object{"commitment": "finalized", "minContextSlot": slot}})
	if e != nil {
		return nil, e
	}
	var x, y struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value *uint64 `json:"value"`
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x.Context.Slot == 0 || y.Context.Slot == 0 || x.Context.Slot < slot || y.Context.Slot < slot || x.Value == nil || y.Value == nil || *x.Value != *y.Value {
		return nil, errors.New("两个 Solana RPC 的最终余额不一致")
	}
	c.observeSlot(min(x.Context.Slot, y.Context.Slot))
	return new(big.Int).SetUint64(*x.Value), nil
}
func (c *cryptoSolana) ValidateToken(ctx context.Context, mint string, decimals int, slot uint64) error {
	if (mint != cryptoSolanaUSDC && mint != cryptoSolanaUSDT) || decimals != 6 {
		return errors.New("Solana 仅支持官方 USDT 与 USDC")
	}
	raw, e := c.account(ctx, mint, slot)
	if e != nil {
		return e
	}
	var v struct {
		Owner string `json:"owner"`
		Data  struct {
			Parsed struct {
				Type string `json:"type"`
				Info struct {
					Decimals    int  `json:"decimals"`
					Initialized bool `json:"isInitialized"`
				} `json:"info"`
			} `json:"parsed"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Owner != cryptoSolanaTokenProgram || v.Data.Parsed.Type != "mint" || !v.Data.Parsed.Info.Initialized || v.Data.Parsed.Info.Decimals != decimals {
		return errors.New("Solana 官方代币元数据核验失败")
	}
	return nil
}
func (c *cryptoSolana) TokenBalance(ctx context.Context, mint, owner string, slot uint64) (*big.Int, error) {
	accounts, e := c.tokenAccounts(ctx, mint, owner, slot)
	if e != nil {
		return nil, e
	}
	total := new(big.Int)
	for _, v := range accounts {
		n, _ := cryptoAtoms(v.Amount)
		total.Add(total, n)
	}
	return total, nil
}

func cryptoSolanaShort(v int) []byte {
	out := []byte{}
	for {
		b := byte(v & 127)
		v >>= 7
		if v != 0 {
			b |= 128
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

type cryptoSolanaMeta struct {
	address  string
	writable bool
}
type cryptoSolanaInstruction struct {
	program  string
	accounts []cryptoSolanaMeta
	data     []byte
}

// Merge trusted account roles then order writable/read-only signers and
// non-signers according to the canonical legacy message header.
func cryptoSolanaMessage(feePayer, source, blockhash string, instructions []cryptoSolanaInstruction) ([]byte, error) {
	type role struct {
		address          string
		signer, writable bool
	}
	ordered := []string{}
	roles := map[string]*role{}
	add := func(a string, signer, writable bool) {
		if r := roles[a]; r != nil {
			r.signer = r.signer || signer
			r.writable = r.writable || writable
		} else {
			roles[a] = &role{a, signer, writable}
			ordered = append(ordered, a)
		}
	}
	add(feePayer, true, true)
	add(source, true, false)
	for _, ix := range instructions {
		add(ix.program, false, false)
		for _, a := range ix.accounts {
			add(a.address, a.address == source || a.address == feePayer, a.writable)
		}
	}
	for _, ix := range instructions {
		r := roles[ix.program]
		if r.signer || r.writable {
			return nil, errors.New("Solana 程序不能作为签名或可写账户")
		}
	}
	keys := []string{}
	readonlySigners, readonlyOthers, signers := 0, 0, 0
	for group := 0; group < 4; group++ {
		for _, a := range ordered {
			r := roles[a]
			g := 3
			if r.signer && r.writable {
				g = 0
			} else if r.signer {
				g = 1
			} else if r.writable {
				g = 2
			}
			if g == group {
				keys = append(keys, a)
				if r.signer {
					signers++
					if !r.writable {
						readonlySigners++
					}
				} else if !r.writable {
					readonlyOthers++
				}
			}
		}
	}
	if len(keys) > 64 || len(keys) == 0 || keys[0] != feePayer {
		return nil, errors.New("Solana 指令账户配置无效")
	}
	message := []byte{byte(signers), byte(readonlySigners), byte(readonlyOthers)}
	message = append(message, cryptoSolanaShort(len(keys))...)
	for _, k := range keys {
		if !cryptoSolanaAddress(k) {
			return nil, errors.New("Solana 指令地址无效")
		}
		message = append(message, base58.Decode(k)...)
	}
	if !cryptoSolanaAddress(blockhash) {
		return nil, errors.New("Solana 区块哈希无效")
	}
	message = append(message, base58.Decode(blockhash)...)
	message = append(message, cryptoSolanaShort(len(instructions))...)
	index := func(a string) byte {
		for i, k := range keys {
			if k == a {
				return byte(i)
			}
		}
		panic("unknown Solana account")
	}
	for _, ix := range instructions {
		message = append(message, index(ix.program))
		message = append(message, cryptoSolanaShort(len(ix.accounts))...)
		for _, a := range ix.accounts {
			message = append(message, index(a.address))
		}
		message = append(message, cryptoSolanaShort(len(ix.data))...)
		message = append(message, ix.data...)
	}
	return message, nil
}
func cryptoSolanaNativeMessage(feePayer, source, to, hash string, amount uint64) ([]byte, error) {
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data, 2)
	binary.LittleEndian.PutUint64(data[4:], amount)
	return cryptoSolanaMessage(feePayer, source, hash, []cryptoSolanaInstruction{{cryptoSolanaSystemProgram, []cryptoSolanaMeta{{source, true}, {to, true}}, data}})
}
func (c *cryptoSolana) latestBlockhash(ctx context.Context) (string, uint64, error) {
	return c.recentBlockhash(ctx, true)
}
func (c *cryptoSolana) recentBlockhash(ctx context.Context, requireValidity bool) (string, uint64, error) {
	a, b, e := c.pair(ctx, "getLatestBlockhash", []any{object{"commitment": "finalized"}})
	if e != nil {
		return "", 0, e
	}
	var x, y struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value struct {
			Blockhash            string `json:"blockhash"`
			LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
		} `json:"value"`
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || !cryptoSolanaAddress(x.Value.Blockhash) || !cryptoSolanaAddress(y.Value.Blockhash) || x.Value.LastValidBlockHeight == 0 || y.Value.LastValidBlockHeight == 0 || x.Context.Slot == 0 || y.Context.Slot == 0 || x.Value.LastValidBlockHeight > 1<<63-1 || y.Value.LastValidBlockHeight > 1<<63-1 || x.Context.Slot > 1<<63-1 || y.Context.Slot > 1<<63-1 {
		return "", 0, errors.New("Solana 最近区块哈希无效")
	}
	if x.Value.Blockhash == y.Value.Blockhash && x.Value.LastValidBlockHeight != y.Value.LastValidBlockHeight {
		return "", 0, errors.New("相同 Solana 区块哈希的有效期不一致")
	}
	chosen := x
	if y.Context.Slot < x.Context.Slot {
		chosen = y
	}
	if requireValidity && x.Value.Blockhash != y.Value.Blockhash {
		aa, bb, e := c.pair(ctx, "isBlockhashValid", []any{chosen.Value.Blockhash, object{"commitment": "finalized"}})
		if e != nil {
			return "", 0, e
		}
		var u, v struct {
			Value bool `json:"value"`
		}
		if json.Unmarshal(aa, &u) != nil || json.Unmarshal(bb, &v) != nil || !u.Value || !v.Value {
			return "", 0, errors.New("Solana 两个来源尚未共同确认交易区块哈希")
		}
	}
	c.observeSlot(chosen.Context.Slot)
	return chosen.Value.Blockhash, chosen.Value.LastValidBlockHeight, nil
}
func (c *cryptoSolana) Fee(ctx context.Context, message []byte) (*big.Int, error) {
	a, b, e := c.pair(ctx, "getFeeForMessage", []any{base64.StdEncoding.EncodeToString(message), object{"commitment": "finalized"}})
	if e != nil {
		return nil, e
	}
	var x, y struct {
		Value *uint64 `json:"value"`
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x.Value == nil || y.Value == nil || *x.Value != *y.Value || *x.Value == 0 {
		return nil, errors.New("Solana 手续费预估失效，请重新刷新")
	}
	return new(big.Int).SetUint64(*x.Value), nil
}
func (c *cryptoSolana) ReferenceFee(ctx context.Context, address string) (*big.Int, error) {
	hash, _, e := c.recentBlockhash(ctx, false)
	if e != nil {
		return nil, e
	}
	m, e := cryptoSolanaNativeMessage(address, address, cryptoSolanaUSDC, hash, 1)
	if e != nil {
		return nil, e
	}
	return c.Fee(ctx, m)
}

type cryptoSolanaTokenAccount struct {
	Address string `json:"address"`
	Amount  string `json:"amount"`
}

func (c *cryptoSolana) tokenAccounts(ctx context.Context, mint, owner string, slot uint64) ([]cryptoSolanaTokenAccount, error) {
	if !cryptoSolanaAddress(owner) || (mint != cryptoSolanaUSDT && mint != cryptoSolanaUSDC) {
		return nil, errors.New("Solana 币种或钱包地址无效")
	}
	a, b, e := c.pair(ctx, "getTokenAccountsByOwner", []any{owner, object{"mint": mint}, object{"encoding": "jsonParsed", "commitment": "finalized", "minContextSlot": slot}})
	if e != nil {
		return nil, e
	}
	decode := func(raw json.RawMessage) ([]cryptoSolanaTokenAccount, error) {
		var v struct {
			Context struct {
				Slot uint64 `json:"slot"`
			} `json:"context"`
			Value *[]struct {
				Pubkey  string `json:"pubkey"`
				Account struct {
					Owner string `json:"owner"`
					Data  struct {
						Parsed struct {
							Type string `json:"type"`
							Info struct {
								Owner       string `json:"owner"`
								Mint        string `json:"mint"`
								State       string `json:"state"`
								TokenAmount struct {
									Amount   string `json:"amount"`
									Decimals int    `json:"decimals"`
								} `json:"tokenAmount"`
							} `json:"info"`
						} `json:"parsed"`
					} `json:"data"`
				} `json:"account"`
			} `json:"value"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Context.Slot < slot || v.Value == nil || len(*v.Value) > 100 {
			return nil, errors.New("Solana 代币账户快照无效或超限")
		}
		out := []cryptoSolanaTokenAccount{}
		seen := map[string]bool{}
		for _, x := range *v.Value {
			info := x.Account.Data.Parsed.Info
			if !cryptoSolanaAddress(x.Pubkey) || seen[x.Pubkey] || x.Account.Owner != cryptoSolanaTokenProgram || x.Account.Data.Parsed.Type != "account" || info.Owner != owner || info.Mint != mint || info.TokenAmount.Decimals != 6 || info.State != "initialized" {
				return nil, errors.New("Solana 代币账户所有权核验失败")
			}
			n, e := cryptoAtoms(info.TokenAmount.Amount)
			if e != nil || !n.IsUint64() {
				return nil, errors.New("Solana 代币数量无效")
			}
			seen[x.Pubkey] = true
			out = append(out, cryptoSolanaTokenAccount{x.Pubkey, n.String()})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
		return out, nil
	}
	x, e := decode(a)
	if e != nil {
		return nil, e
	}
	y, e := decode(b)
	if e != nil {
		return nil, e
	}
	if string(jsonBytes(x)) != string(jsonBytes(y)) {
		return nil, errors.New("两个 Solana RPC 的代币余额不一致")
	}
	return x, nil
}
