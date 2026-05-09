package commands

import (
	"context"
	"testing"

	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/messaging"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

func TestMessagingBridgeAgentSubjectCandidatesIncludeSandboxTemplate(t *testing.T) {
	t.Parallel()

	source := &sandboxAgentSource{
		targetsBy: map[string]sandboxAgentTarget{
			"name-lower:openclaw-agent": {
				Agent: skyagent.AgentInfo{
					ID:      "agent/openclaw-agent",
					Name:    "OpenClaw Agent",
					KeyName: "openclaw-agent",
				},
				Sandbox: skysandbox.Record{
					Slug:     "openclaw-agent",
					Name:     "OpenClaw Agent",
					Template: "openclaw",
				},
			},
		},
	}
	backend := &messagingBridgeBackend{sandboxAgents: source}

	candidates := backend.agentSubjectCandidates(context.Background(), "openclaw-agent")
	if _, ok := candidates["openclaw"]; !ok {
		t.Fatalf("agentSubjectCandidates() missing sandbox template: %+v", candidates)
	}
	if !exposureMatchesAgent(messaging.Exposure{
		SubjectKind: messaging.ExposureSubjectKindRuntime,
		SubjectID:   "runtime:openclaw",
	}, candidates) {
		t.Fatalf("runtime:openclaw exposure did not match candidates: %+v", candidates)
	}
}
