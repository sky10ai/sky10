package discovery

import (
	"context"
	"testing"

	"github.com/sky10/sky10/pkg/x402"
)

func TestStaticSourceFetchReturnsCopies(t *testing.T) {
	t.Parallel()
	original := []x402.ServiceManifest{sampleManifest()}
	src := NewStaticSource("test", original)

	got, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	// Mutating the caller's slice must not affect future Fetches.
	original[0].DisplayName = "Different"
	got2, _ := src.Fetch(context.Background())
	if got2[0].DisplayName == "Different" {
		t.Fatal("StaticSource should isolate caller's slice")
	}
}

func TestStaticSourceCancelled(t *testing.T) {
	t.Parallel()
	src := NewStaticSource("test", []x402.ServiceManifest{sampleManifest()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := src.Fetch(ctx); err == nil {
		t.Fatal("expected cancelled context error")
	}
}

func TestBuiltinPrimitivesIncludesFour(t *testing.T) {
	t.Parallel()
	primitives := BuiltinPrimitives()
	want := map[string]bool{"deepgram": false, "fal": false, "e2b": false, "browserbase": false}
	for _, m := range primitives {
		if _, ok := want[m.ID]; ok {
			want[m.ID] = true
		}
	}
	for id, present := range want {
		if !present {
			t.Errorf("BuiltinPrimitives missing %q", id)
		}
	}
}
