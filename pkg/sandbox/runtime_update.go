package sandbox

import (
	"context"
	"fmt"
	"strings"
)

type RuntimeStatusResult struct {
	Name              string                 `json:"name"`
	Slug              string                 `json:"slug"`
	Template          string                 `json:"template"`
	Endpoint          string                 `json:"endpoint,omitempty"`
	Reachable         bool                   `json:"reachable"`
	Version           string                 `json:"version,omitempty"`
	HealthStatus      string                 `json:"health_status,omitempty"`
	Uptime            string                 `json:"uptime,omitempty"`
	UpdateStatus      map[string]interface{} `json:"update_status,omitempty"`
	UpdateStatusError string                 `json:"update_status_error,omitempty"`
	Error             string                 `json:"error,omitempty"`
}

type RuntimeUpgradeResult struct {
	Name     string                 `json:"name"`
	Slug     string                 `json:"slug"`
	Template string                 `json:"template"`
	Endpoint string                 `json:"endpoint,omitempty"`
	Status   string                 `json:"status"`
	Result   map[string]interface{} `json:"result,omitempty"`
}

type runtimeGuestHealth struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

func (m *Manager) RuntimeStatus(ctx context.Context, name string) (*RuntimeStatusResult, error) {
	rec, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}

	result := baseRuntimeStatus(*rec)
	if m.guestRPC == nil {
		result.Error = "guest RPC unavailable"
		return result, nil
	}
	if result.Endpoint == "" {
		result.Error = "guest RPC endpoint unavailable"
		return result, nil
	}

	var health runtimeGuestHealth
	if err := m.guestRPC(ctx, result.Endpoint, "skyfs.health", nil, &health); err != nil {
		result.Error = fmt.Sprintf("guest health: %v", err)
		return result, nil
	}
	result.Reachable = true
	result.HealthStatus = health.Status
	result.Version = health.Version
	result.Uptime = health.Uptime

	var updateStatus map[string]interface{}
	if err := m.guestRPC(ctx, result.Endpoint, "system.update.status", nil, &updateStatus); err != nil {
		result.UpdateStatusError = err.Error()
		return result, nil
	}
	result.UpdateStatus = updateStatus
	return result, nil
}

func (m *Manager) RuntimeUpgrade(ctx context.Context, name string) (*RuntimeUpgradeResult, error) {
	rec, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if m.guestRPC == nil {
		return nil, fmt.Errorf("guest RPC unavailable")
	}

	endpoint := strings.TrimSpace(guestSky10RPCAddress(*rec))
	if endpoint == "" {
		return nil, fmt.Errorf("guest RPC endpoint unavailable for sandbox %q", rec.Name)
	}

	var updateResult map[string]interface{}
	if err := m.guestRPC(ctx, endpoint, "system.update", nil, &updateResult); err != nil {
		return nil, fmt.Errorf("upgrading runtime for sandbox %q: %w", rec.Name, err)
	}

	status := "requested"
	if raw, ok := updateResult["status"]; ok {
		if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
			status = value
		}
	}

	return &RuntimeUpgradeResult{
		Name:     rec.Name,
		Slug:     rec.Slug,
		Template: rec.Template,
		Endpoint: endpoint,
		Status:   status,
		Result:   updateResult,
	}, nil
}

func baseRuntimeStatus(rec Record) *RuntimeStatusResult {
	return &RuntimeStatusResult{
		Name:     rec.Name,
		Slug:     rec.Slug,
		Template: rec.Template,
		Endpoint: strings.TrimSpace(guestSky10RPCAddress(rec)),
	}
}
