// Package wallet wraps the Open Wallet Standard (OWS) CLI for
// agent-to-agent payments on Solana. The ows binary is optional;
// all methods return ErrNotInstalled when it is missing.
package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	skyapps "github.com/sky10/sky10/pkg/apps"
)

const (
	// Solana CAIP-2 chain identifier used by OWS.
	ChainSolana = "solana"
	// Base CAIP-2 chain identifier used by OWS for EVM wallets.
	ChainBase = "eip155:8453"
)

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
	if status, err := skyapps.StatusFor(skyapps.AppOWS); err == nil && status.Managed && status.ActivePath != "" {
		return &Client{bin: status.ActivePath}
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
	appStatus, err := skyapps.StatusFor(skyapps.AppOWS)
	if err != nil {
		return nil, err
	}
	if !appStatus.Installed {
		return &StatusResult{
			Installed:   false,
			Managed:     false,
			ManagedPath: appStatus.ManagedPath,
		}, nil
	}
	if c == nil && appStatus.ActivePath != "" {
		c = &Client{bin: appStatus.ActivePath}
	}
	if c == nil {
		c = findClient()
	}
	if c == nil {
		return &StatusResult{
			Installed:   false,
			Managed:     appStatus.Managed,
			ManagedPath: appStatus.ManagedPath,
			Version:     appStatus.Version,
			BinPath:     appStatus.ActivePath,
		}, nil
	}

	wallets, err := c.ListWallets(ctx)
	if err != nil {
		if appStatus.Version == "" {
			return &StatusResult{
				Installed:   false,
				Managed:     appStatus.Managed,
				ManagedPath: appStatus.ManagedPath,
				BinPath:     appStatus.ActivePath,
			}, nil
		}
		return &StatusResult{
			Installed:   true,
			Managed:     appStatus.Managed,
			ManagedPath: appStatus.ManagedPath,
			Version:     appStatus.Version,
			BinPath:     appStatus.ActivePath,
		}, err
	}
	return &StatusResult{
		Installed:   true,
		Managed:     appStatus.Managed,
		ManagedPath: appStatus.ManagedPath,
		Wallets:     len(wallets),
		Version:     appStatus.Version,
		BinPath:     appStatus.ActivePath,
	}, nil
}

// StatusResult summarizes OWS state.
type StatusResult struct {
	Installed   bool   `json:"installed"`
	Managed     bool   `json:"managed"`
	ManagedPath string `json:"managed_path,omitempty"`
	Wallets     int    `json:"wallets"`
	Version     string `json:"version,omitempty"`
	BinPath     string `json:"bin_path,omitempty"`
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
	return c.AddressForChain(ctx, walletName, ChainSolana)
}

// AddressForChain returns the address for the given wallet and chain.
func (c *Client) AddressForChain(ctx context.Context, walletName, chain string) (string, error) {
	if c == nil {
		return "", ErrNotInstalled
	}
	// wallet list shows all addresses; find the line for the requested chain.
	out, err := c.run(ctx, "wallet", "list")
	if err != nil {
		return "", fmt.Errorf("getting address: %w", err)
	}
	return parseAddressForChain(string(out), walletName, chain)
}

// parseSolanaAddress extracts the Solana address from wallet list output.
// Looks for a line containing "solana:" with "→" pointing to the address.
func parseSolanaAddress(output, walletName string) (string, error) {
	return parseAddressForChain(output, walletName, ChainSolana)
}

// parseBaseAddress extracts the Base address from wallet list output.
// If OWS only lists a generic EVM address, it falls back to that value.
func parseBaseAddress(output, walletName string) (string, error) {
	return parseAddressForChain(output, walletName, ChainBase)
}

func parseAddressForChain(output, walletName, chain string) (string, error) {
	inWallet := false
	exactMatchers, fallbackMatchers, chainLabel := walletAddressMatchers(chain)
	var fallbackAddress string

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Name:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "Name:"))
			inWallet = name == walletName
		}
		if !inWallet {
			continue
		}
		if address, ok := walletAddressFromLine(trimmed, exactMatchers); ok {
			return address, nil
		}
		if fallbackAddress == "" {
			if address, ok := walletAddressFromLine(trimmed, fallbackMatchers); ok {
				fallbackAddress = address
			}
		}
	}
	if fallbackAddress != "" {
		return fallbackAddress, nil
	}
	return "", fmt.Errorf("no %s address found for wallet %q", chainLabel, walletName)
}

func walletAddressMatchers(chain string) (exact []string, fallback []string, label string) {
	normalized := strings.ToLower(strings.TrimSpace(chain))
	switch normalized {
	case "", ChainSolana:
		return []string{"solana:"}, nil, "Solana"
	case "base", ChainBase:
		// OWS may only list an EVM address once, usually under eip155:1.
		return []string{ChainBase}, []string{"eip155:"}, "Base"
	default:
		if strings.HasPrefix(normalized, "eip155:") {
			return []string{normalized}, []string{"eip155:"}, normalized
		}
		return []string{normalized}, nil, normalized
	}
}

func walletAddressFromLine(line string, matchers []string) (string, bool) {
	if len(matchers) == 0 || !strings.Contains(line, "→") {
		return "", false
	}
	parts := strings.SplitN(line, "→", 2)
	if len(parts) != 2 {
		return "", false
	}
	left := strings.ToLower(strings.TrimSpace(parts[0]))
	for _, matcher := range matchers {
		if strings.Contains(left, strings.ToLower(matcher)) {
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

// MaxTransferResult holds the maximum sendable amount and fee.
type MaxTransferResult struct {
	Max string `json:"max"`
	Fee string `json:"fee"`
}

// MaxTransfer returns the maximum SOL that can be sent, accounting for fees.
func (c *Client) MaxTransfer(ctx context.Context, walletName string) (*MaxTransferResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	addr, err := c.Address(ctx, walletName)
	if err != nil {
		return nil, err
	}
	max, fee, err := maxSOLTransfer(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &MaxTransferResult{
		Max: formatLamports(max),
		Fee: formatLamports(fee),
	}, nil
}

// Balance returns token balances for the wallet on Solana.
// Queries the Solana RPC directly for native SOL and SPL token balances.
func (c *Client) Balance(ctx context.Context, walletName string) (*BalanceResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	addr, err := c.Address(ctx, walletName)
	if err != nil {
		return nil, fmt.Errorf("getting address for balance: %w", err)
	}
	return solanaBalances(ctx, addr)
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

// Transfer sends SOL or SPL tokens from the wallet to a Solana address.
// It builds the appropriate transaction, then uses `ows sign send-tx`
// to sign and broadcast it.
func (c *Client) Transfer(ctx context.Context, walletName, to, amount, token string) (*PayResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}

	from, err := c.Address(ctx, walletName)
	if err != nil {
		return nil, fmt.Errorf("getting sender address: %w", err)
	}

	var txBytes []byte
	switch token {
	case "", "SOL":
		lamports, err := parseSOLAmount(amount)
		if err != nil {
			return nil, err
		}
		txBytes, err = buildSOLTransferTx(ctx, from, to, lamports)
		if err != nil {
			return nil, fmt.Errorf("building SOL transfer: %w", err)
		}
	case "USDC":
		units, err := parseTokenAmount(amount, usdcDecimals)
		if err != nil {
			return nil, err
		}
		txBytes, err = buildSPLTransferTx(ctx, from, to, usdcMint, units)
		if err != nil {
			return nil, fmt.Errorf("building USDC transfer: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported token: %s", token)
	}

	txHex := hex.EncodeToString(txBytes)
	out, err := c.run(ctx, "sign", "send-tx",
		"--chain", "solana",
		"--wallet", walletName,
		"--tx", txHex,
		"--json",
	)
	if err != nil {
		return nil, fmt.Errorf("transfer to %s: %w", to, err)
	}

	return &PayResult{
		Status: string(out),
		Amount: amount,
	}, nil
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
