package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// RPCHandler dispatches wallet.* RPC methods. All methods return a
// helpful error when the ows binary is not installed.
type RPCHandler struct {
	client     *Client
	emit       Emitter
	installing atomic.Bool
}

// NewRPCHandler creates an RPC handler for wallet operations.
// A nil client is allowed; methods will return ErrNotInstalled.
// Pass emit to receive install/update progress events.
func NewRPCHandler(client *Client, emit Emitter) *RPCHandler {
	return &RPCHandler{client: client, emit: emit}
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
	case "wallet.install":
		result, err = h.rpcInstall()
	case "wallet.checkUpdate":
		result, err = h.rpcCheckUpdate()
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
	case "wallet.maxTransfer":
		result, err = h.rpcMaxTransfer(ctx, params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

func (h *RPCHandler) rpcStatus(ctx context.Context) (interface{}, error) {
	// Re-check for the binary if we don't have a client yet.
	// Handles manual installs or installs by other processes.
	if h.client == nil {
		h.client = NewClient()
	}
	return h.client.Status(ctx)
}

func (h *RPCHandler) rpcInstall() (interface{}, error) {
	if !h.installing.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("install already in progress")
	}

	go func() {
		defer h.installing.Store(false)

		current := InstalledVersion()
		info, err := CheckRelease(current)
		if err != nil {
			h.emit("wallet:install:error", map[string]string{"message": err.Error()})
			return
		}
		if !info.Available {
			h.emit("wallet:install:complete", map[string]string{
				"version": info.Current,
				"status":  "already up to date",
			})
			return
		}

		err = Install(info, func(downloaded, total int64) {
			h.emit("wallet:install:progress", map[string]int64{
				"downloaded": downloaded,
				"total":      total,
			})
		})
		if err != nil {
			h.emit("wallet:install:error", map[string]string{"message": err.Error()})
			return
		}

		// Refresh client now that the binary is installed.
		h.client = NewClient()

		h.emit("wallet:install:complete", map[string]string{
			"version": info.Latest,
			"status":  "installed",
		})
	}()

	return map[string]string{"status": "installing"}, nil
}

func (h *RPCHandler) rpcCheckUpdate() (interface{}, error) {
	current := InstalledVersion()
	return CheckRelease(current)
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

func (h *RPCHandler) rpcMaxTransfer(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p walletParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	return h.client.MaxTransfer(ctx, p.Wallet)
}
