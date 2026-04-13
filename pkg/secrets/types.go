package secrets

import "time"

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

	// RequesterAgent is the soft-gated agent access path. Full caller auth
	// still requires daemon-level RPC authentication.
	RequesterAgent = "agent"
)

// AccessPolicy controls how agent-mode requests are evaluated.
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
	Payload            []byte
	RecipientDeviceIDs []string
	Policy             AccessPolicy
}

// RewrapParams rotates a secret to a fresh data key and recipient set.
type RewrapParams struct {
	IDOrName           string
	RecipientDeviceIDs []string
	Policy             AccessPolicy
}

// SecretSummary is the non-sensitive metadata returned by list/status APIs.
type SecretSummary struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Kind               string       `json:"kind"`
	ContentType        string       `json:"content_type"`
	Size               int64        `json:"size"`
	SHA256             string       `json:"sha256"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	RecipientDeviceIDs []string     `json:"recipient_device_ids"`
	Policy             AccessPolicy `json:"policy,omitempty"`
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
	Current bool   `json:"current"`
}
