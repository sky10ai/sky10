package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// paymentRequirementsV1 is the v1 wire shape of one `accepts` entry.
// The amount is quoted as a decimal USDC string (e.g. "0.005"). It
// converts to the canonical PaymentRequirements via toCanonicalV1.
type paymentRequirementsV1 struct {
	Scheme            string                 `json:"scheme"`
	Network           string                 `json:"network"`
	MaxAmountRequired string                 `json:"maxAmountRequired"`
	Resource          string                 `json:"resource,omitempty"`
	Description       string                 `json:"description,omitempty"`
	MimeType          string                 `json:"mimeType,omitempty"`
	OutputSchema      json.RawMessage        `json:"outputSchema,omitempty"`
	PayTo             string                 `json:"payTo"`
	MaxTimeoutSeconds int64                  `json:"maxTimeoutSeconds,omitempty"`
	Asset             string                 `json:"asset"`
	Extra             map[string]interface{} `json:"extra,omitempty"`
}

// paymentChallengeV1 is the v1 wire shape of a 402 challenge body.
type paymentChallengeV1 struct {
	X402Version int                     `json:"x402Version"`
	Accepts     []paymentRequirementsV1 `json:"accepts"`
	Error       string                  `json:"error,omitempty"`
}

// paymentPayloadV1 is the v1 wire shape of an X-PAYMENT envelope.
// The Payload field carries an ExactSchemePayload for scheme=exact.
type paymentPayloadV1 struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Payload     json.RawMessage `json:"payload"`
}

// parseChallengeV1Body decodes the v1 challenge body bytes into the
// canonical PaymentChallenge. Decimal `maxAmountRequired` strings are
// converted to integer micro-USDC base units so downstream code only
// has to deal with one form.
func parseChallengeV1Body(raw []byte) (PaymentChallenge, error) {
	var v1 paymentChallengeV1
	if err := json.Unmarshal(raw, &v1); err != nil {
		return PaymentChallenge{}, err
	}
	if len(v1.Accepts) == 0 {
		return PaymentChallenge{}, errors.New("challenge offered no payment options")
	}
	out := PaymentChallenge{Version: X402ProtocolV1}
	for _, r := range v1.Accepts {
		canon, err := toCanonicalV1(r)
		if err != nil {
			return PaymentChallenge{}, err
		}
		out.Accepts = append(out.Accepts, canon)
	}
	return out, nil
}

// toCanonicalV1 converts a v1 wire requirement into the canonical
// form. `maxAmountRequired` is parsed as decimal USDC and stored as
// integer micro-USDC.
func toCanonicalV1(r paymentRequirementsV1) (PaymentRequirements, error) {
	if strings.TrimSpace(r.Scheme) == "" {
		return PaymentRequirements{}, errors.New("requirement missing scheme")
	}
	if strings.TrimSpace(r.Network) == "" {
		return PaymentRequirements{}, errors.New("requirement missing network")
	}
	if strings.TrimSpace(r.PayTo) == "" {
		return PaymentRequirements{}, errors.New("requirement missing payTo")
	}
	if strings.TrimSpace(r.MaxAmountRequired) == "" {
		return PaymentRequirements{}, errors.New("requirement missing maxAmountRequired")
	}
	micros, err := parseUSDC(r.MaxAmountRequired)
	if err != nil {
		return PaymentRequirements{}, fmt.Errorf("v1 maxAmountRequired %q: %w", r.MaxAmountRequired, err)
	}
	if micros.Sign() <= 0 {
		return PaymentRequirements{}, fmt.Errorf("v1 maxAmountRequired %q must be positive", r.MaxAmountRequired)
	}
	return PaymentRequirements{
		Scheme:            r.Scheme,
		Network:           r.Network,
		AmountMicros:      micros.String(),
		PayTo:             r.PayTo,
		Asset:             r.Asset,
		MaxTimeoutSeconds: r.MaxTimeoutSeconds,
		Extra:             r.Extra,
	}, nil
}

// encodePaymentV1 builds the X-PAYMENT base64 string for a v1 server.
// inner must be a JSON-encoded ExactSchemePayload (or another scheme's
// inner); the outer envelope echoes scheme and network at the top
// level as v1 facilitators route on those fields.
func encodePaymentV1(req PaymentRequirements, inner json.RawMessage) (string, error) {
	payload := paymentPayloadV1{
		X402Version: X402ProtocolV1,
		Scheme:      req.Scheme,
		Network:     req.Network,
		Payload:     inner,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode v1 payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// parseReceiptV1 parses a v1 X-PAYMENT-RESPONSE header value as plain
// JSON into the canonical PaymentReceipt.
func parseReceiptV1(value string) (PaymentReceipt, error) {
	var r PaymentReceipt
	if err := json.Unmarshal([]byte(value), &r); err != nil {
		return r, fmt.Errorf("v1 receipt parse: %w", err)
	}
	return r, nil
}
