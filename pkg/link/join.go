package link

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

// JoinProtocol is the libp2p protocol ID for the P2P join handshake.
const JoinProtocol = protocol.ID("/sky10/join/1.0.0")

const p2pInvitePrefix = "sky10p2p_"

// P2PInvite contains what a new device needs to find and join the inviter.
type P2PInvite struct {
	Address     string   `json:"address"`      // inviter's sky10q... identity address
	NostrRelays []string `json:"nostr_relays"` // relays where inviter publishes multiaddrs
	InviteID    string   `json:"invite_id"`    // random ID for this invite
}

// CreateP2PInvite generates a P2P invite code (no S3 credentials).
func CreateP2PInvite(address string, relays []string) (string, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("generating invite ID: %w", err)
	}
	invite := P2PInvite{
		Address:     address,
		NostrRelays: relays,
		InviteID:    hex.EncodeToString(idBytes),
	}
	data, err := json.Marshal(invite)
	if err != nil {
		return "", err
	}
	return p2pInvitePrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeP2PInvite parses a P2P invite code.
func DecodeP2PInvite(code string) (*P2PInvite, error) {
	if len(code) < len(p2pInvitePrefix) || code[:len(p2pInvitePrefix)] != p2pInvitePrefix {
		return nil, fmt.Errorf("invalid P2P invite code: missing prefix")
	}
	data, err := base64.RawURLEncoding.DecodeString(code[len(p2pInvitePrefix):])
	if err != nil {
		return nil, fmt.Errorf("invalid P2P invite code: %w", err)
	}
	var invite P2PInvite
	if err := json.Unmarshal(data, &invite); err != nil {
		return nil, fmt.Errorf("invalid P2P invite code: %w", err)
	}
	if invite.Address == "" || invite.InviteID == "" {
		return nil, fmt.Errorf("invalid P2P invite code: missing fields")
	}
	return &invite, nil
}

// joinRequest is sent by the joining device.
type joinRequest struct {
	InviteID     string `json:"invite_id"`
	DevicePubKey string `json:"device_pubkey"` // joiner's sky10q... device address
	DeviceName   string `json:"device_name"`
}

// joinResponse is sent by the inviter after approval.
type joinResponse struct {
	Approved    bool            `json:"approved"`
	IdentityKey json.RawMessage `json:"identity_key,omitempty"` // encrypted identity private key
	Manifest    json.RawMessage `json:"manifest,omitempty"`     // signed device manifest
	NSKeys      []wrappedNSKey  `json:"ns_keys,omitempty"`      // namespace keys wrapped for joiner
	Error       string          `json:"error,omitempty"`
}

// wrappedNSKey is a namespace key wrapped for the joiner's identity.
type wrappedNSKey struct {
	Namespace string `json:"namespace"`
	Wrapped   []byte `json:"wrapped"` // key wrapped with identity pubkey
}

// JoinHandler handles incoming join requests on the inviter side.
type JoinHandler struct {
	bundle  *id.Bundle
	logger  *slog.Logger
	approve func(req joinRequest) bool // callback to approve/deny
}

// NewJoinHandler creates a join handler. The approve callback is called
// when a join request arrives — return true to approve, false to deny.
func NewJoinHandler(bundle *id.Bundle, approve func(joinRequest) bool, logger *slog.Logger) *JoinHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &JoinHandler{bundle: bundle, approve: approve, logger: logger}
}

// HandleStream processes an incoming join stream.
func (h *JoinHandler) HandleStream(s network.Stream) {
	defer s.Close()

	// Read join request.
	msg, err := ReadMessage(s)
	if err != nil {
		h.logger.Debug("join: read request failed", "error", err)
		return
	}

	var req joinRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		h.logger.Debug("join: unmarshal request failed", "error", err)
		return
	}

	h.logger.Info("join request received",
		"invite_id", req.InviteID,
		"device", req.DeviceName,
		"pubkey", req.DevicePubKey,
	)

	resp := joinResponse{Approved: false}

	if h.approve != nil && h.approve(req) {
		resp.Approved = true

		// Encrypt identity private key for transfer.
		// Use a shared secret derived from the joiner's address.
		joinerKey, err := skykey.ParseAddress(req.DevicePubKey)
		if err != nil {
			resp.Error = "invalid device pubkey"
			writeJoinResponse(s, msg.ID, resp)
			return
		}

		// Wrap identity private key with joiner's public key.
		wrappedIdentity, err := skykey.WrapKey(h.bundle.Identity.PrivateKey, joinerKey.PublicKey)
		if err != nil {
			resp.Error = "failed to wrap identity key"
			writeJoinResponse(s, msg.ID, resp)
			return
		}
		resp.IdentityKey = json.RawMessage(fmt.Sprintf("%q", wrappedIdentity))

		// Add joiner to manifest.
		manifest := h.bundle.Manifest
		if !manifest.HasDevice(joinerKey.PublicKey) {
			manifest.AddDevice(joinerKey.PublicKey, req.DeviceName)
			manifest.Sign(h.bundle.Identity.PrivateKey)
		}
		manifestData, _ := json.Marshal(manifest)
		resp.Manifest = manifestData

		h.logger.Info("join approved", "device", req.DeviceName)
	} else {
		h.logger.Info("join denied", "device", req.DeviceName)
	}

	writeJoinResponse(s, msg.ID, resp)
}

func writeJoinResponse(s network.Stream, reqID string, resp joinResponse) {
	data, _ := json.Marshal(resp)
	WriteMessage(s, &Message{
		ID:     reqID,
		Result: data,
	})
}

// RequestJoin connects to the inviter and requests to join. Blocks until
// the inviter responds (approved or denied).
func RequestJoin(ctx context.Context, node *Node, resolver *Resolver, invite *P2PInvite, devicePubKey string, deviceName string) (*joinResponse, error) {
	// Resolve and connect to inviter.
	info, err := resolver.Resolve(ctx, invite.Address)
	if err != nil {
		return nil, fmt.Errorf("resolving inviter: %w", err)
	}
	if err := node.host.Connect(ctx, *info); err != nil {
		return nil, fmt.Errorf("connecting to inviter: %w", err)
	}

	// Open join stream.
	s, err := node.host.NewStream(ctx, info.ID, JoinProtocol)
	if err != nil {
		return nil, fmt.Errorf("opening join stream: %w", err)
	}
	defer s.Close()

	req := joinRequest{
		InviteID:     invite.InviteID,
		DevicePubKey: devicePubKey,
		DeviceName:   deviceName,
	}
	params, _ := json.Marshal(req)

	if err := WriteMessage(s, &Message{
		ID:     invite.InviteID,
		Method: "join",
		Params: params,
	}); err != nil {
		return nil, fmt.Errorf("sending join request: %w", err)
	}
	s.CloseWrite()

	// Wait for response.
	respMsg, err := ReadMessage(s)
	if err != nil {
		return nil, fmt.Errorf("reading join response: %w", err)
	}

	var resp joinResponse
	if err := json.Unmarshal(respMsg.Result, &resp); err != nil {
		return nil, fmt.Errorf("parsing join response: %w", err)
	}
	return &resp, nil
}
