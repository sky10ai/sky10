package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DemoBuyParams triggers the built-in marketplace demo flow.
type DemoBuyParams struct {
	AgentAddress string `json:"agent_address,omitempty"`
	OfferSKU     string `json:"offer_sku"`
	Request      string `json:"request,omitempty"`
}

// DemoBuyResult is the end-to-end result for the local demo checkout flow.
type DemoBuyResult struct {
	QuoteID        string `json:"quote_id"`
	ReceiptID      string `json:"receipt_id"`
	AgentID        string `json:"agent_id"`
	AgentAddress   string `json:"agent_address"`
	AgentName      string `json:"agent_name"`
	OfferSKU       string `json:"offer_sku"`
	OfferTitle     string `json:"offer_title"`
	Amount         string `json:"amount"`
	Asset          string `json:"asset"`
	PaymentStatus  string `json:"payment_status"`
	Fulfillment    string `json:"fulfillment"`
	ResultMarkdown string `json:"result_markdown"`
	FulfilledAt    int64  `json:"fulfilled_at"`
}

// DemoMarketplace is the local fulfillment engine for the built-in seller.
type DemoMarketplace struct {
	catalog Catalog
}

// NewDemoMarketplace creates a local demo marketplace engine.
func NewDemoMarketplace(catalog Catalog) *DemoMarketplace {
	return &DemoMarketplace{catalog: catalog}
}

// Buy simulates discovery, quote, payment, and fulfillment for demo offers.
func (m *DemoMarketplace) Buy(ctx context.Context, p DemoBuyParams) (*DemoBuyResult, error) {
	if m == nil || m.catalog == nil {
		return nil, fmt.Errorf("demo marketplace is unavailable")
	}
	if strings.TrimSpace(p.OfferSKU) == "" {
		return nil, fmt.Errorf("offer_sku is required")
	}

	cards, err := m.catalog.Discover(ctx, DiscoverParams{SKU: p.OfferSKU, Limit: 20})
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("offer %q not found", p.OfferSKU)
	}

	var card *AgentCard
	var offer *Offer
	for i := range cards {
		if p.AgentAddress != "" && cards[i].AgentAddress != p.AgentAddress {
			continue
		}
		for j := range cards[i].Offers {
			if cards[i].Offers[j].SKU == p.OfferSKU {
				card = &cards[i]
				offer = &cards[i].Offers[j]
				break
			}
		}
		if card != nil {
			break
		}
	}
	if card == nil || offer == nil {
		return nil, fmt.Errorf("offer %q not found for selected agent", p.OfferSKU)
	}

	now := time.Now().UTC()
	return &DemoBuyResult{
		QuoteID:        "quote-" + uuid.NewString(),
		ReceiptID:      "receipt-" + uuid.NewString(),
		AgentID:        card.AgentID,
		AgentAddress:   card.AgentAddress,
		AgentName:      card.Name,
		OfferSKU:       offer.SKU,
		OfferTitle:     offer.Title,
		Amount:         offer.Price.Amount,
		Asset:          offer.Price.Asset,
		PaymentStatus:  "simulated_paid",
		Fulfillment:    firstNonEmpty(offer.Fulfillment, "digital"),
		ResultMarkdown: demoFulfillmentMarkdown(offer.SKU, p.Request),
		FulfilledAt:    now.Unix(),
	}, nil
}

func demoFulfillmentMarkdown(sku, request string) string {
	request = strings.TrimSpace(request)
	switch sku {
	case "cookie-recipe-v1":
		return strings.TrimSpace(`
# Cookie Recipe Pack

## Brown Butter Chocolate Chip
- 225g flour
- 170g browned butter
- 150g brown sugar
- 100g white sugar
- 1 egg + 1 yolk
- 170g dark chocolate

Bake at 350F for 11-13 minutes. Rest dough 30 minutes for better texture.

## Chewy Oatmeal Raisin
- 180g flour
- 120g rolled oats
- 170g butter
- 160g brown sugar
- cinnamon + vanilla

Bake at 350F for 10-12 minutes.

## Snickerdoodles
- 250g flour
- 170g butter
- 150g sugar
- 1 egg
- cream of tartar + cinnamon sugar coating

Roll in cinnamon sugar and bake at 375F for 9-10 minutes.
`)
	case "research-brief-v1":
		if request == "" {
			request = "recent trends in local-first agent commerce"
		}
		return fmt.Sprintf(strings.TrimSpace(`
# Research Brief

Topic: %s

## Executive Summary
The space is moving toward broker-style agents that buy services on behalf of users while keeping discovery and payment mostly invisible.

## Findings
1. Trust improves when discovery, quote, payment, and fulfillment are traceable.
2. Lightweight offer catalogs outperform full marketplace UX for early demos.
3. Public listings matter for visibility, but users still want task-first interaction.

## Recommendation
Lead with brokered task execution and keep the listing browser as an observability and demo surface.
`), request)
	case "summarize-doc-v1":
		if request == "" {
			request = "No source text was provided, so this demo generated a default executive summary."
		}
		return fmt.Sprintf(strings.TrimSpace(`
# Executive Summary

## Input
%s

## Summary
The document argues for a narrow initial product scope, visible operational tooling, and iterative hardening before broader rollout.

## Action Items
1. Keep the first demo path simple.
2. Prove one transaction flow end to end.
3. Add payment and transport reliability after the UX is clear.
`), request)
	case "product-compare-v1":
		if request == "" {
			request = "Compare three options for an agent marketplace MVP."
		}
		return fmt.Sprintf(strings.TrimSpace(`
# Product Comparison

Goal: %s

## Best Fit
Option A is the best fit for an MVP because it minimizes moving parts while still demonstrating real buyer intent and fulfillment.

## Why
- lowest implementation risk
- easiest to demo live
- preserves room for later payment and reputation layers

## Tradeoff
You give up flexibility in the first version, but gain a much more reliable story.
`), request)
	default:
		return "# Demo Result\n\nThis offer completed successfully."
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
