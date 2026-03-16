package fs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
)

const invitePrefix = "sky10invite_"

// Invite contains everything a second device needs to join.
type Invite struct {
	Endpoint       string `json:"endpoint"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	ForcePathStyle bool   `json:"force_path_style,omitempty"`
	DevicePubKey   string `json:"device_pubkey"` // inviter's sky10q... address
	InviteID       string `json:"invite_id"`
}

// CreateInvite generates an invite code and writes a waiting marker to S3.
func CreateInvite(ctx context.Context, backend adapter.Backend, cfg InviteConfig) (string, error) {
	// Generate random invite ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("generating invite ID: %w", err)
	}
	inviteID := hex.EncodeToString(idBytes)

	invite := Invite{
		Endpoint:       cfg.Endpoint,
		Bucket:         cfg.Bucket,
		Region:         cfg.Region,
		AccessKey:      cfg.AccessKey,
		SecretKey:      cfg.SecretKey,
		ForcePathStyle: cfg.ForcePathStyle,
		DevicePubKey:   cfg.DevicePubKey,
		InviteID:       inviteID,
	}

	// Write waiting marker to S3
	marker := []byte(`{"status":"waiting","created":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	r := bytes.NewReader(marker)
	if err := backend.Put(ctx, "invites/"+inviteID+"/waiting", r, int64(len(marker))); err != nil {
		return "", fmt.Errorf("writing invite marker: %w", err)
	}

	// Encode invite as base64
	data, err := json.Marshal(invite)
	if err != nil {
		return "", err
	}

	code := invitePrefix + base64.RawURLEncoding.EncodeToString(data)
	return code, nil
}

// DecodeInvite parses an invite code back to an Invite struct.
func DecodeInvite(code string) (*Invite, error) {
	if len(code) < len(invitePrefix) || code[:len(invitePrefix)] != invitePrefix {
		return nil, fmt.Errorf("invalid invite code: missing prefix")
	}

	data, err := base64.RawURLEncoding.DecodeString(code[len(invitePrefix):])
	if err != nil {
		return nil, fmt.Errorf("invalid invite code: %w", err)
	}

	var invite Invite
	if err := json.Unmarshal(data, &invite); err != nil {
		return nil, fmt.Errorf("invalid invite code: %w", err)
	}

	if invite.InviteID == "" || invite.Bucket == "" || invite.Endpoint == "" {
		return nil, fmt.Errorf("invalid invite code: missing fields")
	}

	return &invite, nil
}

// SubmitJoin writes the joining device's public key to the invite mailbox.
func SubmitJoin(ctx context.Context, backend adapter.Backend, inviteID string, joinerPubKey string) error {
	payload := []byte(`{"pubkey":"` + joinerPubKey + `","submitted":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	r := bytes.NewReader(payload)
	return backend.Put(ctx, "invites/"+inviteID+"/pubkey", r, int64(len(payload)))
}

// CheckJoinRequest reads the joining device's public key from the invite mailbox.
// Returns empty string if no join request yet.
func CheckJoinRequest(ctx context.Context, backend adapter.Backend, inviteID string) (string, error) {
	rc, err := backend.Get(ctx, "invites/"+inviteID+"/pubkey")
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			return "", nil // no join request yet
		}
		return "", err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	var result struct {
		PubKey string `json:"pubkey"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}

	return result.PubKey, nil
}

// ApproveJoin grants the joining device access to all namespaces and writes the granted marker.
func ApproveJoin(ctx context.Context, backend adapter.Backend, identity *Identity, joinerAddress string, inviteID string) error {
	// Parse joiner's public key from address
	joinerKey, err := parseAddressToPublicKey(joinerAddress)
	if err != nil {
		return fmt.Errorf("parsing joiner address: %w", err)
	}

	// List all namespace keys and wrap for the joiner
	nsKeys, err := backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return fmt.Errorf("listing namespace keys: %w", err)
	}

	for _, nsKeyPath := range nsKeys {
		// Only process keys we can unwrap (skip other devices' wrapped keys)
		rc, err := backend.Get(ctx, nsKeyPath)
		if err != nil {
			continue
		}
		wrapped, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		nsKey, err := UnwrapNamespaceKey(wrapped, identity.PrivateKey)
		if err != nil {
			continue // not our key, skip
		}

		// Re-wrap for the joiner's public key
		joinerWrapped, err := WrapNamespaceKey(nsKey, joinerKey)
		if err != nil {
			continue
		}

		// Write joiner's wrapped key using their device ID
		// Path: keys/namespaces/<namespace>.<deviceID>.ns.enc
		joinerID := shortPubkeyID(joinerAddress)
		nsName := extractNamespaceName(nsKeyPath)
		joinerKeyPath := "keys/namespaces/" + nsName + "." + joinerID + ".ns.enc"
		r := bytes.NewReader(joinerWrapped)
		backend.Put(ctx, joinerKeyPath, r, int64(len(joinerWrapped)))
	}

	// Write granted marker
	granted := []byte(`{"status":"granted","granted":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	r := bytes.NewReader(granted)
	if err := backend.Put(ctx, "invites/"+inviteID+"/granted", r, int64(len(granted))); err != nil {
		return fmt.Errorf("writing granted marker: %w", err)
	}

	return nil
}

// IsGranted checks if an invite has been approved.
func IsGranted(ctx context.Context, backend adapter.Backend, inviteID string) (bool, error) {
	_, err := backend.Head(ctx, "invites/"+inviteID+"/granted")
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CleanupInvite removes all invite artifacts from S3.
func CleanupInvite(ctx context.Context, backend adapter.Backend, inviteID string) {
	keys, _ := backend.List(ctx, "invites/"+inviteID+"/")
	for _, k := range keys {
		backend.Delete(ctx, k)
	}
}

// InviteConfig holds the parameters needed to create an invite.
type InviteConfig struct {
	Endpoint       string
	Bucket         string
	Region         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	DevicePubKey   string
}

// extractNamespaceName gets the namespace from a key path.
// "keys/namespaces/default.ns.enc" → "default"
// "keys/namespaces/journal.abc123.ns.enc" → "journal"
func extractNamespaceName(path string) string {
	// Strip prefix
	name := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		name = path[i+1:]
	}
	// Take everything before the first dot
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// parseAddressToPublicKey converts a sky10q... address to an ed25519.PublicKey.
func parseAddressToPublicKey(address string) ([]byte, error) {
	k, err := skykey.ParseAddress(address)
	if err != nil {
		return nil, err
	}
	return k.PublicKey, nil
}
