package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveModelSelectorRawOpenAIModel(t *testing.T) {
	t.Parallel()

	resolved, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default", "gpt-5.4-mini"),
		testSelectorConnection("anthropic-work", "Anthropic Work", ProviderAnthropic, "claude-default"),
	}, "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("resolveModelSelector() error = %v", err)
	}
	if resolved.Connection.ID != "openai-work" || resolved.Model != "gpt-5.4-mini" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveModelSelectorRawAnthropicModel(t *testing.T) {
	t.Parallel()

	resolved, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default"),
		testSelectorConnection("anthropic-work", "Anthropic Work", ProviderAnthropic, "claude-default"),
	}, "claude-opus-4.7")
	if err != nil {
		t.Fatalf("resolveModelSelector() error = %v", err)
	}
	if resolved.Connection.ID != "anthropic-work" || resolved.Model != "claude-opus-4.7" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveModelSelectorProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	resolved, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default"),
		testSelectorConnection("anthropic-work", "Anthropic Work", ProviderAnthropic, "claude-default"),
	}, "anthropic/claude-test")
	if err != nil {
		t.Fatalf("resolveModelSelector() error = %v", err)
	}
	if resolved.Connection.ID != "anthropic-work" || resolved.Model != "claude-test" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveModelSelectorConnectionQualifiedModel(t *testing.T) {
	t.Parallel()

	resolved, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default"),
		testSelectorConnection("anthropic-work", "Anthropic Work", ProviderAnthropic, "claude-default"),
	}, "anthropic-work/claude-test")
	if err != nil {
		t.Fatalf("resolveModelSelector() error = %v", err)
	}
	if resolved.Connection.ID != "anthropic-work" || resolved.Model != "claude-test" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveModelSelectorVeniceModelKeepsProviderSlash(t *testing.T) {
	t.Parallel()

	resolved, err := resolveModelSelector([]Connection{
		testSelectorConnection("bovilus-venice", "Bovilus Venice", ProviderVenice, DefaultVeniceModel, "anthropic/opus-4-7"),
	}, "venice/anthropic/opus-4-7")
	if err != nil {
		t.Fatalf("resolveModelSelector() error = %v", err)
	}
	if resolved.Connection.ID != "bovilus-venice" || resolved.Model != "anthropic/opus-4-7" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveModelSelectorVeniceConnectionQualifiedModel(t *testing.T) {
	t.Parallel()

	resolved, err := resolveModelSelector([]Connection{
		testSelectorConnection("bovilus-venice", "Bovilus Venice", ProviderVenice, DefaultVeniceModel),
	}, "bovilus-venice/anthropic/opus-4-7")
	if err != nil {
		t.Fatalf("resolveModelSelector() error = %v", err)
	}
	if resolved.Connection.ID != "bovilus-venice" || resolved.Model != "anthropic/opus-4-7" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestResolveModelSelectorUnknownProviderOrConnection(t *testing.T) {
	t.Parallel()

	_, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default"),
	}, "missing/gpt-test")
	if err == nil {
		t.Fatal("resolveModelSelector() error = nil, want unknown selector error")
	}
	if !strings.Contains(err.Error(), "unknown provider or connection") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveModelSelectorAmbiguousRawModel(t *testing.T) {
	t.Parallel()

	_, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default", "gpt-shared"),
		testSelectorConnection("openai-personal", "OpenAI Personal", ProviderOpenAI, "gpt-other", "gpt-shared"),
	}, "gpt-shared")
	if err == nil {
		t.Fatal("resolveModelSelector() error = nil, want ambiguous model error")
	}
	if !strings.Contains(err.Error(), "matches multiple connections") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveModelSelectorAmbiguousProviderModel(t *testing.T) {
	t.Parallel()

	_, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default"),
		testSelectorConnection("openai-personal", "OpenAI Personal", ProviderOpenAI, "gpt-other"),
	}, "openai/gpt-new")
	if err == nil {
		t.Fatal("resolveModelSelector() error = nil, want ambiguous provider error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveModelSelectorConnectionWithoutDefaultModel(t *testing.T) {
	t.Parallel()

	_, err := resolveModelSelector([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, ""),
	}, "openai-work")
	if err == nil {
		t.Fatal("resolveModelSelector() error = nil, want missing default model error")
	}
	if !strings.Contains(err.Error(), "does not have a default model") {
		t.Fatalf("error = %v", err)
	}
}

func TestHostModelsForConnectionsListsResolvableSelectors(t *testing.T) {
	t.Parallel()

	models := hostModelsForConnections([]Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-shared", "gpt-work"),
		testSelectorConnection("openai-personal", "OpenAI Personal", ProviderOpenAI, "gpt-shared", "gpt-personal"),
		testSelectorConnection("anthropic-work", "Anthropic Work", ProviderAnthropic, "claude-default"),
		testSelectorConnection("bovilus-venice", "Bovilus Venice", ProviderVenice, DefaultVeniceModel, "anthropic/opus-4-7"),
	})
	ids := make(map[string]HostModel, len(models))
	for _, model := range models {
		ids[model.ID] = model
	}
	for _, want := range []string{
		"gpt-work",
		"gpt-personal",
		"openai-work/gpt-shared",
		"openai-personal/gpt-shared",
		"anthropic/claude-default",
		"anthropic-work/claude-default",
		"venice/anthropic/opus-4-7",
		"bovilus-venice/anthropic/opus-4-7",
	} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("models missing %q in %+v", want, models)
		}
	}
	if _, ok := ids["gpt-shared"]; ok {
		t.Fatalf("ambiguous raw model was listed: %+v", models)
	}
	if _, ok := ids["openai-work"]; ok {
		t.Fatalf("standalone connection id was listed: %+v", models)
	}
	if ids["openai-work/gpt-shared"].OwnedBy != "OpenAI Work" {
		t.Fatalf("OwnedBy = %q", ids["openai-work/gpt-shared"].OwnedBy)
	}
}

func TestChatAndResponsesUseSameModelSelector(t *testing.T) {
	t.Parallel()

	connections := []Connection{
		testSelectorConnection("openai-work", "OpenAI Work", ProviderOpenAI, "gpt-default"),
		testSelectorConnection("anthropic-work", "Anthropic Work", ProviderAnthropic, "claude-default"),
	}

	chatResolved, err := resolveModelSelector(connections, "anthropic/claude-test")
	if err != nil {
		t.Fatalf("chat resolve error = %v", err)
	}
	respChatReq, err := ConvertResponsesRequestToChat(ResponsesRequest{
		Model: "anthropic/claude-test",
		Input: json.RawMessage(`"hello"`),
	})
	if err != nil {
		t.Fatalf("responses convert error = %v", err)
	}
	responseResolved, err := resolveModelSelector(connections, respChatReq.Model)
	if err != nil {
		t.Fatalf("responses resolve error = %v", err)
	}
	if chatResolved.Connection.ID != responseResolved.Connection.ID || chatResolved.Model != responseResolved.Model {
		t.Fatalf("chat resolved %+v, responses resolved %+v", chatResolved, responseResolved)
	}
}

func testSelectorConnection(id, label, provider, defaultModel string, models ...string) Connection {
	return Connection{
		ID:           id,
		Label:        label,
		Provider:     provider,
		BaseURL:      "https://example.test/v1",
		DefaultModel: defaultModel,
		Models:       models,
		Auth:         AuthConfig{Method: AuthMethodAPIKey, APIKeyEnv: "TEST_API_KEY"},
	}
}
