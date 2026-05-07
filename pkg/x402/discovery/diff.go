package discovery

import (
	"reflect"

	"github.com/sky10/sky10/pkg/x402"
)

// DiffKind captures how a manifest observation relates to the
// registry's prior view of the same service. Refresh acts on each
// kind differently — safe kinds apply automatically, risky kinds
// queue for re-approval, and removed/relisted bracket service
// lifecycle.
type DiffKind int

const (
	// DiffKindUnchanged indicates the live manifest matches the
	// registry's prior copy; no action.
	DiffKindUnchanged DiffKind = iota

	// DiffKindNew indicates a service the registry has never seen.
	// Refresh adds it as approved=false, tier=convenience by
	// default. The overlay may promote tier later.
	DiffKindNew

	// DiffKindMetadataOnly indicates description, category, or
	// display-name text changed but the load-bearing fields
	// (endpoint, price, networks) are unchanged. Safe to
	// auto-apply.
	DiffKindMetadataOnly

	// DiffKindPriceDecreased indicates max_price_usdc went down.
	// Safe to auto-apply; the new lower price strictly favors the
	// user.
	DiffKindPriceDecreased

	// DiffKindPriceIncreased indicates max_price_usdc went up.
	// Risky: the agent's pinned baseline no longer covers worst-
	// case spend. Queue for re-approval.
	DiffKindPriceIncreased

	// DiffKindEndpointChanged indicates endpoint host or path
	// changed. Risky: a malicious or compromised directory could
	// redirect approved services. Queue for re-approval.
	DiffKindEndpointChanged

	// DiffKindBreaking indicates a schema or scope change beyond
	// price and endpoint that warrants explicit re-approval.
	// Reserved for future schema fields; M2 always returns one of
	// the more specific kinds above.
	DiffKindBreaking

	// DiffKindRemoved indicates a service that was previously in
	// the catalog is no longer reported by any source. Refresh
	// marks the registry entry removed but does not delete it,
	// preserving receipts and giving the user a chance to react.
	DiffKindRemoved

	// DiffKindRelisted indicates a service that was previously
	// removed has reappeared in the catalog. Treated like
	// DiffKindNew (fresh approval required).
	DiffKindRelisted
)

// String renders the kind for log lines and error messages. Stable
// strings let downstream code (CLI, audit log) match without
// importing this package's enum.
func (k DiffKind) String() string {
	switch k {
	case DiffKindUnchanged:
		return "unchanged"
	case DiffKindNew:
		return "new"
	case DiffKindMetadataOnly:
		return "metadata_only"
	case DiffKindPriceDecreased:
		return "price_decreased"
	case DiffKindPriceIncreased:
		return "price_increased"
	case DiffKindEndpointChanged:
		return "endpoint_changed"
	case DiffKindBreaking:
		return "breaking"
	case DiffKindRemoved:
		return "removed"
	case DiffKindRelisted:
		return "relisted"
	default:
		return "unknown"
	}
}

// IsSafe reports whether a diff kind can be written to the registry
// without queuing for explicit user re-approval. New services are
// considered safe-to-add even though they require approval for use:
// the registry needs the manifest in place before any approval flow
// can act on it. Approval state itself (`Approved` records) is
// orthogonal — IsSafe is purely about manifest persistence.
func (k DiffKind) IsSafe() bool {
	switch k {
	case DiffKindNew, DiffKindRelisted, DiffKindMetadataOnly, DiffKindPriceDecreased:
		return true
	default:
		return false
	}
}

// Diff is one observed change. Service is the live observation;
// Previous is the registry's prior copy when one existed.
type Diff struct {
	Kind     DiffKind
	Service  x402.ServiceManifest
	Previous *x402.ServiceManifest
	Reason   string
}

// Classify compares a live observation against the registry's prior
// manifest and returns the appropriate diff. Either side may be a
// zero-valued manifest:
//
//   - prev empty (ID=="") + curr present  → DiffKindNew
//   - prev present + curr empty            → DiffKindRemoved
//   - prev removed + curr present          → DiffKindRelisted
//   - both present + identical            → DiffKindUnchanged
//   - both present + price decreased      → DiffKindPriceDecreased
//   - both present + price increased      → DiffKindPriceIncreased
//   - both present + endpoint changed     → DiffKindEndpointChanged
//   - otherwise (description-only)        → DiffKindMetadataOnly
//
// "removed" in the prior is signalled by a non-zero RemovedAt; that
// state lands in a follow-up. M2 treats absence-from-catalog as
// removal at the Refresh layer rather than as a manifest field.
func Classify(prev *x402.ServiceManifest, curr x402.ServiceManifest) Diff {
	if prev == nil || prev.ID == "" {
		return Diff{Kind: DiffKindNew, Service: curr}
	}
	if curr.ID == "" {
		return Diff{Kind: DiffKindRemoved, Service: *prev, Previous: prev}
	}
	d := Diff{Service: curr, Previous: prev}
	prevHost := manifestHost(*prev)
	currHost := manifestHost(curr)
	if prevHost != currHost {
		d.Kind = DiffKindEndpointChanged
		d.Reason = "endpoint host changed"
		return d
	}
	priceCmp := compareUSDC(prev.MaxPriceUSDC, curr.MaxPriceUSDC)
	if priceCmp < 0 {
		d.Kind = DiffKindPriceIncreased
		d.Reason = "max_price_usdc increased"
		return d
	}
	if priceCmp > 0 {
		d.Kind = DiffKindPriceDecreased
		d.Reason = "max_price_usdc decreased"
		return d
	}
	if prev.DisplayName != curr.DisplayName ||
		prev.Description != curr.Description ||
		prev.Category != curr.Category ||
		prev.ServiceURL != curr.ServiceURL ||
		!reflect.DeepEqual(prev.Endpoints, curr.Endpoints) {
		d.Kind = DiffKindMetadataOnly
		d.Reason = "metadata changed"
		return d
	}
	d.Kind = DiffKindUnchanged
	return d
}

// manifestHost extracts the lowercased host:port from a manifest's
// endpoint URL, returning "" on parse failure. Used by Classify to
// detect endpoint redirects.
func manifestHost(m x402.ServiceManifest) string {
	pin, err := x402.PinFromManifest(m)
	if err != nil {
		return ""
	}
	return pin.EndpointHost
}

// compareUSDC returns -1, 0, +1 reflecting the relationship between
// two decimal USDC strings. A parse failure on either side returns 0
// (treat as equal) so a malformed manifest doesn't masquerade as a
// price change.
func compareUSDC(a, b string) int {
	if a == b {
		return 0
	}
	cmp, err := x402.CompareUSDC(a, b)
	if err != nil {
		return 0
	}
	return cmp
}
