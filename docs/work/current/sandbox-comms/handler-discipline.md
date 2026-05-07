---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Envelope Handler Discipline

Sandbox comms is **secure-by-default-frame**: handlers receive raw
bytes from the wire and the *shape* of the code makes "trust this"
visibly out of place. The frame only delivers if every handler is
written in a shape that enforces it. These six rules turn the frame
from a habit into a structural property.

The per-intent endpoint split (one URL per capability) gives this
its bite: each capability is in its own subpackage with its own
handlers, so accidental cross-capability leakage is structurally
prevented. The discipline rules below apply *within* a capability
to keep each handler narrow.

## Rule 1 — No auto-binding. Payload is opaque bytes until a handler explicitly parses.

Handlers receive an `Envelope` whose `Payload` field is
`json.RawMessage`. The first non-trivial line of every handler is
unmarshaling and validating that payload. There is no framework
that auto-deserializes into a typed struct on the way in, because
that's exactly the affordance that makes RPC handlers feel trusted.

**Wrong:**

```go
func (h *X402Handler) ServiceCall(ctx context.Context, env Envelope, params ServiceCallParams) (ServiceCallResult, error) {
    // params already feels validated. It is not.
    return h.x402.Call(ctx, env.AgentID, params.ServiceID, params.Path, params.Body, params.MaxPriceUSDC)
}
```

**Right:**

```go
func handleServiceCall(ctx context.Context, env Envelope) (json.RawMessage, error) {
    var p struct {
        ServiceID    string          `json:"service_id"`
        Path         string          `json:"path"`
        Body         json.RawMessage `json:"body"`
        MaxPriceUSDC string          `json:"max_price_usdc"`
    }
    if err := json.Unmarshal(env.Payload, &p); err != nil {
        return nil, ErrInvalidPayload
    }
    if err := validateServiceID(p.ServiceID); err != nil {
        return nil, fmt.Errorf("service_id: %w", err)
    }
    if err := validatePath(p.Path); err != nil {
        return nil, fmt.Errorf("path: %w", err)
    }
    quote, err := parseUSDC(p.MaxPriceUSDC)
    if err != nil {
        return nil, fmt.Errorf("max_price_usdc: %w", err)
    }
    return callX402Service(ctx, env.AgentID, p.ServiceID, p.Path, p.Body, quote)
}
```

## Rule 2 — Identity is plumbing infrastructure, never payload.

`agent_id`, `device_id`, `ts`, `nonce` are stamped by the
plumbing from the authenticated transport. Handlers read them
from the `Envelope` struct, never from the payload. There is no
API in the handler interface to read "the caller-claimed
agent_id" because the concept is forbidden.

The plumbing's deserialization step explicitly drops any top-
level `agent_id` or `device_id` keys from the wire JSON before
stamping its own values. An agent **cannot** lie about who it
is — not because of a check, but because the field they would
lie in is not in the struct.

## Rule 3 — One handler per file. One envelope type per handler.

Layout within a capability:

```
pkg/sandbox/comms/x402/
├── endpoint.go            registers /comms/metered-services/ws
├── list_services.go       envelope x402.list_services
├── service_call.go        envelope x402.service_call
├── budget_status.go       envelope x402.budget_status
└── changes.go             envelope x402.changes (push)
```

No `handlers.go` with five exported functions. No `helpers.go`
imported by all of them. If two handlers want shared logic, it
lives in a non-handler package (`pkg/x402` for x402 business
logic) and each handler calls it explicitly.

This rule plus the per-intent endpoint split (Rule 0, implicit)
means a `wallet.transfer` handler and an `x402.service_call`
handler don't share even an import path, much less a function.

## Rule 4 — Mandatory metadata at registration.

Registering an envelope type is a deliberate code change that
must declare:

```go
endpoint.Register(comms.TypeSpec{
    Name:           "x402.service_call",
    Direction:      comms.RequestResponse,
    MaxPayloadSize: 64 * 1024,
    RateLimit:      comms.RateLimit{PerAgent: 10, Burst: 20, Window: time.Minute},
    NonceWindow:    10 * time.Minute,
    AuditLevel:     comms.AuditFull,
    Handler:        handleServiceCall,
})
```

There is **no default constructor** that fills missing fields with
"reasonable defaults." A new envelope type that omits any required
field fails at `init`-time and panics loudly in daemon startup,
before the endpoint accepts traffic.

This forces "what's the rate limit?" to be answered at the moment
the envelope is added — not later, in a postmortem.

## Rule 5 — Validation must be the first non-trivial statement.

After unmarshaling, the next statements must validate. Business
logic is forbidden until validation has passed. Enforced by an
arch-test:

```go
// pkg/sandbox/comms/arch_test.go
func TestHandlersValidateBeforeUse(t *testing.T) {
    for _, file := range envelopeHandlerFiles() {
        ast := parseGo(t, file)
        ensureFirstNonTrivialIsValidation(t, ast)
    }
}
```

The arch-test walks the AST of each handler and confirms that the
early statements consist of unmarshal calls, validate-prefixed
calls, or returns. Any reach into business logic before validation
completes fails the test. The check is conservative — it catches
the obvious mistake, not every edge case — but it makes the wrong
shape visibly fail in CI.

The arch-test scans all subdirectories under `pkg/sandbox/comms/`,
so adding a new capability subpackage automatically gets covered.

## Rule 6 — Mandatory header comment per handler file.

Top of every handler file:

```go
// envelope: x402.service_call
//
// UNTRUSTED INPUT FROM A SANDBOXED AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are plumbing-stamped and trustworthy.
```

Performative — doesn't change behavior. Sets the mental frame of
every reader, including reviewers and AI coding agents. Removing
it is a review red flag.

## Why these rules can't be defaults

A "secure default" does the right thing if you don't think about
it. None of these rules can be defaults:

- **Auto-binding** is a nice default for internal RPC. It would
  silently undermine rule 1.
- **Identity claimed by caller** is the default in most JSON-RPC
  implementations. It would silently undermine rule 2.
- **Multiple handlers per file** is the default in Go's idiom. It
  would silently undermine rule 3.
- **Reasonable defaults for rate limits** would silently undermine
  rule 4.
- **No arch-test** would silently undermine rule 5.
- **No required header comment** would silently undermine rule 6.

The rules require active enforcement because the *defaults* of
every contributing tool — Go's idioms, JSON-RPC frameworks, code
review without checklists — push toward the wrong shape. The
arch-test is the load-bearing piece; the comments and conventions
are the cultural piece. Both are necessary.

## Per-intent endpoints carry the structural side

The discipline rules keep each handler narrow. The per-intent
endpoint split keeps each capability isolated from the others.
The two layers together make the shape "untrusted bytes from the
wire, processed by a single-purpose handler in a single-purpose
package, behind a single-purpose URL" — the shape itself
encourages the right code.

If you find yourself wanting to share helpers across capabilities,
that's the signal to move that logic into a non-comms package
(e.g. `pkg/x402`, `pkg/wallet`) and have both capabilities call
into it. The comms package is glue, not business logic.
