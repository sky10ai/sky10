package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"go.yaml.in/yaml/v2"
)

const (
	// DocumentKind is the canonical kind for messaging policy documents.
	DocumentKind = "sky10.messaging.policy"
	// DocumentVersion is the current policy document schema version.
	DocumentVersion = "v1alpha1"

	actorTypeAI     = "ai"
	actorTypeHuman  = "human"
	actorTypeSystem = "system"
)

// Document is the human/AI-authored policy file format. The broker still
// enforces compiled messaging.Policy records; this layer preserves the user's
// conversational intent and review provenance.
type Document struct {
	Kind        string       `json:"kind" yaml:"kind"`
	Version     string       `json:"version" yaml:"version"`
	Intent      string       `json:"intent" yaml:"intent"`
	Policy      PolicySpec   `json:"policy" yaml:"policy"`
	Bindings    []Binding    `json:"bindings,omitempty" yaml:"bindings,omitempty"`
	GeneratedBy *Actor       `json:"generated_by,omitempty" yaml:"generated_by,omitempty"`
	Review      Review       `json:"review,omitempty" yaml:"review,omitempty"`
	Metadata    MetadataSpec `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// PolicySpec is the compiled policy section inside a policy document.
type PolicySpec struct {
	ID       messaging.PolicyID `json:"id" yaml:"id"`
	Name     string             `json:"name" yaml:"name"`
	Rules    Rules              `json:"rules" yaml:"rules"`
	Metadata MetadataSpec       `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// MetadataSpec stores string metadata without exposing map aliasing.
type MetadataSpec map[string]string

// Rules mirrors messaging.PolicyRules with file-friendly YAML tags.
type Rules struct {
	ReadInbound           bool                    `json:"read_inbound" yaml:"read_inbound"`
	CreateDrafts          bool                    `json:"create_drafts" yaml:"create_drafts"`
	SendMessages          bool                    `json:"send_messages" yaml:"send_messages"`
	RequireApproval       bool                    `json:"require_approval" yaml:"require_approval"`
	ReplyOnly             bool                    `json:"reply_only" yaml:"reply_only"`
	AllowNewConversations bool                    `json:"allow_new_conversations" yaml:"allow_new_conversations"`
	AllowAttachments      bool                    `json:"allow_attachments" yaml:"allow_attachments"`
	MarkRead              bool                    `json:"mark_read" yaml:"mark_read"`
	ManageMessages        bool                    `json:"manage_messages" yaml:"manage_messages"`
	AllowedContainerIDs   []messaging.ContainerID `json:"allowed_container_ids,omitempty" yaml:"allowed_container_ids,omitempty"`
	SearchIdentities      bool                    `json:"search_identities" yaml:"search_identities"`
	SearchConversations   bool                    `json:"search_conversations" yaml:"search_conversations"`
	SearchMessages        bool                    `json:"search_messages" yaml:"search_messages"`
	AllowedIdentityIDs    []messaging.IdentityID  `json:"allowed_identity_ids,omitempty" yaml:"allowed_identity_ids,omitempty"`
}

// Binding declares where a compiled policy should be attached when applied.
type Binding struct {
	ConnectionID messaging.ConnectionID `json:"connection_id,omitempty" yaml:"connection_id,omitempty"`
	ExposureID   messaging.ExposureID   `json:"exposure_id,omitempty" yaml:"exposure_id,omitempty"`
}

// Actor records who or what produced the current policy document.
type Actor struct {
	Type      string     `json:"type" yaml:"type"`
	Name      string     `json:"name" yaml:"name"`
	Model     string     `json:"model,omitempty" yaml:"model,omitempty"`
	PromptRef string     `json:"prompt_ref,omitempty" yaml:"prompt_ref,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`
}

// Review records the human review state for an authored policy document.
type Review struct {
	Required   bool       `json:"required" yaml:"required"`
	ApprovedBy string     `json:"approved_by,omitempty" yaml:"approved_by,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty" yaml:"approved_at,omitempty"`
	Notes      string     `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// LoadDocument reads and strictly decodes a JSON or YAML policy document.
func LoadDocument(path string) (Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read policy document: %w", err)
	}
	doc, err := ParseDocument(raw, documentFormat(path))
	if err != nil {
		return Document{}, fmt.Errorf("parse policy document %s: %w", path, err)
	}
	return doc, nil
}

// ParseDocument strictly decodes a policy document in json, yaml, or yml form.
func ParseDocument(raw []byte, format string) (Document, error) {
	var doc Document
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), ".")) {
	case "json":
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&doc); err != nil {
			return Document{}, err
		}
	case "yaml", "yml", "":
		if err := yaml.UnmarshalStrict(raw, &doc); err != nil {
			return Document{}, err
		}
	default:
		return Document{}, fmt.Errorf("unsupported policy document format %q", format)
	}
	if err := doc.Validate(); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// Validate checks document structure without requiring final human approval.
func (d Document) Validate() error {
	if strings.TrimSpace(d.Kind) != DocumentKind {
		return fmt.Errorf("policy document kind = %q, want %q", d.Kind, DocumentKind)
	}
	if strings.TrimSpace(d.Version) != DocumentVersion {
		return fmt.Errorf("policy document version = %q, want %q", d.Version, DocumentVersion)
	}
	if strings.TrimSpace(d.Intent) == "" {
		return fmt.Errorf("policy document intent is required")
	}
	if strings.TrimSpace(string(d.Policy.ID)) == "" {
		return fmt.Errorf("policy id is required")
	}
	if strings.TrimSpace(d.Policy.Name) == "" {
		return fmt.Errorf("policy name is required")
	}
	if err := d.Policy.Rules.MessagingRules().Validate(); err != nil {
		return err
	}
	if d.GeneratedBy != nil {
		if err := d.GeneratedBy.Validate(); err != nil {
			return err
		}
		if d.GeneratedBy.IsAI() && !d.Review.Required {
			return fmt.Errorf("ai-generated policy documents must set review.required")
		}
	}
	for idx, binding := range d.Bindings {
		if err := binding.Validate(); err != nil {
			return fmt.Errorf("bindings[%d]: %w", idx, err)
		}
	}
	return nil
}

// CompilePolicy compiles the authored document into the broker-enforced policy
// model.
func (d Document) CompilePolicy() (messaging.Policy, error) {
	if err := d.Validate(); err != nil {
		return messaging.Policy{}, err
	}
	policy := messaging.Policy{
		ID:       d.Policy.ID,
		Name:     d.Policy.Name,
		Rules:    d.Policy.Rules.MessagingRules(),
		Metadata: d.policyMetadata(),
	}
	if err := policy.Validate(); err != nil {
		return messaging.Policy{}, err
	}
	return policy, nil
}

// ReviewRequired reports whether this document must be explicitly approved by
// a human before being applied to broker state.
func (d Document) ReviewRequired() bool {
	return d.Review.Required || (d.GeneratedBy != nil && d.GeneratedBy.IsAI())
}

// EnsureReviewedForApply rejects policy documents that still need human review.
func (d Document) EnsureReviewedForApply() error {
	if err := d.Validate(); err != nil {
		return err
	}
	if !d.ReviewRequired() {
		return nil
	}
	if strings.TrimSpace(d.Review.ApprovedBy) == "" || d.Review.ApprovedAt == nil || d.Review.ApprovedAt.IsZero() {
		return fmt.Errorf("policy document requires human approval before apply")
	}
	return nil
}

// MessagingRules converts file-friendly rules into broker-enforced rules.
func (r Rules) MessagingRules() messaging.PolicyRules {
	return messaging.PolicyRules{
		ReadInbound:           r.ReadInbound,
		CreateDrafts:          r.CreateDrafts,
		SendMessages:          r.SendMessages,
		RequireApproval:       r.RequireApproval,
		ReplyOnly:             r.ReplyOnly,
		AllowNewConversations: r.AllowNewConversations,
		AllowAttachments:      r.AllowAttachments,
		MarkRead:              r.MarkRead,
		ManageMessages:        r.ManageMessages,
		AllowedContainerIDs:   append([]messaging.ContainerID(nil), r.AllowedContainerIDs...),
		SearchIdentities:      r.SearchIdentities,
		SearchConversations:   r.SearchConversations,
		SearchMessages:        r.SearchMessages,
		AllowedIdentityIDs:    append([]messaging.IdentityID(nil), r.AllowedIdentityIDs...),
	}
}

// RulesFromMessagingRules converts broker rules into the document rules shape.
func RulesFromMessagingRules(r messaging.PolicyRules) Rules {
	return Rules{
		ReadInbound:           r.ReadInbound,
		CreateDrafts:          r.CreateDrafts,
		SendMessages:          r.SendMessages,
		RequireApproval:       r.RequireApproval,
		ReplyOnly:             r.ReplyOnly,
		AllowNewConversations: r.AllowNewConversations,
		AllowAttachments:      r.AllowAttachments,
		MarkRead:              r.MarkRead,
		ManageMessages:        r.ManageMessages,
		AllowedContainerIDs:   append([]messaging.ContainerID(nil), r.AllowedContainerIDs...),
		SearchIdentities:      r.SearchIdentities,
		SearchConversations:   r.SearchConversations,
		SearchMessages:        r.SearchMessages,
		AllowedIdentityIDs:    append([]messaging.IdentityID(nil), r.AllowedIdentityIDs...),
	}
}

// Validate checks actor provenance.
func (a Actor) Validate() error {
	switch strings.TrimSpace(a.Type) {
	case actorTypeAI, actorTypeHuman, actorTypeSystem:
	default:
		return fmt.Errorf("generated_by.type = %q, want ai, human, or system", a.Type)
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("generated_by.name is required")
	}
	return nil
}

// IsAI reports whether this actor is an AI/model-generated policy author.
func (a Actor) IsAI() bool {
	return strings.TrimSpace(a.Type) == actorTypeAI
}

// Validate checks binding structure.
func (b Binding) Validate() error {
	if b.ConnectionID == "" && b.ExposureID == "" {
		return fmt.Errorf("connection_id or exposure_id is required")
	}
	return nil
}

func (d Document) policyMetadata() map[string]string {
	metadata := cloneMetadata(d.Metadata)
	for key, value := range cloneMetadata(d.Policy.Metadata) {
		metadata[key] = value
	}
	metadata["policy_document_kind"] = d.Kind
	metadata["policy_document_version"] = d.Version
	metadata["policy_intent"] = d.Intent
	if d.GeneratedBy != nil {
		metadata["policy_generated_by_type"] = d.GeneratedBy.Type
		metadata["policy_generated_by_name"] = d.GeneratedBy.Name
		if strings.TrimSpace(d.GeneratedBy.Model) != "" {
			metadata["policy_generated_by_model"] = d.GeneratedBy.Model
		}
		if strings.TrimSpace(d.GeneratedBy.PromptRef) != "" {
			metadata["policy_generated_by_prompt_ref"] = d.GeneratedBy.PromptRef
		}
	}
	if d.Review.Required {
		metadata["policy_review_required"] = "true"
	}
	if strings.TrimSpace(d.Review.ApprovedBy) != "" {
		metadata["policy_review_approved_by"] = d.Review.ApprovedBy
	}
	if d.Review.ApprovedAt != nil && !d.Review.ApprovedAt.IsZero() {
		metadata["policy_review_approved_at"] = d.Review.ApprovedAt.Format(time.RFC3339)
	}
	return metadata
}

func cloneMetadata(metadata map[string]string) map[string]string {
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func documentFormat(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}
