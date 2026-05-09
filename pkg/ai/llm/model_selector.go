package llm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const modelSelectorHelp = "use <model>, <provider>/<model>, or <connection>/<model>"

type parsedModelSelector struct {
	raw       string
	prefix    string
	model     string
	qualified bool
}

type resolvedModelSelector struct {
	Connection Connection
	Model      string
}

func parseModelSelector(raw string) (parsedModelSelector, error) {
	selector := strings.TrimSpace(raw)
	if selector == "" {
		return parsedModelSelector{}, fmt.Errorf("model is required; %s", modelSelectorHelp)
	}
	prefix, model, qualified := strings.Cut(selector, "/")
	if !qualified {
		return parsedModelSelector{
			raw:   selector,
			model: selector,
		}, nil
	}
	prefix = strings.TrimSpace(prefix)
	model = strings.TrimSpace(model)
	if prefix == "" {
		return parsedModelSelector{}, fmt.Errorf("model selector %q is invalid; %s", selector, modelSelectorHelp)
	}
	return parsedModelSelector{
		raw:       selector,
		prefix:    prefix,
		model:     model,
		qualified: true,
	}, nil
}

func resolveModelSelector(connections []Connection, raw string) (resolvedModelSelector, error) {
	connections = modelRoutableConnections(connections)
	if len(connections) == 0 {
		return resolvedModelSelector{}, errors.New("no OpenAI or Anthropic connections are configured")
	}
	selector, err := parseModelSelector(raw)
	if err != nil {
		return resolvedModelSelector{}, err
	}
	if selector.qualified {
		return resolveQualifiedModelSelector(connections, selector)
	}
	return resolveRawModelSelector(connections, selector.model)
}

func resolveQualifiedModelSelector(connections []Connection, selector parsedModelSelector) (resolvedModelSelector, error) {
	if connection, ok := connectionByID(connections, selector.prefix); ok {
		model, err := selectorModelOrDefault(selector.model, connection)
		if err != nil {
			return resolvedModelSelector{}, err
		}
		return resolvedModelSelector{Connection: connection, Model: model}, nil
	}

	if supportedProvider(selector.prefix) {
		providerConnections := connectionsByProvider(connections, selector.prefix)
		if len(providerConnections) == 0 {
			return resolvedModelSelector{}, fmt.Errorf("no %s connection is configured", selector.prefix)
		}
		return resolveWithinProvider(providerConnections, selector)
	}

	return resolvedModelSelector{}, fmt.Errorf("unknown provider or connection %q in model selector %q; %s", selector.prefix, selector.raw, modelSelectorHelp)
}

func resolveWithinProvider(connections []Connection, selector parsedModelSelector) (resolvedModelSelector, error) {
	if selector.model == "" {
		if len(connections) != 1 {
			return resolvedModelSelector{}, fmt.Errorf("model selector %q is ambiguous across %d %s connections; use <connection>/<model>", selector.raw, len(connections), selector.prefix)
		}
		model, err := selectorModelOrDefault("", connections[0])
		if err != nil {
			return resolvedModelSelector{}, err
		}
		return resolvedModelSelector{Connection: connections[0], Model: model}, nil
	}

	matches := connectionsWithModel(connections, selector.model)
	switch len(matches) {
	case 1:
		return resolvedModelSelector{Connection: matches[0], Model: selector.model}, nil
	case 0:
		if len(connections) == 1 {
			return resolvedModelSelector{Connection: connections[0], Model: selector.model}, nil
		}
		return resolvedModelSelector{}, fmt.Errorf("model selector %q is ambiguous across %d %s connections; use <connection>/<model>", selector.raw, len(connections), selector.prefix)
	default:
		return resolvedModelSelector{}, fmt.Errorf("model selector %q matches multiple %s connections; use <connection>/<model>", selector.raw, selector.prefix)
	}
}

func resolveRawModelSelector(connections []Connection, model string) (resolvedModelSelector, error) {
	if connection, ok := connectionByID(connections, model); ok {
		selectedModel, err := selectorModelOrDefault("", connection)
		if err != nil {
			return resolvedModelSelector{}, err
		}
		return resolvedModelSelector{Connection: connection, Model: selectedModel}, nil
	}

	matches := connectionsWithModel(connections, model)
	if len(matches) == 1 {
		return resolvedModelSelector{Connection: matches[0], Model: model}, nil
	}
	if len(matches) > 1 {
		return resolvedModelSelector{}, fmt.Errorf("model %q matches multiple connections; use <connection>/<model>", model)
	}

	provider := ProviderOpenAI
	if looksAnthropicModel(model) {
		provider = ProviderAnthropic
	}
	providerConnections := connectionsByProvider(connections, provider)
	if len(providerConnections) == 0 {
		return resolvedModelSelector{}, fmt.Errorf("no %s connection is configured for model %q", provider, model)
	}
	if len(providerConnections) > 1 {
		return resolvedModelSelector{}, fmt.Errorf("model %q is ambiguous across %d %s connections; use <connection>/<model>", model, len(providerConnections), provider)
	}
	return resolvedModelSelector{Connection: providerConnections[0], Model: model}, nil
}

func selectorModelOrDefault(model string, connection Connection) (string, error) {
	model = strings.TrimSpace(model)
	if model != "" {
		return model, nil
	}
	if strings.TrimSpace(connection.DefaultModel) != "" {
		return strings.TrimSpace(connection.DefaultModel), nil
	}
	return "", fmt.Errorf("connection %q does not have a default model; use %s/<model>", connection.ID, connection.ID)
}

func modelRoutableConnections(connections []Connection) []Connection {
	out := make([]Connection, 0, len(connections))
	for _, connection := range connections {
		if connection.Provider == ProviderOpenAI || connection.Provider == ProviderAnthropic {
			out = append(out, connection)
		}
	}
	return out
}

func connectionsByProvider(connections []Connection, provider string) []Connection {
	out := make([]Connection, 0, len(connections))
	for _, connection := range connections {
		if connection.Provider == provider {
			out = append(out, connection)
		}
	}
	return out
}

func connectionsWithModel(connections []Connection, model string) []Connection {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	var out []Connection
	for _, connection := range connections {
		if connectionHasModel(connection, model) {
			out = append(out, connection)
		}
	}
	return out
}

func connectionHasModel(connection Connection, model string) bool {
	if strings.TrimSpace(connection.DefaultModel) == model {
		return true
	}
	for _, candidate := range connection.Models {
		if strings.TrimSpace(candidate) == model {
			return true
		}
	}
	return false
}

func modelIDsForConnection(connection Connection) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, ok := seen[model]; ok {
			return
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	add(connection.DefaultModel)
	for _, model := range connection.Models {
		add(model)
	}
	sort.Strings(out)
	return out
}

func hostModelsForConnections(connections []Connection) []HostModel {
	connections = modelRoutableConnections(connections)
	rawCounts := map[string]int{}
	providerCounts := map[string]int{}
	for _, connection := range connections {
		for _, model := range modelIDsForConnection(connection) {
			rawCounts[model]++
			providerCounts[connection.Provider+"/"+model]++
		}
	}

	seen := map[string]struct{}{}
	var models []HostModel
	add := func(id string, connection Connection) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		models = append(models, HostModel{
			ID:      id,
			Object:  "model",
			OwnedBy: firstNonEmpty(strings.TrimSpace(connection.Label), connection.Provider, "sky10"),
		})
	}

	for _, connection := range connections {
		for _, model := range modelIDsForConnection(connection) {
			if rawCounts[model] == 1 {
				add(model, connection)
			}
			if providerCounts[connection.Provider+"/"+model] == 1 {
				add(connection.Provider+"/"+model, connection)
			}
			add(connection.ID+"/"+model, connection)
		}
	}

	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func connectionByID(connections []Connection, id string) (Connection, bool) {
	for _, connection := range connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return Connection{}, false
}

func looksAnthropicModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic.")
}
