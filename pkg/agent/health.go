package agent

import (
	"context"
	"log/slog"
	"time"
)

const (
	healthInterval = 30 * time.Second
	maxFailedPings = 3
)

// HealthChecker periodically pings registered agents and deregisters
// those that fail to respond.
type HealthChecker struct {
	registry *Registry
	caller   *Caller
	emit     Emitter
	logger   *slog.Logger
}

// NewHealthChecker creates a health checker.
func NewHealthChecker(registry *Registry, caller *Caller, emit Emitter, logger *slog.Logger) *HealthChecker {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthChecker{
		registry: registry,
		caller:   caller,
		emit:     emit,
		logger:   logger,
	}
}

// Run starts the health check loop. Blocks until ctx is cancelled.
func (h *HealthChecker) Run(ctx context.Context) {
	// Track consecutive failures per agent ID.
	failures := make(map[string]int)

	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.check(ctx, failures)
		}
	}
}

func (h *HealthChecker) check(ctx context.Context, failures map[string]int) {
	agents := h.registry.List()
	for _, info := range agents {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := h.caller.Ping(pingCtx, info.Endpoint)
		cancel()

		if err == nil {
			delete(failures, info.ID)
			continue
		}

		failures[info.ID]++
		h.logger.Warn("agent health check failed",
			"id", info.ID,
			"name", info.Name,
			"failures", failures[info.ID],
			"error", err,
		)

		if failures[info.ID] >= maxFailedPings {
			h.logger.Info("deregistering unresponsive agent",
				"id", info.ID,
				"name", info.Name,
			)
			h.registry.Deregister(info.ID)
			delete(failures, info.ID)

			if h.emit != nil {
				h.emit("agent.disconnected", map[string]interface{}{
					"id":        info.ID,
					"name":      info.Name,
					"device_id": info.DeviceID,
				})
			}
		}
	}

	// Clean up failure entries for agents that no longer exist.
	for id := range failures {
		if h.registry.Get(id) == nil {
			delete(failures, id)
		}
	}
}
