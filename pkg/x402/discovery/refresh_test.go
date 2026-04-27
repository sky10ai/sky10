package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

type erroringSource struct {
	name string
	err  error
}

func (s erroringSource) Name() string { return s.name }
func (s erroringSource) Fetch(_ context.Context) ([]x402.ServiceManifest, error) {
	return nil, s.err
}

func newRegistry(t *testing.T) *x402.Registry {
	t.Helper()
	r, err := x402.NewRegistry(x402.NewMemoryRegistryStore(), func() time.Time {
		return time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRefreshIngestsNewServices(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	src := NewStaticSource("test", []x402.ServiceManifest{sampleManifest()})
	overlay, _ := LoadOverlayBytes(nil)

	got, err := Refresh(context.Background(), r, overlay, []Source{src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Applied) != 1 || got.Applied[0].Kind != DiffKindNew {
		t.Fatalf("Applied = %+v, want one new", got.Applied)
	}
	if len(got.Queued) != 0 || len(got.Removed) != 0 {
		t.Fatalf("did not expect queued/removed, got %+v", got)
	}
	if _, err := r.Manifest(sampleManifest().ID); err != nil {
		t.Fatalf("registry should hold the new manifest: %v", err)
	}
}

func TestRefreshIsIdempotent(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	src := NewStaticSource("test", []x402.ServiceManifest{sampleManifest()})
	overlay, _ := LoadOverlayBytes(nil)

	if _, err := Refresh(context.Background(), r, overlay, []Source{src}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Refresh(context.Background(), r, overlay, []Source{src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Applied) != 0 || len(got.Queued) != 0 || len(got.Removed) != 0 {
		t.Fatalf("idempotent refresh should produce no diffs, got %+v", got)
	}
}

func TestRefreshSafeChangeIsAutoApplied(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	src1 := NewStaticSource("v1", []x402.ServiceManifest{sampleManifest()})
	overlay, _ := LoadOverlayBytes(nil)
	if _, err := Refresh(context.Background(), r, overlay, []Source{src1}, nil); err != nil {
		t.Fatal(err)
	}
	updated := sampleManifest()
	updated.Description = "Updated description"
	src2 := NewStaticSource("v2", []x402.ServiceManifest{updated})
	got, _ := Refresh(context.Background(), r, overlay, []Source{src2}, nil)
	if len(got.Applied) != 1 || got.Applied[0].Kind != DiffKindMetadataOnly {
		t.Fatalf("Applied = %+v, want one metadata_only", got.Applied)
	}
	stored, _ := r.Manifest(sampleManifest().ID)
	if stored.Description != "Updated description" {
		t.Fatalf("registry not updated: %+v", stored)
	}
}

func TestRefreshRiskyChangeIsQueued(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	src1 := NewStaticSource("v1", []x402.ServiceManifest{sampleManifest()})
	overlay, _ := LoadOverlayBytes(nil)
	if _, err := Refresh(context.Background(), r, overlay, []Source{src1}, nil); err != nil {
		t.Fatal(err)
	}
	hiked := sampleManifest()
	hiked.MaxPriceUSDC = "0.500"
	src2 := NewStaticSource("v2", []x402.ServiceManifest{hiked})
	got, _ := Refresh(context.Background(), r, overlay, []Source{src2}, nil)
	if len(got.Queued) != 1 || got.Queued[0].Kind != DiffKindPriceIncreased {
		t.Fatalf("Queued = %+v, want one price_increased", got.Queued)
	}
	stored, _ := r.Manifest(sampleManifest().ID)
	if stored.MaxPriceUSDC == "0.500" {
		t.Fatal("registry should NOT auto-apply price hike")
	}
}

func TestRefreshDetectsRemoval(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	src1 := NewStaticSource("v1", []x402.ServiceManifest{sampleManifest()})
	overlay, _ := LoadOverlayBytes(nil)
	if _, err := Refresh(context.Background(), r, overlay, []Source{src1}, nil); err != nil {
		t.Fatal(err)
	}
	src2 := NewStaticSource("v2", nil) // empty
	got, _ := Refresh(context.Background(), r, overlay, []Source{src2}, nil)
	if len(got.Removed) != 1 || got.Removed[0].Kind != DiffKindRemoved {
		t.Fatalf("Removed = %+v, want one removed", got.Removed)
	}
	// Manifest is retained for receipt history.
	if _, err := r.Manifest(sampleManifest().ID); err != nil {
		t.Fatal("removed manifest should still be retrievable for receipts")
	}
}

func TestRefreshAppliesOverlayPolicyEntries(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	src := NewStaticSource("primitives", BuiltinPrimitives())
	overlay, _ := LoadOverlay()

	if _, err := Refresh(context.Background(), r, overlay, []Source{src}, nil); err != nil {
		t.Fatal(err)
	}
	// Approve deepgram for an agent so ListApproved sees the
	// overlay-supplied tier and hint via the registry.
	if err := r.Approve("A-1", "deepgram", "0.005"); err != nil {
		t.Fatal(err)
	}
	listed := r.ListApproved("A-1")
	if len(listed) != 1 {
		t.Fatalf("listings = %d, want 1", len(listed))
	}
	if listed[0].Tier != x402.TierPrimitive || listed[0].Hint == "" {
		t.Fatalf("overlay metadata not applied to listing: %+v", listed[0])
	}
}

func TestRefreshSurfacesSourceErrors(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	overlay, _ := LoadOverlayBytes(nil)
	good := NewStaticSource("good", []x402.ServiceManifest{sampleManifest()})
	bad := erroringSource{name: "bad", err: errors.New("upstream down")}
	got, err := Refresh(context.Background(), r, overlay, []Source{bad, good}, nil)
	if err != nil {
		t.Fatalf("Refresh top-level err = %v, want nil despite per-source error", err)
	}
	if len(got.Errors) != 1 || got.Errors[0].Source != "bad" {
		t.Fatalf("Errors = %+v, want one entry attributed to 'bad'", got.Errors)
	}
	if len(got.Applied) != 1 {
		t.Fatalf("good source should still apply: %+v", got.Applied)
	}
}
