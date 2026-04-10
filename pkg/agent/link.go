package agent

import (
	"context"
	"encoding/json"
	"fmt"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	"github.com/sky10/sky10/pkg/link"
)

// RegisterLinkHandlers registers skylink capability handlers so remote
// devices can send messages to and list agents on this device.
func RegisterLinkHandlers(node *link.Node, registry *Registry, emit Emitter, router *Router) {
	node.RegisterCapability(
		link.Capability{Name: "agent.send", Description: "send a message to a local agent"},
		agentSendHandler(registry, emit, router),
	)
	node.RegisterCapability(
		link.Capability{Name: "agent.list", Description: "list local agents"},
		agentListHandler(registry),
	)
	if router != nil && router.mailbox != nil {
		node.RegisterCapability(
			link.Capability{Name: "agent.mailbox.deliver", Description: "deliver a durable mailbox item"},
			agentMailboxDeliverHandler(router),
		)
	}
}

func agentSendHandler(registry *Registry, emit Emitter, router *Router) link.HandlerFunc {
	return func(ctx context.Context, req *link.PeerRequest) (interface{}, error) {
		var msg Message
		if err := json.Unmarshal(req.Params, &msg); err != nil {
			return nil, err
		}

		// Cache the sender's device ID → peer ID so we can route
		// responses back without needing agent.list first.
		if msg.From != "" && router != nil {
			router.cachePeer(msg.From, req.PeerID)
		}

		if router != nil {
			return router.routeIncoming(ctx, msg)
		}
		if emit != nil {
			emit("agent.message", msg)
		}
		return map[string]string{"id": msg.ID, "status": "sent"}, nil
	}
}

func agentListHandler(registry *Registry) link.HandlerFunc {
	return func(_ context.Context, _ *link.PeerRequest) (interface{}, error) {
		agents := registry.List()
		return map[string]interface{}{
			"agents": agents,
			"count":  len(agents),
		}, nil
	}
}

type mailboxDeliverParams struct {
	Item agentmailbox.Item `json:"item"`
}

func agentMailboxDeliverHandler(router *Router) link.HandlerFunc {
	return func(ctx context.Context, req *link.PeerRequest) (interface{}, error) {
		if router == nil || router.mailbox == nil {
			return nil, fmt.Errorf("mailbox not configured")
		}

		var p mailboxDeliverParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}

		if from := p.Item.From.RouteAddress(); from != "" {
			router.cacheAddress(from, req.PeerID)
		}

		if existing, ok := router.mailbox.Get(p.Item.ID); ok {
			return map[string]interface{}{
				"status": "accepted",
				"item":   existing,
			}, nil
		}

		record, err := router.mailbox.Create(ctx, p.Item)
		if err != nil {
			return nil, err
		}
		router.emitMailboxUpdate("received", record)
		if p.Item.Kind == agentmailbox.ItemKindMessage && p.Item.To != nil {
			go router.DrainLocalPending(context.Background(), p.Item.To.ID)
		}
		return map[string]interface{}{
			"status": "accepted",
			"item":   record,
		}, nil
	}
}
