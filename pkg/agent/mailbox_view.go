package agent

import (
	"fmt"
	"sort"
	"strings"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

const (
	mailboxViewRoleHuman = "human"
	mailboxViewRoleAgent = "agent"
)

type mailboxView struct {
	ViewID    string                 `json:"view_id"`
	Label     string                 `json:"label"`
	Role      string                 `json:"role"`
	Principal agentmailbox.Principal `json:"principal"`
	Skills    []string               `json:"skills,omitempty"`
}

type mailboxViewContext struct {
	view mailboxView
}

func (v mailboxViewContext) Principal() agentmailbox.Principal {
	return v.view.Principal
}

func (v mailboxViewContext) Role() string {
	return v.view.Role
}

func (v mailboxViewContext) hasSkill(skill string) bool {
	skill = strings.TrimSpace(skill)
	if skill == "" {
		return false
	}
	for _, declared := range v.view.Skills {
		if declared == skill {
			return true
		}
	}
	return false
}

func (h *RPCHandler) mailboxViews() []mailboxView {
	views := []mailboxView{h.defaultMailboxView().view}

	agents := h.registry.List()
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Name == agents[j].Name {
			return agents[i].ID < agents[j].ID
		}
		return agents[i].Name < agents[j].Name
	})
	for _, info := range agents {
		views = append(views, mailboxView{
			ViewID: "agent:" + info.ID,
			Label:  info.Name,
			Role:   mailboxViewRoleAgent,
			Principal: agentmailbox.Principal{
				ID:         info.ID,
				Kind:       agentmailbox.PrincipalKindLocalAgent,
				Scope:      agentmailbox.ScopePrivateNetwork,
				DeviceHint: info.DeviceID,
				RouteHint:  h.defaultRouteHint(),
			},
			Skills: append([]string(nil), info.Skills...),
		})
	}
	return views
}

func (h *RPCHandler) defaultMailboxView() mailboxViewContext {
	return mailboxViewContext{
		view: mailboxView{
			ViewID: "human:" + h.defaultActorID(),
			Label:  "You",
			Role:   mailboxViewRoleHuman,
			Principal: agentmailbox.Principal{
				ID:         h.defaultActorID(),
				Kind:       agentmailbox.PrincipalKindHuman,
				Scope:      agentmailbox.ScopePrivateNetwork,
				DeviceHint: h.registry.DeviceID(),
				RouteHint:  h.defaultRouteHint(),
			},
		},
	}
}

func (h *RPCHandler) mailboxViewFromPrincipal(principalID, principalKind string) (mailboxViewContext, error) {
	principalID = strings.TrimSpace(principalID)
	principalKind = strings.TrimSpace(principalKind)
	if principalID == "" && principalKind == "" {
		return h.defaultMailboxView(), nil
	}
	if principalKind == "" {
		principalKind = inferMailboxPrincipalKind(principalID)
	}
	if principalID == "" {
		switch principalKind {
		case agentmailbox.PrincipalKindHuman:
			return h.defaultMailboxView(), nil
		default:
			return mailboxViewContext{}, fmt.Errorf("principal_id is required")
		}
	}

	switch principalKind {
	case agentmailbox.PrincipalKindHuman:
		label := principalID
		if principalID == h.defaultActorID() {
			label = "You"
		}
		return mailboxViewContext{
			view: mailboxView{
				ViewID: "human:" + principalID,
				Label:  label,
				Role:   mailboxViewRoleHuman,
				Principal: agentmailbox.Principal{
					ID:         principalID,
					Kind:       agentmailbox.PrincipalKindHuman,
					Scope:      agentmailbox.ScopePrivateNetwork,
					DeviceHint: h.registry.DeviceID(),
					RouteHint:  h.defaultRouteHint(),
				},
			},
		}, nil
	case agentmailbox.PrincipalKindLocalAgent:
		info := h.registry.Get(principalID)
		label := principalID
		var skills []string
		deviceHint := h.registry.DeviceID()
		if info != nil {
			label = info.Name
			skills = append([]string(nil), info.Skills...)
			deviceHint = info.DeviceID
		}
		return mailboxViewContext{
			view: mailboxView{
				ViewID: "agent:" + principalID,
				Label:  label,
				Role:   mailboxViewRoleAgent,
				Principal: agentmailbox.Principal{
					ID:         principalID,
					Kind:       agentmailbox.PrincipalKindLocalAgent,
					Scope:      agentmailbox.ScopePrivateNetwork,
					DeviceHint: deviceHint,
					RouteHint:  h.defaultRouteHint(),
				},
				Skills: skills,
			},
		}, nil
	case agentmailbox.PrincipalKindNetworkAgent:
		return mailboxViewContext{
			view: mailboxView{
				ViewID: "network:" + principalID,
				Label:  principalID,
				Role:   mailboxViewRoleAgent,
				Principal: agentmailbox.Principal{
					ID:        principalID,
					Kind:      agentmailbox.PrincipalKindNetworkAgent,
					Scope:     agentmailbox.ScopeSky10Network,
					RouteHint: principalID,
				},
			},
		}, nil
	default:
		return mailboxViewContext{}, fmt.Errorf("unsupported principal kind %q", principalKind)
	}
}

func inferMailboxPrincipalKind(principalID string) string {
	switch {
	case strings.HasPrefix(principalID, "A-"), strings.HasPrefix(principalID, "agent:"):
		return agentmailbox.PrincipalKindLocalAgent
	case strings.HasPrefix(principalID, "human:"), strings.HasPrefix(principalID, "sky10"):
		return agentmailbox.PrincipalKindHuman
	default:
		return agentmailbox.PrincipalKindHuman
	}
}

func mailboxRecordVisibleToView(record agentmailbox.Record, view mailboxViewContext, registry *Registry) bool {
	principal := view.Principal()
	if record.Item.From.ID == principal.ID {
		return true
	}
	if mailboxPrincipalMatchesID(principal, record.Item.RecipientID(), registry) {
		return true
	}
	if record.Claim != nil && record.Claim.Holder == principal.ID {
		return true
	}
	return mailboxQueueVisibleToView(record, view, registry)
}

func mailboxQueueVisibleToView(record agentmailbox.Record, view mailboxViewContext, registry *Registry) bool {
	if record.Item.QueueName() == "" || record.Terminal() {
		return false
	}
	principal := view.Principal()
	if record.Claim != nil && record.Claim.Holder == principal.ID {
		return true
	}
	if view.Role() != mailboxViewRoleAgent {
		return false
	}
	if principal.Kind != agentmailbox.PrincipalKindLocalAgent && principal.Kind != agentmailbox.PrincipalKindNetworkAgent {
		return false
	}
	targetSkill := strings.TrimSpace(record.Item.TargetSkill)
	if targetSkill == "" {
		return false
	}
	return view.hasSkill(targetSkill)
}

func mailboxPrincipalMatchesID(principal agentmailbox.Principal, mailboxID string, registry *Registry) bool {
	mailboxID = strings.TrimSpace(mailboxID)
	if mailboxID == "" {
		return false
	}
	if principal.ID == mailboxID {
		return true
	}
	if principal.Kind != agentmailbox.PrincipalKindLocalAgent || registry == nil {
		return false
	}
	info := registry.Get(principal.ID)
	if info == nil {
		return false
	}
	return info.Name == mailboxID || info.KeyName == mailboxID
}

func filterMailboxRecords(records []agentmailbox.Record, fn func(agentmailbox.Record) bool) []agentmailbox.Record {
	if len(records) == 0 {
		return nil
	}
	out := make([]agentmailbox.Record, 0, len(records))
	for _, record := range records {
		if fn(record) {
			out = append(out, record)
		}
	}
	return out
}

func mailboxRecordMatchesListParams(record agentmailbox.Record, p mailboxListParams) bool {
	requestID := strings.TrimSpace(p.RequestID)
	if requestID != "" && record.Item.RequestID != requestID {
		return false
	}
	replyTo := strings.TrimSpace(p.ReplyTo)
	if replyTo != "" && record.Item.ReplyTo != replyTo {
		return false
	}
	queue := strings.TrimSpace(p.Queue)
	if queue != "" && record.Item.QueueName() != queue {
		return false
	}
	return true
}

func (h *RPCHandler) authorizeMailboxAction(record agentmailbox.Record, actor agentmailbox.Principal, action string) error {
	switch action {
	case "ack":
		if !mailboxPrincipalMatchesID(actor, record.Item.RecipientID(), h.registry) {
			return fmt.Errorf("mailbox item %s is not visible to actor %s", record.Item.ID, actor.ID)
		}
		return nil
	case "approve", "reject":
		if record.Item.Kind != agentmailbox.ItemKindApprovalRequest {
			return fmt.Errorf("mailbox item %s is not an approval request", record.Item.ID)
		}
		if !mailboxPrincipalMatchesID(actor, record.Item.RecipientID(), h.registry) {
			return fmt.Errorf("mailbox item %s is not addressed to actor %s", record.Item.ID, actor.ID)
		}
		return nil
	case "claim":
		if actor.Kind != agentmailbox.PrincipalKindLocalAgent && actor.Kind != agentmailbox.PrincipalKindNetworkAgent {
			return fmt.Errorf("actor %s cannot claim queue work", actor.ID)
		}
		if record.Item.QueueName() == "" {
			return fmt.Errorf("mailbox item %s is not claimable", record.Item.ID)
		}
		if record.Terminal() {
			return fmt.Errorf("mailbox item %s is already terminal", record.Item.ID)
		}
		if actor.Kind == agentmailbox.PrincipalKindLocalAgent {
			if info := h.registry.Get(actor.ID); info != nil {
				targetSkill := strings.TrimSpace(record.Item.TargetSkill)
				if targetSkill != "" && !info.HasSkill(targetSkill) {
					return fmt.Errorf("agent %s does not declare skill %s", actor.ID, targetSkill)
				}
			}
		}
		return nil
	case "release":
		if record.Claim == nil {
			return fmt.Errorf("mailbox item %s has no active claim", record.Item.ID)
		}
		if record.Claim.Holder != actor.ID {
			return fmt.Errorf("mailbox claim for %s is held by %s", record.Item.ID, record.Claim.Holder)
		}
		return nil
	case "complete":
		if record.Item.Kind != agentmailbox.ItemKindTaskRequest {
			return fmt.Errorf("mailbox item %s is not a task request", record.Item.ID)
		}
		if record.Claim != nil {
			if record.Claim.Holder != actor.ID {
				return fmt.Errorf("mailbox task %s is claimed by %s", record.Item.ID, record.Claim.Holder)
			}
			return nil
		}
		if !mailboxPrincipalMatchesID(actor, record.Item.RecipientID(), h.registry) {
			return fmt.Errorf("mailbox task %s is not assigned to actor %s", record.Item.ID, actor.ID)
		}
		if actor.Kind != agentmailbox.PrincipalKindLocalAgent && actor.Kind != agentmailbox.PrincipalKindNetworkAgent {
			return fmt.Errorf("actor %s cannot complete task work", actor.ID)
		}
		return nil
	case "retry":
		if record.State != agentmailbox.StateQueued && record.State != agentmailbox.StateFailed && record.State != agentmailbox.StateDeadLettered {
			return fmt.Errorf("mailbox item %s is not retryable", record.Item.ID)
		}
		if record.Item.From.ID != actor.ID && !mailboxPrincipalMatchesID(actor, record.Item.RecipientID(), h.registry) && (record.Claim == nil || record.Claim.Holder != actor.ID) {
			return fmt.Errorf("mailbox item %s is not visible to actor %s", record.Item.ID, actor.ID)
		}
		return nil
	default:
		return nil
	}
}
