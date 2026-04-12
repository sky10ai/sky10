# Tailscale And Headscale Robustness Study

Decided: 2026-04-12

## Scope

Re-studied the Tailscale and headscale code paths that actually make the
network feel reliable, not just the public architecture summaries.

Primary sources inspected:

- Tailscale:
  - `tailcfg.MapResponse` session, keepalive, and patch fields
  - `control/controlclient/direct.go` and `control/controlclient/map.go`
  - `net/netcheck/netcheck.go`
  - `net/dnsfallback/dnsfallback.go`
  - `wgengine/magicsock/magicsock.go`
- headscale:
  - `config-example.yaml`
  - `hscontrol/poll.go`
  - `hscontrol/app.go`

## What The Code Actually Shows

### 1. The control stream is stateful and incremental

Tailscale does not treat control as periodic refresh. It keeps a long-lived map
session with:

- session identity and sequence (`MapSessionHandle`, `Seq`)
- explicit keepalive frames
- incremental patches (`PeersChangedPatch`, `PeerSeenChange`,
  `OnlineChange`)
- watchdogs around stalled map streams
- in-memory session state that is patched instead of rebuilt on every change

headscale mirrors this with long-poll sessions, keepalive writes, and a
batcher that pushes `MapResponse` updates to connected clients.

Lesson for `sky10`:

- if we ever add `skycoord`, it must be a resumable watch stream with sequence
  and patch semantics, not another periodic "refresh everything" service
- event-driven convergence was the right move, but Nostr subscriptions still
  do not provide the same "resume from sequence N" semantics

### 2. Relay is a first-class live transport tier

The Tailscale code is stricter than our previous plan:

- DERP is not treated like a side fallback; it is an explicit transport tier
- relay choice is sticky enough to avoid flapping
- relay data is visible in status, metrics, and `NetInfo`
- newer Tailscale code also tracks peer relays, not just DERP

headscale also reflects this architecture:

- it can run an embedded DERP plus STUN server
- it distributes DERP maps to clients
- it periodically refreshes the DERP map and pushes changes

Lesson for `sky10`:

- `mailbox` and Nostr relay/dropbox do not close the live-transport gap
- we still need a first-class relay layer for live RPC traffic on bad NATs
- that relay tier needs its own configuration, health, and selection policy

### 3. Relay bootstrap state is cached on disk

Tailscale keeps a static DERP fallback map compiled into the binary and merges
it with a cached on-disk DERP map. That lets relay bootstrap keep working even
when control is down.

Lesson for `sky10`:

- relay configuration cannot live only in ephemeral runtime state
- we need cached relay bootstrap state that survives restart and partial
  coordinator loss
- invites and config should be able to seed that cache

### 4. Netcheck is continuous input to path selection, not a one-shot report

The Tailscale `netcheck` code does more than discover a public address:

- full vs incremental probe plans
- forced inclusion of the current home relay to avoid relay flapping
- history-aware relay choice
- explicit reporting of UDP, port mapping, mapping variance, and preferred
  relay

Lesson for `sky10`:

- STUN was still worth adding, but the next step is relay-aware path memory
- path scoring should remember relay success and keep a sticky home relay
  unless there is strong evidence to move

### 5. Online/offline state is damped

headscale delays disconnect handling briefly to avoid reconnect flapping.
Tailscale similarly separates keepalive/watchdog concerns from actual network
state mutations.

Lesson for `sky10`:

- convergence logic should continue to prefer bounded retries and damped state
  transitions over instant offline/online flips
- relay and coordinator health should expose degraded states, not only binary
  success/failure

## Implications For sky10

The earlier conclusion that little remained beyond real-world validation was
too optimistic.

We were right about:

- separating transport from mailbox delivery
- using STUN as a diagnostic and transport input
- using Nostr for coordination, receipts, and async fallback
- making convergence event-driven

We were wrong to treat that as nearly equivalent to Tailscale/headscale
robustness.

The main missing piece is:

- a managed live relay tier with sticky selection, cached bootstrap state, and
  explicit operator-facing health

That means:

- `Nostr` remains coordination plus durable async relay for mailbox/public
  queue flows
- libp2p direct remains the primary live transport
- a dedicated relay tier is still needed for dependable live traffic on ugly
  networks
- the optional coordinator decision belongs after that, not before it

## What To Build Next

1. Add a first-class live relay milestone to the private-network plan.
2. Treat relay selection as path memory, not just another candidate address.
3. Add cached relay bootstrap state that survives restart.
4. If `skycoord` is ever added, require watch-stream resume and patch updates.

## Sources

- Tailscale control/data plane docs:
  https://tailscale.com/docs/concepts/control-data-planes
- Tailscale connection types:
  https://tailscale.com/docs/reference/connection-types
- Tailscale DERP docs:
  https://tailscale.com/docs/reference/derp-servers
- Tailscale peer relay docs:
  https://tailscale.com/docs/features/peer-relay
- Tailscale source:
  https://github.com/tailscale/tailscale
- headscale source:
  https://github.com/juanfont/headscale
