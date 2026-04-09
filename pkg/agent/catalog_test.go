package agent

import (
	"context"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestMemoryCatalogDiscoverFilters(t *testing.T) {
	t.Parallel()

	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	catalog := NewMemoryCatalog(nil)
	card, err := BuildAgentCard(owner, PublishParams{
		Name:    "Demo Seller",
		KeyName: "demo-seller",
		Summary: "Research and recipe offers",
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
				SKU:      "cookie-recipe-v1",
				Title:    "Cookie Recipe",
				Category: "recipe",
				Price: Price{
					Amount: "4",
					Asset:  "USDC",
				},
			},
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
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildAgentCard: %v", err)
	}
	if err := catalog.Publish(context.Background(), card); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	tests := []struct {
		name  string
		query DiscoverParams
		want  int
	}{
		{name: "all", query: DiscoverParams{}, want: 1},
		{name: "skill", query: DiscoverParams{Skill: "research"}, want: 1},
		{name: "category", query: DiscoverParams{Category: "recipe"}, want: 1},
		{name: "sku", query: DiscoverParams{SKU: "cookie-recipe-v1"}, want: 1},
		{name: "asset", query: DiscoverParams{Asset: "USDC"}, want: 1},
		{name: "query", query: DiscoverParams{Query: "cookie"}, want: 1},
		{name: "miss", query: DiscoverParams{Query: "pizza"}, want: 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := catalog.Discover(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("Discover(%+v) = %d, want %d", tt.query, len(got), tt.want)
			}
		})
	}
}

func TestMemoryCatalogRejectsOlderSeq(t *testing.T) {
	t.Parallel()

	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	catalog := NewMemoryCatalog(nil)
	first, err := BuildAgentCard(owner, PublishParams{
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
				SKU:   "research-brief-v1",
				Title: "Research Brief",
				Price: Price{
					Amount: "10",
					Asset:  "USDC",
				},
			},
		},
		Seq: 200,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildAgentCard first: %v", err)
	}
	if err := catalog.Publish(context.Background(), first); err != nil {
		t.Fatalf("Publish first: %v", err)
	}

	second, err := BuildAgentCard(owner, PublishParams{
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
				SKU:   "research-brief-v1",
				Title: "Research Brief",
				Price: Price{
					Amount: "10",
					Asset:  "USDC",
				},
			},
		},
		Seq: 100,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildAgentCard second: %v", err)
	}
	if err := catalog.Publish(context.Background(), second); err == nil {
		t.Fatal("expected older seq publish to fail")
	}
}
