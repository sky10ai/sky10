package agent

import (
	"context"
	"log/slog"
	"time"
)

const (
	healthInterval = 30 * time.Second
	maxMissedBeats = 3
)

// HealthChecker monitors agent heartbeats and deregisters agents that
// stop sending them.
type HealthChecker struct {
	registry *Registry
	emit     Emitter
	logger   *slog.Logger
}

// NewHealthChecker creates a health checker.
func NewHealthChecker(registry *Registry, emit Emitter, logger *slog.Logger) *HealthChecker {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthChecker{
		registry: registry,
		emit:     emit,
		logger:   logger,
	}
}

// Run starts the health check loop. Blocks until ctx is cancelled.
func (h *HealthChecker) Run(ctx context.Context) {
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.check()
		}
	}
}

func (h *HealthChecker) check() {
	now := time.Now().UTC()
	deadline := now.Add(-healthInterval * maxMissedBeats)

	for _, info := range h.registry.List() {
		last, ok := h.registry.LastHeartbeat(info.ID)
		if !ok {
			continue
		}
		if last.Before(deadline) {
			h.logger.Info("deregistering unresponsive agent",
				"id", info.ID,
				"name", info.Name,
				"last_heartbeat", last,
			)
			h.registry.Deregister(info.ID)

			if h.emit != nil {
				h.emit("agent.disconnected", map[string]interface{}{
					"id":        info.ID,
					"name":      info.Name,
					"device_id": info.DeviceID,
				})
			}
		}
	}
}
