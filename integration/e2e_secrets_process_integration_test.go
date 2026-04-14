//go:build integration

package integration

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	skysecrets "github.com/sky10/sky10/pkg/secrets"
)

func TestIntegrationTwoProcessTrustedSecretSyncAfterJoin(t *testing.T) {
	bin := buildSky10Binary(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"))
	waitForKVReady(t, bin, nodeA.home)
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	runCLI(t, bin, filepath.Join(base, "node-b"), "join", inviteB)
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeB.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	put := rpcCall[struct {
		ID                 string   `json:"id"`
		Name               string   `json:"name"`
		Scope              string   `json:"scope"`
		RecipientDeviceIDs []string `json:"recipient_device_ids"`
	}](t, nodeA.home, "secrets.put", map[string]any{
		"name":         "shared-api-key",
		"kind":         "api-key",
		"content_type": "text/plain; charset=utf-8",
		"scope":        skysecrets.ScopeTrusted,
		"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-joined-secret")),
	})
	if put.Scope != skysecrets.ScopeTrusted {
		t.Fatalf("put scope = %q, want %q", put.Scope, skysecrets.ScopeTrusted)
	}
	if len(put.RecipientDeviceIDs) != 2 {
		t.Fatalf("recipient count = %d, want 2", len(put.RecipientDeviceIDs))
	}

	got := waitForSecretValueRPC(t, nodeB.home, "shared-api-key", "sk-test-joined-secret", skysecrets.ScopeTrusted)
	if got.Name != "shared-api-key" {
		t.Fatalf("secret name = %q, want shared-api-key", got.Name)
	}
}

func TestIntegrationTwoProcessTrustedSecretReconcilesAfterJoin(t *testing.T) {
	bin := buildSky10Binary(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"))
	waitForKVReady(t, bin, nodeA.home)
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID

	put := rpcCall[struct {
		ID                 string   `json:"id"`
		Name               string   `json:"name"`
		Scope              string   `json:"scope"`
		RecipientDeviceIDs []string `json:"recipient_device_ids"`
	}](t, nodeA.home, "secrets.put", map[string]any{
		"name":         "prejoin-api-key",
		"kind":         "api-key",
		"content_type": "text/plain; charset=utf-8",
		"scope":        skysecrets.ScopeTrusted,
		"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-prejoin-secret")),
	})
	if len(put.RecipientDeviceIDs) != 1 {
		t.Fatalf("recipient count before join = %d, want 1", len(put.RecipientDeviceIDs))
	}

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	runCLI(t, bin, filepath.Join(base, "node-b"), "join", inviteB)
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeB.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	got := waitForSecretValueRPC(t, nodeB.home, "prejoin-api-key", "sk-test-prejoin-secret", skysecrets.ScopeTrusted)
	if got.Name != "prejoin-api-key" {
		t.Fatalf("secret name = %q, want prejoin-api-key", got.Name)
	}
}

func TestIntegrationTwoProcessTrustedSecretRevokedAfterDeviceRemoval(t *testing.T) {
	bin := buildSky10Binary(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"))
	waitForKVReady(t, bin, nodeA.home)
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	runCLI(t, bin, filepath.Join(base, "node-b"), "join", inviteB)
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeB.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	put := rpcCall[struct {
		ID string `json:"id"`
	}](t, nodeA.home, "secrets.put", map[string]any{
		"name":         "removal-api-key",
		"kind":         "api-key",
		"content_type": "text/plain; charset=utf-8",
		"scope":        skysecrets.ScopeTrusted,
		"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-removal-secret")),
	})

	waitForSecretValueRPC(t, nodeB.home, "removal-api-key", "sk-test-removal-secret", skysecrets.ScopeTrusted)

	deviceB := identityInfo(t, nodeB.home)
	rpcCall[map[string]any](t, nodeA.home, "identity.deviceRemove", map[string]any{
		"pubkey": deviceB.DevicePubKey,
	})

	waitForSecretRecipientCountRPC(t, nodeA.home, put.ID, 1)
	waitForSecretErrorRPC(t, nodeB.home, put.ID, "secret access denied")
}

func TestIntegrationTwoProcessSandboxJoinDoesNotInheritTrustedSecret(t *testing.T) {
	bin := buildSky10Binary(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"))
	waitForKVReady(t, bin, nodeA.home)
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID

	put := rpcCall[struct {
		ID string `json:"id"`
	}](t, nodeA.home, "secrets.put", map[string]any{
		"name":         "sandbox-blocked-api-key",
		"kind":         "api-key",
		"content_type": "text/plain; charset=utf-8",
		"scope":        skysecrets.ScopeTrusted,
		"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-sandbox-secret")),
	})

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	runCLI(t, bin, filepath.Join(base, "node-b"), "join", "--role", "sandbox", inviteB)
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeB.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	waitForSecretErrorRPC(t, nodeB.home, put.ID, "secret access denied")
}
