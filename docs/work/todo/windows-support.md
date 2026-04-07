---
created: 2026-04-04
updated: 2026-04-05
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

- [ ] `commands/serve.go` — replace `syscall.SIGTERM` with `os.Interrupt` (cross-platform)
- [ ] `pkg/fs/pidfile.go` — replace `syscall.SIGTERM`/`SIGKILL`/`Signal(0)` with Windows process management (`os.FindProcess` + `Process.Kill`)
- [ ] `pkg/fs/debug.go` — replace `SIGUSR1` debug dump trigger with alternative (named event, HTTP endpoint, or file-based trigger)
- [ ] Create platform-specific files: `signal_unix.go` / `signal_windows.go` if needed

## 4. Path handling

**Mostly done — service-manager paths still need a Windows pass.**

- [ ] Audit daemon log paths in `commands/daemon_darwin.go` and `commands/daemon_linux.go` templates (these are platform-gated, so fine as-is — just verify)
- [ ] Verify `RuntimeDir()` works correctly on Windows (`%TEMP%\sky10`)

## 5. Build and release

- [ ] Add Windows targets to Makefile (`windows-amd64`, `windows-arm64`)
- [ ] Binary name needs `.exe` suffix
- [ ] Update `checksums.txt` generation to include Windows binaries
- [ ] Update `pkg/update/update.go` if asset naming needs adjustment
- [ ] Test cross-compilation from macOS/Linux

## 6. Installer

- [ ] Create PowerShell install script (equivalent of `install.sh`)
- [ ] `install.sh` already rejects Windows (lines 14-17) — add pointer to Windows installer
- [ ] Decide install location (`%LOCALAPPDATA%\sky10` or `%PROGRAMFILES%\sky10`)
- [ ] Add to PATH or create shell shim

## 7. Browser open

**Trivial.**

- [ ] `commands/ui.go` — add `case "windows": exec.Command("cmd", "/c", "start", url)`

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

## Priority order

1. RPC transport (everything depends on it)
2. Signal handling (serve command won't run without it)
3. Build targets + browser open (quick wins, enables manual testing)
4. Path fix (one-liner)
5. Windows Service (needed for production use)
6. Installer (needed for distribution)
7. Symlinks (feature parity)
8. CI (ongoing quality)
