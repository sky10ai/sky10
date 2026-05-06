package x402

import (
	"bytes"
	"context"
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
// a Payment-Response header.
//
// On retry we set BOTH X-Payment and Payment-Signature with the
// same base64 envelope value — that's what OWS pay request emits
// and some servers (Smartflow) check the Payment-Signature header
// rather than X-Payment. Receipts are read from either Payment-Response
// or X-Payment-Response, whichever the server sets first.
const (
	HeaderPayment            = "X-PAYMENT"
	HeaderPaymentSignatureV2 = "Payment-Signature"
	HeaderPaymentResponse    = "X-PAYMENT-RESPONSE"
	HeaderPaymentRequiredV2  = "Payment-Required"
	HeaderPaymentResponseV2  = "Payment-Response"
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
// returned a Payment-Response (v2) or X-PAYMENT-RESPONSE (v1) header
// on the retry; for free (unmetered) endpoints Receipt is nil.
type CallResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
	Receipt *PaymentReceipt
}

// Call performs the full x402 round-trip, dispatching on the
// detected protocol version:
//
//  1. issue the request unauthorized
//  2. if 200, return as-is (free endpoint or already authorized)
//  3. if 402, detect v1 vs v2 from response shape, parse the
//     challenge, sign via Transport.Signer, retry with X-PAYMENT
//  4. on the retry, parse the appropriate receipt header and return
//     it alongside the body
//
// The version is fixed once detected; we never mix v1 and v2 wire
// shapes on the same request.
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
		return readCallResponse(resp, X402ProtocolV1)
	}

	version, challenge, err := readChallenge(resp)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("parse 402 challenge: %w", err)
	}
	requirement, err := challenge.SelectRequirements()
	if err != nil {
		return nil, err
	}

	signed, err := t.Signer.Sign(ctx, requirement)
	if err != nil {
		return nil, fmt.Errorf("sign payment: %w", err)
	}

	encoded, err := encodePayment(version, requirement, signed.Inner, challenge.Resource)
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
	return readCallResponse(retry, version)
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
		httpReq.Header.Set(HeaderPaymentSignatureV2, paymentHeader)
	}
	return t.HTTP.Do(httpReq)
}

// readChallenge dispatches between v1 (body-encoded) and v2
// (header-encoded) challenge shapes. v2 takes precedence so a
// dual-emitting server does not regress to v1 parsing.
func readChallenge(resp *http.Response) (int, PaymentChallenge, error) {
	if hdr := strings.TrimSpace(resp.Header.Get(HeaderPaymentRequiredV2)); hdr != "" {
		c, err := parseChallengeV2Header(hdr)
		return X402ProtocolV2, c, err
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, PaymentChallenge{}, err
	}
	c, err := parseChallengeV1Body(raw)
	return X402ProtocolV1, c, err
}

// encodePayment dispatches the X-PAYMENT envelope encoding to the
// matching version.
func encodePayment(version int, req PaymentRequirements, inner json.RawMessage, resource *Resource) (string, error) {
	switch version {
	case X402ProtocolV1:
		return encodePaymentV1(req, inner)
	case X402ProtocolV2:
		return encodePaymentV2(req, inner, resource)
	default:
		return "", fmt.Errorf("unsupported x402 version %d", version)
	}
}

// readCallResponse builds the CallResponse from the retry response.
// The version argument is unused for receipt parsing — receipts are
// version-blind in practice (servers mix the v1-named
// X-Payment-Response header with v2-encoded base64 content) — but is
// kept on the signature so callers preserve the wire-version
// awareness for future use.
func readCallResponse(resp *http.Response, _ int) (*CallResponse, error) {
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
	if rec := readReceiptHeader(resp.Header); rec != "" {
		parsed, err := parseReceipt(rec)
		if err == nil {
			out.Receipt = &parsed
		}
		// Malformed receipt headers are logged-and-ignored so a
		// server bookkeeping mistake doesn't block the agent; the
		// budget package re-derives spend from the request side via
		// the challenge amount when needed.
	}
	return out, nil
}

// readReceiptHeader picks the first non-empty receipt header value,
// preferring the v2-named lowercase form but falling back to the
// v1-named X-PAYMENT-RESPONSE for servers still on the legacy name.
func readReceiptHeader(h http.Header) string {
	if v := h.Get(HeaderPaymentResponseV2); v != "" {
		return v
	}
	return h.Get(HeaderPaymentResponse)
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
