package wallet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

const (
	baseChainID            = 8453
	evmNativeGasLimit      = 21000
	evmGasLimitBumpDiv     = 5
	defaultPriorityFeeGwei = 1_000_000_000
)

func baseMaxTransfer(ctx context.Context, address string) (*MaxTransferResult, error) {
	balance, err := evmGetBalance(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("getting base balance: %w", err)
	}

	_, maxFeePerGas, err := evmSuggestedFees(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting base fees: %w", err)
	}

	feeUnits := new(big.Int).Mul(big.NewInt(evmNativeGasLimit), maxFeePerGas)
	maxUnits := new(big.Int).Sub(new(big.Int).Set(balance), feeUnits)
	if maxUnits.Sign() < 0 {
		maxUnits = big.NewInt(0)
	}

	return &MaxTransferResult{
		Max: formatEVMUnits(maxUnits, ethDecimals),
		Fee: formatEVMUnits(feeUnits, ethDecimals),
	}, nil
}

func (c *Client) transferBase(ctx context.Context, walletName, to, amount, token string) (*PayResult, error) {
	from, err := c.AddressForChain(ctx, walletName, ChainBase)
	if err != nil {
		return nil, fmt.Errorf("getting sender address: %w", err)
	}

	txBytes, err := buildBaseTransferTx(ctx, from, to, amount, token)
	if err != nil {
		return nil, err
	}

	txHex := hex.EncodeToString(txBytes)
	out, err := c.run(ctx, "sign", "send-tx",
		"--chain", owsChainArg(ChainBase),
		"--wallet", walletName,
		"--tx", txHex,
		"--rpc-url", baseRPC,
		"--json",
	)
	if err != nil {
		return nil, fmt.Errorf("base transfer to %s: %w", to, err)
	}

	result := &PayResult{
		Status: string(out),
		Amount: amount,
	}
	parseBroadcastResult(out, result)
	return result, nil
}

func buildBaseTransferTx(ctx context.Context, from, to, amount, token string) ([]byte, error) {
	fromAddr, err := normalizeEVMAddress(from)
	if err != nil {
		return nil, fmt.Errorf("invalid sender address: %w", err)
	}
	toAddr, err := normalizeEVMAddress(to)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient address: %w", err)
	}

	nonce, err := evmGetTransactionCount(ctx, fromAddr)
	if err != nil {
		return nil, fmt.Errorf("getting base nonce: %w", err)
	}
	maxPriorityFeePerGas, maxFeePerGas, err := evmSuggestedFees(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting base fees: %w", err)
	}

	var (
		txTo     string
		txValue  = big.NewInt(0)
		txData   []byte
		gasLimit uint64
	)

	switch strings.ToUpper(strings.TrimSpace(token)) {
	case "", "ETH":
		txTo = toAddr
		txValue, err = parseEVMAmount(amount, ethDecimals)
		if err != nil {
			return nil, err
		}
		gasLimit = evmNativeGasLimit
	case "USDC":
		txTo, err = normalizeEVMAddress(baseUSDCContract)
		if err != nil {
			return nil, fmt.Errorf("invalid base USDC contract: %w", err)
		}
		units, err := parseEVMAmount(amount, baseUSDCDecimals)
		if err != nil {
			return nil, err
		}
		txData, err = encodeERC20Transfer(toAddr, units)
		if err != nil {
			return nil, fmt.Errorf("encoding ERC-20 transfer: %w", err)
		}
		estimate, err := evmEstimateGas(ctx, fromAddr, txTo, txValue, txData)
		if err != nil {
			return nil, fmt.Errorf("estimating base USDC gas: %w", err)
		}
		gasLimit = evmBumpedGasLimit(estimate)
	default:
		return nil, fmt.Errorf("unsupported token: %s", token)
	}

	txBytes, err := encodeEIP1559UnsignedTx(baseChainID, nonce, maxPriorityFeePerGas, maxFeePerGas, gasLimit, txTo, txValue, txData)
	if err != nil {
		return nil, fmt.Errorf("building base transaction: %w", err)
	}
	return txBytes, nil
}

func parseBroadcastResult(out []byte, result *PayResult) {
	if result == nil {
		return
	}

	var payload struct {
		TxHash          string `json:"txHash"`
		TransactionHash string `json:"transaction_hash"`
		TxResponse      struct {
			TxHash string `json:"txhash"`
		} `json:"tx_response"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return
	}
	switch {
	case payload.TransactionHash != "":
		result.TxHash = payload.TransactionHash
	case payload.TxHash != "":
		result.TxHash = payload.TxHash
	case payload.TxResponse.TxHash != "":
		result.TxHash = payload.TxResponse.TxHash
	}
}

func normalizeEVMAddress(address string) (string, error) {
	raw, err := decodeEVMAddress(address)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(raw), nil
}

func decodeEVMAddress(address string) ([]byte, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(address), "0x")
	if len(trimmed) != 40 {
		return nil, fmt.Errorf("invalid evm address %q", address)
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decoding evm address %q: %w", address, err)
	}
	return decoded, nil
}

func parseEVMAmount(s string, decimals int) (*big.Int, error) {
	value := strings.TrimSpace(s)
	if value == "" {
		return nil, fmt.Errorf("amount is required")
	}
	if strings.HasPrefix(value, "-") {
		return nil, fmt.Errorf("amount must be positive")
	}

	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid amount %q", s)
	}

	wholePart := parts[0]
	if wholePart == "" {
		wholePart = "0"
	}
	if !decimalDigitsOnly(wholePart) {
		return nil, fmt.Errorf("invalid amount %q", s)
	}

	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
		if !decimalDigitsOnly(fracPart) {
			return nil, fmt.Errorf("invalid amount %q", s)
		}
		if len(fracPart) > decimals {
			return nil, fmt.Errorf("too many decimal places in %q", s)
		}
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole, ok := new(big.Int).SetString(wholePart, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	whole.Mul(whole, scale)

	if fracPart == "" {
		return whole, nil
	}

	paddedFrac := fracPart + strings.Repeat("0", decimals-len(fracPart))
	frac, ok := new(big.Int).SetString(paddedFrac, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	return whole.Add(whole, frac), nil
}

func decimalDigitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func encodeERC20Transfer(to string, amount *big.Int) ([]byte, error) {
	toBytes, err := decodeEVMAddress(to)
	if err != nil {
		return nil, err
	}
	if amount == nil || amount.Sign() < 0 {
		return nil, fmt.Errorf("invalid ERC-20 amount")
	}

	data := make([]byte, 0, 4+32+32)
	data = append(data, 0xa9, 0x05, 0x9c, 0xbb)
	data = append(data, make([]byte, 12)...)
	data = append(data, toBytes...)
	data = append(data, leftPadBytes(amount.Bytes(), 32)...)
	return data, nil
}

func leftPadBytes(src []byte, size int) []byte {
	if len(src) >= size {
		return src[len(src)-size:]
	}
	out := make([]byte, size)
	copy(out[size-len(src):], src)
	return out
}

func evmGetTransactionCount(ctx context.Context, address string) (uint64, error) {
	value, err := evmRPCQuantity(ctx, "eth_getTransactionCount", []interface{}{address, "pending"})
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("nonce overflows uint64")
	}
	return value.Uint64(), nil
}

func evmEstimateGas(ctx context.Context, from, to string, value *big.Int, data []byte) (uint64, error) {
	arg := map[string]string{
		"from":  from,
		"to":    to,
		"value": evmHexQuantity(value),
		"data":  "0x" + hex.EncodeToString(data),
	}
	quantity, err := evmRPCQuantity(ctx, "eth_estimateGas", []interface{}{arg})
	if err != nil {
		return 0, err
	}
	if !quantity.IsUint64() {
		return 0, fmt.Errorf("gas estimate overflows uint64")
	}
	return quantity.Uint64(), nil
}

func evmSuggestedFees(ctx context.Context) (*big.Int, *big.Int, error) {
	gasPrice, err := evmRPCQuantity(ctx, "eth_gasPrice", []interface{}{})
	if err != nil {
		return nil, nil, err
	}

	priorityFee, err := evmRPCQuantity(ctx, "eth_maxPriorityFeePerGas", []interface{}{})
	if err != nil || priorityFee.Sign() <= 0 {
		priorityFee = big.NewInt(defaultPriorityFeeGwei)
		if gasPrice.Sign() > 0 && priorityFee.Cmp(gasPrice) > 0 {
			priorityFee = new(big.Int).Set(gasPrice)
		}
	}

	maxFee := new(big.Int).Set(gasPrice)
	if baseFee, err := evmLatestBaseFeePerGas(ctx); err == nil && baseFee.Sign() > 0 {
		maxFee = new(big.Int).Mul(baseFee, big.NewInt(2))
		maxFee.Add(maxFee, priorityFee)
	}
	if maxFee.Cmp(gasPrice) < 0 {
		maxFee = new(big.Int).Set(gasPrice)
	}
	if maxFee.Cmp(priorityFee) < 0 {
		maxFee = new(big.Int).Set(priorityFee)
	}
	return priorityFee, maxFee, nil
}

func evmLatestBaseFeePerGas(ctx context.Context) (*big.Int, error) {
	body, err := baseRPCCall(ctx, "eth_getBlockByNumber", []interface{}{"latest", false})
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result struct {
			BaseFeePerGas string `json:"baseFeePerGas"`
		} `json:"result"`
		Error *evmRPCError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing eth_getBlockByNumber: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("eth_getBlockByNumber: %s", resp.Error.Message)
	}
	return parseHexBigInt(resp.Result.BaseFeePerGas)
}

func evmRPCQuantity(ctx context.Context, method string, params interface{}) (*big.Int, error) {
	body, err := baseRPCCall(ctx, method, params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result string       `json:"result"`
		Error  *evmRPCError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
	}
	return parseHexBigInt(resp.Result)
}

func evmHexQuantity(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0x0"
	}
	return "0x" + value.Text(16)
}

func evmBumpedGasLimit(estimate uint64) uint64 {
	if estimate == 0 {
		return 0
	}
	return estimate + estimate/evmGasLimitBumpDiv
}

func encodeEIP1559UnsignedTx(
	chainID int64,
	nonce uint64,
	maxPriorityFeePerGas, maxFeePerGas *big.Int,
	gasLimit uint64,
	to string,
	value *big.Int,
	data []byte,
) ([]byte, error) {
	toBytes, err := decodeEVMAddress(to)
	if err != nil {
		return nil, err
	}

	encoded := rlpEncodeList(
		rlpEncodeBigInt(big.NewInt(chainID)),
		rlpEncodeUint64(nonce),
		rlpEncodeBigInt(maxPriorityFeePerGas),
		rlpEncodeBigInt(maxFeePerGas),
		rlpEncodeUint64(gasLimit),
		rlpEncodeBytes(toBytes),
		rlpEncodeBigInt(value),
		rlpEncodeBytes(data),
		rlpEncodeList(),
	)
	return append([]byte{0x02}, encoded...), nil
}

func rlpEncodeUint64(value uint64) []byte {
	if value == 0 {
		return rlpEncodeBytes(nil)
	}
	return rlpEncodeBytes(new(big.Int).SetUint64(value).Bytes())
}

func rlpEncodeBigInt(value *big.Int) []byte {
	if value == nil || value.Sign() == 0 {
		return rlpEncodeBytes(nil)
	}
	return rlpEncodeBytes(value.Bytes())
}

func rlpEncodeBytes(value []byte) []byte {
	length := len(value)
	if length == 1 && value[0] < 0x80 {
		return append([]byte(nil), value...)
	}
	prefix := rlpLengthPrefix(0x80, 0xb7, length)
	return append(prefix, value...)
}

func rlpEncodeList(items ...[]byte) []byte {
	totalLen := 0
	for _, item := range items {
		totalLen += len(item)
	}
	out := make([]byte, 0, len(rlpLengthPrefix(0xc0, 0xf7, totalLen))+totalLen)
	out = append(out, rlpLengthPrefix(0xc0, 0xf7, totalLen)...)
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func rlpLengthPrefix(shortBase, longBase byte, length int) []byte {
	if length <= 55 {
		return []byte{shortBase + byte(length)}
	}
	lenBytes := new(big.Int).SetUint64(uint64(length)).Bytes()
	return append([]byte{longBase + byte(len(lenBytes))}, lenBytes...)
}
