---
created: 2026-04-04
updated: 2026-04-24
---

# Windows Support

Tracking all work needed to run sky10 on Windows.

## 1. RPC transport — Unix sockets on Windows

**Blocker: nothing works without this.**

- [ ] Decide transport: Go 1.23 native Unix sockets (requires Win10 1803+), Named Pipes (`go-winio`), or TCP localhost
- [ ] Update `pkg/rpc/server.go` — `net.Listen("unix", ...)` path
- [ ] Update `commands/rpc.go` — `net.Dial("unix", ...)` path
- [ ] Update `pkg/fs/pidfile.go` — socket path logic
- [ ] Update all RPC tests (`pkg/fs/rpc_test.go`, `pkg/fs/rpc_drive_test.go`)
- [ ] Verify socket cleanup on crash (no stale socket files)

## 2. Windows Service daemon management

**Required for `sky10 serve` to run as a background service.**

- [ ] Create `commands/daemon_windows.go` using `golang.org/x/sys/windows/svc`
- [ ] Implement install (register service with SC Manager)
- [ ] Implement uninstall (remove service)
- [ ] Implement start/stop/restart
- [ ] Implement status check
- [ ] Log to Windows Event Log or file (no syslog/journald)
- [ ] Existing `daemon_other.go` currently returns "unsupported" — keep as fallback for other OSes

## 3. Signal handling

**Unix signals don't exist on Windows.**

- [x] `commands/serve.go` — use platform-specific shutdown signal lists (`os.Interrupt` on Windows, `SIGTERM` + interrupt on Unix)
- [x] `pkg/fs/pidfile.go` — replace Unix signal probing with platform process helpers (`Process.Kill` plus Windows handle liveness checks on Windows)
- [ ] `pkg/fs/debug.go` — add an operator-triggered Windows debug dump alternative (named event, HTTP endpoint, or file-based trigger)
- [x] Create platform-specific files: `signals_unix.go` / `signals_windows.go` if needed

## 4. Path handling

**Mostly done — service-manager paths still need a Windows pass.**

- [ ] Audit daemon log paths in `commands/daemon_darwin.go` and `commands/daemon_linux.go` templates (these are platform-gated, so fine as-is — just verify)
- [ ] Verify `RuntimeDir()` works correctly on Windows (`%TEMP%\sky10`)
- [ ] Land the FS-specific normalization and case-collision work from
      [`../current/fs-windows-normalization-plan.md`](../current/fs-windows-normalization-plan.md)

## 5. Build and release

- [x] Add Windows targets to Makefile (`windows-amd64`, `windows-arm64`)
- [x] Binary name needs `.exe` suffix
- [x] Update `checksums.txt` generation to include Windows binaries
- [x] Update `pkg/update/update.go` if asset naming needs adjustment
- [x] Update `pkg/update` staged/install paths for Windows binary naming and menu entrypoint naming (`sky10.exe`, `sky10-menu.exe`)
- [ ] Add Windows `sky10-menu` release assets, or make the updater explicitly skip menu updates on Windows until the tray app is supported there
- [x] Test cross-compilation from macOS (`make platforms`); tag-time Linux verification covers Windows release assets

## 6. Installer

- [ ] Create PowerShell install script (equivalent of `install.sh`)
- [ ] `install.sh` already rejects Windows (lines 14-17) — add pointer to Windows installer
- [ ] Decide install location (`%LOCALAPPDATA%\sky10` or `%PROGRAMFILES%\sky10`)
- [ ] Add to PATH or create shell shim

## 7. Browser open

**Trivial.**

- [x] `commands/ui.go` — add `case "windows": exec.Command("cmd", "/c", "start", "", url)`

## 8. Symlinks

**Windows symlinks require Developer Mode or elevation.**

- [ ] Add runtime check for symlink capability
- [ ] Skip symlink tests on Windows if not available (`t.Skip`)
- [ ] Document requirement in install guide
- [ ] Evaluate whether symlink features can degrade gracefully (copy instead of link)

## 9. Testing

- [ ] Set up Windows CI (GitHub Actions `windows-latest`)
- [ ] Run `go test ./... -count=1` on Windows
- [ ] Guard Unix-only tests with `//go:build !windows` or `t.Skip`
- [ ] Verify filesystem watcher (`fsnotify`) behavior on NTFS

## 10. Agent Bootstrap Runtimes

**Current gap: `sky10 sandbox create ... --provider lima --template openclaw|hermes` is macOS-only because the templates use Lima `vz`.**

- [ ] Define the Windows equivalent of agent sandbox/bootstrap
- [ ] Decide whether that is WSL, a local host sandbox, or a packaged VM runtime
- [ ] Keep the higher-level agent bootstrap UX consistent even when the runtime differs by platform
- [ ] Extend managed-app archive installs for Windows runtime bundles (`pkg/apps`) — current Lima bundle support only targets Darwin/Linux asset naming and entrypoint layout
- [ ] Define Windows-compatible terminal access for sandbox detail pages — current PTY-backed `/rpc/sandboxes/{slug}/terminal` flow assumes Lima/host PTY semantics

## Priority order

1. RPC transport (everything depends on it)
2. Signal handling (serve command won't run without it)
3. Build targets + browser open (quick wins, enables manual testing)
4. Path fix (one-liner)
5. Windows Service (needed for production use)
6. Installer (needed for distribution)
7. Symlinks (feature parity)
8. CI (ongoing quality)
