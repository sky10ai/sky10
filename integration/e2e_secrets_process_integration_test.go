//go:build integration

package integration

import (
	"encoding/base64"
	"os"
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

func TestIntegrationTwoProcessSecretsNamespaceBootstrapsAfterLegacyJoin(t *testing.T) {
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

	infoA := identityInfo(t, nodeA.home)
	infoB := identityInfo(t, nodeB.home)

	stopNode := func(node *processNode) {
		node.cancel()
		_ = node.cmd.Wait()
	}
	stopNode(nodeA)
	stopNode(nodeB)

	clearSecretsNamespaceState := func(home, deviceID string) {
		_ = os.Remove(filepath.Join(home, "kv", "keys", deviceID, skysecrets.DefaultNamespace+".key"))
		_ = os.Remove(filepath.Join(home, "kv", "nsids", skysecrets.DefaultNamespace))
		_ = os.RemoveAll(filepath.Join(home, "secrets", "stores", skysecrets.DefaultNamespace))
	}
	clearSecretsNamespaceState(nodeA.home, infoA.DeviceID)
	clearSecretsNamespaceState(nodeB.home, infoB.DeviceID)

	nodeA = startProcessNode(t, bin, "node-a-restart", nodeA.home)
	waitForKVReady(t, bin, nodeA.home)
	statusA = waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr = statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID
	nodeB = startProcessNode(t, bin, "node-b-restart", nodeB.home, "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeB.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	put := rpcCall[struct {
		ID                 string   `json:"id"`
		Scope              string   `json:"scope"`
		RecipientDeviceIDs []string `json:"recipient_device_ids"`
	}](t, nodeA.home, "secrets.put", map[string]any{
		"name":         "legacy-bootstrap-api-key",
		"kind":         "api-key",
		"content_type": "text/plain; charset=utf-8",
		"scope":        skysecrets.ScopeTrusted,
		"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-legacy-bootstrap-secret")),
	})
	if put.Scope != skysecrets.ScopeTrusted {
		t.Fatalf("put scope = %q, want %q", put.Scope, skysecrets.ScopeTrusted)
	}
	if len(put.RecipientDeviceIDs) != 2 {
		t.Fatalf("recipient count = %d, want 2", len(put.RecipientDeviceIDs))
	}

	waitForSecretValueRPC(t, nodeB.home, put.ID, "sk-test-legacy-bootstrap-secret", skysecrets.ScopeTrusted)
}

func TestIntegrationThreeProcessSecretScopes(t *testing.T) {
	nodeA, nodeB, nodeC, infoA, infoB, infoC := setupThreeTrustedSecretNodes(t)

	t.Run("current", func(t *testing.T) {
		put := rpcCall[struct {
			ID                 string   `json:"id"`
			Scope              string   `json:"scope"`
			RecipientDeviceIDs []string `json:"recipient_device_ids"`
		}](t, nodeA.home, "secrets.put", map[string]any{
			"name":         "scope-current-api-key",
			"kind":         "api-key",
			"content_type": "text/plain; charset=utf-8",
			"scope":        skysecrets.ScopeCurrent,
			"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-current-secret")),
		})
		if put.Scope != skysecrets.ScopeCurrent {
			t.Fatalf("put scope = %q, want %q", put.Scope, skysecrets.ScopeCurrent)
		}
		if len(put.RecipientDeviceIDs) != 1 || put.RecipientDeviceIDs[0] != infoA.DeviceID {
			t.Fatalf("current recipients = %v, want [%s]", put.RecipientDeviceIDs, infoA.DeviceID)
		}

		waitForSecretRecipientCountRPC(t, nodeA.home, put.ID, 1)
		waitForSecretValueRPC(t, nodeA.home, put.ID, "sk-test-current-secret", skysecrets.ScopeCurrent)
		waitForSecretErrorRPC(t, nodeB.home, put.ID, "secret access denied")
		waitForSecretErrorRPC(t, nodeC.home, put.ID, "secret access denied")
	})

	t.Run("trusted", func(t *testing.T) {
		put := rpcCall[struct {
			ID                 string   `json:"id"`
			Scope              string   `json:"scope"`
			RecipientDeviceIDs []string `json:"recipient_device_ids"`
		}](t, nodeA.home, "secrets.put", map[string]any{
			"name":         "scope-trusted-api-key",
			"kind":         "api-key",
			"content_type": "text/plain; charset=utf-8",
			"scope":        skysecrets.ScopeTrusted,
			"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-trusted-secret")),
		})
		if put.Scope != skysecrets.ScopeTrusted {
			t.Fatalf("put scope = %q, want %q", put.Scope, skysecrets.ScopeTrusted)
		}
		if len(put.RecipientDeviceIDs) != 3 {
			t.Fatalf("trusted recipient count = %d, want 3", len(put.RecipientDeviceIDs))
		}
		if !recipientIDsContainAll(put.RecipientDeviceIDs, infoA.DeviceID, infoB.DeviceID, infoC.DeviceID) {
			t.Fatalf("trusted recipients = %v, want devices %s, %s, %s", put.RecipientDeviceIDs, infoA.DeviceID, infoB.DeviceID, infoC.DeviceID)
		}

		waitForSecretRecipientCountRPC(t, nodeA.home, put.ID, 3)
		waitForSecretValueRPC(t, nodeA.home, put.ID, "sk-test-trusted-secret", skysecrets.ScopeTrusted)
		waitForSecretValueRPC(t, nodeB.home, put.ID, "sk-test-trusted-secret", skysecrets.ScopeTrusted)
		waitForSecretValueRPC(t, nodeC.home, put.ID, "sk-test-trusted-secret", skysecrets.ScopeTrusted)
	})

	t.Run("explicit", func(t *testing.T) {
		put := rpcCall[struct {
			ID                 string   `json:"id"`
			Scope              string   `json:"scope"`
			RecipientDeviceIDs []string `json:"recipient_device_ids"`
		}](t, nodeA.home, "secrets.put", map[string]any{
			"name":              "scope-explicit-api-key",
			"kind":              "api-key",
			"content_type":      "text/plain; charset=utf-8",
			"scope":             skysecrets.ScopeExplicit,
			"recipient_devices": []string{infoA.DeviceID, infoC.DeviceID},
			"payload":           base64.StdEncoding.EncodeToString([]byte("sk-test-explicit-secret")),
		})
		if put.Scope != skysecrets.ScopeExplicit {
			t.Fatalf("put scope = %q, want %q", put.Scope, skysecrets.ScopeExplicit)
		}
		if len(put.RecipientDeviceIDs) != 2 {
			t.Fatalf("explicit recipient count = %d, want 2", len(put.RecipientDeviceIDs))
		}
		if !recipientIDsContainAll(put.RecipientDeviceIDs, infoA.DeviceID, infoC.DeviceID) {
			t.Fatalf("explicit recipients = %v, want devices %s and %s", put.RecipientDeviceIDs, infoA.DeviceID, infoC.DeviceID)
		}
		if recipientIDsContainAll(put.RecipientDeviceIDs, infoB.DeviceID) {
			t.Fatalf("explicit recipients unexpectedly include device %s: %v", infoB.DeviceID, put.RecipientDeviceIDs)
		}

		waitForSecretRecipientCountRPC(t, nodeA.home, put.ID, 2)
		waitForSecretValueRPC(t, nodeA.home, put.ID, "sk-test-explicit-secret", skysecrets.ScopeExplicit)
		waitForSecretValueRPC(t, nodeC.home, put.ID, "sk-test-explicit-secret", skysecrets.ScopeExplicit)
		waitForSecretErrorRPC(t, nodeB.home, put.ID, "secret access denied")
	})
}

func TestIntegrationThreeProcessTrustedSecretDelete(t *testing.T) {
	nodeA, nodeB, nodeC, _, _, _ := setupThreeTrustedSecretNodes(t)

	put := rpcCall[struct {
		ID string `json:"id"`
	}](t, nodeA.home, "secrets.put", map[string]any{
		"name":         "delete-trusted-api-key",
		"kind":         "api-key",
		"content_type": "text/plain; charset=utf-8",
		"scope":        skysecrets.ScopeTrusted,
		"payload":      base64.StdEncoding.EncodeToString([]byte("sk-test-delete-secret")),
	})

	waitForSecretValueRPC(t, nodeA.home, put.ID, "sk-test-delete-secret", skysecrets.ScopeTrusted)
	waitForSecretValueRPC(t, nodeB.home, put.ID, "sk-test-delete-secret", skysecrets.ScopeTrusted)
	waitForSecretValueRPC(t, nodeC.home, put.ID, "sk-test-delete-secret", skysecrets.ScopeTrusted)

	rpcCall[map[string]string](t, nodeA.home, "secrets.delete", map[string]any{
		"id_or_name": put.ID,
	})

	waitForSecretErrorRPC(t, nodeA.home, put.ID, "secret not found")
	waitForSecretErrorRPC(t, nodeB.home, put.ID, "secret not found")
	waitForSecretErrorRPC(t, nodeC.home, put.ID, "secret not found")
	waitForSecretMissingInListRPC(t, nodeA.home, put.ID)
	waitForSecretMissingInListRPC(t, nodeB.home, put.ID)
	waitForSecretMissingInListRPC(t, nodeC.home, put.ID)
}

func setupThreeTrustedSecretNodes(t *testing.T) (*processNode, *processNode, *processNode, rpcIdentityInfo, rpcIdentityInfo, rpcIdentityInfo) {
	t.Helper()

	bin := buildSky10Binary(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"))
	waitForKVReady(t, bin, nodeA.home)
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID

	joinTrustedNode := func(name string) *processNode {
		home := filepath.Join(base, name)
		invite := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
		runCLI(t, bin, home, "join", invite)
		node := startProcessNode(t, bin, name, home, "--link-bootstrap", bootstrapAddr)
		waitForKVReady(t, bin, node.home)
		return node
	}

	nodeB := joinTrustedNode("node-b")
	nodeC := joinTrustedNode("node-c")

	waitForPeerCountAtLeast(t, bin, nodeA.home, 2)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeC.home, 1)

	return nodeA, nodeB, nodeC, identityInfo(t, nodeA.home), identityInfo(t, nodeB.home), identityInfo(t, nodeC.home)
}

func recipientIDsContainAll(have []string, want ...string) bool {
	seen := make(map[string]bool, len(have))
	for _, id := range have {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}
