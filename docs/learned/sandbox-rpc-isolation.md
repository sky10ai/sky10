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

A typed-envelope agent bus carries every cross-trust-boundary
operation. Each envelope type is its own audited capability with its
own narrow handler file. Identity is stamped by the bus from the
authenticated transport, never from the payload. Validation is the
first non-trivial statement in every handler, enforced by an
arch-test. The payload is `json.RawMessage` until a handler
explicitly parses it — there is no auto-binding that would let a
handler treat its inputs as already-trusted.

The bus is **not** more secure than RPC at the cryptographic
primitive level. It is more secure at the *frame* level: the shape
of the code (raw bytes from the wire, identity injected by
infrastructure, one handler per file) keeps "trust this" code
visibly out of place. We lock the frame in structurally — not as a
review convention — because review conventions drift.

See [`docs/work/current/agent-bus/`](../work/current/agent-bus/) for
the full design and the rules that hold the frame in place.

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
