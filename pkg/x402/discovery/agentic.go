package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

// DefaultAgenticMarketBaseURL is the canonical agentic.market API
// host. Discovered from the directory's llms.txt:
// https://api.agentic.market/v1/services .
const DefaultAgenticMarketBaseURL = "https://api.agentic.market"

// AgenticMarketSource fetches the live x402 service catalog from
// agentic.market. The wire schema is documented at
// https://agentic.market/llms.txt; this source decodes the relevant
// fields into pkg/x402.ServiceManifest values.
//
// Each upstream service may have multiple per-endpoint URLs and per-
// endpoint pricing. The AgenticMarketSource collapses that into one
// manifest per service:
//
//   - manifest endpoint = scheme://host of the service's first
//     endpoint URL (the directory groups providers by domain, so the
//     first URL is canonical); all per-call paths are sent relative
//     to this base
//   - manifest max_price_usdc = max of every endpoint's USDC pricing
//     amount, so per-call budget enforcement covers the worst case
//   - networks = lowercased values from the upstream "networks" array,
//     with anything unrecognized dropped
type AgenticMarketSource struct {
	name    string
	baseURL string
	client  *http.Client
}

// NewAgenticMarketSource constructs a source pointing at the live
// agentic.market API. baseURL may be overridden in tests; client may
// be nil to use a 30-second-default http.Client.
func NewAgenticMarketSource(baseURL string, client *http.Client) *AgenticMarketSource {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultAgenticMarketBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &AgenticMarketSource{
		name:    "agentic.market",
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

// Name implements Source.
func (s *AgenticMarketSource) Name() string { return s.name }

// Fetch implements Source. Retrieves /v1/services and converts every
// entry it can parse into a ServiceManifest. Entries with no callable
// endpoint or no recognizable network are silently skipped — Refresh
// treats them as if they did not appear in the directory.
func (s *AgenticMarketSource) Fetch(ctx context.Context) ([]x402.ServiceManifest, error) {
	target := s.baseURL + "/v1/services"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", target, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload agenticResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", target, err)
	}

	out := make([]x402.ServiceManifest, 0, len(payload.Services))
	for _, svc := range payload.Services {
		manifest, ok := convertAgenticService(svc)
		if !ok {
			continue
		}
		out = append(out, manifest)
	}
	return out, nil
}

// agenticResponse is the top-level shape of /v1/services.
type agenticResponse struct {
	Services []agenticService `json:"services"`
}

// agenticService captures the fields we currently use. Unknown fields
// are ignored — the upstream schema can grow without breaking us.
type agenticService struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Domain      string            `json:"domain"`
	ProviderURL string            `json:"providerUrl"`
	Category    string            `json:"category"`
	Networks    []string          `json:"networks"`
	Endpoints   []agenticEndpoint `json:"endpoints"`
}

type agenticEndpoint struct {
	URL         string         `json:"url"`
	Description string         `json:"description"`
	Pricing     agenticPricing `json:"pricing"`
	Method      string         `json:"method"`
}

type agenticPricing struct {
	Amount    string `json:"amount"`
	MaxAmount string `json:"maxAmount"`
	Currency  string `json:"currency"`
	Network   string `json:"network"`
}

// convertAgenticService projects one upstream service onto a
// ServiceManifest. Returns false if the service has no usable
// endpoint URL — those are not worth exposing to the agent.
func convertAgenticService(svc agenticService) (x402.ServiceManifest, bool) {
	if strings.TrimSpace(svc.ID) == "" {
		return x402.ServiceManifest{}, false
	}
	endpoint, ok := canonicalEndpoint(svc.Endpoints)
	if !ok {
		return x402.ServiceManifest{}, false
	}
	maxPrice, err := maxEndpointPrice(svc.Endpoints)
	if err != nil {
		// One unparseable price should not poison the whole entry;
		// fall back to "0" (free / unmetered) and let the caller's
		// max_price_usdc enforce.
		maxPrice = "0"
	}
	return x402.ServiceManifest{
		ID:           svc.ID,
		DisplayName:  svc.Name,
		Description:  svc.Description,
		Category:     svc.Category,
		Endpoint:     endpoint,
		ServiceURL:   canonicalServiceURL(svc.ProviderURL, svc.Domain),
		Endpoints:    serviceEndpoints(svc.Endpoints),
		Networks:     normalizeNetworks(svc.Networks),
		MaxPriceUSDC: maxPrice,
	}, true
}

// canonicalEndpoint returns scheme://host (no path, no query) of the
// first endpoint URL it can parse. Everything else falls through to
// false.
func canonicalEndpoint(endpoints []agenticEndpoint) (string, bool) {
	for _, ep := range endpoints {
		u, ok := parseHTTPURL(ep.URL)
		if !ok {
			continue
		}
		return u.Scheme + "://" + u.Host, true
	}
	return "", false
}

func serviceEndpoints(endpoints []agenticEndpoint) []x402.ServiceEndpoint {
	out := make([]x402.ServiceEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		u, ok := parseHTTPURL(ep.URL)
		if !ok {
			continue
		}
		entry := x402.ServiceEndpoint{
			URL:         u.String(),
			Method:      strings.ToUpper(strings.TrimSpace(ep.Method)),
			Description: strings.TrimSpace(ep.Description),
			PriceUSDC:   endpointPriceUSDC(ep),
			Network:     normalizeNetwork(ep.Pricing.Network),
		}
		out = append(out, entry)
	}
	return out
}

func endpointPriceUSDC(ep agenticEndpoint) string {
	amount := endpointMaxAmount(ep)
	if amount == "" {
		return ""
	}
	if _, err := x402.CompareUSDC(amount, "0"); err != nil {
		return ""
	}
	return amount
}

func endpointMaxAmount(ep agenticEndpoint) string {
	if maxAmount := strings.TrimSpace(ep.Pricing.MaxAmount); maxAmount != "" {
		return maxAmount
	}
	return strings.TrimSpace(ep.Pricing.Amount)
}

func canonicalServiceURL(providerURL, domain string) string {
	if home := x402.ServiceHomeURL(providerURL); home != "" {
		return home
	}
	return x402.ServiceHomeURL(domain)
}

func parseHTTPURL(raw string) (*url.URL, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, false
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return nil, false
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, false
	}
	return u, true
}

// maxEndpointPrice returns the largest non-empty USDC price across
// all endpoints, formatted as the standard decimal string. Endpoints
// without a price (free or dynamic) contribute zero. Returns an
// error only when at least one parsed price had unrecoverable
// formatting; in that case the caller may fall back to "0".
func maxEndpointPrice(endpoints []agenticEndpoint) (string, error) {
	var best *big.Int
	for _, ep := range endpoints {
		amt := endpointMaxAmount(ep)
		if amt == "" {
			continue
		}
		cmp, err := x402.CompareUSDC(amt, "0")
		if err != nil {
			return "0", err
		}
		if cmp <= 0 {
			continue
		}
		if best == nil {
			best = newUSDCMicros(amt)
			continue
		}
		current := newUSDCMicros(amt)
		if current != nil && current.Cmp(best) > 0 {
			best = current
		}
	}
	if best == nil {
		return "0", nil
	}
	return formatUSDCMicros(best), nil
}

// newUSDCMicros parses a decimal USDC string into micro-USDC big.Int.
// Returns nil on parse failure (callers treat it as "skip this
// endpoint's contribution"). We re-use pkg/x402.CompareUSDC's parser
// indirectly by issuing a comparison and then computing micros from
// the canonical formatting helpers.
func newUSDCMicros(amount string) *big.Int {
	// Round-trip through CompareUSDC to validate format; on failure
	// we return nil.
	if _, err := x402.CompareUSDC(amount, "0"); err != nil {
		return nil
	}
	parts := strings.SplitN(amount, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	for len(frac) < 6 {
		frac = frac + "0"
	}
	combined := whole + frac
	combined = strings.TrimLeft(combined, "0")
	if combined == "" {
		combined = "0"
	}
	v, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil
	}
	return v
}

// formatUSDCMicros mirrors pkg/x402's internal formatter so the
// strings we produce here match what budget.go consumes.
func formatUSDCMicros(v *big.Int) string {
	const decimals = 6
	if v == nil {
		return "0"
	}
	s := v.String()
	for len(s) <= decimals {
		s = "0" + s
	}
	whole := s[:len(s)-decimals]
	frac := s[len(s)-decimals:]
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

// normalizeNetworks lowercases each entry and drops anything outside
// the recognized set.
func normalizeNetworks(in []string) []x402.Network {
	out := make([]x402.Network, 0, len(in))
	seen := make(map[x402.Network]struct{}, len(in))
	for _, raw := range in {
		network := normalizeNetwork(raw)
		if network == "" {
			continue
		}
		if _, ok := seen[network]; ok {
			continue
		}
		seen[network] = struct{}{}
		out = append(out, network)
	}
	return out
}

func normalizeNetwork(raw string) x402.Network {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "base":
		return x402.NetworkBase
	case "solana":
		return x402.NetworkSolana
	default:
		return ""
	}
}

// ErrInvalidAgenticPayload signals a Fetch failed because the upstream
// payload could not be decoded into the expected shape. Other errors
// (network, HTTP status) are left as plain wrapped errors.
var ErrInvalidAgenticPayload = errors.New("agentic.market: invalid payload")
