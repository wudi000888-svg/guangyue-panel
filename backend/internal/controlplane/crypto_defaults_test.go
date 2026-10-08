package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCryptoSetupAtomicPreflightReplayAndPermissions(t *testing.T) {
	a, owner, wallet, rpc := cryptoSweepFixture(t)
	var calls, rates atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var v struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &v)
		if v.Method == "eth_getLogs" {
			jsonResponse(w, 200, object{"id": 1, "jsonrpc": "2.0", "result": []any{}})
			return
		}
		rpc.serve(w, r)
	})
	primary := httptest.NewServer(handler)
	defer primary.Close()
	backup := httptest.NewServer(handler)
	defer backup.Close()
	defaults := map[int64][2]string{1: {primary.URL, backup.URL}}
	a.cryptoRateFetch = func(context.Context) (cryptoMarketRates, error) { rates.Add(1); return cryptoTestRates(), nil }
	in := cryptoSetupInput{WalletID: wallet.ID, ChainIDs: []int64{1}, AssetIDs: []string{"ethereum-usdc"}, OperationID: randomToken(24)}
	settings, e := a.setupCryptoPayments(context.Background(), owner, in, defaults)
	if e != nil {
		t.Fatal(e)
	}
	if !settings.Enabled || settings.RateMode != "automatic" || settings.SetupCheckedAt == 0 || settings.Chains[0].RPCURL != "" || !settings.Chains[0].HasRPC || !settings.Chains[0].HasRPCBackup {
		t.Fatal("invalid setup response", settings)
	}
	a.store.commerceErr = errors.New("fixture integrity failure")
	badStatus := decoded[struct {
		Readiness struct {
			Ready bool `json:"ready"`
		} `json:"readiness"`
	}](t, req(t, a, owner, "GET", "/api/commerce/crypto/admin/setup/status", nil), 200)
	if badStatus.Readiness.Ready {
		t.Fatal("integrity failure reported ready")
	}
	a.store.commerceErr = nil
	beforeCalls, beforeRates := calls.Load(), rates.Load()
	replay, e := a.setupCryptoPayments(context.Background(), owner, in, defaults)
	if e != nil {
		t.Fatal(e)
	}
	if replay.Revision != settings.Revision || !replay.Chains[0].HasRPC || !replay.Chains[0].HasRPCBackup || calls.Load() != beforeCalls || rates.Load() != beforeRates {
		t.Fatal("setup replay repeated side effects or lost RPC flags")
	}
	stored, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	if stored.Chains[0].RPCURL != primary.URL || stored.Assets[1].CNYPerToken != "7.1" {
		t.Fatal("defaults or rates not saved")
	}
	bad := in
	bad.OperationID = randomToken(24)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer broken.Close()
	if _, e = a.setupCryptoPayments(context.Background(), owner, bad, map[int64][2]string{1: {primary.URL, broken.URL}}); e == nil {
		t.Fatal("partial preflight accepted")
	}
	unchanged, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	if unchanged.Revision != stored.Revision || unchanged.Chains[0].RPCBackupURL != backup.URL {
		t.Fatal("failed preflight changed settings")
	}
	// A failed attempt is not consumed; the same operation can safely retry.
	if _, e = a.setupCryptoPayments(context.Background(), owner, bad, defaults); e != nil {
		t.Fatal(e)
	}
	current, e := a.store.cryptoPaymentSettings()
	if e != nil {
		t.Fatal(e)
	}
	for i := range current.Assets {
		current.Assets[i].Enabled = false
	}
	doc, e := a.store.vault.seal(current)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_payment_settings SET doc=? WHERE id=1", doc); e != nil {
		t.Fatal(e)
	}
	status := decoded[struct {
		Readiness struct {
			Ready bool `json:"ready"`
		} `json:"readiness"`
	}](t, req(t, a, owner, "GET", "/api/commerce/crypto/admin/setup/status", nil), 200)
	if status.Readiness.Ready {
		t.Fatal("no enabled assets reported ready")
	}
	if _, e = a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[56]' WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	bad.OperationID = randomToken(24)
	if _, e = a.setupCryptoPayments(context.Background(), owner, bad, defaults); e == nil {
		t.Fatal("wallet chain gate bypassed")
	}
	bad.ChainIDs = []int64{42161}
	bad.AssetIDs = []string{"arbitrum-usdc"}
	bad.OperationID = randomToken(24)
	if _, e = a.setupCryptoPayments(context.Background(), owner, bad, defaults); e == nil {
		t.Fatal("L2 setup bypassed finality gate")
	}
	member := testUser(t, a, "setup-member", "user")
	for _, actor := range []Record{member, owner} {
		if actor.Role == "owner" {
			a.cfg.Role = "business"
			a.cfg.LocalManagement = false
		}
		w := req(t, a, actor, "POST", "/api/commerce/crypto/admin/setup", object{"wallet_id": wallet.ID, "chain_ids": []int64{56}, "asset_ids": []string{"bsc-usdc"}, "password": commerceTestPassword, "operation_id": randomToken(24)})
		if w.Code != 403 {
			t.Fatal("setup permission", w.Code, w.Body.String())
		}
	}
}

func TestCryptoSettingsCanDisableExpiredRates(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	wallet, _ := cryptoTestCreate(t, a, owner, false)
	s := cryptoDefaultSettings()
	s.WalletID = wallet.ID
	s.Assets[1].Enabled = true
	s.Assets[1].CNYPerToken = "7"
	s.Assets[1].RateExpiresAt = 1
	makeBody := func() object {
		out := object{}
		_ = json.Unmarshal(jsonBytes(s), &out)
		out["password"] = commerceTestPassword
		out["operation_id"] = randomToken(24)
		return out
	}
	if w := req(t, a, owner, "POST", "/api/commerce/crypto/admin/settings", makeBody()); w.Code != 200 {
		t.Fatal("cannot disable expired quotes", w.Code, w.Body.String())
	}
	s.Revision = 1
	s.Enabled = true
	if w := req(t, a, owner, "POST", "/api/commerce/crypto/admin/settings", makeBody()); w.Code != 400 {
		t.Fatal("enabled expired quotes", w.Code, w.Body.String())
	}
	saved, e := a.store.cryptoPaymentSettings()
	if e != nil || saved.Enabled || saved.Revision != 1 {
		t.Fatal("invalid enabling changed settings", e)
	}
}
