package sandbox

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/sky10/sky10/pkg/sandbox/bridge"
	bridgemessengers "github.com/sky10/sky10/pkg/sandbox/bridge/messengers"
)

type MessengersBridgeManager struct {
	bridge *sandboxBridgeManager
}

func NewMessengersBridgeManager(backend bridgemessengers.Backend, logger *slog.Logger) *MessengersBridgeManager {
	if backend == nil {
		return &MessengersBridgeManager{}
	}
	return &MessengersBridgeManager{
		bridge: newSandboxBridgeManager(
			"messengers",
			logger,
			messengersBridgeURL,
			func(ctx context.Context, rec Record, wsURL string) (*bridge.Conn, *http.Response, error) {
				return bridge.Dial(ctx, wsURL, bridgemessengers.NewBridgeHandler(backend, rec.Slug))
			},
		),
	}
}

func (m *MessengersBridgeManager) Connect(ctx context.Context, rec Record) error {
	if m == nil || m.bridge == nil {
		return nil
	}
	return m.bridge.Connect(ctx, rec)
}

func (m *MessengersBridgeManager) Close(slug string) {
	if m == nil || m.bridge == nil {
		return
	}
	m.bridge.Close(slug)
}

func messengersBridgeURL(rec Record) (string, error) {
	return sandboxCapabilityBridgeURL(
		rec,
		bridgemessengers.EndpointPath,
		bridgemessengers.BridgeRoleQuery,
		bridgemessengers.BridgeRoleHost,
	)
}
