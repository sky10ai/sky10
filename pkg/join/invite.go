// Package join handles device invite and join flows — both P2P (default)
// and S3-backed (legacy). The invite code is the authorization: no
// separate approval step is needed.
package join

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

const (
	p2pInvitePrefix = "sky10p2p_"
	s3InvitePrefix  = "sky10invite_"
)

// P2PInvite contains what a new device needs to find and join the inviter.
type P2PInvite struct {
	Address     string   `json:"address"`      // inviter's sky10q... identity address
	NostrRelays []string `json:"nostr_relays"` // relays where inviter publishes multiaddrs
	InviteID    string   `json:"invite_id"`    // random ID for this invite
}

// S3Invite contains S3 credentials for the legacy join flow.
type S3Invite struct {
	Endpoint       string `json:"endpoint"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	ForcePathStyle bool   `json:"force_path_style,omitempty"`
	DevicePubKey   string `json:"device_pubkey"`
	InviteID       string `json:"invite_id"`
}

// S3InviteConfig holds the parameters needed to create an S3 invite.
type S3InviteConfig struct {
	Endpoint       string
	Bucket         string
	Region         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	DevicePubKey   string
}

// IsP2PInvite reports whether a code is a P2P invite.
func IsP2PInvite(code string) bool {
	return strings.HasPrefix(code, p2pInvitePrefix)
}

// --- P2P invite ---

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
	if !strings.HasPrefix(code, p2pInvitePrefix) {
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

// --- S3 invite ---

// CreateS3Invite generates an S3 invite code and writes a waiting marker.
func CreateS3Invite(ctx context.Context, backend adapter.Backend, cfg S3InviteConfig) (string, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("generating invite ID: %w", err)
	}
	inviteID := hex.EncodeToString(idBytes)

	invite := S3Invite{
		Endpoint:       cfg.Endpoint,
		Bucket:         cfg.Bucket,
		Region:         cfg.Region,
		AccessKey:      cfg.AccessKey,
		SecretKey:      cfg.SecretKey,
		ForcePathStyle: cfg.ForcePathStyle,
		DevicePubKey:   cfg.DevicePubKey,
		InviteID:       inviteID,
	}

	marker := []byte(`{"status":"waiting","created":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	if err := backend.Put(ctx, "invites/"+inviteID+"/waiting", bytes.NewReader(marker), int64(len(marker))); err != nil {
		return "", fmt.Errorf("writing invite marker: %w", err)
	}

	data, err := json.Marshal(invite)
	if err != nil {
		return "", err
	}
	return s3InvitePrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeS3Invite parses an S3 invite code.
func DecodeS3Invite(code string) (*S3Invite, error) {
	if !strings.HasPrefix(code, s3InvitePrefix) {
		return nil, fmt.Errorf("invalid S3 invite code: missing prefix")
	}
	data, err := base64.RawURLEncoding.DecodeString(code[len(s3InvitePrefix):])
	if err != nil {
		return nil, fmt.Errorf("invalid invite code: %w", err)
	}
	var invite S3Invite
	if err := json.Unmarshal(data, &invite); err != nil {
		return nil, fmt.Errorf("invalid invite code: %w", err)
	}
	if invite.InviteID == "" || invite.Bucket == "" || invite.Endpoint == "" {
		return nil, fmt.Errorf("invalid invite code: missing fields")
	}
	return &invite, nil
}

// --- S3 mailbox operations ---

// SubmitJoin writes the joining device's public key to the S3 invite mailbox.
func SubmitJoin(ctx context.Context, backend adapter.Backend, inviteID, joinerPubKey string) error {
	payload := []byte(`{"pubkey":"` + joinerPubKey + `","submitted":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	return backend.Put(ctx, "invites/"+inviteID+"/pubkey", bytes.NewReader(payload), int64(len(payload)))
}

// CheckJoinRequest reads the joiner's public key from the S3 invite mailbox.
func CheckJoinRequest(ctx context.Context, backend adapter.Backend, inviteID string) (string, error) {
	rc, err := backend.Get(ctx, "invites/"+inviteID+"/pubkey")
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			return "", nil
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

// IsGranted checks if an S3 invite has been approved.
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

// ApproveJoin wraps namespace keys for the joiner and writes the granted marker.
func ApproveJoin(ctx context.Context, backend adapter.Backend, identity *skykey.Key, joinerAddress, inviteID string) error {
	joinerKey, err := skykey.ParseAddress(joinerAddress)
	if err != nil {
		return fmt.Errorf("parsing joiner address: %w", err)
	}

	nsKeys, err := backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return fmt.Errorf("listing namespace keys: %w", err)
	}

	joinerID := shortPubkeyID(joinerAddress)
	for _, nsKeyPath := range nsKeys {
		rc, err := backend.Get(ctx, nsKeyPath)
		if err != nil {
			continue
		}
		wrapped, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		nsKey, err := skykey.UnwrapKey(wrapped, identity.PrivateKey)
		if err != nil {
			continue
		}
		joinerWrapped, err := skykey.WrapKey(nsKey, joinerKey.PublicKey)
		if err != nil {
			continue
		}
		nsName := extractNamespaceName(nsKeyPath)
		joinerKeyPath := "keys/namespaces/" + nsName + "." + joinerID + ".ns.enc"
		backend.Put(ctx, joinerKeyPath, bytes.NewReader(joinerWrapped), int64(len(joinerWrapped)))
	}

	granted := []byte(`{"status":"granted","granted":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	if err := backend.Put(ctx, "invites/"+inviteID+"/granted", bytes.NewReader(granted), int64(len(granted))); err != nil {
		return fmt.Errorf("writing granted marker: %w", err)
	}
	return nil
}

// CleanupInvite removes all invite artifacts from S3.
func CleanupInvite(ctx context.Context, backend adapter.Backend, inviteID string) {
	keys, _ := backend.List(ctx, "invites/"+inviteID+"/")
	for _, k := range keys {
		backend.Delete(ctx, k)
	}
}

func shortPubkeyID(address string) string {
	if len(address) > 13 {
		return "D-" + address[5:13]
	}
	return address
}

func extractNamespaceName(path string) string {
	name := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		name = path[i+1:]
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}
