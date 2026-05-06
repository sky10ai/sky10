package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Network identifies the settlement network for an x402 payment.
type Network string

const (
	NetworkBase   Network = "base"
	NetworkSolana Network = "solana"
)

// Currency identifies the currency a service quotes payment in.
// Today only USDC is in use across the agentic.market catalog;
// future currencies plug in here.
type Currency string

const (
	CurrencyUSDC Currency = "USDC"
)

// SchemeExact is the canonical x402 scheme name for one-shot
// authorization-based payments. All currently-supported services use
// this scheme; future schemes (e.g. streaming, batched) plug in here.
const SchemeExact = "exact"

// X402 protocol versions this implementation knows about. v1 was the
// original spec where the 402 challenge lived in the response body
// under `accepts` and the amount field was named `maxAmountRequired`.
// v2 (the current revision live on agentic.market) moves the
// challenge into a `Payment-Required` response header carrying
// base64-JSON, and renames the amount field to `amount`. We accept
// either shape on the read path and echo the challenge's version on
// the X-PAYMENT response so a v1 server keeps seeing v1 and a v2
// server keeps seeing v2.
const (
	X402ProtocolV1 = 1
	X402ProtocolV2 = 2
	// X402ProtocolVersion is the version we stamp on outgoing
	// payloads when no challenge has supplied one to echo. Defaults
	// to v2 because that is what current services emit.
	X402ProtocolVersion = X402ProtocolV2
)

// PaymentRequirements is one entry in a 402 challenge's `accepts`
// array. The server emits one of these per acceptable payment scheme/
// network combination; the client picks one and signs against it.
//
// Fields follow the x402 spec at https://x402.org. Both v1 (`maxAmountRequired`)
// and v2 (`amount`) field names are accepted on the read path; callers
// should use MaxAmount() rather than the raw fields. Unknown fields
// are preserved on the wire envelope so we can reproduce the exact
// server directive when computing the EIP-712 message.
type PaymentRequirements struct {
	Scheme  string `json:"scheme"`
	Network string `json:"network"`
	// Amount is the v2 field name for the payment amount in the
	// asset's base unit (micro-USDC for USDC). Read with MaxAmount()
	// rather than directly so v1 servers' `maxAmountRequired` is
	// also honored.
	Amount string `json:"amount,omitempty"`
	// MaxAmountRequired is the v1 field name for the same value.
	// Kept on the struct so v1 challenges round-trip cleanly.
	MaxAmountRequired string                 `json:"maxAmountRequired,omitempty"`
	Resource          string                 `json:"resource,omitempty"`
	Description       string                 `json:"description,omitempty"`
	MimeType          string                 `json:"mimeType,omitempty"`
	OutputSchema      json.RawMessage        `json:"outputSchema,omitempty"`
	PayTo             string                 `json:"payTo"`
	MaxTimeoutSeconds int64                  `json:"maxTimeoutSeconds,omitempty"`
	Asset             string                 `json:"asset"`
	Extra             map[string]interface{} `json:"extra,omitempty"`
}

// MaxAmount returns the payment amount as a base-unit decimal string,
// picking the v2 `amount` field when present and falling back to the
// v1 `maxAmountRequired` field otherwise. Empty string if neither
// was supplied.
func (r PaymentRequirements) MaxAmount() string {
	if v := strings.TrimSpace(r.Amount); v != "" {
		return v
	}
	return strings.TrimSpace(r.MaxAmountRequired)
}

// PaymentChallenge is the parsed body of a 402 response. Servers
// emit it when an unauthorized request hits a paid endpoint.
type PaymentChallenge struct {
	X402Version int                   `json:"x402Version"`
	Accepts     []PaymentRequirements `json:"accepts"`
	Error       string                `json:"error,omitempty"`
}

// SelectRequirements returns the first PaymentRequirements compatible
// with our supported schemes and networks. ErrNoCompatibleRequirements
// is returned when none of the offered options can be honored.
func (c PaymentChallenge) SelectRequirements() (PaymentRequirements, error) {
	for _, req := range c.Accepts {
		if !isSupportedScheme(req.Scheme) {
			continue
		}
		if !isSupportedNetwork(req.Network) {
			continue
		}
		return req, nil
	}
	return PaymentRequirements{}, ErrNoCompatibleRequirements
}

// ErrNoCompatibleRequirements indicates the server's 402 challenge
// listed no scheme+network combination this client supports.
var ErrNoCompatibleRequirements = errors.New("x402: no compatible payment requirements offered")

func isSupportedScheme(scheme string) bool {
	return strings.EqualFold(scheme, SchemeExact)
}

func isSupportedNetwork(network string) bool {
	_, ok := canonicalizeNetwork(network)
	return ok
}

// canonicalizeNetwork maps the wire-level `network` field — which
// may be a bare name ("base", "solana") or a CAIP-2 / CAIP-2-prefixed
// identifier ("eip155:8453", "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp")
// — to the package's canonical Network value. The bool is false if
// the network is not one we currently support.
func canonicalizeNetwork(network string) (Network, bool) {
	raw := strings.ToLower(strings.TrimSpace(network))
	if raw == "" {
		return "", false
	}
	if i := strings.IndexByte(raw, ':'); i > 0 {
		raw = raw[:i]
	}
	switch raw {
	case string(NetworkBase), "eip155":
		return NetworkBase, true
	case string(NetworkSolana):
		return NetworkSolana, true
	default:
		return "", false
	}
}

// PaymentPayload is the decoded shape of the X-PAYMENT request
// header value. The wire form is base64-encoded JSON of this struct.
type PaymentPayload struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Payload     json.RawMessage `json:"payload"`
}

// Encode renders the payload to the base64-JSON form servers expect
// on the X-PAYMENT request header.
func (p PaymentPayload) Encode() (string, error) {
	buf, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// DecodePaymentPayload parses an X-PAYMENT header value back into the
// typed shape. Used by tests and verifiers.
func DecodePaymentPayload(value string) (PaymentPayload, error) {
	var p PaymentPayload
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

// ExactSchemePayload is the shape of PaymentPayload.Payload when
// scheme == "exact". For EVM USDC this maps onto an EIP-3009
// TransferWithAuthorization signature plus the authorization fields
// the server replays on-chain.
type ExactSchemePayload struct {
	Signature     string               `json:"signature"`
	Authorization EIP3009Authorization `json:"authorization"`
}

// EIP3009Authorization is the TransferWithAuthorization message a
// client signs to authorize a USDC transfer without prior approval.
// Field types match the on-chain signature.
type EIP3009Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

// PaymentReceipt is the parsed shape of the X-PAYMENT-RESPONSE
// header servers set on a successful retry.
type PaymentReceipt struct {
	Tx         string    `json:"tx,omitempty"`
	Network    Network   `json:"network,omitempty"`
	AmountUSDC string    `json:"amount_usdc,omitempty"`
	SettledAt  time.Time `json:"settled_at,omitempty"`
}

// ServiceManifest is the canonical per-service description sky10 pins
// when a user approves a service. The fields capture what the host
// needs to validate and execute future calls without re-asking the
// upstream service or the directory.
type ServiceManifest struct {
	ID           string    `json:"id"`
	DisplayName  string    `json:"display_name"`
	Category     string    `json:"category,omitempty"`
	Description  string    `json:"description,omitempty"`
	Endpoint     string    `json:"endpoint"`
	Networks     []Network `json:"networks"`
	MaxPriceUSDC string    `json:"max_price_usdc"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// EIP712TypedData is the JSON shape consumed by `ows sign message
// --typed-data` and equivalent EIP-712 signing implementations. The
// OWSSigner builds one of these per challenge and hands it to OWS.
type EIP712TypedData struct {
	Types       map[string][]EIP712Field `json:"types"`
	Domain      EIP712Domain             `json:"domain"`
	PrimaryType string                   `json:"primaryType"`
	Message     map[string]interface{}   `json:"message"`
}

// EIP712Field is one element of a TypedData.Types entry.
type EIP712Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// EIP712Domain is the EIP-712 domain separator. For x402 over USDC,
// the on-chain contract dictates name/version/chainId/verifyingContract.
type EIP712Domain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainID           int64  `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
}

// BuildTransferWithAuthorizationTypedData constructs the typed-data
// payload a client signs to authorize a USDC TransferWithAuthorization
// matching the supplied PaymentRequirements. validAfter is fixed to 0
// (immediately valid) and validBefore is now + maxTimeoutSeconds, or
// 60 seconds when the requirement omits the field.
func BuildTransferWithAuthorizationTypedData(req PaymentRequirements, fromAddress string, valueMicros string, nonceHex string, now time.Time) (EIP712TypedData, EIP3009Authorization, error) {
	chainID, ok := chainIDForNetwork(req.Network)
	if !ok {
		return EIP712TypedData{}, EIP3009Authorization{}, fmt.Errorf("unsupported network %q", req.Network)
	}
	domainName, _ := req.Extra["name"].(string)
	if domainName == "" {
		domainName = "USD Coin"
	}
	domainVersion, _ := req.Extra["version"].(string)
	if domainVersion == "" {
		domainVersion = "2"
	}
	timeout := req.MaxTimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	validBefore := now.UTC().Add(time.Duration(timeout) * time.Second).Unix()

	auth := EIP3009Authorization{
		From:        fromAddress,
		To:          req.PayTo,
		Value:       valueMicros,
		ValidAfter:  "0",
		ValidBefore: fmt.Sprintf("%d", validBefore),
		Nonce:       nonceHex,
	}
	td := EIP712TypedData{
		Types: map[string][]EIP712Field{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"TransferWithAuthorization": {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		Domain: EIP712Domain{
			Name:              domainName,
			Version:           domainVersion,
			ChainID:           chainID,
			VerifyingContract: req.Asset,
		},
		PrimaryType: "TransferWithAuthorization",
		Message: map[string]interface{}{
			"from":        auth.From,
			"to":          auth.To,
			"value":       auth.Value,
			"validAfter":  auth.ValidAfter,
			"validBefore": auth.ValidBefore,
			"nonce":       auth.Nonce,
		},
	}
	return td, auth, nil
}

// chainIDForNetwork maps the x402 network identifier to its EVM
// chain ID for EIP-712 domain construction. Solana is signed via a
// different scheme and is handled separately by the signer.
func chainIDForNetwork(network string) (int64, bool) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case string(NetworkBase):
		return 8453, true
	default:
		return 0, false
	}
}
