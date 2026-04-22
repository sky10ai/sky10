package policy

import (
	"testing"

	"github.com/sky10/sky10/pkg/messaging"
)

func TestReadInbound(t *testing.T) {
	t.Parallel()

	p := messaging.Policy{
		ID:   "policy/read",
		Name: "Read",
		Rules: messaging.PolicyRules{
			ReadInbound: true,
		},
	}
	if decision := ReadInbound(p); decision.Outcome != OutcomeAllow {
		t.Fatalf("ReadInbound() outcome = %q, want allow", decision.Outcome)
	}
}

func TestCreateDraftRejectsAttachmentsAndIdentity(t *testing.T) {
	t.Parallel()

	p := messaging.Policy{
		ID:   "policy/draft",
		Name: "Draft",
		Rules: messaging.PolicyRules{
			CreateDrafts:       true,
			AllowAttachments:   false,
			AllowedIdentityIDs: []messaging.IdentityID{"identity/allowed"},
		},
	}

	decision := CreateDraft(p, DraftInput{
		RequestedIdentityID: "identity/other",
	})
	if decision.Outcome != OutcomeDeny {
		t.Fatalf("CreateDraft(identity) outcome = %q, want deny", decision.Outcome)
	}

	decision = CreateDraft(p, DraftInput{
		RequestedIdentityID: "identity/allowed",
		HasAttachments:      true,
	})
	if decision.Outcome != OutcomeDeny {
		t.Fatalf("CreateDraft(attachments) outcome = %q, want deny", decision.Outcome)
	}
}

func TestSendMessageApprovalAndReplyOnly(t *testing.T) {
	t.Parallel()

	p := messaging.Policy{
		ID:   "policy/send",
		Name: "Send",
		Rules: messaging.PolicyRules{
			SendMessages:    true,
			RequireApproval: true,
		},
	}
	if decision := SendMessage(p, SendInput{}); decision.Outcome != OutcomeRequireApproval {
		t.Fatalf("SendMessage() outcome = %q, want require_approval", decision.Outcome)
	}

	p.Rules.RequireApproval = false
	p.Rules.ReplyOnly = true
	if decision := SendMessage(p, SendInput{NewConversation: true}); decision.Outcome != OutcomeDeny {
		t.Fatalf("SendMessage(new conversation) outcome = %q, want deny", decision.Outcome)
	}

	p.Rules.ReplyOnly = false
	p.Rules.AllowNewConversations = false
	if decision := SendMessage(p, SendInput{NewConversation: true}); decision.Outcome != OutcomeDeny {
		t.Fatalf("SendMessage(allow_new_conversations=false) outcome = %q, want deny", decision.Outcome)
	}
}

func TestSearchScopes(t *testing.T) {
	t.Parallel()

	p := messaging.Policy{
		ID:   "policy/search",
		Name: "Search",
		Rules: messaging.PolicyRules{
			SearchMessages: true,
		},
	}
	if decision := Search(p, SearchScopeMessages); decision.Outcome != OutcomeAllow {
		t.Fatalf("Search(messages) outcome = %q, want allow", decision.Outcome)
	}
	if decision := Search(p, SearchScopeConversations); decision.Outcome != OutcomeDeny {
		t.Fatalf("Search(conversations) outcome = %q, want deny", decision.Outcome)
	}
}
