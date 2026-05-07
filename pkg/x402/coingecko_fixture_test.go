package x402

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCoinGeckoFixtureParsesChallengeAndPaidResponse(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/coingecko-solana-trending-pools.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture liveFixtureWire
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if fixture.Service != "pro-api-coingecko-com-solana" {
		t.Fatalf("service = %q", fixture.Service)
	}
	if len(fixture.Exchanges) != 2 {
		t.Fatalf("exchanges = %d, want 2", len(fixture.Exchanges))
	}

	challengeExchange := fixture.Exchanges[0]
	if challengeExchange.ResponseStatus != 402 {
		t.Fatalf("first status = %d, want 402", challengeExchange.ResponseStatus)
	}
	header := getHeaderCanonical(challengeExchange.ResponseHeaders, HeaderPaymentRequiredV2)
	if header == "" {
		t.Fatal("CoinGecko fixture missing Payment-Required header")
	}
	challenge, err := parseChallengeV2Header(header)
	if err != nil {
		t.Fatalf("parseChallengeV2Header: %v", err)
	}
	requirement, err := challenge.PreferAndCheapest([]Network{NetworkSolana})
	if err != nil {
		t.Fatalf("PreferAndCheapest(solana): %v", err)
	}
	if requirement.Network != "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp" {
		t.Fatalf("network = %q", requirement.Network)
	}
	if requirement.AmountMicros != "10000" {
		t.Fatalf("amount = %q, want 10000 micro-USDC", requirement.AmountMicros)
	}
	if requirement.Asset != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" {
		t.Fatalf("asset = %q, want Solana USDC mint", requirement.Asset)
	}
	if requirement.PayTo == "" {
		t.Fatal("payTo empty")
	}

	paidExchange := fixture.Exchanges[1]
	if paidExchange.ResponseStatus != 200 {
		t.Fatalf("retry status = %d, want 200", paidExchange.ResponseStatus)
	}
	var response struct {
		Data []struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				Name              string `json:"name"`
				BaseTokenPriceUSD string `json:"base_token_price_usd"`
				ReserveUSD        string `json:"reserve_in_usd"`
			} `json:"attributes"`
			Relationships struct {
				BaseToken struct {
					Data struct {
						ID   string `json:"id"`
						Type string `json:"type"`
					} `json:"data"`
				} `json:"base_token"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(paidExchange.ResponseBody), &response); err != nil {
		t.Fatalf("decode paid response: %v", err)
	}
	if len(response.Data) == 0 {
		t.Fatal("paid response has no pools")
	}
	first := response.Data[0]
	if !strings.HasPrefix(first.ID, "solana_") || first.Type != "pool" {
		t.Fatalf("first pool identity = %s/%s", first.ID, first.Type)
	}
	if first.Attributes.Name == "" || first.Attributes.BaseTokenPriceUSD == "" || first.Attributes.ReserveUSD == "" {
		t.Fatalf("first pool attributes = %+v", first.Attributes)
	}
	if !strings.HasPrefix(first.Relationships.BaseToken.Data.ID, "solana_") || first.Relationships.BaseToken.Data.Type != "token" {
		t.Fatalf("first pool base token = %+v", first.Relationships.BaseToken.Data)
	}
}
