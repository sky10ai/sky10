package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

type Registry struct {
	collector localCollector
	now       func() time.Time
}

var defaultRegistry = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{
		collector: newLocalCollector(),
		now:       time.Now,
	}
}

// DeviceName returns a human-readable name for this device via the default collector.
func (r *Registry) DeviceName() string {
	return r.collector.deviceName()
}

// LocalInfo builds the current local device snapshot without reading or writing storage.
func (r *Registry) LocalInfo(deviceID string, pubKeyHex string, name string, version string) Info {
	meta := r.collector.current()
	if strings.TrimSpace(name) == "" {
		name = meta.Name
	}
	now := r.now().UTC().Format(time.RFC3339)
	return Info{
		ID:       deviceID,
		PubKey:   pubKeyHex,
		Name:     name,
		Platform: meta.Platform,
		IP:       meta.IP,
		Location: meta.Location,
		Version:  version,
		Joined:   now,
		LastSeen: now,
	}
}

// LocalInfo builds the current local device snapshot without reading or writing storage.
func LocalInfo(deviceID string, pubKeyHex string, name string, version string) Info {
	return defaultRegistry.LocalInfo(deviceID, pubKeyHex, name, version)
}

// Register writes this device's info to the S3 registry.
// deviceID is the 16-char identifier, pubKeyHex is the hex-encoded
// Ed25519 device public key.
// On re-registration (daemon restart), it preserves the original join date
// but refreshes the IP and location.
func (r *Registry) Register(ctx context.Context, backend adapter.Backend, deviceID string, pubKeyHex string, name string, version string) error {
	key := deviceKeyPath(deviceID)

	info := r.LocalInfo(deviceID, pubKeyHex, name, version)
	if existing, err := r.read(ctx, backend, key); err == nil {
		if existing.Joined != "" {
			info.Joined = existing.Joined
		}
		info.Alias = existing.Alias
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	rdr := bytes.NewReader(data)
	return backend.Put(ctx, key, rdr, int64(len(data)))
}

// Register writes this device's info to the S3 registry.
func Register(ctx context.Context, backend adapter.Backend, deviceID string, pubKeyHex string, name string, version string) error {
	return defaultRegistry.Register(ctx, backend, deviceID, pubKeyHex, name, version)
}

// UpdateMultiaddrs updates the multiaddrs field on an existing device entry.
func (r *Registry) UpdateMultiaddrs(ctx context.Context, backend adapter.Backend, deviceID string, addrs []string) error {
	key := deviceKeyPath(deviceID)

	existing, err := r.read(ctx, backend, key)
	if err != nil {
		return fmt.Errorf("reading device: %w", err)
	}

	existing.Multiaddrs = append([]string(nil), addrs...)
	existing.LastSeen = r.now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return backend.Put(ctx, key, bytes.NewReader(data), int64(len(data)))
}

// UpdateMultiaddrs updates the multiaddrs field on an existing device entry.
func UpdateMultiaddrs(ctx context.Context, backend adapter.Backend, deviceID string, addrs []string) error {
	return defaultRegistry.UpdateMultiaddrs(ctx, backend, deviceID, addrs)
}

// List returns all registered devices from the registry.
func (r *Registry) List(ctx context.Context, backend adapter.Backend) ([]Info, error) {
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		return nil, err
	}

	type result struct {
		idx int
		dev *Info
	}
	ch := make(chan result, len(keys))
	for i, k := range keys {
		go func(idx int, key string) {
			d, err := r.read(ctx, backend, key)
			if err != nil {
				ch <- result{idx: idx}
				return
			}
			ch <- result{idx: idx, dev: d}
		}(i, k)
	}

	devices := make([]Info, 0, len(keys))
	for range keys {
		r := <-ch
		if r.dev != nil {
			devices = append(devices, *r.dev)
		}
	}

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Name < devices[j].Name
	})
	return devices, nil
}

// List returns all registered devices from the registry.
func List(ctx context.Context, backend adapter.Backend) ([]Info, error) {
	return defaultRegistry.List(ctx, backend)
}

func (r *Registry) read(ctx context.Context, backend adapter.Backend, key string) (*Info, error) {
	rc, err := backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var d Info
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func deviceKeyPath(deviceID string) string {
	return "devices/" + deviceID + ".json"
}
