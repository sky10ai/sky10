package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/sky10/sky10/pkg/x402"
)

// RefreshResult is the per-tick outcome of Refresh. Applied lists
// safe diffs the registry now reflects; Queued lists risky diffs the
// daemon should surface for user re-approval; Removed lists services
// that disappeared from every source. Errors records per-source
// fetch failures (one source failing does not abort the others).
type RefreshResult struct {
	Applied []Diff
	Queued  []Diff
	Removed []Diff
	Errors  []SourceError
}

// SourceError attributes a fetch failure to one source.
type SourceError struct {
	Source string
	Err    error
}

// Error renders the SourceError for a wrapped error chain.
func (e SourceError) Error() string {
	return fmt.Sprintf("%s: %v", e.Source, e.Err)
}

// Refresh performs one ingestion pass:
//
//  1. Apply Overlay entries to the registry's policy table so any
//     editorial changes since last refresh are visible.
//  2. Fetch from every Source. Per-source errors are collected but
//     do not abort other sources.
//  3. Build a unified observation map (service_id → manifest) by
//     merging across sources; the first source to report a service
//     wins ties.
//  4. For every service in the registry plus every observed service,
//     classify the diff against the registry's prior view.
//  5. Apply safe diffs (DiffKindNew, DiffKindMetadataOnly,
//     DiffKindPriceDecreased) by writing the new manifest into the
//     registry. Queue risky diffs (DiffKindPriceIncreased,
//     DiffKindEndpointChanged, DiffKindBreaking) for surface-level
//     review. Note services no longer reported by any source as
//     DiffKindRemoved.
//
// Refresh is idempotent: calling it twice with no upstream change
// produces an empty Applied/Queued/Removed.
func Refresh(ctx context.Context, registry *x402.Registry, overlay *Overlay, sources []Source, logger *slog.Logger) (RefreshResult, error) {
	if registry == nil {
		return RefreshResult{}, errors.New("discovery: nil registry")
	}
	if logger == nil {
		logger = slog.Default()
	}

	if overlay != nil {
		for _, entry := range overlay.Entries() {
			if err := registry.SetPolicy(entry); err != nil {
				logger.Warn("apply overlay entry failed", "service_id", entry.ServiceID, "error", err)
			}
		}
	}

	var result RefreshResult
	observed := make(map[string]x402.ServiceManifest)
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		manifests, err := src.Fetch(ctx)
		if err != nil {
			result.Errors = append(result.Errors, SourceError{Source: src.Name(), Err: err})
			logger.Warn("source fetch failed", "source", src.Name(), "error", err)
			continue
		}
		for _, m := range manifests {
			if m.ID == "" {
				continue
			}
			if _, exists := observed[m.ID]; !exists {
				observed[m.ID] = m
			}
		}
	}

	priors := registryManifests(registry)

	for id, curr := range observed {
		var prevPtr *x402.ServiceManifest
		if prev, ok := priors[id]; ok {
			pCopy := prev
			prevPtr = &pCopy
		}
		diff := Classify(prevPtr, curr)
		switch diff.Kind {
		case DiffKindUnchanged:
			// no-op
		case DiffKindNew, DiffKindRelisted, DiffKindMetadataOnly, DiffKindPriceDecreased:
			if err := registry.AddManifest(curr); err != nil {
				logger.Warn("apply safe diff failed", "service_id", id, "kind", diff.Kind.String(), "error", err)
				result.Errors = append(result.Errors, SourceError{Source: "registry", Err: err})
				continue
			}
			result.Applied = append(result.Applied, diff)
		default:
			// Risky: PriceIncreased, EndpointChanged, Breaking. Do
			// not touch the registry; surface for re-approval.
			result.Queued = append(result.Queued, diff)
		}
	}

	for id, prev := range priors {
		if _, ok := observed[id]; ok {
			continue
		}
		diff := Diff{Kind: DiffKindRemoved, Service: prev, Previous: nil, Reason: "no source reported this service"}
		result.Removed = append(result.Removed, diff)
	}

	sortDiffs(result.Applied)
	sortDiffs(result.Queued)
	sortDiffs(result.Removed)
	return result, nil
}

// registryManifests returns the registry's current manifest set
// keyed by service id. The registry exposes Manifest(id) for single
// lookups; this helper does the snapshot we need for diffing.
func registryManifests(r *x402.Registry) map[string]x402.ServiceManifest {
	listings := r.AllManifests()
	out := make(map[string]x402.ServiceManifest, len(listings))
	for _, m := range listings {
		out[m.ID] = m
	}
	return out
}

func sortDiffs(diffs []Diff) {
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Service.ID < diffs[j].Service.ID
	})
}
