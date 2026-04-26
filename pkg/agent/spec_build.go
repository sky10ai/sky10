package agent

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var dollarRatePattern = regexp.MustCompile(`\$\s*([0-9]+(?:\.[0-9]+)?)`)

func BuildAgentSpec(prompt string, now time.Time) AgentSpec {
	prompt = strings.TrimSpace(prompt)
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if isDexterFinancePrompt(prompt) {
		return buildDexterFinanceSpec(prompt, timestamp)
	}
	if isCodexCodingPrompt(prompt) {
		return buildCodexCodingSpec(prompt, timestamp)
	}
	if isMediaAccentPrompt(prompt) {
		return buildMediaAccentSpec(prompt, timestamp)
	}
	return buildGenericAgentSpec(prompt, timestamp)
}

func buildMediaAccentSpec(prompt, timestamp string) AgentSpec {
	pricing := AgentPricingSpec{Model: "free"}
	commerceEnabled := promptLooksCommercial(prompt)
	if commerceEnabled {
		pricing = AgentPricingSpec{
			Model: "variable",
			Unit:  "audio_minutes",
			Rate:  extractDollarRate(prompt),
			PaymentAsset: &AgentPaymentAsset{
				ChainID:  "eip155:8453",
				AssetID:  "eip155:8453/erc20:0x833589fCD6EDB6E08f4c7C32D4f71b54bdA02913",
				Symbol:   "USDC",
				Decimals: 6,
			},
		}
	}
	effects := []string{"file.read", "file.write", "network.http"}
	if commerceEnabled {
		effects = append(effects, "payment.charge")
	}
	return AgentSpec{
		Spec:        AgentSpecVersion,
		ID:          "aspec_" + uuid.NewString(),
		Status:      SpecStatusDraft,
		Prompt:      prompt,
		Name:        "media-accent-agent",
		Description: "Process audio and video files and produce British-accent media outputs.",
		Runtime: AgentRuntimeSpec{
			Target:   "sandbox",
			Provider: defaultSandboxProvider,
			Template: defaultSandboxTemplate,
			Harness:  defaultAgentHarness,
			Packages: []string{"ffmpeg"},
			Containers: []AgentContainerSpec{
				{Name: "media-worker", Image: "ubuntu:24.04", Packages: []string{"ffmpeg"}},
			},
		},
		Fulfillment: AgentFulfillment{Mode: "autonomous"},
		Tools: []AgentToolSpec{
			{
				Name:              "media.convert",
				Capability:        "media.convert",
				Description:       "Convert an audio or video file into a British-accent output artifact.",
				Audience:          "private",
				Scope:             "current",
				InputSchema:       mediaAccentInputSchema(),
				OutputSchema:      mediaAccentOutputSchema(),
				StreamSchema:      progressStreamSchema(),
				Effects:           effects,
				Availability:      AgentAvailability{Status: "available"},
				Fulfillment:       AgentFulfillment{Mode: "autonomous"},
				Pricing:           pricing,
				SupportsCancel:    true,
				SupportsStreaming: true,
				Meta: map[string]interface{}{
					"packages": []interface{}{"ffmpeg"},
					"services": []interface{}{"voice-provider"},
				},
			},
		},
		Inputs: []AgentIOSpec{
			{
				Kind:        "payload_ref",
				Description: "Audio or video file to process.",
				MimeTypes:   []string{"audio/*", "video/*"},
				Required:    true,
			},
		},
		Outputs: []AgentIOSpec{
			{Kind: "artifact", Description: "British-accent audio output.", MimeTypes: []string{"audio/*"}, Required: true},
			{Kind: "artifact", Description: "British-accent video output when the input is video.", MimeTypes: []string{"video/*"}},
			{Kind: "artifact", Description: "Optional transcript or subtitle artifacts.", MimeTypes: []string{"text/plain", "text/srt"}},
		},
		Secrets: []AgentSecretSpec{
			{
				Name:        "voice-provider-api-key",
				Env:         "ELEVENLABS_API_KEY",
				Description: "Optional external voice provider key; attach before runtime boot if that provider is selected.",
			},
		},
		Permissions: effects,
		Commerce: AgentCommerceSpec{
			Enabled:        commerceEnabled,
			DefaultPricing: pricing,
		},
		JobPolicy: AgentJobPolicy{
			SupportsCancel:    true,
			SupportsStreaming: true,
			RetentionDays:     30,
		},
		PublishPolicy: AgentPublishPolicy{Audience: "private", Scope: "current"},
		CreatedAt:     timestamp,
		UpdatedAt:     timestamp,
		Meta:          map[string]string{"spec_source": "agent.spec.create"},
	}
}

func buildGenericAgentSpec(prompt, timestamp string) AgentSpec {
	description := "Created from prompt: " + prompt
	return AgentSpec{
		Spec:        AgentSpecVersion,
		ID:          "aspec_" + uuid.NewString(),
		Status:      SpecStatusDraft,
		Prompt:      prompt,
		Name:        "custom-agent",
		Description: description,
		Runtime:     AgentRuntimeSpec{Target: "sandbox", Provider: defaultSandboxProvider, Template: defaultSandboxTemplate, Harness: defaultAgentHarness},
		Fulfillment: AgentFulfillment{Mode: "autonomous"},
		Tools: []AgentToolSpec{
			{
				Name:              "agent.run",
				Capability:        "automation.run",
				Description:       "Run the approved agent workflow.",
				Audience:          "private",
				Scope:             "current",
				InputSchema:       genericInputSchema(),
				OutputSchema:      genericOutputSchema(),
				Effects:           []string{"file.read", "file.write"},
				Availability:      AgentAvailability{Status: "available"},
				Fulfillment:       AgentFulfillment{Mode: "autonomous"},
				Pricing:           AgentPricingSpec{Model: "free"},
				SupportsCancel:    true,
				SupportsStreaming: true,
			},
		},
		Inputs:        []AgentIOSpec{{Kind: "prompt", Description: "Structured job input for the approved workflow.", Required: true}},
		Outputs:       []AgentIOSpec{{Kind: "artifact", Description: "Workflow result artifacts.", Required: true}},
		Permissions:   []string{"file.read", "file.write"},
		Commerce:      AgentCommerceSpec{DefaultPricing: AgentPricingSpec{Model: "free"}},
		JobPolicy:     AgentJobPolicy{SupportsCancel: true, SupportsStreaming: true, RetentionDays: 30},
		PublishPolicy: AgentPublishPolicy{Audience: "private", Scope: "current"},
		CreatedAt:     timestamp,
		UpdatedAt:     timestamp,
		Meta:          map[string]string{"spec_source": "agent.spec.create"},
	}
}

func buildCodexCodingSpec(prompt, timestamp string) AgentSpec {
	return AgentSpec{
		Spec:        AgentSpecVersion,
		ID:          "aspec_" + uuid.NewString(),
		Status:      SpecStatusDraft,
		Prompt:      prompt,
		Name:        "codex-coding-agent",
		Description: "Use Codex as the agentic harness for repository analysis, code changes, tests, and pull request artifacts.",
		Runtime: AgentRuntimeSpec{
			Target:   "sandbox",
			Provider: "lima",
			Template: "codex-docker",
			Harness:  "codex",
			Packages: []string{"git", "nodejs", "python3"},
			Containers: []AgentContainerSpec{
				{Name: "codex-worker", Image: "ubuntu:24.04", Packages: []string{"git", "nodejs", "python3"}},
			},
		},
		Fulfillment: AgentFulfillment{Mode: "autonomous"},
		Tools: []AgentToolSpec{
			{
				Name:              "github.issue.fix",
				Capability:        "github.fix",
				Description:       "Analyze a repository task, modify code, run checks, and return branch or patch artifacts.",
				Audience:          "private",
				Scope:             "current",
				InputSchema:       repositoryTaskInputSchema(),
				OutputSchema:      repositoryTaskOutputSchema(),
				StreamSchema:      progressStreamSchema(),
				Effects:           []string{"repo.read", "file.write", "git.commit", "test.run", "network.http"},
				Availability:      AgentAvailability{Status: "available"},
				Fulfillment:       AgentFulfillment{Mode: "autonomous"},
				Pricing:           AgentPricingSpec{Model: "free"},
				SupportsCancel:    true,
				SupportsStreaming: true,
				Meta: map[string]interface{}{
					"harness":  "codex",
					"services": []interface{}{"git", "github"},
					"tags":     []interface{}{"coding", "automation"},
				},
			},
		},
		Inputs: []AgentIOSpec{
			{Kind: "repo_ref", Description: "Repository checkout or URI to work on.", Required: true},
			{Kind: "prompt", Description: "Coding task, bug report, or issue description.", Required: true},
		},
		Outputs: []AgentIOSpec{
			{Kind: "artifact", Description: "Patch, branch, commit, or pull request reference.", Required: true},
			{Kind: "artifact", Description: "Test logs and summary.", MimeTypes: []string{"text/plain"}},
		},
		Secrets: []AgentSecretSpec{
			{Name: "github-token", Env: "GITHUB_TOKEN", Description: "Optional GitHub token for pull request and issue operations."},
		},
		Permissions: []string{"repo.read", "file.write", "git.commit", "test.run", "network.http"},
		Commerce:    AgentCommerceSpec{DefaultPricing: AgentPricingSpec{Model: "free"}},
		JobPolicy: AgentJobPolicy{
			SupportsCancel:     true,
			SupportsStreaming:  true,
			MaxDurationSeconds: 7200,
			RetentionDays:      30,
		},
		PublishPolicy: AgentPublishPolicy{Audience: "private", Scope: "current"},
		CreatedAt:     timestamp,
		UpdatedAt:     timestamp,
		Meta:          map[string]string{"spec_source": "agent.spec.create"},
	}
}

func buildDexterFinanceSpec(prompt, timestamp string) AgentSpec {
	return AgentSpec{
		Spec:        AgentSpecVersion,
		ID:          "aspec_" + uuid.NewString(),
		Status:      SpecStatusDraft,
		Prompt:      prompt,
		Name:        "dexter-financial-research-agent",
		Description: "Use Dexter as the agentic harness for deep financial research with market data and web search.",
		Runtime: AgentRuntimeSpec{
			Target:   "sandbox",
			Provider: "lima",
			Template: "dexter-docker",
			Harness:  "dexter",
			Packages: []string{"bun", "git"},
			Containers: []AgentContainerSpec{
				{Name: "dexter-worker", Image: "oven/bun:1.1", Packages: []string{"git"}},
			},
		},
		Fulfillment: AgentFulfillment{Mode: "autonomous"},
		Tools: []AgentToolSpec{
			{
				Name:              "finance.research",
				Capability:        "finance.research",
				Description:       "Research a financial question using task planning, market data, and source-backed analysis.",
				Audience:          "private",
				Scope:             "current",
				InputSchema:       financeResearchInputSchema(),
				OutputSchema:      financeResearchOutputSchema(),
				StreamSchema:      progressStreamSchema(),
				Effects:           []string{"network.http", "market_data.read", "file.write"},
				Availability:      AgentAvailability{Status: "available"},
				Fulfillment:       AgentFulfillment{Mode: "autonomous"},
				Pricing:           AgentPricingSpec{Model: "free"},
				SupportsCancel:    true,
				SupportsStreaming: true,
				Meta: map[string]interface{}{
					"harness":  "dexter",
					"services": []interface{}{"financial-datasets", "exa"},
					"source":   "https://github.com/virattt/dexter",
				},
			},
		},
		Inputs: []AgentIOSpec{
			{Kind: "prompt", Description: "Financial research question.", Required: true},
			{Kind: "payload_ref", Description: "Optional portfolio, filings, or dataset references.", Required: false},
		},
		Outputs: []AgentIOSpec{
			{Kind: "artifact", Description: "Research memo with sources and confidence notes.", MimeTypes: []string{"text/markdown"}, Required: true},
			{Kind: "artifact", Description: "Scratchpad or audit trail.", MimeTypes: []string{"application/jsonl"}},
		},
		Secrets: []AgentSecretSpec{
			{Name: "openai-api-key", Env: "OPENAI_API_KEY", Required: true, Description: "Model provider key for Dexter."},
			{Name: "financial-datasets-api-key", Env: "FINANCIAL_DATASETS_API_KEY", Required: true, Description: "Market data API key for financial statements and company data."},
			{Name: "exa-search-api-key", Env: "EXASEARCH_API_KEY", Description: "Optional Exa key for web research."},
		},
		Permissions: []string{"network.http", "market_data.read", "file.write"},
		Commerce:    AgentCommerceSpec{DefaultPricing: AgentPricingSpec{Model: "free"}},
		JobPolicy: AgentJobPolicy{
			SupportsCancel:     true,
			SupportsStreaming:  true,
			MaxDurationSeconds: 10800,
			RetentionDays:      30,
		},
		PublishPolicy: AgentPublishPolicy{Audience: "private", Scope: "current"},
		CreatedAt:     timestamp,
		UpdatedAt:     timestamp,
		Meta:          map[string]string{"spec_source": "agent.spec.create"},
	}
}

func isMediaAccentPrompt(prompt string) bool {
	value := strings.ToLower(prompt)
	return strings.Contains(value, "media") &&
		(strings.Contains(value, "accent") || strings.Contains(value, "british"))
}

func isCodexCodingPrompt(prompt string) bool {
	value := strings.ToLower(prompt)
	return strings.Contains(value, "codex") ||
		(strings.Contains(value, "coding") && strings.Contains(value, "agent")) ||
		(strings.Contains(value, "code") && strings.Contains(value, "agent"))
}

func isDexterFinancePrompt(prompt string) bool {
	value := strings.ToLower(prompt)
	return strings.Contains(value, "dexter") ||
		strings.Contains(value, "financial agent") ||
		strings.Contains(value, "finance agent") ||
		(strings.Contains(value, "financial") && strings.Contains(value, "research"))
}

func promptLooksCommercial(prompt string) bool {
	value := strings.ToLower(prompt)
	return strings.Contains(value, "charge") ||
		strings.Contains(value, "paid") ||
		strings.Contains(value, "sell") ||
		strings.Contains(value, "customers") ||
		strings.Contains(value, "$")
}

func extractDollarRate(prompt string) string {
	match := dollarRatePattern.FindStringSubmatch(prompt)
	if len(match) == 2 {
		return match[1]
	}
	return "2.00"
}
