//go:build integration

package main_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type processNode struct {
	name    string
	home    string
	logPath string
	cancel  context.CancelFunc
	cmd     *exec.Cmd
}

type linkStatus struct {
	PeerID     string
	Peers      int
	ListenAddr []string
}

func TestIntegrationThreeProcessKVSetDelete(t *testing.T) {
	t.Parallel()

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

func buildSky10Binary(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "sky10")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func startProcessNode(t *testing.T, bin, name, home string, extraServeArgs ...string) *processNode {
	t.Helper()

	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", home, err)
	}
	logPath := filepath.Join(home, name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log %s: %v", logPath, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	args := []string{
		"--home", home,
		"serve",
		"--http-port", "0",
		"--no-default-relays",
		"--no-default-bootstrap",
		"--link-listen", "/ip4/127.0.0.1/tcp/0",
	}
	args = append(args, extraServeArgs...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		logFile.Close()
		t.Fatalf("start %s: %v", name, err)
	}

	node := &processNode{
		name:    name,
		home:    home,
		logPath: logPath,
		cancel:  cancel,
		cmd:     cmd,
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		_ = logFile.Close()
	})

	waitForNodeReady(t, bin, home, logPath)
	return node
}

func waitForNodeReady(t *testing.T, bin, home, logPath string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = runCLIAllowError(bin, home, "link", "status")
		if lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("node %s not ready: %v\nlog:\n%s", home, lastErr, readFile(t, logPath))
}

func waitForKVReady(t *testing.T, bin, home string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, err := runCLIAllowError(bin, home, "kv", "status")
		if err == nil && strings.Contains(out, "ready:     true") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("kv not ready for %s", home)
}

func waitForPeerCountAtLeast(t *testing.T, bin, home string, want int) linkStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var status linkStatus
	for time.Now().Before(deadline) {
		status = waitForLinkStatus(t, bin, home, 0)
		if status.Peers >= want {
			return status
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("peer count for %s = %d, want at least %d", home, status.Peers, want)
	return linkStatus{}
}

func waitForLinkStatus(t *testing.T, bin, home string, minListen int) linkStatus {
	t.Helper()
	out := runCLI(t, bin, home, "link", "status")
	status := parseLinkStatus(t, out)
	if len(status.ListenAddr) < minListen {
		t.Fatalf("listen addrs = %v, want at least %d", status.ListenAddr, minListen)
	}
	return status
}

func parseLinkStatus(t *testing.T, output string) linkStatus {
	t.Helper()
	var status linkStatus
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "peer id:"):
			status.PeerID = strings.TrimSpace(strings.TrimPrefix(line, "peer id:"))
		case strings.HasPrefix(line, "peers:"):
			raw := strings.TrimSpace(strings.TrimPrefix(line, "peers:"))
			count, err := strconv.Atoi(raw)
			if err != nil {
				t.Fatalf("parse peers from %q: %v", line, err)
			}
			status.Peers = count
		case strings.HasPrefix(line, "listen:"):
			status.ListenAddr = append(status.ListenAddr, strings.TrimSpace(strings.TrimPrefix(line, "listen:")))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan link status: %v", err)
	}
	if status.PeerID == "" {
		t.Fatalf("missing peer id in link status:\n%s", output)
	}
	return status
}

func inviteCode(t *testing.T, output string) string {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "sky10p2p_") || strings.HasPrefix(line, "sky10invite_") {
			return line
		}
	}
	t.Fatalf("invite code not found in output:\n%s", output)
	return ""
}

func waitForKVValue(t *testing.T, bin, home, key, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, err := runCLIAllowError(bin, home, "kv", "get", key)
		last = out
		if err == nil && strings.TrimSpace(out) == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("kv %s on %s = %q, want %q", key, home, last, want)
}

func waitForKVMissing(t *testing.T, bin, home, key string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, err := runCLIAllowError(bin, home, "kv", "get", key)
		last = out
		if err != nil && strings.Contains(out, "key not found") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("kv %s on %s still present; last output:\n%s", key, home, last)
}

func runCLI(t *testing.T, bin, home string, args ...string) string {
	t.Helper()
	out, err := runCLIAllowError(bin, home, args...)
	if err != nil {
		t.Fatalf("run %v: %v\n%s", args, err, out)
	}
	return out
}

func runCLIAllowError(bin, home string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmdArgs := append([]string{"--home", home}, args...)
	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("read %s: %v", path, err)
	}
	return string(data)
}
