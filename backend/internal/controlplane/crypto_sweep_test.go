package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

type cryptoSweepRPCFixture struct {
	mu                                sync.Mutex
	app                               *App
	native, tokens                    map[string]*big.Int
	txs                               map[string]*types.Transaction
	receipts                          map[string]object
	latest, final                     uint64
	chain                             int64
	price                             int64
	sends                             int
	acceptedError, wrongNonce, revert bool
	durabilityFailed                  bool
	funding, receive, contract        string
}

func (f *cryptoSweepRPCFixture) balance(values map[string]*big.Int, address string) *big.Int {
	if n := values[strings.ToLower(address)]; n != nil {
		return new(big.Int).Set(n)
	}
	return new(big.Int)
}
func (f *cryptoSweepRPCFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(400)
		return
	}
	str := func(i int) string { var s string; _ = json.Unmarshal(req.Params[i], &s); return s }
	var result any
	switch req.Method {
	case "eth_chainId":
		result = cryptoHex(uint64(f.chain))
	case "eth_getBlockByNumber":
		tag := str(0)
		n := uint64(0)
		if tag == "latest" {
			n = f.latest
		} else if tag == "finalized" {
			n = f.final
		} else {
			n, _ = cryptoHexU64(tag)
		}
		result = object{"number": cryptoHex(n), "hash": cryptoFixtureHash(n), "timestamp": cryptoHex(uint64(time.Now().Unix()))}
	case "eth_getCode":
		result = "0x60016000"
	case "eth_call":
		var call struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(req.Params[0], &call)
		if call.Data == "0x313ce567" {
			result = "0x6"
		} else {
			result = "0x" + f.balance(f.tokens, "0x"+call.Data[len(call.Data)-40:]).Text(16)
		}
	case "eth_getBalance":
		result = "0x" + f.balance(f.native, str(0)).Text(16)
	case "eth_gasPrice":
		result = cryptoHex(uint64(f.price))
	case "eth_estimateGas":
		var call struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(req.Params[0], &call)
		result = "0x5208"
		if call.Data != "" {
			result = cryptoHex(50000)
		}
	case "eth_getTransactionCount":
		result = "0x0"
	case "eth_getTransactionReceipt":
		result = f.receipts[str(0)]
		if f.receipts[str(0)] == nil {
			result = nil
		}
	case "eth_getTransactionByHash":
		hash := str(0)
		tx := f.txs[hash]
		if tx != nil && f.receipts[hash] != nil {
			from, _ := types.Sender(types.NewEIP155Signer(tx.ChainId()), tx)
			nonce := tx.Nonce()
			if f.wrongNonce {
				nonce++
			}
			result = object{"hash": hash, "from": from.Hex(), "to": tx.To().Hex(), "nonce": cryptoHex(nonce), "value": "0x" + tx.Value().Text(16), "input": "0x" + hex.EncodeToString(tx.Data()), "chainId": cryptoHex(tx.ChainId().Uint64()), "blockHash": f.receipts[hash]["blockHash"], "blockNumber": f.receipts[hash]["blockNumber"]}
		}
	case "eth_sendRawTransaction":
		raw, _ := hex.DecodeString(strings.TrimPrefix(str(0), "0x"))
		var tx types.Transaction
		if tx.UnmarshalBinary(raw) != nil {
			w.WriteHeader(500)
			return
		}
		hash := tx.Hash().Hex()
		var count int
		if e := f.app.store.db.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions WHERE tx_hash=? AND raw=?", hash, raw).Scan(&count); e != nil || count != 1 {
			f.durabilityFailed = true
		}
		f.txs[hash] = &tx
		f.sends++
		result = hash
		if f.acceptedError {
			w.WriteHeader(503)
			return
		}
	default:
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "id": 1, "result": result})
}
func (f *cryptoSweepRPCFixture) mine(final bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest++
	for hash, tx := range f.txs {
		if f.receipts[hash] != nil {
			continue
		}
		from, _ := types.Sender(types.NewEIP155Signer(tx.ChainId()), tx)
		status := "0x1"
		if f.revert {
			status = "0x0"
		}
		logs := []object{}
		if !f.revert {
			if len(tx.Data()) == 0 {
				f.native[strings.ToLower(tx.To().Hex())] = new(big.Int).Add(f.balance(f.native, tx.To().Hex()), tx.Value())
			} else {
				data := tx.Data()
				dest := "0x" + hex.EncodeToString(data[16:36])
				amount := new(big.Int).SetBytes(data[36:68])
				f.tokens[strings.ToLower(from.Hex())] = new(big.Int).Sub(f.balance(f.tokens, from.Hex()), amount)
				f.tokens[strings.ToLower(dest)] = new(big.Int).Add(f.balance(f.tokens, dest), amount)
				logs = append(logs, object{"address": f.contract, "topics": []string{cryptoTransferTopic, "0x" + cryptoABIAddress(from.Hex()), "0x" + cryptoABIAddress(dest)}, "data": fmt.Sprintf("0x%064x", amount), "blockNumber": cryptoHex(f.latest), "blockHash": cryptoFixtureHash(f.latest), "transactionHash": hash, "logIndex": "0x0", "removed": false})
			}
		}
		f.receipts[hash] = object{"transactionHash": hash, "blockHash": cryptoFixtureHash(f.latest), "blockNumber": cryptoHex(f.latest), "from": from.Hex(), "to": tx.To().Hex(), "status": status, "logs": logs}
	}
	if final {
		f.final = f.latest
	}
}
func cryptoSweepFixture(t *testing.T) (*App, Record, CryptoWallet, *cryptoSweepRPCFixture) {
	t.Helper()
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v, _ := cryptoTestCreate(t, a, owner, true)
	v = cryptoTestConfirm(t, a, owner, v)
	alloc := cryptoTestAllocate(t, a, owner, v)
	v = alloc.Wallet
	funding, e := a.store.cryptoFundingAddress(context.Background(), v)
	if e != nil {
		t.Fatal(e)
	}
	f := &cryptoSweepRPCFixture{app: a, native: map[string]*big.Int{strings.ToLower(funding.Address): big.NewInt(1000000000000)}, tokens: map[string]*big.Int{strings.ToLower(alloc.Address.Address): big.NewInt(2000000)}, txs: map[string]*types.Transaction{}, receipts: map[string]object{}, latest: 100, final: 100, chain: 1, price: 100, funding: funding.Address, receive: alloc.Address.Address}
	p := httptest.NewServer(http.HandlerFunc(f.serve))
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(p.Close)
	t.Cleanup(b.Close)
	s := cryptoDefaultSettings()
	s.Revision = 1
	s.Enabled = true
	s.WalletID = v.ID
	s.Chains[0].Enabled = true
	s.Chains[0].FinalityVerified = true
	s.Chains[0].RPCURL = p.URL
	s.Chains[0].RPCBackupURL = b.URL
	s.Assets[1].Enabled = true
	f.contract = s.Assets[1].Contract
	raw, e := a.store.vault.seal(s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,1,?)", raw); e != nil {
		t.Fatal(e)
	}
	return a, owner, v, f
}
func cryptoSweepTestInput() cryptoSweepInput {
	return cryptoSweepInput{ChainID: "ethereum", AssetID: "ethereum-usdc", Destination: "0x2222222222222222222222222222222222222222", MinAtoms: "1", MaxGasAtoms: "100000000000"}
}
func cryptoSweepTestPreview(t *testing.T, a *App, owner Record, v CryptoWallet) CryptoSweepPreview {
	t.Helper()
	p, e := a.cryptoPreviewSweep(context.Background(), owner, v.ID, cryptoSweepTestInput())
	if e != nil || !p.CanSubmit {
		t.Fatalf("preview failed %+v %v", p, e)
	}
	return p
}
func cryptoSweepTestCreate(t *testing.T, a *App, owner Record, v CryptoWallet, p CryptoSweepPreview) CryptoSweepJob {
	t.Helper()
	j, e := a.cryptoCreateSweep(context.Background(), owner, v.ID, cryptoSweepInput{Quote: p.Quote, OperationID: randomToken(24)})
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func cryptoSweepTestCount(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if e := a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_chain_transactions").Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestCryptoSweepDurableGasConfirmationAndRestart(t *testing.T) {
	a, owner, v, f := cryptoSweepFixture(t)
	p := cryptoSweepTestPreview(t, a, owner, v)
	if len(p.Items) != 1 || p.Items[0].TopupAtoms == "0" || p.TotalGasAtoms == p.TotalTopupAtoms {
		t.Fatal("invalid fee/topup estimate", p)
	}
	j := cryptoSweepTestCreate(t, a, owner, v, p)
	f.acceptedError = true
	if e := a.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	if cryptoSweepTestCount(t, a) != 1 || f.durabilityFailed {
		t.Fatal("not durable before first broadcast")
	}
	b := cryptoIndependentApp(t, a)
	if e := b.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	if cryptoSweepTestCount(t, a) != 1 || len(f.txs) != 1 {
		t.Fatal("unknown send generated another transaction")
	}
	if e := a.cryptoCancelSweep(context.Background(), owner, j.ID, cryptoSweepInput{OperationID: randomToken(24)}); e == nil {
		t.Fatal("cancelled unknown broadcast")
	}
	f.acceptedError = false
	f.mine(false)
	if e := b.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	if cryptoSweepTestCount(t, a) != 1 {
		t.Fatal("token sent before gas finality")
	}
	f.mu.Lock()
	f.final = f.latest
	f.mu.Unlock()
	if e := b.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	if cryptoSweepTestCount(t, a) != 2 {
		t.Fatal("missing token transaction")
	}
	f.mine(true)
	if e := b.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := b.cryptoSweepWork(context.Background()); e != nil {
		t.Fatal(e)
	}
	out, e := a.store.cryptoSweepJob(j.ID)
	if e != nil || out.State != "complete" || f.durabilityFailed {
		t.Fatal(out, e)
	}
	replay, e := a.cryptoCreateSweep(context.Background(), owner, v.ID, cryptoSweepInput{Quote: p.Quote, OperationID: randomToken(24)})
	if e != nil || replay.ID != j.ID || replay.State != "complete" {
		t.Fatal("completed quote was consumed again", replay, e)
	}
	for _, newline := range []string{"\n", "\r\n"} {
		alternate := p.Quote[:16] + newline + p.Quote[16:]
		if _, e := a.cryptoCreateSweep(context.Background(), owner, v.ID, cryptoSweepInput{Quote: alternate, OperationID: randomToken(24)}); e == nil {
			t.Fatal("noncanonical base64 bypassed single-use quote")
		}
	}
	if f.balance(f.tokens, p.Destination).String() != "2000000" {
		t.Fatal("wrong final token balance")
	}
}
func TestCryptoSweepConcurrentCreateLeaseAndCancel(t *testing.T) {
	a, owner, v, _ := cryptoSweepFixture(t)
	p := cryptoSweepTestPreview(t, a, owner, v)
	b := cryptoIndependentApp(t, a)
	in := cryptoSweepInput{Quote: p.Quote, OperationID: randomToken(24)}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	for _, app := range []*App{a, b} {
		wg.Add(1)
		go func(app *App) {
			defer wg.Done()
			j, e := app.cryptoCreateSweep(context.Background(), owner, v.ID, in)
			ids <- j.ID
			errs <- e
		}(app)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate task")
		}
		id = got
	}
	job, lease, e := a.cryptoSweepClaim(context.Background())
	if e != nil || job != id || lease == "" {
		t.Fatal(job, lease, e)
	}
	other, _, e := b.cryptoSweepClaim(context.Background())
	if e != nil || other != "" {
		t.Fatal("two leases", other, e)
	}
	if e = a.cryptoRetrySweep(context.Background(), owner, id, cryptoSweepInput{OperationID: randomToken(24)}); e == nil {
		t.Fatal("retry stole live lease")
	}
	if e = a.cryptoCancelSweep(context.Background(), owner, id, cryptoSweepInput{OperationID: randomToken(24)}); e == nil {
		t.Fatal("cancelled live lease")
	}
	if _, e = a.store.db.Exec("UPDATE crypto_sweep_jobs SET lease_until=? WHERE id=?", time.Now().Unix()-1, id); e != nil {
		t.Fatal(e)
	}
	if e = a.cryptoRetrySweep(context.Background(), owner, id, cryptoSweepInput{OperationID: randomToken(24)}); e != nil {
		t.Fatal(e)
	}
	var staleToken string
	var staleUntil int64
	if e = a.store.db.QueryRow("SELECT lease_token,lease_until FROM crypto_sweep_jobs WHERE id=?", id).Scan(&staleToken, &staleUntil); e != nil || staleToken != "" || staleUntil != 0 {
		t.Fatal("retry retained expired lease", staleToken, staleUntil, e)
	}
	job, lease, e = a.cryptoSweepClaim(context.Background())
	if e != nil || job != id || lease == "" {
		t.Fatal(job, lease, e)
	}
	if e = a.cryptoSweepFinish(context.Background(), id, lease, "failed", "test failure before signing"); e != nil {
		t.Fatal(e)
	}
	if e = a.cryptoCancelSweep(context.Background(), owner, id, cryptoSweepInput{OperationID: randomToken(24)}); e != nil {
		t.Fatal(e)
	}
	p = cryptoSweepTestPreview(t, a, owner, v)
	_ = cryptoSweepTestCreate(t, b, owner, v, p)
}
func TestCryptoSweepPreviewConstraintsAndFrozenChain(t *testing.T) {
	a, owner, v, f := cryptoSweepFixture(t)
	in := cryptoSweepTestInput()
	in.MaxGasAtoms = "1"
	p, e := a.cryptoPreviewSweep(context.Background(), owner, v.ID, in)
	if e != nil || p.CanSubmit || p.Quote != "" || p.TotalGasAtoms == "" || p.RequiredFundingAtoms == "" {
		t.Fatal("incomplete low budget preview", p, e)
	}
	f.mu.Lock()
	f.native[strings.ToLower(f.funding)] = new(big.Int)
	f.mu.Unlock()
	in = cryptoSweepTestInput()
	p, e = a.cryptoPreviewSweep(context.Background(), owner, v.ID, in)
	if e != nil || p.CanSubmit || p.Quote != "" || p.RequiredFundingAtoms == "0" {
		t.Fatal("incomplete funding preview", p, e)
	}
	for _, dest := range []string{f.contract, f.receive, f.funding} {
		in.Destination = dest
		if _, e = a.cryptoPreviewSweep(context.Background(), owner, v.ID, in); e == nil {
			t.Fatal("unsafe destination accepted", dest)
		}
	}
	f.mu.Lock()
	f.native[strings.ToLower(f.funding)] = big.NewInt(1000000000000)
	f.mu.Unlock()
	p = cryptoSweepTestPreview(t, a, owner, v)
	_ = cryptoSweepTestCreate(t, a, owner, v, p)
	_ = a.store.freezeCryptoChain(1)
	if e = a.cryptoSweepWork(context.Background()); e == nil || cryptoSweepTestCount(t, a) != 0 {
		t.Fatal("frozen chain signed", e)
	}
	if _, e = a.cryptoPreviewSweep(context.Background(), owner, v.ID, cryptoSweepTestInput()); e == nil {
		t.Fatal("frozen chain preview allowed")
	}
}
func TestCryptoSweepRevertedAndTamperedEvidence(t *testing.T) {
	for _, mode := range []string{"revert", "nonce", "raw"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, v, f := cryptoSweepFixture(t)
			p := cryptoSweepTestPreview(t, a, owner, v)
			j := cryptoSweepTestCreate(t, a, owner, v, p)
			if e := a.cryptoSweepWork(context.Background()); e != nil {
				t.Fatal(e)
			}
			if mode == "raw" {
				if _, e := a.store.db.Exec("UPDATE crypto_chain_transactions SET amount_atoms='1'"); e != nil {
					t.Fatal(e)
				}
			} else {
				f.revert = mode == "revert"
				f.wrongNonce = mode == "nonce"
				f.mine(true)
			}
			before := f.sends
			if e := a.cryptoSweepWork(context.Background()); e == nil {
				t.Fatal("invalid evidence accepted")
			}
			out, e := a.store.cryptoSweepJob(j.ID)
			if e != nil || out.State != "failed" {
				t.Fatal(out, e)
			}
			if f.sends != before || cryptoSweepTestCount(t, a) != 1 {
				t.Fatal("resent or resigned after permanent failure")
			}
			if mode == "revert" {
				if !out.CanCancel {
					t.Fatal("confirmed revert cannot release task")
				}
				if e = a.cryptoCancelSweep(context.Background(), owner, j.ID, cryptoSweepInput{OperationID: randomToken(24)}); e != nil {
					t.Fatal(e)
				}
			} else if out.CanCancel {
				t.Fatal("unverified transaction reservations released")
			}
		})
	}
}
func TestCryptoSigningFundingAndIntent(t *testing.T) {
	xp, path, first, e := cryptoHDFromMnemonic(cryptoTestMnemonic)
	if e != nil {
		t.Fatal(e)
	}
	f, e := cryptoFunding(CryptoWallet{Mode: "hot", XPub: xp, Path: path})
	if e != nil || f.Address == first || f.Path != path+"/1/0" {
		t.Fatal(f, e)
	}
	for _, derivation := range []string{path + "/0/0", f.Path} {
		signed, e := cryptoSignTransfer(cryptoTestMnemonic, derivation, 1, 9, 21000, big.NewInt(20000000000), big.NewInt(1000000000000000000), "0x3535353535353535353535353535353535353535", nil)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := hex.DecodeString(signed.Raw[2:])
		expected := first
		if derivation == f.Path {
			expected = f.Address
		}
		if signed.From != expected {
			t.Fatal("key derivation mismatch")
		}
		v := cryptoChainTransaction{ChainID: 1, From: signed.From, To: "0x3535353535353535353535353535353535353535", Amount: "1000000000000000000", Nonce: 9, Hash: signed.Hash, Raw: raw, GasLimit: 21000, GasPrice: "20000000000"}
		if _, e = cryptoValidateSignedIntent(v); e != nil {
			t.Fatal(e)
		}
		v.Nonce++
		if _, e = cryptoValidateSignedIntent(v); e == nil {
			t.Fatal("mutated nonce accepted")
		}
	}
	for _, id := range []int64{10, 8453, 42161} {
		if _, e = cryptoSignTransfer(cryptoTestMnemonic, path+"/0/0", id, 0, 21000, big.NewInt(1), big.NewInt(1), first, nil); e == nil {
			t.Fatal("L2 signer bypass")
		}
		if e = cryptoSweepAllowed(CryptoChain{ChainID: id, FinalityVerified: true}); e == nil {
			t.Fatal("L2 gate bypass")
		}
	}
	if e = cryptoSweepWallet(CryptoWallet{Mode: "watch", BackupConfirmed: true}); e == nil {
		t.Fatal("watch-only signer enabled")
	}
}

func TestCryptoSweepBusinessAgentCannotExecute(t *testing.T) {
	a, owner, v, _ := cryptoSweepFixture(t)
	p := cryptoSweepTestPreview(t, a, owner, v)
	_ = cryptoSweepTestCreate(t, a, owner, v, p)
	a.cfg.Role = "business"
	a.cfg.LocalManagement = false
	if e := a.cryptoSweepWork(context.Background()); e != nil || cryptoSweepTestCount(t, a) != 0 {
		t.Fatal("business agent ran money worker", e)
	}
}

type cryptoDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *cryptoDeadlineRecorder) SetWriteDeadline(at time.Time) error { w.deadline = at; return nil }
func TestCryptoSweepRouteBusinessGateAndScopedDeadline(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	routes := []struct {
		method, path string
		slow         bool
	}{
		{"GET", "wallets/missing/balances?chain_id=ethereum", true},
		{"POST", "wallets/missing/sweeps/preview", true},
		{"POST", "wallets/missing/sweeps", false},
		{"GET", "sweeps", false},
		{"POST", "sweeps/missing/retry", false},
		{"POST", "sweeps/missing/cancel", false},
	}
	for _, route := range routes {
		for _, role := range []string{"owner", "member", "business"} {
			t.Run(role+"/"+route.path, func(t *testing.T) {
				a.cfg.Role = ""
				a.cfg.LocalManagement = false
				actor := owner
				if role == "member" {
					actor = member
				}
				if role == "business" {
					a.cfg.Role = "business"
				}
				w := &cryptoDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
				r := httptest.NewRequest(route.method, cryptoWalletPrefix+route.path, strings.NewReader("{}"))
				before := time.Now()
				if !a.cryptoSweepAPIRoute(w, r, actor) {
					t.Fatal("route not claimed")
				}
				if role != "owner" {
					if w.Code != 403 || !w.deadline.IsZero() {
						t.Fatal("forbidden role reached deadline or RPC work", w.Code, w.deadline)
					}
					return
				}
				if route.slow {
					delta := w.deadline.Sub(before)
					if delta < 99*time.Second || delta > 101*time.Second {
						t.Fatal("missing scoped long deadline", delta)
					}
				} else if !w.deadline.IsZero() {
					t.Fatal("fast route extended deadline")
				}
			})
		}
	}
}

func TestCryptoSweepRejectsMissingItemsAndFalseCompletion(t *testing.T) {
	for _, mode := range []string{"missing", "false_complete", "false_confirmed"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, v, _ := cryptoSweepFixture(t)
			p := cryptoSweepTestPreview(t, a, owner, v)
			j := cryptoSweepTestCreate(t, a, owner, v, p)
			var e error
			switch mode {
			case "missing":
				_, e = a.store.db.Exec("DELETE FROM crypto_sweep_items WHERE job_id=?", j.ID)
			case "false_complete":
				_, e = a.store.db.Exec("UPDATE crypto_sweep_items SET state='complete' WHERE job_id=?", j.ID)
			case "false_confirmed":
				if e = a.cryptoSweepWork(context.Background()); e != nil {
					t.Fatal(e)
				}
				_, e = a.store.db.Exec("UPDATE crypto_chain_transactions SET state='confirmed',block_number=101,block_hash=? WHERE job_id=?", cryptoFixtureHash(101), j.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = a.cryptoSweepWork(context.Background()); e == nil {
				t.Fatal("missing detail or false state advanced money task")
			}
			current, e := a.store.cryptoSweepJob(j.ID)
			if e != nil || current.State != "failed" {
				t.Fatal(current, e)
			}
		})
	}
}
