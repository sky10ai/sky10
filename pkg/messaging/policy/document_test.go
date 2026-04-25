package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
)

func TestParseDocumentCompilesPolicyWithIntentAndProvenance(t *testing.T) {
	t.Parallel()

	doc, err := ParseDocument([]byte(testPolicyDocumentYAML), "yaml")
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	if !doc.ReviewRequired() {
		t.Fatal("ReviewRequired() = false, want true for AI-generated policy")
	}
	if err := doc.EnsureReviewedForApply(); err == nil {
		t.Fatal("EnsureReviewedForApply() error = nil, want review-required error")
	}

	policy, err := doc.CompilePolicy()
	if err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	if policy.ID != "policy/board-replies" {
		t.Fatalf("policy id = %q, want policy/board-replies", policy.ID)
	}
	if !policy.Rules.ReadInbound || !policy.Rules.CreateDrafts || !policy.Rules.SendMessages {
		t.Fatalf("policy rules = %+v, want read/draft/send enabled", policy.Rules)
	}
	if !policy.Rules.RequireApproval || !policy.Rules.ReplyOnly || policy.Rules.AllowAttachments {
		t.Fatalf("policy rules = %+v, want approval, reply-only, no attachments", policy.Rules)
	}
	if got := policy.Metadata["policy_intent"]; !strings.Contains(got, "board emails") {
		t.Fatalf("policy intent metadata = %q, want board emails", got)
	}
	if got := policy.Metadata["policy_generated_by_model"]; got != "gpt-5.4" {
		t.Fatalf("policy generated_by model = %q, want gpt-5.4", got)
	}
}

func TestReviewedAIDocumentCanBeApplied(t *testing.T) {
	t.Parallel()

	approvedAt := time.Date(2026, 4, 25, 15, 30, 0, 0, time.UTC)
	doc := Document{
		Kind:    DocumentKind,
		Version: DocumentVersion,
		Intent:  "Allow the assistant to draft Slack replies, but require approval before sending.",
		Policy: PolicySpec{
			ID:   "policy/slack-replies",
			Name: "Slack Replies",
			Rules: Rules{
				ReadInbound:     true,
				CreateDrafts:    true,
				SendMessages:    true,
				RequireApproval: true,
				ReplyOnly:       true,
			},
		},
		GeneratedBy: &Actor{
			Type:  "ai",
			Name:  "sky10-policy-assistant",
			Model: "gpt-5.4",
		},
		Review: Review{
			Required:   true,
			ApprovedBy: "human:alice",
			ApprovedAt: &approvedAt,
		},
	}
	if err := doc.EnsureReviewedForApply(); err != nil {
		t.Fatalf("EnsureReviewedForApply() error = %v", err)
	}
	policy, err := doc.CompilePolicy()
	if err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	if got := policy.Metadata["policy_review_approved_at"]; got != approvedAt.Format(time.RFC3339) {
		t.Fatalf("policy review approved_at = %q, want %q", got, approvedAt.Format(time.RFC3339))
	}
}

func TestParseDocumentRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := ParseDocument([]byte(`
kind: sky10.messaging.policy
version: v1alpha1
intent: Let Sky10 draft replies.
policy:
  id: policy/minimal
  name: Minimal
  rules:
    create_drafts: true
unknown: true
`), "yaml")
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("ParseDocument() error = %v, want unknown field error", err)
	}
}

func TestParseDocumentRejectsAIWithoutRequiredReview(t *testing.T) {
	t.Parallel()

	_, err := ParseDocument([]byte(`
kind: sky10.messaging.policy
version: v1alpha1
intent: Allow reply drafts.
policy:
  id: policy/replies
  name: Replies
  rules:
    create_drafts: true
generated_by:
  type: ai
  name: sky10-policy-assistant
`), "yaml")
	if err == nil || !strings.Contains(err.Error(), "review.required") {
		t.Fatalf("ParseDocument() error = %v, want review.required error", err)
	}
}

func TestRulesRoundTripMessagingRules(t *testing.T) {
	t.Parallel()

	rules := messaging.PolicyRules{
		ReadInbound:           true,
		CreateDrafts:          true,
		SendMessages:          true,
		RequireApproval:       true,
		ReplyOnly:             true,
		AllowNewConversations: false,
		AllowAttachments:      true,
		MarkRead:              true,
		ManageMessages:        true,
		AllowedContainerIDs:   []messaging.ContainerID{"container/inbox", "container/archive"},
		SearchIdentities:      true,
		SearchConversations:   true,
		SearchMessages:        false,
		AllowedIdentityIDs:    []messaging.IdentityID{"identity/work"},
	}
	got := RulesFromMessagingRules(rules).MessagingRules()
	if len(got.AllowedContainerIDs) != 2 || got.AllowedContainerIDs[1] != "container/archive" {
		t.Fatalf("allowed containers = %#v, want copied containers", got.AllowedContainerIDs)
	}
	if len(got.AllowedIdentityIDs) != 1 || got.AllowedIdentityIDs[0] != "identity/work" {
		t.Fatalf("allowed identities = %#v, want copied identities", got.AllowedIdentityIDs)
	}
	if !got.RequireApproval || !got.AllowAttachments || got.SearchMessages {
		t.Fatalf("round-tripped rules = %+v, want approval/attachments true and search_messages false", got)
	}
}

const testPolicyDocumentYAML = `
kind: sky10.messaging.policy
version: v1alpha1
intent: >
  When anyone from the board emails me, let Sky10 read the board emails,
  draft a reply, and ask me before sending. Do not send attachments.
policy:
  id: policy/board-replies
  name: Board Reply Approval
  metadata:
    owner: alice
  rules:
    read_inbound: true
    create_drafts: true
    send_messages: true
    require_approval: true
    reply_only: true
    allow_new_conversations: false
    allow_attachments: false
    search_identities: true
    search_conversations: true
    search_messages: true
    allowed_identity_ids:
      - identity/work-email
bindings:
  - connection_id: gmail/work
generated_by:
  type: ai
  name: sky10-policy-assistant
  model: gpt-5.4
  prompt_ref: prompt/board-replies
review:
  required: true
`
