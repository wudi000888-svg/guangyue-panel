package controlplane

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Optional execution test against an isolated, unforked Anvil instance. The two
// endpoints here exercise client code only; they are not independent providers.
// Never point this test at a public network or a node holding valuable funds.
func TestCryptoAnvilPaymentGasAndTokenExecution(t *testing.T) {
	endpoint := os.Getenv("GY_TEST_CRYPTO_RPC")
	if endpoint == "" {
		t.Skip("isolated local Anvil RPC not configured")
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Path != "" {
		t.Fatal("test RPC must be plain HTTP on 127.0.0.1")
	}
	code, e := os.ReadFile(os.Getenv("GY_TEST_CRYPTO_TOKEN_CODE"))
	if e != nil || !strings.HasPrefix(string(code), "0x") {
		t.Fatal("compile the documented test-only ERC20 before running", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	settings := cryptoDefaultSettings()
	chainIndex, assetIndex := 0, 1
	switch os.Getenv("GY_TEST_CRYPTO_CHAIN_ID") {
	case "", "1":
	case "56":
		chainIndex, assetIndex = 1, 3
	default:
		t.Fatal("isolated execution supports only fixture chain 1 or 56")
	}
	chain := settings.Chains[chainIndex]
	chain.Enabled, chain.FinalityVerified = true, true
	chain.RPCURL = endpoint
	backup := *u
	backup.Host = "localhost:" + u.Port()
	chain.RPCBackupURL = backup.String()
	rpc, e := newCryptoEVM(chain, true)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method string, params any, out any) {
		t.Helper()
		if e := rpc.rpc(ctx, endpoint, method, params, out); e != nil {
			t.Fatalf("local RPC %s: %v", method, e)
		}
	}
	var client string
	call("web3_clientVersion", []any{}, &client)
	if !strings.Contains(strings.ToLower(client), "anvil") {
		t.Fatal("test requires disposable Anvil")
	}
	var node object
	call("anvil_nodeInfo", []any{}, &node)
	if fork, ok := node["forkConfig"].(map[string]any); ok && fork["forkUrl"] != nil {
		t.Fatal("forked networks are forbidden in this test")
	}
	var snapshot string
	call("evm_snapshot", []any{}, &snapshot)
	t.Cleanup(func() {
		var reverted bool
		if e := rpc.rpc(context.Background(), endpoint, "evm_revert", []any{snapshot}, &reverted); e != nil || !reverted {
			t.Errorf("restore disposable EVM snapshot: %v", e)
		}
	})
	for _, asset := range settings.Assets {
		if asset.ChainID == chain.ChainID {
			var ok any
			call("anvil_setCode", []any{asset.Contract, string(code)}, &ok)
		}
	}
	mine := func() { var ok any; call("anvil_mine", []any{"0x80"}, &ok) }
	mine() // Make the installed test code visible at a finalized block.
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	wallet, _ := cryptoTestCreate(t, a, owner, true)
	wallet = cryptoTestConfirm(t, a, owner, wallet)
	settings.Enabled, settings.Revision, settings.WalletID = true, 1, wallet.ID
	settings.Chains[chainIndex] = chain
	settings.Assets[assetIndex].Enabled = true
	settings.Assets[assetIndex].CNYPerToken = "7"
	settings.Assets[assetIndex].RateUpdatedAt = time.Now().Unix()
	settings.Assets[assetIndex].RateExpiresAt = time.Now().Unix() + 86400
	sealed, e := a.store.vault.seal(settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.db.Exec("INSERT INTO crypto_payment_settings(id,revision,doc) VALUES(1,1,?)", sealed); e != nil {
		t.Fatal(e)
	}
	order := newOrder(t, a, member, commerceOffer(t, a, owner))
	invoice := decoded[CryptoInvoice](t, req(t, a, member, "POST", "/api/commerce/crypto/invoices", object{"order_id": order.ID, "asset_id": settings.Assets[assetIndex].ID, "operation_id": randomToken(24)}), 201)
	var accounts []string
	call("eth_accounts", []any{}, &accounts)
	if len(accounts) == 0 {
		t.Fatal("test node has no unlocked fixture account")
	}
	amount, e := cryptoAtoms(invoice.ExpectedAtoms)
	if e != nil {
		t.Fatal(e)
	}
	var hash string
	// 0x40c10f19 is mint(address,uint256), available only in the local mock.
	call("eth_sendTransaction", []any{object{"from": accounts[0], "to": invoice.Contract, "data": "0x40c10f19" + cryptoABIAddress(accounts[0]) + fmt.Sprintf("%064x", amount)}}, &hash)
	call("eth_sendTransaction", []any{object{"from": accounts[0], "to": invoice.Contract, "data": cryptoTransferData(invoice.Address, amount)}}, &hash)
	mine()
	stored, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.scanCryptoInvoice(ctx, stored); e != nil {
		t.Fatal("scan executed token transfer", e)
	}
	for range 2 {
		if e = a.paymentWork(); e != nil {
			t.Fatal(e)
		}
		if e = a.commerceWork(time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	paid, e := a.store.order(order.ID)
	if e != nil || paid.State != "completed" {
		current, _ := a.store.cryptoInvoice(a.store.db, invoice.ID)
		view, _ := a.store.decorateCryptoInvoice(current)
		t.Fatalf("real ERC20 receipt did not fulfil order: order=%s invoice=%+v err=%v", paid.State, view, e)
	}
	funding, e := a.store.cryptoFundingAddress(ctx, wallet)
	if e != nil {
		t.Fatal(e)
	}
	input := cryptoSweepTestInput()
	input.ChainID, input.AssetID, input.MaxGasAtoms = chain.ID, invoice.AssetID, ""
	preview, e := a.cryptoPreviewSweep(ctx, owner, wallet.ID, input)
	if e != nil || preview.CanSubmit || len(preview.Items) != 1 || preview.Items[0].TopupAtoms == "0" || preview.TotalGasAtoms == "0" {
		t.Fatal("missing real estimate with empty Gas funding", preview, e)
	}
	var ok any
	call("anvil_setBalance", []any{funding.Address, "0xde0b6b3a7640000"}, &ok)
	mine()
	preview, e = a.cryptoPreviewSweep(ctx, owner, wallet.ID, input)
	if e != nil || !preview.CanSubmit {
		t.Fatal("funded execution preview", preview, e)
	}
	if preview.MaxGasAtoms == "0" || preview.MaxGasAtoms != preview.TotalGasAtoms {
		t.Fatal("automatic fee cap must freeze the buffered estimate", preview)
	}
	job := decoded[CryptoSweepJob](t, req(t, a, owner, "POST", cryptoWalletPrefix+"wallets/"+wallet.ID+"/sweeps", object{"quote": preview.Quote, "password": commerceTestPassword, "operation_id": randomToken(24)}), 201)
	for range 8 {
		if e = a.cryptoSweepWork(ctx); e != nil {
			t.Fatal("execute persistent signed transaction", e)
		}
		mine()
		job, e = a.store.cryptoSweepJob(job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if job.State == "complete" {
			break
		}
	}
	if job.State != "complete" || cryptoSweepTestCount(t, a) != 2 {
		t.Fatal("expected one Gas transfer and one ERC20 transfer", job)
	}
	final, e := rpc.Finalized(ctx)
	if e != nil {
		t.Fatal(e)
	}
	balance, e := rpc.TokenBalance(ctx, invoice.Contract, input.Destination, final.Number)
	if e != nil || balance.Cmp(amount) != 0 {
		t.Fatal("recipient did not receive exact atoms", balance, e)
	}
	remaining, e := rpc.TokenBalance(ctx, invoice.Contract, invoice.Address, final.Number)
	if e != nil || remaining.Cmp(new(big.Int)) != 0 {
		t.Fatal("source token balance not emptied", remaining, e)
	}
	if e = a.cryptoSweepWork(ctx); e != nil || cryptoSweepTestCount(t, a) != 2 {
		t.Fatal("completed job executed twice", e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal("completed on-chain workflow integrity", e)
	}
}
