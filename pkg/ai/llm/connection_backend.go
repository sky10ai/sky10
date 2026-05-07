package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SecretResolver resolves credential references stored in Connection.Auth.
type SecretResolver interface {
	ResolveSecret(context.Context, string) (string, error)
}

// SecretResolverFunc adapts a function into SecretResolver.
type SecretResolverFunc func(context.Context, string) (string, error)

func (fn SecretResolverFunc) ResolveSecret(ctx context.Context, ref string) (string, error) {
	return fn(ctx, ref)
}

// ConnectionChatBackend routes host OpenAI-compatible chat requests through
// saved OpenAI and Anthropic connections.
type ConnectionChatBackend struct {
	store          *Store
	secretResolver SecretResolver
	httpClient     *http.Client
	now            func() time.Time
}

type ConnectionChatBackendOptions struct {
	SecretResolver SecretResolver
	HTTPClient     *http.Client
	Now            func() time.Time
}

func NewConnectionChatBackend(store *Store, opts ConnectionChatBackendOptions) *ConnectionChatBackend {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ConnectionChatBackend{
		store:          store,
		secretResolver: opts.SecretResolver,
		httpClient:     opts.HTTPClient,
		now:            now,
	}
}

func (b *ConnectionChatBackend) ChatCompletions(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	adapter, routedReq, err := b.adapterForRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	routedReq.Stream = false
	return adapter.ChatCompletions(ctx, routedReq)
}

func (b *ConnectionChatBackend) StreamChatCompletions(ctx context.Context, req ChatCompletionRequest, send func(ChatCompletionStreamChunk) error) error {
	adapter, routedReq, err := b.adapterForRequest(ctx, req)
	if err != nil {
		return err
	}
	routedReq.Stream = true
	streaming, ok := adapter.(StreamingChatAdapter)
	if !ok {
		return ErrStreamingNotImplemented
	}
	return streaming.StreamChatCompletions(ctx, routedReq, send)
}

func (b *ConnectionChatBackend) adapterForRequest(ctx context.Context, req ChatCompletionRequest) (ChatAdapter, ChatCompletionRequest, error) {
	if b == nil {
		return nil, ChatCompletionRequest{}, ErrHostBackendNotConfigured
	}
	connection, model, err := b.resolveConnection(req.Model)
	if err != nil {
		return nil, ChatCompletionRequest{}, err
	}
	if connection.Provider == ProviderVenice {
		return nil, ChatCompletionRequest{}, fmt.Errorf("venice guest provider is not wired yet")
	}
	apiKey, err := b.resolveAPIKey(ctx, connection)
	if err != nil {
		return nil, ChatCompletionRequest{}, err
	}
	adapter, err := NewNativeAdapter(connection, AdapterOptions{
		APIKey:           apiKey,
		HTTPClient:       b.httpClient,
		Model:            model,
		AnthropicVersion: connection.Auth.APIVersion,
		Now:              b.now,
	})
	if err != nil {
		return nil, ChatCompletionRequest{}, err
	}
	routedReq := req
	routedReq.Model = model
	return adapter, routedReq, nil
}

func (b *ConnectionChatBackend) resolveAPIKey(ctx context.Context, connection Connection) (string, error) {
	if strings.TrimSpace(connection.Auth.SecretRef) == "" {
		return "", nil
	}
	if b.secretResolver == nil {
		return "", fmt.Errorf("%s secret_ref %q cannot be resolved", connection.Provider, connection.Auth.SecretRef)
	}
	apiKey, err := b.secretResolver.ResolveSecret(ctx, connection.Auth.SecretRef)
	if err != nil {
		return "", fmt.Errorf("resolve %s secret_ref %q: %w", connection.Provider, connection.Auth.SecretRef, err)
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("%s secret_ref %q resolved to an empty api key", connection.Provider, connection.Auth.SecretRef)
	}
	return apiKey, nil
}

func (b *ConnectionChatBackend) resolveConnection(model string) (Connection, string, error) {
	connections, err := b.availableConnections()
	if err != nil {
		return Connection{}, "", err
	}
	if len(connections) == 0 {
		return Connection{}, "", fmt.Errorf("no AI connections are configured")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		connection, ok := firstConnectionForProvider(connections, ProviderOpenAI)
		if !ok {
			connection = firstNonVeniceConnection(connections)
		}
		return connection, defaultModelForConnection(connection), nil
	}

	if prefix, rest, ok := strings.Cut(model, "/"); ok {
		prefix = strings.TrimSpace(prefix)
		rest = strings.TrimSpace(rest)
		if connection, ok := connectionByID(connections, prefix); ok {
			return connection, firstNonEmpty(rest, defaultModelForConnection(connection)), nil
		}
		if supportedProvider(prefix) {
			connection, ok := firstConnectionForProvider(connections, prefix)
			if !ok {
				return Connection{}, "", fmt.Errorf("no %s connection is configured", prefix)
			}
			return connection, firstNonEmpty(rest, defaultModelForConnection(connection)), nil
		}
	}

	if connection, ok := connectionByID(connections, model); ok {
		return connection, defaultModelForConnection(connection), nil
	}
	if connection, ok := connectionByModel(connections, model); ok {
		return connection, model, nil
	}
	if looksAnthropicModel(model) {
		if connection, ok := firstConnectionForProvider(connections, ProviderAnthropic); ok {
			return connection, model, nil
		}
	}
	if connection, ok := firstConnectionForProvider(connections, ProviderOpenAI); ok {
		return connection, model, nil
	}
	connection := firstNonVeniceConnection(connections)
	if connection.ID == "" {
		return Connection{}, "", fmt.Errorf("no OpenAI or Anthropic connection is configured")
	}
	return connection, model, nil
}

func (b *ConnectionChatBackend) availableConnections() ([]Connection, error) {
	var connections []Connection
	if b.store != nil {
		listed, err := b.store.List()
		if err != nil {
			return nil, err
		}
		connections = append(connections, listed...)
	}
	connections = appendMissingDefaultConnection(connections, defaultOpenAIConnection())
	connections = appendMissingDefaultConnection(connections, defaultAnthropicConnection())
	return connections, nil
}

func appendMissingDefaultConnection(connections []Connection, fallback Connection) []Connection {
	for _, connection := range connections {
		if connection.ID == fallback.ID || connection.Provider == fallback.Provider {
			return connections
		}
	}
	return append(connections, fallback)
}

func defaultOpenAIConnection() Connection {
	return Connection{
		ID:           DefaultOpenAIConnectionID,
		Label:        DefaultOpenAILabel,
		Provider:     ProviderOpenAI,
		BaseURL:      DefaultOpenAIBaseURL,
		DefaultModel: DefaultOpenAIModel,
		Auth: AuthConfig{
			Method:    AuthMethodAPIKey,
			APIKeyEnv: DefaultOpenAIAPIKeyEnv,
		},
	}
}

func defaultAnthropicConnection() Connection {
	return Connection{
		ID:           DefaultAnthropicConnectionID,
		Label:        DefaultAnthropicLabel,
		Provider:     ProviderAnthropic,
		BaseURL:      DefaultAnthropicBaseURL,
		DefaultModel: DefaultAnthropicModel,
		Auth: AuthConfig{
			Method:     AuthMethodAPIKey,
			APIKeyEnv:  DefaultAnthropicAPIKeyEnv,
			APIVersion: DefaultAnthropicAPIVersion,
		},
	}
}

func connectionByID(connections []Connection, id string) (Connection, bool) {
	for _, connection := range connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return Connection{}, false
}

func connectionByModel(connections []Connection, model string) (Connection, bool) {
	for _, connection := range connections {
		if connection.DefaultModel == model {
			return connection, true
		}
		for _, candidate := range connection.Models {
			if candidate == model {
				return connection, true
			}
		}
	}
	return Connection{}, false
}

func firstConnectionForProvider(connections []Connection, provider string) (Connection, bool) {
	for _, connection := range connections {
		if connection.Provider == provider {
			return connection, true
		}
	}
	return Connection{}, false
}

func firstNonVeniceConnection(connections []Connection) Connection {
	for _, connection := range connections {
		if connection.Provider == ProviderOpenAI || connection.Provider == ProviderAnthropic {
			return connection
		}
	}
	return Connection{}
}

func defaultModelForConnection(connection Connection) string {
	switch connection.Provider {
	case ProviderOpenAI:
		return firstNonEmpty(connection.DefaultModel, DefaultOpenAIModel)
	case ProviderAnthropic:
		return firstNonEmpty(connection.DefaultModel, DefaultAnthropicModel)
	case ProviderVenice:
		return firstNonEmpty(connection.DefaultModel, DefaultVeniceModel)
	default:
		return connection.DefaultModel
	}
}

func looksAnthropicModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic.")
}
