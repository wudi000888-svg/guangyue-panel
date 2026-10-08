package controlplane

import (
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/tyler-smith/go-bip39"
)

// Public BIP39 fixture. It has no funds and must never be used for a real wallet.
const cryptoTestMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func TestCryptoHDPublicVectors(t *testing.T) {
	xpub, path, first, err := cryptoHDFromMnemonic(cryptoTestMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	// Independently reproduced with the actual official Wallet Core 4.8.4 WASM
	// in a browser, including the explicit XPUB version (Ethereum defaults may
	// otherwise produce an empty extended key).
	if xpub != "xpub6DCoCpSuQZB2jawqnGMEPS63ePKWkwWPH4TU45Q7LPXWuNd8TMtVxRrgjtEshuqpK3mdhaWHPFsBngh5GFZaM6si3yZdUsT8ddYM3PwnATt" {
		t.Fatal("Wallet Core/BIP32 public key mismatch")
	}
	if path != cryptoHotPath || first != "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" {
		t.Fatalf("BIP44/EIP55 mismatch: %s %s", path, first)
	}
	branch, err := cryptoReceiveXPub(xpub, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = cryptoValidateXPub(branch, path+"/0"); err != nil {
		t.Fatal(err)
	}
	if identity, err := cryptoReceiveXPub(branch, path+"/0"); err != nil || identity != branch {
		t.Fatal("account/branch identity mismatch", err)
	}
	for index, want := range []string{"0x9858EfFD232B4033E47d90003D41EC34EcaEda94", "0x6Fac4D18c912343BF86fa7049364Dd4E424Ab9C0"} {
		for _, desc := range []struct{ xpub, path string }{{xpub, path}, {branch, path + "/0"}} {
			got, err := cryptoDeriveAddress(desc.xpub, desc.path, uint32(index))
			if err != nil || got != want {
				t.Fatalf("address %d: %s != %s (%v)", index, got, want, err)
			}
		}
	}
	// A high public index uses the exact same /0/i path as Wallet Core; no
	// floating-point conversion or accidental extra change level is involved.
	for _, index := range []uint32{50, 65536, hdkeychain.HardenedKeyStart - 1} {
		accountAddress, err := cryptoDeriveAddress(xpub, path, index)
		branchAddress, branchErr := cryptoDeriveAddress(branch, path+"/0", index)
		if err != nil || branchErr != nil || accountAddress != branchAddress {
			t.Fatal("public branch derivation differs", index, err, branchErr)
		}
	}
}

func TestCryptoHDRejectsWrongScopeAndPrivateMaterial(t *testing.T) {
	xpub, path, _, err := cryptoHDFromMnemonic(cryptoTestMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := cryptoReceiveXPub(xpub, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct{ xpub, path string }{
		{xpub, "m"}, {xpub, "m/44'/0'/0'"}, {xpub, "m/44'/60'/1'"},
		{xpub, path + "/0"}, {branch, path}, {branch, path + "/1"},
		{xpub, "m/44'/60'/2147483648'"}, {xpub, "m/44'/60'/00'"},
		{xpub, path + "/0/0"}, {xpub[:110] + "0", path}, {cryptoTestMnemonic, path},
	} {
		if err = cryptoValidateXPub(invalid.xpub, invalid.path); err == nil {
			t.Fatal("accepted invalid scope", invalid.path)
		}
	}
	seed := bip39.NewSeed(cryptoTestMnemonic, "")
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Zero()
	if err = cryptoValidateXPub(master.String(), path); err == nil {
		t.Fatal("accepted xprv")
	}
	if _, err = cryptoDeriveAddress(xpub, path, hdkeychain.HardenedKeyStart); err == nil {
		t.Fatal("accepted hardened receiving address")
	}
	for _, bad := range []string{"", strings.Replace(cryptoTestMnemonic, "about", "abandon", 1), strings.ToUpper(cryptoTestMnemonic), cryptoTestMnemonic + " ", " " + cryptoTestMnemonic} {
		if _, _, _, err = cryptoHDFromMnemonic(bad); err == nil {
			t.Fatal("accepted noncanonical/invalid mnemonic")
		}
	}
}
