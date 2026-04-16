---
created: 2026-04-16
updated: 2026-04-16
model: gpt-5.4
---

# FS Windows Normalization Plan

## Goal

Make `sky10` file sync behave predictably on Windows without changing the
logical sync model used by macOS and Linux peers.

This work should close the remaining `Milestone 7` gap around Windows,
case-sensitivity, and path-normalization edge cases.

## Core Rule

`sky10` needs two different path layers and must stop mixing them:

- **logical FS paths** for metadata, CRDT state, RPC payloads, and peer sync
- **local OS paths** for materializing those logical paths onto a machine's
  filesystem

Logical paths must be cross-platform and canonical. Local OS paths may vary by
platform, but they must always be derived from canonical logical paths through
one controlled mapping layer.

## Current Observations

The FS engine is already partly aligned with this model:

- watcher and scan code already convert local paths to slash-separated relative
  paths using `filepath.Rel(...); filepath.ToSlash(...)`
- many local materialization sites already use `filepath.FromSlash(...)`
- peer metadata and CRDT state are largely path-keyed using slash-separated
  relative names

The remaining risk is that some ingress points still accept raw user or local
OS paths and rely on ad hoc `filepath.Clean`, `filepath.Join`, and prefix
checks. That is not strong enough for Windows.

High-risk ingress/materialization touchpoints:

- `pkg/fs/rpc_http.go`
- `pkg/fs/rpc_files.go`
- `pkg/fs/rpc_handler.go`
- `pkg/fs/watcher_handler.go`
- `pkg/fs/reconciler.go`
- `pkg/fs/scan.go`
- `pkg/fs/watcher.go`

## Decisions

### 1. Canonical logical paths

All FS metadata paths should be canonicalized to one form:

- relative only
- `/` separators only
- no leading slash
- no empty path segments
- no `.` or `..` segments
- no raw drive letters or UNC roots
- normalize `\` to `/` before validation at ingress

These logical paths are the only paths that should appear in:

- `opslog`
- outbox entries
- transfer sessions
- RPC payloads
- peer metadata exchange
- health/conflict surfaces

### 2. Local filesystem mapping

Materializing a logical path onto disk must always go through one helper that:

- maps `/` to the native separator with `filepath.FromSlash`
- joins beneath the drive root
- rejects escape attempts
- applies Windows-specific invalid-name policy

The engine should not scatter `filepath.Clean` plus prefix checks across many
call sites.

### 3. No silent renames

If a synced logical path is illegal or ambiguous on Windows, `sky10` should
not silently rewrite it.

Instead:

- mark the item invalid or conflicting
- surface the exact path and reason
- keep the drive explainably degraded until the user resolves it

Silent renames would make cross-device behavior impossible to reason about.

### 4. Case collision is a sync conflict

Windows materialization is effectively case-insensitive for ordinary paths.

If two logical paths differ only by case in a way that collides on Windows,
that should be treated as an explicit conflict/degraded condition, not as
"last writer wins" on the local disk.

### 5. Symlink policy must be explicit

Windows symlinks require elevation or Developer Mode and behave differently
from Unix.

For the first Windows-ready FS pass, we should likely:

- keep symlink metadata in the logical model
- disable Windows symlink materialization by default, or gate it on an
  explicit capability check
- surface unsupported symlink entries as degraded sync state instead of
  failing silently

## Workstream 1: Path API

Create one shared path normalization module, likely `pkg/fs/path.go`, that
defines the only supported conversions:

- `NormalizeLogicalPath(string) (string, error)`
- `MustNormalizeLogicalPath` only for tests if needed
- `LocalPathToLogical(root, local string) (string, error)`
- `LogicalPathToLocal(root, logical string) (string, error)`
- `WindowsPathIssue(logical string) (reason string, ok bool)` or equivalent

Checklist:

- [x] Add a single canonical logical-path normalizer.
- [x] Add one local-to-logical conversion helper.
- [x] Add one logical-to-local conversion helper.
- [ ] Remove ad hoc path normalization logic from ingress points.
- [ ] Ensure all FS metadata producers normalize before writing state.

Done when:

- [ ] New metadata cannot be written with backslashes, absolute roots, or
      `..` segments.
- [ ] Local materialization always goes through one code path.

## Workstream 2: Windows invalid-name policy

Define and implement Windows filename/path rejection rules for materialization.

Minimum blocked cases:

- reserved DOS names such as `CON`, `PRN`, `AUX`, `NUL`, `COM1`-`COM9`,
  `LPT1`-`LPT9`
- segments ending in `.` or space
- `:` inside segments to avoid alternate data stream semantics
- NUL and control characters
- absolute drive-prefixed or UNC-like paths

Checklist:

- [x] Add a Windows path-segment validator.
- [x] Validate per segment rather than only at the full path string.
- [x] Surface invalid-path issues through health/activity APIs.
- [x] Decide whether invalid paths are omitted from materialization or block
      the whole drive from "healthy" state.

Done when:

- [x] A Windows node reports illegal remote names deterministically.
- [x] Invalid names no longer depend on accidental OS behavior at write time.

## Workstream 3: Case-fold collision handling

Add Windows collision detection at the logical-path layer.

This should detect cases like:

- `Docs/Readme.md`
- `docs/readme.md`

Both may be distinct logical paths in the CRDT, but they collide on a
case-insensitive local filesystem.

Checklist:

- [x] Define a Windows collision key for logical paths.
- [x] Detect collisions before materialization.
- [x] Surface the collision as explicit degraded/conflict state.
- [x] Add conflict entries to activity/health surfaces.
- [ ] Decide whether collisions should also block local watcher ingestion from
      creating ambiguous entries on Windows.

Done when:

- [x] Windows nodes do not silently overwrite one colliding path with another.
- [x] The user can see which logical paths collided.

## Workstream 4: Ingress hardening

Replace ad hoc path acceptance at all FS entry points.

Checklist:

- [x] Normalize RPC-upload paths in `pkg/fs/rpc_http.go`.
- [x] Normalize RPC file-operation paths in `pkg/fs/rpc_files.go`.
- [x] Normalize RPC materialization and inspection paths in
      `pkg/fs/rpc_handler.go`.
- [x] Normalize watcher-produced paths consistently in
      `pkg/fs/watcher.go` and `pkg/fs/watcher_handler.go`.
- [x] Normalize scan-produced paths consistently in `pkg/fs/scan.go`.
- [ ] Ensure reconciler and transfer-session code only consume canonical
      logical paths.

Done when:

- [ ] Every FS path enters the engine in canonical logical form.
- [ ] No edge path producer can bypass normalization by writing directly into
      logs or transfer state.

## Workstream 5: Symlink policy

Make Windows symlink behavior an explicit product decision instead of a side
effect.

Checklist:

- [ ] Document the Windows symlink capability policy.
- [ ] Add runtime capability detection for symlink creation where needed.
- [ ] Decide whether Windows should materialize safe relative symlinks only.
- [ ] Decide whether Windows should materialize all supported symlinks when
      capability exists.
- [ ] Decide whether Windows should instead treat symlinks as unsupported in
      v1 and surface them as degraded.
- [ ] Add Windows-aware tests or skips for symlink cases.

Done when:

- [ ] Windows behavior for synced symlinks is intentional and documented.

## Workstream 6: Health and UX

If Windows path issues exist, the user should be able to see them without
reading raw logs.

Checklist:

- [x] Add health counters for invalid-path and case-collision issues.
- [x] Surface those issues in `skyfs.health`, `skyfs.driveList`, and
      `skyfs.syncActivity`.
- [x] Show the offending logical path plus a short reason in the UI.
- [x] Distinguish ordinary transfer degradation from path-policy degradation.

Done when:

- [x] A Windows user can tell whether sync is blocked by naming issues versus
      peer/S3/network issues.

## Workstream 7: Test matrix

This work should be driven by tests before broad code changes.

### Fast tests

- [x] Unit tests for logical path normalization.
- [x] Unit tests for local path materialization guards.
- [x] Unit tests for Windows reserved names.
- [x] Unit tests for trailing-dot and trailing-space rejection.
- [x] Unit tests for case-collision detection.

### Platform-aware tests

- [ ] Windows-only tests for local materialization behavior.
- [ ] Windows-aware watcher/scan tests where path separators differ.
- [ ] Symlink capability tests with skip behavior when Windows cannot create
      symlinks.

### End-to-end tests

- [ ] Daemon integration case for a peer-created case collision arriving on a
      Windows node.
- [ ] Daemon integration case for a peer-created invalid Windows path.
- [ ] Daemon integration case for symlink behavior under the chosen Windows
      policy.

Done when:

- [ ] Windows normalization behavior is verified by repeatable tests rather
      than manual reasoning.

## Recommended Execution Order

1. Workstream 1: Path API
2. Workstream 2: Windows invalid-name policy
3. Workstream 3: Case-fold collision handling
4. Workstream 4: Ingress hardening
5. Workstream 6: Health and UX
6. Workstream 5: Symlink policy
7. Workstream 7: Test matrix completion

## Acceptance Criteria

- [ ] FS metadata stores only canonical logical paths.
- [ ] Windows-invalid names are detected before local materialization.
- [ ] Case collisions do not silently overwrite data.
- [ ] Windows path issues are visible in health/activity surfaces.
- [ ] The daemon integration suite covers the critical Windows naming failure
      modes.

## Repo Touchpoints

- `pkg/fs/path.go` or equivalent new path-normalization module
- `pkg/fs/rpc_http.go`
- `pkg/fs/rpc_files.go`
- `pkg/fs/rpc_handler.go`
- `pkg/fs/watcher.go`
- `pkg/fs/watcher_handler.go`
- `pkg/fs/scan.go`
- `pkg/fs/reconciler.go`
- `pkg/fs/conflict.go`
- `pkg/fs/symlink_test.go`
- `pkg/fs/rpc_drive_test.go`
- `integration/e2e_fs_process_integration_test.go`
- `docs/work/todo/windows-support.md`

## Relationship To The Broader Windows Plan

This document is intentionally narrower than
[`../todo/windows-support.md`](../todo/windows-support.md).

That file tracks all Windows work across RPC transport, daemon management,
signals, builds, installers, and agent bootstrap.

This plan only covers the FS-specific normalization and naming work needed to
close the remaining `Milestone 7` reliability gap.
