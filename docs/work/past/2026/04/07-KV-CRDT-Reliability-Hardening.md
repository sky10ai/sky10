---
created: 2026-04-07
model: gpt-5.4
---

# KV CRDT Reliability Hardening

Reworked `pkg/kv` so private-network KV behaves like a much more
reliable P2P primitive instead of a lucky snapshot exchange that mostly
worked when clocks, baselines, and reconnect timing happened to line up.
This work introduced causal metadata, replicated delete state, bounded
anti-entropy, and explicit sync-health surfaces so offline devices can
rejoin and converge more defensibly without depending on any durable
backend.

This work shipped incrementally across `v0.41.0` through `v0.41.3`.

## Why

The old KV model had three structural weaknesses:

- merge authority was effectively wall-clock-first, which is the wrong
  source of truth for distributed writes
- delete propagation depended too much on baseline diffs and luck, so a
  reconnecting peer could miss delete intent if the right prior state
  was gone
- sync correctness depended on one lucky push after reconnect instead of
  bounded anti-entropy and clear health reporting

That was not good enough for `sky10`'s actual requirement. P2P is the
only substrate that can be assumed. Devices can be offline for long
periods, come back in any order, and still need to converge with high
confidence.

## What Changed

### 1. KV mutations now carry causal metadata

KV entries now carry per-device actor, counter, and causal-context
metadata instead of relying only on timestamps and local sequence.
Actor identity is tied to the device public key rather than the shared
private-network identity.

That matters because one private network can have many devices. Causal
metadata needs to answer "which device wrote this, and what had it seen"
instead of pretending the whole network is one actor.

### 2. Snapshots now replicate tombstones and causal summaries

The replicated snapshot format now carries explicit tombstones plus a
causal summary. Delete propagation no longer depends only on a lucky
baseline surviving on the receiving peer.

That matters because in a P2P-only system, absence alone is too weak.
When an offline device comes back, the latest replicated state needs to
be self-sufficient enough to teach it both visible values and delete
intent.

### 3. Merge now prefers causality before clocks

Merge logic now prefers causal ordering when it is known and only falls
back to the legacy clock rules for truly concurrent writes.

That matters because wall clock can impose a fake total order on writes
that were actually concurrent or causally related. The closer merge gets
to causal truth, the more trustworthy reconnect behavior becomes.

### 4. P2P sync is now summary-first anti-entropy

Peers now exchange a namespace summary first, then ship only the state
delta the other side is missing instead of always blasting a whole
snapshot.

That matters because reconnect should be retry-safe and efficient. When
two peers are already close, they should exchange just enough state to
heal, not restart from scratch every time.

### 5. KV now heals periodically even without new writes

The store now runs periodic bounded anti-entropy in the background,
including the real daemon lifecycle where the store can start before the
P2P sync layer is attached.

That matters because convergence should not depend on a fresh mutation.
If a reconnect happens after a missed push or a temporary stream
failure, the next background round should still heal the namespace.

### 6. Sync health is now visible and namespace drift fails loudly

KV now exposes readiness, sync state, peer counts, NSID, and the last
sync error through `skykv.status`, the CLI, logs, and the web KV page.
Namespace mismatches are surfaced explicitly instead of silently
drifting.

That matters because operator trust collapses when distributed state is
quietly wrong. When two devices cannot sync, the product now says so.

### 7. Fresh-join KV startup became deterministic

Fresh private-network joins exposed one more reliability bug: the daemon
could complete startup and begin bootstrap work before registering the
KV sync protocol. That left newly joined peers connected but unable to
negotiate KV sync until later retries or another restart.

That was fixed by registering the KV sync protocol before blocking
bootstrap work starts.

## Test Coverage Added

Coverage now includes:

- causal ordering beating bad clocks
- tombstone round-trip and delete healing without a prior baseline
- summary-first anti-entropy over real libp2p streams
- periodic bounded anti-entropy without write-triggered pushes
- DHT-backed KV reconnect coverage in an isolated deterministic test
  overlay
- fresh-join KV startup regression coverage

That matters because this work is all about restarts, reconnects,
duplicates, and timing edges. If the tests cannot exercise those cases,
the reliability claims are weak.

## User-Facing Outcomes

- KV set and delete now converge more reliably after reconnect
- a missed push no longer leaves the namespace stale forever
- delete propagation is more defensible because tombstones are
  replicated state
- KV reports when the namespace is unhealthy instead of failing quietly
- fresh private-network joins no longer miss KV protocol registration
  during startup

## Important Releases In This Series

- `v0.41.0`: causal metadata, tombstones, causal summaries, summary-first
  anti-entropy, and periodic bounded anti-entropy
- `v0.41.1`: loud KV sync-health surfaces and namespace mismatch
  reporting
- `v0.41.3`: fix fresh-join KV protocol registration ordering

## What This Did Not Solve

This work materially improved KV reliability, but it did not make fresh
private-network join feel immediate. The join/bootstrap follow-on is
archived separately in:

- [Invite & Join Bootstrap Hardening](07-Invite-Join-Bootstrap-Hardening.md)

## Remaining Gaps

KV is more reliable now, but it is still not a finished "perfect CRDT"
story. Remaining medium-term work includes:

- defining causal tombstone GC
- deciding whether to stay state-based or move further toward op/delta
  exchange
- tightening concurrency semantics for truly concurrent writes

That work is now better framed as follow-on CRDT evolution, not as the
same urgent reliability problem that blocked everyday private-network KV
sync.
