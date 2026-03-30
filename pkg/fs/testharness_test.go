package fs

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
)

// MinIOHarness manages a MinIO server subprocess for integration tests.
type MinIOHarness struct {
	cmd      *exec.Cmd
	dataDir  string
	endpoint string
	port     int
}

// StartMinIO starts a MinIO server on a free port with a temp data directory.
// Returns nil if minio binary is not installed (test should skip).
func StartMinIO(t *testing.T) *MinIOHarness {
	t.Helper()

	binary, err := exec.LookPath("minio")
	if err != nil {
		t.Skip("minio not installed — skipping integration test")
		return nil
	}

	port := freePort(t)
	dataDir := t.TempDir()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(binary, "server", dataDir, "--address", fmt.Sprintf(":%d", port), "--quiet")
	cmd.Env = append(os.Environ(),
		"MINIO_ROOT_USER=minioadmin",
		"MINIO_ROOT_PASSWORD=minioadmin",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting minio: %v", err)
	}

	h := &MinIOHarness{
		cmd:      cmd,
		dataDir:  dataDir,
		endpoint: endpoint,
		port:     port,
	}

	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	// Wait for MinIO to be ready
	if !h.waitReady(3 * time.Second) {
		t.Fatal("minio did not start within 3 seconds")
	}

	return h
}

// Backend creates an S3 backend connected to this MinIO instance.
// Creates the bucket if it doesn't exist.
func (h *MinIOHarness) Backend(t *testing.T, bucket string) *s3backend.Backend {
	t.Helper()
	ctx := context.Background()

	os.Setenv("S3_ACCESS_KEY_ID", "minioadmin")
	os.Setenv("S3_SECRET_ACCESS_KEY", "minioadmin")

	backend, err := s3backend.New(ctx, s3backend.Config{
		Bucket:         bucket,
		Region:         "us-east-1",
		Endpoint:       h.endpoint,
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("creating backend: %v", err)
	}

	// Create the bucket
	h.createBucket(t, bucket)

	return backend
}

// Endpoint returns the MinIO endpoint URL.
func (h *MinIOHarness) Endpoint() string {
	return h.endpoint
}

func (h *MinIOHarness) createBucket(t *testing.T, bucket string) {
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

func (h *MinIOHarness) waitReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", h.port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
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
	l.Close()
	return port
}

// NewTestBucket creates a unique bucket name for a test.
func NewTestBucket(t *testing.T) string {
	t.Helper()
	name := filepath.Base(t.Name())
	// S3 bucket names: lowercase, 3-63 chars, no dots or underscores
	safe := strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
	bucket := fmt.Sprintf("test-%s-%d", safe, time.Now().UnixNano()%100000)
	if len(bucket) > 63 {
		bucket = bucket[:63]
	}
	return bucket
}
