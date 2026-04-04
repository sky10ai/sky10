---
created: 2026-04-03
model: claude-opus-4-6
---

# Remove Cirrus — Migration Plan

## Goal

Replace the Cirrus SwiftUI macOS app with:
1. Web UI enhancements to cover feature gaps
2. A Tauri systray app (pre-built ~3MB binary, no Rust needed at runtime)
3. launchd integration for daemon auto-start on macOS
4. `sky10 daemon` and `sky10 open` CLI commands

Then delete all Cirrus code (`cirrus/` directory, Swift tests, Makefile
targets, release steps, and scattered references).

---

## Feature Gap: Cirrus vs Web UI

| Feature | Cirrus | Web UI | Gap |
|---------|--------|--------|-----|
| File browsing | Yes | Yes | -- |
| Drive listing + status | Yes | Yes | -- |
| Device list + invite | Yes | Yes | -- |
| KV store | No | Yes | Web is ahead |
| P2P network dashboard | No | Yes | Web is ahead |
| S3 bucket browser | Yes | Yes | -- |
| Identity + settings | Yes | Yes | -- |
| Real-time SSE events | Yes (socket) | Yes (SSE) | -- |
| Command palette | No | Yes | Web is ahead |
| **File upload** | Yes (native picker) | **No** | **Must add** |
| **File download** | Yes (native save) | **No** | **Must add** |
| **Drive creation UI** | Yes | **No** (RPC exists, no UI) | **Must add** |
| **Activity feed** | Yes (live log) | **No** | **Must add** |
| **S3 config / onboarding** | Yes (first-run) | **No** | **Must add** |
| **Daemon management** | Yes (DaemonManager) | **No** | **CLI + tray** |
| **Menu bar tray** | Yes (MenuBarExtra) | N/A | **Tauri tray** |
| **Launch at login** | Yes (SMAppService) | N/A | **launchd plist** |
| macOS notifications | Yes | No | Skip (SSE sufficient) |
| File Provider (Finder) | Yes | N/A | Skip (too complex, low ROI) |
| Storage provider presets | Yes (B2/R2/DO/etc) | No | Nice-to-have, defer |
| Debug tools (compact/reset/dump) | Yes | No | CLI sufficient |
| Poll interval config | Yes | No | Defer (default is fine) |

---

## Web UI Detailed Audit

### Per-Page Status

#### FileBrowser (`web/src/pages/FileBrowser.tsx`)

**Has:** browse folders, create folder (button + context menu), delete
file/folder (context menu), breadcrumb navigation, file metadata display
(size, modified, chunks, checksum), real-time SSE refresh.

**Missing:**
- [ ] **File upload** — no button, no drag-and-drop, no HTTP endpoint
- [ ] **File download** — no button, no context menu item, no endpoint
- [ ] File rename / move
- [ ] Multi-select
- [ ] Search / filter within drive
- [ ] File content preview
- [ ] Sort by column headers

#### Drives (`web/src/pages/Drives.tsx`)

**Has:** list all drives with status cards (Synced/Syncing/Stopped),
file count per drive, outbox pending count, click card to browse,
bucket access card.

**Missing:**
- [ ] **Create drive** — `skyfs.driveCreate` RPC exists + defined in
  rpc.ts but never called from UI
- [ ] **Start/stop drive** — `skyfs.driveStart`/`driveStop` defined in
  rpc.ts but never called
- [ ] Delete drive — `skyfs.driveRemove` exists on backend, not in
  rpc.ts at all
- [ ] Storage usage stats per drive

#### Devices (`web/src/pages/Devices.tsx`)

**Has:** list devices with platform/location/version/last seen/P2P
addrs, "This Device" badge, invite button, copy device ID (hover icon).

**Missing:**
- [ ] **Remove device** — `skyfs.deviceRemove` exists on backend, not
  in rpc.ts
- [ ] Copy ID success feedback (toast/flash)

#### Settings (`web/src/pages/Settings.tsx`)

**Has:** identity address + copy icon, device peer ID, hostname,
authorized device count, version/commit/build date, uptime, RPC client
count, Skylink mode display, authorized devices list, listen addresses.

**Missing:**
- [ ] **Private/Network toggle is dead** — buttons at lines 173-182
  are styled as clickable but have zero onClick handlers
- [ ] S3 storage configuration
- [ ] Hostname change

#### KVStore (`web/src/pages/KVStore.tsx`)

**Has:** full CRUD (create/read/update/delete), JSON vs plaintext
detection, key list with A-Z sort, context menu delete, dirty state
tracking ("Unsaved" badge), namespace display, key count.

**Missing:**
- [ ] Namespace creation / switching
- [ ] Bulk delete
- [ ] Key export / import

#### Network (`web/src/pages/Network.tsx`)

**Has:** P2P topology graph, peer list with details (name, platform,
location, last seen), listen addresses, peer ID, mode, uptime.

**Missing:**
- [ ] **Connect to peer** — `skylink.connect` defined in rpc.ts but
  never called from any UI component
- [ ] Disconnect peer

#### Bucket (`web/src/pages/Bucket.tsx`)

**Has:** browse S3 prefixes, search/filter, breadcrumb navigation,
file type icons, object sizes. Entirely read-only.

**Missing:**
- [ ] **Delete S3 object** — `skyfs.s3Delete` exists on backend, not
  in rpc.ts
- [ ] Upload / download objects

### RPC Coverage

**49 total daemon RPC methods.** 15 actively called by UI, 8 defined
in `web/src/lib/rpc.ts` but never called, 26 not in the web client.

**Defined in rpc.ts but never called from UI:**

| Method | Notes |
|--------|-------|
| `skyfs.driveCreate` | Need "New Drive" form |
| `skyfs.driveStart` | Need toggle on DriveCard |
| `skyfs.driveStop` | Need toggle on DriveCard |
| `skyfs.syncStatus` | Could show per-drive sync detail |
| `skyfs.status` | Could show global sync status |
| `skyfs.approve` | Could add approval UI for pending devices |
| `skylink.connect` | Need address input on Network page |
| `skykv.get` / `skykv.list` | UI uses `getAll` instead (fine) |

**Not in rpc.ts at all (backend-only):**

| Method | Priority |
|--------|----------|
| `skyfs.put` | Must add (file upload) |
| `skyfs.get` | Must add (file download) |
| `skyfs.driveRemove` | Should add |
| `skyfs.deviceRemove` | Should add |
| `skyfs.s3Delete` | Should add |
| `skyfs.versions` | Nice-to-have (file version history) |
| `skyfs.syncActivity` | Must add (activity feed) |
| `skyfs.info` | Nice-to-have (store stats) |
| `skyfs.compact` | Defer (CLI sufficient) |
| `skyfs.gc` | Defer (CLI sufficient) |
| `skyfs.reset` | Defer (CLI sufficient, dangerous) |
| `skyfs.join` | Defer (invite flow uses CLI) |
| `skyfs.debugDump/List/Get` | Defer (dev-only) |
| `skyfs.driveState` | Nice-to-have |
| `skykv.sync` | Defer (auto-syncs) |
| `skylink.call/resolve/publish` | Defer (internal) |

### Blocking vs Non-Blocking

**Must-have before removing Cirrus:**

| Item | Backend work | Frontend work |
|------|-------------|---------------|
| File upload | `POST /upload` endpoint in `pkg/rpc/http.go` | Upload button + file input in FileBrowser |
| File download | `GET /download` endpoint in `pkg/rpc/http.go` | Download in context menu + BrowserTable |
| Drive creation | RPC exists, just wire UI | "New Drive" button + form on Drives page |
| Drive start/stop | RPCs exist, just wire UI | Toggle on DriveCard |
| Activity feed | Add `skyfs.syncActivity` to rpc.ts | New Activity page + SSE subscriptions |

**Should-have (improves parity, not blocking):**

| Item | Work |
|------|------|
| Device remove | Add `skyfs.deviceRemove` to rpc.ts, confirm dialog on Devices page |
| S3 object delete | Add `skyfs.s3Delete` to rpc.ts, confirm in Bucket context menu |
| Connect to peer | Wire `skylink.connect` with address input on Network page |
| Fix dead Skylink toggle | Add onClick handlers in Settings.tsx:173-182 or remove buttons |
| Drive delete | Add `skyfs.driveRemove` to rpc.ts, confirm on DriveCard |
| File versions | Add `skyfs.versions` to rpc.ts, version history panel in FileBrowser |

---

## Execution Plan

### Phase 1: Web UI Feature Gaps

#### 1a. File Upload

Add an upload button to the file browser toolbar. Browser `<input
type="file">` for file selection. POST the file to a new HTTP endpoint
on the daemon (`POST /upload?drive=X&path=Y`), which calls
`store.Put`. Show progress via SSE events that already exist
(`upload.start`, `upload.complete`).

**Files:**
- `pkg/rpc/http.go` — add `POST /upload` multipart handler
- `web/src/pages/FileBrowser.tsx` — add upload button + file input
- `web/src/lib/rpc.ts` — add upload helper (fetch, not JSON-RPC)

#### 1b. File Download

Add a download action to the file browser context menu and/or a
download button. Use a new HTTP endpoint (`GET /download?drive=X&path=Y`)
that calls `store.Get` and streams the decrypted file.

**Files:**
- `pkg/rpc/http.go` — add `GET /download` handler
- `web/src/components/files/BrowserContextMenu.tsx` — add "Download" item
- `web/src/components/files/BrowserTable.tsx` — add download click handler

#### 1c. Drive Creation UI

The RPC method `skyfs.driveCreate` already exists. Add a "New Drive"
button on the Drives page that opens a form (name + local path). Since
the browser can't pick local folders, accept the path as a text input
(the daemon validates it).

**Files:**
- `web/src/pages/Drives.tsx` — add "New Drive" button + modal/form
- `web/src/components/drives/NewDriveForm.tsx` — new component

#### 1d. Activity Feed

Add a page or panel showing real-time sync activity. Subscribe to SSE
events (`upload.*`, `download.*`, `sync.*`, `poll.*`) and display a
scrolling log. The daemon already emits `skyfs.syncActivity()` RPC —
use it for the initial state, then append live events.

**Files:**
- `web/src/pages/Activity.tsx` — new page
- `web/src/App.tsx` — add `/activity` route
- `web/src/components/Sidebar.tsx` — add Activity nav item

#### 1e. S3 Configuration / Onboarding

Keep initial S3 setup as CLI (`sky10 fs init`). The web UI is served by
the daemon, which needs S3 config to start — chicken-and-egg. The daemon
already starts without S3 for the HTTP server, but can't do anything
useful. A simple "Run `sky10 fs init` to get started" message in the web
UI is sufficient for v1.

**Files:**
- `web/src/pages/Settings.tsx` — add "not configured" state if no drives

### Phase 2: Tauri Systray App

A pre-built Tauri v2 binary (~3MB) that lives in the menu bar. Ships
alongside `sky10` — users never need Rust. We build it once per release
in CI or on a dev Mac.

**Menu items:**
1. **Open Web UI** → opens `http://localhost:9101` in default browser
2. **Restart Daemon** → runs `sky10 daemon restart`
3. **Quit** → runs `sky10 daemon stop`, exits tray app

**Tauri details:**
- Systray-only, no window (uses `ActivationPolicy::Accessory` on macOS
  to hide from Dock)
- Uses system WebKit — nothing bundled, ~3MB binary
- Template icon for light/dark menu bar adaptation
- Config is just a `tauri.conf.json` + ~20 lines of Rust for menu items
  that shell out to `sky10` CLI commands

**Project structure:**
```
tray/
├── src-tauri/
│   ├── Cargo.toml
│   ├── tauri.conf.json
│   └── src/
│       └── main.rs          # ~30 lines: systray + 3 menu items
├── src/
│   └── index.html           # empty (no window)
└── icons/
    └── icon.png             # menu bar template icon
```

**Build:** `cd tray && cargo tauri build` produces
`tray/src-tauri/target/release/bundle/macos/Sky10.app` (or just the
binary). Built once per release, checked into releases as an artifact.

**Distribution:** Ship alongside the CLI binary in GitHub releases and
Homebrew. The `sky10 daemon install` command can optionally register the
tray app as a login item via launchd.

### Phase 3: Daemon Management (CLI + launchd)

#### 3a. CLI Commands

- `sky10 open` — opens `http://localhost:9101` in default browser
- `sky10 daemon install` — writes launchd plist, loads agent
- `sky10 daemon uninstall` — unloads agent, removes plist
- `sky10 daemon status` — shows whether agent is loaded + PID
- `sky10 daemon restart` — `launchctl kickstart` or kill + relaunch
- `sky10 daemon stop` — `launchctl unload` (temporary, until next login)

**Files:**
- `commands/open.go` — new `sky10 open` command
- `commands/daemon.go` — install/uninstall/status/restart/stop
- `pkg/fs/launchd.go` — plist template + launchctl helpers

#### 3b. launchd Plist

`~/Library/LaunchAgents/ai.sky10.daemon.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>ai.sky10.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/bin/sky10</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/sky10/daemon.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/sky10/daemon.stderr.log</string>
</dict>
</plist>
```

### Phase 4: Remove Cirrus

Once phases 1-3 are done and working:

#### 4a. Delete files
- `rm -rf cirrus/`
- `rm docs/work/current/cirrus-macos-plan.md`

#### 4b. Update Go code (comments/strings)
- `commands/rpc.go:17` — remove "or Cirrus" from error message
- `pkg/fs/skyfs.go:37` — remove `"cirrus/0.4.1"` from comment
- `pkg/fs/op.go:42` — remove `"cirrus/0.4.1"` from comment
- `pkg/fs/outbox_worker.go:21` — change "push events to Cirrus" →
  "push events to caller"
- `pkg/fs/logbuf.go:115-118` — rewrite Cirrus pipe comments
- `pkg/fs/debug.go:32-33` — rewrite Cirrus pipe comment
- `docs/work/current/sync-algorithm-state.md:146` — remove "from Cirrus"
- `docs/work/current/reconciler-completeness.md:51,113` — remove
  Cirrus refs

#### 4c. Update build/config
- `Makefile` — remove `build-swift`, `test-skyfs-ui-macos` targets;
  update `.PHONY` and `test-skyfs` dependency
- `CLAUDE.md` — remove `cd cirrus/macos && swift test` from test list;
  remove `cirrus` from commit scope list
- `README.md` — remove `cirrus/` from architecture tree; update
  `make test` comment; add `tray/` to architecture tree

#### 4d. Update docs
- `docs/work/README.md` — remove cirrus plan from current table
- `docs/work/current/` — delete `cirrus-macos-plan.md`
- `docs/learned/dependencies.md` — remove fsnotify/cirrus reference
- `.claude/skills/release/SKILL.md` — rewrite: remove Cirrus version
  bumps, Swift build/restart steps, Homebrew `sky10-cirrus.rb`; add
  Tauri tray binary to release artifacts

---

## Execution Order

```
Phase 1a: File upload          ← most impactful gap
Phase 1b: File download        ← pairs with upload
Phase 1c: Drive creation UI    ← small, quick win
Phase 1d: Activity feed        ← valuable but deferrable
Phase 1e: Onboarding message   ← minimal "run sky10 fs init" message
Phase 2:  Tauri systray app    ← build once, ship binary
Phase 3:  CLI + launchd        ← sky10 open/daemon commands
Phase 4:  Delete Cirrus        ← cleanup after above is working
```

Phases 1a-1c are prerequisites for removing Cirrus. Phases 1d-1e, 2, 3
can proceed in parallel. Phase 4 happens last.

---

## What We're NOT Porting

- **File Provider extension** — macOS Finder integration. Complex,
  requires entitlements, app bundle. Low ROI given the web UI.
- **macOS notifications** — SSE events + web UI activity feed is
  sufficient. Can add browser Notification API later if wanted.
- **Storage provider presets** — Nice UX but not blocking. Users can
  look up their provider's endpoint.
- **Debug tools UI** — `sky10 fs compact`, `sky10 fs gc`, and CLI
  are sufficient for power users.
- **Poll interval config** — Default is fine. Can add to Settings
  page later if needed.
