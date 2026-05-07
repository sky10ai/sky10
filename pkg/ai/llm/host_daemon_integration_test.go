package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skyconfig "github.com/sky10/sky10/pkg/config"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

const (
	liveLLMEnvFileEnv          = "SKY10_LLM_ENV_FILE"
	liveLLMGateEnv             = "SKY10_LLM_LIVE"
	liveOpenAIModelEnv         = "SKY10_LLM_LIVE_OPENAI_MODEL"
	liveAnthropicModelEnv      = "SKY10_LLM_LIVE_ANTHROPIC_MODEL"
	liveOpenAIModelDefault     = "gpt-5.4-mini"
	liveAnthropicModelDefault  = "claude-haiku-4-5"
	liveChatCompletionMaxToken = 360
	liveStreamingPrompt        = `You are validating an OpenAI-compatible streaming proxy.
Return exactly eight numbered lines, each 12 to 18 words.
Line 1 must include alpha-stream and describe request framing.
Line 2 must include beta-stream and describe response headers.
Line 3 must include gamma-stream and describe event decoding.
Line 4 must include delta-stream and describe token forwarding.
Line 5 must include epsilon-stream and describe cancellation.
Line 6 must include zeta-stream and describe provider auth.
Line 7 must include eta-stream and describe model routing.
Line 8 must include theta-stream and say validation complete.
No preamble, no markdown fence, no extra lines.`
)

var liveRequiredMarkers = []string{
	"alpha-stream",
	"beta-stream",
	"gamma-stream",
	"delta-stream",
	"epsilon-stream",
	"zeta-stream",
	"eta-stream",
	"theta-stream",
}

func TestHostDaemonLiveOpenAICompatibleEndpointStreamsOpenAI(t *testing.T) {
	requireLiveLLM(t)
	requireLiveAPIKey(t, DefaultOpenAIAPIKeyEnv)

	model := liveModel(liveOpenAIModelEnv, liveOpenAIModelDefault)
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:           "openai-live",
		Label:        "OpenAI Live",
		Provider:     ProviderOpenAI,
		BaseURL:      DefaultOpenAIBaseURL,
		DefaultModel: model,
		Auth: AuthConfig{
			Method:    AuthMethodAPIKey,
			APIKeyEnv: DefaultOpenAIAPIKeyEnv,
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}

	daemon := startLLMHostHTTPDaemon(t, store)
	assertDaemonModels(t, daemon, "openai-live", model)
	temperature := 0.0
	result := postStreamingChatCompletion(t, daemon, ChatCompletionRequest{
		Model:       "openai-live/" + model,
		Stream:      true,
		MaxTokens:   liveChatCompletionMaxToken,
		Temperature: &temperature,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: liveStreamingPrompt,
		}},
	})

	assertSuccessfulDaemonStream(t, result, model, liveRequiredMarkers)
}

func TestHostDaemonLiveOpenAICompatibleEndpointStreamsAnthropic(t *testing.T) {
	requireLiveLLM(t)
	requireLiveAPIKey(t, DefaultAnthropicAPIKeyEnv)

	model := liveModel(liveAnthropicModelEnv, liveAnthropicModelDefault)
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:           "anthropic-live",
		Label:        "Anthropic Live",
		Provider:     ProviderAnthropic,
		BaseURL:      DefaultAnthropicBaseURL,
		DefaultModel: model,
		Auth: AuthConfig{
			Method:     AuthMethodAPIKey,
			APIKeyEnv:  DefaultAnthropicAPIKeyEnv,
			APIVersion: DefaultAnthropicAPIVersion,
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}

	daemon := startLLMHostHTTPDaemon(t, store)
	assertDaemonModels(t, daemon, "anthropic-live", model)
	temperature := 0.0
	result := postStreamingChatCompletion(t, daemon, ChatCompletionRequest{
		Model:         "anthropic-live/" + model,
		Stream:        true,
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
		MaxTokens:     liveChatCompletionMaxToken,
		Temperature:   &temperature,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: liveStreamingPrompt,
		}},
	})

	assertSuccessfulDaemonStream(t, result, model, liveRequiredMarkers)
	if !result.hasUsage() {
		t.Fatalf("daemon stream missing usage chunk:\n%s", result.raw)
	}
}

func requireLiveLLM(t *testing.T) {
	t.Helper()

	loadLiveLLMEnv(t)
	if os.Getenv(liveLLMGateEnv) != "1" {
		t.Skipf("set %s=1 to run live LLM daemon integration tests", liveLLMGateEnv)
	}
}

func requireLiveAPIKey(t *testing.T, envName string) {
	t.Helper()

	if strings.TrimSpace(os.Getenv(envName)) == "" {
		t.Skipf("set %s to run this live LLM daemon integration test", envName)
	}
}

func loadLiveLLMEnv(t *testing.T) {
	t.Helper()

	explicitEnvFile := strings.TrimSpace(os.Getenv(liveLLMEnvFileEnv))
	for _, candidate := range liveLLMEnvSearchPaths(t) {
		loaded, err := loadLiveLLMEnvFile(candidate)
		if err != nil {
			if explicitEnvFile != "" && filepath.Clean(candidate) == filepath.Clean(explicitEnvFile) {
				t.Fatalf("load %s: %v", liveLLMEnvFileEnv, err)
			}
			continue
		}
		if loaded {
			return
		}
	}
}

func liveLLMEnvSearchPaths(t *testing.T) []string {
	t.Helper()

	seen := map[string]struct{}{}
	var out []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}

	add(os.Getenv(liveLLMEnvFileEnv))
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; {
			add(filepath.Join(dir, ".env"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if root, err := skyconfig.RootDir(); err == nil {
		add(filepath.Join(root, ".env"))
	}
	return out
}

func loadLiveLLMEnvFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		_ = os.Setenv(key, value)
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func liveModel(envName, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value
	}
	return fallback
}

func startLLMHostHTTPDaemon(t *testing.T, store *Store) string {
	t.Helper()

	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "test.sock"), "test", nil)
	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend:     NewConnectionChatBackend(store, ConnectionChatBackendOptions{}),
		ModelLister: StoreModelLister(store),
	})
	server.HandleHTTP("/v1/health", handler.HandleHealth)
	server.HandleHTTP("/v1/models", handler.HandleModels)
	server.HandleHTTP("/v1/chat/completions", handler.HandleChatCompletions)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ServeHTTP(ctx, 0)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for server.HTTPAddr() == "" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for daemon HTTP server")
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon HTTP server returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for daemon HTTP shutdown")
		}
	})

	return "http://" + server.HTTPAddr()
}

type daemonStreamResult struct {
	chunks []ChatCompletionStreamChunk
	done   bool
	raw    string
}

func (r daemonStreamResult) text() string {
	var out strings.Builder
	for _, chunk := range r.chunks {
		for _, choice := range chunk.Choices {
			out.WriteString(choice.Delta.Content)
		}
	}
	return out.String()
}

func (r daemonStreamResult) hasUsage() bool {
	for _, chunk := range r.chunks {
		if chunk.Usage != nil {
			return true
		}
	}
	return false
}

func (r daemonStreamResult) contentChunkCount() int {
	count := 0
	for _, chunk := range r.chunks {
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				count++
			}
		}
	}
	return count
}

func assertDaemonModels(t *testing.T, daemonURL string, expectedIDs ...string) {
	t.Helper()

	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(daemonURL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("read /v1/models error response: %v", readErr)
		}
		t.Fatalf("GET /v1/models status = %d, body = %s", resp.StatusCode, raw)
	}

	var models hostModelList
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		t.Fatalf("decode /v1/models response: %v", err)
	}
	if models.Object != "list" {
		t.Fatalf("/v1/models object = %q, want list", models.Object)
	}
	seen := make(map[string]struct{}, len(models.Data))
	for _, model := range models.Data {
		seen[model.ID] = struct{}{}
	}
	for _, expectedID := range expectedIDs {
		if _, ok := seen[expectedID]; !ok {
			t.Fatalf("/v1/models missing %q in %#v", expectedID, models.Data)
		}
	}
}

func postStreamingChatCompletion(t *testing.T, daemonURL string, req ChatCompletionRequest) daemonStreamResult {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encode chat completion request: %v", err)
	}

	client := http.Client{Timeout: 45 * time.Second}
	resp, err := client.Post(daemonURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("read daemon error response: %v", readErr)
		}
		t.Fatalf("POST /v1/chat/completions status = %d, body = %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if _, _, err := net.SplitHostPort(strings.TrimPrefix(daemonURL, "http://")); err != nil {
		t.Fatalf("daemon URL %q did not include host:port: %v", daemonURL, err)
	}

	return collectDaemonStream(t, resp.Body)
}

func collectDaemonStream(t *testing.T, r io.Reader) daemonStreamResult {
	t.Helper()

	result := daemonStreamResult{}
	var raw strings.Builder
	if err := scanServerSentEvents(context.Background(), r, func(event serverSentEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil
		}
		raw.WriteString("data: ")
		raw.WriteString(data)
		raw.WriteString("\n\n")
		if data == "[DONE]" {
			result.done = true
			return nil
		}
		var chunk ChatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode daemon stream chunk: %w", err)
		}
		result.chunks = append(result.chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("read daemon stream: %v", err)
	}
	result.raw = raw.String()
	return result
}

func assertSuccessfulDaemonStream(t *testing.T, result daemonStreamResult, model string, requiredMarkers []string) {
	t.Helper()

	if !result.done {
		t.Fatalf("daemon stream missing DONE:\n%s", result.raw)
	}
	if len(result.chunks) == 0 {
		t.Fatalf("daemon stream returned no chunks")
	}
	for _, chunk := range result.chunks {
		if chunk.Model != "" && !strings.Contains(chunk.Model, model) {
			t.Fatalf("daemon stream model = %q, want it to include %q", chunk.Model, model)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason == "error" {
				t.Fatalf("daemon stream returned error chunk: %s", choice.Delta.Content)
			}
		}
	}
	text := strings.TrimSpace(result.text())
	if text == "" {
		t.Fatalf("daemon stream returned no assistant text:\n%s", result.raw)
	}
	if count := result.contentChunkCount(); count < 2 {
		t.Fatalf("daemon stream returned %d content chunks, want at least 2:\n%s", count, result.raw)
	}
	lowerText := strings.ToLower(text)
	for _, marker := range requiredMarkers {
		if !strings.Contains(lowerText, marker) {
			t.Fatalf("daemon stream text missing marker %q:\n%s", marker, text)
		}
	}
}
