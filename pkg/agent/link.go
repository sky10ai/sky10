package agent

import (
	"context"
	"encoding/json"

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
