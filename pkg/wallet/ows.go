// Package wallet wraps the Open Wallet Standard (OWS) CLI for
// agent-to-agent payments on Solana. The ows binary is optional;
// all methods return ErrNotInstalled when it is missing.
package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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

// NewClient returns a Client if the ows binary is found.
// Checks ~/.sky10/bin/ows first, then PATH. Returns nil if not found.
func NewClient() *Client {
	return findClient()
}

// findClient locates the ows binary, preferring the managed install.
func findClient() *Client {
	// Check managed install location first.
	if p, err := BinPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			return &Client{bin: p}
		}
	}
	// Fall back to PATH.
	if bin, err := exec.LookPath("ows"); err == nil {
		return &Client{bin: bin}
	}
	return nil
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
		return &StatusResult{Installed: true, Version: InstalledVersion(), BinPath: c.bin}, err
	}
	return &StatusResult{
		Installed: true,
		Wallets:   len(wallets),
		Version:   InstalledVersion(),
		BinPath:   c.bin,
	}, nil
}

// StatusResult summarizes OWS state.
type StatusResult struct {
	Installed bool   `json:"installed"`
	Wallets   int    `json:"wallets"`
	Version   string `json:"version,omitempty"`
	BinPath   string `json:"bin_path,omitempty"`
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
	// Parse: "Wallet created: <id>"
	w := Wallet{Name: name}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Wallet created:") {
			w.ID = strings.TrimSpace(strings.TrimPrefix(line, "Wallet created:"))
		}
	}
	return &w, nil
}

// ListWallets returns all wallets managed by OWS.
func (c *Client) ListWallets(ctx context.Context) ([]Wallet, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "wallet", "list")
	if err != nil {
		return nil, fmt.Errorf("listing wallets: %w", err)
	}
	return parseWalletList(string(out)), nil
}

// parseWalletList parses the text output of `ows wallet list`.
// Format:
//
//	ID:      <uuid>
//	Name:    <name>
//	...
func parseWalletList(output string) []Wallet {
	if strings.Contains(output, "No wallets found") {
		return nil
	}
	var wallets []Wallet
	var current Wallet
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID:") {
			if current.ID != "" {
				wallets = append(wallets, current)
			}
			current = Wallet{ID: strings.TrimSpace(strings.TrimPrefix(line, "ID:"))}
		} else if strings.HasPrefix(line, "Name:") {
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		}
	}
	if current.ID != "" {
		wallets = append(wallets, current)
	}
	return wallets
}

// Address returns the Solana address for the given wallet.
func (c *Client) Address(ctx context.Context, walletName string) (string, error) {
	if c == nil {
		return "", ErrNotInstalled
	}
	// wallet list shows all addresses; find the solana line for this wallet.
	out, err := c.run(ctx, "wallet", "list")
	if err != nil {
		return "", fmt.Errorf("getting address: %w", err)
	}
	return parseSolanaAddress(string(out), walletName)
}

// parseSolanaAddress extracts the Solana address from wallet list output.
// Looks for a line containing "solana:" with "→" pointing to the address.
func parseSolanaAddress(output, walletName string) (string, error) {
	inWallet := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Name:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "Name:"))
			inWallet = name == walletName
		}
		if inWallet && strings.Contains(trimmed, "solana:") && strings.Contains(trimmed, "→") {
			parts := strings.SplitN(trimmed, "→", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}
	return "", fmt.Errorf("no Solana address found for wallet %q", walletName)
}

// Balance returns token balances for the wallet on Solana.
func (c *Client) Balance(ctx context.Context, walletName string) (*BalanceResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "fund", "balance", "--wallet", walletName, "--chain", ChainSolana)
	if err != nil {
		return nil, fmt.Errorf("checking balance: %w", err)
	}
	result := &BalanceResult{Chain: ChainSolana}
	text := string(out)
	// "No tokens found for <addr> on solana"
	if strings.Contains(text, "No tokens found for") {
		parts := strings.Fields(text)
		for i, p := range parts {
			if p == "for" && i+1 < len(parts) {
				result.Address = parts[i+1]
				break
			}
		}
		return result, nil
	}
	// TODO: parse token balance table when tokens exist.
	return result, nil
}

// Pay makes an x402 payment to a URL using the given wallet.
func (c *Client) Pay(ctx context.Context, walletName, url string) (*PayResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	out, err := c.run(ctx, "pay", "request", url, "--wallet", walletName)
	if err != nil {
		return nil, fmt.Errorf("paying %q: %w", url, err)
	}
	return &PayResult{Status: string(out)}, nil
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
	out, err := c.run(ctx, "fund", "deposit", "--wallet", walletName, "--chain", ChainSolana)
	if err != nil {
		return nil, fmt.Errorf("deposit for wallet %q: %w", walletName, err)
	}
	result := &DepositResult{Chain: ChainSolana, Status: string(out)}
	// Look for a URL in the output.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			result.URL = line
			break
		}
	}
	return result, nil
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
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("transfer to %s: %w", to, err)
	}
	return &PayResult{Status: string(out)}, nil
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
