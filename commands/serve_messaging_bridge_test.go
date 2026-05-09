package commands

import (
	"context"
	"testing"

	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/messaging"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

func TestMessagingBridgeAgentSubjectCandidatesIncludeSandboxRuntimeFamilies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		agentID     string
		template    string
		runtimeName string
	}{
		{
			name:        "openclaw docker",
			agentID:     "openclaw-agent",
			template:    sandboxTemplateOpenClawDocker,
			runtimeName: sandboxTemplateOpenClaw,
		},
		{
			name:        "hermes docker",
			agentID:     "hermes-agent",
			template:    sandboxTemplateHermesDocker,
			runtimeName: sandboxTemplateHermes,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := &sandboxAgentSource{
				targetsBy: map[string]sandboxAgentTarget{
					"name-lower:" + tc.agentID: {
						Agent: skyagent.AgentInfo{
							ID:      "agent/" + tc.agentID,
							Name:    tc.agentID,
							KeyName: tc.agentID,
						},
						Sandbox: skysandbox.Record{
							Slug:     tc.agentID,
							Name:     tc.agentID,
							Template: tc.template,
						},
					},
				},
			}
			backend := &messagingBridgeBackend{sandboxAgents: source}

			candidates := backend.agentSubjectCandidates(context.Background(), tc.agentID)
			if _, ok := candidates[tc.template]; !ok {
				t.Fatalf("agentSubjectCandidates() missing sandbox template: %+v", candidates)
			}
			if _, ok := candidates[tc.runtimeName]; !ok {
				t.Fatalf("agentSubjectCandidates() missing runtime family %q: %+v", tc.runtimeName, candidates)
			}
			if !exposureMatchesAgent(messaging.Exposure{
				SubjectKind: messaging.ExposureSubjectKindRuntime,
				SubjectID:   "runtime:" + tc.runtimeName,
			}, candidates) {
				t.Fatalf("runtime:%s exposure did not match candidates: %+v", tc.runtimeName, candidates)
			}
		})
	}
}
