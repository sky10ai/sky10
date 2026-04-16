package join

import (
	"encoding/json"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

// Protocol is the libp2p protocol ID for the P2P join handshake.
const Protocol = protocol.ID("/sky10/join/1.0.0")

const (
	NSScopeKV = "kv"
	NSScopeFS = "fs"
)

// NSKeyProvider returns namespace keys to send to a joining device.
type NSKeyProvider func() []NSKey

// NSKey is a namespace name and its symmetric encryption key.
type NSKey struct {
	Namespace string
	Scope     string
	Key       []byte
}

// Request is sent by the joining device.
type Request struct {
	InviteID     string `json:"invite_id"`
	DevicePubKey string `json:"device_pubkey"`
	DeviceName   string `json:"device_name"`
	DeviceRole   string `json:"device_role,omitempty"`
}

// Response is sent by the inviter after approval.
type Response struct {
	Approved    bool            `json:"approved"`
	IdentityKey json.RawMessage `json:"identity_key,omitempty"`
	Manifest    json.RawMessage `json:"manifest,omitempty"`
	NSKeys      []WrappedNSKey  `json:"ns_keys,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// WrappedNSKey is a namespace key wrapped for the joiner's identity.
type WrappedNSKey struct {
	Namespace string `json:"namespace"`
	Scope     string `json:"scope,omitempty"`
	Wrapped   []byte `json:"wrapped"`
}

// Handler handles incoming join requests on the inviter side.
type Handler struct {
	bundle          *id.Bundle
	logger          *slog.Logger
	approve         func(req Request) bool // nil = auto-approve
	nsKeyProv       NSKeyProvider
	onBundleUpdated func(*id.Bundle) error
}

// NewHandler creates a join handler. If approve is nil, all requests
// are auto-approved (the invite code itself is the authorization).
func NewHandler(bundle *id.Bundle, approve func(Request) bool, logger *slog.Logger) *Handler {
	logger = componentLogger(logger)
	return &Handler{bundle: bundle, approve: approve, logger: logger}
}

// SetNSKeyProvider sets a function that returns namespace keys to share
// with joining devices.
func (h *Handler) SetNSKeyProvider(fn NSKeyProvider) {
	h.nsKeyProv = fn
}

// SetOnBundleUpdated registers a callback invoked after the join handler
// mutates the bundle manifest. Returning an error rejects the join so the
// inviter never hands out a membership update it failed to persist locally.
func (h *Handler) SetOnBundleUpdated(fn func(*id.Bundle) error) {
	h.onBundleUpdated = fn
}

// HandleStream processes an incoming join stream.
func (h *Handler) HandleStream(s network.Stream) {
	defer s.Close()

	msg, err := link.ReadMessage(s)
	if err != nil {
		h.logger.Debug("join: read request failed", "error", err)
		return
	}

	var req Request
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		h.logger.Debug("join: unmarshal request failed", "error", err)
		return
	}

	h.logger.Info("join request received",
		"invite_id", req.InviteID,
		"device", req.DeviceName,
		"pubkey", req.DevicePubKey,
	)

	approved := h.approve == nil || h.approve(req)

	resp := Response{Approved: approved}
	if !approved {
		h.logger.Info("join denied", "device", req.DeviceName)
		writeResponse(s, msg.ID, resp)
		return
	}

	joinerKey, err := skykey.ParseAddress(req.DevicePubKey)
	if err != nil {
		resp.Approved = false
		resp.Error = "invalid device pubkey"
		writeResponse(s, msg.ID, resp)
		return
	}
	deviceRole, err := NormalizeJoinDeviceRole(req.DeviceRole)
	if err != nil {
		resp.Approved = false
		resp.Error = err.Error()
		writeResponse(s, msg.ID, resp)
		return
	}

	wrappedIdentity, err := skykey.WrapKey(h.bundle.Identity.PrivateKey, joinerKey.PublicKey)
	if err != nil {
		resp.Approved = false
		resp.Error = "failed to wrap identity key"
		writeResponse(s, msg.ID, resp)
		return
	}
	identityJSON, _ := json.Marshal(wrappedIdentity)
	resp.IdentityKey = identityJSON

	manifest := h.bundle.Manifest
	changed := false
	if !manifest.HasDevice(joinerKey.PublicKey) {
		manifest.AddDeviceWithRole(joinerKey.PublicKey, req.DeviceName, deviceRole)
		manifest.Sign(h.bundle.Identity.PrivateKey)
		changed = true
	}
	if changed && h.onBundleUpdated != nil {
		if err := h.onBundleUpdated(h.bundle); err != nil {
			resp.Approved = false
			resp.Error = "failed to persist private-network membership"
			writeResponse(s, msg.ID, resp)
			return
		}
	}
	manifestData, _ := json.Marshal(manifest)
	resp.Manifest = manifestData

	if h.nsKeyProv != nil {
		for _, nsk := range h.nsKeyProv() {
			wrapped, err := skykey.WrapKey(nsk.Key, h.bundle.Identity.PublicKey)
			if err != nil {
				continue
			}
			resp.NSKeys = append(resp.NSKeys, WrappedNSKey{
				Namespace: nsk.Namespace,
				Scope:     nsk.Scope,
				Wrapped:   wrapped,
			})
		}
	}

	h.logger.Info("join approved", "device", req.DeviceName, "role", id.NormalizeDeviceRole(deviceRole), "ns_keys", len(resp.NSKeys))
	writeResponse(s, msg.ID, resp)
}

func writeResponse(s network.Stream, reqID string, resp Response) {
	data, _ := json.Marshal(resp)
	link.WriteMessage(s, &link.Message{
		ID:     reqID,
		Result: data,
	})
	s.CloseWrite()
}
