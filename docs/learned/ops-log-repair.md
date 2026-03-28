# Ops Log Repair: Don't Clear ops.jsonl

Decided: 2026-03-28

## Problem

When a device's local ops.jsonl has corrupted state (e.g., timestamp
pollution from repeated `seedStateFromDisk` runs stamping every entry
with `time.Now()`), the tempting fix is to delete ops.jsonl and let
the daemon rebuild from the S3 snapshot.

This doesn't work. On restart with an empty ops.jsonl:

1. `catchUpFromSnapshot` loads the S3 snapshot into the empty CRDT.
   The second pass (delete propagation) finds nothing to delete because
   the local snapshot was empty — there's no prior state to compare.

2. `seedStateFromDisk` scans disk, finds thousands of files not in the
   CRDT (only the S3 snapshot entries are there), treats them all as
   "new local files," and queues them in the outbox for upload.

3. The outbox re-uploads stale files to S3, undoing any previous
   deletes and breaking convergence in the other direction.

The fundamental issue: **clearing ops.jsonl destroys the history needed
to distinguish "stale download" from "genuinely new local file."** Both
look identical — file on disk, not in CRDT, not in S3 snapshot. Without
history, seed must assume they're new.

## Correct Repair Approach

Build a repair command that rewrites ops.jsonl from the S3 snapshot
instead of clearing it. This means:

1. Load the S3 snapshot (authoritative state)
2. Write one synthetic entry per file into a fresh ops.jsonl
3. Seed then sees files on disk that match the CRDT and skips them
4. Genuinely new files (created while daemon was off) are detected
   normally — they're on disk but not in the rewritten ops.jsonl

This preserves the ability to detect new files while resetting the
CRDT to match S3.

## If You Must Clear ops.jsonl

Accept that both machines will converge to having ALL files (including
previously deleted ones). The stale files get re-uploaded, the other
machine ingests them, and both match. Then delete the unwanted files
once — the delete will propagate correctly going forward.

Convergence first, cleanup second.
