package discovery

import (
	"testing"

	"github.com/sky10/sky10/pkg/x402"
)

func TestLoadOverlayParsesEmbeddedJSON(t *testing.T) {
	t.Parallel()
	o, err := LoadOverlay()
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	deepgram, ok := o.For("deepgram")
	if !ok {
		t.Fatal("expected deepgram entry in overlay")
	}
	if deepgram.Tier != x402.TierPrimitive || !deepgram.DefaultOn {
		t.Fatalf("deepgram entry = %+v, want tier=primitive default_on=true", deepgram)
	}
	tripadvisor, ok := o.For("tripadvisor")
	if !ok {
		t.Fatal("expected tripadvisor entry in overlay")
	}
	if tripadvisor.Tier != x402.TierConvenience || tripadvisor.DefaultOn {
		t.Fatalf("tripadvisor entry = %+v, want tier=convenience default_on=false", tripadvisor)
	}
}

func TestLoadOverlayBytesEmpty(t *testing.T) {
	t.Parallel()
	o, err := LoadOverlayBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := o.For("deepgram"); ok {
		t.Fatal("empty overlay should know nothing")
	}
	if entries := o.Entries(); len(entries) != 0 {
		t.Fatalf("Entries() = %d, want 0", len(entries))
	}
}

func TestLoadOverlayBytesRejectsMissingServiceID(t *testing.T) {
	t.Parallel()
	bad := []byte(`[{"tier":"primitive","default_on":true}]`)
	if _, err := LoadOverlayBytes(bad); err == nil {
		t.Fatal("expected error for entry missing service_id")
	}
}

func TestLoadOverlayBytesRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	if _, err := LoadOverlayBytes([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestOverlayForUnknownService(t *testing.T) {
	t.Parallel()
	o, _ := LoadOverlay()
	_, ok := o.For("nonexistent-service")
	if ok {
		t.Fatal("For unknown service should return false")
	}
}
