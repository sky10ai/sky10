package discovery

import (
	"testing"

	"github.com/sky10/sky10/pkg/x402"
)

func sampleManifest() x402.ServiceManifest {
	return x402.ServiceManifest{
		ID:           "perplexity",
		DisplayName:  "Perplexity",
		Category:     "search",
		Description:  "AI-powered search.",
		Endpoint:     "https://api.perplexity.ai",
		Networks:     []x402.Network{x402.NetworkBase},
		MaxPriceUSDC: "0.005",
	}
}

func TestClassifyNew(t *testing.T) {
	t.Parallel()
	d := Classify(nil, sampleManifest())
	if d.Kind != DiffKindNew {
		t.Fatalf("kind = %s, want new", d.Kind)
	}
}

func TestClassifyRemoved(t *testing.T) {
	t.Parallel()
	prev := sampleManifest()
	d := Classify(&prev, x402.ServiceManifest{})
	if d.Kind != DiffKindRemoved {
		t.Fatalf("kind = %s, want removed", d.Kind)
	}
}

func TestClassifyUnchanged(t *testing.T) {
	t.Parallel()
	prev := sampleManifest()
	curr := sampleManifest()
	d := Classify(&prev, curr)
	if d.Kind != DiffKindUnchanged {
		t.Fatalf("kind = %s, want unchanged", d.Kind)
	}
}

func TestClassifyMetadataOnly(t *testing.T) {
	t.Parallel()
	prev := sampleManifest()
	curr := sampleManifest()
	curr.Description = "AI-powered search, with citations."
	d := Classify(&prev, curr)
	if d.Kind != DiffKindMetadataOnly {
		t.Fatalf("kind = %s, want metadata_only", d.Kind)
	}
	if !d.Kind.IsSafe() {
		t.Fatal("metadata_only should be safe to auto-apply")
	}
}

func TestClassifyPriceDecreased(t *testing.T) {
	t.Parallel()
	prev := sampleManifest()
	curr := sampleManifest()
	curr.MaxPriceUSDC = "0.003"
	d := Classify(&prev, curr)
	if d.Kind != DiffKindPriceDecreased {
		t.Fatalf("kind = %s, want price_decreased", d.Kind)
	}
	if !d.Kind.IsSafe() {
		t.Fatal("price_decreased should be safe")
	}
}

func TestClassifyPriceIncreased(t *testing.T) {
	t.Parallel()
	prev := sampleManifest()
	curr := sampleManifest()
	curr.MaxPriceUSDC = "0.500"
	d := Classify(&prev, curr)
	if d.Kind != DiffKindPriceIncreased {
		t.Fatalf("kind = %s, want price_increased", d.Kind)
	}
	if d.Kind.IsSafe() {
		t.Fatal("price_increased must NOT be safe")
	}
}

func TestClassifyEndpointChanged(t *testing.T) {
	t.Parallel()
	prev := sampleManifest()
	curr := sampleManifest()
	curr.Endpoint = "https://api.evil.example"
	d := Classify(&prev, curr)
	if d.Kind != DiffKindEndpointChanged {
		t.Fatalf("kind = %s, want endpoint_changed", d.Kind)
	}
	if d.Kind.IsSafe() {
		t.Fatal("endpoint_changed must NOT be safe")
	}
}

func TestDiffKindStringStable(t *testing.T) {
	t.Parallel()
	cases := map[DiffKind]string{
		DiffKindUnchanged:       "unchanged",
		DiffKindNew:             "new",
		DiffKindMetadataOnly:    "metadata_only",
		DiffKindPriceDecreased:  "price_decreased",
		DiffKindPriceIncreased:  "price_increased",
		DiffKindEndpointChanged: "endpoint_changed",
		DiffKindBreaking:        "breaking",
		DiffKindRemoved:         "removed",
		DiffKindRelisted:        "relisted",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("DiffKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}
