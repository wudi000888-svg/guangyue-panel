package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type cryptoPaymentRPCFixture struct {
	mu            sync.Mutex
	latest, final uint64
	created       int64
	logs          []cryptoEVMLog
	mismatch      bool
	chainID       int64
	fork          bool
}

func cryptoFixtureHash(n uint64) string { return fmt.Sprintf("0x%064x", n+1000) }
func (f *cryptoPaymentRPCFixture) serve(w http.ResponseWriter, r *http.Request) {
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
	var result any
	decode := func(i int) string { var s string; json.Unmarshal(req.Params[i], &s); return s }
	switch req.Method {
	case "eth_chainId":
		id := f.chainID
		if id == 0 {
			id = 1
		}
		result = cryptoHex(uint64(id))
	case "eth_getBlockByNumber":
		n := uint64(0)
		tag := decode(0)
		if tag == "latest" {
			n = f.latest
		} else if tag == "finalized" {
			n = f.final
		} else {
			n, _ = strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
		}
		stamp := f.created - 100 + int64(n)
		if n == 0 {
			stamp = 0
		}
		hash := cryptoFixtureHash(n)
		if f.fork && n > 0 {
			hash = cryptoFixtureHash(n + 5000)
		}
		result = object{"number": cryptoHex(n), "hash": hash, "timestamp": cryptoHex(uint64(stamp))}
	case "eth_getCode":
		result = "0x60016000"
	case "eth_call":
		result = "0x6"
	case "eth_getLogs":
		var filter struct {
			Address string            `json:"address"`
			From    string            `json:"fromBlock"`
			To      string            `json:"toBlock"`
			Topics  []json.RawMessage `json:"topics"`
		}
		json.Unmarshal(req.Params[0], &filter)
		from, _ := strconv.ParseUint(filter.From[2:], 16, 64)
		to, _ := strconv.ParseUint(filter.To[2:], 16, 64)
		var recipient string
		json.Unmarshal(filter.Topics[2], &recipient)
		list := []object{}
		for _, l := range f.logs {
			if l.Address == filter.Address && l.Topics[2] == recipient && l.BlockNumber >= from && l.BlockNumber <= to {
				list = append(list, cryptoFixtureRPCLog(l))
			}
		}
		result = list
	case "eth_getTransactionReceipt":
		hash := decode(0)
		list := []object{}
		var match cryptoEVMLog
		for _, l := range f.logs {
			if l.TxHash == hash {
				list = append(list, cryptoFixtureRPCLog(l))
				match = l
			}
		}
		if len(list) > 0 {
			if f.mismatch {
				list = []object{}
			}
			result = object{"transactionHash": hash, "blockHash": match.BlockHash, "blockNumber": cryptoHex(match.BlockNumber), "from": "0x1111111111111111111111111111111111111111", "to": "0x2222222222222222222222222222222222222222", "status": "0x1", "logs": list}
		}
	default:
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "id": 1, "result": result})
}
func cryptoFixtureRPCLog(l cryptoEVMLog) object {
	return object{"address": l.Address, "topics": l.Topics, "data": l.Data, "blockNumber": cryptoHex(l.BlockNumber), "blockHash": l.BlockHash, "transactionHash": l.TxHash, "logIndex": cryptoHex(l.Index), "removed": false}
}
func (f *cryptoPaymentRPCFixture) add(v CryptoInvoice, n uint64, atoms string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	amount, _ := new(big.Int).SetString(atoms, 10)
	f.logs = append(f.logs, cryptoEVMLog{Address: v.Contract, Topics: []string{cryptoTransferTopic, "0x" + cryptoABIAddress("0x1111111111111111111111111111111111111111"), "0x" + cryptoABIAddress(v.Address)}, Data: fmt.Sprintf("0x%064x", amount), BlockNumber: n, BlockHash: cryptoFixtureHash(n), TxHash: cryptoFixtureHash(n + 100), Index: 0})
}
func cryptoPaymentFixture(t *testing.T) (*App, Record, Record, CryptoWallet, *cryptoPaymentRPCFixture) {
	t.Helper()
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	wallet, _ := cryptoTestCreate(t, a, owner, false)
	f := &cryptoPaymentRPCFixture{latest: 100, final: 100, created: time.Now().Unix()}
	p := httptest.NewServer(http.HandlerFunc(f.serve))
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(p.Close)
	t.Cleanup(b.Close)
	s := cryptoDefaultSettings()
	s.Revision = 1
	s.Enabled = true
	s.WalletID = wallet.ID
	s.Chains[0].Enabled = true
	s.Chains[0].FinalityVerified = true
	s.Chains[0].RPCURL = p.URL
	s.Chains[0].RPCBackupURL = b.URL
	s.Assets[1].Enabled = true
	s.Assets[1].CNYPerToken = "7"
	s.Assets[1].RateUpdatedAt = time.Now().Unix()
	s.Assets[1].RateExpiresAt = time.Now().Unix() + 86400
	raw, e := a.store.vault.seal(s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,1,?)", raw); e != nil {
		t.Fatal(e)
	}
	return a, owner, user, wallet, f
}
func cryptoPaymentInvoice(t *testing.T, a *App, owner, user Record) (Order, CryptoInvoice) {
	t.Helper()
	o := newOrder(t, a, user, commerceOffer(t, a, owner))
	v := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": o.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}), 201)
	return o, v
}
func TestCryptoPaymentExactQuoteAndDisabledOptions(t *testing.T) {
	for _, c := range []struct {
		rate              string
		decimals, payment int
		amount            int64
		want              string
	}{{"7", 18, 2, 1000, "1430000000000000000"}, {"7", 6, 2, 1000, "1430000"}, {"7.12345678", 6, 6, 1, "1404"}} {
		got, e := cryptoQuote(c.amount, CryptoAsset{CNYPerToken: c.rate, Decimals: c.decimals, PaymentDecimals: c.payment})
		if e != nil || got != c.want {
			t.Fatal(got, e, c)
		}
	}
	for _, rate := range []string{"1e3", "NaN", "0", "-1", "0.000000001"} {
		if _, e := cryptoQuote(100, CryptoAsset{CNYPerToken: rate, Decimals: 6, PaymentDecimals: 2}); e == nil {
			t.Fatal(rate)
		}
	}
	a := testApp(t)
	u := testUser(t, a, "u", "user")
	response := decoded[struct {
		Enabled bool
		Assets  []object
	}](t, req(t, a, u, "GET", "/api/commerce/crypto/options", nil), 200)
	if response.Enabled || len(response.Assets) != 0 {
		t.Fatal(response)
	}
}
func TestCryptoInvoiceAllocationReplaySwitchAndPermissions(t *testing.T) {
	a, owner, user, wallet, _ := cryptoPaymentFixture(t)
	o := newOrder(t, a, user, commerceOffer(t, a, owner))
	if current := cryptoTestCurrent(t, a, wallet.ID); current.NextIndex != 0 {
		t.Fatal("ordinary order allocated address")
	}
	in := object{"order_id": o.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}
	v := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", in), 201)
	replayed := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", in), 200)
	if replayed.ID != v.ID || replayed.Address != v.Address || cryptoTestCurrent(t, a, wallet.ID).NextIndex != 1 {
		t.Fatal("invoice retry reallocated")
	}
	stranger := testUser(t, a, "stranger", "user")
	if w := req(t, a, stranger, "GET", "/api/commerce/crypto/invoices/"+v.ID, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := req(t, a, stranger, "POST", "/api/commerce/crypto/invoices", in); w.Code != 409 {
		t.Fatal(w.Code)
	}
	in["operation_id"] = randomToken(24)
	if w := req(t, a, user, "POST", "/api/commerce/crypto/invoices", in); w.Code != 409 {
		t.Fatal("same asset allocated twice", w.Code)
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	settings.Assets[0].Enabled = true
	settings.Assets[0].CNYPerToken = "7"
	settings.Assets[0].RateUpdatedAt = time.Now().Unix()
	settings.Assets[0].RateExpiresAt = time.Now().Unix() + 86400
	sealed, e := a.store.vault.seal(settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", sealed); e != nil {
		t.Fatal(e)
	}
	in["asset_id"] = "ethereum-usdt"
	second := decoded[CryptoInvoice](t, req(t, a, user, "POST", "/api/commerce/crypto/invoices", in), 201)
	if second.Address == v.Address || cryptoTestCurrent(t, a, wallet.ID).NextIndex != 2 {
		t.Fatal("switched invoice reused address")
	}
	old, e := a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil || old.State != "superseded" {
		t.Fatal(old.State, e)
	}
}
func TestCryptoPaymentPartialFinalityAndExactlyOnceFulfillment(t *testing.T) {
	a, owner, user, _, f := cryptoPaymentFixture(t)
	o, v := cryptoPaymentInvoice(t, a, owner, user)
	f.add(v, 101, "700000")
	f.mu.Lock()
	f.latest = 102
	f.final = 100
	f.mu.Unlock()
	internal, e := a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
		t.Fatal(e)
	}
	state, e := a.store.decorateCryptoInvoice(internal)
	if e != nil || state.State != "confirming" || state.ConfirmedAtoms != "0" {
		t.Fatal(state, e)
	}
	if w := req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": o.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal("pending transfer allowed switch", w.Code)
	}
	f.mu.Lock()
	f.final = 102
	f.mu.Unlock()
	if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
		t.Fatal(e)
	}
	state, e = a.store.decorateCryptoInvoice(internal)
	if e != nil || state.State != "partial" || state.RemainingAtoms != "730000" || !strings.HasSuffix(state.QRURI, "uint256=730000") {
		t.Fatal(state, e)
	}
	f.add(v, 103, "800000")
	f.mu.Lock()
	f.latest = 104
	f.final = 104
	f.mu.Unlock()
	if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = a.paymentWork(); e != nil {
			t.Fatal(e)
		}
		if e = a.commerceWork(time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	order, e := a.store.order(o.ID)
	if e != nil || order.State != "completed" {
		t.Fatal(order.State, e)
	}
	current, e := a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	state, e = a.store.decorateCryptoInvoice(current)
	if e != nil || state.State != "paid" || state.OverpaidAtoms != "70000" || state.ReceiptState != "applied" {
		t.Fatal(state, e)
	}
	var count int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", o.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	assertLedger(t, a)
}
func TestCryptoPaymentLateCancelledAndMissingReceiptStayUncredited(t *testing.T) {
	for _, mode := range []string{"late", "cancelled", "missing-receipt"} {
		t.Run(mode, func(t *testing.T) {
			a, owner, user, _, f := cryptoPaymentFixture(t)
			o, v := cryptoPaymentInvoice(t, a, owner, user)
			if mode == "late" {
				v.Expires = f.created + 1
				if _, e := a.store.db.Exec("UPDATE crypto_invoices SET expires=? WHERE id=?", v.Expires, v.ID); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "cancelled" {
				if w := req(t, a, user, "POST", "/api/commerce/orders/action", object{"id": o.ID, "action": "cancel", "operation_id": randomToken(24)}); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			f.add(v, 102, "1430000")
			f.mu.Lock()
			f.latest = 103
			f.final = 103
			f.mismatch = mode == "missing-receipt"
			f.mu.Unlock()
			internal, e := a.store.cryptoInvoice(a.store.db, v.ID)
			if e != nil {
				t.Fatal(e)
			}
			e = a.scanCryptoInvoice(context.Background(), internal)
			if mode == "missing-receipt" && e == nil {
				t.Fatal("missing receipt accepted")
			}
			if mode != "missing-receipt" && e != nil {
				t.Fatal(e)
			}
			var count int
			if e = a.store.db.QueryRow("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", o.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
			if mode != "missing-receipt" {
				current, e := a.store.cryptoInvoice(a.store.db, v.ID)
				if e != nil || current.State != "review_required" {
					t.Fatal(current.State, e)
				}
			}
		})
	}
}

func TestCryptoPaymentReorgReindexesAcrossInvoicesWithoutDoubleCredit(t *testing.T) {
	a, owner, user, _, f := cryptoPaymentFixture(t)
	_, first := cryptoPaymentInvoice(t, a, owner, user)
	secondUser := testUser(t, a, "second", "user")
	_, second := cryptoPaymentInvoice(t, a, owner, secondUser)
	f.add(first, 101, "600000")
	f.add(second, 101, "830000")
	f.mu.Lock()
	f.logs[0].Index = 100
	f.logs[1].Index = 101
	f.latest = 102
	f.final = 100
	f.mu.Unlock()
	load := func(id string) CryptoInvoice {
		v, e := a.store.cryptoInvoice(a.store.db, id)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	for _, id := range []string{first.ID, second.ID} {
		if e := a.scanCryptoInvoice(context.Background(), load(id)); e != nil {
			t.Fatal(e)
		}
	}
	f.mu.Lock()
	oldTx := f.logs[0].TxHash
	f.logs = nil
	f.mu.Unlock()
	f.add(first, 102, "600000")
	f.add(first, 102, "830000")
	f.mu.Lock()
	f.logs[0].Index = 101
	f.logs[1].Index = 102
	f.logs[0].TxHash = oldTx
	f.logs[1].TxHash = oldTx
	f.latest = 103
	f.final = 103
	f.mu.Unlock()
	for _, id := range []string{first.ID, second.ID} {
		if e := a.scanCryptoInvoice(context.Background(), load(id)); e != nil {
			t.Fatal(e)
		}
	}
	got, e := a.store.decorateCryptoInvoice(load(first.ID))
	if e != nil || got.ConfirmedAtoms != "1430000" || got.State != "paid" {
		t.Fatal(got, e)
	}
	other, e := a.store.decorateCryptoInvoice(load(second.ID))
	if e != nil || other.ReceivedAtoms != "0" || other.ConfirmedAtoms != "0" {
		t.Fatal(other, e)
	}
	var observations, transfers int
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_transfer_observations").Scan(&observations); e != nil {
		t.Fatal(e)
	}
	if e = a.store.db.QueryRow("SELECT COUNT(*) FROM crypto_transfers").Scan(&transfers); e != nil || observations != 0 || transfers != 2 {
		t.Fatal(observations, transfers, e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestCryptoPaymentFinalityViolationFreezesWholeChain(t *testing.T) {
	a, owner, user, _, f := cryptoPaymentFixture(t)
	_, v := cryptoPaymentInvoice(t, a, owner, user)
	internal, e := a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.latest = 102
	f.final = 102
	f.mu.Unlock()
	if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.fork = true
	f.mu.Unlock()
	if e = a.scanCryptoInvoice(context.Background(), internal); !errors.Is(e, errCryptoFinality) {
		t.Fatal("finalized fork not frozen", e)
	}
	var frozen int
	if e = a.store.db.QueryRow("SELECT frozen FROM crypto_chain_state WHERE chain_id=1").Scan(&frozen); e != nil || frozen != 1 {
		t.Fatal(frozen, e)
	}
	another := testUser(t, a, "another", "user")
	o := newOrder(t, a, another, commerceOffer(t, a, owner))
	if w := req(t, a, another, "POST", "/api/commerce/crypto/invoices", object{"order_id": o.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	options := decoded[struct {
		Assets []struct {
			Available bool `json:"available"`
		}
	}](t, req(t, a, user, "GET", "/api/commerce/crypto/options", nil), 200)
	if len(options.Assets) != 1 || options.Assets[0].Available {
		t.Fatal("frozen chain advertised")
	}
}
func TestCryptoInvoiceConcurrentAllocationAndRestart(t *testing.T) {
	a, owner, user, wallet, _ := cryptoPaymentFixture(t)
	o := newOrder(t, a, user, commerceOffer(t, a, owner))
	peer := cryptoIndependentApp(t, a)
	in := cryptoInvoiceInput{OrderID: o.ID, AssetID: "ethereum-usdc", OperationID: randomToken(24)}
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 8)
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app := a
			if i%2 == 1 {
				app = peer
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/commerce/crypto/invoices", strings.NewReader(string(jsonBytes(in))))
			e := app.createCryptoInvoice(w, r, user)
			responses <- w
			errs <- e
		}(i)
	}
	wg.Wait()
	close(responses)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id string
	for w := range responses {
		if w.Code != 200 && w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v CryptoInvoice
		if json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Body.String())
		}
		if id != "" && id != v.ID {
			t.Fatal("concurrent allocation diverged")
		}
		id = v.ID
	}
	if cryptoTestCurrent(t, a, wallet.ID).NextIndex != 1 {
		t.Fatal("concurrent replay consumed indices")
	}
	reopened := cryptoIndependentApp(t, a)
	v, e := reopened.store.cryptoInvoice(reopened.store.db, id)
	if e != nil || v.OrderID != o.ID {
		t.Fatal(v, e)
	}
	if e = reopened.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}

}
func TestCryptoPaymentSettingsRedactionReauthenticationAndReplay(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	settings := cryptoDefaultSettings()
	settings.Chains[0].RPCURL = "https://one.example.com/private-key"
	settings.Chains[0].RPCBackupURL = "https://two.example.com/private-key"
	input := object{}
	json.Unmarshal(jsonBytes(settings), &input)
	input["password"] = commerceTestPassword
	input["operation_id"] = randomToken(24)
	v := decoded[CryptoPaymentSettings](t, req(t, a, owner, "POST", "/api/commerce/crypto/admin/settings", input), 200)
	if v.Revision != 1 || v.Chains[0].RPCURL != "" || !v.Chains[0].HasRPC {
		t.Fatal(v)
	}
	replay := decoded[CryptoPaymentSettings](t, req(t, a, owner, "POST", "/api/commerce/crypto/admin/settings", input), 200)
	if replay.Revision != v.Revision {
		t.Fatal("settings replay mutated")
	}
	input["password"] = "wrong"
	if w := req(t, a, owner, "POST", "/api/commerce/crypto/admin/settings", input); w.Code != 403 {
		t.Fatal(w.Code)
	}
	var raw []byte
	if e := a.store.db.QueryRow("SELECT doc FROM crypto_payment_settings WHERE id=1").Scan(&raw); e != nil || strings.Contains(string(raw), "private-key") {
		t.Fatal("RPC credentials not sealed", e)
	}
}

func TestCryptoPaymentTimelyBlockAfterDeadlineAndDisabledConfiguration(t *testing.T) {
	a, owner, user, _, f := cryptoPaymentFixture(t)
	o, v := cryptoPaymentInvoice(t, a, owner, user)
	// The canonical payment was mined before expiry; discovery/finality is late.
	now := time.Now().Unix()
	created, expires := now-200, now-50
	f.mu.Lock()
	f.created = now - 180
	f.mu.Unlock()
	if _, e := a.store.db.Exec("UPDATE crypto_invoices SET created=?,expires=? WHERE id=?", created, expires, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := a.store.db.Exec("UPDATE payment_attempts SET expires=? WHERE id=?", expires, v.AttemptID); e != nil {
		t.Fatal(e)
	}
	current, e := a.store.order(o.ID)
	if e != nil {
		t.Fatal(e)
	}
	current.Created = created
	current.Expires = expires
	tx, e := a.store.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = saveOrder(tx, current, "pending"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	settings.Enabled = false
	settings.Chains[0].Enabled = false
	sealed, e := a.store.vault.seal(settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", sealed); e != nil {
		t.Fatal(e)
	}
	f.add(v, 102, "1430000")
	f.mu.Lock()
	f.latest = 300
	f.final = 300
	f.mu.Unlock()
	if e = a.paymentWork(); e != nil {
		t.Fatal(e)
	}
	if e = a.commerceWork(now); e != nil {
		t.Fatal(e)
	}
	current, e = a.store.order(o.ID)
	if e != nil || current.State != "pending" {
		t.Fatal("wall clock expired chain invoice", current.State, e)
	}
	invoice, e := a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.scanCryptoInvoice(context.Background(), invoice); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = a.paymentWork(); e != nil {
			t.Fatal(e)
		}
		if e = a.commerceWork(now); e != nil {
			t.Fatal(e)
		}
	}
	current, e = a.store.order(o.ID)
	if e != nil || current.State != "completed" {
		t.Fatal("timely final payment not fulfilled", current.State, e)
	}
	var received int64
	if e = a.store.db.QueryRow("SELECT created FROM payment_receipts WHERE attempt_id=?", v.AttemptID).Scan(&received); e != nil || received >= expires {
		t.Fatal("receipt lost canonical block time", received, e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestCryptoInvoiceFreeOrderAndWalletRecoveryGate(t *testing.T) {
	a, owner, user, wallet, _ := cryptoPaymentFixture(t)
	offer := commerceOffer(t, a, owner)
	offer = decoded[Offer](t, req(t, a, owner, "POST", "/api/commerce/offers", object{"plan_id": offer.Plan.ID, "plan_version": offer.Plan.Version, "price": "0", "enabled": true}), 200)
	order := newOrder(t, a, user, offer)
	if w := req(t, a, user, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if cryptoTestCurrent(t, a, wallet.ID).NextIndex != 0 {
		t.Fatal("free order consumed address")
	}
	another := testUser(t, a, "paid-user", "user")
	order = newOrder(t, a, another, commerceOffer(t, a, owner))
	if _, e := a.store.db.Exec("UPDATE crypto_wallets SET recovery_required=1 WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	if w := req(t, a, another, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": "ethereum-usdc", "operation_id": randomToken(24)}); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if cryptoTestCurrent(t, a, wallet.ID).NextIndex != 0 {
		t.Fatal("recovery-gated wallet allocated address")
	}
}

func TestCryptoPaymentWrongNetworkIsRecordedForReview(t *testing.T) {
	a, owner, user, _, main := cryptoPaymentFixture(t)
	_, v := cryptoPaymentInvoice(t, a, owner, user)
	other := &cryptoPaymentRPCFixture{chainID: 56, latest: 102, final: 102, created: main.created}
	first := httptest.NewServer(http.HandlerFunc(other.serve))
	second := httptest.NewServer(http.HandlerFunc(other.serve))
	defer first.Close()
	defer second.Close()
	settings, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	settings.Chains[1].RPCURL = first.URL
	settings.Chains[1].RPCBackupURL = second.URL
	settings.Chains[1].FinalityVerified = true
	sealed, e := a.store.vault.seal(settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", sealed); e != nil {
		t.Fatal(e)
	}
	wrong := v
	wrong.Contract = settings.Assets[2].Contract
	other.add(wrong, 101, "1430000000000000000")
	internal, e := a.store.cryptoInvoice(a.store.db, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
		t.Fatal(e)
	}
	detail := decoded[CryptoInvoice](t, req(t, a, owner, "GET", "/api/commerce/crypto/invoices/"+v.ID, nil), 200)
	if detail.State != "review_required" || detail.ConfirmedAtoms != "0" || len(detail.Transfers) != 1 || detail.Transfers[0].ChainID != 56 || detail.Transfers[0].Atoms != "1430000000000000000" || detail.Transfers[0].State != "review_required" {
		t.Fatal(detail)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestCryptoPaymentFreezeBlocksConfirmationAndProvisioning(t *testing.T) {
	for _, stage := range []string{"paid", "provisioning"} {
		t.Run(stage, func(t *testing.T) {
			a, owner, user, _, f := cryptoPaymentFixture(t)
			o, v := cryptoPaymentInvoice(t, a, owner, user)
			f.add(v, 101, v.ExpectedAtoms)
			f.mu.Lock()
			f.latest = 102
			f.final = 102
			f.mu.Unlock()
			internal, e := a.store.cryptoInvoice(a.store.db, v.ID)
			if e != nil {
				t.Fatal(e)
			}
			if e = a.scanCryptoInvoice(context.Background(), internal); e != nil {
				t.Fatal(e)
			}
			if stage == "provisioning" {
				if e = a.paymentWork(); e != nil {
					t.Fatal(e)
				}
			}
			_ = a.store.freezeCryptoChain(1)
			if stage == "paid" {
				if e = a.confirmExternalOrder(o.ID, user, v.AttemptID); !errors.Is(e, errCryptoFinality) {
					t.Fatal("confirmation bypassed freeze", e)
				}
			} else {
				if e = a.commerceWork(time.Now().Unix()); e == nil {
					t.Fatal("provisioning bypassed freeze")
				}
			}
			order, e := a.store.order(o.ID)
			if e != nil || order.State == "completed" || order.State == "failed" {
				t.Fatal(order.State, e)
			}
			attempt, e := a.store.paymentAttempt(v.AttemptID)
			if e != nil || attempt.State != "paid" {
				t.Fatal(attempt.State, e)
			}
		})
	}
}
