package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func (s *FSHandler) rpcDeviceList(ctx context.Context) (interface{}, error) {
	devices, err := ListDevices(ctx, s.store.backend)
	if err != nil {
		return nil, err
	}
	thisDevice := s.store.devicePubKey
	if thisDevice == "" {
		thisDevice = s.store.identity.Address() // fallback for non-multi-device
	}
	return map[string]interface{}{
		"devices":     devices,
		"this_device": thisDevice,
	}, nil
}

func (s *FSHandler) rpcDeviceRemove(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Pubkey == s.store.identity.Address() {
		return nil, fmt.Errorf("cannot remove this device")
	}
	id := shortPubkeyID(p.Pubkey)
	// Delete device registration
	devKey := "devices/" + id + ".json"
	if err := s.store.backend.Delete(ctx, devKey); err != nil {
		return nil, err
	}
	// Delete device snapshots across all namespace prefixes
	snapPrefix := "fs/"
	allKeys, _ := s.store.backend.List(ctx, snapPrefix)
	deleted := 0
	for _, k := range allKeys {
		if strings.Contains(k, "/snapshots/"+id+"/") {
			s.store.backend.Delete(ctx, k)
			deleted++
		}
	}
	return map[string]any{"status": "ok", "snapshots_deleted": deleted}, nil
}

func (s *FSHandler) rpcInvite(ctx context.Context) (interface{}, error) {
	accessKey := os.Getenv("S3_ACCESS_KEY_ID")
	secretKey := os.Getenv("S3_SECRET_ACCESS_KEY")

	// Read endpoint/bucket from config file
	home, _ := os.UserHomeDir()
	cfgData, err := os.ReadFile(home + "/.sky10/fs/config.json")
	var endpoint, bucket, region string
	var pathStyle bool
	if err == nil {
		var cfg struct {
			Endpoint       string `json:"endpoint"`
			Bucket         string `json:"bucket"`
			Region         string `json:"region"`
			ForcePathStyle bool   `json:"force_path_style"`
		}
		json.Unmarshal(cfgData, &cfg)
		endpoint = cfg.Endpoint
		bucket = cfg.Bucket
		region = cfg.Region
		pathStyle = cfg.ForcePathStyle
	}

	code, err := CreateInvite(ctx, s.store.backend, InviteConfig{
		Endpoint:       endpoint,
		Bucket:         bucket,
		Region:         region,
		AccessKey:      accessKey,
		SecretKey:      secretKey,
		ForcePathStyle: pathStyle,
		DevicePubKey:   s.store.identity.Address(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"code": code}, nil
}

func (s *FSHandler) rpcJoin(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		InviteID string `json:"invite_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.InviteID == "" {
		return nil, fmt.Errorf("invite_id required")
	}

	// Submit this device's pubkey to the invite mailbox
	if err := SubmitJoin(ctx, s.store.backend, p.InviteID, s.store.identity.Address()); err != nil {
		return nil, fmt.Errorf("submitting join: %w", err)
	}

	// Poll for approval (up to 60 seconds)
	for i := 0; i < 12; i++ {
		granted, err := IsGranted(ctx, s.store.backend, p.InviteID)
		if err != nil {
			return nil, err
		}
		if granted {
			// Register this device
			dpk := s.store.devicePubKey
			if dpk == "" {
				dpk = s.store.identity.Address()
			}
			RegisterDevice(ctx, s.store.backend, s.store.identity.Address(), dpk, GetDeviceName(), s.version)
			return map[string]string{"status": "approved"}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	return map[string]string{"status": "pending"}, nil
}

func (s *FSHandler) rpcApprove(ctx context.Context) (interface{}, error) {
	// Find pending invites
	inviteKeys, err := s.store.backend.List(ctx, "invites/")
	if err != nil {
		return nil, err
	}

	inviteIDs := make(map[string]bool)
	for _, k := range inviteKeys {
		if id := splitInvitePath2(k); id != "" {
			inviteIDs[id] = true
		}
	}

	approved := 0
	for inviteID := range inviteIDs {
		joinerAddr, err := CheckJoinRequest(ctx, s.store.backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := IsGranted(ctx, s.store.backend, inviteID)
		if granted {
			if s.joinerHasAllKeys(ctx, joinerAddr) {
				continue
			}
		}
		if err := ApproveJoin(ctx, s.store.backend, s.store.identity, joinerAddr, inviteID); err != nil {
			continue
		}
		// Register the joiner as a device
		approved++
		// Don't cleanup — joiner needs to poll and see the granted marker
	}

	return map[string]int{"approved": approved}, nil
}

// autoApproveLoop polls for pending join requests and approves them automatically.
// The invite code itself is the authorization — no manual step needed.
func (s *FSHandler) autoApproveLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	// Run once immediately on startup
	s.tryAutoApprove(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tryAutoApprove(ctx)
		}
	}
}

func (s *FSHandler) tryAutoApprove(ctx context.Context) {
	// Hard timeout: entire cycle must finish in 10 seconds.
	// If any S3 call hangs, we bail out instead of blocking forever.
	cycleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	s.logger.Debug("auto-approve: checking")
	inviteKeys, err := s.store.backend.List(cycleCtx, "invites/")
	if err != nil {
		s.logger.Warn("auto-approve: list failed", "error", err)
		return
	}

	inviteIDs := make(map[string]bool)
	for _, k := range inviteKeys {
		if id := splitInvitePath2(k); id != "" {
			inviteIDs[id] = true
		}
	}
	s.logger.Debug("auto-approve: invites", "count", len(inviteIDs))

	for inviteID := range inviteIDs {
		// Skip invites we've already confirmed are fully complete
		s.mu.Lock()
		if s.completedInvites[inviteID] {
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		joinerAddr, err := CheckJoinRequest(cycleCtx, s.store.backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := IsGranted(cycleCtx, s.store.backend, inviteID)
		if granted {
			if s.joinerHasAllKeys(cycleCtx, joinerAddr) {
				s.logger.Debug("auto-approve: already complete", "invite", inviteID[:8])
				s.mu.Lock()
				s.completedInvites[inviteID] = true
				s.mu.Unlock()
				continue
			}
		}
		if err := ApproveJoin(cycleCtx, s.store.backend, s.store.identity, joinerAddr, inviteID); err != nil {
			s.logger.Warn("auto-approve failed", "invite", inviteID, "error", err)
			continue
		}
		s.logger.Info("auto-approved device", "address", joinerAddr)
	}
}

// joinerHasAllKeys checks if the joiner has a wrapped key for every
// namespace that this device (the approver) has access to.
func (s *FSHandler) joinerHasAllKeys(ctx context.Context, joinerAddr string) bool {
	joinerID := shortPubkeyID(joinerAddr)
	myID := shortPubkeyID(s.store.identity.Address())

	allKeys, err := s.store.backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return false
	}

	// Find namespaces we have access to (our device-specific key or the base key)
	myNamespaces := make(map[string]bool)
	for _, k := range allKeys {
		ns := extractNamespaceName(k)
		// Check if we can unwrap this key (it's ours)
		if strings.Contains(k, "."+myID+".") || strings.HasSuffix(k, ns+".ns.enc") {
			myNamespaces[ns] = true
		}
	}

	// Check joiner has a key for each namespace
	for ns := range myNamespaces {
		joinerKeyPath := "keys/namespaces/" + ns + "." + joinerID + ".ns.enc"
		if _, err := s.store.backend.Head(ctx, joinerKeyPath); err != nil {
			return false
		}
	}

	return true
}

func splitInvitePath2(key string) string {
	if len(key) < 9 || key[:8] != "invites/" {
		return ""
	}
	rest := key[8:]
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return ""
}
