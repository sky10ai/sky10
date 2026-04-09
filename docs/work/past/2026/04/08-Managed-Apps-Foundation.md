---
created: 2026-04-08
model: gpt-5.4
---

# Managed Apps Foundation

sky10 now has a dedicated managed-app layer for optional helper
binaries. This keeps third-party tools like OWS separate from sky10's
own self-update flow, while giving both the CLI and the web UI a common
place to install, upgrade, inspect, and remove helper executables.

## Why

OWS started as wallet-specific install code, but that shape does not
scale. We already needed install/update/version management for OWS, and
the same machinery will be needed again for tools like `mkcert` and
`lima`.

The design goal for this pass was:

- keep `pkg/update` focused on replacing the sky10 binary itself
- move helper-binary lifecycle into a reusable package
- expose a power-user surface in a hidden `/settings/apps` route
- add a matching `sky10 apps` CLI
- store managed binaries by version under `~/.sky10/apps/`

## What Landed

### `pkg/apps`

Managed helper binaries now live in `pkg/apps/` instead of being owned
by `pkg/wallet/`.

The package currently provides:

- app registry metadata, starting with `ows`
- GitHub latest-release lookup and platform asset selection
- install, upgrade, status, and uninstall operations
- normalized version comparison so `ows --version` output matches GitHub
  tags cleanly
- managed install metadata via `current.json`
- legacy migration from the older direct `~/.sky10/bin/ows` layout

The on-disk layout is now:

```text
~/.sky10/apps/<app>/versions/<version>/<exe>
~/.sky10/apps/<app>/current.json
~/.sky10/bin/<app>
```

`~/.sky10/bin/<app>` remains the stable entrypoint, while the real
binary payload is stored in the versioned install tree.

App data was intentionally left alone. For now, helpers like Lima keep
using their upstream/default state paths instead of being relocated into
`~/.sky10/apps/<app>/`.

### CLI

sky10 now has a top-level managed-app command group:

- `sky10 apps list`
- `sky10 apps status <app>`
- `sky10 apps install <app>`
- `sky10 apps upgrade <app>`
- `sky10 apps update <app>` as an alias for `upgrade`
- `sky10 apps uninstall <app>`

This gives helper binaries a package-manager-style surface without
overloading `sky10 update`, which remains reserved for sky10 itself.

### Web UI

The management UI lives at `/settings/apps` and is intentionally hidden
from the main Settings navigation.

That page currently manages the OWS binary only:

- installed vs not installed state
- managed vs external PATH install detection
- current version and latest version
- active path and managed install path
- install/update/delete actions
- download progress via the existing wallet install event stream

The wallet UX itself stays on the main `/settings` page. `/settings/apps`
is only for binary lifecycle management.

## Key Decisions

- **Self-update stays separate**: `pkg/update` and `sky10 update` are
  still dedicated to sky10's own staged binary replacement flow.
- **Versioned installs under `apps/`**: helper binaries are immutable
  payloads stored under `versions/<version>/`, with a stable bin
  entrypoint pointing at the active version.
- **Default app data paths for now**: mutable helper state was not moved
  under `~/.sky10/apps/`; only binaries and metadata are managed there.
- **Power-user UI, not consumer UI**: app management is exposed under a
  hidden `/settings/apps` route rather than on the main Settings page.

## Commit Trail

- `5215601` `feat(settings): add hidden apps management route`
- `1ed7471` `feat(wallet): manage installed ows binary`
- `25987bd` `feat(apps): add managed apps cli`
- `3001098` `fix(apps): normalize managed app versions`
- `84c9977` `feat(apps): store managed binaries by version`

## Main Files

- `pkg/apps/apps.go`
- `commands/apps.go`
- `pkg/wallet/ows.go`
- `pkg/wallet/rpc.go`
- `web/src/pages/SettingsApps.tsx`
- `web/src/App.tsx`
