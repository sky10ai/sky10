//go:build integration

package fs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// Regression: ops at the same Unix second as the poller cursor must not
// be permanently skipped. Previously ReadSince used ts <= since.
func TestIntegrationPollerSameTimestamp(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "poller-ts")

	// A creates namespace, uploads seed file
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "seed.txt", strings.NewReader("seed"))

	simulateApprove(t, ctx, backend, idA, idB)

	// A uploads more files — all in the same second
	storeA.Put(ctx, "a1.txt", strings.NewReader("aaa1"))
	storeA.Put(ctx, "a2.txt", strings.NewReader("aaa2"))

	// B polls — picks up everything
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)
	poller := NewPollerV2(storeB, localLog, time.Hour, "shared", nil)
	poller.pollOnce(ctx)

	cursor := localLog.LastRemoteOp()

	if _, ok := localLog.Lookup("a1.txt"); !ok {
		t.Fatal("a1.txt not in local log after first poll")
	}
	if _, ok := localLog.Lookup("a2.txt"); !ok {
		t.Fatal("a2.txt not in local log after first poll")
	}

	// A uploads another file — same second as cursor (no sleep)
	storeA.Put(ctx, "a3.txt", strings.NewReader("aaa3"))

	// Verify timestamp matches cursor
	opsLog, _ := storeB.getOpsLog(ctx)
	allEntries, _ := opsLog.ReadSince(ctx, 0)
	var a3Ts int64
	for _, e := range allEntries {
		if e.Path == "a3.txt" {
			a3Ts = e.Timestamp
		}
	}
	if a3Ts != cursor {
		t.Skipf("timestamps differ (cursor=%d, a3=%d) — race window missed", cursor, a3Ts)
	}

	// Second poll — a3.txt must be found
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("a3.txt"); !ok {
		t.Error("a3.txt not in local log — poller skipped op at same timestamp as cursor")
	}
}
