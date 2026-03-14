---
created: 2026-03-14
model: claude-opus-4-6
---

# skyshare macOS — SwiftUI Desktop App

## Problems Solved

### Milestone 1: Go RPC Server
- JSON-RPC 2.0 server over Unix domain socket (`~/.skyfs/skyfs.sock`)
- Methods: list, put, get, remove, info, status, versions, compact, gc
- Concurrent client connections (goroutine per client)
- Event bus for server-push (logged, not yet pushed to clients)
- `skyfs serve` CLI command
- 7 RPC tests: list, put, get, remove, info, status, method-not-found

### Milestone 2: Menu Bar App Shell
- `@main` struct with `MenuBarExtra` scene (tray icon)
- `WindowGroup` for browser window + `Settings` scene
- Tray icon changes based on sync state (SF Symbols: cloud variants)
- Dropdown: status, Open Browser, Sync Now, Preferences, Quit

### Milestone 3: Daemon Manager + RPC Client
- `DaemonManager`: start/stop Go skyfs process, auto-restart on crash
- `RPCClient`: JSON-RPC 2.0 over Unix domain socket (async/await)
- `SkyClient`: high-level API (listFiles, putFile, getFile, removeFile, getInfo)
- Connect to backend on app launch, show real sync status

### Milestone 4: File Browser
- `NavigationSplitView`: sidebar (namespaces) + file table + detail
- `SidebarView`: namespace list with per-type SF Symbol icons
- `FileListView`: sortable `Table` with Name, Size, Modified, Namespace columns
- Context menu: Download, Copy Path, Delete
- Search filtering by filename
- Empty state with guidance text

### Milestone 5: File Operations
- Upload via NSOpenPanel or toolbar button
- Download via NSSavePanel from context menu or double-click
- Delete with confirmation
- Detail panel: file metadata, checksum, chunk count, actions

### Milestone 6: Settings
- Tabbed settings: General, Storage, Account
- General: sync directory picker, launch at login, poll interval
- Storage: bucket, region, endpoint, test connection
- Account: identity display (sky://k1_...), file count, total size
- `@AppStorage` for persistence

## Decisions Made

- **Unix domain socket for IPC** — simpler than HTTP, lower overhead, naturally scoped to the local machine. Socket at `~/.skyfs/skyfs.sock`.
- **JSON-RPC 2.0** — standard protocol, easy to debug (`echo '{"jsonrpc":"2.0","method":"skyfs.list","params":{"prefix":""},"id":1}' | socat - UNIX-CONNECT:~/.skyfs/skyfs.sock`)
- **Events logged, not pushed** — event broadcasting to RPC connections caused response interleaving. Deferred to separate event socket or polling.
- **Zero third-party Swift deps** — all Apple frameworks. Keeps the app lightweight and avoids dependency management overhead.
- **RPCClient as actor** — thread-safe by design, all socket I/O serialized.

## Files Created

```
Go:
  skyfs/rpc.go, rpc_test.go         JSON-RPC server + tests

Swift:
  skyshare/skyshare/App.swift                         app entry point
  skyshare/skyshare/Models/AppState.swift              central observable state
  skyshare/skyshare/Models/SyncState.swift             sync state enum
  skyshare/skyshare/Models/FileNode.swift              file model + StoreInfo
  skyshare/skyshare/Services/DaemonManager.swift       Go process lifecycle
  skyshare/skyshare/Services/RPCClient.swift           JSON-RPC client
  skyshare/skyshare/Services/SkyClient.swift           high-level API
  skyshare/skyshare/Views/MenuBar/MenuBarView.swift    tray dropdown
  skyshare/skyshare/Views/Browser/BrowserView.swift    three-column layout
  skyshare/skyshare/Views/Browser/SidebarView.swift    namespace sidebar
  skyshare/skyshare/Views/Browser/FileListView.swift   sortable file table
  skyshare/skyshare/Views/Browser/FileRowView.swift    file row with icon
  skyshare/skyshare/Views/Browser/DetailView.swift     file detail panel
  skyshare/skyshare/Views/Settings/SettingsView.swift  preferences
```

## Test Count

131 Go tests (7 new RPC tests). SwiftUI views need Xcode project to test.
