package x402

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrPinMismatch indicates the live manifest hash, endpoint host, or
// max-price baseline diverged from a service's pinned values. Calls
// that trigger this fail closed; the user must explicitly re-approve
// the service.
var ErrPinMismatch = errors.New("x402: pinned manifest diverged from live")

// Pin captures the values frozen at approval time. The transport
// re-checks every Pin field on every Call before signing.
type Pin struct {
	ServiceID    string `json:"service_id"`
	EndpointHost string `json:"endpoint_host"`
	ManifestHash string `json:"manifest_hash"`
	MaxPriceUSDC string `json:"max_price_usdc_baseline"`
}

// PinFromManifest constructs a Pin from a fresh manifest. The hash is
// computed deterministically over a canonical JSON encoding so
// equivalent manifests pin identically.
func PinFromManifest(m ServiceManifest) (Pin, error) {
	host, err := manifestHost(m)
	if err != nil {
		return Pin{}, err
	}
	hash, err := manifestHash(m)
	if err != nil {
		return Pin{}, err
	}
	return Pin{
		ServiceID:    m.ID,
		EndpointHost: host,
		ManifestHash: hash,
		MaxPriceUSDC: m.MaxPriceUSDC,
	}, nil
}

// Verify checks the live manifest against the Pin. Returns
// ErrPinMismatch (wrapped with detail) if anything load-bearing has
// changed.
func (p Pin) Verify(m ServiceManifest) error {
	if m.ID != p.ServiceID {
		return fmt.Errorf("%w: service_id %q != pinned %q", ErrPinMismatch, m.ID, p.ServiceID)
	}
	host, err := manifestHost(m)
	if err != nil {
		return err
	}
	if host != p.EndpointHost {
		return fmt.Errorf("%w: endpoint host %q != pinned %q", ErrPinMismatch, host, p.EndpointHost)
	}
	hash, err := manifestHash(m)
	if err != nil {
		return err
	}
	if hash != p.ManifestHash {
		return fmt.Errorf("%w: manifest hash diverged", ErrPinMismatch)
	}
	return nil
}

// manifestHost extracts the canonical host (lowercased, port preserved)
// from the manifest's Endpoint URL. Used by Pin to detect host swaps.
func manifestHost(m ServiceManifest) (string, error) {
	u, err := url.Parse(strings.TrimSpace(m.Endpoint))
	if err != nil {
		return "", fmt.Errorf("parsing endpoint %q: %w", m.Endpoint, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("endpoint %q has no host", m.Endpoint)
	}
	return strings.ToLower(u.Host), nil
}

// manifestHash returns sha256 of the canonical JSON encoding of the
// manifest. Two manifests produce the same hash iff they are
// byte-equivalent post-canonicalization.
func manifestHash(m ServiceManifest) (string, error) {
	canonical, err := canonicalManifestJSON(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalManifestJSON returns a JSON encoding of m with stable field
// ordering. Go's encoding/json emits struct fields in declaration
// order, which is deterministic; we zero out the UpdatedAt timestamp
// and display-only catalog metadata before hashing so a refresh that
// only changes UI copy or endpoint descriptions does not trigger
// spurious re-approval.
func canonicalManifestJSON(m ServiceManifest) ([]byte, error) {
	clone := m
	clone.UpdatedAt = time.Time{}
	clone.ServiceURL = ""
	clone.Endpoints = nil
	return json.Marshal(clone)
}
