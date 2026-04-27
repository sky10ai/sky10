package discovery

import (
	"context"

	"github.com/sky10/sky10/pkg/x402"
)

// Source produces a snapshot of service manifests visible to it. A
// Source is a one-shot fetcher; Refresh decides cadence.
//
// Implementations include StaticSource (curated primitives baked into
// the binary) and AgenticMarketSource (polls the directory, fetches
// per-service /.well-known/x402.json — the latter lands in a follow-
// up commit alongside live integration).
type Source interface {
	// Name identifies the source for audit logging and diff
	// attribution.
	Name() string

	// Fetch returns the current set of manifests this Source knows
	// about. Implementations should return cleanly when ctx is
	// cancelled.
	Fetch(ctx context.Context) ([]x402.ServiceManifest, error)
}
