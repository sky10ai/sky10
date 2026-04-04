---
created: 2026-04-04
model: claude-opus-4-6
---

# Remove Cirrus — Replace SwiftUI App with Web UI

Removed the Cirrus SwiftUI macOS desktop app and replaced it with web
UI enhancements, CLI daemon management, and a Tauri menu bar app.
Cirrus code archived to github.com/sky10ai/cirrus.

## Why

Cirrus was a full SwiftUI macOS app (2641 files) that duplicated
functionality the web UI already had. Maintaining Swift + Go + React
was too much surface area. The web UI is cross-platform and served
directly by the daemon — no separate app to build, sign, or distribute.

## What Changed

### Web UI additions
- File upload (`POST /upload` endpoint + toolbar button)
- File download (`GET /download` endpoint + context menu)
- Drive creation form (wires existing `skyfs.driveCreate` RPC)
- Drive start/stop toggle on DriveCard
- Activity feed page with real-time SSE event log
- Device remove button with confirmation
- Connect-to-peer input on Network page
- Fixed dead Skylink mode toggle (now display-only)

### CLI daemon management (`sky10 daemon`)
- `install` / `uninstall` — launchd on macOS, systemd on Linux
- `status` / `restart` / `stop`
- Platform-specific files with build tags

### Tauri menu bar app (`sky10-menu`)
- Constellation tray icons with 5 states (connected, disconnected,
  syncing, update available, error)
- Menu: version display, Open, update section, Quit
- Queries daemon via JSON-RPC over HTTP for version and update status
- Polls every 30s, switches icon based on state
- CI workflow builds for all 4 platforms on release tag

### Install changes
- Binary installs to `~/.bin/sky10` (no sudo)
- `install.sh` cleans up old `/usr/local/bin` and Homebrew installs
- Downloads `sky10-menu` alongside CLI binary
- Adds `~/.bin` to PATH in shell rc

### Cleanup
- Deleted `cirrus/` directory (2641 files, -10696 lines)
- Removed Swift test CI job
- Removed Makefile Swift targets (`build-swift`, `test-skyfs-ui-macos`)
- Removed Cirrus from CLAUDE.md, README, release skill, docs
- Updated storage-providers guide
- Rewrote release skill without Cirrus/Homebrew steps

## Files Created
- `pkg/fs/rpc_http.go` — upload/download HTTP handlers
- `commands/daemon.go` — shared daemon command tree
- `commands/daemon_darwin.go` — launchd implementation
- `commands/daemon_linux.go` — systemd implementation
- `commands/daemon_other.go` — unsupported platform fallback
- `menu/` — Tauri systray app (Cargo.toml, main.rs, icons, CI workflow)
- `web/src/pages/Activity.tsx` — activity feed page
- `web/src/components/drives/NewDriveForm.tsx` — drive creation form

## Decisions
- **No File Provider**: Finder integration was complex and low ROI
- **No macOS notifications**: SSE + web activity feed is sufficient
- **No storage provider presets**: Users look up their endpoint
- **Tauri over native Go systray**: Avoids CGo, CI builds the binary
- **`~/.bin` over `/usr/local/bin`**: No sudo needed anywhere
- **launchd + systemd**: Platform-native daemon management, no custom PID polling
