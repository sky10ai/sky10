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
	var v2 paymentChallengeV2
	if err := json.Unmarshal(raw, &v2); err != nil {
		return PaymentChallenge{}, fmt.Errorf("decode v2 challenge JSON: %w", err)
	}
	if len(v2.Accepts) == 0 {
		return PaymentChallenge{}, errors.New("challenge offered no payment options")
	}
	out := PaymentChallenge{Version: X402ProtocolV2, Resource: v2.Resource}
	for _, r := range v2.Accepts {
		canon, err := toCanonicalV2(r)
		if err != nil {
			return PaymentChallenge{}, err
		}
		out.Accepts = append(out.Accepts, canon)
	}
	return out, nil
}

// toCanonicalV2 converts a v2 wire requirement into the canonical
// form. `amount` is already the integer base unit; we validate and
// pass it through unchanged into AmountMicros.
func toCanonicalV2(r paymentRequirementsV2) (PaymentRequirements, error) {
	if strings.TrimSpace(r.Scheme) == "" {
		return PaymentRequirements{}, errors.New("requirement missing scheme")
	}
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
// includes `accepted` (the picked requirement, mirrored back into v2
// wire shape) and `resource` (echoed from the challenge).
func encodePaymentV2(req PaymentRequirements, inner json.RawMessage, resource *Resource) (string, error) {
	wire := paymentRequirementsV2{
		Scheme:            req.Scheme,
		Network:           req.Network,
		Amount:            req.AmountMicros,
		PayTo:             req.PayTo,
		Asset:             req.Asset,
		MaxTimeoutSeconds: req.MaxTimeoutSeconds,
		Extra:             req.Extra,
	}
	payload := paymentPayloadV2{
		X402Version: X402ProtocolV2,
		Accepted:    wire,
		Resource:    resource,
		Payload:     inner,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode v2 payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// parseReceipt extracts a PaymentReceipt from a Payment-Response or
// X-PAYMENT-RESPONSE header value. The wire is messy in the wild:
// some services emit plain JSON, others base64 JSON, the tx field
// is sometimes `tx` and sometimes `transaction`, and the same
// service might use a v1-named header with v2-encoded content
// (Blockrun ships X-Payment-Response containing base64 JSON with a
// `transaction` field). This parser is intentionally version-blind
// because the receipt's encoding doesn't track the request/response
// version cleanly.
func parseReceipt(value string) (PaymentReceipt, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return PaymentReceipt{}, errors.New("empty receipt header")
	}
	// First try base64 → JSON (the v2-style encoding most current
	// servers ship). Fall back to plain JSON for v1-era servers.
	raw, err := decodePaymentB64(value)
	if err != nil {
		raw = []byte(value)
	}
	var loose struct {
		Success     bool    `json:"success,omitempty"`
		Transaction string  `json:"transaction,omitempty"`
		Tx          string  `json:"tx,omitempty"`
		Network     Network `json:"network,omitempty"`
		Payer       string  `json:"payer,omitempty"`
		Asset       string  `json:"asset,omitempty"`
		AmountUSDC  string  `json:"amount_usdc,omitempty"`
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		// As a last resort, try the original value as JSON (handles
		// the "wasn't actually base64" case where decodePaymentB64's
		// permissive variants happened to match noise bytes).
		if jerr := json.Unmarshal([]byte(value), &loose); jerr != nil {
			return PaymentReceipt{}, fmt.Errorf("receipt parse: %w", err)
		}
	}
	tx := loose.Transaction
	if tx == "" {
		tx = loose.Tx
	}
	return PaymentReceipt{
		Tx:         tx,
		Network:    loose.Network,
		AmountUSDC: loose.AmountUSDC,
	}, nil
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
