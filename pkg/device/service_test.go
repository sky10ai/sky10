package device

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skyid "github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func TestPrivateNetworkMetadataIncludesLocalCurrentDeviceWithoutBackend(t *testing.T) {
	t.Parallel()

	bundle := testBundle(t, "node-a")
	service := testDeviceService()

	metadata, err := service.PrivateNetworkMetadata(context.Background(), bundle, nil, nil)
	if err != nil {
		t.Fatalf("PrivateNetworkMetadata: %v", err)
	}

	currentMeta, ok := metadata[bundle.DevicePubKeyHex()]
	if !ok {
		t.Fatalf("missing current device metadata for %s", bundle.DevicePubKeyHex())
	}
	if currentMeta.Platform != "Linux" {
		t.Fatalf("platform = %q, want Linux", currentMeta.Platform)
	}
	if currentMeta.IP != "203.0.113.8" {
		t.Fatalf("ip = %q, want 203.0.113.8", currentMeta.IP)
	}
	if currentMeta.Location != "Austin, Texas, United States" {
		t.Fatalf("location = %q, want Austin, Texas, United States", currentMeta.Location)
	}
	if currentMeta.LastSeen != "2026-04-18T12:00:00Z" {
		t.Fatalf("last_seen = %q, want 2026-04-18T12:00:00Z", currentMeta.LastSeen)
	}
}

func TestPrivateNetworkMetadataFillsMissingCurrentDeviceFieldsFromLocalState(t *testing.T) {
	t.Parallel()

	bundle := testBundle(t, "node-a")
	service := testDeviceService()
	backend := s3adapter.NewMemory()
	entry := `{
  "id": "` + bundle.DeviceID() + `",
  "pubkey": "` + bundle.DevicePubKeyHex() + `",
  "name": "node-a",
  "alias": "office laptop",
  "joined": "2026-04-01T00:00:00Z"
}`

	if err := backend.Put(
		context.Background(),
		"devices/"+bundle.DeviceID()+".json",
		strings.NewReader(entry),
		int64(len(entry)),
	); err != nil {
		t.Fatalf("Put registry entry: %v", err)
	}

	metadata, err := service.PrivateNetworkMetadata(context.Background(), bundle, backend, nil)
	if err != nil {
		t.Fatalf("PrivateNetworkMetadata: %v", err)
	}

	currentMeta := metadata[bundle.DevicePubKeyHex()]
	if currentMeta.Alias != "office laptop" {
		t.Fatalf("alias = %q, want office laptop", currentMeta.Alias)
	}
	if currentMeta.Platform != "Linux" {
		t.Fatalf("platform = %q, want Linux", currentMeta.Platform)
	}
	if currentMeta.Location != "Austin, Texas, United States" {
		t.Fatalf("location = %q, want Austin, Texas, United States", currentMeta.Location)
	}
}

func TestPrivateNetworkMetadataUsesRegistryCurrentDeviceFieldsWithoutLocalLookup(t *testing.T) {
	t.Parallel()

	bundle := testBundle(t, "node-a")
	backend := s3adapter.NewMemory()
	entry := `{
  "id": "` + bundle.DeviceID() + `",
  "pubkey": "` + bundle.DevicePubKeyHex() + `",
  "name": "node-a",
  "alias": "office laptop",
  "platform": "Linux",
  "ip": "198.51.100.20",
  "location": "Chicago, Illinois, United States",
  "last_seen": "2026-04-10T00:00:00Z",
  "joined": "2026-04-01T00:00:00Z"
}`

	if err := backend.Put(
		context.Background(),
		"devices/"+bundle.DeviceID()+".json",
		strings.NewReader(entry),
		int64(len(entry)),
	); err != nil {
		t.Fatalf("Put registry entry: %v", err)
	}

	lookups := 0
	service := &Service{
		registry: &Registry{
			collector: localCollector{
				client: &http.Client{
					Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						lookups++
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader(`{"query":"203.0.113.8"}`)),
							Header:     make(http.Header),
						}, nil
					}),
				},
				hostname: func() (string, error) { return "test-host", nil },
				goos:     "linux",
			},
			now: func() time.Time {
				return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
			},
		},
		now: func() time.Time {
			return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
		},
	}

	metadata, err := service.PrivateNetworkMetadata(context.Background(), bundle, backend, nil)
	if err != nil {
		t.Fatalf("PrivateNetworkMetadata: %v", err)
	}

	currentMeta := metadata[bundle.DevicePubKeyHex()]
	if currentMeta.IP != "198.51.100.20" {
		t.Fatalf("ip = %q, want 198.51.100.20", currentMeta.IP)
	}
	if currentMeta.Location != "Chicago, Illinois, United States" {
		t.Fatalf("location = %q, want Chicago, Illinois, United States", currentMeta.Location)
	}
	if lookups != 0 {
		t.Fatalf("geo-ip lookups = %d, want 0", lookups)
	}
}

func TestPrivateNetworkMetadataUsesConnectedPrivatePeers(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := testSharedBundles(t)
	service := testDeviceService()

	nodeA, err := link.New(bundleA, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodeA.SetVersion("v-test")

	nodeB, err := link.New(bundleB, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	startTestLinkNode(t, nodeA)
	startTestLinkNode(t, nodeB)
	connectTestNodes(t, nodeA, nodeB)

	waitForCondition(t, 5*time.Second, func() bool {
		return len(nodeA.ConnectedPrivateNetworkPeers()) == 1
	})

	metadata, err := service.PrivateNetworkMetadata(context.Background(), bundleA, nil, nodeA)
	if err != nil {
		t.Fatalf("PrivateNetworkMetadata: %v", err)
	}

	currentMeta, ok := metadata[bundleA.DevicePubKeyHex()]
	if !ok {
		t.Fatalf("missing current device metadata for %s", bundleA.DevicePubKeyHex())
	}
	if currentMeta.Version != "v-test" {
		t.Fatalf("current version = %q, want v-test", currentMeta.Version)
	}
	if currentMeta.Platform != "Linux" {
		t.Fatalf("current platform = %q, want Linux", currentMeta.Platform)
	}
	if currentMeta.Location != "Austin, Texas, United States" {
		t.Fatalf("current location = %q, want Austin, Texas, United States", currentMeta.Location)
	}
	if len(currentMeta.Multiaddrs) == 0 {
		t.Fatal("expected current device multiaddrs")
	}

	remoteMeta, ok := metadata[bundleB.DevicePubKeyHex()]
	if !ok {
		t.Fatalf("missing remote device metadata for %s", bundleB.DevicePubKeyHex())
	}
	if len(remoteMeta.Multiaddrs) == 0 {
		t.Fatal("expected remote device multiaddrs from connected private peer")
	}
	if remoteMeta.LastSeen == "" {
		t.Fatal("expected remote device last_seen from connected private peer")
	}
}

func testDeviceService() *Service {
	return &Service{
		registry: &Registry{
			collector: localCollector{
				client:   testHTTPClient(`{"query":"203.0.113.8","city":"Austin","regionName":"Texas","country":"United States"}`),
				hostname: func() (string, error) { return "test-host", nil },
				goos:     "linux",
			},
			now: func() time.Time {
				return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
			},
		},
		now: func() time.Time {
			return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
		},
	}
}

func testBundle(t *testing.T, name string) *skyid.Bundle {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	device, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := skyid.NewManifest(identity)
	manifest.AddDevice(device.PublicKey, name)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundle, err := skyid.New(identity, device, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func testSharedBundles(t *testing.T) (*skyid.Bundle, *skyid.Bundle) {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := skyid.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := skyid.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := skyid.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundleA, bundleB
}

func startTestLinkNode(t *testing.T, node *link.Node) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- node.Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for node.Host() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if node.Host() == nil {
		cancel()
		t.Fatal("node did not start")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
}

func connectTestNodes(t *testing.T, a, b *link.Node) {
	t.Helper()

	info := b.Host().Peerstore().PeerInfo(b.PeerID())
	info.Addrs = b.Host().Addrs()
	if err := a.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("connect nodes: %v", err)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
