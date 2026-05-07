package discovery

import (
	"context"

	"github.com/sky10/sky10/pkg/x402"
)

// StaticSource returns a fixed set of manifests on every Fetch.
// Useful for two cases:
//
//   - tests: pass in a tailored manifest set
//   - bootstrapping: seed the registry with curated primitives at
//     daemon start before live integrations come online
//
// The returned manifests are copies; callers may mutate the
// resulting slice without affecting future Fetches.
type StaticSource struct {
	name      string
	manifests []x402.ServiceManifest
}

// NewStaticSource constructs a StaticSource. name appears in audit
// log and diff attribution; manifests is the set this source
// reports.
func NewStaticSource(name string, manifests []x402.ServiceManifest) *StaticSource {
	clone := make([]x402.ServiceManifest, len(manifests))
	copy(clone, manifests)
	return &StaticSource{name: name, manifests: clone}
}

// Name implements Source.
func (s *StaticSource) Name() string { return s.name }

// Fetch implements Source. ctx is honored so a slow caller can
// cancel; for an in-memory source this means returning nil
// immediately if ctx is already done.
func (s *StaticSource) Fetch(ctx context.Context) ([]x402.ServiceManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]x402.ServiceManifest, len(s.manifests))
	copy(out, s.manifests)
	return out, nil
}

// BuiltinPrimitives returns the curated set of x402 primitive
// services sky10 ships with. These are the capabilities that the
// local browser/search/file/shell tool stack genuinely cannot
// substitute (audio I/O, image/video generation, sandboxed code
// execution, residential-IP browsing).
//
// Endpoints listed here are *placeholder* values. They reflect each
// service's public API host, but neither the URL nor the price has
// been verified against the live x402 protocol. The expectation is
// that AgenticMarketSource (follow-up commit) replaces these with
// freshly-fetched manifests from the directory.
//
// We ship the set anyway so the catalog is non-empty for users who
// want to inspect, approve, and exercise the flow before live
// integration lands. With StubSigner as the production default no
// call actually charges; the whole loop is exercise-only.
func BuiltinPrimitives() []x402.ServiceManifest {
	return []x402.ServiceManifest{
		{
			ID:           "deepgram",
			DisplayName:  "Deepgram",
			Category:     "media",
			Description:  "Speech-to-text and audio analysis.",
			Endpoint:     "https://api.deepgram.com",
			Networks:     []x402.Network{x402.NetworkBase},
			Protocols:    []x402.PaymentProtocol{x402.ProtocolX402},
			MaxPriceUSDC: "0.005",
		},
		{
			ID:           "fal",
			DisplayName:  "fal.ai",
			Category:     "media",
			Description:  "Image and video generation models.",
			Endpoint:     "https://fal.run",
			Networks:     []x402.Network{x402.NetworkBase},
			Protocols:    []x402.PaymentProtocol{x402.ProtocolX402},
			MaxPriceUSDC: "0.020",
		},
		{
			ID:           "e2b",
			DisplayName:  "E2B",
			Category:     "infrastructure",
			Description:  "Sandboxed code execution environments.",
			Endpoint:     "https://api.e2b.dev",
			Networks:     []x402.Network{x402.NetworkBase},
			Protocols:    []x402.PaymentProtocol{x402.ProtocolX402},
			MaxPriceUSDC: "0.010",
		},
		{
			ID:           "browserbase",
			DisplayName:  "Browserbase",
			Category:     "infrastructure",
			Description:  "Residential-IP headless browser sessions.",
			Endpoint:     "https://api.browserbase.com",
			Networks:     []x402.Network{x402.NetworkBase},
			Protocols:    []x402.PaymentProtocol{x402.ProtocolX402},
			MaxPriceUSDC: "0.050",
		},
	}
}
