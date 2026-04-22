package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type RPCHandler struct {
	service *Service
}

func NewRPCHandler(service *Service) *RPCHandler {
	return &RPCHandler{service: service}
}

func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "codex.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "codex.status":
		result, err = h.service.Status(ctx)
	case "codex.loginStart":
		result, err = h.service.StartLogin(ctx)
	case "codex.loginComplete":
		var parsed CompleteLoginParams
		if len(params) > 0 && string(params) != "null" {
			if decodeErr := json.Unmarshal(params, &parsed); decodeErr != nil {
				return nil, fmt.Errorf("decode params: %w", decodeErr), true
			}
		}
		result, err = h.service.CompleteLogin(ctx, parsed)
	case "codex.loginCancel":
		result, err = h.service.CancelLogin(ctx)
	case "codex.logout":
		result, err = h.service.Logout(ctx)
	case "codex.chat":
		var parsed ChatParams
		if len(params) > 0 && string(params) != "null" {
			if decodeErr := json.Unmarshal(params, &parsed); decodeErr != nil {
				return nil, fmt.Errorf("decode params: %w", decodeErr), true
			}
		}
		result, err = h.service.Chat(ctx, parsed)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}
