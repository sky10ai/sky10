//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
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
	waitForJoinApprovalPrompt(t, joinB)
	approvePendingJoin(t, bin, nodeA.home)
	joinB.wait(t)

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
