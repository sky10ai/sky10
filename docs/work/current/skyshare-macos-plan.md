# skyshare macOS — SwiftUI Desktop App

Status: not started
Created: 2026-03-14

## Goal

A native macOS app for skyfs. Menu bar tray icon for sync status, a full
Finder-like file browser for managing encrypted files, and a File Provider
extension so skyfs files appear directly in Finder's sidebar.

The Go skyfs engine runs as a child process. The Swift UI communicates with
it over a Unix domain socket using JSON-RPC.

## Architecture

```
┌─────────────────────────────────────────────────┐
│ skyshare.app (SwiftUI)                          │
│                                                  │
│  MenuBarExtra        — tray icon, sync status    │
│  NavigationSplitView — file browser, sharing     │
│  Settings            — bucket config, identity   │
│                                                  │
│  FileProvider Extension — skyfs in Finder sidebar │
│                                                  │
└──────────────┬──────────────────────────────────┘
               │ Unix domain socket (JSON-RPC)
               ▼
┌──────────────────────────┐
│ skyfs daemon (Go binary) │
│                          │
│  sync engine             │
│  crypto, chunking        │
│  S3 backend              │
│  ops log, manifest       │
└──────────────────────────┘
```

## Out of Scope (v1)

- iOS app (later — shares SwiftUI views but needs separate target)
- File Provider thumbnails/Quick Look (polish, v2)
- Sharing links (needs skylink relay, separate feature)
- Auto-update (Sparkle framework, add later)
- App Store distribution (start with direct download)

## Project Structure

```
skyshare/                           separate repo or sky10/skyshare/
├── skyshare.xcodeproj
├── skyshare/
│   ├── App.swift                   @main, MenuBarExtra + WindowGroup
│   ├── Models/
│   │   ├── SkyClient.swift         JSON-RPC client to Go backend
│   │   ├── FileNode.swift          file tree model
│   │   ├── SyncStatus.swift        sync state model
│   │   └── Identity.swift          user identity model
│   ├── Views/
│   │   ├── MenuBar/
│   │   │   ├── MenuBarView.swift   tray icon + dropdown
│   │   │   └── SyncStatusView.swift
│   │   ├── Browser/
│   │   │   ├── BrowserView.swift   NavigationSplitView root
│   │   │   ├── SidebarView.swift   namespace list
│   │   │   ├── FileListView.swift  file table with columns
│   │   │   ├── FileRowView.swift   single file row
│   │   │   └── DetailView.swift    file details + preview
│   │   ├── Settings/
│   │   │   ├── SettingsView.swift  bucket config, identity
│   │   │   └── AccountView.swift   identity display
│   │   └── Shared/
│   │       ├── SyncBadge.swift     sync status indicator
│   │       └── FormatHelpers.swift size/date formatting
│   ├── Services/
│   │   ├── DaemonManager.swift     start/stop Go process
│   │   ├── RPCClient.swift         JSON-RPC over Unix socket
│   │   └── FileWatcher.swift       observe backend events
│   └── Resources/
│       ├── Assets.xcassets         app icon, tray icons
│       └── skyfs                   embedded Go binary
├── FileProvider/
│   ├── FileProviderExtension.swift
│   ├── FileProviderItem.swift
│   └── FileProviderEnumerator.swift
└── skyshare-tests/
```

## Go Backend RPC Interface

The Go skyfs binary exposes a JSON-RPC server over a Unix domain socket.
The Swift app connects as a client. This keeps the crypto and storage logic
in Go while the UI is pure Swift.

```
RPC Methods:
  skyfs.init        { bucket, region, endpoint }  → { identity }
  skyfs.put         { path, local_path }          → { size, chunks }
  skyfs.get         { path, out_path }            → { size }
  skyfs.list        { prefix }                    → { files[] }
  skyfs.remove      { path }                      → { }
  skyfs.info        { }                           → { identity, file_count, total_size, namespaces }
  skyfs.sync        { dir, once }                 → { uploaded, downloaded, errors }
  skyfs.syncStart   { dir }                       → { }  (start daemon)
  skyfs.syncStop    { }                           → { }  (stop daemon)
  skyfs.status      { }                           → { syncing, last_sync, pending_ops, conflicts }
  skyfs.versions    { path }                      → { versions[] }
  skyfs.restore     { path, timestamp, out_path } → { size }
  skyfs.compact     { keep }                      → { ops_compacted }
  skyfs.gc          { dry_run }                   → { blobs_deleted, bytes_reclaimed }
```

Events (server → client push over same socket):
```
  sync.started      { }
  sync.progress     { file, bytes_transferred, bytes_total }
  sync.completed    { uploaded, downloaded, errors }
  sync.conflict     { path, device_a, device_b }
  file.changed      { path, type }
```

---

## Milestone 1: Go RPC Server

Expose skyfs operations over a Unix domain socket with JSON-RPC.

### Tasks

- [ ] `skyfs/rpc.go` — JSON-RPC server:
  - [ ] `RPCServer` struct wrapping a `Store`
  - [ ] Listen on Unix domain socket at `~/.skyfs/skyfs.sock`
  - [ ] Handle concurrent connections (one goroutine per client)
  - [ ] Register methods: `skyfs.list`, `skyfs.put`, `skyfs.get`, `skyfs.remove`,
        `skyfs.info`, `skyfs.status`, `skyfs.versions`, `skyfs.compact`, `skyfs.gc`
  - [ ] Use `net/rpc/jsonrpc` or implement minimal JSON-RPC 2.0
  - [ ] Request/response types as exported structs
- [ ] `skyfs/rpc_events.go` — event push:
  - [ ] `EventBus` with subscribe/publish
  - [ ] Events: `sync.started`, `sync.progress`, `sync.completed`, `sync.conflict`
  - [ ] Push events as JSON lines to connected clients
- [ ] `cmd/skyfs/main.go` — add `skyfs serve` command:
  - [ ] Start RPC server in foreground
  - [ ] Graceful shutdown on SIGTERM/SIGINT
  - [ ] Write socket path to stdout for client discovery
- [ ] Tests:
  - [ ] RPC list returns files
  - [ ] RPC put + get round-trip
  - [ ] RPC remove works
  - [ ] RPC info returns correct stats
  - [ ] Multiple concurrent clients
  - [ ] Server shuts down cleanly

### Acceptance

`skyfs serve` starts a JSON-RPC server on a Unix socket. Swift can connect
and call methods. Events push to connected clients.

---

## Milestone 2: Xcode Project + Menu Bar App Shell

Create the SwiftUI app with a menu bar tray icon. No file browsing yet —
just the tray icon and basic status.

### Tasks

- [ ] Create Xcode project: `skyshare`
  - [ ] macOS app target, SwiftUI lifecycle
  - [ ] Minimum deployment: macOS 14 (Sonoma)
  - [ ] Signing: Developer ID (direct distribution, not App Store)
- [ ] `App.swift`:
  - [ ] `@main` struct with `MenuBarExtra` scene
  - [ ] `WindowGroup` for the main browser window (hidden by default)
  - [ ] App icon in Assets.xcassets
- [ ] `MenuBarView.swift`:
  - [ ] Tray icon: cloud icon (SF Symbols: `cloud.fill`)
  - [ ] Sync status: synced ✓ / syncing ↻ / error ✗
  - [ ] Dropdown menu:
    - [ ] "Synced — 42 files" status line
    - [ ] "Open Sky Browser" → opens main window
    - [ ] "Sync Now" → trigger manual sync
    - [ ] "Preferences..." → open settings
    - [ ] Separator
    - [ ] "Quit skyshare"
- [ ] `SyncStatus.swift` model:
  - [ ] `enum SyncState { case synced, syncing, error, offline }`
  - [ ] Observable object for SwiftUI binding
- [ ] Tray icon changes based on sync state:
  - [ ] Synced: solid cloud
  - [ ] Syncing: cloud with arrows (animated)
  - [ ] Error: cloud with exclamation
  - [ ] Offline: cloud with slash
- [ ] Tests:
  - [ ] App launches without crash
  - [ ] Menu bar icon visible
  - [ ] Menu items render

### Acceptance

App shows a tray icon. Dropdown menu works. Clicking "Open Sky Browser"
opens a window (empty for now).

---

## Milestone 3: Daemon Manager + RPC Client

Connect the SwiftUI app to the Go backend.

### Tasks

- [ ] `DaemonManager.swift`:
  - [ ] Embed `skyfs` Go binary in app bundle (Resources/)
  - [ ] `start()` — launch Go process with `serve` command
  - [ ] `stop()` — send SIGTERM, wait for exit
  - [ ] Auto-restart if process crashes
  - [ ] Track process state: starting, running, stopped, error
  - [ ] Start on app launch, stop on app quit
- [ ] `RPCClient.swift`:
  - [ ] Connect to Unix domain socket at `~/.skyfs/skyfs.sock`
  - [ ] JSON-RPC 2.0 client:
    - [ ] `call<T: Decodable>(_ method: String, params: Encodable?) async throws -> T`
  - [ ] Reconnect on disconnect with backoff
  - [ ] Timeout: 30 seconds per call
- [ ] `SkyClient.swift` — high-level API wrapping RPCClient:
  - [ ] `listFiles(prefix: String) async throws -> [FileNode]`
  - [ ] `getInfo() async throws -> StoreInfo`
  - [ ] `syncOnce(dir: String) async throws -> SyncResult`
  - [ ] `getStatus() async throws -> SyncStatus`
  - [ ] `getVersions(path: String) async throws -> [FileVersion]`
- [ ] `FileWatcher.swift` — listen for server-push events:
  - [ ] Parse JSON lines from socket
  - [ ] Publish events via Combine/AsyncStream
  - [ ] Update SyncStatus model on sync events
- [ ] Update `MenuBarView`:
  - [ ] Status line reads from SkyClient
  - [ ] "Sync Now" calls SkyClient.syncOnce
  - [ ] Tray icon reflects real sync state
- [ ] Tests:
  - [ ] DaemonManager starts and stops Go process
  - [ ] RPCClient connects and calls a method
  - [ ] SkyClient.listFiles returns data
  - [ ] Event stream receives sync events

### Acceptance

App launches Go backend automatically. Menu bar shows real sync status
from the backend. "Sync Now" triggers a real sync.

---

## Milestone 4: File Browser — Sidebar + File List

The main window with a Finder-like three-column layout.

### Tasks

- [ ] `BrowserView.swift` — root view:
  - [ ] `NavigationSplitView` with sidebar, content, detail columns
  - [ ] Column widths: sidebar 200pt, content flexible, detail 300pt
- [ ] `SidebarView.swift`:
  - [ ] List of namespaces (loaded from SkyClient.getInfo)
  - [ ] Each namespace shows: name, file count, icon
  - [ ] "All Files" item at top
  - [ ] Selection drives the file list
  - [ ] SF Symbols for namespace icons:
    - [ ] Default: `folder.fill`
    - [ ] Can customize per namespace later
- [ ] `FileListView.swift`:
  - [ ] `Table` (macOS 13+) with columns: Name, Size, Modified, Status
  - [ ] Sort by clicking column headers
  - [ ] Selection: single and multi-select
  - [ ] Double-click → open/preview file
  - [ ] Right-click context menu:
    - [ ] "Download" / "Open"
    - [ ] "Copy Path"
    - [ ] "Show Versions..."
    - [ ] "Delete"
  - [ ] Toolbar:
    - [ ] Search field (filter by name)
    - [ ] Upload button (open file picker)
    - [ ] Refresh button
    - [ ] View toggle (list / grid) — list only for v1
- [ ] `FileRowView.swift`:
  - [ ] File icon based on extension (SF Symbols or NSWorkspace)
  - [ ] Name, size (formatted), modified date
  - [ ] Sync badge: ✓ synced, ↻ syncing, ↓ downloading, ↑ uploading
- [ ] `FileNode.swift` model:
  - [ ] `id`, `path`, `name`, `size`, `modified`, `checksum`, `namespace`
  - [ ] `isDirectory` (derived from path structure)
  - [ ] `syncState` enum
- [ ] Directory navigation:
  - [ ] Click directory → navigate into it (update file list)
  - [ ] Breadcrumb path bar at top of file list
  - [ ] Back button / keyboard shortcut (Cmd+[)
- [ ] Tests:
  - [ ] File list loads and displays files
  - [ ] Sorting works for all columns
  - [ ] Search filters correctly
  - [ ] Context menu actions fire

### Acceptance

Three-column browser shows namespaces on the left, files in the center.
Files sortable, searchable. Context menu works. Directory navigation works.

---

## Milestone 5: File Operations

Upload, download, delete, and preview files from the browser.

### Tasks

- [ ] Upload:
  - [ ] Toolbar "Upload" button → NSOpenPanel (file picker)
  - [ ] Drag & drop files into the file list area
  - [ ] Upload via SkyClient.put → shows progress in file row
  - [ ] After upload, refresh file list
- [ ] Download:
  - [ ] Double-click or "Download" context menu
  - [ ] NSSavePanel for destination (or download to ~/Downloads/)
  - [ ] Download via SkyClient.get → shows progress
  - [ ] Open file after download (NSWorkspace.open)
- [ ] Delete:
  - [ ] Context menu "Delete" → confirmation alert
  - [ ] SkyClient.remove → refresh file list
  - [ ] Cmd+Delete keyboard shortcut
- [ ] Preview:
  - [ ] Select file → detail panel shows:
    - [ ] File name, size, modified date
    - [ ] Namespace, checksum (truncated)
    - [ ] Number of chunks
    - [ ] Quick Look preview for common types (text, images, PDFs)
  - [ ] Download to temp directory for preview, clean up on deselect
- [ ] Progress overlay:
  - [ ] During upload/download, show progress bar in the file row
  - [ ] Or: bottom bar showing "Uploading report.pdf — 45%"
- [ ] Error handling:
  - [ ] Network error → alert with retry option
  - [ ] File not found → remove from list, show toast
- [ ] Tests:
  - [ ] Upload adds file to remote
  - [ ] Download creates local file
  - [ ] Delete removes from remote
  - [ ] Progress callback fires during transfer

### Acceptance

Users can upload, download, delete, and preview files from the browser.
Drag & drop works. Progress visible during transfers.

---

## Milestone 6: Settings + Onboarding

First-run setup and preferences.

### Tasks

- [ ] `SettingsView.swift` (Settings scene):
  - [ ] Tab: General
    - [ ] Sync directory path (with folder picker)
    - [ ] Launch at login toggle
    - [ ] Sync interval (polling frequency)
  - [ ] Tab: Storage
    - [ ] S3 bucket name
    - [ ] Region
    - [ ] Endpoint URL
    - [ ] Test connection button
  - [ ] Tab: Account
    - [ ] Identity: sky10://k1_... (copyable)
    - [ ] Public key display
    - [ ] Export identity (save key file)
- [ ] First-run onboarding flow:
  - [ ] Step 1: "Welcome to skyshare" — explain encrypted sync
  - [ ] Step 2: Storage setup — bucket, region, endpoint, credentials
  - [ ] Step 3: Identity — generate or import keypair
  - [ ] Step 4: Sync folder — pick local directory
  - [ ] Step 5: "You're ready" — start first sync
  - [ ] Store setup state in UserDefaults
  - [ ] Show onboarding if `~/.skyfs/config.json` doesn't exist
- [ ] Launch at login:
  - [ ] Use `SMAppService` (macOS 13+) for login item registration
  - [ ] Toggle in settings
- [ ] Credentials:
  - [ ] Store S3 credentials in macOS Keychain (not in config file)
  - [ ] `KeychainHelper.swift` for read/write
  - [ ] Pass to Go backend via environment variables on launch
- [ ] Tests:
  - [ ] Settings save and persist
  - [ ] Onboarding flow completes
  - [ ] Keychain read/write works
  - [ ] Connection test reports success/failure

### Acceptance

New user can set up skyshare from scratch via onboarding. Settings persist.
Credentials stored securely in Keychain.

---

## Milestone 7: File Provider Extension

Make skyfs appear as a location in Finder's sidebar.

### Tasks

- [ ] Create File Provider Extension target in Xcode
- [ ] `FileProviderExtension.swift`:
  - [ ] Subclass `NSFileProviderReplicatedExtension`
  - [ ] Connect to Go backend via same Unix socket RPC
  - [ ] `item(for identifier:)` — fetch file metadata
  - [ ] `fetchContents(for itemIdentifier:)` — download + decrypt via RPC
  - [ ] `createItem(basedOn:)` — upload new file via RPC
  - [ ] `modifyItem(identifier:)` — re-upload modified file
  - [ ] `deleteItem(identifier:)` — remove via RPC
- [ ] `FileProviderItem.swift`:
  - [ ] Map skyfs `FileEntry` to `NSFileProviderItem`
  - [ ] Properties: filename, size, modified, parent, content type
  - [ ] Sync state: `.uploaded`, `.downloading`, `.uploading`
- [ ] `FileProviderEnumerator.swift`:
  - [ ] Enumerate root → list namespaces as folders
  - [ ] Enumerate namespace → list files
  - [ ] Support pagination for large directories
  - [ ] Incremental changes via `currentSyncAnchor` + ops log
- [ ] Domain registration:
  - [ ] Register file provider domain on app launch
  - [ ] `NSFileProviderManager.add(domain:)` → "Sky" appears in Finder sidebar
  - [ ] Remove domain on app uninstall
- [ ] Conflict handling:
  - [ ] If local and remote differ, present conflict resolution UI
  - [ ] Or: automatic LWW (matching skyfs default)
- [ ] Tests:
  - [ ] Extension loads and enumerates root
  - [ ] File download via extension works
  - [ ] File upload via Finder drag-and-drop works
  - [ ] Changes in Finder reflect in skyfs (and vice versa)

### Acceptance

"Sky" appears in Finder sidebar. Users can browse, open, save, and delete
encrypted files directly in Finder. Changes sync automatically.

---

## Milestone 8: Sync Status + Notifications

Real-time sync feedback throughout the app.

### Tasks

- [ ] Sync activity view:
  - [ ] Bottom bar in browser window: "Syncing 3 files..."
  - [ ] Per-file progress in file list (badge or progress bar)
  - [ ] Animation on tray icon during sync
- [ ] macOS notifications:
  - [ ] "Sync complete — 5 files updated"
  - [ ] "Sync conflict — report.pdf modified on 2 devices"
  - [ ] "Sync error — connection to S3 failed"
  - [ ] Use `UserNotifications` framework
  - [ ] Configurable in settings (enable/disable per type)
- [ ] Conflict resolution UI:
  - [ ] Alert: "report.pdf was modified on both devices"
  - [ ] Options: "Keep Latest", "Keep Both", "Choose..."
  - [ ] "Choose..." → side-by-side diff (for text files)
- [ ] Activity log:
  - [ ] View in settings or separate window
  - [ ] List of recent sync events with timestamps
  - [ ] "Uploaded report.pdf (2.3 MB)"
  - [ ] "Downloaded notes.md (1.2 KB)"
  - [ ] "Conflict resolved: meeting.md (kept latest)"
- [ ] Tests:
  - [ ] Notifications fire on sync events
  - [ ] Conflict alert appears
  - [ ] Activity log populates

### Acceptance

Users see real-time sync progress. Conflicts prompt for resolution.
Notifications inform about sync activity.

---

## Milestone 9: Polish + Performance

Final polish for a shippable v1.

### Tasks

- [ ] App icon:
  - [ ] Design app icon (cloud + lock motif)
  - [ ] All required sizes (16, 32, 128, 256, 512, 1024)
  - [ ] Tray icon: template image for light/dark mode
- [ ] Keyboard shortcuts:
  - [ ] Cmd+N → new file (from template or empty)
  - [ ] Cmd+O → open/download selected file
  - [ ] Cmd+Delete → delete selected file
  - [ ] Cmd+R → refresh file list
  - [ ] Cmd+, → settings
  - [ ] Cmd+F → focus search field
- [ ] Performance:
  - [ ] Lazy loading for large file lists (1000+ files)
  - [ ] Background refresh (don't block UI on RPC calls)
  - [ ] Cache file list locally for instant display on launch
- [ ] DMG installer:
  - [ ] Create DMG with app + Applications symlink
  - [ ] Background image with drag instructions
  - [ ] Code sign with Developer ID
  - [ ] Notarize with Apple
- [ ] Accessibility:
  - [ ] VoiceOver labels on all interactive elements
  - [ ] Keyboard navigation throughout
- [ ] Error states:
  - [ ] "No connection" empty state in browser
  - [ ] "Backend not running" with "Start" button
  - [ ] "Not configured" with link to settings
- [ ] Tests:
  - [ ] Launch time < 1 second
  - [ ] File list renders 1000+ files without lag
  - [ ] All keyboard shortcuts work
  - [ ] DMG installs correctly

### Acceptance

App is polished, signed, notarized, and distributed via DMG. Keyboard
accessible. Performs well with large file collections.

---

## Dependencies (Swift)

```
Apple frameworks (no external deps):
  SwiftUI                   UI framework
  FileProvider              Finder integration
  UserNotifications         system notifications
  ServiceManagement         launch at login (SMAppService)
  Security                  Keychain access
  UniformTypeIdentifiers    file type detection
  QuickLookUI               file preview

Go binary:
  Embedded in app bundle as Resources/skyfs
  Communicates via Unix domain socket (JSON-RPC 2.0)
```

No third-party Swift dependencies. All Apple frameworks.

---

## Order of Implementation

```
1. Go RPC server           enables all UI work
2. Xcode project + menu bar   visible app shell
3. Daemon manager + RPC client  connected to backend
├── 1-3 are sequential (each depends on the last)
4. File browser              core UI value
5. File operations           upload/download/delete
├── 4-5 are the main feature work
6. Settings + onboarding     first-run experience
7. File Provider extension   Finder integration
├── 6-7 can parallelize
8. Sync status + notifications  real-time feedback
9. Polish + distribution       ship it
```

Milestones 1-3 establish the foundation. Milestones 4-5 deliver the core
file browser. Milestones 6-9 are polish and platform integration.

---

## V2 Thoughts (macOS app)

- **Quick Look previews** in Finder via File Provider thumbnails
- **Share extension** — share files from other apps into skyfs
- **Spotlight integration** — search encrypted file names via Core Spotlight
- **Touch Bar support** (if still relevant)
- **iOS companion app** — shared SwiftUI views, selective sync only
- **Drag from skyshare to other apps** — drag a file out of the browser
  to Desktop, Mail, etc. (download on drag, provide NSItemProvider)
