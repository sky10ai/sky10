---
created: 2026-04-23
updated: 2026-04-23
model: gpt-5.5
---

# Windows Support Readiness

The April 23 Windows readiness checkpoint moved sky10 from "mostly Unix-shaped"
toward a downloadable Windows build with a first-pass install path. It did not
make Windows feature-complete, and it did not implement the Windows agent
hypervisor/runtime story.

## What Changed

### 1. CLI release artifacts now include Windows

The release build surface now produces:

- `sky10-windows-amd64.exe`
- `sky10-windows-arm64.exe`

`make platforms` and `make checksums` include those artifacts, and
`.github/workflows/verify-release.yml` verifies Windows CLI release assets
against reproducible source builds.

The updater also learned platform-aware executable names, so Windows update
staging and install paths use `sky10.exe` and `sky10-menu.exe` instead of
Unix-style extensionless names.

### 2. Windows has a PowerShell bootstrap path

`install.ps1` is the first Windows installer path. It installs into the
per-user location:

```text
%LOCALAPPDATA%\sky10\bin
```

It downloads the latest Windows CLI asset, downloads the Windows tray/menu
asset when available, verifies checksums from the release manifests, updates
the user PATH, creates a Start Menu shortcut for `sky10-menu.exe`, and registers
per-user startup entries in:

```text
HKCU\Software\Microsoft\Windows\CurrentVersion\Run
```

The shell installer now redirects Windows users to the PowerShell installer.

### 3. The tray/menu release workflow now targets Windows

The Tauri menu workflow now builds and verifies:

- `sky10-menu-windows-amd64.exe`
- `sky10-menu-windows-arm64.exe`

The tray app already used an HTTP RPC path, so it was closer to Windows than
the CLI daemon RPC path. The Windows `cmd start` browser invocation was fixed
to pass an empty title argument before the URL.

### 4. Daemon lifecycle has a Windows user-mode fallback

`commands/daemon_windows.go` adds a first-pass Windows daemon management path.
It is intentionally per-user and pragmatic:

- `daemon install` registers `sky10 serve` in the current user's `Run` key
- `daemon restart` stops the stale PID target and starts `sky10.exe serve`
- `daemon status` checks the HTTP health endpoint
- `daemon uninstall` removes the user startup entries

This is not the final Windows Service implementation. It is enough for a
download-and-run user install, but not enough for enterprise/service-manager
parity.

### 5. Unix signal assumptions were split by platform

Daemon shutdown and stale-process handling now use platform files instead of
assuming Unix signal semantics everywhere. Windows uses process handles for
liveness checks and `Process.Kill` for stale daemon cleanup.

## What Is Still Not Implemented

### 1. The Windows hypervisor/runtime is still missing

This is the biggest product gap.

The current agent bootstrap story still depends on Lima templates and macOS
virtualization assumptions. There is no Windows equivalent yet for:

- choosing WSL, Hyper-V, local host execution, or another packaged VM runtime
- creating OpenClaw/Hermes sandboxes on Windows
- provisioning guest `sky10` into a Windows-supported runtime
- host-to-guest terminal behavior on Windows
- one-click Windows agent install/bootstrap parity

Until this lands, Windows can move toward CLI, daemon, updater, and tray
readiness, but it is not ready for the full "download sky10 and install agents"
product goal.

### 2. RPC transport is still a blocker

The daemon and CLI still rely on Unix socket RPC paths in the core RPC client
and server. Windows support needs a deliberate transport decision: native Unix
sockets on supported Windows versions, named pipes, or localhost TCP.

### 3. Real Windows Service support remains

The per-user `Run` key flow is useful for the installer, but a production
Windows daemon should still support Service Control Manager registration,
start/stop/status, logging, and uninstall behavior.

### 4. Windows CI and runtime validation remain

The branch cross-compiles Windows Go packages from macOS and extends GitHub
release workflows, but it still needs actual `windows-latest` test coverage for:

- `go test ./... -count=1`
- PowerShell installer parsing/execution
- tray/menu build and launch
- filesystem watcher behavior on NTFS
- path, symlink, and case-collision behavior on a real Windows filesystem

## Validation Done

The readiness checkpoint was validated locally with:

```text
make build-web
go test ./pkg/update ./commands ./pkg/fs -count=1
make check
go test ./... -count=1
GOOS=windows GOARCH=amd64 go build ./...
make -B platforms
make checksums
```

`build-menu.yml` was YAML-parsed locally. Local validation could not run the
PowerShell installer or Tauri menu binary because this development machine did
not have `pwsh` or `cargo` installed.

## Practical State After This Checkpoint

Windows is now much closer to a user-installable CLI/tray product:

- release artifacts have Windows names
- the updater knows Windows executable names
- the installer path exists
- tray assets are part of the release workflow
- daemon startup has a per-user fallback

But Windows is not done. The next high-value work is still RPC transport and
the Windows agent runtime/hypervisor decision. The hypervisor gap is the one
that keeps Windows from satisfying the product constraint that a Windows user
can install sky10 agents through a simple, mostly one-click flow.
