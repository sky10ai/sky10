package id

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	skykey "github.com/sky10/sky10/pkg/key"
)

// RPCHandler dispatches identity.* RPC methods.
type RPCHandler struct {
	bundle                 *Bundle
	deviceMetadataProvider DeviceMetadataProvider
	inviteHandler          InviteHandler
	joinHandler            JoinHandler
	approveHandler         ApproveHandler
	deviceRemoveHandler    DeviceRemoveHandler
}

// NewRPCHandler creates an RPC handler for identity operations.
func NewRPCHandler(bundle *Bundle) *RPCHandler {
	return &RPCHandler{bundle: bundle}
}

// DeviceMetadata is optional per-device information that can be merged into
// the manifest-backed identity.deviceList response.
type DeviceMetadata struct {
	Alias      string
	Platform   string
	IP         string
	Location   string
	Version    string
	LastSeen   string
	Multiaddrs []string
}

// DeviceMetadataProvider returns additional metadata keyed by device public key
// hex. It should return best-effort data and can safely return partial results.
type DeviceMetadataProvider func(context.Context) (map[string]DeviceMetadata, error)

// InviteHandler generates an invite code for this identity.
type InviteHandler func(context.Context) (string, error)

// JoinHandler joins another identity/private network using an invite code.
type JoinHandler func(context.Context, string) (interface{}, error)

// ApproveHandler approves pending join requests and returns the count.
type ApproveHandler func(context.Context) (int, error)

// DeviceRemoveHandler removes a device by its canonical device public key hex.
type DeviceRemoveHandler func(context.Context, string) (interface{}, error)

// SetDeviceMetadataProvider configures optional metadata enrichment for
// identity.deviceList.
func (h *RPCHandler) SetDeviceMetadataProvider(fn DeviceMetadataProvider) {
	h.deviceMetadataProvider = fn
}

// SetInviteHandler configures identity.invite.
func (h *RPCHandler) SetInviteHandler(fn InviteHandler) {
	h.inviteHandler = fn
}

// SetJoinHandler configures identity.join.
func (h *RPCHandler) SetJoinHandler(fn JoinHandler) {
	h.joinHandler = fn
}

// SetApproveHandler configures identity.approve.
func (h *RPCHandler) SetApproveHandler(fn ApproveHandler) {
	h.approveHandler = fn
}

// SetDeviceRemoveHandler configures identity.deviceRemove.
func (h *RPCHandler) SetDeviceRemoveHandler(fn DeviceRemoveHandler) {
	h.deviceRemoveHandler = fn
}

// Dispatch handles identity.* methods.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "identity.") {
		return nil, nil, false
	}

	switch method {
	case "identity.show":
		return h.rpcShow()
	case "identity.devices":
		return h.rpcDevices()
	case "identity.deviceList":
		return h.rpcDeviceList(ctx)
	case "identity.invite":
		return h.rpcInvite(ctx)
	case "identity.join":
		return h.rpcJoin(ctx, params)
	case "identity.approve":
		return h.rpcApprove(ctx)
	case "identity.deviceRemove":
		return h.rpcDeviceRemove(ctx, params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

type showResult struct {
	Address      string `json:"address"`
	DeviceID     string `json:"device_id"`
	DevicePubKey string `json:"device_pubkey"`
	DeviceCount  int    `json:"device_count"`
}

func (h *RPCHandler) rpcShow() (interface{}, error, bool) {
	return showResult{
		Address:      h.bundle.Address(),
		DeviceID:     h.bundle.DeviceID(),
		DevicePubKey: h.bundle.DevicePubKeyHex(),
		DeviceCount:  len(h.bundle.Manifest.Devices),
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
			AddedAt:   d.AddedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Current:   pub == devicePub,
		})
	}

	return devicesResult{
		Identity: h.bundle.Address(),
		Devices:  devices,
	}, nil, true
}

type deviceListItem struct {
	ID         string   `json:"id"`
	PubKey     string   `json:"pubkey"`
	Name       string   `json:"name"`
	Alias      string   `json:"alias,omitempty"`
	Joined     string   `json:"joined"`
	Platform   string   `json:"platform,omitempty"`
	IP         string   `json:"ip,omitempty"`
	Location   string   `json:"location,omitempty"`
	Version    string   `json:"version,omitempty"`
	LastSeen   string   `json:"last_seen,omitempty"`
	Multiaddrs []string `json:"multiaddrs,omitempty"`
	Current    bool     `json:"current"`
}

type deviceListResult struct {
	Identity   string           `json:"identity"`
	Devices    []deviceListItem `json:"devices"`
	ThisDevice string           `json:"this_device"`
}

func (h *RPCHandler) rpcDeviceList(ctx context.Context) (interface{}, error, bool) {
	metadataByKey := map[string]DeviceMetadata{}
	if h.deviceMetadataProvider != nil {
		metadata, err := h.deviceMetadataProvider(ctx)
		if err != nil {
			return nil, err, true
		}
		if metadata != nil {
			metadataByKey = metadata
		}
	}

	devices := make([]deviceListItem, 0, len(h.bundle.Manifest.Devices))
	currentPub := h.bundle.DevicePubKeyHex()

	for _, d := range h.bundle.Manifest.Devices {
		pubHex := hex.EncodeToString(d.PublicKey)
		meta := metadataByKey[strings.ToLower(pubHex)]
		devices = append(devices, deviceListItem{
			ID:         deviceIDFromPubKey(d.PublicKey),
			PubKey:     pubHex,
			Name:       d.Name,
			Alias:      meta.Alias,
			Joined:     d.AddedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Platform:   meta.Platform,
			IP:         meta.IP,
			Location:   meta.Location,
			Version:    meta.Version,
			LastSeen:   meta.LastSeen,
			Multiaddrs: append([]string(nil), meta.Multiaddrs...),
			Current:    strings.EqualFold(pubHex, currentPub),
		})
	}

	return deviceListResult{
		Identity:   h.bundle.Address(),
		Devices:    devices,
		ThisDevice: h.bundle.DeviceID(),
	}, nil, true
}

func (h *RPCHandler) rpcInvite(ctx context.Context) (interface{}, error, bool) {
	if h.inviteHandler == nil {
		return nil, fmt.Errorf("identity.invite not available"), true
	}
	code, err := h.inviteHandler(ctx)
	if err != nil {
		return nil, err, true
	}
	return map[string]string{"code": code}, nil, true
}

func (h *RPCHandler) rpcJoin(ctx context.Context, params json.RawMessage) (interface{}, error, bool) {
	if h.joinHandler == nil {
		return nil, fmt.Errorf("identity.join not available"), true
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err), true
	}
	if strings.TrimSpace(p.Code) == "" {
		return nil, fmt.Errorf("code required"), true
	}
	result, err := h.joinHandler(ctx, strings.TrimSpace(p.Code))
	if err != nil {
		return nil, err, true
	}
	return result, nil, true
}

func (h *RPCHandler) rpcApprove(ctx context.Context) (interface{}, error, bool) {
	if h.approveHandler == nil {
		return map[string]int{"approved": 0}, nil, true
	}
	approved, err := h.approveHandler(ctx)
	if err != nil {
		return nil, err, true
	}
	return map[string]int{"approved": approved}, nil, true
}

func (h *RPCHandler) rpcDeviceRemove(ctx context.Context, params json.RawMessage) (interface{}, error, bool) {
	if h.deviceRemoveHandler == nil {
		return nil, fmt.Errorf("identity.deviceRemove not available"), true
	}
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err), true
	}
	if p.Pubkey == "" {
		return nil, fmt.Errorf("pubkey required"), true
	}
	result, err := h.deviceRemoveHandler(ctx, p.Pubkey)
	if err != nil {
		return nil, err, true
	}
	return result, nil, true
}

func deviceIDFromPubKey(pub ed25519.PublicKey) string {
	return "D-" + skykey.FromPublicKey(pub).ShortID()
}
