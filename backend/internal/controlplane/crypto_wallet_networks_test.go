package controlplane

import (
	"context"
	"slices"
	"testing"
)

func cryptoSelectedWallet(t *testing.T, a *App, owner Record, hot bool, ids []int64, status int) CryptoWallet {
	t.Helper()
	xp, path, first, e := cryptoHDFromMnemonic(cryptoTestMnemonic)
	if e != nil {
		t.Fatal(e)
	}
	in := object{"name": "BNB收款钱包", "xpub": xp, "path": path, "password": commerceTestPassword, "operation_id": randomToken(24), "supported_chain_ids": ids}
	route := "wallets/xpub"
	if hot {
		route = "wallets/hot"
		in["mnemonic"], in["first_address"], in["engine_version"], in["risk_ack"] = cryptoTestMnemonic, first, "4.8.4", true
	}
	w := req(t, a, owner, "POST", cryptoWalletPrefix+route, in)
	if w.Code != status {
		t.Fatalf("selected wallet: %d want %d: %s", w.Code, status, w.Body.String())
	}
	if status != 201 {
		return CryptoWallet{}
	}
	return decoded[CryptoWallet](t, w, status)
}

func TestCryptoWalletSelectedNetworksAndImmediateGas(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	for _, ids := range [][]int64{{}, {56, 56}, {999}} {
		cryptoSelectedWallet(t, a, owner, true, ids, 400)
	}
	v := cryptoSelectedWallet(t, a, owner, true, []int64{56}, 201)
	if !slices.Equal(v.SupportedChainIDs, []int64{56}) || v.FundingAddress == "" || v.FundingPath != v.Path+"/1/0" || v.FundingAddress == v.FirstAddress || v.NextIndex != 0 {
		t.Fatal("new wallet missing selected network or independent Gas branch", v)
	}
	var funding string
	if e := a.store.db.QueryRow("SELECT address FROM crypto_funding_addresses WHERE wallet_id=?", v.ID).Scan(&funding); e != nil || funding != v.FundingAddress {
		t.Fatal("Gas address not persisted at creation", funding, e)
	}
	list := decoded[struct {
		Items []CryptoWallet `json:"items"`
	}](t, req(t, a, owner, "GET", cryptoWalletPrefix+"wallets", nil), 200)
	if len(list.Items) != 1 || list.Items[0].FundingAddress != funding || !slices.Equal(list.Items[0].SupportedChainIDs, []int64{56}) {
		t.Fatal("wallet list depends on RPC or loses network", list)
	}
	if _, e := a.cryptoWalletBalance(context.Background(), v.ID, "ethereum", -1); e == nil {
		t.Fatal("BNB-only wallet allowed Ethereum balance RPC")
	}
	backup := cryptoTestExport(t, a, owner, v)
	dump, e := cryptoDecryptBackup(backup, cryptoBackupTestPassword)
	if e != nil || !slices.Equal(dump.Wallet.SupportedChainIDs, []int64{56}) || dump.Wallet.FundingAddress != funding || cryptoValidateDump(dump) != nil {
		t.Fatal("backup lost chain policy or Gas descriptor", e)
	}
	dump.Wallet.FundingAddress = v.FirstAddress
	if cryptoValidateDump(dump) == nil {
		t.Fatal("altered Gas descriptor accepted")
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}

func TestCryptoWalletWatchOnlySelectedNetwork(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	v := cryptoSelectedWallet(t, a, owner, false, []int64{56}, 201)
	if v.FundingAddress != "" || v.FundingPath != "" || !slices.Equal(v.SupportedChainIDs, []int64{56}) {
		t.Fatal("watch-only wallet advertised local Gas signing", v)
	}
}

func TestCryptoSweepAutomaticBudgetAndSelectedNetwork(t *testing.T) {
	a, owner, wallet, _ := cryptoSweepFixture(t)
	in := cryptoSweepTestInput()
	in.MaxGasAtoms = ""
	p, e := a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, in)
	if e != nil || !p.CanSubmit || p.TotalGasAtoms == "0" || p.MaxGasAtoms != p.TotalGasAtoms || p.Quote == "" {
		t.Fatal("automatic budget did not freeze estimate", p, e)
	}
	if _, e = a.store.db.Exec("UPDATE crypto_wallets SET supported_chain_ids='[56]' WHERE id=?", wallet.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.cryptoPreviewSweep(context.Background(), owner, wallet.ID, in); e == nil {
		t.Fatal("preview allowed an unselected chain")
	}
	if _, e = a.cryptoCreateSweep(context.Background(), owner, wallet.ID, cryptoSweepInput{Quote: p.Quote, OperationID: randomToken(24)}); e == nil {
		t.Fatal("stale quote allowed an unselected chain")
	}
}
