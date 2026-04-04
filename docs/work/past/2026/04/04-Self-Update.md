---
created: 2026-04-04
model: claude-opus-4-6
---

# Self-Update Command and RPC

sky10 can now update itself. `sky10 update` (or `sky10 upgrade`) checks
GitHub for the latest release, downloads the platform binary, and
replaces the running executable in place. The same flow is available
through the daemon's RPC, with real-time progress over SSE for the
web UI.

## Why

Devices running sky10 are often headless Linux machines or remote Macs
that aren't managed by a package manager. Updating previously meant
SSH-ing in, downloading the binary, replacing it, and restarting the
daemon — per device. With dozens of devices across a fleet, this doesn't
scale. A built-in update path makes it possible to keep every node
current from the web UI or a single RPC call, without manual
intervention.

## What was built

### CLI command
- `sky10 update` / `sky10 upgrade` — checks GitHub releases, downloads,
  replaces binary. `--check` flag for dry-run inspection.

### RPC methods (system.* namespace)
- `system.checkUpdate` — synchronous version check, returns current vs
  latest and asset URL.
- `system.update` — fully async. Returns `{"status":"checking"}`
  immediately, then streams progress via SSE:
  - `update:progress` — `{downloaded, total}` throttled to ~10/sec
  - `update:complete` — `{previous, updated}`
  - `update:error` — `{message}`
- Concurrent-update guard via `atomic.Bool` — second call rejected
  while one is in-flight.

### Periodic background check
- Daemon checks GitHub on startup and every 2 hours.
- Emits `update:available` via SSE when a new version is found.
- Web UI subscribes and can show a banner without polling.

## Design decisions

- **CLI works standalone** — no daemon required. The command hits GitHub
  directly, so you can update even if the daemon is down.
- **Atomic binary replacement** — writes to a temp file in the same
  directory, then `os.Rename` for an atomic swap. The running daemon
  keeps the old inode open; the new binary takes effect on restart.
- **Progress via SSE, not polling** — the existing event system
  (`server.Emit`) already pushes to all subscribers. No new transport
  needed.
- **Throttled progress callbacks** — the `progressReader` wraps
  `io.Reader` and fires at most once per 100ms to avoid flooding SSE.
- **No auto-restart** — after updating the binary, the daemon tells the
  caller to restart. Auto-restarting from within the process is fragile;
  systemd/launchd watchdogs handle this better.

## Files created
- `pkg/update/update.go` — `Check()`, `Apply()`, `PeriodicCheck()`,
  `progressReader`
- `pkg/update/rpc.go` — `RPCHandler` for `system.*` methods
- `pkg/update/update_test.go` — unit + integration tests (mock HTTP
  server for unit, real GitHub API for integration)
- `commands/update.go` — CLI command with `upgrade` alias, `Version` var

## Files modified
- `main.go` — registers `UpdateCmd`, sets `commands.Version`
- `commands/serve.go` — registers `system.*` RPC handler, starts
  periodic update check goroutine
