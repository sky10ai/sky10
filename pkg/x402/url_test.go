package x402

import "testing"

func TestServiceHomeURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "api subdomain", raw: "https://api.run402.com/v1/services", want: "https://run402.com"},
		{name: "provider domain", raw: "run402.com", want: "https://run402.com"},
		{name: "public suffix", raw: "https://api.example.co.uk/path", want: "https://example.co.uk"},
		{name: "localhost fallback", raw: "http://localhost:9101/rpc", want: "http://localhost"},
		{name: "reject non-http", raw: "ftp://api.run402.com", want: ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ServiceHomeURL(tt.raw); got != tt.want {
				t.Fatalf("ServiceHomeURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
