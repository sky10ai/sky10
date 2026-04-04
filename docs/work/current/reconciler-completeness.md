---
created: 2026-03-23
model: claude-opus-4-6
---

# Reconciler Completeness Bug

## Problem

The reconciler misses files during normal operation. Files exist in the CRDT
snapshot but are never downloaded to disk. Restarting the
daemon fixes it — `seedStateFromDisk` triggers a fresh reconcile pass that
picks up the stranded files.

## Observed behavior

1. Daemon starts, poller imports ops, pokes reconciler
2. Reconciler runs one pass, downloads most files
3. Reconciler finishes with `failed == 0`, goes idle
4. Poller continues polling, finds `ops=0` — never pokes reconciler again
5. Some files remain in the snapshot but not on disk — permanently stranded
6. Restart → `seedStateFromDisk` → reconciler downloads the missing files

## Root cause (suspected)

The reconciler only runs when poked. Once it completes a pass with no
failures, it blocks on `<-r.poke` forever. If files were added to the
snapshot AFTER the reconciler called `Snapshot()` but BEFORE it finished
its pass, those files are missed.

Likely race: the poller and reconciler run concurrently. The poller appends
ops to the local log (updating the snapshot) while the reconciler is mid-pass
using a stale snapshot. When the poller tries to poke the reconciler, the
poke channel (capacity 1) is already full — **poke dropped**. The reconciler
finishes its pass, returns to the select loop, and the channel is empty.

```
1. Poller poll N:   finds ops → appends → pokes reconciler (chan now has 1)
2. Reconciler:      reads poke → starts reconcile(ctx) with snapshot S1
3. Poller poll N+1: finds MORE ops → appends → poke DROPPED (chan full)
4. Reconciler:      finishes pass (used S1, missed ops from step 3)
5. Reconciler:      returns to select → chan empty → blocks forever
```

Files from step 3 are in the snapshot but never reconciled.

## Evidence

- Daemon logs show reconciler downloading files at 23:09-23:10, then idle
- Poller shows `poll ops=0` from 23:27 onward — no more pokes
- Web UI shows files without Merkle hashes (in snapshot, not on disk)
- Restarting daemon triggers `seedStateFromDisk` which finds and downloads
  the missing files every time

## Possible fixes

### A. Double-tap: always reconcile twice
After completing a pass, immediately do one more pass. If the second pass
finds nothing, go idle. Catches ops that arrived during the first pass.

### B. Drain the poke channel after reconcile
After `reconcile(ctx)` returns, check if a poke was queued during the pass.
Use a non-blocking read from the channel. If found, run another pass.

### C. Periodic reconcile tick
Add a timer (e.g., every 60s) that triggers a reconcile pass regardless of
pokes. Guarantees eventual consistency even if pokes are dropped.

### D. Compare snapshot before and after
After reconcile finishes, re-read the snapshot. If it differs from the one
used during the pass, run another pass immediately.

## Recommendation

Option B is simplest and most correct. After each reconcile pass, do a
non-blocking read from the poke channel. If a poke was queued during the
pass, loop and reconcile again. This drains any pokes that arrived while
the reconciler was busy.

```go
func (r *Reconciler) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-r.poke:
            r.reconcile(ctx)
            // Drain any poke that arrived during reconcile
            for {
                select {
                case <-r.poke:
                    r.reconcile(ctx)
                default:
                    goto wait
                }
            }
        wait:
        }
    }
}
```

Option C (periodic tick) is a good belt-and-suspenders addition regardless.

## Related bugs (fixed in v0.13.7)

- Watcher event channel overflow: `flushPending` dropped events permanently
- Poller fence-post: `ts <= since` skipped same-second ops

## Related bugs (not yet fixed)

- Directory deletion doesn't remove empty folders on remote machine
- Merkle hash misclassifies macOS packages (cosmetic, not sync)
