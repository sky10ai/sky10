---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Envelope Handler Discipline

The bus is **secure-by-default-frame**, not secure-by-cryptographic-
primitive. The frame only delivers if every handler is written in a
shape that makes "trust this" code visibly out of place. These six
rules turn the frame from a habit into a structural property of the
codebase.

If the rules feel pedantic, that's the point. They cost developer
keystrokes; they buy the property that the bus stays narrow as the
codebase grows.

## Rule 1 — No auto-binding. Payload is opaque bytes until a handler explicitly parses.

Handlers receive an `Envelope` whose `Payload` field is
`json.RawMessage`. The first non-trivial line of every handler is
unmarshaling and validating that payload. There is no framework that
auto-deserializes into a typed struct on the way in, because that's
exactly the affordance that makes RPC handlers feel trusted.

**Wrong:**

```go
func (h *X402Handler) SignPayment(ctx context.Context, env Envelope, params SignPaymentParams) (SignPaymentResult, error) {
    // params already feels validated. It is not.
    return h.sign(ctx, env.AgentID, params.ServiceID, params.Challenge, params.MaxPriceUSDC)
}
```

**Right:**

```go
func handleX402SignPayment(ctx context.Context, env Envelope) (json.RawMessage, error) {
    var p struct {
        ServiceID    string `json:"service_id"`
        Challenge    string `json:"challenge"`
        MaxPriceUSDC string `json:"max_price_usdc"`
    }
    if err := json.Unmarshal(env.Payload, &p); err != nil {
        return nil, ErrInvalidPayload
    }
    if err := validateServiceID(p.ServiceID); err != nil {
        return nil, fmt.Errorf("service_id: %w", err)
    }
    if err := validateChallenge(p.Challenge); err != nil {
        return nil, fmt.Errorf("challenge: %w", err)
    }
    quote, err := parseUSDC(p.MaxPriceUSDC)
    if err != nil {
        return nil, fmt.Errorf("max_price_usdc: %w", err)
    }
    return signX402Payment(ctx, env.AgentID, p.ServiceID, p.Challenge, quote)
}
```

## Rule 2 — Identity is bus infrastructure, never payload.

`agent_id`, `device_id`, `ts`, `nonce` are stamped by the bus from
the authenticated transport channel. Handlers read them from the
`Envelope` struct, never from the payload. There is no API in the
handler interface to read "the caller-claimed agent_id" because the
concept is forbidden.

The bus's deserialization step explicitly drops any top-level
`agent_id` or `device_id` keys from the wire JSON before stamping its
own values. An agent **cannot** lie about who it is — not because of
a check, but because the field they would lie in is not in the
struct.

## Rule 3 — One handler per file. One envelope type per handler.

Layout:

```
pkg/bus/envelopes/
├── chat_send.go              type "chat.send"
├── chat_receive.go           type "chat.receive"
├── messaging_send.go         type "messaging.send"
├── messaging_search.go       type "messaging.search"
├── messaging_arrived.go      type "messaging.message_arrived"
├── wallet_balance_subscribe.go
├── wallet_transfer.go
├── secrets_issue_token.go
├── x402_list_services.go
├── x402_sign_payment.go
├── x402_budget_status.go
└── home_*.go
```

No `messaging_handlers.go` with five exported functions. No `helpers.go`
imported by all of them. If two handlers want shared logic, that
logic lives in a non-handler package (`pkg/messaging/broker`,
`pkg/x402`, etc.) and each handler calls it explicitly. The handler
file is the entire surface for that one envelope type.

## Rule 4 — Mandatory metadata at registration.

Registering an envelope type is a deliberate code change that must
declare:

```go
bus.Register(bus.TypeSpec{
    Name:           "x402.sign_payment",
    Direction:      bus.RequestResponse,           // RequestResponse | Push | Subscribe
    MaxPayloadSize: 4 * 1024,                      // bytes
    RateLimit:      bus.RateLimit{PerAgent: 10, Burst: 20, Window: time.Minute},
    NonceWindow:    10 * time.Minute,
    AuditLevel:     bus.AuditFull,                 // None | Headers | Full
    Handler:        handleX402SignPayment,
})
```

There is **no default constructor** that fills missing fields with
"reasonable defaults." A new envelope type that omits any of these
fails to compile (zero values on required fields are caught by the
registration code at `init`-time and panic loudly in the daemon's
startup, before the bus accepts traffic).

This forces the question "what's the rate limit?" at the moment the
envelope is added — not later, when something goes wrong in
production.

## Rule 5 — Validation must be the first non-trivial statement.

After unmarshaling, the next statements must validate. Business logic
is forbidden until validation has passed. This is enforced by an
arch-test:

```go
// pkg/bus/envelopes/arch_test.go
func TestHandlersValidateBeforeUse(t *testing.T) {
    for _, file := range envelopeHandlerFiles() {
        ast := parseGo(t, file)
        ensureFirstNonTrivialIsValidation(t, ast)
    }
}
```

The arch-test is intentionally simple-minded: it walks the AST of
each handler and confirms that the early statements consist of
unmarshal calls, validate-prefixed calls, or returns. Any reach into
business logic before validation completes fails the test. The check
is conservative — it catches the obvious mistake, not every edge
case — but it makes the wrong shape visibly fail in CI.

## Rule 6 — Mandatory header comment per handler file.

Top of every handler file:

```go
// envelope: x402.sign_payment
//
// UNTRUSTED INPUT FROM A GUEST AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are bus-stamped and trustworthy.
```

This is performative — it doesn't change any behavior. It sets the
mental frame of every reader, including reviewers and AI coding
agents. Removing it is a review red flag.

## Why these rules can't be defaults

A "secure default" is one that does the right thing if you don't
think about it. None of these rules can be defaults:

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

The rules require active enforcement because the *defaults* of every
contributing tool — Go's idioms, JSON-RPC frameworks, code review
without checklists — push toward the wrong shape. The arch-test is
the load-bearing piece; the comments and conventions are the cultural
piece. Both are necessary.
