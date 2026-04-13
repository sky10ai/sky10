//go:build integration

package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sky10/sky10/pkg/kv"
)

func TestIntegrationThreeProcessKVSetDelete(t *testing.T) {
	bin := buildSky10Binary(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"))
	waitForKVReady(t, bin, nodeA.home)
	statusA := waitForLinkStatus(t, bin, nodeA.home, 0)
	if len(statusA.ListenAddr) == 0 {
		t.Fatalf("node A has no listen addresses; log:\n%s", readFile(t, nodeA.logPath))
	}
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	runCLI(t, bin, filepath.Join(base, "node-b"), "join", inviteB)
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeB.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	inviteC := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	runCLI(t, bin, filepath.Join(base, "node-c"), "join", inviteC)
	nodeC := startProcessNode(t, bin, "node-c", filepath.Join(base, "node-c"), "--link-bootstrap", bootstrapAddr)
	waitForKVReady(t, bin, nodeC.home)

	waitForPeerCountAtLeast(t, bin, nodeA.home, 2)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeC.home, 1)

	runCLI(t, bin, nodeA.home, "kv", "set", "alpha", "one")
	runCLI(t, bin, nodeA.home, "kv", "set", "beta", "two")

	waitForKVValue(t, bin, nodeB.home, "alpha", "one")
	waitForKVValue(t, bin, nodeC.home, "alpha", "one")
	waitForKVValue(t, bin, nodeB.home, "beta", "two")
	waitForKVValue(t, bin, nodeC.home, "beta", "two")

	runCLI(t, bin, nodeA.home, "kv", "delete", "alpha")

	waitForKVMissing(t, bin, nodeB.home, "alpha")
	waitForKVMissing(t, bin, nodeC.home, "alpha")
	waitForKVValue(t, bin, nodeB.home, "beta", "two")
	waitForKVValue(t, bin, nodeC.home, "beta", "two")
}

func TestIntegrationTwoProcessKVSyncMaxInlinePayload(t *testing.T) {
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

	payload := strings.Repeat("x", kv.MaxValueSize)
	rpcCall[map[string]string](t, nodeA.home, "skykv.set", map[string]any{
		"key":   "max-inline",
		"value": payload,
	})

	gotB := waitForKVValueRPC(t, nodeB.home, "max-inline", payload)
	if len(gotB.Value) != kv.MaxValueSize {
		t.Fatalf("node B value length = %d, want %d", len(gotB.Value), kv.MaxValueSize)
	}
}
