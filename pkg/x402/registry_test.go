package x402

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryApproveAndCall(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(NewMemoryRegistryStore(), func() time.Time {
		return time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddManifest(sampleManifest()); err != nil {
		t.Fatal(err)
	}
	if err := r.Approve("A-1", "perplexity", "0.005"); err != nil {
		t.Fatal(err)
	}
	approval, err := r.Approval("A-1", "perplexity")
	if err != nil {
		t.Fatal(err)
	}
	if approval.AgentID != "A-1" || approval.ServiceID != "perplexity" {
		t.Fatalf("approval = %+v", approval)
	}
	pin, err := r.Pin("A-1", "perplexity")
	if err != nil {
		t.Fatal(err)
	}
	if pin.ServiceID != "perplexity" {
		t.Fatalf("pin = %+v", pin)
	}
	if err := pin.Verify(sampleManifest()); err != nil {
		t.Fatalf("pin verify after approval: %v", err)
	}
}

func TestRegistryListApprovedScopesByAgent(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(nil, nil)
	if err := r.AddManifest(sampleManifest()); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPolicy(PolicyEntry{ServiceID: "perplexity", Tier: TierPrimitive, DefaultOn: false, Hint: "use this for current events"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Approve("A-1", "perplexity", "0.005"); err != nil {
		t.Fatal(err)
	}
	listed := r.ListApproved("A-1")
	if len(listed) != 1 {
		t.Fatalf("len(listed) = %d, want 1", len(listed))
	}
	if listed[0].Tier != TierPrimitive || listed[0].Hint == "" {
		t.Fatalf("policy overlay not applied: %+v", listed[0])
	}
	if listed[0].Endpoint != "https://api.perplexity.ai" || len(listed[0].Networks) != 1 || listed[0].Networks[0] != NetworkBase {
		t.Fatalf("manifest metadata not propagated: %+v", listed[0])
	}
	if got := r.ListApproved("A-other"); len(got) != 0 {
		t.Fatalf("agent without approvals returned %d listings", len(got))
	}
}

func TestRegistryRevokeRemovesApprovalAndPin(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(nil, nil)
	_ = r.AddManifest(sampleManifest())
	_ = r.Approve("A-1", "perplexity", "0.005")
	if err := r.Revoke("A-1", "perplexity"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Approval("A-1", "perplexity"); !errors.Is(err, ErrServiceNotApproved) {
		t.Fatalf("approval after revoke err = %v, want ErrServiceNotApproved", err)
	}
	if _, err := r.Pin("A-1", "perplexity"); !errors.Is(err, ErrServiceNotApproved) {
		t.Fatalf("pin after revoke err = %v, want ErrServiceNotApproved", err)
	}
}

func TestRegistryUnknownService(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(nil, nil)
	if err := r.Approve("A-1", "missing", "0.005"); !errors.Is(err, ErrServiceUnknown) {
		t.Fatalf("approve missing svc err = %v, want ErrServiceUnknown", err)
	}
	if _, err := r.Manifest("missing"); !errors.Is(err, ErrServiceUnknown) {
		t.Fatalf("manifest missing svc err = %v, want ErrServiceUnknown", err)
	}
}

func TestFileRegistryStoreRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	store := NewFileRegistryStore(path)

	r, err := NewRegistry(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.AddManifest(sampleManifest())
	_ = r.Approve("A-1", "perplexity", "0.005")

	// Reload from the same path; state should survive.
	r2, err := NewRegistry(NewFileRegistryStore(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Manifest("perplexity"); err != nil {
		t.Fatalf("manifest after reload: %v", err)
	}
	if _, err := r2.Approval("A-1", "perplexity"); err != nil {
		t.Fatalf("approval after reload: %v", err)
	}
	if _, err := r2.Pin("A-1", "perplexity"); err != nil {
		t.Fatalf("pin after reload: %v", err)
	}
}

func TestFileRegistryStoreLoadMissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.json")
	snap, err := NewFileRegistryStore(path).Load()
	if err != nil {
		t.Fatalf("Load missing file err = %v, want nil", err)
	}
	if len(snap.Manifests) != 0 || len(snap.Approvals) != 0 {
		t.Fatalf("missing file Load should return empty snapshot, got %+v", snap)
	}
}
