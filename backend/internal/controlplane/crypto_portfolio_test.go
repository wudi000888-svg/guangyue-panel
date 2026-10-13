package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type cryptoPortfolioRPCFixture struct {
	mu               sync.Mutex
	base             *cryptoSweepRPCFixture
	calls            int
	failAddress      string
	failAll, failGas bool
	started          chan struct{}
	release          chan struct{}
	once             sync.Once
	tags             []string
	reorg            bool
}

func (f *cryptoPortfolioRPCFixture) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var in struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &in)
	var addr, tag string
	if in.Method == "eth_getBalance" {
		_ = json.Unmarshal(in.Params[0], &addr)
		_ = json.Unmarshal(in.Params[1], &tag)
	}
	if in.Method == "eth_call" {
		var call struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(in.Params[0], &call)
		if strings.HasPrefix(call.Data, "0x70a08231") {
			addr = "0x" + call.Data[len(call.Data)-40:]
			_ = json.Unmarshal(in.Params[1], &tag)
		}
	}
	f.mu.Lock()
	f.calls++
	if tag != "" {
		f.tags = append(f.tags, tag)
	}
	fail := f.failAll || f.failGas && in.Method == "eth_gasPrice" || f.failAddress != "" && strings.EqualFold(addr, f.failAddress)
	start, release := f.started, f.release
	reorg := f.reorg
	f.mu.Unlock()
	if start != nil && in.Method == "eth_getBalance" {
		f.once.Do(func() { close(start) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
	}
	if fail {
		w.WriteHeader(503)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if reorg && in.Method == "eth_getBlockByNumber" {
		var blockTag string
		_ = json.Unmarshal(in.Params[0], &blockTag)
		if blockTag == "0x64" {
			recorder := httptest.NewRecorder()
			f.base.serve(recorder, r)
			var envelope struct {
				JSONRPC string         `json:"jsonrpc"`
				ID      int            `json:"id"`
				Result  map[string]any `json:"result"`
			}
			_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
			envelope.Result["hash"] = "0x" + strings.Repeat("a", 64)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(envelope)
			return
		}
	}
	f.base.serve(w, r)
}

func cryptoPortfolioFixture(t *testing.T, count int) (*App, Record, CryptoWallet, *cryptoPortfolioRPCFixture) {
	t.Helper()
	a, owner, wallet, base := cryptoSweepFixture(t)
	for i := 1; i < count; i++ {
		addr, e := cryptoDeriveAddress(wallet.XPub, wallet.Path, uint32(i))
		if e != nil {
			t.Fatal(e)
		}
		_, e = a.store.db.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", fmt.Sprintf("portfolio-%d", i), wallet.ID, i, cryptoFullPath(wallet.Path, int64(i)), addr, fmt.Sprintf("Address %d", i), time.Now().Unix())
		if e != nil {
			t.Fatal(e)
		}
		base.native[strings.ToLower(addr)] = big.NewInt(int64(i + 1))
		base.tokens[strings.ToLower(addr)] = new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), big.NewInt(int64(i)))
	}
	_, e := a.store.db.Exec("UPDATE crypto_wallets SET next_index=? WHERE id=?", count, wallet.ID)
	if e != nil {
		t.Fatal(e)
	}
	fixture := &cryptoPortfolioRPCFixture{base: base}
	primary := httptest.NewServer(http.HandlerFunc(fixture.serve))
	backup := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(primary.Close)
	t.Cleanup(backup.Close)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	s.Chains[0].RPCURL, s.Chains[0].RPCBackupURL = primary.URL, backup.URL
	raw, e := a.store.vault.seal(s)
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", raw)
	if e != nil {
		t.Fatal(e)
	}
	return a, owner, wallet, fixture
}

func cryptoPortfolioRead(t *testing.T, a *App, owner Record, wallet CryptoWallet, query string) CryptoWalletPortfolio {
	t.Helper()
	w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/portfolio?chain_id=ethereum"+query, nil)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("wallet data must not be cached by HTTP")
	}
	return decoded[CryptoWalletPortfolio](t, w, 200)
}

func TestCryptoPortfolioAggregatesAllAddressesExactly(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 25)
	v := cryptoPortfolioRead(t, a, owner, wallet, "")
	if !v.Complete || v.TotalAddresses != 25 || v.CheckedAddresses != 25 || v.FundedAddresses != 25 || len(v.Items) != 20 || v.NextAfter == nil {
		t.Fatalf("incomplete portfolio: %+v", v)
	}
	total := big.NewInt(2000000)
	native := new(big.Int)
	for i := 1; i < 25; i++ {
		total.Add(total, new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), big.NewInt(int64(i))))
		native.Add(native, big.NewInt(int64(i+1)))
	}
	if len(v.Tokens) != 2 || v.Tokens[0].BalanceAtoms != total.String() || v.Tokens[1].BalanceAtoms != total.String() || v.NativeBalanceAtoms != native.String() || v.FundingBalanceAtoms != "1000000000000" {
		t.Fatalf("exact totals or funding isolation failed: %+v", v)
	}
	if v.Finality != "finalized" || v.BlockNumber != 100 || v.GasPriceAtoms != "110" || v.ReferenceNativeTransferFeeAtoms != "2310000" {
		t.Fatalf("invalid finality/gas: %+v", v)
	}
	fixture.mu.Lock()
	calls := fixture.calls
	for _, tag := range fixture.tags {
		if tag != "0x64" {
			t.Errorf("balance used non-final snapshot %q", tag)
		}
	}
	fixture.mu.Unlock()
	page := cryptoPortfolioRead(t, a, owner, wallet, "&after=19&filter=funded")
	if len(page.Items) != 5 || page.Items[0].Index != 20 || page.NextAfter != nil || page.Tokens[0].BalanceAtoms != total.String() {
		t.Fatalf("page changed summary: %+v", page)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.calls != calls {
		t.Fatal("pagination re-scanned cached wallet")
	}
}

func TestCryptoPortfolioPreservesBalancesOnRPCFailure(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 3)
	first := cryptoPortfolioRead(t, a, owner, wallet, "")
	a.cryptoPortfolioMu.Lock()
	for _, entry := range a.cryptoPortfolios {
		entry.expires = time.Time{}
	}
	a.cryptoPortfolioMu.Unlock()
	fixture.mu.Lock()
	fixture.failAddress = fixture.base.receive
	fixture.mu.Unlock()
	next := cryptoPortfolioRead(t, a, owner, wallet, "&filter=failed")
	if next.Complete || next.FailedAddresses != 1 || len(next.Items) != 1 || next.Items[0].Error == "" || !next.Items[0].Stale || next.Items[0].LastSuccessAt == 0 || next.Items[0].Tokens[0].BalanceAtoms != "2000000" || next.Tokens[0].BalanceAtoms != first.Tokens[0].BalanceAtoms {
		t.Fatalf("RPC failure erased successful balance: %+v", next)
	}
	a.cryptoPortfolioMu.Lock()
	for _, entry := range a.cryptoPortfolios {
		entry.expires = time.Time{}
	}
	a.cryptoPortfolioMu.Unlock()
	fixture.mu.Lock()
	fixture.failAll = true
	fixture.mu.Unlock()
	next = cryptoPortfolioRead(t, a, owner, wallet, "")
	if next.Complete || !next.Stale || next.BlockNumber != first.BlockNumber || next.Tokens[0].BalanceAtoms != first.Tokens[0].BalanceAtoms || len(next.Errors) == 0 {
		t.Fatalf("boundary failure erased snapshot: %+v", next)
	}
}

func TestCryptoPortfolioPermissionsAndChainScope(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 1)
	member := testUser(t, a, "member", "user")
	for _, who := range []Record{{}, member} {
		w := req(t, a, who, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/portfolio?chain_id=ethereum", nil)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("unauthorized portfolio", w.Code, w.Body.String())
		}
	}
	if w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/portfolio?chain_id=missing", nil); w.Code != 400 {
		t.Fatal("unknown network", w.Code)
	}
	if _, e := a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids=? WHERE id=?", []byte(`[56]`), wallet.ID); e != nil {
		t.Fatal(e)
	}
	if w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/portfolio?chain_id=ethereum", nil); w.Code != 409 {
		t.Fatal("unsupported wallet network", w.Code)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.calls != 0 {
		t.Fatal("unauthorized request triggered RPC")
	}
}

func TestCryptoPortfolioCoalescesRefreshAndCancellation(t *testing.T) {
	a, _, wallet, fixture := cryptoPortfolioFixture(t, 1)
	fixture.started, fixture.release = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := a.cryptoWalletPortfolio(ctx, wallet.ID, "ethereum", -1, "all"); done <- e }()
	select {
	case <-fixture.started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh not started")
	}
	second := make(chan CryptoWalletPortfolio, 1)
	go func() {
		v, _ := a.cryptoWalletPortfolio(context.Background(), wallet.ID, "ethereum", -1, "all")
		second <- v
	}()
	close(fixture.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	v := <-second
	if !v.Complete || v.TotalAddresses != 1 {
		t.Fatal("coalesced refresh", v)
	}
	fixture.mu.Lock()
	initial := fixture.calls
	fixture.mu.Unlock()
	_, e := a.cryptoWalletPortfolio(context.Background(), wallet.ID, "ethereum", -1, "all")
	if e != nil {
		t.Fatal(e)
	}
	fixture.mu.Lock()
	if fixture.calls != initial {
		t.Fatal("cache ignored")
	}
	fixture.mu.Unlock()
	cancel()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	a.cryptoPortfolioMu.Lock()
	for _, entry := range a.cryptoPortfolios {
		entry.expires = time.Time{}
	}
	a.cryptoPortfolioMu.Unlock()
	_, _ = a.cryptoWalletPortfolio(ctx, wallet.ID, "ethereum", -1, "all")
	v, e = a.cryptoWalletPortfolio(context.Background(), wallet.ID, "ethereum", -1, "all")
	if e != nil || v.Refreshing {
		t.Fatal("cancel left refresh stuck", v, e)
	}
}

func TestCryptoPortfolioChunkCursorFiltersAndConfigInvalidation(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 105)
	fixture.base.mu.Lock()
	for addr := range fixture.base.native {
		if addr != strings.ToLower(fixture.base.funding) {
			fixture.base.native[addr] = new(big.Int)
		}
	}
	for addr := range fixture.base.tokens {
		fixture.base.tokens[addr] = new(big.Int)
	}
	fixture.base.tokens[strings.ToLower(fixture.base.receive)] = big.NewInt(5)
	fixture.base.mu.Unlock()
	v := cryptoPortfolioRead(t, a, owner, wallet, "&filter=empty")
	if !v.Complete || v.TotalAddresses != 105 || v.EmptyAddresses != 104 || v.FundedAddresses != 1 || len(v.Items) != 20 || v.Items[0].Index != 1 || v.NextAfter == nil || *v.NextAfter != 20 {
		t.Fatalf("cursor/filter did not scan all chunks: %+v", v)
	}
	last := cryptoPortfolioRead(t, a, owner, wallet, "&after=100&filter=empty")
	if len(last.Items) != 4 || last.Items[0].Index != 101 || last.NextAfter != nil || last.Tokens[0].BalanceAtoms != "5" {
		t.Fatalf("last chunk missing: %+v", last)
	}
	fixture.mu.Lock()
	calls := fixture.calls
	fixture.failGas = true
	fixture.mu.Unlock()
	fixture.base.mu.Lock()
	fixture.base.tokens[strings.ToLower(fixture.base.receive)] = big.NewInt(7)
	fixture.base.mu.Unlock()
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	// Missing catalog entries are restored by the defaults merge. Change an
	// existing asset's denomination metadata instead, so this regression still
	// proves that the asset fingerprint invalidates an otherwise fresh snapshot.
	changedDecimals := 0
	for i := range s.Assets {
		if s.Assets[i].ID == "ethereum-usdc" {
			s.Assets[i].Decimals++
			changedDecimals = s.Assets[i].Decimals
		}
	}
	if changedDecimals == 0 {
		t.Fatal("fixture is missing the configured USDC asset")
	}
	raw, e := a.store.vault.seal(s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", raw); e != nil {
		t.Fatal(e)
	}
	changed := cryptoPortfolioRead(t, a, owner, wallet, "")
	if len(changed.Tokens) != len(v.Tokens) || changed.GasError == "" || changed.GasPriceAtoms != "" || changed.Complete {
		t.Fatalf("changed config reused old assets/Gas: %+v", changed)
	}
	usdcUpdated := false
	for _, token := range changed.Tokens {
		if token.AssetID == "ethereum-usdc" {
			usdcUpdated = token.Decimals == changedDecimals && token.BalanceAtoms == "7"
		}
	}
	if !usdcUpdated {
		t.Fatalf("asset metadata or newly observed funds were cached: %+v", changed.Tokens)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.calls == calls {
		t.Fatal("asset config fingerprint did not invalidate snapshot")
	}
}

func TestCryptoPortfolioFirstFailedScanNeverClaimsZero(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 2)
	fixture.mu.Lock()
	fixture.failAddress = fixture.base.receive
	fixture.mu.Unlock()
	v := cryptoPortfolioRead(t, a, owner, wallet, "&filter=failed")
	if v.Complete || !v.Stale || v.FailedAddresses != 1 || len(v.Items) != 1 || v.Items[0].NativeAtoms != "" || v.Items[0].LastSuccessAt != 0 || v.Items[0].Error == "" {
		t.Fatalf("missing amount looked like zero: %+v", v)
	}
	fixture.mu.Lock()
	fixture.failAll = true
	fixture.mu.Unlock()
	a.cryptoPortfolioMu.Lock()
	a.cryptoPortfolios = nil
	a.cryptoPortfolioMu.Unlock()
	v = cryptoPortfolioRead(t, a, owner, wallet, "")
	if v.Complete || v.NativeBalanceAtoms != "" || len(v.Tokens) != 2 || v.Tokens[0].BalanceAtoms != "" || v.FundingBalanceAtoms != "" || v.FailedAddresses != 2 || len(v.Errors) == 0 {
		t.Fatalf("failed boundary reported confirmed zero: %+v", v)
	}
}

func TestCryptoPortfolioRefreshRetainsGasAndFundingFailures(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 2)
	first := cryptoPortfolioRead(t, a, owner, wallet, "")
	fixture.mu.Lock()
	fixture.failGas = true
	fixture.failAddress = fixture.base.funding
	fixture.mu.Unlock()
	a.cryptoPortfolioMu.Lock()
	for _, entry := range a.cryptoPortfolios {
		entry.expires = time.Time{}
	}
	a.cryptoPortfolioMu.Unlock()
	v := cryptoPortfolioRead(t, a, owner, wallet, "")
	if v.Complete || v.FundingError == "" || v.GasError == "" || v.FundingBalanceAtoms != first.FundingBalanceAtoms || v.GasPriceAtoms != first.GasPriceAtoms || v.GasCheckedAt != first.GasCheckedAt || v.FundingCheckedAt != first.FundingCheckedAt || v.CheckedAddresses != 2 {
		t.Fatalf("funding/Gas failure erased prior success %+v", v)
	}
}

func TestCryptoPortfolioInvalidFilterCursorAndReadonlyL2(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 1)
	for _, suffix := range []string{"&filter=typo", "&after=-1", "&after=bad"} {
		if w := req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/portfolio?chain_id=ethereum"+suffix, nil); w.Code != 400 {
			t.Fatal("invalid query accepted", suffix, w.Code)
		}
	}
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	s.Chains[0].ChainID = 10
	s.Chains[0].ID = "op"
	s.Chains[0].FinalityVerified = false
	for i := range s.Assets {
		if s.Assets[i].ChainID == 1 {
			s.Assets[i].ChainID = 10
		}
	}
	raw, e := a.store.vault.seal(s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", raw); e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids=? WHERE id=?", []byte(`[10]`), wallet.ID); e != nil {
		t.Fatal(e)
	}
	fixture.base.mu.Lock()
	fixture.base.chain = 10
	fixture.base.mu.Unlock()
	v := decoded[CryptoWalletPortfolio](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets/"+wallet.ID+"/portfolio?chain_id=op", nil), 200)
	if !v.Complete || v.Finality != "latest" || v.OperationsSupported || v.UnavailableReason == "" {
		t.Fatalf("L2 view enabled operations: %+v", v)
	}
}

func TestCryptoPortfolioNewAllocationKeepsKnownBalancesDuringOutage(t *testing.T) {
	a, owner, wallet, fixture := cryptoPortfolioFixture(t, 2)
	first := cryptoPortfolioRead(t, a, owner, wallet, "")
	address, e := cryptoDeriveAddress(wallet.XPub, wallet.Path, 2)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("INSERT INTO crypto_wallet_addresses(id,wallet_id,address_index,path,address,label,created) VALUES(?,?,?,?,?,?,?)", "late-order", wallet.ID, 2, cryptoFullPath(wallet.Path, 2), address, "New order", time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_wallets SET next_index=3,revision=revision+1 WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	fixture.mu.Lock()
	fixture.failAll = true
	fixture.mu.Unlock()
	v := cryptoPortfolioRead(t, a, owner, wallet, "")
	if v.TotalAddresses != 3 || v.Complete || v.Tokens[0].BalanceAtoms != first.Tokens[0].BalanceAtoms || v.NativeBalanceAtoms != first.NativeBalanceAtoms || len(v.Items) != 3 || v.Items[2].LastSuccessAt != 0 || v.Items[2].NativeAtoms != "" {
		t.Fatalf("new address discarded known funds: %+v", v)
	}
}

func TestCryptoPortfolioResumesTailAtSameFinalizedBlock(t *testing.T) {
	a, _, wallet, fixture := cryptoPortfolioFixture(t, 105)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	assets := []CryptoAsset{}
	for _, asset := range s.Assets {
		if asset.ChainID == 1 {
			assets = append(assets, asset)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := a.cryptoRefreshPortfolio(ctx, wallet, s.Chains[0], assets, 105, 104, CryptoWalletPortfolio{}, make(chan struct{}, 4), func(n int) {
		if n >= 100 {
			cancel()
		}
	})
	if first.Complete || first.CheckedAddresses != 100 || !first.resumable || first.scanAfter != 99 || first.BlockNumber != 100 {
		t.Fatalf("interrupted refresh discarded cursor: %+v", first)
	}
	fixture.mu.Lock()
	before := fixture.calls
	fixture.mu.Unlock()
	fixture.base.mu.Lock()
	fixture.base.final = 101
	fixture.base.latest = 101
	fixture.base.mu.Unlock()
	second := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 105, 104, first, make(chan struct{}, 4), func(int) {})
	if !second.Complete || second.CheckedAddresses != 105 || len(second.Items) != 105 || second.BlockNumber != 100 || second.Items[104].LastSuccessAt == 0 || second.resumable {
		t.Fatalf("tail starved or mixed snapshot blocks: %+v", second)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.calls-before > 60 {
		t.Fatalf("resume re-scanned first100 addresses: %d calls", fixture.calls-before)
	}
}

func TestCryptoPortfolioFrozenBlockReorgAndFailedRowsReset(t *testing.T) {
	a, _, wallet, fixture := cryptoPortfolioFixture(t, 105)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	assets := []CryptoAsset{}
	for _, asset := range s.Assets {
		if asset.ChainID == 1 {
			assets = append(assets, asset)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := a.cryptoRefreshPortfolio(ctx, wallet, s.Chains[0], assets, 105, 104, CryptoWalletPortfolio{}, make(chan struct{}, 4), func(n int) {
		if n >= 100 {
			cancel()
		}
	})
	if first.Complete || first.CheckedAddresses != 100 || first.FailedAddresses != 5 || !first.resumable {
		t.Fatalf("failure snapshot: %+v", first)
	}
	fixture.mu.Lock()
	fixture.failAddress = ""
	fixture.reorg = true
	fixture.mu.Unlock()
	fixture.base.mu.Lock()
	fixture.base.final = 101
	fixture.base.latest = 101
	fixture.base.mu.Unlock()
	invalid := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 105, 104, first, make(chan struct{}, 4), func(int) {})
	if invalid.Complete || invalid.resumable || invalid.BlockNumber != 100 || invalid.CheckedAddresses != 0 || len(invalid.Errors) == 0 {
		t.Fatalf("reorg retained valid frozen session: %+v", invalid)
	}
	final := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 105, 104, invalid, make(chan struct{}, 4), func(int) {})
	if !final.Complete || final.CheckedAddresses != 105 || final.FailedAddresses != 0 || final.BlockNumber != 101 || final.Items[0].LastSuccessAt == 0 || final.Items[0].Error != "" {
		t.Fatalf("reorg failed to restart all rows at new block: %+v", final)
	}
}

func TestCryptoPortfolioPersistentErrorsStillRefreshHealthyBalances(t *testing.T) {
	for _, failure := range []string{"address", "gas"} {
		t.Run(failure, func(t *testing.T) {
			a, _, wallet, fixture := cryptoPortfolioFixture(t, 2)
			s, e := a.store.cryptoPaymentSettings()
			if e != nil {
				t.Fatal(e)
			}
			assets := []CryptoAsset{}
			for _, asset := range s.Assets {
				if asset.ChainID == 1 {
					assets = append(assets, asset)
				}
			}
			fixture.mu.Lock()
			if failure == "address" {
				fixture.failAddress = fixture.base.receive
			} else {
				fixture.failGas = true
			}
			fixture.mu.Unlock()
			first := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 2, 1, CryptoWalletPortfolio{}, make(chan struct{}, 4), func(int) {})
			if first.Complete || first.resumable {
				t.Fatal("full failed pass froze network", first)
			}
			healthyAddress, e := cryptoDeriveAddress(wallet.XPub, wallet.Path, 1)
			if e != nil {
				t.Fatal(e)
			}
			fixture.base.mu.Lock()
			fixture.base.latest = 101
			fixture.base.final = 101
			fixture.base.native[strings.ToLower(healthyAddress)] = big.NewInt(12345)
			fixture.base.tokens[strings.ToLower(healthyAddress)] = big.NewInt(54321)
			fixture.base.mu.Unlock()
			next := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 2, 1, first, make(chan struct{}, 4), func(int) {})
			if next.Complete || next.resumable || next.BlockNumber != 101 || next.Items[1].NativeAtoms != "12345" || next.Items[1].Tokens[0].BalanceAtoms != "54321" || next.Items[1].BlockNumber != 101 {
				t.Fatalf("persistent error froze healthy balance: %+v", next)
			}
		})
	}
}

func TestCryptoPortfolioExpiredFrozenCycleAdvancesWithoutStarvingTail(t *testing.T) {
	a, _, wallet, fixture := cryptoPortfolioFixture(t, 105)
	s, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	assets := []CryptoAsset{}
	for _, asset := range s.Assets {
		if asset.ChainID == 1 {
			assets = append(assets, asset)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := a.cryptoRefreshPortfolio(ctx, wallet, s.Chains[0], assets, 105, 104, CryptoWalletPortfolio{}, make(chan struct{}, 4), func(n int) {
		if n >= 100 {
			cancel()
		}
	})
	first.CheckedAt = time.Now().Add(-cryptoPortfolioMaxFrozenAge - time.Minute).Unix()
	lastAddress, e := cryptoDeriveAddress(wallet.XPub, wallet.Path, 104)
	if e != nil {
		t.Fatal(e)
	}
	fixture.base.mu.Lock()
	fixture.base.final = 101
	fixture.base.latest = 101
	fixture.base.native[strings.ToLower(lastAddress)] = big.NewInt(333)
	fixture.base.tokens[strings.ToLower(lastAddress)] = big.NewInt(777)
	fixture.base.mu.Unlock()
	fixture.mu.Lock()
	before := fixture.calls
	fixture.mu.Unlock()
	tail := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 105, 104, first, make(chan struct{}, 4), func(int) {})
	if tail.Complete || !tail.resumable || tail.BlockNumber != 101 || tail.CheckedAddresses != 5 || tail.Items[104].NativeAtoms != "333" || tail.Items[104].Tokens[0].BalanceAtoms != "777" || tail.Items[0].BlockNumber != 100 || !tail.Items[0].Stale || tail.Items[0].Error == "" || tail.CheckedAt == first.CheckedAt {
		t.Fatalf("expired snapshot restarted before tail or mixed current rows: %+v", tail)
	}
	fixture.mu.Lock()
	calls := fixture.calls - before
	fixture.mu.Unlock()
	if calls > 60 {
		t.Fatalf("expiry re-scanned old prefix: %d RPC calls", calls)
	}
	wrapped := a.cryptoRefreshPortfolio(context.Background(), wallet, s.Chains[0], assets, 105, 104, tail, make(chan struct{}, 4), func(int) {})
	if !wrapped.Complete || wrapped.CheckedAddresses != 105 || wrapped.BlockNumber != 101 || wrapped.Items[0].Error != "" || wrapped.Items[104].NativeAtoms != "333" {
		t.Fatalf("new snapshot failed to wrap prefix: %+v", wrapped)
	}
}
