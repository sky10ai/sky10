package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
)

var baseRPC = "https://mainnet.base.org"

const (
	baseUSDCContract = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	ethDecimals      = 18
	baseUSDCDecimals = 6
)

// baseBalances queries the Base RPC for native ETH and USDC balances.
func baseBalances(ctx context.Context, address string) (*BalanceResult, error) {
	result := &BalanceResult{
		Address: address,
		Chain:   ChainBase,
	}

	type balanceResult struct {
		value *big.Int
		err   error
	}

	ethCh := make(chan balanceResult, 1)
	usdcCh := make(chan balanceResult, 1)

	go func() {
		value, err := evmGetBalance(ctx, address)
		ethCh <- balanceResult{value: value, err: err}
	}()
	go func() {
		value, err := evmGetERC20Balance(ctx, address, baseUSDCContract)
		usdcCh <- balanceResult{value: value, err: err}
	}()

	ethResult := <-ethCh
	usdcResult := <-usdcCh

	ethBalance := "0"
	if ethResult.err == nil && ethResult.value != nil && ethResult.value.Sign() > 0 {
		ethBalance = formatEVMUnits(ethResult.value, ethDecimals)
	}
	result.Tokens = append(result.Tokens, TokenBalance{
		Symbol:  "ETH",
		Balance: ethBalance,
	})

	usdcBalance := "0"
	if usdcResult.err == nil && usdcResult.value != nil && usdcResult.value.Sign() > 0 {
		usdcBalance = formatEVMUnits(usdcResult.value, baseUSDCDecimals)
	}
	result.Tokens = append(result.Tokens, TokenBalance{
		Symbol:  "USDC",
		Balance: usdcBalance,
		Mint:    baseUSDCContract,
	})

	return result, nil
}

func evmGetBalance(ctx context.Context, address string) (*big.Int, error) {
	body, err := baseRPCCall(ctx, "eth_getBalance", []interface{}{address, "latest"})
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result string       `json:"result"`
		Error  *evmRPCError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing eth_getBalance: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("eth_getBalance: %s", resp.Error.Message)
	}
	return parseHexBigInt(resp.Result)
}

func evmGetERC20Balance(ctx context.Context, address, contract string) (*big.Int, error) {
	data, err := encodeERC20BalanceOf(address)
	if err != nil {
		return nil, err
	}

	body, err := baseRPCCall(ctx, "eth_call", []interface{}{
		map[string]string{
			"to":   contract,
			"data": data,
		},
		"latest",
	})
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result string       `json:"result"`
		Error  *evmRPCError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing eth_call: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("eth_call: %s", resp.Error.Message)
	}
	return parseHexBigInt(resp.Result)
}

func encodeERC20BalanceOf(address string) (string, error) {
	decoded, err := decodeEVMAddress(address)
	if err != nil {
		return "", err
	}
	return "0x70a08231" + strings.Repeat("0", 24) + hex.EncodeToString(decoded), nil
}

func formatEVMUnits(value *big.Int, decimals int) string {
	if value == nil || value.Sign() <= 0 {
		return "0"
	}

	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Div(new(big.Int).Set(value), divisor)
	frac := new(big.Int).Mod(new(big.Int).Set(value), divisor)
	if frac.Sign() == 0 {
		return whole.String()
	}

	fracString := frac.String()
	if len(fracString) < decimals {
		fracString = strings.Repeat("0", decimals-len(fracString)) + fracString
	}
	return trimTrailingZeros(whole.String() + "." + fracString)
}

func parseHexBigInt(value string) (*big.Int, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if trimmed == "" {
		return big.NewInt(0), nil
	}
	parsed, ok := new(big.Int).SetString(trimmed, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex quantity %q", value)
	}
	return parsed, nil
}

type evmRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func baseRPCCall(ctx context.Context, method string, params interface{}) ([]byte, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling base RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseRPC, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating base RPC request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("base RPC: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading base RPC response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("base RPC status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
