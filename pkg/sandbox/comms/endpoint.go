package comms

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Endpoint is one capability's per-intent websocket endpoint. Construct
// it with NewEndpoint, declare every envelope type it accepts via
// Register, and mount its Handler() at a unique URL on the daemon's
// HTTP mux (e.g. /bridge/metered-services/ws).
//
// Endpoints are intentionally not generic: by design each capability
// owns one Endpoint with a small registered type set, and there is no
// mechanism to compose Endpoints into a larger surface. The URL path
// is the dispatch key; the Endpoint is the policy boundary for one
// capability.
type Endpoint struct {
	name             string
	types            map[string]TypeSpec
	identityResolver IdentityResolver
	audit            AuditWriter
	replay           *ReplayStore
	quota            *QuotaStore
	logger           *slog.Logger
	clock            func() time.Time
	started          atomic.Bool
}

// Option configures an Endpoint at construction. See WithAuditWriter,
// WithLogger, WithReplayStore, WithQuotaStore, WithClock.
type Option func(*Endpoint)

// NewEndpoint constructs an Endpoint with a required name (used in
// logs and audit lines) and a required IdentityResolver. The name and
// resolver have no defaults — passing the empty string or a nil
// resolver panics.
//
// Sensible defaults are filled in for Audit (NoopAuditWriter), Logger
// (a discard logger), ReplayStore (2-minute skew, 10-minute max nonce
// window), QuotaStore (in-memory token buckets), and Clock (time.Now).
// Tests typically override these via Options.
func NewEndpoint(name string, resolver IdentityResolver, opts ...Option) *Endpoint {
	if name == "" {
		panic("comms: NewEndpoint requires a non-empty name")
	}
	if resolver == nil {
		panic("comms: NewEndpoint requires a non-nil IdentityResolver")
	}
	e := &Endpoint{
		name:             name,
		types:            make(map[string]TypeSpec),
		identityResolver: resolver,
		clock:            time.Now,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.audit == nil {
		e.audit = NoopAuditWriter{}
	}
	if e.logger == nil {
		e.logger = slog.New(slog.DiscardHandler)
	}
	if e.replay == nil {
		e.replay = NewReplayStore(2*time.Minute, 10*time.Minute, e.clock)
	}
	if e.quota == nil {
		e.quota = NewQuotaStore(e.clock)
	}
	return e
}

// WithAuditWriter sets the AuditWriter the endpoint emits to.
func WithAuditWriter(w AuditWriter) Option {
	return func(e *Endpoint) { e.audit = w }
}

// WithLogger sets the slog logger used for diagnostic events.
func WithLogger(l *slog.Logger) Option {
	return func(e *Endpoint) { e.logger = l }
}

// WithReplayStore overrides the default ReplayStore. Useful when
// multiple endpoints want to share replay state, or in tests with a
// fake clock.
func WithReplayStore(s *ReplayStore) Option {
	return func(e *Endpoint) { e.replay = s }
}

// WithQuotaStore overrides the default QuotaStore. Useful when sharing
// quota state across endpoints, or in tests.
func WithQuotaStore(s *QuotaStore) Option {
	return func(e *Endpoint) { e.quota = s }
}

// WithClock overrides the time source. Useful in tests; in production
// time.Now is the right answer.
func WithClock(now func() time.Time) Option {
	return func(e *Endpoint) {
		if now != nil {
			e.clock = now
		}
	}
}

// Register declares one envelope type the endpoint accepts. Panics
// (with a clear message) if the spec is missing required fields,
// duplicates an already-registered type, or is called after Handler
// has been invoked. Registration is a daemon-startup activity; runtime
// registration is a misuse pattern that this package refuses.
func (e *Endpoint) Register(spec TypeSpec) {
	if e.started.Load() {
		panic(fmt.Sprintf("comms: cannot Register %q after Endpoint %q has started serving", spec.Name, e.name))
	}
	spec.validate()
	if _, exists := e.types[spec.Name]; exists {
		panic(fmt.Sprintf("comms: duplicate TypeSpec %q on Endpoint %q", spec.Name, e.name))
	}
	e.types[spec.Name] = spec
}

// Handler returns the http.HandlerFunc the daemon mounts on its mux.
// First call freezes the type registry; subsequent Register calls
// panic.
func (e *Endpoint) Handler() http.HandlerFunc {
	e.started.Store(true)
	return e.serveHTTP
}

// Name returns the endpoint name for use in logs and audit lines.
func (e *Endpoint) Name() string {
	return e.name
}

func (e *Endpoint) serveHTTP(w http.ResponseWriter, r *http.Request) {
	agentID, deviceID, err := e.identityResolver(r)
	if err != nil {
		status := http.StatusUnauthorized
		if !errors.Is(err, ErrUnauthenticated) {
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}
	if agentID == "" {
		http.Error(w, "comms: identity resolver returned empty agent_id", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
	if err != nil {
		// websocket.Accept already wrote the HTTP error response.
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	e.runConnection(r.Context(), conn, agentID, deviceID)
}
