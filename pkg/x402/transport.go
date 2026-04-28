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

// HeaderPayment is the request header carrying the signed
// authorization on retry.
const HeaderPayment = "X-PAYMENT"

// HeaderPaymentResponse is the response header the service sets
// containing the settlement receipt for the just-completed call.
const HeaderPaymentResponse = "X-PAYMENT-RESPONSE"

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
// supplied Signer is required.
func NewTransport(signer Signer) *Transport {
	return &Transport{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
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

// parseChallenge reads the 402 response body and decodes it into a
// PaymentChallenge. The canonical x402 server emits the challenge as
// a JSON body with `accepts` listing each acceptable scheme/network
// combination. Servers must offer at least one option; an empty list
// is treated as a malformed challenge.
func parseChallenge(resp *http.Response) (PaymentChallenge, error) {
	var c PaymentChallenge
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
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
		if strings.TrimSpace(req.MaxAmountRequired) == "" {
			return c, errors.New("challenge requirement missing maxAmountRequired")
		}
	}
	return c, nil
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
	if rec := resp.Header.Get(HeaderPaymentResponse); rec != "" {
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

func parsePaymentReceipt(value string) (PaymentReceipt, error) {
	var r PaymentReceipt
	if err := json.Unmarshal([]byte(value), &r); err != nil {
		return r, err
	}
	return r, nil
}
