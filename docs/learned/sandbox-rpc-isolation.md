---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Sandbox RPC Isolation — Why We Don't Expose Host RPC to Guests

## The lesson

Do not let an agent inside a Lima sandbox reach the host daemon's RPC
surface, even behind an allowlist. The very fact that the surface is
reachable is the attack surface — once present, it tends to grow
teeth, and a single bug (parser, auth, scope-injection) compromises
all of it. Cross-trust-boundary capabilities go through the typed
agent bus, not through generic RPC.

## The history

An earlier iteration of sky10 had sandboxed agents call host RPC
directly — same socket, same handlers, "just" with different
identity. It was simple to reason about. It also collapsed sandbox
isolation: an agent in VM A could call `sandbox.list`, `agent.list`,
`skylink.call` to other peers, `identity.deviceRemove`, etc. Once you
have access to `sandbox.list` you can effectively operate against any
other VM. Sandboxing became theatre — VM overhead with none of the
isolation it implies.

We pulled back from that model. The intermediate state we landed in —
where the guest sky10 daemon registers handlers but most of them
silently return empty data because their host-resident dependencies
(wallet, secrets, broker connections) live elsewhere — is also wrong;
it's just less actively dangerous. An agent calling
`messaging.searchMessages` from inside the guest gets zero matches
not because of policy but because the guest broker is empty. That
quietly broken state is what motivated the agent bus design.

## Why allowlist + policy + scope injection isn't enough

The natural reach-for fix is "allowlist of forwardable methods +
per-agent policy + bus stamps the requester identity." On primitive
security terms it's defensible. In practice it fails for a frame
reason: the *default mental model* when you write an RPC method
handler is "my caller is authenticated, my args are typed, I can
trust them." Skepticism has to be added on top, and it leaks out as
the codebase grows. The list drifts upward. Method composition
discovers paths the original allowlist designer didn't consider.

## What we do instead

Per-intent websocket endpoints under `pkg/sandbox/comms/`, one
per capability, each with its own URL path, its own subpackage,
and its own envelope handlers. The Go HTTP mux routes by path —
the URL is the dispatch. There is no central bus, no shared
dispatcher, no allowlist that grows over time.

Each handler receives raw bytes from the wire (`json.RawMessage`)
and must validate explicitly. Identity is stamped by the
plumbing from the authenticated transport, never from the
payload. Validation is the first non-trivial statement in every
handler, enforced by an arch-test. There is no auto-binding that
would let a handler treat its inputs as already-trusted.

This is more secure at two levels:

- **Frame.** The shape of the code (raw bytes from the wire,
  identity injected by infrastructure, one handler per file)
  keeps "trust this" code visibly out of place.
- **Blast radius.** A capability bug in `/comms/wallet/ws`
  cannot affect `/comms/metered-services/ws` because the code paths don't
  intersect. There is no shared dispatcher to compromise.

See [`docs/work/current/sandbox-comms/`](../work/current/sandbox-comms/)
for the full design and the rules that hold the frame in place.

## Why per-intent and not one bus with discipline rules

We considered a single endpoint that multiplexes envelope types,
guarded by strict handler discipline rules. It is structurally
worse than per-intent endpoints for a non-obvious reason:

A single endpoint drifts back to RPC over time. Adding capability
N+1 to an existing bus is a small, low-friction change that
always feels small; six months in, the bus is functionally the
host's RPC surface in a different costume. Discipline rules
notwithstanding, because review attention drifts and "just one
more envelope type" never trips a threshold.

Per-intent endpoints put friction in the right place: a new
capability requires a new URL, a new subpackage, a new endpoint
registration. That friction is the feature. It forces the
question "is this really its own capability or a stretch of an
existing one?" to be answered every time, not just at design
review.

Discipline rules can't substitute for structural drift
prevention. The per-intent split *is* the drift prevention. The
discipline rules are the additional defense within each
capability's surface.

## When this lesson applies

Any time someone proposes "let the guest just call host RPC for X"
or "let's expose this method to sandboxed agents directly," remember:

- The reachability of a generic surface is the attack surface.
- "Just one method" turns into thirty over time.
- Frame defaults beat primitive checks for codebases that grow.

The right answer is always "add a narrow envelope type with its own
handler, audit, quota, and scope filter." If the proposed work is too
big to express as a narrow envelope, it is probably too big to expose
to a sandboxed agent at all.
