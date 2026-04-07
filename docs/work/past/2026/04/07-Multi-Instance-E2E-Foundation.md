---
created: 2026-04-07
model: gpt-5.4
---

# Multi-Instance E2E Foundation

Reworked `sky10` so multiple real daemons can run on one host without
trampling each other's local state, then used that isolation to build
real end-to-end integration coverage around three-device private-network
KV sync and three-device MinIO-backed file sync. This was the first pass
that turned "maybe Docker, maybe VMs" into a concrete, CI-runnable
process harness built around the real binary and the real daemon entry
points.

This work landed on `main` on 2026-04-07.

## Why

The original testing problem was not really "we need Docker."

The actual blocker was that `sky10` still behaved like a mostly
singleton daemon on one host:

- local state lived in several hard-coded places under the user home
  and `/tmp`
- runtime artifacts such as PID files and sockets assumed one active
  daemon
- the daemon defaulted toward one set of implicit discovery/runtime
  paths and one set of implicit process assumptions

That made "run three devices on one machine" fundamentally shaky.
Containers or VMs would only hide the problem instead of fixing it.

The right first move was:

1. centralize local state resolution
2. make multi-instance runtime/network behavior explicit
3. prove the architecture with real local processes
4. only then talk about heavier packaging like Docker

## What Changed

### 1. Local instance state became rootable

`sky10` gained a real per-instance root via `--home` / `SKY10_HOME`,
and local state resolution was centralized instead of being spread
through feature code.

That matters because config, keys, FS/KV caches, drive state, and
runtime artifacts now have one controlling root instead of implicitly
sharing global host paths.

### 2. Runtime socket handling became safe for deep per-instance roots

Multi-instance testing immediately exposed a real Unix socket path
length issue when the test harness used deep temp roots. The daemon now
falls back to a short hashed socket path when the normal per-instance
socket path would be too long.

That matters because "multiple isolated homes" is only useful if the
default socket path still works under realistic temp/test roots.

### 3. `serve` gained explicit hermetic network controls

The daemon now supports explicit private-network test controls:

- custom libp2p listen addresses
- explicit bootstrap peers
- disable-default-bootstrap mode
- explicit Nostr relay overrides
- disable-default-relays mode

That matters because real e2e tests should not depend on ambient public
bootstrap or public relay behavior when what we want to validate is our
own private-network convergence.

### 4. Sync drives gained a configurable poll interval

`serve` now exposes `--fs-poll-seconds`, which feeds the drive manager's
poll interval for real sync daemons.

That matters because process-level FS e2e tests need fast, bounded sync
loops. Waiting on the default 30-second drive poll would make the tests
too slow and too noisy for CI.

### 5. A shared real-process integration harness was added

A root-level test harness now:

- builds the real `sky10` binary
- starts isolated daemon processes with separate homes
- drives real CLI flows such as `invite`, `join`, `kv`, and `fs drive`
- starts a local MinIO process when storage-backed flows are under test
- waits on observable convergence instead of reaching into internals

That matters because this is the first integration layer in the repo
that validates the actual binary, actual process startup, actual RPC
surface, and actual inter-daemon behavior together.

## Test Coverage Added

### 1. Three-process KV process test

The new root integration suite now covers:

- three real daemons on one host
- private-network invite/join
- peer discovery and convergence
- KV set propagation
- KV delete propagation

This is much closer to a true user path than the older in-process DHT
tests alone.

### 2. Three-process MinIO-backed FS process test

The FS process coverage now covers:

- S3 initialization against a real local MinIO process
- invite/join of two additional devices
- creation of the same named drive on all three devices
- initial file catch-up from device A to devices B and C
- live propagation of a new file created on B
- delete convergence after A restarts and reseeds the removed file

This is intentionally built on the same real-process harness as KV so
the repo has one coherent direction for end-to-end testing instead of
multiple unrelated test worlds.

### 3. CI now runs the root integration suite

GitHub Actions now runs the root `integration`-tagged tests in addition
to the existing `pkg/fs` integration lane.

That matters because the new multi-instance process tests are now part
of normal regression coverage, not just something that passed once on a
developer machine.

## Why This Approach Was Chosen

The repo needed a foundation, not a testing costume.

Local processes were the right first target because they:

- exercise the real daemon and real CLI
- make local state collisions impossible to ignore
- are fast enough for CI
- keep failure debugging simple
- can later be wrapped by Docker if we still want container packaging

Apple virtualization and Docker may still have value later, but they are
not the architectural fix. The architectural fix is instance isolation
plus explicit runtime/network control.

## What This Accomplished

By the end of this work, the repo had a materially better e2e story:

- multiple `sky10` daemons can be started on one host with isolated
  local state
- private-network KV has real three-process regression coverage
- MinIO-backed file sync has real three-process regression coverage
- CI runs the root process suite
- the harness is reusable for additional private-network and file-sync
  scenarios

This is the point where "three devices on one machine" stopped being an
idea and became a tested development primitive.

## What This Did Not Finish

This foundation does **not** mean the FS e2e story is complete.

Important remaining gaps:

- the new FS process test proves delete convergence through restart +
  startup reseed, not a purely live watcher-driven delete path
- many older `pkg/fs` integration tests are still skipped with
  `snapshot-exchange: requires rewrite`
- failure diagnostics for process e2e can still be richer

So the repo now has a credible end-to-end base, but not yet a fully
finished file-sync reliability matrix.

## Recommended Follow-On

The next high-value steps are:

1. add a direct live-delete FS process regression on continuously
   running daemons
2. port the highest-value skipped FS integration cases to the new
   process harness
3. improve failure dumps so timeouts capture per-node logs and drive
   state automatically

That follow-on work is now much easier because the hard part is done:
the repo finally has a real multi-instance e2e substrate to build on.
