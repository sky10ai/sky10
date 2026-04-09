package agent

import (
	"context"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestDemoMarketplaceBuy(t *testing.T) {
	t.Parallel()

	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	catalog := NewMemoryCatalog(nil)
	card, err := BuildAgentCard(owner, PublishParams{
		Name:    "Demo Seller",
		KeyName: "demo-seller",
		Offers: []Offer{
			{
				SKU:      "research-brief-v1",
				Title:    "Research Brief",
				Category: "research",
				Price: Price{
					Amount: "12",
					Asset:  "USDC",
				},
			},
		},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildAgentCard: %v", err)
	}
	if err := catalog.Publish(context.Background(), card); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	market := NewDemoMarketplace(catalog)
	result, err := market.Buy(context.Background(), DemoBuyParams{
		AgentAddress: card.AgentAddress,
		OfferSKU:     "research-brief-v1",
		Request:      "compare the current local-first agent products",
	})
	if err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if result.AgentName != "Demo Seller" {
		t.Fatalf("AgentName = %s, want Demo Seller", result.AgentName)
	}
	if result.PaymentStatus != "simulated_paid" {
		t.Fatalf("PaymentStatus = %s, want simulated_paid", result.PaymentStatus)
	}
	if result.QuoteID == "" || result.ReceiptID == "" {
		t.Fatal("expected quote and receipt ids")
	}
	if result.ResultMarkdown == "" {
		t.Fatal("expected result markdown")
	}
}
