package mpp

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
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

func TestParsePaySHMPPChallengeFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		path       string
		amount     string
		priceUSD   float64
		scale      int
		unit       string
		splitCount int
	}{
		{
			name:       "vision",
			path:       "v1/images:annotate",
			amount:     "1500",
			priceUSD:   1.5,
			scale:      1000,
			unit:       "requests",
			splitCount: 2,
		},
		{
			name:     "texttospeech",
			path:     "v1/text:synthesize",
			amount:   "30",
			priceUSD: 30.0,
			scale:    1000000,
			unit:     "characters",
		},
		{
			name:       "airquality",
			path:       "v1/currentConditions:lookup",
			amount:     "1000",
			priceUSD:   0.001,
			scale:      1,
			unit:       "requests",
			splitCount: 2,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fixture := loadPaySHMPPFixture(t, "pay-sh-google-"+tt.name+"-402.json")
			if fixture.Response.Status != http.StatusPaymentRequired || fixture.Response.Body.Payment.Protocol != "mpp" {
				t.Fatalf("fixture response = %+v", fixture.Response)
			}
			if fixture.Response.Body.Endpoint.Method != http.MethodPost || fixture.Response.Body.Endpoint.Path != tt.path {
				t.Fatalf("endpoint = %+v", fixture.Response.Body.Endpoint)
			}
			if fixture.Response.Body.Payment.Challenges != 3 {
				t.Fatalf("advertised challenges = %d, want 3", fixture.Response.Body.Payment.Challenges)
			}
			if len(fixture.Response.Body.Pricing.Dimensions) != 1 {
				t.Fatalf("pricing dimensions = %+v", fixture.Response.Body.Pricing.Dimensions)
			}
			dim := fixture.Response.Body.Pricing.Dimensions[0]
			if math.Abs(dim.PriceUSD-tt.priceUSD) > 0.0000001 || dim.Scale != tt.scale || dim.Unit != tt.unit {
				t.Fatalf("pricing dimension = %+v", dim)
			}

			challenges, err := ParseChallenges(fixture.Response.Headers)
			if err != nil {
				t.Fatalf("ParseChallenges: %v", err)
			}
			if len(challenges) != 1 {
				t.Fatalf("challenges = %d, want 1 captured challenge", len(challenges))
			}
			if challenges[0].Method != "solana" || challenges[0].Intent != "charge" {
				t.Fatalf("challenge = %+v", challenges[0])
			}
			req, details, err := challenges[0].DecodeChargeRequest()
			if err != nil {
				t.Fatalf("DecodeChargeRequest: %v", err)
			}
			if req.Amount != tt.amount || req.Currency != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" {
				t.Fatalf("charge request = %+v", req)
			}
			if req.Recipient == "" || req.Description == "" {
				t.Fatalf("charge request missing recipient/description: %+v", req)
			}
			if details.Network != "mainnet" || details.Decimals == nil || *details.Decimals != 6 {
				t.Fatalf("method details = %+v", details)
			}
			if details.FeePayer == nil || !*details.FeePayer || details.FeePayerKey == "" || details.RecentBlockhash == "" {
				t.Fatalf("fee payer/blockhash details = %+v", details)
			}
			if details.TokenProgram != "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA" {
				t.Fatalf("token program = %q", details.TokenProgram)
			}
			if len(details.Splits) != tt.splitCount {
				t.Fatalf("splits = %+v, want %d", details.Splits, tt.splitCount)
			}
			if tt.splitCount > 0 && (details.Splits[0].Amount != "250" || details.Splits[1].Amount != "1") {
				t.Fatalf("splits = %+v", details.Splits)
			}
		})
	}
}

type paySHMPPFixture struct {
	Response struct {
		Status  int         `json:"status"`
		Headers http.Header `json:"headers"`
		Body    struct {
			Endpoint struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"endpoint"`
			Error   string `json:"error"`
			Payment struct {
				Protocol   string `json:"protocol"`
				Challenges int    `json:"challenges"`
			} `json:"payment"`
			Pricing struct {
				Dimensions []struct {
					Direction string  `json:"direction"`
					PriceUSD  float64 `json:"price_usd"`
					Scale     int     `json:"scale"`
					Unit      string  `json:"unit"`
				} `json:"dimensions"`
			} `json:"pricing"`
		} `json:"body"`
	} `json:"response"`
}

func loadPaySHMPPFixture(t *testing.T, name string) paySHMPPFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var fixture paySHMPPFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return fixture
}
