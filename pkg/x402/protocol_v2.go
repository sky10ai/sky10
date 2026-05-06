package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// paymentRequirementsV2 is the v2 wire shape of one `accepts` entry.
// The amount is quoted as the integer base unit ("1000" = 0.001 USDC
// at 6 decimals). It converts to canonical PaymentRequirements via
// toCanonicalV2.
type paymentRequirementsV2 struct {
	Scheme            string                 `json:"scheme"`
	Network           string                 `json:"network"`
	Amount            string                 `json:"amount"`
	PayTo             string                 `json:"payTo"`
	MaxTimeoutSeconds int64                  `json:"maxTimeoutSeconds,omitempty"`
	Asset             string                 `json:"asset"`
	Extra             map[string]interface{} `json:"extra,omitempty"`
}

// paymentChallengeV2 is the v2 wire shape of a 402 challenge,
// delivered base64-encoded in the `Payment-Required` response header.
// The body is typically a short JSON error string and is ignored.
type paymentChallengeV2 struct {
	X402Version int                     `json:"x402Version"`
	Accepts     []paymentRequirementsV2 `json:"accepts"`
	Resource    *Resource               `json:"resource,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

// paymentPayloadV2 is the v2 wire shape of an X-PAYMENT envelope.
// `accepted` echoes the picked PaymentRequirements back to the
// server; `resource` echoes the challenge's resource block. Both are
// required by v2 facilitators to reconstruct the canonical EIP-712
// hash they verify against. Top-level scheme/network are absent on
// the wire — they live inside `accepted`.
type paymentPayloadV2 struct {
	X402Version int                   `json:"x402Version"`
	Accepted    paymentRequirementsV2 `json:"accepted"`
	Resource    *Resource             `json:"resource,omitempty"`
	Payload     json.RawMessage       `json:"payload"`
}

// parseChallengeV2Header decodes the base64-encoded JSON value of a
// v2 Payment-Required response header into the canonical
// PaymentChallenge. Encoding is permissive across base64 variants
// because real servers don't all pick standard padded encoding.
func parseChallengeV2Header(value string) (PaymentChallenge, error) {
	raw, err := decodePaymentB64(value)
	if err != nil {
		return PaymentChallenge{}, fmt.Errorf("decode v2 challenge header: %w", err)
	}
	// Decode into a permissive outer shape so we can keep each
	// accepts entry's raw bytes for verbatim echo on the X-PAYMENT
	// envelope. Some servers (Venice) emit non-spec per-entry
	// fields that the verifier expects mirrored back exactly.
	var rawOuter struct {
		X402Version int                        `json:"x402Version"`
		Accepts     []json.RawMessage          `json:"accepts"`
		Resource    *Resource                  `json:"resource,omitempty"`
		Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
		Error       string                     `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &rawOuter); err != nil {
		return PaymentChallenge{}, fmt.Errorf("decode v2 challenge JSON: %w", err)
	}
	if len(rawOuter.Accepts) == 0 {
		return PaymentChallenge{}, errors.New("challenge offered no payment options")
	}
	out := PaymentChallenge{
		Version:    X402ProtocolV2,
		Resource:   rawOuter.Resource,
		Extensions: rawOuter.Extensions,
	}
	for _, entry := range rawOuter.Accepts {
		var r paymentRequirementsV2
		if err := json.Unmarshal(entry, &r); err != nil {
			return PaymentChallenge{}, err
		}
		canon, err := toCanonicalV2(r)
		if err != nil {
			return PaymentChallenge{}, err
		}
		canon.RawWire = append(json.RawMessage(nil), entry...)
		out.Accepts = append(out.Accepts, canon)
	}
	return out, nil
}

// toCanonicalV2 converts a v2 wire requirement into the canonical
// form. `amount` is already the integer base unit; we validate and
// pass it through unchanged into AmountMicros.
//
// `scheme` is optional — Venice's /x402/top-up endpoint omits the
// field entirely (substitutes a top-level `protocol: "x402"` field
// not in the spec). When absent we leave the canonical Scheme
// empty; isSupportedScheme treats that as the implicit "exact"
// default, which is the only scheme we currently sign for anyway.
func toCanonicalV2(r paymentRequirementsV2) (PaymentRequirements, error) {
	if strings.TrimSpace(r.Network) == "" {
		return PaymentRequirements{}, errors.New("requirement missing network")
	}
	if strings.TrimSpace(r.PayTo) == "" {
		return PaymentRequirements{}, errors.New("requirement missing payTo")
	}
	if strings.TrimSpace(r.Amount) == "" {
		return PaymentRequirements{}, errors.New("requirement missing amount")
	}
	if !isAllDigits(strings.TrimSpace(r.Amount)) {
		return PaymentRequirements{}, fmt.Errorf("v2 amount %q must be integer base units", r.Amount)
	}
	return PaymentRequirements{
		Scheme:            r.Scheme,
		Network:           r.Network,
		AmountMicros:      strings.TrimSpace(r.Amount),
		PayTo:             r.PayTo,
		Asset:             r.Asset,
		MaxTimeoutSeconds: r.MaxTimeoutSeconds,
		Extra:             r.Extra,
	}, nil
}

// encodePaymentV2 builds the X-PAYMENT base64 string for a v2 server.
// `inner` is a JSON-encoded ExactSchemePayload; the outer envelope
// is a hybrid that satisfies every facilitator we've observed:
//
//   - top-level `scheme` and `network` (canonical x402 npm shape;
//     Venice's verifier hard-requires these and won't accept
//     the v2 nested-only form)
//   - `accepted` block echoed from the challenge (Smartflow's
//     verifier reads this to confirm the picked requirement;
//     omitting it triggers their replay-protection check)
//   - `resource` block echoed when present (Smartflow again)
//
// Top-level `network` normalizes to the bare canonical name
// ("base", "solana") since Venice rejects CAIP-2 here.
//
// When the requirement carries RawWire bytes (set by the parser) we
// echo them verbatim under `accepted` so vendor extensions
// (Venice's protocol/version per accepts entry) round-trip exactly.
func encodePaymentV2(req PaymentRequirements, inner json.RawMessage, resource *Resource) (string, error) {
	var accepted json.RawMessage
	if len(req.RawWire) > 0 {
		accepted = append(json.RawMessage(nil), req.RawWire...)
	} else {
		wire := paymentRequirementsV2{
			Scheme:            req.Scheme,
			Network:           req.Network,
			Amount:            req.AmountMicros,
			PayTo:             req.PayTo,
			Asset:             req.Asset,
			MaxTimeoutSeconds: req.MaxTimeoutSeconds,
			Extra:             req.Extra,
		}
		raw, err := json.Marshal(wire)
		if err != nil {
			return "", fmt.Errorf("encode v2 accepted: %w", err)
		}
		accepted = raw
	}
	scheme := strings.TrimSpace(req.Scheme)
	if scheme == "" {
		scheme = SchemeExact
	}
	network := normalizeNetworkBare(req.Network)
	payload := struct {
		X402Version int             `json:"x402Version"`
		Scheme      string          `json:"scheme"`
		Network     string          `json:"network"`
		Accepted    json.RawMessage `json:"accepted,omitempty"`
		Resource    *Resource       `json:"resource,omitempty"`
		Payload     json.RawMessage `json:"payload"`
	}{
		X402Version: X402ProtocolV2,
		Scheme:      scheme,
		Network:     network,
		Accepted:    accepted,
		Resource:    resource,
		Payload:     inner,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode v2 payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// normalizeNetworkBare returns the bare canonical name ("base",
// "solana") for a wire network identifier. Venice's verifier (and
// the canonical x402 npm `createPaymentHeader`) reject CAIP-2
// identifiers in the top-level envelope, even though they accept
// them inside `accepted`.
func normalizeNetworkBare(network string) string {
	canon, ok := canonicalizeNetwork(network)
	if !ok {
		return network
	}
	return string(canon)
}

// parseReceipt extracts a PaymentReceipt from a Payment-Response or
// X-PAYMENT-RESPONSE header value. The wire is messy in the wild:
//
//   - some services emit plain JSON ({"tx": "0x…", "network": "base"})
//   - some emit base64-wrapped JSON with `transaction` instead of `tx`
//     (Blockrun does this even under the v1-named X-Payment-Response)
//   - some emit the bare tx identifier with no JSON wrapper at all
//     (Messari sets X-Payment-Response to "0x<64 hex>" on Base or an
//     88-character base58 string on Solana)
//
// The parser is version-blind because the receipt encoding doesn't
// track the request/response version cleanly. We try the JSON paths
// first; if they fail we fall back to treating the value as a bare
// tx identifier.
func parseReceipt(value string) (PaymentReceipt, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return PaymentReceipt{}, errors.New("empty receipt header")
	}
	if r, ok := parseReceiptJSON(value); ok {
		return r, nil
	}
	if r, ok := parseReceiptBareTx(value); ok {
		return r, nil
	}
	return PaymentReceipt{}, fmt.Errorf("receipt parse: unrecognized format (length=%d)", len(value))
}

// parseReceiptJSON tries plain JSON, then base64-decoded JSON.
func parseReceiptJSON(value string) (PaymentReceipt, bool) {
	var loose struct {
		Success     bool    `json:"success,omitempty"`
		Transaction string  `json:"transaction,omitempty"`
		Tx          string  `json:"tx,omitempty"`
		Network     Network `json:"network,omitempty"`
		Payer       string  `json:"payer,omitempty"`
		Asset       string  `json:"asset,omitempty"`
		AmountUSDC  string  `json:"amount_usdc,omitempty"`
	}
	tryDecode := func(b []byte) bool {
		if err := json.Unmarshal(b, &loose); err != nil {
			return false
		}
		return loose.Tx != "" || loose.Transaction != ""
	}
	if tryDecode([]byte(value)) {
		// matched plain JSON
	} else if raw, err := decodePaymentB64(value); err == nil && tryDecode(raw) {
		// matched base64-wrapped JSON
	} else {
		return PaymentReceipt{}, false
	}
	tx := loose.Transaction
	if tx == "" {
		tx = loose.Tx
	}
	return PaymentReceipt{
		Tx:         tx,
		Network:    loose.Network,
		AmountUSDC: loose.AmountUSDC,
	}, true
}

// parseReceiptBareTx detects a header value that's just a raw tx
// identifier with no JSON wrapper. Two formats observed in the
// wild:
//
//   - "0x" + 64 hex chars: an EVM (Base) tx hash
//   - 86–88 base58 chars: a Solana signature (the canonical tx
//     identifier on SVM, 64 raw bytes encoded as base58)
//
// Network is left blank when we can't infer it from the format
// alone — the caller can backfill from the manifest if needed.
func parseReceiptBareTx(value string) (PaymentReceipt, bool) {
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		hex := value[2:]
		if len(hex) == 64 && isHex(hex) {
			return PaymentReceipt{Tx: value, Network: NetworkBase}, true
		}
	}
	if l := len(value); l >= 86 && l <= 88 && isBase58(value) {
		return PaymentReceipt{Tx: value, Network: NetworkSolana}, true
	}
	return PaymentReceipt{}, false
}

// isHex reports whether every rune is a hex digit (0-9 a-f A-F).
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// isBase58 reports whether every rune is in the Bitcoin/Solana
// base58 alphabet (no 0, O, I, l).
func isBase58(s string) bool {
	const alpha = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(alpha, r) {
			return false
		}
	}
	return true
}

// decodePaymentB64 decodes a base64 string permissively: standard,
// raw-standard, URL-safe, and raw-URL-safe forms are all accepted so
// we don't break against a server that picks a different variant.
func decodePaymentB64(value string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if raw, err := enc.DecodeString(value); err == nil {
			return raw, nil
		}
	}
	return nil, errors.New("not valid base64")
}
