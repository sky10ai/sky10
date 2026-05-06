package siwx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Hint is the parsed sign-in-with-x extension from a 402 challenge.
// Some servers describe the SIWX requirement on every 402 (Venice's
// /balance and /transactions); others assume the client knows.
// pkg/x402.ServiceManifest's SIWXDomain field is the manifest-side
// configuration that lets a Builder run without depending on the
// extension being present.
type Hint struct {
	Domain          string
	URI             string
	Statement       string
	Version         string
	SupportedChains []ChainOption
}

// ChainOption is one entry in the challenge's supportedChains list.
// Today every observed Venice/Run402 deployment offers
// {chainId: "eip155:8453", type: "eip191"}; the parser keeps the
// raw shape so future signing-types (eip1271 contract sigs, etc.)
// can plug in.
type ChainOption struct {
	ChainID string
	Type    string
}

// Detect extracts the sign-in-with-x extension from the raw
// extensions map of a 402 challenge. Returns ok=false when the
// extension is absent. The hint's URI is left empty when the
// challenge omitted it; the Builder fills in the actual request URL.
func Detect(extensions map[string]json.RawMessage) (*Hint, bool) {
	raw, ok := extensions["sign-in-with-x"]
	if !ok || len(raw) == 0 {
		return nil, false
	}
	var ext struct {
		Info struct {
			Domain    string `json:"domain"`
			URI       string `json:"uri"`
			Statement string `json:"statement"`
			Version   string `json:"version"`
		} `json:"info"`
		SupportedChains []ChainOption `json:"supportedChains"`
	}
	if err := json.Unmarshal(raw, &ext); err != nil {
		return nil, false
	}
	if strings.TrimSpace(ext.Info.Domain) == "" {
		return nil, false
	}
	return &Hint{
		Domain:          ext.Info.Domain,
		URI:             ext.Info.URI,
		Statement:       ext.Info.Statement,
		Version:         ext.Info.Version,
		SupportedChains: ext.SupportedChains,
	}, true
}

// Signer signs an EIP-191 personal_sign message and returns the
// signature hex (with or without 0x prefix; the encoder normalizes).
type Signer interface {
	SignPersonalMessage(ctx context.Context, message string) (signature string, err error)
}

// Builder constructs X-Sign-In-With-X header values for a specific
// wallet against a specific service. One Builder serves an entire
// service: it owns the domain (used in the SIWE message), the wallet
// name to delegate signing to, and the underlying Signer. Each call
// to Header() produces a fresh nonce + timestamp.
type Builder struct {
	// Address is the wallet's checksummed Ethereum-style address.
	// Required.
	Address string
	// Domain matches the service's expected SIWE domain (e.g.
	// "api.venice.ai"). Required.
	Domain string
	// ChainID is the EVM chain id the SIWE message names. Required.
	// Defaults to 8453 (Base) when zero.
	ChainID int64
	// Statement is the human-readable banner that appears in the
	// SIWE message. Optional; when empty we use a sensible default.
	Statement string
	// SignInExpiry sets how far in the future the message's
	// `expirationTime` is set. Default 5 minutes — matches Venice's
	// SDK constant SIGN_IN_EXPIRY_MS.
	SignInExpiry time.Duration
	// Signer is the wallet shim that produces the EIP-191 signature.
	// Required.
	Signer Signer
	// Now is the clock used to stamp `issuedAt` and derive
	// `expirationTime`. Tests override; production uses time.Now.
	Now func() time.Time
	// Nonce overrides the random nonce generator. Tests set this to
	// produce deterministic golden output.
	Nonce func() string
}

// Header constructs the X-Sign-In-With-X header value for one
// outgoing request to the given resource URL. The URL is normalized
// per Venice's SDK behaviour: relative inputs are resolved against
// the configured Domain.
func (b *Builder) Header(ctx context.Context, resourceURL string) (string, error) {
	if b == nil {
		return "", errors.New("siwx: nil builder")
	}
	if strings.TrimSpace(b.Address) == "" {
		return "", errors.New("siwx: builder address required")
	}
	if strings.TrimSpace(b.Domain) == "" {
		return "", errors.New("siwx: builder domain required")
	}
	if b.Signer == nil {
		return "", errors.New("siwx: builder signer required")
	}
	chainID := b.ChainID
	if chainID == 0 {
		chainID = 8453
	}
	statement := b.Statement
	if statement == "" {
		statement = "Sign in to " + b.Domain
	}
	expiry := b.SignInExpiry
	if expiry <= 0 {
		expiry = 5 * time.Minute
	}
	nowFn := b.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	nonceFn := b.Nonce
	if nonceFn == nil {
		nonceFn = randomNonce16
	}
	resolvedURL, err := normalizeResourceURL(resourceURL, b.Domain)
	if err != nil {
		return "", fmt.Errorf("siwx: resolve resource url: %w", err)
	}
	message := buildSiweMessage(siweFields{
		Domain:         b.Domain,
		Address:        b.Address,
		Statement:      statement,
		URI:            resolvedURL,
		Version:        "1",
		ChainID:        chainID,
		Nonce:          nonceFn(),
		IssuedAt:       now.Format("2006-01-02T15:04:05.000Z"),
		ExpirationTime: now.Add(expiry).Format("2006-01-02T15:04:05.000Z"),
	})
	signature, err := b.Signer.SignPersonalMessage(ctx, message)
	if err != nil {
		return "", fmt.Errorf("siwx: sign message: %w", err)
	}
	envelope := map[string]any{
		"address":   b.Address,
		"message":   message,
		"signature": normalizeSignature(signature),
		"timestamp": now.UnixMilli(),
		"chainId":   chainID,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("siwx: encode envelope: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// HeaderName is the request header that carries the encoded SIWX
// envelope. Same name across every service that implements SIWX
// today (Venice, Stablephone, Run402).
const HeaderName = "X-Sign-In-With-X"

// siweFields is the structured form of an EIP-4361 message before
// it gets serialized to the canonical text shape.
type siweFields struct {
	Domain         string
	Address        string
	Statement      string
	URI            string
	Version        string
	ChainID        int64
	Nonce          string
	IssuedAt       string
	ExpirationTime string
}

// buildSiweMessage formats a SIWE (EIP-4361) message string. The
// shape is what Ethereum-ecosystem SDKs (siwe, viem) produce by
// default — verified byte-for-byte against the venice-x402-client
// SDK's output.
func buildSiweMessage(f siweFields) string {
	var b strings.Builder
	b.WriteString(f.Domain)
	b.WriteString(" wants you to sign in with your Ethereum account:\n")
	b.WriteString(f.Address)
	b.WriteString("\n\n")
	b.WriteString(f.Statement)
	b.WriteString("\n\n")
	b.WriteString("URI: ")
	b.WriteString(f.URI)
	b.WriteString("\n")
	b.WriteString("Version: ")
	b.WriteString(f.Version)
	b.WriteString("\n")
	b.WriteString("Chain ID: ")
	b.WriteString(fmt.Sprintf("%d", f.ChainID))
	b.WriteString("\n")
	b.WriteString("Nonce: ")
	b.WriteString(f.Nonce)
	b.WriteString("\n")
	b.WriteString("Issued At: ")
	b.WriteString(f.IssuedAt)
	b.WriteString("\n")
	b.WriteString("Expiration Time: ")
	b.WriteString(f.ExpirationTime)
	return b.String()
}

// normalizeResourceURL resolves a request input against the service's
// domain. Absolute URLs pass through unchanged; relative paths are
// joined onto an https://<domain> origin.
func normalizeResourceURL(input, domain string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "https://" + domain, nil
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		// Validate parses cleanly.
		if _, err := url.Parse(trimmed); err != nil {
			return "", err
		}
		return trimmed, nil
	}
	base := &url.URL{Scheme: "https", Host: domain}
	rel, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(rel).String(), nil
}

// randomNonce16 returns a 16-character lowercase hex nonce. Matches
// what the venice-x402-client SDK produces (UUID with dashes
// stripped, sliced to 16 chars).
func randomNonce16() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// On systems where crypto/rand fails the wallet can't sign
		// anyway; leave a deterministic placeholder so the failure
		// surfaces at the signing step.
		return "00000000deadbeef"
	}
	return hex.EncodeToString(buf[:])
}

// normalizeSignature ensures the signature is 0x-prefixed for the
// SIWX envelope. OWS already emits prefixed signatures, but we
// defensively normalize so a future OWS change cannot silently
// produce a malformed envelope.
func normalizeSignature(sig string) string {
	sig = strings.TrimSpace(sig)
	if strings.HasPrefix(sig, "0x") || strings.HasPrefix(sig, "0X") {
		return sig
	}
	return "0x" + sig
}
