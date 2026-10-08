package controlplane

import (
	"slices"
)

// Network selection constrains new payment and withdrawal intents. Historical
// chain transactions retain their frozen network and must continue reconciling.
func cryptoWalletChainIDs(ids []int64) ([]int64, error) {
	if ids == nil {
		return []int64{1, 56}, nil
	}
	if len(ids) == 0 || len(ids) > 5 {
		return nil, commerceFail(400, "请选择钱包支持的网络")
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	for i, id := range out {
		if id != 1 && id != 56 && id != 10 && id != 42161 && id != 8453 || i > 0 && out[i-1] == id {
			return nil, commerceFail(400, "钱包网络不在支持清单中或重复")
		}
	}
	return out, nil
}

func cryptoWalletSupports(v CryptoWallet, chainID int64) bool {
	return slices.Contains(v.SupportedChainIDs, chainID)
}

func cryptoWalletFundingDTO(v CryptoWallet) (CryptoWallet, error) {
	f, e := cryptoFunding(v)
	if e != nil {
		return v, e
	}
	v.FundingAddress, v.FundingPath = f.Address, f.Path
	return v, nil
}
