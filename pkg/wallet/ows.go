// Package wallet wraps the Open Wallet Standard (OWS) CLI for
// agent-to-agent payments on supported chains like Solana and Base.
// The ows binary is optional; all methods return ErrNotInstalled when
// it is missing.
package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
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

func owsChainArg(chain string) string {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "", ChainSolana:
		return ChainSolana
	case "base", ChainBase:
		return "base"
	default:
		return strings.TrimSpace(chain)
	}
}

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

type signResult struct {
	Signature  string `json:"signature"`
	RecoveryID *int   `json:"recovery_id,omitempty"`
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

// AddressForChain returns the account address for the given wallet and chain.
// For EVM chains, it resolves the wallet's shared eip155 account.
func (c *Client) AddressForChain(ctx context.Context, walletName, chain string) (string, error) {
	if c == nil {
		return "", ErrNotInstalled
	}
	out, err := c.run(ctx, "wallet", "list")
	if err != nil {
		return "", fmt.Errorf("getting address: %w", err)
	}
	return parseWalletAddress(string(out), walletName, chain)
}

// parseSolanaAddress extracts the Solana address from wallet list output.
// Looks for a line containing "solana:" with "→" pointing to the address.
func parseSolanaAddress(output, walletName string) (string, error) {
	return parseWalletAddress(output, walletName, ChainSolana)
}

// parseWalletAddress extracts an account address for the requested chain from
// `ows wallet list` output. It prefers exact chain ID matches, then falls back
// to the chain namespace so EVM wallets can reuse the same account on Base.
func parseWalletAddress(output, walletName, chain string) (string, error) {
	requestedChain := strings.TrimSpace(strings.ToLower(chain))
	requestedNS := chainNamespace(requestedChain)
	inWallet := false
	var namespaceAddress string

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Name:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "Name:"))
			if inWallet && name != walletName {
				break
			}
			inWallet = name == walletName
			namespaceAddress = ""
			continue
		}
		if !inWallet || !strings.Contains(trimmed, "→") {
			continue
		}

		accountPart, addressPart, ok := strings.Cut(trimmed, "→")
		if !ok {
			continue
		}
		accountID := strings.TrimSpace(accountPart)
		if idx := strings.Index(accountID, " "); idx >= 0 {
			accountID = strings.TrimSpace(accountID[:idx])
		}
		address := strings.TrimSpace(addressPart)
		if address == "" {
			continue
		}

		if strings.EqualFold(accountID, requestedChain) {
			return address, nil
		}
		if chainNamespace(strings.ToLower(accountID)) == requestedNS && namespaceAddress == "" {
			namespaceAddress = address
		}
	}

	if namespaceAddress != "" {
		return namespaceAddress, nil
	}
	return "", fmt.Errorf("no %s address found for wallet %q", chain, walletName)
}

func chainNamespace(chain string) string {
	switch {
	case strings.Contains(chain, ":"):
		ns, _, _ := strings.Cut(chain, ":")
		return strings.ToLower(ns)
	case chain == "", chain == ChainSolana:
		return ChainSolana
	case chain == "base", chain == "base-sepolia", chain == "ethereum", chain == "arbitrum", chain == "optimism", chain == "polygon", chain == "evm":
		return "eip155"
	default:
		for _, r := range chain {
			if r < '0' || r > '9' {
				return strings.ToLower(chain)
			}
		}
		return "eip155"
	}
}

// MaxTransferResult holds the maximum sendable amount and fee.
type MaxTransferResult struct {
	Max string `json:"max"`
	Fee string `json:"fee"`
}

// MaxTransfer returns the maximum native amount that can be sent on Solana,
// accounting for fees.
func (c *Client) MaxTransfer(ctx context.Context, walletName string) (*MaxTransferResult, error) {
	return c.MaxTransferForChain(ctx, walletName, ChainSolana)
}

// MaxTransferForChain returns the maximum native amount that can be sent on the requested chain,
// accounting for estimated network fees.
func (c *Client) MaxTransferForChain(ctx context.Context, walletName, chain string) (*MaxTransferResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	addr, err := c.AddressForChain(ctx, walletName, chain)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "", ChainSolana:
		max, fee, err := maxSOLTransfer(ctx, addr)
		if err != nil {
			return nil, err
		}
		return &MaxTransferResult{
			Max: formatLamports(max),
			Fee: formatLamports(fee),
		}, nil
	case "base", ChainBase:
		return baseMaxTransfer(ctx, addr)
	default:
		return nil, fmt.Errorf("unsupported max transfer chain: %s", chain)
	}
}

// Balance returns token balances for the wallet on Solana.
func (c *Client) Balance(ctx context.Context, walletName string) (*BalanceResult, error) {
	return c.BalanceForChain(ctx, walletName, ChainSolana)
}

// BalanceForChain returns token balances for the wallet on the requested chain.
// Solana and Base use direct chain RPCs so native assets show up alongside USDC.
func (c *Client) BalanceForChain(ctx context.Context, walletName, chain string) (*BalanceResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	addr, err := c.AddressForChain(ctx, walletName, chain)
	if err != nil {
		return nil, fmt.Errorf("getting address for balance: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "", ChainSolana:
		return solanaBalances(ctx, addr)
	case "base", ChainBase:
		return baseBalances(ctx, addr)
	default:
		return nil, fmt.Errorf("unsupported balance chain: %s", chain)
	}
}

// SignMessage signs an EIP-191 or chain-native message using the given wallet.
func (c *Client) SignMessage(ctx context.Context, walletName, chain, message string) (string, error) {
	if c == nil {
		return "", ErrNotInstalled
	}
	out, err := c.run(ctx,
		"sign", "message",
		"--chain", chain,
		"--wallet", walletName,
		"--message", message,
		"--json",
	)
	if err != nil {
		return "", fmt.Errorf("signing message on %q: %w", chain, err)
	}
	return parseSignResult(out)
}

// SignTypedData signs EIP-712 typed data using the given wallet.
func (c *Client) SignTypedData(ctx context.Context, walletName, chain, typedData string) (string, error) {
	if c == nil {
		return "", ErrNotInstalled
	}
	out, err := c.run(ctx,
		"sign", "message",
		"--chain", chain,
		"--wallet", walletName,
		"--message", "",
		"--typed-data", typedData,
		"--json",
	)
	if err != nil {
		return "", fmt.Errorf("signing typed data on %q: %w", chain, err)
	}
	return parseSignResult(out)
}

func parseSignResult(output []byte) (string, error) {
	var result signResult
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("parsing sign result: %w", err)
	}
	if strings.TrimSpace(result.Signature) == "" {
		return "", fmt.Errorf("sign result missing signature")
	}
	return strings.TrimSpace(result.Signature), nil
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
	return c.DepositForChain(ctx, walletName, ChainSolana)
}

// DepositForChain initiates a fiat-to-crypto on-ramp for the given wallet on the requested chain.
func (c *Client) DepositForChain(ctx context.Context, walletName, chain string) (*DepositResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}
	normalizedChain := strings.TrimSpace(chain)
	if normalizedChain == "" {
		normalizedChain = ChainSolana
	}
	out, err := c.run(ctx, "fund", "deposit", "--wallet", walletName, "--chain", owsChainArg(normalizedChain))
	if err != nil {
		return nil, fmt.Errorf("deposit for wallet %q on %s: %w", walletName, normalizedChain, err)
	}
	result := &DepositResult{Chain: normalizedChain, Status: string(out)}
	// Look for a URL in the output.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "https://"); idx >= 0 {
			result.URL = line[idx:]
			break
		}
		if idx := strings.Index(line, "http://"); idx >= 0 {
			result.URL = line[idx:]
			break
		}
	}
	return result, nil
}

// Transfer sends a supported token on Solana.
func (c *Client) Transfer(ctx context.Context, walletName, to, amount, token string) (*PayResult, error) {
	return c.TransferForChain(ctx, walletName, ChainSolana, to, amount, token)
}

// TransferForChain sends a supported token on the requested chain.
func (c *Client) TransferForChain(ctx context.Context, walletName, chain, to, amount, token string) (*PayResult, error) {
	if c == nil {
		return nil, ErrNotInstalled
	}

	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "", ChainSolana:
		return c.transferSolana(ctx, walletName, to, amount, token)
	case "base", ChainBase:
		return c.transferBase(ctx, walletName, to, amount, token)
	default:
		return nil, fmt.Errorf("unsupported transfer chain: %s", chain)
	}
}

func (c *Client) transferSolana(ctx context.Context, walletName, to, amount, token string) (*PayResult, error) {
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

	result := &PayResult{
		Status: string(out),
		Amount: amount,
	}
	parseBroadcastResult(out, result)
	return result, nil
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
