# Cirrus

Encrypted file sync for macOS. Native SwiftUI app backed by the sky10 Go engine.

## What It Does

- Menu bar tray icon with sync status
- Finder-like file browser (namespaces, sortable columns, search)
- Select a folder → continuous bidirectional sync to S3
- File Provider extension (sky10 files in Finder sidebar)
- Notifications for sync events and conflicts
- Pre-configured storage providers (Backblaze B2, Cloudflare R2, DigitalOcean, AWS, Wasabi, MinIO)

## Architecture

```
Cirrus (SwiftUI)
├── Menu bar + browser + settings
├── Communicates via JSON-RPC over Unix socket
└── Launches sky10 Go binary as sidecar

sky10 fs serve (Go)
├── Encrypted file storage engine
├── Sync daemon (watcher + poller)
└── All crypto happens here — Swift never sees plaintext
```

## Build

Requires Xcode and Go.

```bash
# From the repo root
make build              # build Go binary (bin/sky10)
make build-swift        # build Swift library
make test-skyfs-ui-macos  # run Swift tests

# Generate Xcode project (for running the app)
cd cirrus
brew install xcodegen   # one-time
xcodegen generate
open cirrus.xcodeproj
```

## Tests

```bash
make test-skyfs-ui-macos    # 43 Swift tests
make test-skyfs-cli         # 205 Go tests
make test                   # both
```
