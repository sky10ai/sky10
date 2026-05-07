package mpp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

// Signer produces an MPP Authorization header value for a challenge.
type Signer interface {
	Sign(ctx context.Context, challenge Challenge) (string, error)
}

// OWSSigner signs MPP Solana charge transactions through OWS.
type OWSSigner struct {
	Client     *skywallet.Client
	WalletName string

	AddressForChain func(ctx context.Context, walletName, chain string) (string, error)
	SignTx          func(ctx context.Context, walletName, chain, unsignedTxHex string) (string, error)
	BuildTx         func(ctx context.Context, opts skywallet.SolanaPaymentTxOptions) (*skywallet.SolanaPaymentTx, error)
}

// NewOWSSigner constructs an OWS-backed MPP signer. Returns nil when OWS is
// unavailable or walletName is empty.
func NewOWSSigner(client *skywallet.Client, walletName string) *OWSSigner {
	if client == nil || strings.TrimSpace(walletName) == "" {
		return nil
	}
	s := &OWSSigner{Client: client, WalletName: walletName}
	s.AddressForChain = func(ctx context.Context, name, chain string) (string, error) {
		return client.AddressForChain(ctx, name, chain)
	}
	s.SignTx = func(ctx context.Context, name, chain, unsignedTxHex string) (string, error) {
		return owsSignTx(ctx, client, name, chain, unsignedTxHex)
	}
	s.BuildTx = func(ctx context.Context, opts skywallet.SolanaPaymentTxOptions) (*skywallet.SolanaPaymentTx, error) {
		return skywallet.BuildSolanaPaymentTx(ctx, opts)
	}
	return s
}

// Sign implements Signer for MPP method=solana, intent=charge.
func (s *OWSSigner) Sign(ctx context.Context, challenge Challenge) (string, error) {
	if s == nil || s.AddressForChain == nil || s.SignTx == nil || s.BuildTx == nil {
		return "", errors.New("mpp: signer not configured")
	}
	if !strings.EqualFold(challenge.Method, "solana") || !strings.EqualFold(challenge.Intent, "charge") {
		return "", fmt.Errorf("mpp: unsupported challenge %s/%s", challenge.Method, challenge.Intent)
	}
	request, details, err := challenge.DecodeChargeRequest()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(request.Recipient) == "" {
		return "", errors.New("mpp: charge request missing recipient")
	}
	amount, err := parseBaseUnitAmount(request.Amount)
	if err != nil {
		return "", fmt.Errorf("mpp: invalid amount: %w", err)
	}
	network := normalizeSolanaNetwork(details.Network)
	from, err := s.AddressForChain(ctx, s.WalletName, skywallet.ChainSolana)
	if err != nil {
		return "", fmt.Errorf("mpp: resolving wallet %q solana address: %w", s.WalletName, err)
	}
	if strings.TrimSpace(from) == "" {
		return "", fmt.Errorf("mpp: wallet %q has no solana address", s.WalletName)
	}

	mint, native := ResolveSolanaMint(request.Currency, network)
	decimals := uint8(6)
	if details.Decimals != nil {
		decimals = *details.Decimals
	}
	tokenProgram := details.TokenProgram
	if tokenProgram == "" && !native {
		tokenProgram = DefaultTokenProgramForCurrency(request.Currency, network)
	}
	feePayer := ""
	if details.FeePayer != nil && *details.FeePayer && strings.TrimSpace(details.FeePayerKey) != "" {
		feePayer = details.FeePayerKey
	}
	splits, err := parseSplits(details.Splits)
	if err != nil {
		return "", err
	}

	tx, err := s.BuildTx(ctx, skywallet.SolanaPaymentTxOptions{
		From:                          from,
		Recipient:                     request.Recipient,
		FeePayer:                      feePayer,
		Mint:                          mint,
		TokenProgram:                  tokenProgram,
		Amount:                        amount,
		Decimals:                      decimals,
		Memo:                          request.ExternalID,
		RecentBlockhash:               details.RecentBlockhash,
		ComputeUnitLimit:              200_000,
		ComputeUnitPriceMicrolamports: 1,
		Splits:                        splits,
	})
	if err != nil {
		return "", fmt.Errorf("mpp: build solana transaction: %w", err)
	}
	signedHex, err := s.SignTx(ctx, s.WalletName, skywallet.ChainSolana, tx.FullUnsignedHex)
	if err != nil {
		return "", fmt.Errorf("mpp: sign solana transaction: %w", err)
	}
	signedBytes, err := completeSignedTransaction(tx, signedHex)
	if err != nil {
		return "", err
	}

	credential := Credential{
		Challenge: challenge.echo(),
		Source:    fmt.Sprintf("did:pkh:solana:%s:%s", network, from),
		Payload: CredentialPayloadTransaction{
			Type:        "transaction",
			Transaction: base64.StdEncoding.EncodeToString(signedBytes),
		},
	}
	return FormatAuthorization(credential)
}

func completeSignedTransaction(tx *skywallet.SolanaPaymentTx, signedHex string) ([]byte, error) {
	raw, err := hex.DecodeString(stripHexPrefix(strings.TrimSpace(signedHex)))
	if err != nil {
		return nil, fmt.Errorf("mpp: decode solana signature: %w", err)
	}
	if len(raw) == 64 {
		signed, err := skywallet.InsertSolanaSignature(tx.FullUnsignedHex, tx.SignerSlot, raw)
		if err != nil {
			return nil, fmt.Errorf("mpp: insert solana signature: %w", err)
		}
		return signed, nil
	}
	if len(raw) > 64 {
		return raw, nil
	}
	return nil, fmt.Errorf("mpp: expected 64-byte signature or signed transaction, got %d bytes", len(raw))
}

func parseBaseUnitAmount(value string) (uint64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("amount required")
	}
	amount, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, err
	}
	if amount == 0 {
		return 0, errors.New("amount must be positive")
	}
	return amount, nil
}

func parseSplits(splits []Split) ([]skywallet.SolanaPaymentSplit, error) {
	out := make([]skywallet.SolanaPaymentSplit, 0, len(splits))
	for _, split := range splits {
		amount, err := parseBaseUnitAmount(split.Amount)
		if err != nil {
			return nil, fmt.Errorf("mpp: invalid split amount: %w", err)
		}
		createATA := false
		if split.ATACreationRequired != nil {
			createATA = *split.ATACreationRequired
		}
		out = append(out, skywallet.SolanaPaymentSplit{
			Recipient: split.Recipient,
			Amount:    amount,
			Memo:      split.Memo,
			CreateATA: createATA,
		})
	}
	return out, nil
}

func owsSignTx(ctx context.Context, client *skywallet.Client, walletName, chain, txHex string) (string, error) {
	out, err := client.RunSignTxJSON(ctx, walletName, chain, txHex)
	if err != nil {
		return "", err
	}
	var resp struct {
		SignedTx    string `json:"signed_tx"`
		Tx          string `json:"tx"`
		Transaction string `json:"transaction"`
		Signature   string `json:"signature"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("decode ows sign tx output: %w", err)
	}
	for _, candidate := range []string{resp.SignedTx, resp.Tx, resp.Transaction, resp.Signature} {
		if strings.TrimSpace(candidate) != "" {
			return candidate, nil
		}
	}
	return "", errors.New("ows sign tx output missing signed bytes")
}

func stripHexPrefix(value string) string {
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return value[2:]
	}
	return value
}

func normalizeSolanaNetwork(network string) string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "", "mainnet", "mainnet-beta", "solana", "solana-mainnet", "solana:5eykt4usfv8p8njdtrepy1vzqkqzkvdp":
		return "mainnet-beta"
	case "devnet", "solana-devnet":
		return "devnet"
	case "testnet", "solana-testnet":
		return "testnet"
	case "localnet", "sandbox":
		return "localnet"
	default:
		return strings.TrimSpace(network)
	}
}
