package x402

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// liveFixtureWire is the on-disk shape of the captures
// live_smoke_test.go writes. Re-declared here (instead of imported
// from live_smoke_test.go) so structural tests run regardless of the
// X402_LIVE gate.
type liveFixtureWire struct {
	Service       string                   `json:"service"`
	Method        string                   `json:"method"`
	URL           string                   `json:"url"`
	CapturedAtUTC string                   `json:"captured_at_utc"`
	Exchanges     []capturedExchangeOnDisk `json:"exchanges"`
}

type capturedExchangeOnDisk struct {
	RequestMethod   string      `json:"request_method"`
	RequestURL      string      `json:"request_url"`
	RequestHeaders  http.Header `json:"request_headers"`
	RequestBody     string      `json:"request_body,omitempty"`
	ResponseStatus  int         `json:"response_status"`
	ResponseHeaders http.Header `json:"response_headers"`
	ResponseBody    string      `json:"response_body,omitempty"`
}

// TestFixtureChallengesParseToCanonical replays every captured 402
// challenge through the version-appropriate parser and asserts the
// canonical PaymentChallenge has the structural properties we
// depend on downstream (non-empty Accepts, supported scheme/network,
// AmountMicros parses positive). Failure here means a real x402
// service has shipped a wire shape we no longer round-trip cleanly.
//
// Skipped for fixtures whose first response is 200 — those are
// SIWX-only or otherwise free endpoints that never produce a 402
// challenge to parse (Venice /balance, etc.).
func TestFixtureChallengesParseToCanonical(t *testing.T) {
	t.Parallel()
	for _, fx := range loadAllFixtures(t) {
		fx := fx
		t.Run(stripExt(fx.path), func(t *testing.T) {
			if !fixtureHas402(fx.fixture) {
				t.Skip("fixture has no 402 exchange — likely a free SIWX-only endpoint")
			}
			challenge := findChallengeExchange(t, fx.fixture)
			version, parsed, err := parseFixtureChallenge(challenge)
			if err != nil {
				t.Fatalf("parse challenge: %v", err)
			}
			if version != X402ProtocolV1 && version != X402ProtocolV2 {
				t.Fatalf("unrecognized version %d", version)
			}
			if len(parsed.Accepts) == 0 {
				t.Fatalf("Accepts empty")
			}
			req, err := parsed.SelectRequirements()
			if err != nil {
				t.Fatalf("SelectRequirements: %v", err)
			}
			if req.AmountMicros == "" || strings.Trim(req.AmountMicros, "0") == "" {
				t.Fatalf("AmountMicros = %q, want positive integer", req.AmountMicros)
			}
			if req.Asset == "" {
				t.Fatalf("Asset empty")
			}
			if req.PayTo == "" {
				t.Fatalf("PayTo empty")
			}
			if version == X402ProtocolV2 && parsed.Resource == nil {
				t.Fatalf("v2 challenge missing resource")
			}
		})
	}
}

// TestFixturePaymentEnvelopeMatchesVersion checks the X-PAYMENT
// envelope we actually sent for each fixture. v1 has top-level
// scheme/network and no `accepted`; v2 has `accepted`+`resource`
// and no top-level scheme/network. This pins the wire shape against
// real captures so a future refactor can't silently emit the wrong
// envelope.
func TestFixturePaymentEnvelopeMatchesVersion(t *testing.T) {
	t.Parallel()
	for _, fx := range loadAllFixtures(t) {
		fx := fx
		t.Run(stripExt(fx.path), func(t *testing.T) {
			if !fixtureHasXPayment(fx.fixture) {
				t.Skip("fixture has no X-PAYMENT request — likely a free SIWX-only endpoint")
			}
			retry := findRetryExchange(t, fx.fixture)
			payment := getHeaderCanonical(retry.RequestHeaders, "X-Payment")
			if payment == "" {
				t.Fatalf("retry request had no X-PAYMENT header; headers=%v", retry.RequestHeaders)
			}
			raw, err := base64.StdEncoding.DecodeString(payment)
			if err != nil {
				t.Fatalf("base64 decode X-PAYMENT: %v", err)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(raw, &top); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			version := versionFromEnvelope(t, top)
			switch version {
			case X402ProtocolV1:
				assertEnvelopeKeys(t, top,
					[]string{"x402Version", "scheme", "network", "payload"},
					[]string{"accepted", "resource"})
			case X402ProtocolV2:
				assertEnvelopeKeys(t, top,
					[]string{"x402Version", "accepted", "payload"},
					[]string{"scheme", "network"})
			default:
				t.Fatalf("unrecognized envelope version %d", version)
			}
		})
	}
}

// TestFixtureReceiptsParseToCanonical replays the retry response's
// Payment-Response / X-PAYMENT-RESPONSE header through parseReceipt
// and verifies canonical fields land. Skipped for services that
// don't echo a receipt header at all (Browserbase) — settlement is
// proven by the 200 status alone there.
func TestFixtureReceiptsParseToCanonical(t *testing.T) {
	t.Parallel()
	for _, fx := range loadAllFixtures(t) {
		fx := fx
		t.Run(stripExt(fx.path), func(t *testing.T) {
			if !fixtureHasXPayment(fx.fixture) {
				t.Skip("fixture has no X-PAYMENT request — likely a free SIWX-only endpoint")
			}
			retry := findRetryExchange(t, fx.fixture)
			if retry.ResponseStatus != 200 {
				t.Skipf("retry status = %d, no receipt expected", retry.ResponseStatus)
			}
			value := readReceiptHeader(retry.ResponseHeaders)
			if value == "" {
				t.Skip("server did not echo a payment-response header; no receipt to parse")
			}
			receipt, err := parseReceipt(value)
			if err != nil {
				t.Fatalf("parse receipt: %v", err)
			}
			if receipt.Tx == "" {
				t.Fatalf("receipt.Tx empty (raw=%q)", value)
			}
			if receipt.Network == "" {
				t.Fatalf("receipt.Network empty")
			}
		})
	}
}

// --- helpers ----------------------------------------------------------------

type fixtureEntry struct {
	path    string
	fixture liveFixtureWire
}

func loadAllFixtures(t *testing.T) []fixtureEntry {
	t.Helper()
	matches, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatalf("glob testdata: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("no fixtures captured yet — run X402_LIVE=1 go test -run TestX402LiveSmoke")
	}
	out := make([]fixtureEntry, 0, len(matches))
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		var fx liveFixtureWire
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("decode %s: %v", m, err)
		}
		out = append(out, fixtureEntry{path: m, fixture: fx})
	}
	return out
}

// fixtureHas402 reports whether any exchange in the fixture
// returned a 402 challenge. SIWX-only endpoints (Venice /balance)
// return 200 directly and have no challenge to parse.
func fixtureHas402(fx liveFixtureWire) bool {
	for _, e := range fx.Exchanges {
		if e.ResponseStatus == 402 {
			return true
		}
	}
	return false
}

// fixtureHasXPayment reports whether any exchange in the fixture
// carried an X-Payment request header. A 402 alone with no retry
// (or no payment at all) means the test that needs the envelope
// should skip.
func fixtureHasXPayment(fx liveFixtureWire) bool {
	for _, e := range fx.Exchanges {
		if getHeaderCanonical(e.RequestHeaders, "X-Payment") != "" {
			return true
		}
	}
	return false
}

func findChallengeExchange(t *testing.T, fx liveFixtureWire) capturedExchangeOnDisk {
	t.Helper()
	for _, e := range fx.Exchanges {
		if e.ResponseStatus == 402 {
			return e
		}
	}
	t.Fatalf("fixture %s has no 402 exchange", fx.URL)
	return capturedExchangeOnDisk{}
}

func findRetryExchange(t *testing.T, fx liveFixtureWire) capturedExchangeOnDisk {
	t.Helper()
	for _, e := range fx.Exchanges {
		if getHeaderCanonical(e.RequestHeaders, "X-Payment") != "" {
			return e
		}
	}
	t.Fatalf("fixture %s has no X-PAYMENT request", fx.URL)
	return capturedExchangeOnDisk{}
}

// parseFixtureChallenge dispatches v1 vs v2 the same way readChallenge
// in transport.go does, except it works on a captured exchange rather
// than a live response.
func parseFixtureChallenge(e capturedExchangeOnDisk) (int, PaymentChallenge, error) {
	if hdr := getHeaderCanonical(e.ResponseHeaders, HeaderPaymentRequiredV2); hdr != "" {
		c, err := parseChallengeV2Header(hdr)
		return X402ProtocolV2, c, err
	}
	c, err := parseChallengeV1Body([]byte(e.ResponseBody))
	return X402ProtocolV1, c, err
}

func versionFromEnvelope(t *testing.T, top map[string]json.RawMessage) int {
	t.Helper()
	raw, ok := top["x402Version"]
	if !ok {
		t.Fatalf("envelope missing x402Version")
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode x402Version: %v", err)
	}
	return v
}

func assertEnvelopeKeys(t *testing.T, top map[string]json.RawMessage, mustHave []string, mustNotHave []string) {
	t.Helper()
	for _, k := range mustHave {
		if _, ok := top[k]; !ok {
			t.Fatalf("envelope missing required key %q (have %v)", k, keysOf(top))
		}
	}
	for _, k := range mustNotHave {
		if _, ok := top[k]; ok {
			t.Fatalf("envelope contains forbidden key %q (have %v)", k, keysOf(top))
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// getHeaderCanonical reads h[k] case-insensitively. http.Header on
// disk preserves canonical form (e.g. "Payment-Required") which
// matches http.Header.Get; this helper guards against future
// fixture changes that might use a different case.
func getHeaderCanonical(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	if v := h.Get(key); v != "" {
		return v
	}
	for k, vs := range h {
		if strings.EqualFold(k, key) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func stripExt(p string) string {
	base := filepath.Base(p)
	if i := strings.LastIndex(base, "."); i > 0 {
		return base[:i]
	}
	return base
}

// Compile-time check that fmt is used (kept for future debug
// formatting in this file).
var _ = fmt.Sprintf
