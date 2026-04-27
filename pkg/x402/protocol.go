package x402

import (
	"encoding/base64"
	"encoding/json"
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

// PaymentChallenge is the parsed shape of an x402 challenge surfaced
// either in the body of a 402 response or in headers. Field names
// match the wire JSON the canonical x402 server emits; unknown fields
// are preserved on the raw envelope so we can echo them in the audit
// log without depending on every server using identical schemas.
type PaymentChallenge struct {
	Version    string          `json:"version,omitempty"`
	Network    Network         `json:"network"`
	Currency   Currency        `json:"currency"`
	Amount     string          `json:"amount"`
	Recipient  string          `json:"recipient"`
	Nonce      string          `json:"nonce"`
	ExpiresAt  time.Time       `json:"expires_at,omitempty"`
	PayURL     string          `json:"pay_url,omitempty"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

// PaymentReceipt is the parsed shape of a settlement receipt the
// service emits in the X-PAYMENT-RESPONSE header (or response body,
// depending on the server) after the upstream call succeeds.
type PaymentReceipt struct {
	Tx         string    `json:"tx"`
	Network    Network   `json:"network"`
	AmountUSDC string    `json:"amount_usdc"`
	SettledAt  time.Time `json:"settled_at,omitempty"`
}

// ServiceManifest is the canonical per-service description sky10 pins
// when a user approves a service. The fields capture what the host
// needs to validate and execute future calls without re-asking the
// upstream service or the directory.
//
// agentic.market and per-service /.well-known/x402.json both produce
// data that maps onto this shape.
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

// PaymentHeader is the wire-side X-PAYMENT header value: a signed
// authorization the upstream verifies on the retry. Constructed by
// Signer.Sign and applied by the transport.
type PaymentHeader struct {
	Network   Network `json:"network"`
	Amount    string  `json:"amount"`
	Recipient string  `json:"recipient"`
	Nonce     string  `json:"nonce"`
	Signature string  `json:"signature"`
}

// Encode renders the header into the value sent on the X-PAYMENT
// header. Servers expect a base64-encoded JSON payload by canonical
// convention; we keep that behavior in one place so any future change
// affects every call site.
func (h PaymentHeader) Encode() (string, error) {
	buf, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// DecodePaymentHeader parses an X-PAYMENT header value back into the
// typed shape. Used by tests against the httptest x402 fake to verify
// signed authorizations on the server side.
func DecodePaymentHeader(value string) (PaymentHeader, error) {
	var h PaymentHeader
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, err
	}
	return h, nil
}
