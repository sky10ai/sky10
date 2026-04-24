package messaging

import (
	"testing"
	"time"
)

func TestAuthInfoValidate(t *testing.T) {
	tests := []struct {
		name    string
		auth    AuthInfo
		wantErr bool
	}{
		{
			name: "zero auth is allowed",
			auth: AuthInfo{},
		},
		{
			name:    "oauth requires credential ref",
			auth:    AuthInfo{Method: AuthMethodOAuth2},
			wantErr: true,
		},
		{
			name: "oauth with credential ref is valid",
			auth: AuthInfo{
				Method:        AuthMethodOAuth2,
				CredentialRef: "secrets/gmail/work-refresh-token",
				Scopes:        []string{"gmail.readonly"},
			},
		},
		{
			name: "none auth can omit credential ref",
			auth: AuthInfo{Method: AuthMethodNone},
		},
		{
			name:    "configured auth needs method",
			auth:    AuthInfo{CredentialRef: "secrets/foo"},
			wantErr: true,
		},
		{
			name: "blank scopes rejected",
			auth: AuthInfo{
				Method:        AuthMethodOAuth2,
				CredentialRef: "secrets/foo",
				Scopes:        []string{"gmail.readonly", ""},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.auth.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("Validate() succeeded, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestConnectionValidate(t *testing.T) {
	conn := Connection{
		ID:        ConnectionID("gmail/work"),
		AdapterID: AdapterID("gmail"),
		Label:     "Work Gmail",
		Status:    ConnectionStatusConnected,
		Auth: AuthInfo{
			Method:        AuthMethodOAuth2,
			CredentialRef: "secrets/gmail/work",
		},
	}
	if err := conn.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestIdentityValidateRequiresRemoteOrAddress(t *testing.T) {
	identity := Identity{
		ID:           IdentityID("gmail/work:primary"),
		ConnectionID: ConnectionID("gmail/work"),
		Kind:         IdentityKindEmail,
	}
	if err := identity.Validate(); err == nil {
		t.Fatal("Validate() succeeded, want error")
	}

	identity.Address = "me@example.com"
	if err := identity.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMessagePartValidate(t *testing.T) {
	validText := MessagePart{Kind: MessagePartKindMarkdown, Text: "hello"}
	if err := validText.Validate(); err != nil {
		t.Fatalf("text Validate() error = %v", err)
	}

	validFile := MessagePart{Kind: MessagePartKindFile, FileName: "report.pdf"}
	if err := validFile.Validate(); err != nil {
		t.Fatalf("file Validate() error = %v", err)
	}

	invalid := MessagePart{Kind: MessagePartKindFile}
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() succeeded, want error")
	}
}

func TestConversationValidate(t *testing.T) {
	conv := Conversation{
		ID:              ConversationID("gmail/work/thread-123"),
		ConnectionID:    ConnectionID("gmail/work"),
		LocalIdentityID: IdentityID("gmail/work:primary"),
		Kind:            ConversationKindEmailThread,
		RemoteID:        "thread-123",
		Participants: []Participant{
			{
				Kind:        ParticipantKindUser,
				Address:     "customer@example.com",
				DisplayName: "Customer",
			},
		},
	}
	if err := conv.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestContainerAndPlacementValidate(t *testing.T) {
	container := Container{
		ID:           "container/inbox",
		ConnectionID: "gmail/work",
		Kind:         ContainerKindInbox,
		Name:         "INBOX",
		RemoteID:     "INBOX",
	}
	if err := container.Validate(); err != nil {
		t.Fatalf("container Validate() error = %v", err)
	}

	placement := Placement{
		MessageID:    "msg/1",
		ConnectionID: "gmail/work",
		ContainerID:  container.ID,
		RemoteID:     "101",
	}
	if err := placement.Validate(); err != nil {
		t.Fatalf("placement Validate() error = %v", err)
	}

	placement.ContainerID = ""
	if err := placement.Validate(); err == nil {
		t.Fatal("placement Validate() succeeded with blank container_id, want error")
	}
}

func TestMessageValidate(t *testing.T) {
	now := time.Now().UTC()
	msg := Message{
		ID:              MessageID("gmail/work/msg-1"),
		ConnectionID:    ConnectionID("gmail/work"),
		ConversationID:  ConversationID("gmail/work/thread-123"),
		LocalIdentityID: IdentityID("gmail/work:primary"),
		Direction:       MessageDirectionInbound,
		Sender: Participant{
			Kind:        ParticipantKindUser,
			Address:     "customer@example.com",
			DisplayName: "Customer",
		},
		Parts: []MessagePart{
			{Kind: MessagePartKindText, Text: "hello"},
		},
		CreatedAt: now,
		Status:    MessageStatusReceived,
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	msg.Parts = nil
	if err := msg.Validate(); err == nil {
		t.Fatal("Validate() succeeded, want error")
	}
}

func TestDraftValidate(t *testing.T) {
	draft := Draft{
		ID:              DraftID("draft-1"),
		ConnectionID:    ConnectionID("gmail/work"),
		ConversationID:  ConversationID("gmail/work/thread-123"),
		LocalIdentityID: IdentityID("gmail/work:primary"),
		Parts: []MessagePart{
			{Kind: MessagePartKindMarkdown, Text: "reply"},
		},
		Status: DraftStatusPending,
	}
	if err := draft.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestApprovalValidate(t *testing.T) {
	now := time.Now().UTC()
	approval := Approval{
		ID:           ApprovalID("approval-1"),
		ConnectionID: ConnectionID("gmail/work"),
		DraftID:      DraftID("draft-1"),
		WorkflowID:   WorkflowID("wf-1"),
		Action:       "send_draft",
		Summary:      "Send reply to board thread",
		Status:       ApprovalStatusPending,
		RequestedAt:  now,
	}
	if err := approval.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	approval.Summary = ""
	if err := approval.Validate(); err == nil {
		t.Fatal("Validate() succeeded with blank summary, want error")
	}
}

func TestPolicyExposureAndEventValidate(t *testing.T) {
	policy := Policy{
		ID:   PolicyID("default-reply-only"),
		Name: "Default Reply Only",
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("policy Validate() error = %v", err)
	}

	exposure := Exposure{
		ID:           ExposureID("openclaw-support"),
		ConnectionID: ConnectionID("gmail/work"),
		SubjectID:    "agent:openclaw",
		SubjectKind:  ExposureSubjectKindAgent,
		PolicyID:     policy.ID,
		Enabled:      true,
	}
	if err := exposure.Validate(); err != nil {
		t.Fatalf("exposure Validate() error = %v", err)
	}

	event := Event{
		ID:           EventID("evt-1"),
		Type:         EventTypeMessageReceived,
		ConnectionID: ConnectionID("gmail/work"),
		Timestamp:    time.Now().UTC(),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("event Validate() error = %v", err)
	}

	policy.Rules.AllowedIdentityIDs = []IdentityID{""}
	if err := policy.Validate(); err == nil {
		t.Fatal("policy Validate() succeeded with blank allowed identity, want error")
	}

	policy.Rules.AllowedIdentityIDs = nil
	policy.Rules.AllowedContainerIDs = []ContainerID{""}
	if err := policy.Validate(); err == nil {
		t.Fatal("policy Validate() succeeded with blank allowed container, want error")
	}
}

func TestWorkflowAndActivityEventValidate(t *testing.T) {
	now := time.Now().UTC()
	workflow := Workflow{
		ID:                 WorkflowID("wf-1"),
		Kind:               "proactive_reply",
		Status:             WorkflowStatusAwaitingApproval,
		SourceConnectionID: ConnectionID("slack/board"),
		Sender: Participant{
			Kind:        ParticipantKindUser,
			RemoteID:    "U123",
			DisplayName: "Latisha",
		},
		BrokerReceivedAt: now,
		LastActivityAt:   now,
	}
	if err := workflow.Validate(); err != nil {
		t.Fatalf("workflow Validate() error = %v", err)
	}

	activity := ActivityEvent{
		ID:           EventID("evt-activity-1"),
		WorkflowID:   workflow.ID,
		Type:         EventTypeApprovalRequired,
		OccurredAt:   now,
		ConnectionID: workflow.SourceConnectionID,
	}
	if err := activity.Validate(); err != nil {
		t.Fatalf("activity Validate() error = %v", err)
	}
}
