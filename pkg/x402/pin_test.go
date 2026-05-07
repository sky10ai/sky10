package x402

import (
	"errors"
	"testing"
	"time"
)

func sampleManifest() ServiceManifest {
	return ServiceManifest{
		ID:           "perplexity",
		DisplayName:  "Perplexity",
		Endpoint:     "https://api.perplexity.ai",
		Networks:     []Network{NetworkBase},
		MaxPriceUSDC: "0.005",
		UpdatedAt:    time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC),
	}
}

func TestPinFromManifest(t *testing.T) {
	t.Parallel()
	pin, err := PinFromManifest(sampleManifest())
	if err != nil {
		t.Fatalf("PinFromManifest: %v", err)
	}
	if pin.ServiceID != "perplexity" {
		t.Fatalf("ServiceID = %q", pin.ServiceID)
	}
	if pin.EndpointHost != "api.perplexity.ai" {
		t.Fatalf("EndpointHost = %q", pin.EndpointHost)
	}
	if pin.MaxPriceUSDC != "0.005" {
		t.Fatalf("MaxPriceUSDC = %q", pin.MaxPriceUSDC)
	}
	if len(pin.ManifestHash) < 10 {
		t.Fatalf("ManifestHash too short: %q", pin.ManifestHash)
	}
}

func TestPinVerifyCatchesEndpointSwap(t *testing.T) {
	t.Parallel()
	pin, _ := PinFromManifest(sampleManifest())
	swapped := sampleManifest()
	swapped.Endpoint = "https://api.evil.example"
	if err := pin.Verify(swapped); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("verify swapped err = %v, want ErrPinMismatch", err)
	}
}

func TestPinVerifyCatchesPriceSwap(t *testing.T) {
	t.Parallel()
	pin, _ := PinFromManifest(sampleManifest())
	swapped := sampleManifest()
	swapped.MaxPriceUSDC = "0.500"
	if err := pin.Verify(swapped); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("verify price-swap err = %v, want ErrPinMismatch", err)
	}
}

func TestPinVerifyAcceptsIdenticalManifest(t *testing.T) {
	t.Parallel()
	pin, _ := PinFromManifest(sampleManifest())
	if err := pin.Verify(sampleManifest()); err != nil {
		t.Fatalf("identical manifest err = %v, want nil", err)
	}
}

func TestPinVerifyTolerantToUpdatedAtDrift(t *testing.T) {
	t.Parallel()
	// UpdatedAt is canonicalized out of the hash so a refresh that
	// only bumps the timestamp doesn't trigger spurious re-approval.
	pin, _ := PinFromManifest(sampleManifest())
	bumped := sampleManifest()
	bumped.UpdatedAt = bumped.UpdatedAt.Add(time.Hour)
	if err := pin.Verify(bumped); err != nil {
		t.Fatalf("UpdatedAt drift err = %v, want nil", err)
	}
}

func TestPinVerifyTolerantToDisplayEndpointMetadata(t *testing.T) {
	t.Parallel()
	pin, _ := PinFromManifest(sampleManifest())
	updated := sampleManifest()
	updated.ServiceURL = "https://perplexity.ai"
	updated.Endpoints = []ServiceEndpoint{{
		URL:         "https://api.perplexity.ai/search",
		Method:      "POST",
		Description: "Search endpoint",
		PriceUSDC:   "0.005",
		Network:     NetworkBase,
	}}
	if err := pin.Verify(updated); err != nil {
		t.Fatalf("display endpoint metadata err = %v, want nil", err)
	}
}
