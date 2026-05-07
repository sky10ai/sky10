// Package llm stores local AI endpoint connection settings.
package llm

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

const (
	ProviderVenice    = "venice"
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"

	AuthMethodX402   = "x402"
	AuthMethodAPIKey = "api_key"

	DefaultVeniceConnectionID = "venice"
	DefaultVeniceLabel        = "Venice"
	DefaultVeniceBaseURL      = "https://api.venice.ai/api/v1"
	DefaultVeniceModel        = "venice-uncensored"
	DefaultVeniceX402Service  = "venice-ai"
	DefaultVeniceWallet       = "default"
	DefaultVeniceMaxPriceUSDC = "10.00"
	DefaultVeniceDailyCapUSDC = "10.00"

	DefaultOpenAIConnectionID = "openai"
	DefaultOpenAILabel        = "OpenAI"
	DefaultOpenAIBaseURL      = "https://api.openai.com/v1"
	DefaultOpenAIModel        = "gpt-5.5"
	DefaultOpenAIAPIKeyEnv    = "OPENAI_API_KEY"

	DefaultAnthropicConnectionID = "anthropic"
	DefaultAnthropicLabel        = "Anthropic"
	DefaultAnthropicBaseURL      = "https://api.anthropic.com/v1"
	DefaultAnthropicModel        = "claude-opus-4-7"
	DefaultAnthropicAPIKeyEnv    = "ANTHROPIC_API_KEY"
	DefaultAnthropicAPIVersion   = "2023-06-01"
	DefaultAnthropicMaxTokens    = 4096
)

var (
	connectionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	usdcPattern         = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,6})?$`)
)

// Connection is a named AI endpoint configuration. The connection chooses
// endpoint, auth/payment method, and default model; callers can still override
// the model at route time.
type Connection struct {
	ID           string     `json:"id"`
	Label        string     `json:"label"`
	Provider     string     `json:"provider"`
	BaseURL      string     `json:"base_url"`
	DefaultModel string     `json:"default_model,omitempty"`
	Models       []string   `json:"models,omitempty"`
	Auth         AuthConfig `json:"auth"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AuthConfig captures provider-specific auth details. For Venice we use x402,
// backed by pkg/x402's wallet signing, service registry, and receipts.
type AuthConfig struct {
	Method       string `json:"method"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	SecretRef    string `json:"secret_ref,omitempty"`
	APIVersion   string `json:"api_version,omitempty"`
	Wallet       string `json:"wallet,omitempty"`
	Network      string `json:"network,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	MaxPriceUSDC string `json:"max_price_usdc,omitempty"`
	DailyCapUSDC string `json:"daily_cap_usdc,omitempty"`
}

// Provider describes one supported provider family for settings UI.
type Provider struct {
	ID             string     `json:"id"`
	DisplayName    string     `json:"display_name"`
	EndpointStyle  string     `json:"endpoint_style"`
	AuthMethods    []string   `json:"auth_methods"`
	DefaultBaseURL string     `json:"default_base_url"`
	DefaultModel   string     `json:"default_model"`
	DefaultModels  []string   `json:"default_models,omitempty"`
	DefaultAuth    AuthConfig `json:"default_auth"`
}

// Providers returns the provider families this build exposes in settings.
func Providers() []Provider {
	return []Provider{
		{
			ID:             ProviderOpenAI,
			DisplayName:    "OpenAI",
			EndpointStyle:  "openai_chat_completions",
			AuthMethods:    []string{AuthMethodAPIKey},
			DefaultBaseURL: DefaultOpenAIBaseURL,
			DefaultModel:   DefaultOpenAIModel,
			DefaultModels:  []string{DefaultOpenAIModel, "gpt-5.4", "gpt-5.4-mini"},
			DefaultAuth: AuthConfig{
				Method:    AuthMethodAPIKey,
				APIKeyEnv: DefaultOpenAIAPIKeyEnv,
			},
		},
		{
			ID:             ProviderAnthropic,
			DisplayName:    "Anthropic",
			EndpointStyle:  "anthropic_messages",
			AuthMethods:    []string{AuthMethodAPIKey},
			DefaultBaseURL: DefaultAnthropicBaseURL,
			DefaultModel:   DefaultAnthropicModel,
			DefaultModels:  []string{DefaultAnthropicModel, "claude-sonnet-4-6", "claude-haiku-4-5"},
			DefaultAuth: AuthConfig{
				Method:     AuthMethodAPIKey,
				APIKeyEnv:  DefaultAnthropicAPIKeyEnv,
				APIVersion: DefaultAnthropicAPIVersion,
			},
		},
		{
			ID:             ProviderVenice,
			DisplayName:    "Venice",
			EndpointStyle:  "openai_chat_completions",
			AuthMethods:    []string{AuthMethodX402},
			DefaultBaseURL: DefaultVeniceBaseURL,
			DefaultModel:   DefaultVeniceModel,
			DefaultModels:  []string{DefaultVeniceModel},
			DefaultAuth: AuthConfig{
				Method:       AuthMethodX402,
				Wallet:       DefaultVeniceWallet,
				Network:      string(x402.NetworkBase),
				ServiceID:    DefaultVeniceX402Service,
				MaxPriceUSDC: DefaultVeniceMaxPriceUSDC,
				DailyCapUSDC: DefaultVeniceDailyCapUSDC,
			},
		},
	}
}

func normalizeConnection(in Connection, existing *Connection, now time.Time) (Connection, error) {
	out := in
	out.ID = strings.TrimSpace(out.ID)
	out.Label = strings.TrimSpace(out.Label)
	out.Provider = strings.TrimSpace(strings.ToLower(out.Provider))
	out.BaseURL = strings.TrimRight(strings.TrimSpace(out.BaseURL), "/")
	out.DefaultModel = strings.TrimSpace(out.DefaultModel)
	out.Models = normalizeModels(out.Models)
	out.Auth = normalizeAuth(out.Auth)

	if out.ID == "" {
		return Connection{}, errors.New("connection id is required")
	}
	if !connectionIDPattern.MatchString(out.ID) {
		return Connection{}, fmt.Errorf("connection id %q must start with a letter or number and contain only letters, numbers, dots, underscores, or dashes", out.ID)
	}
	if out.Provider == "" {
		out.Provider = ProviderVenice
	}
	if !supportedProvider(out.Provider) {
		return Connection{}, fmt.Errorf("provider %q is not supported yet", out.Provider)
	}
	normalizeProviderDefaults(&out)
	if out.Label == "" {
		return Connection{}, errors.New("label is required")
	}
	if err := validateBaseURL(out.BaseURL); err != nil {
		return Connection{}, err
	}
	if err := validateAuth(out.Provider, out.Auth); err != nil {
		return Connection{}, err
	}

	if existing != nil && !existing.CreatedAt.IsZero() {
		out.CreatedAt = existing.CreatedAt.UTC()
	}
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now.UTC()
	}
	out.UpdatedAt = now.UTC()
	return out, nil
}

func normalizeAuth(in AuthConfig) AuthConfig {
	return AuthConfig{
		Method:       strings.TrimSpace(strings.ToLower(in.Method)),
		APIKeyEnv:    strings.TrimSpace(in.APIKeyEnv),
		SecretRef:    strings.TrimSpace(in.SecretRef),
		APIVersion:   strings.TrimSpace(in.APIVersion),
		Wallet:       strings.TrimSpace(in.Wallet),
		Network:      strings.TrimSpace(strings.ToLower(in.Network)),
		ServiceID:    strings.TrimSpace(in.ServiceID),
		MaxPriceUSDC: strings.TrimSpace(in.MaxPriceUSDC),
		DailyCapUSDC: strings.TrimSpace(in.DailyCapUSDC),
	}
}

func normalizeModels(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, model := range in {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func supportedProvider(provider string) bool {
	switch provider {
	case ProviderVenice, ProviderOpenAI, ProviderAnthropic:
		return true
	default:
		return false
	}
}

func normalizeProviderDefaults(c *Connection) {
	switch c.Provider {
	case ProviderOpenAI:
		normalizeOpenAIDefaults(c)
	case ProviderAnthropic:
		normalizeAnthropicDefaults(c)
	case ProviderVenice:
		normalizeVeniceDefaults(c)
	}
}

func normalizeOpenAIDefaults(c *Connection) {
	if c.Label == "" {
		c.Label = DefaultOpenAILabel
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultOpenAIBaseURL
	}
	if c.Auth.Method == "" {
		c.Auth.Method = AuthMethodAPIKey
	}
	if c.Auth.APIKeyEnv == "" && c.Auth.SecretRef == "" {
		c.Auth.APIKeyEnv = DefaultOpenAIAPIKeyEnv
	}
}

func normalizeAnthropicDefaults(c *Connection) {
	if c.Label == "" {
		c.Label = DefaultAnthropicLabel
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultAnthropicBaseURL
	}
	if c.Auth.Method == "" {
		c.Auth.Method = AuthMethodAPIKey
	}
	if c.Auth.APIKeyEnv == "" && c.Auth.SecretRef == "" {
		c.Auth.APIKeyEnv = DefaultAnthropicAPIKeyEnv
	}
	if c.Auth.APIVersion == "" {
		c.Auth.APIVersion = DefaultAnthropicAPIVersion
	}
}

func normalizeVeniceDefaults(c *Connection) {
	if c.Label == "" {
		c.Label = DefaultVeniceLabel
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultVeniceBaseURL
	}
	if c.Auth.Method == "" {
		c.Auth.Method = AuthMethodX402
	}
	if c.Auth.Wallet == "" {
		c.Auth.Wallet = DefaultVeniceWallet
	}
	if c.Auth.Network == "" {
		c.Auth.Network = string(x402.NetworkBase)
	}
	if c.Auth.ServiceID == "" {
		c.Auth.ServiceID = DefaultVeniceX402Service
	}
	if c.Auth.MaxPriceUSDC == "" {
		c.Auth.MaxPriceUSDC = DefaultVeniceMaxPriceUSDC
	}
	if c.Auth.DailyCapUSDC == "" {
		c.Auth.DailyCapUSDC = DefaultVeniceDailyCapUSDC
	}
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return errors.New("base_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("base_url is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("base_url scheme %q is not supported", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("base_url host is required")
	}
	return nil
}

func validateAuth(provider string, auth AuthConfig) error {
	switch provider {
	case ProviderOpenAI, ProviderAnthropic:
		return validateAPIKeyAuth(provider, auth)
	case ProviderVenice:
		return validateVeniceAuth(auth)
	default:
		return fmt.Errorf("provider %q is not supported yet", provider)
	}
}

func validateAPIKeyAuth(provider string, auth AuthConfig) error {
	if auth.Method != AuthMethodAPIKey {
		return fmt.Errorf("%s auth method %q is not supported", provider, auth.Method)
	}
	if auth.APIKeyEnv == "" && auth.SecretRef == "" {
		return errors.New("api_key_env or secret_ref is required")
	}
	if provider == ProviderAnthropic && auth.APIVersion == "" {
		return errors.New("anthropic api_version is required")
	}
	return nil
}

func validateVeniceAuth(auth AuthConfig) error {
	if auth.Method != AuthMethodX402 {
		return fmt.Errorf("venice auth method %q is not supported", auth.Method)
	}
	if auth.ServiceID == "" {
		return errors.New("x402 service_id is required")
	}
	if auth.Network == "" {
		return errors.New("x402 network is required")
	}
	if auth.MaxPriceUSDC != "" && !usdcPattern.MatchString(auth.MaxPriceUSDC) {
		return fmt.Errorf("max_price_usdc %q must be a non-negative USDC decimal with at most 6 fractional digits", auth.MaxPriceUSDC)
	}
	if auth.DailyCapUSDC != "" && !usdcPattern.MatchString(auth.DailyCapUSDC) {
		return fmt.Errorf("daily_cap_usdc %q must be a non-negative USDC decimal with at most 6 fractional digits", auth.DailyCapUSDC)
	}
	return nil
}
