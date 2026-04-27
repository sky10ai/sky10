package discovery

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/sky10/sky10/pkg/x402"
)

//go:embed overlay.json
var overlayJSON []byte

// Overlay is sky10's editorial layer over the upstream service
// catalog: per-service tier classification, default-on flag, and a
// routing hint surfaced to the LLM in tool descriptions.
//
// Overlay data lives in overlay.json embedded into the binary.
// Updates ride on sky10 releases initially; future work moves it to
// its own update channel so we can re-tier without cutting a
// release.
type Overlay struct {
	entries map[string]x402.PolicyEntry
}

// LoadOverlay parses the embedded overlay.json into an Overlay. The
// returned Overlay is read-only; callers mutate via a separate
// editorial pipeline that produces a new overlay.json.
func LoadOverlay() (*Overlay, error) {
	return parseOverlay(overlayJSON)
}

// LoadOverlayBytes parses an arbitrary overlay payload. Useful in
// tests that want to exercise the overlay logic without modifying
// the embedded data.
func LoadOverlayBytes(data []byte) (*Overlay, error) {
	return parseOverlay(data)
}

func parseOverlay(data []byte) (*Overlay, error) {
	var entries []x402.PolicyEntry
	if len(data) == 0 {
		return &Overlay{entries: map[string]x402.PolicyEntry{}}, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse overlay: %w", err)
	}
	out := &Overlay{entries: make(map[string]x402.PolicyEntry, len(entries))}
	for _, e := range entries {
		if e.ServiceID == "" {
			return nil, fmt.Errorf("overlay entry missing service_id")
		}
		out.entries[e.ServiceID] = e
	}
	return out, nil
}

// For returns the overlay entry for serviceID. The bool is false
// when the overlay has no opinion on this service; callers should
// fall back to defaults (tier=convenience, default_on=false, no
// hint).
func (o *Overlay) For(serviceID string) (x402.PolicyEntry, bool) {
	if o == nil {
		return x402.PolicyEntry{}, false
	}
	entry, ok := o.entries[serviceID]
	return entry, ok
}

// Entries returns a snapshot of every overlay entry in stable
// service-id order. Used by Refresh to seed the registry's policy
// table on each refresh tick.
func (o *Overlay) Entries() []x402.PolicyEntry {
	if o == nil {
		return nil
	}
	out := make([]x402.PolicyEntry, 0, len(o.entries))
	for _, e := range o.entries {
		out = append(out, e)
	}
	return out
}
