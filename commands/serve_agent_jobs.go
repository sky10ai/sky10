package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	skyagent "github.com/sky10/sky10/pkg/agent"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/sky10/sky10/pkg/sandbox/comms/agentjobs"
)

func installAgentJobBridgeEndpoint(server *skyrpc.Server, agentRPC *skyagent.RPCHandler, jobStore *skyagent.JobStore, sandboxManager *skysandbox.Manager, logger *slog.Logger) error {
	if server == nil {
		return errors.New("agent-jobs: nil rpc server")
	}
	if agentRPC == nil {
		return errors.New("agent-jobs: nil agent rpc handler")
	}
	if jobStore == nil {
		return errors.New("agent-jobs: nil job store")
	}

	forwarder := agentjobs.NewForwardingBackend()
	server.HandleHTTP("GET "+agentjobs.EndpointPath, agentjobs.HandlerWithHostBridge(forwarder))
	if sandboxGuestMode() {
		agentRPC.SetJobForwarder(forwarder)
	}
	if sandboxManager != nil {
		bridgeManager := agentjobs.NewBridgeManager(agentJobBridgeBackend{store: jobStore}, logger)
		sandboxManager.AddBridgeConnector(bridgeManager.Connect, bridgeManager.Close)
	}
	return nil
}

type agentJobBridgeBackend struct {
	store *skyagent.JobStore
}

func (b agentJobBridgeBackend) UpdateStatus(ctx context.Context, agentRef string, params skyagent.AgentJobStatusParams) (*skyagent.AgentJobResult, error) {
	if err := b.authorize(ctx, agentRef, params.JobID); err != nil {
		return nil, err
	}
	return b.store.UpdateStatus(ctx, params)
}

func (b agentJobBridgeBackend) Complete(ctx context.Context, agentRef string, params skyagent.AgentJobCompleteParams) (*skyagent.AgentJobResult, error) {
	if err := b.authorize(ctx, agentRef, params.JobID); err != nil {
		return nil, err
	}
	return b.store.Complete(ctx, params)
}

func (b agentJobBridgeBackend) Fail(ctx context.Context, agentRef string, params skyagent.AgentJobFailParams) (*skyagent.AgentJobResult, error) {
	if err := b.authorize(ctx, agentRef, params.JobID); err != nil {
		return nil, err
	}
	return b.store.Fail(ctx, params)
}

func (b agentJobBridgeBackend) authorize(ctx context.Context, agentRef, jobID string) error {
	if b.store == nil {
		return fmt.Errorf("agent job store is not configured")
	}
	result, err := b.store.Get(ctx, jobID)
	if err != nil {
		return err
	}
	agentRef = strings.TrimSpace(agentRef)
	if agentRef == "" {
		return nil
	}
	job := result.Job
	if job.AgentID == agentRef || job.AgentName == agentRef {
		return nil
	}
	return fmt.Errorf("agent job %q is not owned by sandbox %q", job.JobID, agentRef)
}
