package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler dispatches wallet.* RPC methods. All methods return a
// helpful error when the ows binary is not installed.
type RPCHandler struct {
	client *Client
}

// NewRPCHandler creates an RPC handler for wallet operations.
// A nil client is allowed; methods will return ErrNotInstalled.
func NewRPCHandler(client *Client) *RPCHandler {
	return &RPCHandler{client: client}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "wallet.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "wallet.status":
		result, err = h.rpcStatus(ctx)
	case "wallet.create":
		result, err = h.rpcCreate(ctx, params)
	case "wallet.list":
		result, err = h.rpcList(ctx)
	case "wallet.address":
		result, err = h.rpcAddress(ctx, params)
	case "wallet.balance":
		result, err = h.rpcBalance(ctx, params)
	case "wallet.pay":
		result, err = h.rpcPay(ctx, params)
	case "wallet.deposit":
		result, err = h.rpcDeposit(ctx, params)
	case "wallet.transfer":
		result, err = h.rpcTransfer(ctx, params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

func (h *RPCHandler) rpcStatus(ctx context.Context) (interface{}, error) {
	return h.client.Status(ctx)
}

type createParams struct {
	Name string `json:"name"`
}

func (h *RPCHandler) rpcCreate(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p createParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return h.client.CreateWallet(ctx, p.Name)
}

func (h *RPCHandler) rpcList(ctx context.Context) (interface{}, error) {
	wallets, err := h.client.ListWallets(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"wallets": wallets,
		"count":   len(wallets),
	}, nil
}

type walletParams struct {
	Wallet string `json:"wallet"`
}

func (h *RPCHandler) rpcAddress(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p walletParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	addr, err := h.client.Address(ctx, p.Wallet)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"wallet":  p.Wallet,
		"chain":   ChainSolana,
		"address": addr,
	}, nil
}

func (h *RPCHandler) rpcBalance(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p walletParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	return h.client.Balance(ctx, p.Wallet)
}

type payParams struct {
	Wallet string `json:"wallet"`
	URL    string `json:"url"`
}

func (h *RPCHandler) rpcPay(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p payParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	if p.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	return h.client.Pay(ctx, p.Wallet, p.URL)
}

func (h *RPCHandler) rpcDeposit(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p walletParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	return h.client.Deposit(ctx, p.Wallet)
}

type transferParams struct {
	Wallet string `json:"wallet"`
	To     string `json:"to"`
	Amount string `json:"amount"`
	Token  string `json:"token,omitempty"`
}

func (h *RPCHandler) rpcTransfer(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p transferParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	if p.To == "" {
		return nil, fmt.Errorf("to address is required")
	}
	if p.Amount == "" {
		return nil, fmt.Errorf("amount is required")
	}
	return h.client.Transfer(ctx, p.Wallet, p.To, p.Amount, p.Token)
}
