package siwx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

// OWSSigner produces EIP-191 personal_sign signatures for SIWX
// envelopes by shelling out to the OWS CLI. The chain argument is
// fixed to "base" because Venice and the other SIWX services we
// support today all settle on Base; the actual signing is plain
// EIP-191 and chain-agnostic, but OWS requires a chain selector.
type OWSSigner struct {
	Client     *skywallet.Client
	WalletName string

	// SignMessage is the function that actually invokes OWS. Tests
	// substitute a fake; production wraps the wallet client.
	SignMessage func(ctx context.Context, walletName, chain, message string) (string, error)
}

// NewOWSSigner constructs a signer using the supplied wallet client.
// Returns nil if client is nil so callers can pattern-match the
// "OWS not installed" case without nil-checking after every Sign.
func NewOWSSigner(client *skywallet.Client, walletName string) *OWSSigner {
	if client == nil || walletName == "" {
		return nil
	}
	s := &OWSSigner{Client: client, WalletName: walletName}
	s.SignMessage = func(ctx context.Context, name, chain, message string) (string, error) {
		out, err := client.RunSignPersonalMessageJSON(ctx, name, chain, message)
		if err != nil {
			return "", err
		}
		var resp struct {
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			return "", fmt.Errorf("decode ows sign output: %w", err)
		}
		if resp.Signature == "" {
			return "", errors.New("ows sign output missing signature")
		}
		return resp.Signature, nil
	}
	return s
}

// SignPersonalMessage implements Signer.
func (s *OWSSigner) SignPersonalMessage(ctx context.Context, message string) (string, error) {
	if s == nil || s.SignMessage == nil {
		return "", errors.New("siwx: ows signer not configured")
	}
	return s.SignMessage(ctx, s.WalletName, "base", message)
}
