package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

// SeedDemoMarketplace publishes the built-in seller used by the UI demo.
func SeedDemoMarketplace(ctx context.Context, owner *skykey.Key, catalog Catalog) (*AgentCard, error) {
	if catalog == nil {
		return nil, fmt.Errorf("catalog is required")
	}

	researchSchema, _ := json.Marshal(map[string]string{
		"query": "string",
		"depth": "brief|deep",
	})
	summarySchema, _ := json.Marshal(map[string]string{
		"document": "string",
		"tone":     "neutral|executive",
	})
	compareSchema, _ := json.Marshal(map[string]string{
		"options": "string[]",
		"goal":    "string",
	})

	card, err := BuildAgentCard(owner, PublishParams{
		Name:    "Market Demo Seller",
		KeyName: "market-demo-seller",
		Summary: "Built-in demo seller for discovery, offers, and later brokered purchases.",
		Skills: []SkillSpec{
			{
				ID:          "research",
				Name:        "Web Research",
				Description: "Research a topic and return a sourced brief.",
				InputSchema: researchSchema,
				Price: &Price{
					Amount: "12",
					Asset:  "USDC",
					Per:    "call",
				},
				Tags: []string{"research", "brief", "sources"},
			},
			{
				ID:          "summarize",
				Name:        "Summarization",
				Description: "Summarize long input into a concise executive brief.",
				InputSchema: summarySchema,
				Price: &Price{
					Amount: "6",
					Asset:  "USDC",
					Per:    "call",
				},
				Tags: []string{"summary", "document", "brief"},
			},
			{
				ID:          "compare-products",
				Name:        "Product Comparison",
				Description: "Compare options and recommend the best fit for a goal.",
				InputSchema: compareSchema,
				Price: &Price{
					Amount: "9",
					Asset:  "USDC",
					Per:    "call",
				},
				Tags: []string{"comparison", "shopping", "decision"},
			},
		},
		Offers: []Offer{
			{
				SKU:         "cookie-recipe-v1",
				Title:       "Cookie Recipe Pack",
				Summary:     "Three crowd-pleasing cookie recipes with ingredient swaps and bake notes.",
				Category:    "recipe",
				Fulfillment: "digital",
				Price: Price{
					Amount: "4",
					Asset:  "USDC",
					Per:    "purchase",
				},
				Tags: []string{"cookies", "recipe", "dessert"},
			},
			{
				SKU:         "research-brief-v1",
				Title:       "Research Brief",
				Summary:     "A sourced briefing memo on a topic you specify.",
				Category:    "research",
				Fulfillment: "digital",
				Price: Price{
					Amount: "12",
					Asset:  "USDC",
					Per:    "purchase",
				},
				Tags: []string{"research", "sources", "brief"},
			},
			{
				SKU:         "summarize-doc-v1",
				Title:       "Document Summary",
				Summary:     "Fast summary of a long document with action items.",
				Category:    "summarization",
				Fulfillment: "digital",
				Price: Price{
					Amount: "6",
					Asset:  "USDC",
					Per:    "purchase",
				},
				Tags: []string{"summary", "document", "notes"},
			},
			{
				SKU:         "product-compare-v1",
				Title:       "Product Comparison",
				Summary:     "Decision memo comparing products against your criteria.",
				Category:    "comparison",
				Fulfillment: "digital",
				Price: Price{
					Amount: "9",
					Asset:  "USDC",
					Per:    "purchase",
				},
				Tags: []string{"compare", "products", "shopping"},
			},
		},
		Payment: PaymentTerms{
			Chain:   "solana",
			Asset:   "USDC",
			Address: "demo-treasury",
		},
		Transport: TransportInfo{
			Preferred: "skylink",
			Fallback:  []string{"nostr"},
		},
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	if err := catalog.Publish(ctx, card); err != nil {
		return nil, err
	}
	return card, nil
}
