package controlplane

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const cryptoTransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

type cryptoEVM struct {
	chain  CryptoChain
	client *http.Client
}
type cryptoEVMBlock struct {
	Number    uint64 `json:"number"`
	Hash      string `json:"hash"`
	Timestamp uint64 `json:"timestamp"`
}
type cryptoEVMLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber uint64   `json:"block_number"`
	BlockHash   string   `json:"block_hash"`
	TxHash      string   `json:"tx_hash"`
	Index       uint64   `json:"index"`
	Removed     bool     `json:"removed"`
}
type cryptoEVMReceipt struct {
	TxHash      string         `json:"tx_hash"`
	BlockHash   string         `json:"block_hash"`
	BlockNumber uint64         `json:"block_number"`
	From        string         `json:"from"`
	To          string         `json:"to"`
	Status      uint64         `json:"status"`
	Logs        []cryptoEVMLog `json:"logs"`
}
type cryptoRPCLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockNumber     string   `json:"blockNumber"`
	BlockHash       string   `json:"blockHash"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        string   `json:"logIndex"`
	Removed         bool     `json:"removed"`
}

var cryptoProductionRPCTransport = &http.Transport{DialContext: publicSourceDial, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second}
var cryptoDevelopmentRPCTransport = &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second}

func newCryptoEVM(chain CryptoChain, dev bool) (*cryptoEVM, error) {
	if chain.ChainID <= 0 {
		return nil, commerceFail(409, "链编号无效")
	}
	for _, address := range []string{chain.RPCURL, chain.RPCBackupURL} {
		u, e := url.Parse(address)
		if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(dev && u.Scheme == "http")) {
			return nil, commerceFail(409, "请配置两个独立的 HTTPS RPC")
		}
		if !dev && validatePublicSourceURL(address) != nil {
			return nil, commerceFail(409, "RPC 必须使用公网 HTTPS 地址")
		}
	}
	a, _ := url.Parse(chain.RPCURL)
	b, _ := url.Parse(chain.RPCBackupURL)
	if chain.RPCURL == chain.RPCBackupURL || (!dev && strings.EqualFold(a.Hostname(), b.Hostname())) {
		return nil, commerceFail(409, "需要两个不同供应商的 RPC 地址")
	}
	tr := cryptoProductionRPCTransport
	if dev {
		tr = cryptoDevelopmentRPCTransport
	}
	return &cryptoEVM{chain: chain, client: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return commerceFail(409, "RPC 不允许重定向") }}}, nil
}
func (c *cryptoEVM) rpc(ctx context.Context, endpoint, method string, params any, out any) error {
	r, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBytes(object{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})))
	if e != nil {
		return commerceFail(409, "RPC 请求无效")
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := c.client.Do(r)
	if e != nil {
		return commerceFail(409, "RPC 连接失败，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return commerceFail(409, "RPC 暂时不可用")
	}
	var v struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if e != nil || len(body) > 4<<20 {
		return commerceFail(409, "RPC 响应超过限制")
	}
	if json.Unmarshal(body, &v) != nil || v.ID != 1 || v.JSONRPC != "2.0" || v.Error != nil || len(v.Result) == 0 {
		return commerceFail(409, "RPC 方法不可用或返回无效数据")
	}
	if json.Unmarshal(v.Result, out) != nil {
		return commerceFail(409, "RPC 响应格式无效")
	}
	return nil
}
func (c *cryptoEVM) pair(ctx context.Context, method string, params any) (json.RawMessage, json.RawMessage, error) {
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 2)
	for _, endpoint := range []string{c.chain.RPCURL, c.chain.RPCBackupURL} {
		go func(endpoint string) {
			var raw json.RawMessage
			e := c.rpc(ctx, endpoint, method, params, &raw)
			ch <- result{raw, e}
		}(endpoint)
	}
	a, b := <-ch, <-ch
	if a.err != nil {
		return nil, nil, a.err
	}
	if b.err != nil {
		return nil, nil, b.err
	}
	return a.raw, b.raw, nil
}
func cryptoHexInt(s string) (*big.Int, error) {
	if len(s) < 3 || !strings.HasPrefix(s, "0x") || len(s) > 66 {
		return nil, commerceFail(409, "链上整数无效")
	}
	for _, r := range s[2:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return nil, commerceFail(409, "链上整数无效")
		}
	}
	v, ok := new(big.Int).SetString(s[2:], 16)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, commerceFail(409, "链上整数无效")
	}
	return v, nil
}
func cryptoHexU64(s string) (uint64, error) {
	v, e := cryptoHexInt(s)
	if e != nil || !v.IsUint64() {
		return 0, commerceFail(409, "链上数量超出范围")
	}
	return v.Uint64(), nil
}
func cryptoHex(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }
func cryptoAtoms(s string) (*big.Int, error) {
	if s == "" || len(s) > 78 || (len(s) > 1 && s[0] == '0') {
		return nil, commerceFail(409, "金额必须为规范非负整数")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return nil, commerceFail(409, "金额必须为规范非负整数")
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.BitLen() > 256 {
		return nil, commerceFail(409, "金额超出范围")
	}
	return v, nil
}
func cryptoHash(s string) bool {
	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return false
	}
	_, e := hex.DecodeString(s[2:])
	return e == nil
}
func (c *cryptoEVM) Chain(ctx context.Context) error {
	a, b, e := c.pair(ctx, "eth_chainId", []any{})
	if e != nil {
		return e
	}
	for _, raw := range []json.RawMessage{a, b} {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return commerceFail(409, "RPC 链编号无效")
		}
		n, e := cryptoHexU64(s)
		if e != nil || n != uint64(c.chain.ChainID) {
			return commerceFail(409, "RPC 链编号与配置不一致")
		}
	}
	_, e = c.block(ctx, "0x0")
	return e
}
func decodeCryptoBlock(raw json.RawMessage) (cryptoEVMBlock, error) {
	var v struct {
		Number    string `json:"number"`
		Hash      string `json:"hash"`
		Timestamp string `json:"timestamp"`
	}
	var out cryptoEVMBlock
	if json.Unmarshal(raw, &v) != nil || !cryptoHash(v.Hash) {
		return out, commerceFail(409, "RPC 区块无效")
	}
	var e error
	out.Number, e = cryptoHexU64(v.Number)
	if e != nil {
		return out, e
	}
	out.Timestamp, e = cryptoHexU64(v.Timestamp)
	out.Hash = strings.ToLower(v.Hash)
	return out, e
}
func (c *cryptoEVM) block(ctx context.Context, tag string) (cryptoEVMBlock, error) {
	a, b, e := c.pair(ctx, "eth_getBlockByNumber", []any{tag, false})
	if e != nil {
		return cryptoEVMBlock{}, e
	}
	x, e := decodeCryptoBlock(a)
	if e != nil {
		return x, e
	}
	y, e := decodeCryptoBlock(b)
	if e != nil {
		return y, e
	}
	if x != y {
		return x, commerceFail(409, "两个 RPC 的规范区块不一致，已暂停资金操作")
	}
	return x, nil
}
func (c *cryptoEVM) boundary(ctx context.Context, tag string) (cryptoEVMBlock, error) {
	if e := c.Chain(ctx); e != nil {
		return cryptoEVMBlock{}, e
	}
	a, b, e := c.pair(ctx, "eth_getBlockByNumber", []any{tag, false})
	if e != nil {
		return cryptoEVMBlock{}, e
	}
	x, e := decodeCryptoBlock(a)
	if e != nil {
		return x, e
	}
	y, e := decodeCryptoBlock(b)
	if e != nil {
		return y, e
	}
	n := min(x.Number, y.Number)
	return c.block(ctx, cryptoHex(n))
}
func (c *cryptoEVM) Latest(ctx context.Context) (cryptoEVMBlock, error) {
	return c.boundary(ctx, "latest")
}
func (c *cryptoEVM) Finalized(ctx context.Context) (cryptoEVMBlock, error) {
	// L2 node finalized tags alone are not an independently verified L1 batch proof.
	if c.chain.ChainID == 10 || c.chain.ChainID == 8453 || c.chain.ChainID == 42161 {
		return cryptoEVMBlock{}, commerceFail(409, "该 L2 尚缺少可核验的 L1 最终性证据，自动资金操作未启用")
	}
	if !c.chain.FinalityVerified {
		return cryptoEVMBlock{}, commerceFail(409, "尚未验证链最终性配置")
	}
	return c.boundary(ctx, "finalized")
}
func (c *cryptoEVM) quantity(ctx context.Context, method string, params any) (*big.Int, error) {
	a, b, e := c.pair(ctx, method, params)
	if e != nil {
		return nil, e
	}
	var x, y string
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return nil, commerceFail(409, "RPC 金额格式无效")
	}
	vx, e := cryptoHexInt(x)
	if e != nil {
		return nil, e
	}
	vy, e := cryptoHexInt(y)
	if e != nil {
		return nil, e
	}
	if vx.Cmp(vy) != 0 {
		return nil, commerceFail(409, "两个 RPC 的金额或 nonce 不一致")
	}
	return vx, nil
}
func (c *cryptoEVM) NativeBalance(ctx context.Context, address string, block uint64) (*big.Int, error) {
	if !common.IsHexAddress(address) {
		return nil, commerceFail(409, "地址无效")
	}
	return c.quantity(ctx, "eth_getBalance", []any{address, cryptoHex(block)})
}
func cryptoABIAddress(address string) string {
	return strings.Repeat("0", 24) + strings.ToLower(strings.TrimPrefix(address, "0x"))
}
func (c *cryptoEVM) TokenBalance(ctx context.Context, contract, address string, block uint64) (*big.Int, error) {
	if !common.IsHexAddress(contract) || !common.IsHexAddress(address) {
		return nil, commerceFail(409, "代币或地址无效")
	}
	return c.quantity(ctx, "eth_call", []any{object{"to": contract, "data": "0x70a08231" + cryptoABIAddress(address)}, cryptoHex(block)})
}
func (c *cryptoEVM) ValidateToken(ctx context.Context, contract string, decimals int, block uint64) error {
	if !common.IsHexAddress(contract) || decimals < 0 || decimals > 18 {
		return commerceFail(409, "代币合约配置无效")
	}
	if e := c.Chain(ctx); e != nil {
		return e
	}
	a, b, e := c.pair(ctx, "eth_getCode", []any{contract, cryptoHex(block)})
	if e != nil {
		return e
	}
	var x, y string
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || len(x) < 4 || !strings.HasPrefix(x, "0x") || !strings.EqualFold(x, y) {
		return commerceFail(409, "两个 RPC 的代币合约代码为空或不一致")
	}
	if _, e = hex.DecodeString(x[2:]); e != nil {
		return commerceFail(409, "代币代码无效")
	}
	v, e := c.quantity(ctx, "eth_call", []any{object{"to": contract, "data": "0x313ce567"}, cryptoHex(block)})
	if e != nil {
		return e
	}
	if !v.IsInt64() || v.Int64() != int64(decimals) {
		return commerceFail(409, "代币 decimals 与已批准配置不一致")
	}
	return nil
}
func (c *cryptoEVM) PendingNonce(ctx context.Context, address string) (uint64, error) {
	v, e := c.quantity(ctx, "eth_getTransactionCount", []any{address, "pending"})
	if e != nil {
		return 0, e
	}
	if !v.IsUint64() || v.BitLen() > 63 {
		return 0, commerceFail(409, "nonce 超出范围")
	}
	return v.Uint64(), nil
}
func decodeCryptoLog(v cryptoRPCLog) (cryptoEVMLog, error) {
	x := cryptoEVMLog{Address: strings.ToLower(v.Address), Topics: v.Topics, Data: strings.ToLower(v.Data), BlockHash: strings.ToLower(v.BlockHash), TxHash: strings.ToLower(v.TransactionHash), Removed: v.Removed}
	var e error
	if !common.IsHexAddress(v.Address) || !cryptoHash(v.BlockHash) || !cryptoHash(v.TransactionHash) {
		return x, commerceFail(409, "RPC 日志身份无效")
	}
	for i := range x.Topics {
		if !cryptoHash(x.Topics[i]) {
			return x, commerceFail(409, "RPC 日志主题无效")
		}
		x.Topics[i] = strings.ToLower(x.Topics[i])
	}
	x.BlockNumber, e = cryptoHexU64(v.BlockNumber)
	if e != nil {
		return x, e
	}
	x.Index, e = cryptoHexU64(v.LogIndex)
	return x, e
}
func decodeCryptoLogs(raw json.RawMessage) ([]cryptoEVMLog, error) {
	var source []cryptoRPCLog
	if json.Unmarshal(raw, &source) != nil || source == nil {
		return nil, commerceFail(409, "RPC 日志列表无效")
	}
	out := make([]cryptoEVMLog, 0, len(source))
	for _, v := range source {
		x, e := decodeCryptoLog(v)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BlockNumber != out[j].BlockNumber {
			return out[i].BlockNumber < out[j].BlockNumber
		}
		return out[i].Index < out[j].Index
	})
	return out, nil
}
func (c *cryptoEVM) Transfers(ctx context.Context, contract, to string, fromBlock, toBlock uint64) ([]cryptoEVMLog, error) {
	if !common.IsHexAddress(contract) || !common.IsHexAddress(to) || toBlock < fromBlock || toBlock-fromBlock > 1000 {
		return nil, commerceFail(409, "转账扫描范围无效或超过1000块")
	}
	a, b, e := c.pair(ctx, "eth_getLogs", []any{object{"address": contract, "fromBlock": cryptoHex(fromBlock), "toBlock": cryptoHex(toBlock), "topics": []any{cryptoTransferTopic, nil, "0x" + cryptoABIAddress(to)}}})
	if e != nil {
		return nil, e
	}
	x, e := decodeCryptoLogs(a)
	if e != nil {
		return nil, e
	}
	y, e := decodeCryptoLogs(b)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(jsonBytes(x), jsonBytes(y)) {
		return nil, commerceFail(409, "两个 RPC 的转账日志不一致")
	}
	for _, v := range x {
		if v.Removed || v.BlockNumber < fromBlock || v.BlockNumber > toBlock || v.Address != strings.ToLower(contract) || len(v.Topics) != 3 || v.Topics[0] != cryptoTransferTopic || v.Topics[2] != "0x"+cryptoABIAddress(to) || len(v.Data) != 66 {
			return nil, commerceFail(409, "RPC 返回了不匹配的转账日志")
		}
		if _, e := cryptoHexInt(v.Data); e != nil {
			return nil, e
		}
	}
	return x, nil
}
func decodeCryptoReceipt(raw json.RawMessage) (*cryptoEVMReceipt, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var v struct {
		TransactionHash string          `json:"transactionHash"`
		BlockHash       string          `json:"blockHash"`
		BlockNumber     string          `json:"blockNumber"`
		From            string          `json:"from"`
		To              json.RawMessage `json:"to"`
		Status          string          `json:"status"`
		Logs            []cryptoRPCLog  `json:"logs"`
	}
	if json.Unmarshal(raw, &v) != nil || !cryptoHash(v.TransactionHash) || !cryptoHash(v.BlockHash) || !common.IsHexAddress(v.From) || v.Logs == nil {
		return nil, commerceFail(409, "RPC 交易凭据无效")
	}
	// Contract creation has an explicit null target. Its constructor may transfer
	// ERC-20 tokens; payment attribution comes from verified Transfer logs.
	// Sweep verification independently requires the signed destination to match.
	var destination string
	if string(v.To) != "null" {
		if json.Unmarshal(v.To, &destination) != nil || !common.IsHexAddress(destination) {
			return nil, commerceFail(409, "RPC 交易凭据目标无效")
		}
	}
	out := &cryptoEVMReceipt{TxHash: strings.ToLower(v.TransactionHash), BlockHash: strings.ToLower(v.BlockHash), From: strings.ToLower(v.From), To: strings.ToLower(destination), Logs: []cryptoEVMLog{}}
	var e error
	out.BlockNumber, e = cryptoHexU64(v.BlockNumber)
	if e != nil {
		return nil, e
	}
	out.Status, e = cryptoHexU64(v.Status)
	if e != nil || out.Status > 1 {
		return nil, commerceFail(409, "交易状态无效")
	}
	seen := map[uint64]bool{}
	for _, l := range v.Logs {
		x, e := decodeCryptoLog(l)
		if e != nil || x.Removed || x.TxHash != out.TxHash || x.BlockNumber != out.BlockNumber || x.BlockHash != out.BlockHash || seen[x.Index] {
			return nil, commerceFail(409, "receipt 内日志身份不一致")
		}
		seen[x.Index] = true
		out.Logs = append(out.Logs, x)
	}
	return out, nil
}
func (c *cryptoEVM) Receipt(ctx context.Context, hash string) (*cryptoEVMReceipt, error) {
	if !cryptoHash(hash) {
		return nil, commerceFail(409, "交易哈希无效")
	}
	a, b, e := c.pair(ctx, "eth_getTransactionReceipt", []any{hash})
	if e != nil {
		return nil, e
	}
	x, e := decodeCryptoReceipt(a)
	if e != nil {
		return nil, e
	}
	y, e := decodeCryptoReceipt(b)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(jsonBytes(x), jsonBytes(y)) {
		return nil, commerceFail(409, "两个 RPC 的完整交易凭据不一致")
	}
	if x != nil && x.TxHash != strings.ToLower(hash) {
		return nil, commerceFail(409, "交易凭据哈希不匹配")
	}
	return x, nil
}
func (c *cryptoEVM) FinalReceipt(ctx context.Context, hash string) (*cryptoEVMReceipt, error) {
	r, e := c.Receipt(ctx, hash)
	if e != nil || r == nil {
		return r, e
	}
	f, e := c.Finalized(ctx)
	if e != nil {
		return nil, e
	}
	if r.BlockNumber > f.Number {
		return nil, nil
	}
	b, e := c.block(ctx, cryptoHex(r.BlockNumber))
	if e != nil {
		return nil, e
	}
	if b.Hash != r.BlockHash {
		return nil, errCryptoFinality
	}
	return r, nil
}
func (c *cryptoEVM) ConfirmedReceipt(ctx context.Context, hash string) (*cryptoEVMReceipt, error) {
	r, e := c.FinalReceipt(ctx, hash)
	if e != nil || r == nil {
		return r, e
	}
	if r.Status != 1 {
		return nil, commerceFail(409, "链上交易执行失败")
	}
	return r, nil
}
func (c *cryptoEVM) ConfirmedTransfers(ctx context.Context, contract, to string, fromBlock, toBlock uint64) ([]cryptoEVMLog, error) {
	f, e := c.Finalized(ctx)
	if e != nil {
		return nil, e
	}
	if toBlock > f.Number {
		return nil, commerceFail(409, "扫描范围尚未最终确认")
	}
	logs, e := c.Transfers(ctx, contract, to, fromBlock, toBlock)
	if e != nil {
		return nil, e
	}
	receipts := map[string]*cryptoEVMReceipt{}
	for _, l := range logs {
		r, ok := receipts[l.TxHash]
		if !ok {
			r, e = c.ConfirmedReceipt(ctx, l.TxHash)
			if e != nil || r == nil {
				if e == nil {
					e = commerceFail(409, "最终转账缺少完整凭据")
				}
				return nil, e
			}
			receipts[l.TxHash] = r
		}
		found := false
		for _, v := range r.Logs {
			if bytes.Equal(jsonBytes(v), jsonBytes(l)) {
				found = true
				break
			}
		}
		if !found {
			return nil, commerceFail(409, "扫描日志不在完整 receipt 中")
		}
	}
	return logs, nil
}
func (c *cryptoEVM) Broadcast(ctx context.Context, raw, expected string) error {
	if e := c.Chain(ctx); e != nil {
		return e
	}
	var hash string
	e := c.rpc(ctx, c.chain.RPCURL, "eth_sendRawTransaction", []any{raw}, &hash)
	if e == nil && strings.EqualFold(hash, expected) {
		return nil
	}
	hash = ""
	e = c.rpc(ctx, c.chain.RPCBackupURL, "eth_sendRawTransaction", []any{raw}, &hash)
	if e == nil && strings.EqualFold(hash, expected) {
		return nil
	}
	return commerceFail(409, "广播结果尚未确认；将查询并重播同一签名交易，不重复转账")
}
func cryptoTransferData(destination string, amount *big.Int) string {
	return "0xa9059cbb" + cryptoABIAddress(destination) + fmt.Sprintf("%064x", amount)
}
