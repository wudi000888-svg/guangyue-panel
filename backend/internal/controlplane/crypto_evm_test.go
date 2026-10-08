package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func cryptoRPCAnswers(t *testing.T, answer func(string, []json.RawMessage) any) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		v := answer(req.Method, req.Params)
		if e, ok := v.(error); ok {
			_ = e
			_ = json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "id": 1, "error": object{"code": -32602}})
			return
		}
		_ = json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "id": 1, "result": v})
	}))
	t.Cleanup(s.Close)
	return s
}
func TestCryptoEVMDivergenceAndFinalityGates(t *testing.T) {
	for _, mode := range []string{"chain", "genesis", "block", "balance", "receipt", "missing_finality", "l2"} {
		t.Run(mode, func(t *testing.T) {
			answer := func(backup bool) func(string, []json.RawMessage) any {
				return func(method string, params []json.RawMessage) any {
					switch method {
					case "eth_chainId":
						if backup && mode == "chain" {
							return "0x38"
						}
						return "0x1"
					case "eth_getBlockByNumber":
						var tag string
						_ = json.Unmarshal(params[0], &tag)
						if mode == "missing_finality" && tag == "finalized" {
							return nil
						}
						n := uint64(100)
						if strings.HasPrefix(tag, "0x") {
							n, _ = cryptoHexU64(tag)
						}
						hash := cryptoFixtureHash(n)
						if backup && ((mode == "genesis" && n == 0) || (mode == "block" && n == 100)) {
							hash = cryptoFixtureHash(n + 500)
						}
						return object{"number": cryptoHex(n), "hash": hash, "timestamp": "0x1"}
					case "eth_getBalance":
						if backup {
							return "0x2"
						}
						return "0x1"
					case "eth_getTransactionReceipt":
						if !backup {
							return nil
						}
						return object{"transactionHash": cryptoFixtureHash(150), "blockHash": cryptoFixtureHash(100), "blockNumber": "0x64", "from": "0x1111111111111111111111111111111111111111", "to": "0x2222222222222222222222222222222222222222", "status": "0x1", "logs": []object{}}
					}
					return nil
				}
			}
			p, b := cryptoRPCAnswers(t, answer(false)), cryptoRPCAnswers(t, answer(true))
			chain := CryptoChain{ChainID: 1, FinalityVerified: true, RPCURL: p.URL, RPCBackupURL: b.URL}
			if mode == "l2" {
				chain.ChainID = 8453
			}
			c, e := newCryptoEVM(chain, true)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "balance":
				_, e = c.NativeBalance(context.Background(), "0x1111111111111111111111111111111111111111", 100)
			case "receipt":
				_, e = c.Receipt(context.Background(), cryptoFixtureHash(150))
			default:
				_, e = c.Finalized(context.Background())
			}
			if e == nil {
				t.Fatal("divergent or unsupported RPC accepted")
			}
		})
	}
}
func TestCryptoEstimateFallbackAndPriceCap(t *testing.T) {
	answer := func(method string, params []json.RawMessage) any {
		switch method {
		case "eth_estimateGas":
			if len(params) < 3 {
				return commerceFail(409, "requires override")
			}
			return "0xc350"
		case "eth_gasPrice":
			return "0x64"
		}
		return nil
	}
	p := cryptoRPCAnswers(t, answer)
	b := cryptoRPCAnswers(t, func(method string, params []json.RawMessage) any {
		switch method {
		case "eth_estimateGas":
			if len(params) < 3 {
				return commerceFail(409, "requires override")
			}
			return "0xc350"
		case "eth_gasPrice":
			return "0x200"
		}
		return nil
	})
	c, e := newCryptoEVM(CryptoChain{ChainID: 1, RPCURL: p.URL, RPCBackupURL: b.URL}, true)
	if e != nil {
		t.Fatal(e)
	}
	gas, e := c.estimateGas(context.Background(), "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222", "0xdead", big.NewInt(0))
	if e != nil || gas != 60000 {
		t.Fatal(gas, e)
	}
	if _, e = c.gasPrice(context.Background()); e == nil {
		t.Fatal("large price divergence accepted")
	}
}
func TestCryptoSweepPriceIncreaseWaitsWithoutSigning(t *testing.T) {
	a, owner, v, f := cryptoSweepFixture(t)
	p := cryptoSweepTestPreview(t, a, owner, v)
	j := cryptoSweepTestCreate(t, a, owner, v, p)
	f.mu.Lock()
	f.price = 200
	f.mu.Unlock()
	if e := a.cryptoSweepWork(context.Background()); e == nil || cryptoSweepTestCount(t, a) != 0 {
		t.Fatal("signed above quote", e)
	}
	out, e := a.store.cryptoSweepJob(j.ID)
	if e != nil || out.State != "waiting" || !out.CanCancel {
		t.Fatal(out, e)
	}
	f.mu.Lock()
	f.price = 100
	f.mu.Unlock()
	if e = a.cryptoSweepWork(context.Background()); e != nil || cryptoSweepTestCount(t, a) != 1 {
		t.Fatal("failed to resume original budget", e)
	}
}

func TestCryptoConstructorReceiptSettlesInvoice(t *testing.T) {
	a, owner, user, _, fixture := cryptoPaymentFixture(t)
	// Keep the scanner fixture while making its enclosing transaction a constructor.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := httptest.NewRecorder()
		fixture.serve(response, r)
		var body map[string]any
		if json.Unmarshal(response.Body.Bytes(), &body) != nil {
			w.WriteHeader(500)
			return
		}
		if receipt, ok := body["result"].(map[string]any); ok && receipt["transactionHash"] != nil {
			receipt["to"] = nil
			receipt["contractAddress"] = "0x3333333333333333333333333333333333333333"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	primary, backup := httptest.NewServer(handler), httptest.NewServer(handler)
	t.Cleanup(primary.Close)
	t.Cleanup(backup.Close)
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	settings.Chains[0].RPCURL, settings.Chains[0].RPCBackupURL = primary.URL, backup.URL
	sealed, e := a.store.vault.seal(settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", sealed); e != nil {
		t.Fatal(e)
	}
	order, invoice := cryptoPaymentInvoice(t, a, owner, user)
	fixture.add(invoice, 101, invoice.ExpectedAtoms)
	fixture.mu.Lock()
	fixture.latest, fixture.final = 102, 102
	fixture.logs[0].Topics[1] = "0x" + cryptoABIAddress("0x3333333333333333333333333333333333333333")
	fixture.mu.Unlock()
	current, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = a.scanCryptoInvoice(context.Background(), current); e != nil {
			t.Fatal("constructor transfer blocked scanner", e)
		}
		if e = a.paymentWork(); e != nil {
			t.Fatal(e)
		}
		if e = a.commerceWork(time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	completed, e := a.store.order(order.ID)
	if e != nil || completed.State != "completed" {
		t.Fatal(completed.State, e)
	}
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", order.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("constructor payment not exactly once", count, e)
	}
	assertLedger(t, a)
}
func TestCryptoReceiptNullTargetDoesNotRelaxSweepIntent(t *testing.T) {
	receipt := object{"transactionHash": cryptoFixtureHash(150), "blockHash": cryptoFixtureHash(100), "blockNumber": "0x64", "from": "0x1111111111111111111111111111111111111111", "to": nil, "status": "0x1", "logs": []object{}}
	decoded, e := decodeCryptoReceipt(jsonBytes(receipt))
	if e != nil || decoded == nil || decoded.To != "" {
		t.Fatal("null creation target rejected", decoded, e)
	}
	for _, invalid := range []any{"", "not-an-address", 123} {
		receipt["to"] = invalid
		if _, e = decodeCryptoReceipt(jsonBytes(receipt)); e == nil {
			t.Fatal("invalid target accepted", invalid)
		}
	}
	delete(receipt, "to")
	if _, e = decodeCryptoReceipt(jsonBytes(receipt)); e == nil {
		t.Fatal("missing target accepted")
	}
	target := "0x2222222222222222222222222222222222222222"
	signed, e := cryptoSignTransfer(cryptoTestMnemonic, "m/44'/60'/0'/0/0", 1, 0, 21000, big.NewInt(1), big.NewInt(1), target, nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := hex.DecodeString(strings.TrimPrefix(signed.Raw, "0x"))
	if e != nil {
		t.Fatal(e)
	}
	tx := cryptoChainTransaction{ChainID: 1, From: signed.From, To: target, Amount: "1", Nonce: 0, Hash: signed.Hash, Raw: raw, GasLimit: 21000, GasPrice: "1"}
	decoded.From, decoded.TxHash = signed.From, signed.Hash
	if e = (&cryptoEVM{}).validateSignedReceipt(context.Background(), tx, decoded); e == nil {
		t.Fatal("null target satisfied signed sweep intent")
	}
}
