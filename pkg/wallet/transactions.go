package wallet

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sky10/sky10/pkg/config"
)

const (
	defaultTransactionLimit = 32
	maxTransactionLimit     = 256
	transactionLogName      = "transactions.jsonl"
)

var transactionLogMu sync.Mutex

// TransactionEntry is a local sky10 wallet transaction row. It records actions
// initiated through sky10 and is not intended to replace authoritative chain history.
type TransactionEntry struct {
	Amount               string `json:"amount,omitempty"`
	Asset                string `json:"asset,omitempty"`
	Chain                string `json:"chain"`
	Counterparty         string `json:"counterparty"`
	CounterpartySubtitle string `json:"counterparty_subtitle,omitempty"`
	CreatedAt            string `json:"created_at"`
	ExternalURL          string `json:"external_url,omitempty"`
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	Memo                 string `json:"memo"`
	Status               string `json:"status"`
	TxHash               string `json:"tx_hash,omitempty"`
	TxURL                string `json:"tx_url,omitempty"`
}

func appendTransaction(walletName string, entry TransactionEntry) error {
	path, err := transactionLogPath(walletName)
	if err != nil {
		return err
	}
	if err := validateTransactionEntry(entry); err != nil {
		return err
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal transaction: %w", err)
	}
	line = append(line, '\n')

	transactionLogMu.Lock()
	defer transactionLogMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create wallet transaction directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open wallet transaction log: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("write wallet transaction log: %w", err)
	}
	return nil
}

func listTransactions(walletName string, limit int) ([]TransactionEntry, error) {
	path, err := transactionLogPath(walletName)
	if err != nil {
		return nil, err
	}
	limit = normalizeTransactionLimit(limit)

	transactionLogMu.Lock()
	defer transactionLogMu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open wallet transaction log: %w", err)
	}
	defer file.Close()

	entries := make([]TransactionEntry, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry TransactionEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if err := validateTransactionEntry(entry); err != nil {
			continue
		}

		entries = append(entries, entry)
		if len(entries) > limit {
			entries = entries[len(entries)-limit:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read wallet transaction log: %w", err)
	}

	slices.Reverse(entries)
	return entries, nil
}

func transactionLogPath(walletName string) (string, error) {
	if err := validateWalletNameForPath(walletName); err != nil {
		return "", err
	}
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, "wallet", "wallets", strings.TrimSpace(walletName), transactionLogName), nil
}

func validateWalletNameForPath(walletName string) error {
	trimmed := strings.TrimSpace(walletName)
	if trimmed == "" {
		return fmt.Errorf("wallet is required")
	}
	if walletName != trimmed {
		return fmt.Errorf("wallet name is not safe for local storage")
	}
	walletName = trimmed
	if walletName == "." || walletName == ".." {
		return fmt.Errorf("wallet name is not safe for local storage")
	}
	if strings.HasSuffix(walletName, ".") {
		return fmt.Errorf("wallet name is not safe for local storage")
	}
	if filepath.IsAbs(walletName) || strings.ContainsAny(walletName, `/\<>:"|?*`) {
		return fmt.Errorf("wallet name is not safe for local storage")
	}
	if isReservedWindowsPathName(walletName) {
		return fmt.Errorf("wallet name is not safe for local storage")
	}
	return nil
}

func isReservedWindowsPathName(name string) bool {
	normalized := strings.TrimRight(strings.ToUpper(name), ". ")
	deviceName, _, _ := strings.Cut(normalized, ".")
	switch deviceName {
	case "CON", "PRN", "AUX", "NUL":
		return true
	case "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9":
		return true
	case "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func validateTransactionEntry(entry TransactionEntry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return fmt.Errorf("transaction id is required")
	}
	switch strings.TrimSpace(entry.Chain) {
	case "base", "solana":
	default:
		return fmt.Errorf("transaction chain is required")
	}
	switch strings.TrimSpace(entry.Kind) {
	case "fund", "send":
	default:
		return fmt.Errorf("transaction kind is required")
	}
	if strings.TrimSpace(entry.Counterparty) == "" {
		return fmt.Errorf("transaction counterparty is required")
	}
	if strings.TrimSpace(entry.CreatedAt) == "" {
		return fmt.Errorf("transaction created_at is required")
	}
	if strings.TrimSpace(entry.Memo) == "" {
		return fmt.Errorf("transaction memo is required")
	}
	if strings.TrimSpace(entry.Status) == "" {
		return fmt.Errorf("transaction status is required")
	}
	return nil
}

func normalizeTransactionLimit(limit int) int {
	if limit <= 0 {
		return defaultTransactionLimit
	}
	if limit > maxTransactionLimit {
		return maxTransactionLimit
	}
	return limit
}
