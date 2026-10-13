package controlplane

import (
	"math/big"
	"strings"
)

// Legacy EVM transactions expose a single gas price, rather than invented
// EIP-1559 priority/base components. All prices are integer native coin atoms.
func cryptoFeePrice(raw *big.Int, percent int64) *big.Int {
	return new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(raw, big.NewInt(percent)), big.NewInt(99)), big.NewInt(100))
}

func cryptoSweepSelectFee(in cryptoSweepInput, raw *big.Int) (string, *big.Int, error) {
	mode := strings.TrimSpace(in.FeeMode)
	if mode == "" {
		mode = "standard"
	}
	if mode != "custom" && strings.TrimSpace(in.GasPriceAtoms) != "" {
		return "", nil, commerceFail(400, "自定义 Gas 价格仅适用于自定义费用选项")
	}
	switch mode {
	case "economy":
		return mode, cryptoFeePrice(raw, 100), nil
	case "standard":
		return mode, cryptoFeePrice(raw, 110), nil
	case "priority":
		return mode, cryptoFeePrice(raw, 120), nil
	case "custom":
		price, err := cryptoAtoms(in.GasPriceAtoms)
		if err != nil || price.Cmp(raw) < 0 || price.Cmp(cryptoFeePrice(raw, 125)) > 0 {
			return "", nil, commerceFail(400, "自定义 Gas 价格必须介于实时网络价格和其 1.25 倍之间")
		}
		return mode, price, nil
	default:
		return "", nil, commerceFail(400, "请选择有效的手续费选项")
	}
}
