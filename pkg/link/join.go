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
	"github.com/libp2p/go-libp2p/core/peer"
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

// JoinResponse is sent by the inviter after approval.
type JoinResponse struct {
	Approved    bool            `json:"approved"`
	IdentityKey json.RawMessage `json:"identity_key,omitempty"` // encrypted identity private key
	Manifest    json.RawMessage `json:"manifest,omitempty"`     // signed device manifest
	NSKeys      []WrappedNSKey  `json:"ns_keys,omitempty"`      // namespace keys wrapped for joiner
	Error       string          `json:"error,omitempty"`
}

// WrappedNSKey is a namespace key wrapped for the joiner's identity.
type WrappedNSKey struct {
	Namespace string `json:"namespace"`
	Wrapped   []byte `json:"wrapped"` // key wrapped with identity pubkey
}

// NSKeyProvider returns namespace keys to send to a joining device.
// Each key is a namespace name + the raw symmetric key (will be wrapped
// for the joiner's identity before sending).
type NSKeyProvider func() []NSKey

// NSKey is a namespace name and its symmetric encryption key.
type NSKey struct {
	Namespace string
	Key       []byte
}

// JoinHandler handles incoming join requests on the inviter side.
type JoinHandler struct {
	bundle    *id.Bundle
	logger    *slog.Logger
	approve   func(req joinRequest) bool // nil = auto-approve
	nsKeyProv NSKeyProvider              // optional: provides namespace keys
}

// NewJoinHandler creates a join handler. If approve is nil, all requests
// are auto-approved (the invite code itself is the authorization).
func NewJoinHandler(bundle *id.Bundle, approve func(joinRequest) bool, logger *slog.Logger) *JoinHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &JoinHandler{bundle: bundle, approve: approve, logger: logger}
}

// SetNSKeyProvider sets a function that returns namespace keys to share
// with joining devices.
func (h *JoinHandler) SetNSKeyProvider(fn NSKeyProvider) {
	h.nsKeyProv = fn
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

	// Auto-approve when callback is nil (invite code = authorization).
	approved := h.approve == nil || h.approve(req)

	resp := JoinResponse{Approved: approved}
	if !approved {
		h.logger.Info("join denied", "device", req.DeviceName)
		writeJoinResponse(s, msg.ID, resp)
		return
	}

	joinerKey, err := skykey.ParseAddress(req.DevicePubKey)
	if err != nil {
		resp.Approved = false
		resp.Error = "invalid device pubkey"
		writeJoinResponse(s, msg.ID, resp)
		return
	}

	// Wrap identity private key with joiner's public key.
	wrappedIdentity, err := skykey.WrapKey(h.bundle.Identity.PrivateKey, joinerKey.PublicKey)
	if err != nil {
		resp.Approved = false
		resp.Error = "failed to wrap identity key"
		writeJoinResponse(s, msg.ID, resp)
		return
	}
	identityJSON, _ := json.Marshal(wrappedIdentity)
	resp.IdentityKey = identityJSON

	// Add joiner to manifest.
	manifest := h.bundle.Manifest
	if !manifest.HasDevice(joinerKey.PublicKey) {
		manifest.AddDevice(joinerKey.PublicKey, req.DeviceName)
		manifest.Sign(h.bundle.Identity.PrivateKey)
	}
	manifestData, _ := json.Marshal(manifest)
	resp.Manifest = manifestData

	// Include namespace keys (wrapped for the shared identity).
	if h.nsKeyProv != nil {
		for _, nsk := range h.nsKeyProv() {
			wrapped, err := skykey.WrapKey(nsk.Key, h.bundle.Identity.PublicKey)
			if err != nil {
				continue
			}
			resp.NSKeys = append(resp.NSKeys, WrappedNSKey{
				Namespace: nsk.Namespace,
				Wrapped:   wrapped,
			})
		}
	}

	h.logger.Info("join approved", "device", req.DeviceName, "ns_keys", len(resp.NSKeys))
	writeJoinResponse(s, msg.ID, resp)
}

func writeJoinResponse(s network.Stream, reqID string, resp JoinResponse) {
	data, _ := json.Marshal(resp)
	WriteMessage(s, &Message{
		ID:     reqID,
		Result: data,
	})
	s.CloseWrite()
}

// RequestJoin connects to the inviter and requests to join. Blocks until
// the inviter responds (approved or denied). If resolver is nil, the
// node must already be connected to the inviter (uses first connected peer).
func RequestJoin(ctx context.Context, node *Node, resolver *Resolver, invite *P2PInvite, devicePubKey string, deviceName string) (*JoinResponse, error) {
	var targetPeerID peer.ID

	if resolver != nil {
		info, err := resolver.Resolve(ctx, invite.Address)
		if err != nil {
			return nil, fmt.Errorf("resolving inviter: %w", err)
		}
		if err := node.host.Connect(ctx, *info); err != nil {
			return nil, fmt.Errorf("connecting to inviter: %w", err)
		}
		targetPeerID = info.ID
	} else {
		// No resolver — use first connected peer.
		peers := node.host.Network().Peers()
		if len(peers) == 0 {
			return nil, fmt.Errorf("no connected peers and no resolver")
		}
		targetPeerID = peers[0]
	}

	// Open join stream.
	s, err := node.host.NewStream(ctx, targetPeerID, JoinProtocol)
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

	var resp JoinResponse
	if err := json.Unmarshal(respMsg.Result, &resp); err != nil {
		return nil, fmt.Errorf("parsing join response: %w", err)
	}
	return &resp, nil
}
