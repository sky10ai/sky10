package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

// DefaultPaySHCatalogURL is the public pay.sh provider catalog. The
// homepage links this as the live provider catalog for agents.
const DefaultPaySHCatalogURL = "https://pay.sh/api/catalog"

// PaySHSource fetches service manifests from pay.sh's live provider catalog.
// pay.sh catalog entries can resolve to x402 or MPP endpoints; the transport
// detects the concrete payment protocol from each endpoint's 402 response.
type PaySHSource struct {
	name       string
	catalogURL string
	client     *http.Client
}

// NewPaySHSource constructs a source for the live pay.sh catalog. catalogURL
// may be overridden in tests; client may be nil to use a 30-second default.
func NewPaySHSource(catalogURL string, client *http.Client) *PaySHSource {
	if strings.TrimSpace(catalogURL) == "" {
		catalogURL = DefaultPaySHCatalogURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &PaySHSource{
		name:       "pay.sh",
		catalogURL: catalogURL,
		client:     client,
	}
}

// Name implements Source.
func (s *PaySHSource) Name() string { return s.name }

// Fetch implements Source.
func (s *PaySHSource) Fetch(ctx context.Context) ([]x402.ServiceManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.catalogURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", s.catalogURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", s.catalogURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var payload paySHCatalog
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", s.catalogURL, err)
	}

	updatedAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(payload.GeneratedAt))
	out := make([]x402.ServiceManifest, 0, len(payload.Providers))
	for _, provider := range payload.Providers {
		manifest, ok := convertPaySHProvider(provider, updatedAt)
		if !ok {
			continue
		}
		out = append(out, manifest)
	}
	return out, nil
}

type paySHCatalog struct {
	GeneratedAt string          `json:"generated_at"`
	Providers   []paySHProvider `json:"providers"`
}

type paySHProvider struct {
	FQN         string      `json:"fqn"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Category    string      `json:"category"`
	ServiceURL  string      `json:"service_url"`
	MaxPriceUSD json.Number `json:"max_price_usd"`
}

func convertPaySHProvider(provider paySHProvider, updatedAt time.Time) (x402.ServiceManifest, bool) {
	fqn := strings.TrimSpace(provider.FQN)
	endpoint := strings.TrimRight(strings.TrimSpace(provider.ServiceURL), "/")
	if fqn == "" || endpoint == "" {
		return x402.ServiceManifest{}, false
	}
	displayName := strings.TrimSpace(provider.Title)
	if displayName == "" {
		displayName = fqn
	}
	price := formatPaySHPrice(provider.MaxPriceUSD)
	if price == "" {
		price = "0"
	}
	return x402.ServiceManifest{
		ID:           "pay.sh/" + fqn,
		DisplayName:  displayName,
		Category:     strings.TrimSpace(provider.Category),
		Description:  strings.TrimSpace(provider.Description),
		Endpoint:     endpoint,
		Networks:     []x402.Network{x402.NetworkBase, x402.NetworkSolana},
		MaxPriceUSDC: price,
		UpdatedAt:    updatedAt,
	}, true
}

func formatPaySHPrice(value json.Number) string {
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return "0"
	}
	if strings.HasPrefix(raw, "-") {
		return "0"
	}
	if strings.ContainsAny(raw, "eE") {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f <= 0 {
			return "0"
		}
		raw = strconv.FormatFloat(f, 'f', 9, 64)
	}
	raw = strings.TrimPrefix(raw, "+")
	if strings.HasPrefix(raw, ".") {
		raw = "0" + raw
	}
	parts := strings.SplitN(raw, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	if !isPaySHDecimalDigits(whole) {
		return "0"
	}
	frac := ""
	if len(parts) == 1 {
		frac = ""
	} else {
		frac = parts[1]
	}
	if frac != "" && !isPaySHDecimalDigits(frac) {
		return "0"
	}
	extra := ""
	if len(frac) > 6 {
		extra = frac[6:]
		frac = frac[:6]
	}
	for len(frac) < 6 {
		frac += "0"
	}
	combined := strings.TrimLeft(whole+frac, "0")
	if combined == "" {
		combined = "0"
	}
	micros, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return "0"
	}
	if hasNonZeroDigit(extra) {
		micros.Add(micros, big.NewInt(1))
	}
	return formatPaySHMicros(micros)
}

func formatPaySHMicros(v *big.Int) string {
	if v == nil || v.Sign() <= 0 {
		return "0"
	}
	const decimals = 6
	s := v.String()
	for len(s) <= decimals {
		s = "0" + s
	}
	whole := s[:len(s)-decimals]
	frac := strings.TrimRight(s[len(s)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

func isPaySHDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasNonZeroDigit(value string) bool {
	for _, r := range value {
		if r >= '1' && r <= '9' {
			return true
		}
	}
	return false
}
