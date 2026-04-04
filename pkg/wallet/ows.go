// Package wallet wraps the Open Wallet Standard (OWS) CLI for
// agent-to-agent payments on Solana. The ows binary is optional;
// all methods return ErrNotInstalled when it is missing.
package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Solana CAIP-2 chain identifier used by OWS.
const ChainSolana = "solana"

// ErrNotInstalled is returned when the ows binary is not on PATH.
var ErrNotInstalled = errors.New("ows is not installed — see https://openwallet.sh")

// Client wraps the ows CLI binary for wallet operations.
type Client struct {
	bin string
}

// NewClient returns a Client if the ows binary is found on PATH.
// Returns nil otherwise. Callers should check Available() or handle
// ErrNotInstalled from individual methods.
func NewClient() *Client {
	bin, err := exec.LookPath("ows")
	if err != nil {
		return nil
	}
	return &Client{bin: bin}
}

// Available reports whether the ows binary was found.
func (c *Client) Available() bool { return c != nil }

// Wallet describes a wallet returned by the ows CLI.
type Wallet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TokenBalance is a single token's balance.
type TokenBalance struct {
	Symbol  string `json:"symbol"`
	Balance string `json:"balance"`
	Mint    string `json:"mint,omitempty"`
}

// BalanceResult holds the balance response for a wallet on a chain.
type BalanceResult struct {
	Address string         `json:"address"`
	Chain   string         `json:"chain"`
	Tokens  []TokenBalance `json:"tokens"`
}

// PayResult holds the result of an x402 payment.
type PayResult struct {
	TxHash string `json:"transaction_hash"`
	Status string `json:"status"`
	Amount string `json:"amount,omitempty"`
}

// Status returns a summary of OWS availability and wallet state.
func (c *Client) Status(ctx context.Context) (*StatusResult, error) {
	if c == nil {
		return &StatusResult{Installed: false}, nil
	}
	wallets, err := c.ListWallets(ctx)
	if err != nil {
		return &StatusResult{Installed: true}, err
	}
	return &StatusResult{
		Installed: true,
		Wallets:   len(wallets),
	}, nil
}

// StatusResult summarizes OWS state.
type StatusResult struct {
	Installed bool `json:"installed"`
	Wallets   int  `json:"wallets"`
}

// CreateWallet creates a new wallet with the given name.
func (c *Client) CreateWallet(ctx context.Context, name string) (*Wallet, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "wallet", "create", "--name", name)
	if err != nil {
		return nil, fmt.Errorf("creating wallet: %w", err)
	}
	var w Wallet
	if err := json.Unmarshal(out, &w); err != nil {
		// CLI may output non-JSON; try to extract name.
		w.Name = name
	}
	if w.Name == "" {
		w.Name = name
	}
	return &w, nil
}

// ListWallets returns all wallets managed by OWS.
func (c *Client) ListWallets(ctx context.Context) ([]Wallet, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "wallet", "list", "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("listing wallets: %w", err)
	}
	var wallets []Wallet
	if err := json.Unmarshal(out, &wallets); err != nil {
		return nil, fmt.Errorf("parsing wallet list: %w", err)
	}
	return wallets, nil
}

// GetWallet returns info for a single wallet by name.
func (c *Client) GetWallet(ctx context.Context, name string) (*Wallet, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "wallet", "info", "--name", name, "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("getting wallet %q: %w", name, err)
	}
	var w Wallet
	if err := json.Unmarshal(out, &w); err != nil {
		return nil, fmt.Errorf("parsing wallet info: %w", err)
	}
	return &w, nil
}

// Address returns the Solana address for the given wallet.
func (c *Client) Address(ctx context.Context, walletName string) (string, error) {
	if c == nil {
		return "", ErrNotInstalled
	}
	out, err := c.run(ctx, "wallet", "info", "--name", walletName, "--chain", ChainSolana, "--output", "json")
	if err != nil {
		return "", fmt.Errorf("getting address: %w", err)
	}
	var info struct {
		Address  string            `json:"address"`
		Chains   map[string]string `json:"chains"`
		Accounts []struct {
			Chain   string `json:"chain"`
			Address string `json:"address"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("parsing address: %w", err)
	}
	// Try top-level address first, then chain map, then accounts list.
	if info.Address != "" {
		return info.Address, nil
	}
	if addr, ok := info.Chains[ChainSolana]; ok {
		return addr, nil
	}
	for _, a := range info.Accounts {
		if a.Chain == ChainSolana {
			return a.Address, nil
		}
	}
	return "", fmt.Errorf("no Solana address found for wallet %q", walletName)
}

// Balance returns token balances for the wallet on Solana.
func (c *Client) Balance(ctx context.Context, walletName string) (*BalanceResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "fund", "balance", "--wallet", walletName, "--chain", ChainSolana, "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("checking balance: %w", err)
	}
	var result BalanceResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing balance: %w", err)
	}
	result.Chain = ChainSolana
	return &result, nil
}

// Pay makes an x402 payment to a URL using the given wallet.
func (c *Client) Pay(ctx context.Context, walletName, url string) (*PayResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "pay", "request", url, "--wallet", walletName, "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("paying %q: %w", url, err)
	}
	var result PayResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing payment result: %w", err)
	}
	return &result, nil
}

// DepositResult holds the result of a deposit request.
type DepositResult struct {
	Address string `json:"address"`
	Chain   string `json:"chain"`
	URL     string `json:"url,omitempty"`
	Status  string `json:"status"`
}

// Deposit initiates a fiat-to-crypto on-ramp for the given wallet on Solana.
func (c *Client) Deposit(ctx context.Context, walletName string) (*DepositResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "fund", "deposit", "--wallet", walletName, "--chain", ChainSolana, "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("deposit for wallet %q: %w", walletName, err)
	}
	var result DepositResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing deposit result: %w", err)
	}
	result.Chain = ChainSolana
	return &result, nil
}

// Transfer sends tokens from the wallet to a Solana address.
func (c *Client) Transfer(ctx context.Context, walletName, to, amount, token string) (*PayResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	args := []string{"fund", "send",
		"--wallet", walletName,
		"--chain", ChainSolana,
		"--to", to,
		"--amount", amount,
	}
	if token != "" {
		args = append(args, "--token", token)
	}
	args = append(args, "--output", "json")
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("transfer to %s: %w", to, err)
	}
	var result PayResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing transfer result: %w", err)
	}
	return &result, nil
}

// run executes the ows CLI with the given arguments.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}
