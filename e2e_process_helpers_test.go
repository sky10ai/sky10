//go:build integration

package main_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

type runningCLI struct {
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	logPath string
}

type minIOHarness struct {
	cmd      *exec.Cmd
	logPath  string
	endpoint string
	port     int
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
	return startProcessNodeEnv(t, nil, bin, name, home, extraServeArgs...)
}

func startProcessNodeEnv(t *testing.T, env []string, bin, name, home string, extraServeArgs ...string) *processNode {
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
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
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

func startCLICommand(t *testing.T, env []string, bin, home string, args ...string) *runningCLI {
	t.Helper()

	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", home, err)
	}
	logFile, err := os.CreateTemp(home, "cli-*.log")
	if err != nil {
		t.Fatalf("create command log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmdArgs := append([]string{"--home", home}, args...)
	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatalf("start command %v: %v", args, err)
	}

	logPath := logFile.Name()
	_ = logFile.Close()
	rc := &runningCLI{
		cmd:     cmd,
		cancel:  cancel,
		logPath: logPath,
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	return rc
}

func (c *runningCLI) wait(t *testing.T) string {
	t.Helper()

	err := c.cmd.Wait()
	out := readFile(t, c.logPath)
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, out)
	}
	return out
}

func (c *runningCLI) waitForOutput(t *testing.T, substring string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out := readFile(t, c.logPath)
		if strings.Contains(out, substring) {
			return
		}
		if c.cmd.ProcessState != nil && c.cmd.ProcessState.Exited() {
			t.Fatalf("command exited before output %q appeared:\n%s", substring, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("output %q not observed:\n%s", substring, readFile(t, c.logPath))
}

func waitForNodeReady(t *testing.T, bin, home, logPath string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = runCLIAllowErrorEnv(nil, bin, home, "link", "status")
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
		out, err := runCLIAllowErrorEnv(nil, bin, home, "kv", "status")
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
		out, err := runCLIAllowErrorEnv(nil, bin, home, "kv", "get", key)
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
		out, err := runCLIAllowErrorEnv(nil, bin, home, "kv", "get", key)
		last = out
		if err != nil && strings.Contains(out, "key not found") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("kv %s on %s still present; last output:\n%s", key, home, last)
}

func waitForFileContent(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			last = string(data)
			if last == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("file %s = %q, want %q", path, last, want)
}

func waitForFileMissing(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("file %s still exists", path)
}

func waitForJoinApprovalPrompt(t *testing.T, joinCmd *runningCLI) {
	t.Helper()
	joinCmd.waitForOutput(t, "Waiting for approval")
}

func approvePendingJoin(t *testing.T, bin, home string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = runCLI(t, bin, home, "fs", "approve")
		if strings.Contains(last, "Approved ") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("join approval did not succeed:\n%s", last)
}

func runCLI(t *testing.T, bin, home string, args ...string) string {
	t.Helper()
	out, err := runCLIAllowErrorEnv(nil, bin, home, args...)
	if err != nil {
		t.Fatalf("run %v: %v\n%s", args, err, out)
	}
	return out
}

func runCLIEnv(t *testing.T, env []string, bin, home string, args ...string) string {
	t.Helper()
	out, err := runCLIAllowErrorEnv(env, bin, home, args...)
	if err != nil {
		t.Fatalf("run %v: %v\n%s", args, err, out)
	}
	return out
}

func runCLIAllowError(bin, home string, args ...string) (string, error) {
	return runCLIAllowErrorEnv(nil, bin, home, args...)
}

func runCLIAllowErrorEnv(env []string, bin, home string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmdArgs := append([]string{"--home", home}, args...)
	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
	cmd.Env = append(os.Environ(), env...)
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

func startMinIO(t *testing.T) *minIOHarness {
	t.Helper()

	binary, err := exec.LookPath("minio")
	if err != nil {
		t.Skip("minio not installed — skipping integration test")
		return nil
	}

	port := freePort(t)
	dataDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "minio.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create minio log: %v", err)
	}

	cmd := exec.Command(binary, "server", dataDir, "--address", fmt.Sprintf(":%d", port), "--quiet")
	cmd.Env = append(os.Environ(),
		"MINIO_ROOT_USER=minioadmin",
		"MINIO_ROOT_PASSWORD=minioadmin",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start minio: %v", err)
	}

	h := &minIOHarness{
		cmd:      cmd,
		logPath:  logPath,
		endpoint: fmt.Sprintf("http://127.0.0.1:%d", port),
		port:     port,
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		_ = logFile.Close()
	})

	if !h.waitReady(5 * time.Second) {
		t.Fatalf("minio did not start:\n%s", readFile(t, logPath))
	}

	return h
}

func (h *minIOHarness) createBucket(t *testing.T, bucket string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(h.endpoint)
		o.UsePathStyle = true
	})
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("create bucket %s: %v", bucket, err)
	}
}

func (h *minIOHarness) waitReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", h.port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getting free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func newTestBucket(t *testing.T) string {
	t.Helper()
	name := filepath.Base(t.Name())
	safe := strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
	bucket := fmt.Sprintf("test-%s-%d", safe, time.Now().UnixNano()%100000)
	if len(bucket) > 63 {
		bucket = bucket[:63]
	}
	return bucket
}
