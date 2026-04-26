package agent

import (
	"context"
	"encoding/json"
	"fmt"
	slashpath "path"
	"strings"
	"time"

	"github.com/google/uuid"
)

const agentJobOutputRoot = "/shared/jobs"

func (h *RPCHandler) rpcCall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	p.Agent = strings.TrimSpace(p.Agent)
	p.Tool = strings.TrimSpace(p.Tool)
	p.BidID = strings.TrimSpace(p.BidID)
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
	if p.Tool == "" {
		return nil, fmt.Errorf("tool is required")
	}

	target, tool, err := h.resolveCallTarget(ctx, p.Agent, p.Tool)
	if err != nil {
		return nil, err
	}
	inputDigest, err := digestJSON(p.Input)
	if err != nil {
		return nil, err
	}

	buyer := h.localBuyerID()
	seller := agentSellerID(target)
	if existing, ok, err := store.FindByIdempotency(ctx, buyer, seller, tool.Name, p.IdempotencyKey); err != nil {
		return nil, err
	} else if ok {
		return callEnvelopeForJob(existing.Job), nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	payloadRefs := normalizePayloadRefs(p.PayloadRef, p.PayloadRefs)
	jobID := "j_" + uuid.NewString()
	outputDir := defaultAgentJobOutputDir(jobID)
	job := AgentJob{
		JobID:          jobID,
		Buyer:          buyer,
		Seller:         seller,
		AgentID:        target.ID,
		AgentName:      target.Name,
		Tool:           tool.Name,
		Capability:     effectiveToolCapability(tool),
		BidID:          p.BidID,
		WorkState:      JobWorkReceived,
		PaymentState:   JobPaymentNone,
		CreatedAt:      now,
		UpdatedAt:      now,
		OutputDir:      outputDir,
		IdempotencyKey: p.IdempotencyKey,
		InputDigest:    inputDigest,
		PayloadRefs:    payloadRefs,
	}
	if _, err := store.Save(ctx, job); err != nil {
		return nil, err
	}

	content, err := json.Marshal(AgentToolCallMessage{
		JobID:          job.JobID,
		Tool:           job.Tool,
		Capability:     job.Capability,
		Input:          normalizeRawInput(p.Input),
		PayloadRef:     p.PayloadRef,
		PayloadRefs:    payloadRefs,
		Budget:         p.Budget,
		BidID:          p.BidID,
		IdempotencyKey: p.IdempotencyKey,
		JobContext: AgentJobContext{
			JobID:     job.JobID,
			OutputDir: outputDir,
			UpdateMethods: []string{
				"agent.job.updateStatus",
				"agent.job.complete",
				"agent.job.fail",
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tool call content: %w", err)
	}

	sendResult, err := h.SendMessage(ctx, SendParams{
		To:        agentAddressForSend(target),
		DeviceID:  target.DeviceID,
		SessionID: job.JobID,
		Type:      "tool_call",
		Content:   content,
	})
	if err != nil {
		job.WorkState = JobWorkFailed
		job.LastError = err.Error()
		job.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = store.Save(ctx, job)
		return nil, err
	}

	job.WorkState = JobWorkAccepted
	job.MessageID = sendResult.ID
	job.Delivery = &sendResult.Delivery
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Save(ctx, job); err != nil {
		return nil, err
	}
	return callEnvelopeForJob(job), nil
}

func (h *RPCHandler) rpcCancel(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentCancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	result, err := store.Cancel(ctx, p.JobID, p.Reason)
	if err != nil {
		return nil, err
	}
	if h.emit != nil {
		h.emit("agent.job.cancel", map[string]string{
			"job_id": result.Job.JobID,
			"reason": strings.TrimSpace(p.Reason),
		})
	}
	return result, nil
}

func (h *RPCHandler) rpcJobGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentJobGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return store.Get(ctx, p.JobID)
}

func (h *RPCHandler) rpcJobList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentJobListParams
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}
	return store.List(ctx, p, h.localBuyerID())
}

func (h *RPCHandler) rpcJobUpdateStatus(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentJobStatusParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return store.UpdateStatus(ctx, p)
}

func (h *RPCHandler) rpcJobComplete(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentJobCompleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return store.Complete(ctx, p)
}

func (h *RPCHandler) rpcJobFail(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireJobStore()
	if err != nil {
		return nil, err
	}
	var p AgentJobFailParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return store.Fail(ctx, p)
}

func (h *RPCHandler) resolveCallTarget(ctx context.Context, agentRef, toolRef string) (AgentInfo, AgentToolSpec, error) {
	agents := h.listCallableAgents(ctx)
	var matches []AgentInfo
	for _, agent := range agents {
		if agentRef != "" && !agentMatchesRef(agent, agentRef) {
			continue
		}
		if _, ok := toolForCall(agent, toolRef); !ok {
			continue
		}
		matches = append(matches, agent)
	}
	if len(matches) == 0 {
		if agentRef != "" {
			return AgentInfo{}, AgentToolSpec{}, fmt.Errorf("agent %q does not expose tool %q", agentRef, toolRef)
		}
		return AgentInfo{}, AgentToolSpec{}, fmt.Errorf("no agent exposes tool %q", toolRef)
	}
	if len(matches) > 1 && agentRef == "" {
		return AgentInfo{}, AgentToolSpec{}, fmt.Errorf("multiple agents expose tool %q; agent is required", toolRef)
	}
	tool, _ := toolForCall(matches[0], toolRef)
	return matches[0], tool, nil
}

func (h *RPCHandler) listCallableAgents(ctx context.Context) []AgentInfo {
	if h.router != nil {
		return h.router.List(ctx)
	}
	return h.registry.List()
}

func (h *RPCHandler) localBuyerID() string {
	if h.owner != nil {
		if address := strings.TrimSpace(h.owner.Address()); address != "" {
			return "sky10://" + address
		}
	}
	if h.registry != nil {
		if deviceID := strings.TrimSpace(h.registry.DeviceID()); deviceID != "" {
			return "device://" + deviceID
		}
	}
	return "sky10://local"
}

func agentMatchesRef(agent AgentInfo, ref string) bool {
	ref = normalizeAgentCallRef(ref)
	if ref == "" {
		return false
	}
	return ref == agent.ID || ref == agent.Name || ref == strings.ToLower(agent.Name)
}

func normalizeAgentCallRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "agent://")
	ref = strings.TrimPrefix(ref, "sky10://")
	return ref
}

func toolForCall(agent AgentInfo, toolRef string) (AgentToolSpec, bool) {
	toolRef = strings.TrimSpace(toolRef)
	for _, tool := range agent.Tools {
		if tool.Name == toolRef || tool.Capability == toolRef {
			if strings.TrimSpace(tool.Name) == "" {
				tool.Name = toolRef
			}
			if strings.TrimSpace(tool.Capability) == "" {
				tool.Capability = tool.Name
			}
			return tool, true
		}
	}
	if len(agent.Tools) == 0 && agent.HasSkill(toolRef) {
		return AgentToolSpec{Name: toolRef, Capability: toolRef}, true
	}
	return AgentToolSpec{}, false
}

func effectiveToolCapability(tool AgentToolSpec) string {
	if capability := strings.TrimSpace(tool.Capability); capability != "" {
		return capability
	}
	return strings.TrimSpace(tool.Name)
}

func agentSellerID(agent AgentInfo) string {
	if id := strings.TrimSpace(agent.ID); id != "" {
		return "sky10://" + id
	}
	if name := strings.TrimSpace(agent.Name); name != "" {
		return "sky10://" + name
	}
	return "sky10://unknown-agent"
}

func agentAddressForSend(agent AgentInfo) string {
	if id := strings.TrimSpace(agent.ID); id != "" {
		return id
	}
	return strings.TrimSpace(agent.Name)
}

func normalizePayloadRefs(one *AgentPayloadRef, many []AgentPayloadRef) []AgentPayloadRef {
	result := make([]AgentPayloadRef, 0, len(many)+1)
	if one != nil {
		result = append(result, *one)
	}
	for _, ref := range many {
		result = append(result, ref)
	}
	return result
}

func normalizeRawInput(raw json.RawMessage) json.RawMessage {
	if strings.TrimSpace(string(raw)) == "" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func defaultAgentJobOutputDir(jobID string) string {
	return slashpath.Join(agentJobOutputRoot, strings.TrimSpace(jobID), "outputs")
}

func callEnvelopeForJob(job AgentJob) *AgentCallResultEnvelope {
	callType := AgentCallAccepted
	switch {
	case job.PaymentState == JobPaymentRequired:
		callType = AgentCallPaymentRequired
	case job.WorkState == JobWorkCompleted:
		callType = AgentCallResult
	case job.WorkState == JobWorkInputRequired:
		callType = AgentCallInputRequired
	case job.WorkState == JobWorkFailed || job.WorkState == JobWorkCanceled || job.WorkState == JobWorkExpired:
		callType = AgentCallError
	}
	envelope := &AgentCallResultEnvelope{
		Type:     callType,
		JobID:    job.JobID,
		Job:      &job,
		Delivery: job.Delivery,
	}
	if callType == AgentCallError {
		code := strings.TrimSpace(job.ErrorCode)
		if code == "" {
			code = job.WorkState
		}
		envelope.Error = &CallError{
			Code:    code,
			Message: strings.TrimSpace(job.LastError),
		}
		if envelope.Error.Message == "" {
			envelope.Error.Message = "job is " + job.WorkState
		}
	}
	return envelope
}
