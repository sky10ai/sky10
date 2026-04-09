package agent

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

const defaultCardTTL = 24 * time.Hour

// Price is the compact commerce price model for demo listings.
type Price struct {
	Amount string `json:"amount"`
	Asset  string `json:"asset"`
	Per    string `json:"per,omitempty"`
}

// SkillSpec is one public capability an agent exposes.
type SkillSpec struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Price       *Price          `json:"price,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
}

// Offer is one demo marketplace item sold by an agent.
type Offer struct {
	SKU         string   `json:"sku"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"`
	Category    string   `json:"category,omitempty"`
	Fulfillment string   `json:"fulfillment,omitempty"`
	Location    string   `json:"location,omitempty"`
	Price       Price    `json:"price"`
	Tags        []string `json:"tags,omitempty"`
}

// PaymentTerms describes how a seller wants to get paid.
type PaymentTerms struct {
	Chain   string `json:"chain,omitempty"`
	Asset   string `json:"asset,omitempty"`
	Address string `json:"address,omitempty"`
}

// TransportInfo describes how the agent expects traffic to reach it.
type TransportInfo struct {
	Preferred string   `json:"preferred,omitempty"`
	Fallback  []string `json:"fallback,omitempty"`
}

// AgentCard is the signed public record for discovery.
type AgentCard struct {
	AgentID      string        `json:"agent_id"`
	AgentAddress string        `json:"agent_address"`
	Owner        string        `json:"owner"`
	OwnerCert    string        `json:"owner_cert,omitempty"`
	Name         string        `json:"name"`
	Summary      string        `json:"summary,omitempty"`
	Skills       []SkillSpec   `json:"skills,omitempty"`
	Offers       []Offer       `json:"offers,omitempty"`
	Payment      PaymentTerms  `json:"payment,omitempty"`
	Transport    TransportInfo `json:"transport,omitempty"`
	Seq          int64         `json:"seq"`
	PublishedAt  int64         `json:"published_at"`
	ExpiresAt    int64         `json:"expires_at,omitempty"`
	Signature    string        `json:"signature"`
}

type unsignedAgentCard struct {
	AgentID      string        `json:"agent_id"`
	AgentAddress string        `json:"agent_address"`
	Owner        string        `json:"owner"`
	OwnerCert    string        `json:"owner_cert,omitempty"`
	Name         string        `json:"name"`
	Summary      string        `json:"summary,omitempty"`
	Skills       []SkillSpec   `json:"skills,omitempty"`
	Offers       []Offer       `json:"offers,omitempty"`
	Payment      PaymentTerms  `json:"payment,omitempty"`
	Transport    TransportInfo `json:"transport,omitempty"`
	Seq          int64         `json:"seq"`
	PublishedAt  int64         `json:"published_at"`
	ExpiresAt    int64         `json:"expires_at,omitempty"`
}

// PublishParams is the input to agent.publish.
type PublishParams struct {
	Name       string        `json:"name"`
	KeyName    string        `json:"key_name,omitempty"`
	Summary    string        `json:"summary,omitempty"`
	Skills     []SkillSpec   `json:"skills,omitempty"`
	Offers     []Offer       `json:"offers,omitempty"`
	Payment    PaymentTerms  `json:"payment,omitempty"`
	Transport  TransportInfo `json:"transport,omitempty"`
	Seq        int64         `json:"seq,omitempty"`
	TTLSeconds int64         `json:"ttl_seconds,omitempty"`
}

// EffectiveKeyName returns the stable identity slug for publishing.
func (p PublishParams) EffectiveKeyName() string {
	if s := normalizeAgentKeyName(p.KeyName); s != "" {
		return s
	}
	return normalizeAgentKeyName(p.Name)
}

// BuildAgentCard derives the agent key, signs the public card, and returns it.
func BuildAgentCard(owner *skykey.Key, p PublishParams, now time.Time) (*AgentCard, error) {
	if owner == nil || !owner.IsPrivate() {
		return nil, fmt.Errorf("owner key is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if len(p.Skills) == 0 && len(p.Offers) == 0 {
		return nil, fmt.Errorf("at least one skill or offer is required")
	}

	agentID, agentKey, err := GenerateAgentID(owner, p.EffectiveKeyName())
	if err != nil {
		return nil, fmt.Errorf("generating agent identity: %w", err)
	}

	now = now.UTC()
	ttl := defaultCardTTL
	if p.TTLSeconds > 0 {
		ttl = time.Duration(p.TTLSeconds) * time.Second
	}

	card := &AgentCard{
		AgentID:      agentID,
		AgentAddress: agentKey.Address(),
		Owner:        owner.Address(),
		Name:         strings.TrimSpace(p.Name),
		Summary:      strings.TrimSpace(p.Summary),
		Skills:       normalizeSkills(p.Skills),
		Offers:       normalizeOffers(p.Offers),
		Payment:      normalizePaymentTerms(p.Payment),
		Transport:    normalizeTransportInfo(p.Transport),
		Seq:          p.Seq,
		PublishedAt:  now.Unix(),
		ExpiresAt:    now.Add(ttl).Unix(),
	}
	if card.Seq == 0 {
		card.Seq = card.PublishedAt
	}

	card.OwnerCert, err = signPayload(owner.PrivateKey, []byte(card.AgentAddress))
	if err != nil {
		return nil, fmt.Errorf("signing owner certificate: %w", err)
	}
	card.Signature, err = card.sign(agentKey.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("signing agent card: %w", err)
	}
	if err := card.Verify(now); err != nil {
		return nil, err
	}
	return card, nil
}

// Verify validates signatures and required fields for a card.
func (c *AgentCard) Verify(now time.Time) error {
	if err := c.Validate(now); err != nil {
		return err
	}
	ownerKey, err := skykey.ParseAddress(c.Owner)
	if err != nil {
		return fmt.Errorf("parsing owner address: %w", err)
	}
	ownerSig, err := base64.StdEncoding.DecodeString(c.OwnerCert)
	if err != nil {
		return fmt.Errorf("decoding owner certificate: %w", err)
	}
	if !ed25519.Verify(ownerKey.PublicKey, []byte(c.AgentAddress), ownerSig) {
		return fmt.Errorf("owner certificate verification failed")
	}

	agentKey, err := skykey.ParseAddress(c.AgentAddress)
	if err != nil {
		return fmt.Errorf("parsing agent address: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil {
		return fmt.Errorf("decoding agent signature: %w", err)
	}
	payload, err := c.payloadBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(agentKey.PublicKey, payload, sig) {
		return fmt.Errorf("agent signature verification failed")
	}
	return nil
}

// Validate checks shape and business rules without verifying signatures.
func (c *AgentCard) Validate(now time.Time) error {
	if strings.TrimSpace(c.AgentID) == "" {
		return fmt.Errorf("agent_id is required")
	}
	if strings.TrimSpace(c.AgentAddress) == "" {
		return fmt.Errorf("agent_address is required")
	}
	if strings.TrimSpace(c.Owner) == "" {
		return fmt.Errorf("owner is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(c.Skills) == 0 && len(c.Offers) == 0 {
		return fmt.Errorf("at least one skill or offer is required")
	}
	if c.PublishedAt == 0 {
		return fmt.Errorf("published_at is required")
	}
	if c.ExpiresAt != 0 && c.ExpiresAt <= c.PublishedAt {
		return fmt.Errorf("expires_at must be after published_at")
	}
	if !now.IsZero() && c.ExpiresAt != 0 && c.ExpiresAt < now.Unix() {
		return fmt.Errorf("agent card has expired")
	}
	if strings.TrimSpace(c.OwnerCert) == "" {
		return fmt.Errorf("owner_cert is required")
	}
	if strings.TrimSpace(c.Signature) == "" {
		return fmt.Errorf("signature is required")
	}
	for _, skill := range c.Skills {
		if strings.TrimSpace(skill.ID) == "" {
			return fmt.Errorf("skill id is required")
		}
		if strings.TrimSpace(skill.Name) == "" {
			return fmt.Errorf("skill name is required")
		}
		if skill.Price != nil {
			if err := validatePrice(*skill.Price); err != nil {
				return fmt.Errorf("skill %s: %w", skill.ID, err)
			}
		}
	}
	for _, offer := range c.Offers {
		if strings.TrimSpace(offer.SKU) == "" {
			return fmt.Errorf("offer sku is required")
		}
		if strings.TrimSpace(offer.Title) == "" {
			return fmt.Errorf("offer title is required")
		}
		if err := validatePrice(offer.Price); err != nil {
			return fmt.Errorf("offer %s: %w", offer.SKU, err)
		}
	}
	return nil
}

func (c *AgentCard) sign(priv ed25519.PrivateKey) (string, error) {
	payload, err := c.payloadBytes()
	if err != nil {
		return "", err
	}
	return signPayload(priv, payload)
}

func (c *AgentCard) payloadBytes() ([]byte, error) {
	unsigned := unsignedAgentCard{
		AgentID:      c.AgentID,
		AgentAddress: c.AgentAddress,
		Owner:        c.Owner,
		OwnerCert:    c.OwnerCert,
		Name:         c.Name,
		Summary:      c.Summary,
		Skills:       c.Skills,
		Offers:       c.Offers,
		Payment:      c.Payment,
		Transport:    c.Transport,
		Seq:          c.Seq,
		PublishedAt:  c.PublishedAt,
		ExpiresAt:    c.ExpiresAt,
	}
	data, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent card payload: %w", err)
	}
	return data, nil
}

func signPayload(priv ed25519.PrivateKey, payload []byte) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("private key is required")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)), nil
}

func normalizeSkills(skills []SkillSpec) []SkillSpec {
	out := make([]SkillSpec, 0, len(skills))
	for _, skill := range skills {
		skill.ID = strings.TrimSpace(skill.ID)
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Description = strings.TrimSpace(skill.Description)
		skill.Tags = normalizeTags(skill.Tags)
		if skill.Price != nil {
			price := normalizePrice(*skill.Price)
			skill.Price = &price
		}
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func normalizeOffers(offers []Offer) []Offer {
	out := make([]Offer, 0, len(offers))
	for _, offer := range offers {
		offer.SKU = strings.TrimSpace(offer.SKU)
		offer.Title = strings.TrimSpace(offer.Title)
		offer.Summary = strings.TrimSpace(offer.Summary)
		offer.Category = strings.TrimSpace(strings.ToLower(offer.Category))
		offer.Fulfillment = strings.TrimSpace(strings.ToLower(offer.Fulfillment))
		offer.Location = strings.TrimSpace(offer.Location)
		offer.Price = normalizePrice(offer.Price)
		offer.Tags = normalizeTags(offer.Tags)
		out = append(out, offer)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
	return out
}

func normalizePrice(price Price) Price {
	price.Amount = strings.TrimSpace(price.Amount)
	price.Asset = strings.TrimSpace(strings.ToUpper(price.Asset))
	price.Per = strings.TrimSpace(strings.ToLower(price.Per))
	return price
}

func normalizePaymentTerms(payment PaymentTerms) PaymentTerms {
	payment.Chain = strings.TrimSpace(payment.Chain)
	payment.Asset = strings.TrimSpace(strings.ToUpper(payment.Asset))
	payment.Address = strings.TrimSpace(payment.Address)
	return payment
}

func normalizeTransportInfo(transport TransportInfo) TransportInfo {
	transport.Preferred = strings.TrimSpace(strings.ToLower(transport.Preferred))
	for i := range transport.Fallback {
		transport.Fallback[i] = strings.TrimSpace(strings.ToLower(transport.Fallback[i]))
	}
	return transport
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func validatePrice(price Price) error {
	if strings.TrimSpace(price.Amount) == "" {
		return fmt.Errorf("price amount is required")
	}
	if strings.TrimSpace(price.Asset) == "" {
		return fmt.Errorf("price asset is required")
	}
	return nil
}
