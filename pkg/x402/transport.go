package x402

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrPaymentNotAccepted indicates the upstream service returned 402
// even after a signed retry. Often a transient failure on the server
// side; agents may retry once more with backoff.
var ErrPaymentNotAccepted = errors.New("x402: payment not accepted by service")

// Header names used on the x402 wire. v1 used the X- prefixed forms
// on responses and put the challenge in the response body; v2 moved
// the challenge into a Payment-Required header and the receipt into
// a Payment-Response header. We read both and write the legacy
// X-PAYMENT request header (case-insensitive on the server side).
const (
	HeaderPayment           = "X-PAYMENT"
	HeaderPaymentResponse   = "X-PAYMENT-RESPONSE"
	HeaderPaymentRequiredV2 = "Payment-Required"
	HeaderPaymentResponseV2 = "Payment-Response"
)

// Transport performs one x402-aware HTTP round-trip: initial request,
// 402 detection, sign, retry. It is intentionally a single function
// rather than an http.RoundTripper because the semantics include
// payment authorization and budget enforcement that don't fit cleanly
// into the RoundTripper contract.
type Transport struct {
	HTTP   *http.Client
	Signer Signer
}

// NewTransport constructs a Transport with sensible defaults. The
// supplied Signer is required. The default per-request HTTP timeout
// is 120 seconds because the x402 retry typically settles a real
// USDC transfer and (for crawl/inference services) does real work
// before responding — 30s is not enough headroom for the slowest
// services in the catalog.
func NewTransport(signer Signer) *Transport {
	return &Transport{
		HTTP: &http.Client{
			Timeout: 120 * time.Second,
		},
		Signer: signer,
	}
}

// CallRequest is the input to Transport.Call.
type CallRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// CallResponse is the output of Transport.Call after a successful
// payment-and-retrieve cycle. Receipt is populated when the service
// returned an X-PAYMENT-RESPONSE header on the retry; for free
// (unmetered) endpoints Receipt is nil.
type CallResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
	Receipt *PaymentReceipt
}

// Call performs the full x402 round-trip. The flow:
//
//  1. issue the request unauthorized
//  2. if 200, return as-is (free endpoint or already authorized)
//  3. if 402, parse the challenge, sign via Transport.Signer, retry
//     with X-PAYMENT
//  4. on the retry, parse the X-PAYMENT-RESPONSE header into a
//     PaymentReceipt and return alongside the body
func (t *Transport) Call(ctx context.Context, req CallRequest) (*CallResponse, error) {
	if t == nil || t.HTTP == nil {
		return nil, errors.New("x402: transport not configured")
	}
	if t.Signer == nil {
		return nil, ErrSignerNotConfigured
	}

	resp, err := t.do(ctx, req, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		return readCallResponse(resp)
	}

	challenge, err := parseChallenge(resp)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("parse 402 challenge: %w", err)
	}
	requirement, err := challenge.SelectRequirements()
	if err != nil {
		return nil, err
	}

	payload, err := t.Signer.Sign(ctx, requirement)
	if err != nil {
		return nil, fmt.Errorf("sign payment: %w", err)
	}
	// Echo the challenge's version so v1 servers see v1 and v2
	// servers see v2. The signer fills in its default (X402ProtocolVersion)
	// when constructing the payload; we override here once we know
	// what the server speaks.
	if challenge.X402Version > 0 {
		payload.X402Version = challenge.X402Version
	}
	encoded, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode payment header: %w", err)
	}

	retry, err := t.do(ctx, req, encoded)
	if err != nil {
		return nil, err
	}
	if retry.StatusCode == http.StatusPaymentRequired {
		retry.Body.Close()
		return nil, ErrPaymentNotAccepted
	}
	return readCallResponse(retry)
}

func (t *Transport) do(ctx context.Context, req CallRequest, paymentHeader string) (*http.Response, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if paymentHeader != "" {
		httpReq.Header.Set(HeaderPayment, paymentHeader)
	}
	return t.HTTP.Do(httpReq)
}

// parseChallenge decodes the 402 challenge from either the v2
// Payment-Required response header (base64 JSON) or the v1 response
// body (raw JSON), preferring the header when both are present so
// servers transitioning between versions still work.
func parseChallenge(resp *http.Response) (PaymentChallenge, error) {
	if hdr := strings.TrimSpace(resp.Header.Get(HeaderPaymentRequiredV2)); hdr != "" {
		raw, err := decodePaymentB64(hdr)
		if err != nil {
			return PaymentChallenge{}, fmt.Errorf("decode %s header: %w", HeaderPaymentRequiredV2, err)
		}
		return decodeAndValidateChallenge(raw)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return PaymentChallenge{}, err
	}
	return decodeAndValidateChallenge(raw)
}

// decodeAndValidateChallenge parses the raw JSON challenge bytes and
// enforces the field-level invariants we depend on downstream.
func decodeAndValidateChallenge(raw []byte) (PaymentChallenge, error) {
	var c PaymentChallenge
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if len(c.Accepts) == 0 {
		return c, errors.New("challenge offered no payment options")
	}
	for _, req := range c.Accepts {
		if strings.TrimSpace(req.Scheme) == "" {
			return c, errors.New("challenge requirement missing scheme")
		}
		if strings.TrimSpace(req.Network) == "" {
			return c, errors.New("challenge requirement missing network")
		}
		if strings.TrimSpace(req.PayTo) == "" {
			return c, errors.New("challenge requirement missing payTo")
		}
		if strings.TrimSpace(req.MaxAmount()) == "" {
			return c, errors.New("challenge requirement missing amount")
		}
	}
	return c, nil
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

func readCallResponse(resp *http.Response) (*CallResponse, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	out := &CallResponse{
		Status:  resp.StatusCode,
		Body:    body,
		Headers: extractStringHeaders(resp.Header),
	}
	rec := resp.Header.Get(HeaderPaymentResponseV2)
	if rec == "" {
		rec = resp.Header.Get(HeaderPaymentResponse)
	}
	if rec != "" {
		parsed, err := parsePaymentReceipt(rec)
		if err == nil {
			out.Receipt = &parsed
		}
		// A malformed receipt header is logged-and-ignored to avoid
		// blocking the agent on a server's bookkeeping mistake; the
		// budget package re-derives spend from the request side via
		// the challenge amount when needed.
	}
	return out, nil
}

// extractStringHeaders flattens an http.Header into a single-value
// map. Multi-valued headers collapse to a comma-joined value, which
// matches HTTP's semantics for fields that allow multiple values and
// keeps the wire format simple.
func extractStringHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// parsePaymentReceipt accepts the X-PAYMENT-RESPONSE / Payment-Response
// header value, which is plain JSON in v1 servers and base64-encoded
// JSON in v2 servers. Try plain first, fall back to base64 decode.
func parsePaymentReceipt(value string) (PaymentReceipt, error) {
	var r PaymentReceipt
	if err := json.Unmarshal([]byte(value), &r); err == nil {
		return r, nil
	}
	raw, err := decodePaymentB64(strings.TrimSpace(value))
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	return r, nil
}
