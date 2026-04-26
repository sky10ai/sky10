package secrets

import (
	"strings"
	"time"
)

const (
	// DefaultNamespace is the internal KV namespace used for secrets metadata
	// and encrypted artifacts.
	DefaultNamespace = "secrets"

	// KindBlob is the generic artifact type for arbitrary binary payloads.
	KindBlob = "blob"

	// KindOWSBackup marks an encrypted OWS backup/export artifact.
	KindOWSBackup = "ows-backup"

	// RequesterOwner is the trusted local owner path.
	RequesterOwner = "owner"

	// RequesterAgent is a deferred internal path for future brokered access.
	// It is not part of the public secrets v1 surface.
	RequesterAgent = "agent"

	// ScopeCurrent stores a secret only for the current device.
	ScopeCurrent = "current"

	// ScopeTrusted stores a secret for all trusted devices in the manifest.
	ScopeTrusted = "trusted"

	// ScopeExplicit stores a secret for an explicit pinned device set.
	ScopeExplicit = "explicit"
)

// AccessPolicy is deferred metadata for future brokered agent access. It is
// not part of the public secrets v1 boundary.
type AccessPolicy struct {
	AllowedAgents   []string `json:"allowed_agents,omitempty"`
	RequireApproval bool     `json:"require_approval,omitempty"`
}

// IsZero reports whether the policy was left unset.
func (p AccessPolicy) IsZero() bool {
	return len(p.AllowedAgents) == 0 && !p.RequireApproval
}

// Requester identifies the logical caller for a secrets read.
type Requester struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

// PutParams creates a new secret or a new version of an existing secret.
type PutParams struct {
	ID                 string
	Name               string
	Kind               string
	ContentType        string
	Scope              string
	Payload            []byte
	RecipientDeviceIDs []string
	Policy             AccessPolicy
}

// RewrapParams rotates a secret to a fresh data key and recipient set.
type RewrapParams struct {
	IDOrName           string
	Scope              string
	RecipientDeviceIDs []string
	Policy             AccessPolicy
}

// DeleteParams identifies a secret to delete.
type DeleteParams struct {
	IDOrName string
}

// SecretSummary is the non-sensitive metadata returned by list/status APIs.
type SecretSummary struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	ContentType        string            `json:"content_type"`
	Scope              string            `json:"scope"`
	Size               int64             `json:"size"`
	SHA256             string            `json:"sha256"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	RecipientDeviceIDs []string          `json:"recipient_device_ids"`
	Policy             AccessPolicy      `json:"policy,omitempty"`
	References         []SecretReference `json:"references,omitempty"`
}

// SecretReference describes another subsystem that owns or relies on a secret.
// Surfacing references in list/get responses lets the UI flag secrets that
// shouldn't be edited directly (e.g. messaging-managed credentials).
type SecretReference struct {
	// Kind is a stable identifier for the reference type, e.g.
	// "messaging-connection".
	Kind string `json:"kind"`
	// Manager is a stable identifier for the owning subsystem (e.g.
	// "messaging"). The UI may group references by manager.
	Manager string `json:"manager"`
	// Subject is a human-readable label, e.g. the connection label or name.
	Subject string `json:"subject,omitempty"`
	// Detail is an optional secondary label, e.g. the adapter id.
	Detail string `json:"detail,omitempty"`
	// Route is an optional UI deep link, e.g. "/settings/messaging".
	Route string `json:"route,omitempty"`
}

// Secret is a decrypted secret artifact.
type Secret struct {
	SecretSummary
	VersionID string `json:"version_id"`
	Payload   []byte `json:"-"`
}

// Device describes one authorized device that can be selected as a recipient.
type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Current bool   `json:"current"`
}

// NormalizeSecretScope returns the canonical scope name or an empty string
// for unknown scopes.
func NormalizeSecretScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", ScopeCurrent:
		return ScopeCurrent
	case ScopeTrusted:
		return ScopeTrusted
	case ScopeExplicit:
		return ScopeExplicit
	default:
		return ""
	}
}

// InferSecretScope preserves older scope-less records by deriving the most
// conservative scope from their resolved recipient set.
func InferSecretScope(scope string, recipientDeviceIDs []string, currentDeviceID string) string {
	if normalized := NormalizeSecretScope(scope); normalized != "" {
		return normalized
	}
	if len(recipientDeviceIDs) == 0 {
		return ScopeCurrent
	}
	if len(recipientDeviceIDs) == 1 && recipientDeviceIDs[0] == currentDeviceID {
		return ScopeCurrent
	}
	return ScopeExplicit
}
