---
created: 2026-03-31
model: claude-opus-4-6
---

# Web Command Center

Added a full React web UI served by the Go daemon's HTTP server,
replacing the need for the native SwiftUI file browser. All pages
are wired to live daemon RPC with real data.

## Stack

- React 19 + TypeScript + Tailwind CSS 3 + React Router 7
- Built with bun + Vite, output embedded via `go:embed all:web/dist`
- Dev mode: Vite on :5173, proxies /rpc and /rpc/events to Go :9101
- Production: single Go binary with embedded SPA + fallback routing

## Screens

- **Drives** — overview with sync status, file counts, outbox pending
- **File Browser** — per-drive file list, right-click context menu
  (create folder via `skyfs.mkdir`, delete via `skyfs.remove`)
- **KV Store** — full CRUD: browse all keys, edit values inline,
  create new keys, delete keys. Detects JSON for syntax display.
- **Devices** — registered devices with addresses, platform, location,
  version, P2P multiaddrs. Highlights "this device".
- **Network** — P2P topology graph (SVG), peer details enriched from
  device list, listen addresses
- **Settings** — identity (sky10 address, peer ID), version/commit/uptime,
  Skylink mode (private/network), S3 storage info, device details
- **Command Palette** — Cmd+K overlay for quick navigation

## New RPC methods

- `skyfs.mkdir` — creates directory in drive's local_path
- `skyfs.remove` — fixed: deletes from drive's local_path via
  `os.RemoveAll` (was stubbed with "not implemented")
- Added `findDrive` helper: looks up drive by ID, prefixed ID, or name

## New CLI command

- `sky10 ui open` — queries daemon for HTTP address via `skyfs.health`,
  extracts port from `[::]:9101` format, opens browser

## Go integration

- `pkg/rpc/webui.go` — SPA handler with `embed.FS` + fallback to
  index.html for client-side routing
- `pkg/rpc/http.go` — registers web UI handler when index.html exists
  in embedded FS, falls back to JSON info endpoint otherwise
- CORS headers on `/rpc` for Vite dev server
- `main.go` — `//go:embed all:web/dist` directive

## Build determinism

Achieved fully deterministic builds across machines:
1. bun version pinned to 1.3.11 via `packageManager` in package.json
2. Dependencies locked by `bun.lock` + `--frozen-lockfile`
3. Vite output is content-hash deterministic (verified: clean
   `rm -rf node_modules dist` + rebuild = identical SHA-256)
4. Go build: `-trimpath -buildvcs=false` + git committer timestamp
5. CI uses `oven-sh/setup-bun` with `bun-version-file: web/package.json`

web/dist is NOT committed to git. CI builds it from source.
verify-release workflow confirms byte-identical binaries.

## Design system

Based on "The Ethereal Vault" design spec (~/Desktop/stitch/):
- Material Design 3 color tokens (primary #0058bc, surface hierarchy)
- Inter font family + Roboto Mono for technical values
- Glassmorphism for floating elements (blur + semi-transparent)
- No solid borders — tonal layering for depth
- Material Symbols Outlined icon font

## Files created

| Layer | Count | Files |
|-------|-------|-------|
| Scaffolding | 7 | package.json, vite/tailwind/postcss/ts configs, index.html, index.css |
| RPC client | 3 | rpc.ts (typed wrappers), events.ts (SSE), useRPC.ts (hook + utils) |
| Layout | 5 | Sidebar, Header, Layout, Icon, CommandPalette |
| Pages | 7 | Drives, FileBrowser, KVStore, Devices, InviteDevice, Network, Settings |
| Go | 3 | webui.go, http.go changes, main.go embed |
| CLI | 1 | commands/ui.go |
| CI | 2 | test.yml + verify-release.yml updated |

## Releases

- v0.26.0 — initial web UI (binary missing embedded frontend)
- v0.26.1 — fix ui open URL parsing, include frontend in binary
- v0.26.2 — commit web/dist for deterministic CI
- v0.26.3 — full-stack determinism without committing dist
