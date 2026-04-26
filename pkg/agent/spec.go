package agent

const (
	AgentSpecVersion = "0.1.0"

	SpecStatusDraft     = "draft"
	SpecStatusApproved  = "approved"
	SpecStatusDiscarded = "discarded"
)

type AgentSpec struct {
	Spec          string             `json:"spec" yaml:"spec"`
	ID            string             `json:"id" yaml:"id"`
	Status        string             `json:"status" yaml:"status"`
	Prompt        string             `json:"prompt" yaml:"prompt"`
	Name          string             `json:"name" yaml:"name"`
	Description   string             `json:"description" yaml:"description"`
	Runtime       AgentRuntimeSpec   `json:"runtime" yaml:"runtime"`
	Fulfillment   AgentFulfillment   `json:"fulfillment" yaml:"fulfillment"`
	Tools         []AgentToolSpec    `json:"tools" yaml:"tools"`
	Inputs        []AgentIOSpec      `json:"inputs" yaml:"inputs"`
	Outputs       []AgentIOSpec      `json:"outputs" yaml:"outputs"`
	Secrets       []AgentSecretSpec  `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Permissions   []string           `json:"permissions" yaml:"permissions"`
	Commerce      AgentCommerceSpec  `json:"commerce" yaml:"commerce"`
	JobPolicy     AgentJobPolicy     `json:"job_policy" yaml:"job_policy"`
	PublishPolicy AgentPublishPolicy `json:"publish_policy" yaml:"publish_policy"`
	CreatedAt     string             `json:"created_at" yaml:"created_at"`
	UpdatedAt     string             `json:"updated_at" yaml:"updated_at"`
	ApprovedAt    string             `json:"approved_at,omitempty" yaml:"approved_at,omitempty"`
	Meta          map[string]string  `json:"meta,omitempty" yaml:"meta,omitempty"`
}

type AgentRuntimeSpec struct {
	Target     string               `json:"target" yaml:"target"`
	Provider   string               `json:"provider,omitempty" yaml:"provider,omitempty"`
	Template   string               `json:"template,omitempty" yaml:"template,omitempty"`
	Harness    string               `json:"harness,omitempty" yaml:"harness,omitempty"`
	Packages   []string             `json:"packages,omitempty" yaml:"packages,omitempty"`
	Containers []AgentContainerSpec `json:"containers,omitempty" yaml:"containers,omitempty"`
}

type AgentContainerSpec struct {
	Name     string   `json:"name" yaml:"name"`
	Image    string   `json:"image,omitempty" yaml:"image,omitempty"`
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
}

type AgentFulfillment struct {
	Mode string `json:"mode" yaml:"mode"`
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

type AgentToolSpec struct {
	Name              string                 `json:"name" yaml:"name"`
	Capability        string                 `json:"capability,omitempty" yaml:"capability,omitempty"`
	Description       string                 `json:"description" yaml:"description"`
	Audience          string                 `json:"audience" yaml:"audience"`
	Scope             string                 `json:"scope" yaml:"scope"`
	InputSchema       map[string]interface{} `json:"input_schema" yaml:"input_schema"`
	OutputSchema      map[string]interface{} `json:"output_schema" yaml:"output_schema"`
	StreamSchema      map[string]interface{} `json:"stream_schema,omitempty" yaml:"stream_schema,omitempty"`
	Effects           []string               `json:"effects,omitempty" yaml:"effects,omitempty"`
	Availability      AgentAvailability      `json:"availability" yaml:"availability"`
	Fulfillment       AgentFulfillment       `json:"fulfillment" yaml:"fulfillment"`
	Pricing           AgentPricingSpec       `json:"pricing" yaml:"pricing"`
	SupportsCancel    bool                   `json:"supports_cancel" yaml:"supports_cancel"`
	SupportsStreaming bool                   `json:"supports_streaming" yaml:"supports_streaming"`
	Meta              map[string]interface{} `json:"meta,omitempty" yaml:"meta,omitempty"`
}

type AgentAvailability struct {
	Status          string `json:"status" yaml:"status"`
	Message         string `json:"message,omitempty" yaml:"message,omitempty"`
	NextAvailableAt string `json:"next_available_at,omitempty" yaml:"next_available_at,omitempty"`
}

type AgentPricingSpec struct {
	Model           string             `json:"model" yaml:"model"`
	PaymentAsset    *AgentPaymentAsset `json:"payment_asset,omitempty" yaml:"payment_asset,omitempty"`
	Amount          string             `json:"amount,omitempty" yaml:"amount,omitempty"`
	Unit            string             `json:"unit,omitempty" yaml:"unit,omitempty"`
	Rate            string             `json:"rate,omitempty" yaml:"rate,omitempty"`
	IntervalSeconds int                `json:"interval_seconds,omitempty" yaml:"interval_seconds,omitempty"`
}

type AgentPaymentAsset struct {
	ChainID  string `json:"chain_id" yaml:"chain_id"`
	AssetID  string `json:"asset_id,omitempty" yaml:"asset_id,omitempty"`
	Symbol   string `json:"symbol" yaml:"symbol"`
	Decimals int    `json:"decimals" yaml:"decimals"`
}

type AgentIOSpec struct {
	Kind        string   `json:"kind" yaml:"kind"`
	Description string   `json:"description" yaml:"description"`
	MimeTypes   []string `json:"mime_types,omitempty" yaml:"mime_types,omitempty"`
	Required    bool     `json:"required" yaml:"required"`
}

type AgentSecretSpec struct {
	Name        string `json:"name" yaml:"name"`
	Env         string `json:"env" yaml:"env"`
	Required    bool   `json:"required" yaml:"required"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

type AgentCommerceSpec struct {
	Enabled        bool             `json:"enabled" yaml:"enabled"`
	DefaultPricing AgentPricingSpec `json:"default_pricing" yaml:"default_pricing"`
	PayoutWallet   string           `json:"payout_wallet,omitempty" yaml:"payout_wallet,omitempty"`
	Terms          string           `json:"terms,omitempty" yaml:"terms,omitempty"`
}

type AgentJobPolicy struct {
	SupportsCancel     bool `json:"supports_cancel" yaml:"supports_cancel"`
	SupportsStreaming  bool `json:"supports_streaming" yaml:"supports_streaming"`
	MaxDurationSeconds int  `json:"max_duration_seconds,omitempty" yaml:"max_duration_seconds,omitempty"`
	RetentionDays      int  `json:"retention_days,omitempty" yaml:"retention_days,omitempty"`
}

type AgentPublishPolicy struct {
	Audience string `json:"audience" yaml:"audience"`
	Scope    string `json:"scope" yaml:"scope"`
}

type AgentSpecCreateParams struct {
	Prompt string `json:"prompt"`
}

type AgentSpecGetParams struct {
	ID string `json:"id"`
}

type AgentSpecListParams struct {
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type AgentSpecUpdateParams struct {
	Spec AgentSpec `json:"spec"`
}

type AgentSpecActionParams struct {
	ID string `json:"id"`
}

type AgentSpecResult struct {
	Spec AgentSpec `json:"spec"`
}

type AgentSpecListResult struct {
	Specs []AgentSpec `json:"specs"`
}
