package id

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler dispatches identity.* RPC methods.
type RPCHandler struct {
	bundle *Bundle
}

// NewRPCHandler creates an RPC handler for identity operations.
func NewRPCHandler(bundle *Bundle) *RPCHandler {
	return &RPCHandler{bundle: bundle}
}

// Dispatch handles identity.* methods.
func (h *RPCHandler) Dispatch(_ context.Context, method string, _ json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "identity.") {
		return nil, nil, false
	}

	switch method {
	case "identity.show":
		return h.rpcShow()
	case "identity.devices":
		return h.rpcDevices()
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

type showResult struct {
	Address       string `json:"address"`
	DeviceAddress string `json:"device_address"`
	DeviceCount   int    `json:"device_count"`
}

func (h *RPCHandler) rpcShow() (interface{}, error, bool) {
	return showResult{
		Address:       h.bundle.Address(),
		DeviceAddress: h.bundle.DeviceAddress(),
		DeviceCount:   len(h.bundle.Manifest.Devices),
	}, nil, true
}

type deviceInfo struct {
	PublicKey string `json:"public_key"`
	Name      string `json:"name"`
	AddedAt   string `json:"added_at"`
	Current   bool   `json:"current"`
}

type devicesResult struct {
	Identity string       `json:"identity"`
	Devices  []deviceInfo `json:"devices"`
}

func (h *RPCHandler) rpcDevices() (interface{}, error, bool) {
	devices := make([]deviceInfo, 0, len(h.bundle.Manifest.Devices))
	devicePub := hex.EncodeToString(h.bundle.Device.PublicKey)

	for _, d := range h.bundle.Manifest.Devices {
		pub := hex.EncodeToString(d.PublicKey)
		devices = append(devices, deviceInfo{
			PublicKey: pub,
			Name:      d.Name,
			AddedAt:   d.AddedAt.Format("2006-01-02T15:04:05Z"),
			Current:   pub == devicePub,
		})
	}

	return devicesResult{
		Identity: h.bundle.Address(),
		Devices:  devices,
	}, nil, true
}
