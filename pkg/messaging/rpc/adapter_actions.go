package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingadapters "github.com/sky10/sky10/pkg/messengers/adapters"
	skysecrets "github.com/sky10/sky10/pkg/secrets"
)

const (
	messagingCredentialKind        = "messaging-credential"
	messagingCredentialContentType = "application/json"
)

// SecretWriter is the minimal secrets store surface needed by generic adapter
// setup. The broker later resolves the returned name through CredentialRef.
type SecretWriter interface {
	Put(context.Context, skysecrets.PutParams) (*skysecrets.SecretSummary, error)
}

type runAdapterActionParams struct {
	AdapterID       messaging.AdapterID        `json:"adapter_id"`
	ActionID        string                     `json:"action_id"`
	ConnectionID    messaging.ConnectionID     `json:"connection_id"`
	Label           string                     `json:"label,omitempty"`
	AuthMethod      messaging.AuthMethod       `json:"auth_method,omitempty"`
	DefaultPolicyID messaging.PolicyID         `json:"default_policy_id,omitempty"`
	Settings        map[string]json.RawMessage `json:"settings,omitempty"`
	SecretScope     string                     `json:"secret_scope,omitempty"`
}

type runAdapterActionResult struct {
	ActionID      string                         `json:"action_id"`
	ActionKind    messagingexternal.ActionKind   `json:"action_kind"`
	Connection    messaging.Connection           `json:"connection,omitempty"`
	Validation    *protocol.ValidateConfigResult `json:"validation,omitempty"`
	Connect       *messagingbroker.ConnectResult `json:"connect,omitempty"`
	URL           string                         `json:"url,omitempty"`
	CredentialRef string                         `json:"credential_ref,omitempty"`
}

func (h *Handler) rpcRunAdapterAction(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p runAdapterActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	info, action, err := h.lookupAdapterAction(p.AdapterID, p.ActionID)
	if err != nil {
		return nil, err
	}

	result := runAdapterActionResult{
		ActionID:   action.ID,
		ActionKind: action.Kind,
		URL:        action.URL,
	}

	switch action.Kind {
	case messagingexternal.ActionKindOpenURL:
		return result, nil
	case messagingexternal.ActionKindValidateConfig:
		connection, err := h.configureAdapterConnection(ctx, info, p, "")
		if err != nil {
			return nil, err
		}
		validation, err := h.broker.ValidateConnectionConfig(ctx, connection.ID)
		if err != nil {
			return nil, err
		}
		result.Connection = connection
		result.CredentialRef = connection.Auth.CredentialRef
		result.Validation = &validation
		return result, nil
	case messagingexternal.ActionKindConnect:
		connection, err := h.configureAdapterConnection(ctx, info, p, messaging.ConnectionStatusConnecting)
		if err != nil {
			return nil, err
		}
		connect, err := h.broker.ConnectConnection(ctx, connection.ID)
		if err != nil {
			return nil, err
		}
		result.Connection = connect.Connection
		result.CredentialRef = connect.Connection.Auth.CredentialRef
		result.Connect = &connect
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported adapter action kind %q", action.Kind)
	}
}

func (h *Handler) lookupAdapterAction(adapterID messaging.AdapterID, actionID string) (messagingexternal.AdapterInfo, messagingexternal.Action, error) {
	if strings.TrimSpace(string(adapterID)) == "" {
		return messagingexternal.AdapterInfo{}, messagingexternal.Action{}, fmt.Errorf("adapter_id is required")
	}
	if strings.TrimSpace(actionID) == "" {
		return messagingexternal.AdapterInfo{}, messagingexternal.Action{}, fmt.Errorf("action_id is required")
	}
	info, ok := h.externalAdapters.Info(adapterID)
	if !ok {
		builtin, found := messagingadapters.Lookup(string(adapterID))
		if !found || len(builtin.Settings) == 0 {
			return messagingexternal.AdapterInfo{}, messagingexternal.Action{}, fmt.Errorf("messaging adapter %q is not registered with generic settings", adapterID)
		}
		info = builtinAdapterInfoForActions(builtin)
	}
	for _, action := range info.Actions {
		if action.ID == actionID {
			return info, action, nil
		}
	}
	return messagingexternal.AdapterInfo{}, messagingexternal.Action{}, fmt.Errorf("adapter %q action %q is not registered", adapterID, actionID)
}

// builtinAdapterInfoForActions synthesizes the AdapterInfo shape that
// configureAdapterConnection expects from a built-in Definition.
func builtinAdapterInfoForActions(item messagingadapters.Definition) messagingexternal.AdapterInfo {
	adapter := item.Adapter
	if strings.TrimSpace(string(adapter.ID)) == "" {
		adapter.ID = messaging.AdapterID(item.Name)
	}
	if strings.TrimSpace(adapter.DisplayName) == "" {
		adapter.DisplayName = item.Name
	}
	if strings.TrimSpace(adapter.Description) == "" {
		adapter.Description = item.Summary
	}
	return messagingexternal.AdapterInfo{
		Adapter:  adapter,
		Settings: item.Settings,
		Actions:  item.Actions,
	}
}

func (h *Handler) configureAdapterConnection(ctx context.Context, info messagingexternal.AdapterInfo, p runAdapterActionParams, defaultStatus messaging.ConnectionStatus) (messaging.Connection, error) {
	if h.processResolver == nil {
		return messaging.Connection{}, fmt.Errorf("messaging process resolver is not configured")
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return messaging.Connection{}, fmt.Errorf("connection_id is required")
	}

	connection := messaging.Connection{
		ID:        p.ConnectionID,
		AdapterID: info.Adapter.ID,
		Label:     strings.TrimSpace(p.Label),
		Status:    defaultStatus,
		Metadata:  make(map[string]string),
	}
	if existing, ok := h.store.GetConnection(p.ConnectionID); ok {
		if existing.AdapterID != info.Adapter.ID {
			return messaging.Connection{}, fmt.Errorf("connection %q already uses adapter %q", p.ConnectionID, existing.AdapterID)
		}
		connection = cloneRPCConnection(existing)
		if strings.TrimSpace(p.Label) != "" {
			connection.Label = strings.TrimSpace(p.Label)
		}
		if strings.TrimSpace(string(defaultStatus)) != "" {
			connection.Status = defaultStatus
		}
		if connection.Metadata == nil {
			connection.Metadata = make(map[string]string)
		}
	}
	if strings.TrimSpace(connection.Label) == "" {
		connection.Label = info.Adapter.DisplayName
	}
	if p.DefaultPolicyID != "" {
		connection.DefaultPolicyID = p.DefaultPolicyID
	}

	credentialValues, err := h.applyAdapterSettings(ctx, &connection, info.Settings, p)
	if err != nil {
		return messaging.Connection{}, err
	}
	if len(credentialValues) > 0 {
		ref, err := h.storeAdapterCredential(ctx, info.Adapter.ID, connection.ID, credentialValues, p.SecretScope)
		if err != nil {
			return messaging.Connection{}, err
		}
		connection.Auth.CredentialRef = ref
	}
	if strings.TrimSpace(string(p.AuthMethod)) != "" {
		connection.Auth.Method = p.AuthMethod
	}
	if strings.TrimSpace(string(connection.Auth.Method)) == "" {
		connection.Auth.Method = defaultAuthMethod(info.Adapter)
	}
	if err := connection.Validate(); err != nil {
		return messaging.Connection{}, err
	}

	process, err := h.processResolver(string(connection.AdapterID))
	if err != nil {
		return messaging.Connection{}, err
	}
	if err := h.broker.UpsertConnection(ctx, messagingbroker.RegisterConnectionParams{
		Connection: connection,
		Process:    process,
	}); err != nil {
		return messaging.Connection{}, err
	}
	if stored, ok := h.store.GetConnection(connection.ID); ok {
		return stored, nil
	}
	return connection, nil
}

func (h *Handler) applyAdapterSettings(_ context.Context, connection *messaging.Connection, settings []messagingexternal.Setting, p runAdapterActionParams) (map[string]string, error) {
	credentialValues := make(map[string]string)
	for _, setting := range settings {
		value, present, err := settingValue(p.Settings, setting)
		if err != nil {
			return nil, fmt.Errorf("setting %s: %w", setting.Key, err)
		}
		if !present {
			if setting.Required && setting.Target == messagingexternal.SettingTargetCredential && strings.TrimSpace(connection.Auth.CredentialRef) == "" {
				return nil, fmt.Errorf("setting %s is required", setting.Key)
			}
			continue
		}
		if setting.Required && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("setting %s is required", setting.Key)
		}
		switch setting.Target {
		case messagingexternal.SettingTargetMetadata:
			if strings.TrimSpace(value) == "" {
				delete(connection.Metadata, setting.Key)
			} else {
				connection.Metadata[setting.Key] = value
			}
		case messagingexternal.SettingTargetAuth:
			if err := applyAuthSetting(&connection.Auth, setting.Key, value); err != nil {
				return nil, err
			}
		case messagingexternal.SettingTargetCredential:
			if strings.TrimSpace(value) != "" {
				credentialValues[setting.Key] = value
			}
		default:
			return nil, fmt.Errorf("unsupported setting target %q", setting.Target)
		}
	}
	return credentialValues, nil
}

func (h *Handler) storeAdapterCredential(ctx context.Context, adapterID messaging.AdapterID, connectionID messaging.ConnectionID, values map[string]string, scope string) (string, error) {
	if h.secretWriter == nil {
		return "", fmt.Errorf("messaging secret writer is not configured")
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal messaging credential: %w", err)
	}
	name := adapterCredentialRef(adapterID, connectionID)
	_, err = h.secretWriter.Put(ctx, skysecrets.PutParams{
		Name:        name,
		Kind:        messagingCredentialKind,
		ContentType: messagingCredentialContentType,
		Scope:       scope,
		Payload:     raw,
	})
	if err != nil {
		return "", fmt.Errorf("store messaging credential: %w", err)
	}
	return name, nil
}

func settingValue(values map[string]json.RawMessage, setting messagingexternal.Setting) (string, bool, error) {
	raw, ok := values[setting.Key]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if strings.TrimSpace(setting.Default) != "" && setting.Target != messagingexternal.SettingTargetCredential {
			return setting.Default, true, nil
		}
		return "", false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text), true, nil
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		return strconv.FormatBool(flag), true, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String(), true, nil
	}
	return "", false, fmt.Errorf("value must be a string, number, boolean, or null")
}

func applyAuthSetting(auth *messaging.AuthInfo, key, value string) error {
	switch key {
	case "method", "auth_method":
		auth.Method = messaging.AuthMethod(value)
	case "external_account":
		auth.ExternalAccount = value
	case "tenant_id":
		auth.TenantID = value
	case "scopes":
		auth.Scopes = splitCSV(value)
	default:
		return fmt.Errorf("unsupported auth setting key %q", key)
	}
	return nil
}

func defaultAuthMethod(adapter messaging.Adapter) messaging.AuthMethod {
	for _, method := range adapter.AuthMethods {
		if strings.TrimSpace(string(method)) != "" && method != messaging.AuthMethodNone {
			return method
		}
	}
	return messaging.AuthMethodNone
}

func adapterCredentialRef(adapterID messaging.AdapterID, connectionID messaging.ConnectionID) string {
	encodedConnectionID := base64.RawURLEncoding.EncodeToString([]byte(connectionID))
	return "secret://messaging/" + string(adapterID) + "/" + encodedConnectionID + "/credential"
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloneRPCConnection(connection messaging.Connection) messaging.Connection {
	if connection.Metadata != nil {
		connection.Metadata = cloneRPCStringMap(connection.Metadata)
	}
	if connection.Auth.Scopes != nil {
		connection.Auth.Scopes = append([]string(nil), connection.Auth.Scopes...)
	}
	return connection
}

func cloneRPCStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
