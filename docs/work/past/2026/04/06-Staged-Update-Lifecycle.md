---
created: 2026-04-06
model: gpt-5.4
---

# Staged Update Lifecycle and Tray Recovery

Reworked sky10 self-update into explicit `check`, `download`,
`status`, and `install` phases, persisted staged artifacts under
`~/.sky10/update/`, and moved the tray app onto that CLI-driven update
flow. This also closed a stale-menu bug and fixed a tray build
regression that shipped in `v0.39.0`, with the corrected release
published as `v0.39.1`.

## Why

The original `sky10 update` command did everything in one step:

- checked GitHub
- downloaded the CLI
- downloaded `sky10-menu`
- replaced binaries in place
- restarted the daemon
- restarted the menu

That was workable for a one-shot CLI command, but it was the wrong shape
for the tray app. The tray needed to be able to:

- check for a new version without changing running processes
- download a new version ahead of time
- show "Restart to update" only after bits were staged locally
- install the staged release only when the user clicked

There was also a separate tray bug: the menu popup was built once at
startup and could stay stuck on "sky10 is not running" even when the
daemon recovered. The updater ordering changes helped, but the tray
itself still needed to refresh its menu when daemon state changed.

## What Changed

### Staged updater model

The updater now has two layers:

- `pkg/update` owns release checks, staged artifacts, and install logic
- `commands/update.go` exposes the user-facing command tree

New subcommands:

- `sky10 update check`
- `sky10 update download`
- `sky10 update status`
- `sky10 update install`

`sky10 update` and `sky10 upgrade` remain convenience wrappers. They now
compose the staged flow instead of performing ad hoc in-place updates.

Staged artifacts are stored in `~/.sky10/update/`:

- `staged.json` metadata
- staged `sky10`
- staged `sky10-menu`

That makes downloads survive daemon restarts, tray restarts, logout, and
reboot. `install` becomes the only disruptive step.

### Tray app now shells out to the CLI

The Tauri tray no longer depends on daemon-side update RPC for installs.
Instead it:

- checks staged local state with `sky10 update status --json`
- checks remote availability with `sky10 update check --json`
- pre-downloads with `sky10 update download`
- installs with `sky10 update install`

This makes the CLI the single source of truth for update behavior across
headless use, direct terminal use, and the tray UI.

### Menu refresh and startup recovery

The tray polling loop now rebuilds the menu when daemon/update state
changes, not just the icon and tooltip. That fixes the stale popup state
where the tray could continue showing "not running" after the daemon was
healthy.

The updater still waits for the daemon HTTP endpoint before relaunching
the tray after install, which improves startup ordering, but the tray is
now resilient even if startup ordering is imperfect.

## Release Sequence

### `v0.39.0`

Published the staged updater and tray refresh work:

- [`5e1a4cd`](https://github.com/sky10ai/sky10/commit/5e1a4cd) `feat(update): split staged update lifecycle`
- [`693b861`](https://github.com/sky10ai/sky10/commit/693b861) `fix(menu): refresh tray menu on status changes`
- [`da78129`](https://github.com/sky10ai/sky10/commit/da78129) `fix(update): wait for daemon HTTP before menu restart`

`verify-release` passed, but `build-menu` failed. The tray code compiled
locally only conceptually because this machine did not have `cargo`.
Release CI surfaced a real Rust ownership bug:

- `prev_state = new_info.state;` partially moved `new_info`
- the loop later assigned `prev_info = new_info`
- all tray builds failed with `error[E0382]: use of partially moved value`

Per release policy, the broken release was not mutated.

### `v0.39.1`

Fixed the tray polling loop by cloning the enum instead of moving it:

- [`68b0aba`](https://github.com/sky10ai/sky10/commit/68b0aba) `fix(menu): clone tray state in poll loop`

Then cut `v0.39.1` as the replacement patch release. Both release
workflows passed:

- `verify-release` succeeded
- `build-menu` succeeded
- all 10 release assets attached, including `checksums-menu.txt`

Dogfooding also succeeded:

- local machine updated from `v0.38.2` to `v0.39.1`
- `sky10-menu` updated
- daemon restarted cleanly
- the machine settled at exactly one running `sky10-menu` process

## Files Created

- `pkg/update/stage.go` — staged release metadata, status, install path
- `pkg/update/stage_test.go` — staged lifecycle regression tests

## Files Modified

- `pkg/update/update.go` — split CLI/menu availability, shared download helpers
- `commands/update.go` — `check` / `download` / `status` / `install`
- `commands/update_test.go` — command-level staged updater regressions
- `menu/src-tauri/src/main.rs` — menu refresh, CLI-driven update flow,
  polling-loop ownership fix

## Key Decisions

- **Persistent stage, not `/tmp`**: downloads live in `~/.sky10/update/`
  so a pre-downloaded release survives reboot and can still be installed
  later.
- **CLI owns update semantics**: the tray shells out to the CLI instead
  of duplicating updater logic or relying on daemon-specific RPC
  behavior.
- **`install` is the disruption boundary**: checking and downloading are
  safe; process restarts only happen during `install`.
- **Broken release replaced with a patch release**: `v0.39.0` remains
  published but incomplete; `v0.39.1` is the release to use.
