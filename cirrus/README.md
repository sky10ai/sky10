# Cirrus

Encrypted file sync. Native desktop apps backed by the sky10 Go engine.

```
cirrus/
├── macos/       SwiftUI macOS app
├── ios/         (future)
├── windows/     (future)
└── linux/       (future)
```

## macOS

Menu bar tray icon, Finder-like file browser, folder sync, File Provider extension, notifications, provider presets (Backblaze B2, Cloudflare R2, DigitalOcean, AWS, Wasabi, MinIO).

### Build

```bash
make build              # Go binary (bin/sky10)
make build-swift        # Swift library
make test-skyfs-ui-macos  # Swift tests

# Xcode project
cd cirrus/macos
brew install xcodegen
xcodegen generate
open cirrus.xcodeproj
```

### Architecture

```
Cirrus macOS (SwiftUI)
├── Menu bar + browser + settings
├── JSON-RPC over Unix socket
└── Launches sky10 Go binary as sidecar

sky10 serve (Go)
├── Encrypted file storage + key-value store
├── Sync daemon (watcher + poller + snapshot exchange)
└── All crypto here — Swift never sees plaintext
```
