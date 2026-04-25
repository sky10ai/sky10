---
created: 2026-04-25
model: gpt-5.5
---

# Lima Sandbox Hardening Follow-Up And Runtime Drift

This entry records the follow-up branch that landed after
[`24-Lima-VM-Hardening.md`](24-Lima-VM-Hardening.md). The April 24 work made
Lima sandbox access explicit and host-owned. This pass cleaned up remaining
hardening footguns, added host-side runtime RPCs for guest drift detection,
and surfaced guest runtime drift in the sandbox UI.

Landed commits:

- `35ba4877` `fix(sandbox): require hermes sky10 rpc config`
- `dea6d5a9` `fix(web): hide sandbox guest ip`
- `d775549b` `feat(sandbox): add runtime RPC methods`
- `1accc1d7` `feat(rpc): add system health alias`
- `b8bec0b2` `feat(web): surface sandbox runtime drift`

## Decisions

### Host-to-guest direct IP is acceptable

The April 24 audit originally treated lingering guest-IP fallbacks as a
hardening concern. The product decision changed: direct host-to-guest IP access
is acceptable when the host already owns the VM. The issue was the UI teaching
guest IPs as a first-class product surface.

What changed:

- removed guest IP display from sandbox list/detail UI
- removed UI URL fallback construction from `ip_address`
- kept backend/CLI guest-IP fallback paths in place

### OpenClaw gateway forwarding stays

The OpenClaw gateway forward remains intentional. It is host-local and part of
the managed OpenClaw runtime surface. Future work should document and test that
policy directly instead of treating the gateway forward as accidental exposure.

### Hermes bridge must not accept host RPC config

The Hermes bridge fallback from `sky10_rpc_url` to `host_rpc_url` was removed.
Generated configs already used guest-local `sky10`; the fallback was old
compatibility code that could silently revive the wrong model if a future
config bug reintroduced `host_rpc_url`.

Main files:

- [`pkg/sandbox/templates/hermes-sky10-bridge.py`](../../../../../pkg/sandbox/templates/hermes-sky10-bridge.py)
- [`templates/lima/hermes-sky10-bridge.py`](../../../../../templates/lima/hermes-sky10-bridge.py)
- [`pkg/agent/chat_websocket_hermes_integration_test.go`](../../../../../pkg/agent/chat_websocket_hermes_integration_test.go)

## Runtime RPC Surface

The branch added host-side sandbox runtime methods:

- `sandbox.runtime.status`
- `sandbox.runtime.upgrade`

These methods use the guest daemon over the known guest RPC endpoint. Status
reads guest `system.health` and `system.update.status`; upgrade triggers guest
`system.update`.

This was deliberately thin. The host does not duplicate the guest updater. It
asks the guest daemon what version it is running and asks that daemon to run
its own updater.

Main files:

- [`pkg/sandbox/runtime_update.go`](../../../../../pkg/sandbox/runtime_update.go)
- [`pkg/sandbox/rpc.go`](../../../../../pkg/sandbox/rpc.go)
- [`web/src/lib/rpc.ts`](../../../../../web/src/lib/rpc.ts)

## Health RPC Naming

`skyfs.health` had become the daemon health endpoint even though it returns
daemon-level fields such as version, uptime, HTTP address, RPC counts, and sync
health. The branch added `system.health` as the canonical method and kept
`skyfs.health` as a compatibility alias.

New callers should use `system.health`. Existing callers can keep using
`skyfs.health` until the compatibility alias is removed in a future release.

Main files:

- [`pkg/fs/rpc_handler.go`](../../../../../pkg/fs/rpc_handler.go)
- [`pkg/update/rpc.go`](../../../../../pkg/update/rpc.go)
- [`pkg/rpc/README.md`](../../../../../pkg/rpc/README.md)
- [`docs/work/todo/rpc-skyfs-health-removal.md`](../../../todo/rpc-skyfs-health-removal.md)

## Runtime Drift UI

The sandbox UI now polls runtime state and compares guest `sky10` against the
host daemon version.

Detail pages show:

- guest `sky10` version
- host `sky10` version
- guest update status
- runtime status
- drift badges such as `Guest stale`, `Version drift`, `Update staged`,
  `Runtime current`, and `Runtime unreachable`

The sandbox inventory shows a compact runtime badge for ready/running
sandboxes, so stale guests are visible before opening the detail page.

Main files:

- [`web/src/lib/sandboxRuntime.ts`](../../../../../web/src/lib/sandboxRuntime.ts)
- [`web/src/pages/SandboxDetail.tsx`](../../../../../web/src/pages/SandboxDetail.tsx)
- [`web/src/pages/Sandboxes.tsx`](../../../../../web/src/pages/Sandboxes.tsx)

## Live Existing-VM Cleanup

The live Hermes sandboxes on this machine were updated after the Hermes bridge
fallback was removed:

- `hermes-t8ry`
- `hermesdock`

Both were smoke tested through the host websocket path with:

```sh
sky10 sandbox smoke hermes-t8ry hermesdock \
  --message 'sky10 websocket smoke test: reply with ok' \
  --timeout 2m --ready-timeout 20s --health-timeout 3s
```

Both sandboxes responded successfully.

## Validation

Commands run during the branch:

```sh
go test ./commands -run 'TestHermesBridgeAssetSubscribesToSky10Events|TestPrepareLimaSharedDirHermes' -count=1
go test ./pkg/sandbox -run 'TestBundledHermesBridgeAssetRegistersWithSky10|TestPrepareHermesSharedDir' -count=1
go test ./pkg/agent -run Hermes -count=1
go test ./pkg/sandbox -count=1
go test ./pkg/fs -count=1
go test ./commands ./pkg/fs ./pkg/sandbox ./pkg/update -count=1
make build-web
git diff --check
```

`cargo fmt` and `rustfmt` were not available in the local environment for the
small tray health-RPC fallback change.

## Follow-Ups

Runtime drift should not require a user to click an upgrade button. The next
design direction is an automatic runtime reconciler with a single compatibility
bundle for:

- guest `sky10`
- OpenClaw
- `sky10-openclaw`
- Linux security packages

The OpenClaw glue should move out of Lima templates into a reusable bundle
layout, likely:

```text
external/runtimebundles/openclaw/
  manifest.json
  sky10-openclaw/
  docker/
```

The plugin glue is first-party today, but it should be easy to extract into its
own repo/package later for unmanaged OpenClaw installs that still want to
connect to sky10.

Concrete follow-up branches:

- move OpenClaw runtime bundle assets out of `templates/lima`
- add bundle manifest/version/hash metadata
- add automatic runtime reconcile on daemon start, sandbox start/reconnect, and
  periodic sweep
- add asset drift detection for staged OpenClaw/Hermes files
- add chat compatibility guard when guest runtime semantics are too old
- document/test the OpenClaw gateway forward as intentional host-local runtime
  access
