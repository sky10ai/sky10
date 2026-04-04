package kv

import (
	"bytes"
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
	skykey "github.com/sky10/sky10/pkg/key"
)

type minioHarness struct {
	cmd      *exec.Cmd
	endpoint string
	port     int
}

func startMinIO(t *testing.T) *minioHarness {
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

	h := &minioHarness{cmd: cmd, endpoint: endpoint, port: port}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	if !h.waitReady(3 * time.Second) {
		t.Fatal("minio did not start within 3 seconds")
	}
	return h
}

func (h *minioHarness) backend(t *testing.T, bucket string) *s3backend.Backend {
	t.Helper()
	ctx := context.Background()
	os.Setenv("S3_ACCESS_KEY_ID", "minioadmin")
	os.Setenv("S3_SECRET_ACCESS_KEY", "minioadmin")

	backend, err := s3backend.New(ctx, s3backend.Config{
		Bucket: bucket, Region: "us-east-1",
		Endpoint: h.endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("creating backend: %v", err)
	}

	h.createBucket(t, bucket)
	return backend
}

func (h *minioHarness) createBucket(t *testing.T, bucket string) {
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

func (h *minioHarness) waitReady(timeout time.Duration) bool {
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

// twoDeviceEnv sets up two simulated KV devices sharing one S3 backend.
type twoDeviceEnv struct {
	backend    *s3backend.Backend
	nsKey      []byte
	nsID       string
	devIDA     string
	devIDB     string
	localLogA  *LocalLog
	localLogB  *LocalLog
	uploaderA  *Uploader
	uploaderB  *Uploader
	pollerA    *Poller
	pollerB    *Poller
	baselinesA *BaselineStore
	baselinesB *BaselineStore
}

func setupTwoDevices(t *testing.T) *twoDeviceEnv {
	t.Helper()
	h := startMinIO(t)
	if h == nil {
		return nil
	}
	bucket := newTestBucket(t)
	backend := h.backend(t, bucket)
	ctx := context.Background()

	idA, _ := skykey.Generate()
	idB, _ := skykey.Generate()
	devIDA := ShortDeviceID(idA)
	devIDB := ShortDeviceID(idB)

	// Register devices
	regDev(t, backend, devIDA)
	regDev(t, backend, devIDB)

	// Shared namespace key
	nsKey, _ := skykey.GenerateSymmetricKey()
	nsID := deriveNSID(nsKey, "test-kv")
	wA, _ := wrapKey(nsKey, idA.PublicKey)
	wB, _ := wrapKey(nsKey, idB.PublicKey)
	backend.Put(ctx, "keys/namespaces/test-kv."+devIDA+".ns.enc",
		bytes.NewReader(wA), int64(len(wA)))
	backend.Put(ctx, "keys/namespaces/test-kv."+devIDB+".ns.enc",
		bytes.NewReader(wB), int64(len(wB)))

	dirA := t.TempDir()
	localLogA := NewLocalLog(filepath.Join(dirA, "kv-ops.jsonl"), devIDA)
	baselinesA := NewBaselineStore(filepath.Join(dirA, "baselines"))

	dirB := t.TempDir()
	localLogB := NewLocalLog(filepath.Join(dirB, "kv-ops.jsonl"), devIDB)
	baselinesB := NewBaselineStore(filepath.Join(dirB, "baselines"))

	uploaderA := NewUploader(backend, localLogA, devIDA, nsID, nsKey, nil)
	uploaderB := NewUploader(backend, localLogB, devIDB, nsID, nsKey, nil)
	pollerA := NewPoller(backend, localLogA, devIDA, nsID, nsKey, 30*time.Second, baselinesA, nil)
	pollerB := NewPoller(backend, localLogB, devIDB, nsID, nsKey, 30*time.Second, baselinesB, nil)

	return &twoDeviceEnv{
		backend: backend, nsKey: nsKey, nsID: nsID,
		devIDA: devIDA, devIDB: devIDB,
		localLogA: localLogA, localLogB: localLogB,
		uploaderA: uploaderA, uploaderB: uploaderB,
		pollerA: pollerA, pollerB: pollerB,
		baselinesA: baselinesA, baselinesB: baselinesB,
	}
}

func regDev(t *testing.T, backend *s3backend.Backend, deviceID string) {
	t.Helper()
	data := []byte(`{"pubkey":"sky10` + deviceID + `placeholder"}`)
	backend.Put(context.Background(), "devices/"+deviceID+".json",
		bytes.NewReader(data), int64(len(data)))
}
