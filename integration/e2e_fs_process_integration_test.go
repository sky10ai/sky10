//go:build integration && skyfs_daemon

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationThreeProcessFSMinIOSync(t *testing.T) {
	bin := buildSky10Binary(t)
	minio := startMinIO(t)
	bucket := newTestBucket(t)
	minio.createBucket(t, bucket)

	base := t.TempDir()
	env := []string{
		"S3_ACCESS_KEY_ID=minioadmin",
		"S3_SECRET_ACCESS_KEY=minioadmin",
	}

	nodeAHome := filepath.Join(base, "node-a")
	nodeBHome := filepath.Join(base, "node-b")
	nodeCHome := filepath.Join(base, "node-c")

	runCLIEnv(t, env, bin, nodeAHome, "fs", "init",
		"--bucket", bucket,
		"--region", "us-east-1",
		"--endpoint", minio.endpoint,
		"--path-style",
	)

	nodeA := startProcessNodeEnv(t, env, bin, "node-a", nodeAHome, "--fs-poll-seconds", "1")

	driveA := filepath.Join(base, "drive-a")
	driveB := filepath.Join(base, "drive-b")
	driveC := filepath.Join(base, "drive-c")
	for _, dir := range []string{driveA, driveB, driveC} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	initialFile := filepath.Join(driveA, "from-a.txt")
	if err := os.WriteFile(initialFile, []byte("hello from A"), 0644); err != nil {
		t.Fatalf("write %s: %v", initialFile, err)
	}
	runCLI(t, bin, nodeA.home, "fs", "drive", "create", "shared", driveA, "--namespace", "shared")

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	joinB := startCLICommand(t, nil, bin, nodeBHome, "join", inviteB)
	completeJoin(t, joinB, bin, nodeA.home)

	nodeB := startProcessNodeEnv(t, env, bin, "node-b", nodeBHome, "--fs-poll-seconds", "1")
	runCLI(t, bin, nodeB.home, "fs", "drive", "create", "shared", driveB, "--namespace", "shared")

	inviteC := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	joinC := startCLICommand(t, nil, bin, nodeCHome, "join", inviteC)
	waitForJoinApprovalPrompt(t, joinC)
	approvePendingJoin(t, bin, nodeA.home)
	joinC.wait(t)

	nodeC := startProcessNodeEnv(t, env, bin, "node-c", nodeCHome, "--fs-poll-seconds", "1")
	runCLI(t, bin, nodeC.home, "fs", "drive", "create", "shared", driveC, "--namespace", "shared")

	waitForFileContent(t, filepath.Join(driveB, "from-a.txt"), "hello from A")
	waitForFileContent(t, filepath.Join(driveC, "from-a.txt"), "hello from A")

	fileB := filepath.Join(driveB, "from-b.txt")
	if err := os.WriteFile(fileB, []byte("hello from B"), 0644); err != nil {
		t.Fatalf("write %s: %v", fileB, err)
	}

	waitForFileContent(t, filepath.Join(driveA, "from-b.txt"), "hello from B")
	waitForFileContent(t, filepath.Join(driveC, "from-b.txt"), "hello from B")

	if err := os.Remove(initialFile); err != nil {
		t.Fatalf("remove %s: %v", initialFile, err)
	}
	nodeA.cancel()
	_ = nodeA.cmd.Wait()
	nodeA = startProcessNodeEnv(t, env, bin, "node-a", nodeAHome, "--fs-poll-seconds", "1")

	waitForFileMissing(t, filepath.Join(driveB, "from-a.txt"))
	waitForFileMissing(t, filepath.Join(driveC, "from-a.txt"))
	waitForFileContent(t, filepath.Join(driveA, "from-b.txt"), "hello from B")
	waitForFileContent(t, filepath.Join(driveB, "from-b.txt"), "hello from B")
	waitForFileContent(t, filepath.Join(driveC, "from-b.txt"), "hello from B")
}

func TestIntegrationTwoProcessFSP2POnlyUsesPeerChunks(t *testing.T) {
	bin := buildSky10Binary(t)

	base := t.TempDir()
	nodeAHome := filepath.Join(base, "node-a")
	nodeBHome := filepath.Join(base, "node-b")
	driveA := filepath.Join(base, "drive-a")
	driveB := filepath.Join(base, "drive-b")
	for _, dir := range []string{driveA, driveB} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	nodeA := startProcessNode(t, bin, "node-a", nodeAHome, "--fs-poll-seconds", "1")
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID
	runCLI(t, bin, nodeA.home, "fs", "drive", "create", "shared", driveA, "--namespace", "shared")

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	joinB := startCLICommand(t, nil, bin, nodeBHome, "join", inviteB)
	completeJoin(t, joinB, bin, nodeA.home)

	nodeB := startProcessNode(t, bin, "node-b", nodeBHome, "--fs-poll-seconds", "1", "--link-bootstrap", bootstrapAddr)
	runCLI(t, bin, nodeB.home, "fs", "drive", "create", "shared", driveB, "--namespace", "shared")

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	publishStableFile(t, filepath.Join(base, "peer-only.tmp"), filepath.Join(driveA, "peer-only.txt"), "hello from peer-only mode")

	if last, ok := waitForFileContentWithin(filepath.Join(driveB, "peer-only.txt"), "hello from peer-only mode", 20*time.Second); !ok {
		stateB := rpcCall[map[string]any](t, nodeB.home, "skyfs.driveState", map[string]any{"id": "drive_shared"})
		t.Fatalf("file %s = %q, want %q\nnode-a log:\n%s\nnode-b log:\n%s\ndrive-b state: %+v",
			filepath.Join(driveB, "peer-only.txt"),
			last,
			"hello from peer-only mode",
			readFile(t, nodeA.logPath),
			readFile(t, nodeB.logPath),
			stateB,
		)
	}
	waitForDriveReadSource(t, nodeB.home, "shared", "peer", 1, 0)
}

func TestIntegrationFSFallsBackToS3WhenPeersAbsent(t *testing.T) {
	bin := buildSky10Binary(t)
	minio := startMinIO(t)
	bucket := newTestBucket(t)
	minio.createBucket(t, bucket)

	base := t.TempDir()
	env := []string{
		"S3_ACCESS_KEY_ID=minioadmin",
		"S3_SECRET_ACCESS_KEY=minioadmin",
	}

	nodeAHome := filepath.Join(base, "node-a")
	nodeBHome := filepath.Join(base, "node-b")
	driveA := filepath.Join(base, "drive-a")
	driveB := filepath.Join(base, "drive-b")
	for _, dir := range []string{driveA, driveB} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	runCLIEnv(t, env, bin, nodeAHome, "fs", "init",
		"--bucket", bucket,
		"--region", "us-east-1",
		"--endpoint", minio.endpoint,
		"--path-style",
	)

	nodeA := startProcessNodeEnv(t, env, bin, "node-a", nodeAHome, "--fs-poll-seconds", "1")
	runCLI(t, bin, nodeA.home, "fs", "drive", "create", "shared", driveA, "--namespace", "shared")

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	joinB := startCLICommand(t, nil, bin, nodeBHome, "join", inviteB)
	waitForJoinApprovalPrompt(t, joinB)
	approvePendingJoin(t, bin, nodeA.home)
	joinB.wait(t)

	publishStableFile(t, filepath.Join(base, "s3-fallback.tmp"), filepath.Join(driveA, "s3-fallback.txt"), "hello from s3 fallback")

	waitForDriveIdle(t, nodeA.home, "shared")
	time.Sleep(2 * time.Second)

	nodeA.cancel()
	_ = nodeA.cmd.Wait()

	nodeB := startProcessNodeEnv(t, env, bin, "node-b", nodeBHome, "--fs-poll-seconds", "1")
	runCLI(t, bin, nodeB.home, "fs", "drive", "create", "shared", driveB, "--namespace", "shared")

	waitForFileContent(t, filepath.Join(driveB, "s3-fallback.txt"), "hello from s3 fallback")
	waitForDriveReadSource(t, nodeB.home, "shared", "s3", 0, 1)
}

func TestIntegrationFSUsesPeerThenS3FallbackAcrossAvailabilityChange(t *testing.T) {
	bin := buildSky10Binary(t)
	minio := startMinIO(t)
	bucket := newTestBucket(t)
	minio.createBucket(t, bucket)

	base := t.TempDir()
	env := []string{
		"S3_ACCESS_KEY_ID=minioadmin",
		"S3_SECRET_ACCESS_KEY=minioadmin",
	}

	nodeAHome := filepath.Join(base, "node-a")
	nodeBHome := filepath.Join(base, "node-b")
	driveA := filepath.Join(base, "drive-a")
	driveB := filepath.Join(base, "drive-b")
	for _, dir := range []string{driveA, driveB} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	runCLIEnv(t, env, bin, nodeAHome, "fs", "init",
		"--bucket", bucket,
		"--region", "us-east-1",
		"--endpoint", minio.endpoint,
		"--path-style",
	)

	nodeA := startProcessNodeEnv(t, env, bin, "node-a", nodeAHome, "--fs-poll-seconds", "1")
	statusA := waitForLinkStatus(t, bin, nodeA.home, 1)
	bootstrapAddr := statusA.ListenAddr[0] + "/p2p/" + statusA.PeerID
	runCLI(t, bin, nodeA.home, "fs", "drive", "create", "shared", driveA, "--namespace", "shared")

	inviteB := inviteCode(t, runCLI(t, bin, nodeA.home, "invite"))
	joinB := startCLICommand(t, nil, bin, nodeBHome, "join", inviteB)
	completeJoin(t, joinB, bin, nodeA.home)

	nodeB := startProcessNodeEnv(t, env, bin, "node-b", nodeBHome, "--fs-poll-seconds", "1", "--link-bootstrap", bootstrapAddr)
	runCLI(t, bin, nodeB.home, "fs", "drive", "create", "shared", driveB, "--namespace", "shared")

	waitForPeerCountAtLeast(t, bin, nodeA.home, 1)
	waitForPeerCountAtLeast(t, bin, nodeB.home, 1)

	publishStableFile(t, filepath.Join(base, "hybrid-peer.tmp"), filepath.Join(driveA, "peer-first.txt"), "hello from peer first")
	waitForFileContent(t, filepath.Join(driveB, "peer-first.txt"), "hello from peer first")
	waitForDriveReadSource(t, nodeB.home, "shared", "peer", 1, 0)

	nodeB.cancel()
	_ = nodeB.cmd.Wait()

	publishStableFile(t, filepath.Join(base, "hybrid-s3.tmp"), filepath.Join(driveA, "s3-later.txt"), "hello from s3 fallback")
	waitForDriveIdle(t, nodeA.home, "shared")
	time.Sleep(2 * time.Second)

	nodeA.cancel()
	_ = nodeA.cmd.Wait()

	nodeB = startProcessNodeEnv(t, env, bin, "node-b", nodeBHome, "--fs-poll-seconds", "1")

	waitForFileContent(t, filepath.Join(driveB, "peer-first.txt"), "hello from peer first")
	waitForFileContent(t, filepath.Join(driveB, "s3-later.txt"), "hello from s3 fallback")
	waitForDriveReadSource(t, nodeB.home, "shared", "s3", 0, 1)
}

type rpcFSDriveListResult struct {
	Drives []struct {
		Name            string `json:"name"`
		OutboxPending   int    `json:"outbox_pending"`
		TransferPending int    `json:"transfer_pending"`
		TransferStaged  int    `json:"transfer_staged"`
		ReadPeerHits    int    `json:"read_peer_hits"`
		ReadS3Hits      int    `json:"read_s3_hits"`
		LastReadSource  string `json:"last_read_source"`
	} `json:"drives"`
}

func waitForDriveIdle(t *testing.T, home, driveName string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		result := rpcCall[rpcFSDriveListResult](t, home, "skyfs.driveList", nil)
		for _, drive := range result.Drives {
			if drive.Name != driveName {
				continue
			}
			if drive.OutboxPending == 0 && drive.TransferPending == 0 && drive.TransferStaged == 0 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("drive %q on %s did not go idle", driveName, home)
}

func waitForDriveReadSource(t *testing.T, home, driveName, wantSource string, minPeerHits, minS3Hits int) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcFSDriveListResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcFSDriveListResult](t, home, "skyfs.driveList", nil)
		for _, drive := range last.Drives {
			if drive.Name != driveName {
				continue
			}
			if drive.LastReadSource != wantSource {
				break
			}
			if drive.ReadPeerHits < minPeerHits || drive.ReadS3Hits < minS3Hits {
				break
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("drive %q on %s did not reach read source %q (peer>=%d s3>=%d); last=%+v", driveName, home, wantSource, minPeerHits, minS3Hits, last.Drives)
}

func completeJoin(t *testing.T, joinCmd *runningCLI, bin, approverHome string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out := readFile(t, joinCmd.logPath)
		switch {
		case strings.Contains(out, "Joined!"):
			joinCmd.wait(t)
			return
		case strings.Contains(out, "Waiting for approval"):
			approvePendingJoin(t, bin, approverHome)
			joinCmd.wait(t)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("join did not complete:\n%s", readFile(t, joinCmd.logPath))
}

func waitForFileContentWithin(path, want string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			last = string(data)
			if last == want {
				return last, true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last, false
}

func publishStableFile(t *testing.T, tempPath, targetPath, content string) string {
	t.Helper()

	if err := os.WriteFile(tempPath, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", tempPath, err)
	}
	time.Sleep(3 * time.Second)
	if err := os.Rename(tempPath, targetPath); err != nil {
		t.Fatalf("rename %s -> %s: %v", tempPath, targetPath, err)
	}
	return targetPath
}
