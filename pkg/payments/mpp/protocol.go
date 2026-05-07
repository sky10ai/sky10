// Package mpp implements the Machine Payments Protocol HTTP Payment
// authentication wire shape for Solana charge payments.
package mpp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	HeaderWWWAuthenticate = "WWW-Authenticate"
	HeaderAuthorization   = "Authorization"
	HeaderPaymentReceipt  = "Payment-Receipt"

	paymentScheme = "Payment"
)

// Challenge is one MPP `WWW-Authenticate: Payment ...` challenge.
type Challenge struct {
	ID          string
	Realm       string
	Method      string
	Intent      string
	Request     string
	Expires     string
	Description string
	Digest      string
	Opaque      string
}

// ChargeRequest is the Solana charge request carried by Challenge.Request.
type ChargeRequest struct {
	Amount        string          `json:"amount"`
	Currency      string          `json:"currency"`
	Recipient     string          `json:"recipient,omitempty"`
	Description   string          `json:"description,omitempty"`
	ExternalID    string          `json:"externalId,omitempty"`
	MethodDetails json.RawMessage `json:"methodDetails,omitempty"`
}

// MethodDetails is the Solana-specific charge extension object.
type MethodDetails struct {
	Network         string  `json:"network,omitempty"`
	Decimals        *uint8  `json:"decimals,omitempty"`
	TokenProgram    string  `json:"tokenProgram,omitempty"`
	FeePayer        *bool   `json:"feePayer,omitempty"`
	FeePayerKey     string  `json:"feePayerKey,omitempty"`
	RecentBlockhash string  `json:"recentBlockhash,omitempty"`
	Splits          []Split `json:"splits,omitempty"`
}

// Split is an additional transfer in the same charge asset.
type Split struct {
	Recipient           string `json:"recipient"`
	Amount              string `json:"amount"`
	ATACreationRequired *bool  `json:"ataCreationRequired,omitempty"`
	Label               string `json:"label,omitempty"`
	Memo                string `json:"memo,omitempty"`
}

// Credential is the JSON object sent in an MPP Authorization header.
type Credential struct {
	Challenge ChallengeEcho `json:"challenge"`
	Source    string        `json:"source,omitempty"`
	Payload   any           `json:"payload"`
}

// ChallengeEcho mirrors the challenge fields the server needs to verify the
// credential target.
type ChallengeEcho struct {
	ID      string `json:"id"`
	Realm   string `json:"realm"`
	Method  string `json:"method"`
	Intent  string `json:"intent"`
	Request string `json:"request"`
	Expires string `json:"expires,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Opaque  string `json:"opaque,omitempty"`
}

// CredentialPayloadTransaction is the Solana pull-mode payload.
type CredentialPayloadTransaction struct {
	Type        string `json:"type"`
	Transaction string `json:"transaction"`
}

// Receipt is the decoded Payment-Receipt header.
type Receipt struct {
	Status      string `json:"status"`
	Method      string `json:"method"`
	Timestamp   string `json:"timestamp"`
	Reference   string `json:"reference"`
	ChallengeID string `json:"challengeId"`
}

// DecodeChargeRequest decodes the challenge request as a charge request plus
// Solana method details.
func (c Challenge) DecodeChargeRequest() (ChargeRequest, MethodDetails, error) {
	raw, err := base64URLDecode(c.Request)
	if err != nil {
		return ChargeRequest{}, MethodDetails{}, err
	}
	var req ChargeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return ChargeRequest{}, MethodDetails{}, fmt.Errorf("decode MPP charge request: %w", err)
	}
	var details MethodDetails
	if len(req.MethodDetails) > 0 {
		if err := json.Unmarshal(req.MethodDetails, &details); err != nil {
			return ChargeRequest{}, MethodDetails{}, fmt.Errorf("decode MPP methodDetails: %w", err)
		}
	}
	return req, details, nil
}

func (c Challenge) echo() ChallengeEcho {
	return ChallengeEcho{
		ID:      c.ID,
		Realm:   c.Realm,
		Method:  c.Method,
		Intent:  c.Intent,
		Request: c.Request,
		Expires: c.Expires,
		Digest:  c.Digest,
		Opaque:  c.Opaque,
	}
}

// ParseChallenges extracts valid Payment challenges from an HTTP response
// header map. Non-MPP authentication schemes are ignored.
func ParseChallenges(headers http.Header) ([]Challenge, error) {
	var out []Challenge
	var parseErr error
	for _, value := range headers.Values(HeaderWWWAuthenticate) {
		for _, chunk := range splitPaymentChallengeValues(value) {
			challenge, err := ParseWWWAuthenticate(chunk)
			if err != nil {
				parseErr = err
				continue
			}
			out = append(out, challenge)
		}
	}
	if len(out) == 0 && parseErr != nil {
		return nil, parseErr
	}
	return out, nil
}

// ParseWWWAuthenticate parses one `Payment ...` challenge value.
func ParseWWWAuthenticate(header string) (Challenge, error) {
	rest := strings.TrimSpace(header)
	if len(rest) < len(paymentScheme) || !strings.EqualFold(rest[:len(paymentScheme)], paymentScheme) {
		return Challenge{}, errors.New("MPP challenge missing Payment scheme")
	}
	if len(rest) > len(paymentScheme) && !isHTTPWhitespace(rest[len(paymentScheme)]) {
		return Challenge{}, errors.New("MPP challenge missing Payment scheme")
	}
	rest = strings.TrimSpace(rest[len(paymentScheme):])
	params, err := parseAuthParams(rest)
	if err != nil {
		return Challenge{}, err
	}
	challenge := Challenge{
		ID:          params["id"],
		Realm:       params["realm"],
		Method:      strings.ToLower(params["method"]),
		Intent:      strings.ToLower(params["intent"]),
		Request:     params["request"],
		Expires:     params["expires"],
		Description: params["description"],
		Digest:      params["digest"],
		Opaque:      params["opaque"],
	}
	if challenge.ID == "" || challenge.Realm == "" || challenge.Method == "" || challenge.Intent == "" || challenge.Request == "" {
		return Challenge{}, errors.New("MPP challenge missing required parameter")
	}
	raw, err := base64URLDecode(challenge.Request)
	if err != nil {
		return Challenge{}, fmt.Errorf("decode MPP request: %w", err)
	}
	if !json.Valid(raw) {
		return Challenge{}, errors.New("MPP request is not valid JSON")
	}
	return challenge, nil
}

// FormatAuthorization builds the MPP Authorization header value.
func FormatAuthorization(credential Credential) (string, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return "", fmt.Errorf("encode MPP credential: %w", err)
	}
	return paymentScheme + " " + base64.RawURLEncoding.EncodeToString(raw), nil
}

// ParseReceipt decodes a Payment-Receipt header.
func ParseReceipt(value string) (*Receipt, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	raw, err := base64URLDecode(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode MPP receipt: %w", err)
	}
	var receipt Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, fmt.Errorf("decode MPP receipt JSON: %w", err)
	}
	return &receipt, nil
}

func base64URLDecode(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, "=")
	value = strings.NewReplacer("+", "-", "/", "_").Replace(value)
	return base64.RawURLEncoding.DecodeString(value)
}

func base64URLEncodeJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func splitPaymentChallengeValues(header string) []string {
	bytes := []byte(header)
	var starts []int
	inQuote := false
	escaped := false
	for i := 0; i < len(bytes); i++ {
		b := bytes[i]
		if inQuote {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inQuote = false
			}
			continue
		}
		if b == '"' {
			inQuote = true
			continue
		}
		if isPaymentSchemeStart(bytes, i) {
			starts = append(starts, i)
		}
	}
	chunks := make([]string, 0, len(starts))
	for i, start := range starts {
		end := len(header)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		chunk := strings.TrimSpace(strings.TrimRight(header[start:end], ","))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

func isPaymentSchemeStart(bytes []byte, index int) bool {
	end := index + len(paymentScheme)
	if end >= len(bytes) {
		return false
	}
	if !strings.EqualFold(string(bytes[index:end]), paymentScheme) {
		return false
	}
	if !isHTTPWhitespace(bytes[end]) {
		return false
	}
	prev := index
	for prev > 0 && isHTTPWhitespace(bytes[prev-1]) {
		prev--
	}
	return prev == 0 || bytes[prev-1] == ','
}

func isHTTPWhitespace(b byte) bool {
	return b == ' ' || b == '\t'
}

func parseAuthParams(input string) (map[string]string, error) {
	params := make(map[string]string)
	i := 0
	for i < len(input) {
		for i < len(input) && (input[i] == ',' || isHTTPWhitespace(input[i])) {
			i++
		}
		if i >= len(input) {
			break
		}
		keyStart := i
		for i < len(input) && input[i] != '=' && input[i] != ',' && !isHTTPWhitespace(input[i]) {
			i++
		}
		if i >= len(input) || input[i] != '=' {
			return nil, errors.New("invalid auth parameter")
		}
		key := input[keyStart:i]
		i++
		if i >= len(input) {
			return nil, errors.New("missing auth parameter value")
		}
		var value string
		if input[i] == '"' {
			i++
			var b strings.Builder
			for i < len(input) {
				if input[i] == '\\' && i+1 < len(input) {
					i++
					b.WriteByte(input[i])
					i++
					continue
				}
				if input[i] == '"' {
					i++
					break
				}
				b.WriteByte(input[i])
				i++
			}
			value = b.String()
		} else {
			valueStart := i
			for i < len(input) && input[i] != ',' && !isHTTPWhitespace(input[i]) {
				i++
			}
			value = input[valueStart:i]
		}
		if _, ok := params[key]; ok {
			return nil, fmt.Errorf("duplicate auth parameter %q", key)
		}
		params[key] = value
	}
	return params, nil
}
