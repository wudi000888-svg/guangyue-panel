package controlplane

import "strings"

const cryptoPlasmaChainID int64 = 9745

// Pinned to Plasma's own mainnet node template, not inferred from agreement
// between RPC operators. Both providers must also agree on each canonical block.
// https://github.com/PlasmaLaboratories/node-templates/blob/main/config/mainnet/genesis.json
const cryptoPlasmaGenesisHash = "0x0d0ccca452bdb244100115e37de64ca640a255585d5a94df5610052a6dada558"

func cryptoChainAutomatic(chainID int64) bool {
	return chainID == 1 || chainID == 56 || chainID == cryptoPlasmaChainID || chainID == 101
}

func cryptoNativeDecimals(chain CryptoChain) int {
	if chain.NativeDecimals > 0 {
		return chain.NativeDecimals
	}
	return 18
}

// Solana base58 mints are case sensitive. EVM contract addresses are not.
func cryptoNormalizeContract(chainID int64, contract string) string {
	if chainID == 101 {
		return contract
	}
	return strings.ToLower(contract)
}

// Reading settings from an older release reveals new networks as disabled
// options without enabling payment, changing a revision, or resealing history.
// Optional chain fields keep old signed/HMAC JSON snapshots byte compatible.
func cryptoMergeNewDefaults(settings CryptoPaymentSettings) CryptoPaymentSettings {
	defaults := cryptoDefaultSettings()
	for _, chain := range defaults.Chains {
		if _, exists := settings.chain(chain.ChainID); !exists {
			settings.Chains = append(settings.Chains, chain)
		}
	}
	for _, asset := range defaults.Assets {
		if _, exists := settings.asset(asset.ID); !exists {
			settings.Assets = append(settings.Assets, asset)
		}
	}
	return settings
}
