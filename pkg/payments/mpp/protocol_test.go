package mpp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

func TestParseWWWAuthenticateSolanaCharge(t *testing.T) {
	t.Parallel()
	request, err := base64URLEncodeJSON(map[string]any{
		"amount":    "1000",
		"currency":  "USDC",
		"recipient": "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM",
		"methodDetails": map[string]any{
			"network":  "mainnet-beta",
			"decimals": 6,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	challenge, err := ParseWWWAuthenticate(`Payment id="abc", realm="api", method="solana", intent="charge", request="` + request + `"`)
	if err != nil {
		t.Fatalf("ParseWWWAuthenticate: %v", err)
	}
	if challenge.ID != "abc" || challenge.Method != "solana" || challenge.Intent != "charge" {
		t.Fatalf("challenge = %+v", challenge)
	}
	req, details, err := challenge.DecodeChargeRequest()
	if err != nil {
		t.Fatalf("DecodeChargeRequest: %v", err)
	}
	if req.Amount != "1000" || req.Currency != "USDC" {
		t.Fatalf("request = %+v", req)
	}
	if details.Network != "mainnet-beta" || details.Decimals == nil || *details.Decimals != 6 {
		t.Fatalf("details = %+v", details)
	}
}

func TestParseChallengesIgnoresNonPaymentSchemes(t *testing.T) {
	t.Parallel()
	request, err := base64URLEncodeJSON(map[string]any{"amount": "1", "currency": "SOL"})
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Add(HeaderWWWAuthenticate, `Bearer realm="api", Payment id="abc", realm="api", method="solana", intent="charge", request="`+request+`"`)

	challenges, err := ParseChallenges(headers)
	if err != nil {
		t.Fatalf("ParseChallenges: %v", err)
	}
	if len(challenges) != 1 || challenges[0].ID != "abc" {
		t.Fatalf("challenges = %+v", challenges)
	}
}

func TestFormatAuthorizationEncodesCredential(t *testing.T) {
	t.Parallel()
	header, err := FormatAuthorization(Credential{
		Challenge: ChallengeEcho{
			ID:      "abc",
			Realm:   "api",
			Method:  "solana",
			Intent:  "charge",
			Request: "eyJhbW91bnQiOiIxIn0",
		},
		Payload: CredentialPayloadTransaction{
			Type:        "transaction",
			Transaction: "base64tx",
		},
	})
	if err != nil {
		t.Fatalf("FormatAuthorization: %v", err)
	}
	const prefix = "Payment "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		t.Fatalf("header = %q", header)
	}
	raw, err := base64.RawURLEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		t.Fatalf("decode authorization: %v", err)
	}
	var decoded struct {
		Challenge ChallengeEcho `json:"challenge"`
		Payload   struct {
			Type        string `json:"type"`
			Transaction string `json:"transaction"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal credential: %v", err)
	}
	if decoded.Challenge.ID != "abc" || decoded.Payload.Type != "transaction" || decoded.Payload.Transaction != "base64tx" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
