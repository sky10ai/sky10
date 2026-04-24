---
created: 2026-04-04
updated: 2026-04-24
---

# Windows Support

Tracking remaining work needed to run sky10 well on Windows.

The completed April 23 readiness checkpoint is documented in
[`../past/2026/04/23-Windows-Support-Readiness.md`](../past/2026/04/23-Windows-Support-Readiness.md).
This file now tracks the gaps left after that checkpoint rather than repeating
the completed work log.

## Completed In The April 23 Checkpoint

- [x] Windows CLI release artifacts: `sky10-windows-amd64.exe` and
      `sky10-windows-arm64.exe`
- [x] Windows executable naming in update check, staging, and install paths:
      `sky10.exe` and `sky10-menu.exe`
- [x] Windows assets in release checksums and release verification
- [x] PowerShell installer: `install.ps1`
- [x] User install location: `%LOCALAPPDATA%\sky10\bin`
- [x] User PATH update from the PowerShell installer
- [x] `install.sh` points Windows users to the PowerShell installer
- [x] Windows tray/menu release assets:
      `sky10-menu-windows-amd64.exe` and `sky10-menu-windows-arm64.exe`
- [x] Start Menu shortcut and per-user tray autostart from the PowerShell
      installer
- [x] Per-user daemon bootstrap via the Windows `Run` registry key
- [x] `sky10 ui open` Windows browser launch
- [x] Platform-specific shutdown signal handling
- [x] Platform-specific stale daemon process cleanup
- [x] macOS-hosted Windows cross-build validation:
      `GOOS=windows GOARCH=amd64 go build ./...`, `make -B platforms`, and
      `make checksums`

## 1. Agent Hypervisor And Runtime — Not Implemented

**This is the largest remaining product gap.**

The Windows agent runtime/hypervisor does not exist yet. The current
OpenClaw/Hermes bootstrap path still assumes Lima and macOS virtualization
semantics. A Windows user can move closer to installing the CLI, daemon,
updater, and tray, but they still cannot install and run sky10 agents through a
simple one-click Windows runtime.

- [ ] Define the Windows equivalent of the Lima-backed agent runtime
- [ ] Decide whether the first runtime is WSL, Hyper-V, local host execution,
      Docker Desktop, or another packaged VM/hypervisor layer
- [ ] Implement `sky10 sandbox create ...` for Windows-compatible templates
- [ ] Provision guest `sky10` into the chosen Windows runtime
- [ ] Define Windows-compatible terminal access for sandbox detail pages
- [ ] Keep the higher-level agent bootstrap UX consistent across macOS,
      Linux, and Windows
- [ ] Extend managed-app archive installs for Windows runtime bundles
- [ ] Add explicit UI/CLI messaging when an agent template is unavailable on
      Windows because the hypervisor/runtime is not implemented yet

## 2. RPC Transport

**Blocker for full CLI/daemon parity.**

The core JSON-RPC client/server still uses Unix socket paths. Windows needs a
deliberate transport decision and implementation.

- [ ] Decide transport: native Unix sockets on supported Windows versions,
      named pipes (`go-winio`), localhost TCP, or a hybrid
- [ ] Update `pkg/rpc/server.go` to listen on the chosen Windows transport
- [ ] Update `commands/rpc.go` to dial the chosen Windows transport
- [ ] Update socket/path naming in `pkg/fs/pidfile.go` as needed
- [ ] Update RPC tests for Windows transport behavior
- [ ] Verify stale transport cleanup after daemon crash

## 3. Windows Service Daemon

The April 23 checkpoint added a per-user `Run` key fallback so the PowerShell
installer can start `sky10 serve` now. That is not a real Windows Service.

- [ ] Create a Service Control Manager implementation using
      `golang.org/x/sys/windows/svc`
- [ ] Register/unregister the daemon as a service
- [ ] Implement service start, stop, restart, and status
- [ ] Log to Windows Event Log or a clear per-user/per-machine log file
- [ ] Decide whether user installs keep using `Run` while machine-wide
      installs use SCM

## 4. Windows CI And Runtime Validation

Cross-compilation is passing, but actual Windows behavior still needs CI and a
real Windows filesystem/runtime.

- [ ] Add GitHub Actions `windows-latest` Go test coverage
- [ ] Run `go test ./... -count=1` on Windows
- [ ] Parse and smoke-test `install.ps1` in CI
- [ ] Build and verify the Tauri menu on Windows runners
- [ ] Verify `sky10-menu.exe` launches and can open the web UI
- [ ] Verify daemon startup/restart/status on Windows
- [ ] Verify filesystem watcher behavior on NTFS

## 5. Filesystem Policy Gaps

The FS Windows normalization work is mostly separate from installer/release
readiness. Keep it moving because Windows file semantics are a sync correctness
issue, not cosmetic platform polish.

- [ ] Land remaining work from
      [`../current/fs-windows-normalization-plan.md`](../current/fs-windows-normalization-plan.md)
- [ ] Add runtime check for Windows symlink capability
- [ ] Skip or adapt symlink tests on Windows when capability is unavailable
- [ ] Document symlink behavior and requirements
- [ ] Evaluate graceful degradation for unsupported symlink materialization
- [ ] Verify path-policy health output on real Windows

## 6. Installer Hardening

The PowerShell installer exists, but it has not been run locally in this
worktree because `pwsh` was unavailable on the development machine.

- [ ] Execute `install.ps1` on a clean Windows amd64 machine
- [ ] Execute `install.ps1` on a Windows arm64 machine or runner if available
- [ ] Confirm checksum failure behavior is clear
- [ ] Decide whether release assets should be Authenticode-signed
- [ ] Add uninstall behavior for the PowerShell path
- [ ] Decide whether to add `winget` after the installer path stabilizes

## Priority Order

1. RPC transport
2. Windows agent hypervisor/runtime
3. Windows CI
4. PowerShell installer smoke tests
5. Real Windows Service support
6. FS Windows policy validation on NTFS
7. Symlink capability and documentation
