package agent

import (
	"context"
	"encoding/json"

	"github.com/sky10/sky10/pkg/link"
)

// RegisterLinkHandlers registers skylink capability handlers so remote
// devices can call and list agents on this device.
func RegisterLinkHandlers(node *link.Node, registry *Registry, caller *Caller) {
	node.RegisterCapability(
		link.Capability{Name: "agent.call", Description: "call a local agent"},
		agentCallHandler(registry, caller),
	)
	node.RegisterCapability(
		link.Capability{Name: "agent.list", Description: "list local agents"},
		agentListHandler(registry),
	)
}

func agentCallHandler(registry *Registry, caller *Caller) link.HandlerFunc {
	return func(ctx context.Context, req *link.PeerRequest) (interface{}, error) {
		var p CallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}

		info := registry.Resolve(p.Agent)
		if info == nil {
			return nil, ErrAgentNotFound
		}

		result, err := caller.Call(ctx, info.Endpoint, p.Method, p.Params)
		if err != nil {
			return CallResult{Error: err.Error()}, nil
		}
		return CallResult{Result: result}, nil
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
