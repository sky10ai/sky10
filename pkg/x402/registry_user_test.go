package x402

import (
	"errors"
	"testing"
	"time"
)

func TestSetUserEnabledRequiresManifest(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(nil, nil)
	if err := r.SetUserEnabled("ghost", "0.005"); !errors.Is(err, ErrServiceUnknown) {
		t.Fatalf("err = %v, want ErrServiceUnknown", err)
	}
}

func TestSetUserEnabledStoresPin(t *testing.T) {
	t.Parallel()
	clock := func() time.Time { return time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC) }
	r, _ := NewRegistry(NewMemoryRegistryStore(), clock)
	manifest := sampleManifest()
	if err := r.AddManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := r.SetUserEnabled(manifest.ID, "0.005"); err != nil {
		t.Fatal(err)
	}
	rec, ok := r.UserEnabled(manifest.ID)
	if !ok {
		t.Fatal("UserEnabled lookup failed after enable")
	}
	if rec.MaxPriceUSDC != "0.005" {
		t.Fatalf("MaxPriceUSDC = %q, want 0.005", rec.MaxPriceUSDC)
	}
	if err := rec.Pin.Verify(manifest); err != nil {
		t.Fatalf("pin verify against original manifest: %v", err)
	}
	swapped := manifest
	swapped.Endpoint = "https://api.evil.example"
	if err := rec.Pin.Verify(swapped); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("pin should reject endpoint swap, got %v", err)
	}
}

func TestSetUserDisabledRemovesRecord(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(nil, nil)
	_ = r.AddManifest(sampleManifest())
	_ = r.SetUserEnabled(sampleManifest().ID, "")
	if _, ok := r.UserEnabled(sampleManifest().ID); !ok {
		t.Fatal("setup failed")
	}
	if err := r.SetUserDisabled(sampleManifest().ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.UserEnabled(sampleManifest().ID); ok {
		t.Fatal("UserEnabled should be cleared after disable")
	}
}

func TestUserEnableSurvivesRegistryReload(t *testing.T) {
	t.Parallel()
	store := NewMemoryRegistryStore()
	r, _ := NewRegistry(store, nil)
	_ = r.AddManifest(sampleManifest())
	_ = r.SetUserEnabled(sampleManifest().ID, "0.005")

	r2, err := NewRegistry(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.UserEnabled(sampleManifest().ID); !ok {
		t.Fatal("UserEnabled should survive reload")
	}
}

func TestListApprovedIncludesUserEnabled(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(nil, nil)
	_ = r.AddManifest(sampleManifest())
	_ = r.SetUserEnabled(sampleManifest().ID, "0.005")
	listings := r.ListApproved("A-anyone")
	if len(listings) != 1 || listings[0].ID != sampleManifest().ID {
		t.Fatalf("listings = %+v, want one entry for any agent", listings)
	}
}
