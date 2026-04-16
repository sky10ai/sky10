---
created: 2026-04-16
model: gpt-5.4
---

# Live Relay Productization And Health Clarity

This entry covers the follow-up network work that landed in `de8d586`
(`fix(network): prefer live relay retries before nostr`), `5344481`
(`fix(link): require managed live relays by default`), and `8c58b4f`
(`fix(web): clarify transport vs coordination health`).

This was the concrete follow-on to
[`12-Private-Network-Robustness.md`](12-Private-Network-Robustness.md). The
earlier robustness series had already added the structural pieces for managed
relays, relay-aware dialing, mailbox durability, and split health signals.
What remained was the product gap between "the codebase knows about relays" and
"operators actually get a reliable P2P-first network with understandable
degraded states."

## Why

After the earlier robustness work, five practical issues were still visible:

- live network delivery still behaved too much like one coarse `skylink`
  attempt followed by Nostr fallback instead of an explicit direct-then-relay
  live transport ladder
- sticky home-relay preference existed, but normal resolver ranking was not
  threading that preference through the dial order
- mailbox `queued` and `handed_off` events still drove global private-network
  convergence instead of targeted retry work, which created avoidable churn
- network mode still allowed a node to start without any operator-managed live
  relay tier, which meant "relay-aware" could still collapse into "direct, then
  Nostr"
- the health model already separated transport, delivery, and coordination, but
  the Network page still did not make the distinction obvious enough for an
  operator reading the dashboard

This follow-up closed those five issues without reopening the broader network
architecture.

## What Shipped

### 1. Live delivery now tries direct, then live relay, then Nostr

[`pkg/link/discovery.go`](../../../../../pkg/link/discovery.go) now splits
resolved peer addresses into explicit connect phases:

- direct multiaddrs first
- relay `/p2p-circuit` multiaddrs second

`Resolver.connectResolvedPeer()` walks those phases in order instead of handing
one mixed `AddrInfo` to `host.Connect()` and hoping libp2p picks the right path.

[`pkg/agent/router.go`](../../../../../pkg/agent/router.go) was updated so
`sendNetworkMailboxLive()` uses `Resolver.ConnectPeer()` for the live attempt,
then only hands off to durable Nostr mailbox transport if the explicit live
transport ladder still fails.

That made the intended transport policy real:

- direct libp2p first
- live libp2p relay second
- durable Nostr handoff last

### 2. Resolver ranking now respects the sticky live relay preference

The sticky home-relay logic from the live relay tracker already existed, but
normal resolver ranking still was not consuming it.

[`pkg/link/discovery.go`](../../../../../pkg/link/discovery.go) now threads
`node.liveRelayPreference()` into
`PrioritizeAddrInfoWithRelayPreference(...)` so relay addrs are ranked with the
current preferred relay in mind instead of being treated as generic relay
candidates.

This matters because a "managed relay tier" is not just "some relay address is
present." It also needs stable relay selection so the node does not bounce
across relays unnecessarily.

### 3. Mailbox updates now trigger targeted retries instead of global convergence

The private-network convergence manager in
[`commands/serve.go`](../../../../../commands/serve.go) was doing too much work
in response to mailbox state:

- republishing discovery state
- reconnect/autoconnect work
- outbox draining

That was too broad for mailbox `queued` / `handed_off` events.

This follow-up introduced a dedicated mailbox retry manager in
[`commands/serve_mailbox_retry.go`](../../../../../commands/serve_mailbox_retry.go)
and rewired the mailbox observer in
[`commands/serve.go`](../../../../../commands/serve.go) so mailbox updates now
fan into targeted retry reasons:

- private mailbox retry by device
- sky10 network retry by route address
- queue-offer retry by item id

This removed the bad control-loop coupling where mailbox backpressure could keep
re-triggering global private-network convergence.

### 4. Network mode now requires managed live relays by default

The live relay tier already existed in the codebase, but startup still treated
"no managed live relay peers at all" as normal.

[`commands/serve_network.go`](../../../../../commands/serve_network.go) now
separates:

- managed live relays from config / `--link-relay`
- cached relay bootstrap entries recovered from disk

and validates them explicitly.

[`commands/serve.go`](../../../../../commands/serve.go) now fails startup by
default unless at least one managed live relay is configured via:

- `--link-relay`
- `config.json` `link_relays`

Cached bootstrap relays alone no longer count as a sufficient relay tier.

For development or intentionally direct-only runs, there is now an explicit
escape hatch:

- `--allow-no-link-relay`

That changed the product stance from "relay support exists if you happen to
configure it correctly" to "network mode requires a real degraded-but-live
relay path unless you opt out on purpose."

### 5. The Network page now says when coordination is degraded but live transport is healthy

The backend health model in
[`pkg/link/health.go`](../../../../../pkg/link/health.go) already had distinct
fields for:

- `transport_degraded_reason`
- `delivery_degraded_reason`
- `coordination_degraded_reason`

The remaining problem was presentation.

[`web/src/pages/Network.tsx`](../../../../../web/src/pages/Network.tsx) now:

- adds a top summary banner that explicitly says when live transport is still
  healthy
- translates raw degraded-reason codes into operator-facing text
- reframes Nostr trouble as coordination trouble rather than generic network
  trouble
- renames the Nostr panel to `Nostr Coordination`
- softens relay-row language to `Live`, `Partial`, `Down`, and `Idle`

This was not a protocol change. It was the UI change needed to make the already
separate health model legible under real degraded conditions.

## Regression Coverage And Validation

This follow-up added or extended regression coverage for the new behavior:

- [`pkg/link/dial_strategy_test.go`](../../../../../pkg/link/dial_strategy_test.go)
  covers live relay preference in resolver ranking and direct-before-relay
  connect phase ordering
- [`commands/serve_mailbox_retry_test.go`](../../../../../commands/serve_mailbox_retry_test.go)
  covers mailbox retry trigger routing for private delivery, sky10 network
  delivery, queue item retry, and ignored updates
- [`commands/serve_network_test.go`](../../../../../commands/serve_network_test.go)
  covers managed live relay resolution, cached-only relay rejection, and the
  explicit opt-out path

Validation run for the code changes included:

- `make check`
- `go test ./... -count=1`
- `make build-web`

There is still no dedicated frontend component-test harness around the Network
page wording; the frontend validation for issue 5 is build-level rather than
page-level unit coverage.

## Remaining Gaps

This follow-up closed the five concrete issues, but it did not finish every
relay question.

The main remaining gaps are:

- a true end-to-end regression that forces direct live transport to fail,
  proves live libp2p relay succeeds, and proves Nostr stays unused in that
  case
- more real-device validation on ugly NATs and unstable networks
- a later decision on whether stable operator-managed relays are enough, or
  whether a dedicated `skyrelay` service is still worth shipping

That means the system is now materially more honest and more reliable, but the
managed relay tier still needs continued real-world validation rather than being
treated as finished forever.
