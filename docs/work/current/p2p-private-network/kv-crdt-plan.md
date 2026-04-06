---
created: 2026-04-06
updated: 2026-04-06
model: gpt-5.4
---

# KV CRDT Plan

The completed private-network discovery and reliability work has been
archived in:

- [Private Network Discovery Hardening](/Users/bf/.baton/worktrees/sky10/debug-agent-key-instability/docs/work/past/2026/04/06-Private-Network-Discovery-Hardening.md)

This document remains in `docs/work/current/` because KV still needs a
future-facing CRDT redesign beyond the discovery work that already
shipped.

## Goal

Make `pkg/kv` a private-network primitive that converges reliably across
partitions, restarts, duplicates, reordering, and long offline periods.

This document separates:

- **v1 hardening**: improve confidence in the current snapshot/LWW design
- **v2 redesign**: move to a full CRDT model with explicit causality and
  tombstones

As of `2026-04-06`, the first causal-metadata checkpoint has landed:

- KV entries now carry per-device actor/counter/context metadata
- actor identity is tied to the device public key, not the shared private-network
  identity
- snapshots now replicate tombstones and a causal summary, so delete propagation
  no longer depends only on a lucky stored baseline
- merge order now prefers causal ordering when available and falls back to the
  legacy clock only for concurrent writes
- P2P anti-entropy now exchanges summaries first and sends only missing state
  deltas instead of always blasting a full snapshot
- the store now runs periodic bounded anti-entropy in the background, including
  the real daemon lifecycle where `Run()` starts before `SetP2PSync()`

## Current State

Today KV is:

- a local JSONL op log on disk
- a materialized LWW snapshot in memory
- snapshot exchange over direct skylink/libp2p streams
- replicated tombstones plus baseline-assisted legacy delete inference

Relevant code:

- `pkg/kv/local.go`
- `pkg/kv/snapshot.go`
- `pkg/kv/poller.go`
- `pkg/kv/p2p.go`

This converges for many common cases, but it is **not** a full CRDT.

## Why It Is Not A Full CRDT Yet

### 1. Deletes are only partially first-class

The current snapshot model now carries tombstones directly, which is a major
reliability improvement over pure absence-based delete inference.

But the system still is not a full CRDT because tombstone lifecycle and causal
GC are not fully defined, and legacy paths still infer deletes from baseline
diffs.

### 2. Merge authority still falls back to wall-clock timestamps

Current LWW ordering uses:

1. timestamp
2. device ID
3. sequence

That is better than before because causal ordering wins when known, but truly
concurrent writes still fall back to the legacy clock. A full CRDT should make
causal metadata the primary authority and keep clock use minimal and clearly
bounded.

### 3. Sync is still snapshot-based, not delta- or op-first

On reconnect, peers exchange whole snapshots. That is workable, but it is less
precise than exchanging:

- causal summaries
- missing ops
- or compact deltas

Snapshot sync is workable, especially now that snapshots carry tombstones and a
causal summary, but it is still less precise than a sync protocol built around
causal summaries and missing ops.

### 4. Tombstone GC is undefined

A real CRDT needs a safe rule for when a delete tombstone can be discarded.
Today there is no causal-stability model because tombstones are not modeled as
first-class replicated state.

## What “Full CRDT” Should Mean Here

For `sky10`, a full-CRDT KV should satisfy these properties:

- two offline devices can both edit the same namespace and converge after
  reconnect
- convergence is independent of message ordering, duplication, or retry
- delete operations cannot be silently lost because a peer missed an older
  baseline
- restart does not lose causal history needed for future merge
- 3+ device private networks converge transitively, not only pairwise
- sync can be retried indefinitely without changing the final result

## Recommended Target Model

Use:

- **state-based OR-Map<String, LWW-Register<bytes>>** as the near-term target
- persisted per-device actor/counter history
- dotted version vectors for causality
- explicit tombstones for delete/remove
- anti-entropy on reconnect
- optional delta/op sync later, once the state model is correct

This keeps the user-facing semantics familiar:

- one key maps to one value
- concurrent writes still resolve deterministically

But it replaces the current fragile parts:

- baseline-derived delete inference
- wall-clock-first conflict authority
- whole-snapshot-only reconciliation

## Data Model

Each local mutation should still be representable as an explicit op:

```json
{
  "namespace": "default",
  "key": "secret",
  "op": "set",
  "value": "...",
  "actor": "device_pubkey_or_device_id",
  "counter": 42,
  "dot": ["actor", 42],
  "context": {
    "actorA": 10,
    "actorB": 7
  }
}
```

Deletes should also be explicit ops:

```json
{
  "namespace": "default",
  "key": "secret",
  "op": "delete",
  "actor": "device_pubkey_or_device_id",
  "counter": 43,
  "dot": ["actor", 43],
  "context": {
    "actorA": 10,
    "actorB": 7
  }
}
```

### Required Durable Fields

- actor ID
- monotonic per-actor counter
- op kind
- key
- value for set
- causal context

### Derived State

Materialized key state should be a cache derived from durable replicated state,
not the sole source of truth.

For a state-based design, the replicated snapshot itself must be self-sufficient
to heal an offline peer:

- live entries
- tombstones
- causal summary

## Conflict Semantics

### Recommended v2 semantic

`LWW-Register` per key inside an OR-Map.

Why:

- easiest user model
- closest to current behavior
- deterministic resolution
- lower UI complexity than multi-value registers

### Optional later semantic

`MV-Register` per key if silent overwrite is unacceptable.

That would preserve concurrent conflicting writes instead of collapsing them to
one winner, but it raises product/UI questions. It should be treated as a
separate decision, not mixed into the first CRDT refactor.

## Delete Semantics

Deletes must remain first-class replicated state.

Required rules:

- a delete must be replicated explicitly, not inferred from absence alone
- a tombstone must dominate older visible values
- tombstones must replicate like any other op
- tombstones can only be garbage-collected after causal stability

Without this, deletes are always the weakest part of the system.

## Sync Protocol

On connection or periodic anti-entropy:

1. peer A sends its namespace summary:
   - version vector
   - optional digest/checksum
2. peer B compares that summary against its own op log
3. peer B sends only the missing ops or deltas
4. peer A applies ops idempotently
5. peer A responds with any ops B is missing
6. both peers update their local materialized snapshot

This is the core change from the current model:

- not “send whole snapshot and infer deletes from absence alone”
- instead “exchange causal summaries and enough replicated state to heal”

## Anti-Entropy

To be trustworthy, KV should not depend on one lucky push after one mutation.

Required behavior:

- push on local mutation
- push on private-network reconnect
- periodic anti-entropy even when no new writes occur
- anti-entropy bounded by short request timeouts

Current status:

- push on local mutation: shipped
- push on reconnect: shipped
- summary-first delta anti-entropy: shipped for P2P sync
- periodic anti-entropy without writes: shipped
- bounded per-exchange timeouts: shipped

This keeps convergence working after:

- daemon restart
- missed push
- transient DHT/discovery delay
- temporary stream failure

## Persistence Requirements

The local durable store should preserve enough history for future merge:

- local actor counter
- retained ops or compacted delta state
- tombstones until safe GC
- peer sync summaries if useful for optimization

If compaction is added, it must preserve CRDT semantics. That usually means:

- checkpoint materialized state
- checkpoint causal summary
- retain not-yet-stable tombstones

## Garbage Collection

GC should be based on causal stability, not age alone.

A tombstone can be removed only when every known replica has observed a causal
summary that dominates the delete op.

Until that exists, tombstone GC should be conservative.

## V1 Hardening Plan

Before the full CRDT redesign, keep the current model but make it more
trustworthy:

1. Keep KV sync limited to connected private-network peers only.
2. Push snapshots again after private-network reconnect or rediscovery.
3. Bound DHT publish/resolve/auto-connect calls with timeouts in daemon
   refresh paths.
4. Add periodic anti-entropy pushes even without new writes.
5. Expand integration coverage around:
   - late discovery
   - restart and rediscovery
   - 3+ devices
   - set/delete races
   - long offline periods

This does not make the system a full CRDT, but it materially raises
confidence in the current model.

## V2 Full CRDT Plan

### Phase 1: Causal Metadata

- introduce actor IDs and per-actor counters
- define version vector format
- prefer causal ordering over clock ordering

Status:

- shipped as an initial checkpoint

### Phase 2: Replicated Delete State And Causal Snapshots

- keep tombstones in replicated snapshots
- persist enough metadata for reconnect healing without old per-peer baselines
- define a stable snapshot summary format

Status:

- partially shipped; tombstones and causal summaries are now in snapshots

### Phase 3: Anti-Entropy Protocol

- exchange version vectors on reconnect
- ship only missing state first, with ops/deltas as an optimization later
- add retry-safe bounded sync loops

Status:

- summary exchange + state delta transfer shipped
- periodic background anti-entropy shipped
- bounded per-exchange deadlines shipped

### Phase 4: Tombstones And GC

- replicate explicit tombstones
- implement causal-stability-based GC

### Phase 5: Delta Or Op Exchange

- exchange missing ops or compact deltas when peers are already close
- keep full-state sync as the correctness fallback

### Phase 6: Compaction

- compact retained history without losing convergence guarantees
- keep checkpoints, vectors, and unstable tombstones

## Test Matrix

The CRDT work is not done unless these are covered:

- concurrent set/set on the same key
- concurrent set/delete on the same key
- delete/delete from different devices
- restart with persisted local logs
- reconnect after long offline periods
- duplicate delivery
- message reordering
- partial delivery and retry
- 3+ device transitive convergence
- tombstone retention and GC
- extreme clock skew, to confirm wall clock is no longer authoritative

## Recommendation

Short term:

- ship the current private-network KV fixes
- harden discovery timeouts and periodic anti-entropy
- keep adding restart/reconnect integration coverage

Medium term:

- redesign KV as an op-based CRDT with explicit tombstones and causal
  metadata

If KV is meant to be a foundational primitive for `sky10`, the medium-term
work is necessary. The current snapshot/LWW design can be improved, but it
should be treated as an interim architecture rather than the final one.
