package host

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	shimrpc "github.com/sky10/sky10/pkg/messaging/shim/rpc"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

// Config configures one runtime-facing shim host.
type Config struct {
	SocketPath string
	Version    string
	Service    shimrpc.Service
	Events     <-chan messaging.Event
	Logger     *slog.Logger
	OnServe    func()
}

// Serve runs a local JSON-RPC server that exposes only messaging.shim.* methods
// for the configured exposure-bound service.
func Serve(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.SocketPath) == "" {
		return fmt.Errorf("messaging shim host socket_path is required")
	}
	if cfg.Service == nil {
		return fmt.Errorf("messaging shim host service is required")
	}
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = "messaging-shim"
	}

	server := skyrpc.NewServer(cfg.SocketPath, version, cfg.Logger)
	server.RegisterHandler(shimrpc.NewHandler(shimrpc.Config{Service: cfg.Service}))
	if cfg.Events != nil {
		go bridgeEvents(ctx, server, cfg.Events)
	}
	if cfg.OnServe != nil {
		server.OnServe(cfg.OnServe)
	}
	return server.Serve(ctx)
}

func bridgeEvents(ctx context.Context, server *skyrpc.Server, events <-chan messaging.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			server.Emit(messaging.FanoutEventName, event)
		}
	}
}
