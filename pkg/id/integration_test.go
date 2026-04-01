package id

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
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

// minioHarness starts a local MinIO server for integration tests.
type minioHarness struct {
	cmd      *exec.Cmd
	endpoint string
	port     int
}

func startMinIO(t *testing.T) *minioHarness {
	t.Helper()
	if _, err := exec.LookPath("minio"); err != nil {
		t.Skip("minio not installed — skipping integration test")
		return nil
	}

	port := freePort(t)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command("minio", "server", t.TempDir(),
		"--address", fmt.Sprintf(":%d", port), "--quiet")
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
	h.createBucket(t, bucket)

	b, err := s3backend.New(ctx, s3backend.Config{
		Bucket: bucket, Region: "us-east-1",
		Endpoint: h.endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("creating backend: %v", err)
	}
	return b
}

func (h *minioHarness) createBucket(t *testing.T, bucket string) {
	t.Helper()
	ctx := context.Background()
	cfg, _ := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", "")),
	)
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(h.endpoint)
		o.UsePathStyle = true
	})
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
}

func (h *minioHarness) waitReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp",
			fmt.Sprintf("127.0.0.1:%d", h.port), 100*time.Millisecond)
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

func newBucket(t *testing.T) string {
	t.Helper()
	safe := strings.NewReplacer("_", "-", "/", "-", ".", "-").Replace(
		strings.ToLower(t.Name()))
	if len(safe) > 50 {
		safe = safe[:50]
	}
	return fmt.Sprintf("id-%s-%d", safe, time.Now().UnixNano()%100000)
}

// deviceJSON is the minimal device registry entry for tests.
type deviceJSON struct {
	PubKey     string   `json:"pubkey"`
	Name       string   `json:"name"`
	Multiaddrs []string `json:"multiaddrs,omitempty"`
}

func registerDevice(t *testing.T, backend *s3backend.Backend,
	identityAddr, deviceID, name string) {
	t.Helper()
	dev := deviceJSON{PubKey: identityAddr, Name: name}
	data, _ := json.Marshal(dev)
	if err := backend.Put(context.Background(), "devices/"+deviceID+".json",
		bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("register device: %v", err)
	}
}

// TestTwoDevicesSharedBucketIntegration verifies two devices with the
// same identity can register in the same S3 bucket and share encrypted
// namespace keys.
func TestTwoDevicesSharedBucketIntegration(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	bucket := newBucket(t)
	backend := h.backend(t, bucket)
	ctx := context.Background()

	// Generate one identity shared by two devices.
	identity, _ := skykey.Generate()
	deviceA, _ := skykey.Generate()
	deviceB, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "laptop")
	manifest.AddDevice(deviceB.PublicKey, "phone")
	manifest.Sign(identity.PrivateKey)

	bundleA, err := New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}

	// Both devices register with the same identity address.
	registerDevice(t, backend, bundleA.Address(), bundleA.DeviceID(), "laptop")
	registerDevice(t, backend, bundleB.Address(), bundleB.DeviceID(), "phone")

	// Verify both devices are discoverable from S3.
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 device entries, got %d", len(keys))
	}

	// Both should have the same identity address in their pubkey field.
	for _, key := range keys {
		rc, err := backend.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		var dev deviceJSON
		json.NewDecoder(rc).Decode(&dev)
		rc.Close()

		if dev.PubKey != identity.Address() {
			t.Errorf("device %s has pubkey %s, want %s",
				dev.Name, dev.PubKey, identity.Address())
		}
	}

	// Verify namespace key sharing: wrap a key for the identity,
	// both devices can unwrap it.
	nsKey, _ := skykey.GenerateSymmetricKey()
	wrapped, err := skykey.WrapKey(nsKey, identity.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	// Both devices share the same identity key, so both can unwrap.
	unwrappedA, err := skykey.UnwrapKey(wrapped, bundleA.Identity.PrivateKey)
	if err != nil {
		t.Fatalf("device A unwrap: %v", err)
	}
	unwrappedB, err := skykey.UnwrapKey(wrapped, bundleB.Identity.PrivateKey)
	if err != nil {
		t.Fatalf("device B unwrap: %v", err)
	}

	if !bytes.Equal(unwrappedA, nsKey) {
		t.Error("device A got wrong namespace key")
	}
	if !bytes.Equal(unwrappedB, nsKey) {
		t.Error("device B got wrong namespace key")
	}
}

// TestManifestPublishToS3 verifies that a manifest can be stored in S3
// and loaded back with valid signature.
func TestManifestPublishToS3(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	bucket := newBucket(t)
	backend := h.backend(t, bucket)
	ctx := context.Background()

	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "laptop")
	manifest.Sign(identity.PrivateKey)

	// Upload manifest to S3.
	data, _ := json.Marshal(manifest)
	key := "identity/" + identity.Address() + "/manifest.json"
	if err := backend.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}

	// Download and verify.
	rc, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	var loaded DeviceManifest
	if err := json.NewDecoder(rc).Decode(&loaded); err != nil {
		t.Fatal(err)
	}

	if !loaded.Verify(identity.PublicKey) {
		t.Error("manifest loaded from S3 should verify")
	}
	if loaded.Identity != identity.Address() {
		t.Errorf("identity = %s, want %s", loaded.Identity, identity.Address())
	}
	if !loaded.HasDevice(device.PublicKey) {
		t.Error("loaded manifest should contain the device")
	}
}

// TestDeviceAddedAfterInitialSync verifies that adding a second device
// to an existing manifest produces a valid bundle.
func TestDeviceAddedAfterInitialSync(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	bucket := newBucket(t)
	backend := h.backend(t, bucket)
	ctx := context.Background()

	// Device A initializes with one-device manifest.
	identity, _ := skykey.Generate()
	deviceA, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "laptop")
	manifest.Sign(identity.PrivateKey)

	bundleA, _ := New(identity, deviceA, manifest)
	registerDevice(t, backend, bundleA.Address(), bundleA.DeviceID(), "laptop")

	// Upload manifest to S3.
	data, _ := json.Marshal(manifest)
	mKey := "identity/manifest.json"
	backend.Put(ctx, mKey, bytes.NewReader(data), int64(len(data)))

	// Later: device B joins. Download manifest, add device, re-sign.
	rc, _ := backend.Get(ctx, mKey)
	var downloaded DeviceManifest
	json.NewDecoder(rc).Decode(&downloaded)
	rc.Close()

	deviceB, _ := skykey.Generate()
	downloaded.AddDevice(deviceB.PublicKey, "phone")
	downloaded.Sign(identity.PrivateKey)

	// Create bundle B with updated manifest.
	bundleB, err := New(identity, deviceB, &downloaded)
	if err != nil {
		t.Fatalf("creating bundle B: %v", err)
	}

	// Upload updated manifest.
	data, _ = json.Marshal(&downloaded)
	backend.Put(ctx, mKey, bytes.NewReader(data), int64(len(data)))
	registerDevice(t, backend, bundleB.Address(), bundleB.DeviceID(), "phone")

	// Verify both bundles agree on identity.
	if bundleA.Address() != bundleB.Address() {
		t.Error("both devices should share identity address")
	}

	// Verify updated manifest has both devices.
	if !downloaded.HasDevice(deviceA.PublicKey) {
		t.Error("updated manifest should still have device A")
	}
	if !downloaded.HasDevice(deviceB.PublicKey) {
		t.Error("updated manifest should have device B")
	}

	// 3 device entries in S3.
	keys, _ := backend.List(ctx, "devices/")
	if len(keys) != 2 {
		t.Errorf("expected 2 device entries, got %d", len(keys))
	}
}
