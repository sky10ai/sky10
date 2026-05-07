package x402

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
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

// Wire-protocol revisions this implementation knows about.
//
// v1 (X402ProtocolV1) was the original spec where the 402 challenge
// lived in the response body under `accepts` and the amount field was
// named `maxAmountRequired` quoted as decimal USDC.
//
// v2 (X402ProtocolV2) is the current spec on agentic.market today:
// challenge is delivered in a `Payment-Required` response header
// carrying base64-JSON, the amount field is `amount` quoted as the
// integer base unit (micro-USDC for USDC), and the X-PAYMENT envelope
// echoes the picked requirement back as `accepted` plus the
// challenge's `resource` block so the facilitator can reconstruct
// the canonical EIP-712 hash.
//
// The two wire shapes share the inner ExactSchemePayload + EIP-3009
// authorization but diverge in every other JSON field. They must be
// kept apart on the read and write side (see protocol_v1.go and
// protocol_v2.go); transport.go detects which version a server
// speaks from the response shape and dispatches accordingly.
const (
	X402ProtocolV1 = 1
	X402ProtocolV2 = 2
)

// PaymentRequirements is the canonical, version-agnostic form of one
// `accepts` entry from a 402 challenge. v1 and v2 wire structs
// (paymentRequirementsV1, paymentRequirementsV2) decode into this
// type via their respective parsers; everything past parsing — the
// signer, the transport, the budget — sees only canonical
// PaymentRequirements.
//
// AmountMicros is always the integer base unit ("1000" = 0.001 USDC
// at 6 decimals). v1's decimal `maxAmountRequired` is converted on
// the way in; v2's `amount` passes through unchanged.
//
// RawWire holds the verbatim JSON of this accepts entry as the
// server emitted it. The encoder uses RawWire (when present) for
// the `accepted` echo on the X-PAYMENT envelope, so non-spec fields
// (Venice's `protocol` + per-entry `version`) round-trip without
// us having to model every vendor extension. Empty when the
// requirement was constructed in-memory rather than parsed.
type PaymentRequirements struct {
	Scheme            string
	Network           string
	AmountMicros      string
	PayTo             string
	Asset             string
	MaxTimeoutSeconds int64
	Extra             map[string]interface{}
	RawWire           json.RawMessage
}

// PaymentChallenge is the parsed, canonical 402 challenge.
type PaymentChallenge struct {
	Version    int
	Accepts    []PaymentRequirements
	Resource   *Resource
	Extensions map[string]json.RawMessage
}

// SelectRequirements returns the first PaymentRequirements compatible
// with our supported schemes and networks. Equivalent to
// PreferAndCheapest(nil) — kept for callers that don't care about
// network preference.
func (c PaymentChallenge) SelectRequirements() (PaymentRequirements, error) {
	return c.PreferAndCheapest(nil)
}

// PreferAndCheapest picks the cheapest compatible requirement,
// optionally restricted to a preferred network set. Real services
// often offer multiple price tiers and multiple chains in a single
// challenge — Quicknode's /solana-mainnet/ ships 22 entries spanning
// Base/Polygon/Solana with prices from $0.0001 to $10. A naive
// "first compatible" pick lands on a 10-USDC daily-pass entry that
// blows past any reasonable per-call budget.
//
//   - When prefer is non-empty, only requirements whose canonical
//     network is in that list are considered. ErrNoCompatibleRequirements
//     is returned if none match.
//   - Among matches, the smallest AmountMicros wins. Ties pick the
//     first occurrence to keep ordering deterministic.
func (c PaymentChallenge) PreferAndCheapest(prefer []Network) (PaymentRequirements, error) {
	prefSet := make(map[Network]struct{}, len(prefer))
	for _, n := range prefer {
		prefSet[n] = struct{}{}
	}
	var best *PaymentRequirements
	var bestMicros *big.Int
	for i := range c.Accepts {
		req := c.Accepts[i]
		if !isSupportedScheme(req.Scheme) {
			continue
		}
		canon, ok := canonicalizeNetwork(req.Network)
		if !ok {
			continue
		}
		if len(prefSet) > 0 {
			if _, want := prefSet[canon]; !want {
				continue
			}
		}
		micros, err := parseIntegerBaseUnits(req.AmountMicros)
		if err != nil {
			continue
		}
		if best == nil || micros.Cmp(bestMicros) < 0 {
			r := req
			best = &r
			bestMicros = micros
		}
	}
	if best == nil {
		return PaymentRequirements{}, ErrNoCompatibleRequirements
	}
	return *best, nil
}

// parseIntegerBaseUnits is the canonical-amount parser used in
// PreferAndCheapest's price comparison. v1 and v2 wires both store
// AmountMicros as integer base units after canonicalization.
func parseIntegerBaseUnits(s string) (*big.Int, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, errors.New("empty amount")
	}
	if !isAllDigits(trimmed) {
		return nil, fmt.Errorf("amount %q must be integer base units", trimmed)
	}
	v, ok := new(big.Int).SetString(trimmed, 10)
	if !ok || v.Sign() <= 0 {
		return nil, fmt.Errorf("amount %q must be positive", trimmed)
	}
	return v, nil
}

// ErrNoCompatibleRequirements indicates the server's 402 challenge
// listed no scheme+network combination this client supports.
var ErrNoCompatibleRequirements = errors.New("x402: no compatible payment requirements offered")

// Resource describes the paid endpoint a 402 challenge targets. v2
// servers include it in the challenge and expect it echoed back on
// the X-PAYMENT envelope so the facilitator can verify the client
// targeted exactly the resource it offered.
//
// All three fields are kept on the wire even when empty. Some
// facilitators (Smartflow) reject envelopes whose echoed resource
// has a different key set than what they sent — so an empty
// `mimeType` in the challenge must round-trip as `"mimeType": ""`,
// not be dropped via omitempty.
type Resource struct {
	URL         string `json:"url"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// ExactSchemePayload is the inner X-PAYMENT payload when scheme ==
// "exact". Both v1 and v2 wire envelopes carry this structure
// verbatim under the `payload` key, so the signer produces it once
// and version-specific encoders embed it.
type ExactSchemePayload struct {
	Signature     string               `json:"signature"`
	Authorization EIP3009Authorization `json:"authorization"`
}

// EIP3009Authorization is the TransferWithAuthorization message a
// client signs to authorize a USDC transfer without prior approval.
// All six fields are wire-public (matches OWS pay request capture
// against Exa).
type EIP3009Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

// PaymentReceipt is the parsed shape of the response receipt header
// servers set on a successful retry. v1 emits it on
// X-PAYMENT-RESPONSE as plain JSON; v2 emits it on Payment-Response
// as base64 JSON. Both decode into this canonical form.
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
	ID           string            `json:"id"`
	DisplayName  string            `json:"display_name"`
	Category     string            `json:"category,omitempty"`
	Description  string            `json:"description,omitempty"`
	Endpoint     string            `json:"endpoint"`
	ServiceURL   string            `json:"service_url,omitempty"`
	Endpoints    []ServiceEndpoint `json:"endpoints,omitempty"`
	Networks     []Network         `json:"networks"`
	MaxPriceUSDC string            `json:"max_price_usdc"`
	UpdatedAt    time.Time         `json:"updated_at,omitempty"`

	// SIWXDomain enables Sign-In-With-X authentication on every
	// request to this service. When non-empty, Backend.Call attaches
	// an X-Sign-In-With-X header (signed with the wallet) to each
	// outgoing request — required by deposit-style services like
	// Venice that gate API access on wallet-bound session auth
	// rather than per-call x402 payments. The value is the domain
	// the SIWE (EIP-4361) message names (e.g. "api.venice.ai");
	// empty disables SIWX for the service.
	SIWXDomain string `json:"siwx_domain,omitempty"`
}

// ServiceEndpoint is display metadata for one callable URL advertised
// by a service directory. Backend.Call still routes through Endpoint
// plus the caller-provided path; these entries help humans and agents
// inspect the available URLs and costs.
type ServiceEndpoint struct {
	URL         string  `json:"url"`
	Method      string  `json:"method,omitempty"`
	Description string  `json:"description,omitempty"`
	PriceUSDC   string  `json:"price_usdc,omitempty"`
	Network     Network `json:"network,omitempty"`
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

	// validAfter = "0" (epoch). Some facilitators (Coinbase's, used
	// by Exa) require this — rejecting validAfter=now with HTTP 400
	// "facilitator returned 400". Setting validAfter=now broke Exa
	// even though it matches OWS's local-server behavior, so we keep
	// the wire-conservative "0" value.
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

// chainIDForNetwork maps the x402 network identifier to its EVM chain
// ID for EIP-712 domain construction. Solana is signed via a different
// scheme and is handled separately by the signer.
func chainIDForNetwork(network string) (int64, bool) {
	canon, ok := canonicalizeNetwork(network)
	if !ok {
		return 0, false
	}
	switch canon {
	case NetworkBase:
		return 8453, true
	default:
		return 0, false
	}
}

// isSupportedScheme reports whether the wire-level scheme value
// is one we know how to sign. The empty string is treated as the
// implicit "exact" default — Venice's /x402/top-up endpoint, for
// example, omits the field entirely. "exact" is the only scheme
// in deployment today, so a missing value is unambiguous.
func isSupportedScheme(scheme string) bool {
	scheme = strings.TrimSpace(scheme)
	if scheme == "" {
		return true
	}
	return strings.EqualFold(scheme, SchemeExact)
}

func isSupportedNetwork(network string) bool {
	_, ok := canonicalizeNetwork(network)
	return ok
}

// solanaMainnetClusterID is the genesis hash CAIP-2 uses to identify
// Solana mainnet-beta. Devnet and testnet have different cluster IDs;
// PreferAndCheapest skips them when a manifest specifies NetworkSolana
// because we have no way to settle on those clusters today.
const solanaMainnetClusterID = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"

// canonicalizeNetwork maps the wire-level `network` field — which may
// be a bare name ("base", "solana") or a CAIP-2 identifier
// ("eip155:8453", "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp") — to the
// package's canonical Network value. Only Base (chain id 8453) and
// Solana mainnet are accepted; other eip155 chains and other Solana
// clusters return false even though we recognize the namespace.
func canonicalizeNetwork(network string) (Network, bool) {
	raw := strings.TrimSpace(network)
	if raw == "" {
		return "", false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "eip155:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(lower, "eip155:"), 10, 64)
		if err != nil {
			return "", false
		}
		if id == 8453 {
			return NetworkBase, true
		}
		return "", false
	}
	if strings.HasPrefix(lower, "solana:") {
		// Compare cluster ID case-sensitively — Solana base58 is
		// case-significant, the genesis hash includes mixed case.
		cluster := raw[len("solana:"):]
		if cluster == solanaMainnetClusterID {
			return NetworkSolana, true
		}
		return "", false
	}
	switch lower {
	case string(NetworkBase):
		return NetworkBase, true
	case string(NetworkSolana):
		return NetworkSolana, true
	default:
		return "", false
	}
}
