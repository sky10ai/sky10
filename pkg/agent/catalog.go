package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// DiscoverParams is the structured filter for public agent discovery.
type DiscoverParams struct {
	Skill    string `json:"skill,omitempty"`
	Category string `json:"category,omitempty"`
	SKU      string `json:"sku,omitempty"`
	Asset    string `json:"asset,omitempty"`
	Location string `json:"location,omitempty"`
	Query    string `json:"query,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// Catalog stores signed public agent cards.
type Catalog interface {
	Publish(ctx context.Context, card *AgentCard) error
	Discover(ctx context.Context, query DiscoverParams) ([]AgentCard, error)
}

// MemoryCatalog keeps published cards in memory for demo and testing.
type MemoryCatalog struct {
	logger *slog.Logger

	mu    sync.RWMutex
	cards map[string]*AgentCard
}

// NewMemoryCatalog creates an in-memory public card catalog.
func NewMemoryCatalog(logger *slog.Logger) *MemoryCatalog {
	return &MemoryCatalog{
		logger: componentLogger(logger),
		cards:  make(map[string]*AgentCard),
	}
}

// Publish verifies and stores the latest card for an agent.
func (c *MemoryCatalog) Publish(_ context.Context, card *AgentCard) error {
	if card == nil {
		return fmt.Errorf("card is required")
	}
	if err := card.Verify(time.Now().UTC()); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	existing := c.cards[card.AgentAddress]
	if existing != nil && existing.Seq > card.Seq {
		return fmt.Errorf("existing card has newer seq")
	}
	cp := *card
	c.cards[card.AgentAddress] = &cp
	c.logger.Info("agent card published", "agent_id", card.AgentID, "name", card.Name, "offers", len(card.Offers), "skills", len(card.Skills))
	return nil
}

// Discover returns cards matching the query.
func (c *MemoryCatalog) Discover(_ context.Context, query DiscoverParams) ([]AgentCard, error) {
	query = normalizeDiscoverParams(query)

	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now().UTC().Unix()
	out := make([]AgentCard, 0, len(c.cards))
	for _, card := range c.cards {
		if card.ExpiresAt != 0 && card.ExpiresAt < now {
			continue
		}
		if !cardMatches(card, query) {
			continue
		}
		out = append(out, *card)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].PublishedAt != out[j].PublishedAt {
			return out[i].PublishedAt > out[j].PublishedAt
		}
		return out[i].Name < out[j].Name
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func normalizeDiscoverParams(query DiscoverParams) DiscoverParams {
	query.Skill = strings.TrimSpace(strings.ToLower(query.Skill))
	query.Category = strings.TrimSpace(strings.ToLower(query.Category))
	query.SKU = strings.TrimSpace(strings.ToLower(query.SKU))
	query.Asset = strings.TrimSpace(strings.ToUpper(query.Asset))
	query.Location = strings.TrimSpace(strings.ToLower(query.Location))
	query.Query = strings.TrimSpace(strings.ToLower(query.Query))
	if query.Limit < 0 {
		query.Limit = 0
	}
	return query
}

func cardMatches(card *AgentCard, query DiscoverParams) bool {
	if card == nil {
		return false
	}
	if query.Skill != "" && !cardHasSkill(card, query.Skill) {
		return false
	}
	if query.Category != "" && !cardHasCategory(card, query.Category) {
		return false
	}
	if query.SKU != "" && !cardHasSKU(card, query.SKU) {
		return false
	}
	if query.Asset != "" && !cardHasAsset(card, query.Asset) {
		return false
	}
	if query.Location != "" && !cardHasLocation(card, query.Location) {
		return false
	}
	if query.Query != "" && !cardHasQuery(card, query.Query) {
		return false
	}
	return true
}

func cardHasSkill(card *AgentCard, skill string) bool {
	for _, s := range card.Skills {
		if strings.EqualFold(s.ID, skill) || strings.EqualFold(s.Name, skill) {
			return true
		}
		for _, tag := range s.Tags {
			if strings.EqualFold(tag, skill) {
				return true
			}
		}
	}
	return false
}

func cardHasCategory(card *AgentCard, category string) bool {
	for _, offer := range card.Offers {
		if strings.EqualFold(offer.Category, category) {
			return true
		}
	}
	return false
}

func cardHasSKU(card *AgentCard, sku string) bool {
	for _, offer := range card.Offers {
		if strings.EqualFold(offer.SKU, sku) {
			return true
		}
	}
	return false
}

func cardHasAsset(card *AgentCard, asset string) bool {
	if strings.EqualFold(card.Payment.Asset, asset) {
		return true
	}
	for _, offer := range card.Offers {
		if strings.EqualFold(offer.Price.Asset, asset) {
			return true
		}
	}
	for _, skill := range card.Skills {
		if skill.Price != nil && strings.EqualFold(skill.Price.Asset, asset) {
			return true
		}
	}
	return false
}

func cardHasLocation(card *AgentCard, location string) bool {
	for _, offer := range card.Offers {
		if containsFold(offer.Location, location) {
			return true
		}
	}
	return false
}

func cardHasQuery(card *AgentCard, query string) bool {
	fields := []string{
		card.Name,
		card.Summary,
		card.Payment.Asset,
		card.Payment.Chain,
	}
	for _, field := range fields {
		if containsFold(field, query) {
			return true
		}
	}
	for _, skill := range card.Skills {
		if containsFold(skill.ID, query) || containsFold(skill.Name, query) || containsFold(skill.Description, query) {
			return true
		}
		for _, tag := range skill.Tags {
			if containsFold(tag, query) {
				return true
			}
		}
	}
	for _, offer := range card.Offers {
		if containsFold(offer.SKU, query) || containsFold(offer.Title, query) || containsFold(offer.Summary, query) || containsFold(offer.Category, query) || containsFold(offer.Location, query) {
			return true
		}
		for _, tag := range offer.Tags {
			if containsFold(tag, query) {
				return true
			}
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
