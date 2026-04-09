package agent

import (
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestBuildAgentCardSignsAndVerifies(t *testing.T) {
	t.Parallel()

	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	card, err := BuildAgentCard(owner, PublishParams{
		Name:    "Demo Seller",
		KeyName: "demo-seller",
		Skills: []SkillSpec{
			{
				ID:   "research",
				Name: "Research",
				Price: &Price{
					Amount: "10",
					Asset:  "USDC",
				},
			},
		},
		Offers: []Offer{
			{
				SKU:      "research-brief-v1",
				Title:    "Research Brief",
				Category: "research",
				Price: Price{
					Amount: "10",
					Asset:  "USDC",
				},
			},
		},
		Payment: PaymentTerms{
			Chain:   "solana",
			Asset:   "USDC",
			Address: "demo-wallet",
		},
	}, time.Unix(1_770_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("BuildAgentCard: %v", err)
	}

	if card.AgentID == "" || card.AgentAddress == "" {
		t.Fatalf("card identity missing: %+v", card)
	}
	if card.Owner != owner.Address() {
		t.Fatalf("owner = %s, want %s", card.Owner, owner.Address())
	}
	if err := card.Verify(time.Unix(1_770_000_010, 0).UTC()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestBuildAgentCardRequiresContent(t *testing.T) {
	t.Parallel()

	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	_, err = BuildAgentCard(owner, PublishParams{
		Name:    "Empty Seller",
		KeyName: "empty-seller",
	}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for empty card")
	}
}
