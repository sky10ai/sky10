package sandbox

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/sky10/sky10/pkg/sandbox/bridge"
	bridgex402 "github.com/sky10/sky10/pkg/sandbox/bridge/x402"
)

type MeteredServicesBridgeManager struct {
	bridge *sandboxBridgeManager
}

func NewMeteredServicesBridgeManager(backend bridgex402.Backend, logger *slog.Logger) *MeteredServicesBridgeManager {
	if backend == nil {
		return &MeteredServicesBridgeManager{}
	}
	return &MeteredServicesBridgeManager{
		bridge: newSandboxBridgeManager(
			"metered-services",
			logger,
			meteredServicesBridgeURL,
			func(ctx context.Context, rec Record, wsURL string) (*bridge.Conn, *http.Response, error) {
				return bridge.Dial(ctx, wsURL, bridgex402.NewBridgeHandler(backend, rec.Slug))
			},
		),
	}
}

func (m *MeteredServicesBridgeManager) Connect(ctx context.Context, rec Record) error {
	if m == nil || m.bridge == nil {
		return nil
	}
	return m.bridge.Connect(ctx, rec)
}

func (m *MeteredServicesBridgeManager) Close(slug string) {
	if m == nil || m.bridge == nil {
		return
	}
	m.bridge.Close(slug)
}

func meteredServicesBridgeURL(rec Record) (string, error) {
	return sandboxCapabilityBridgeURL(
		rec,
		bridgex402.EndpointPath,
		bridgex402.BridgeRoleQuery,
		bridgex402.BridgeRoleHost,
	)
}
