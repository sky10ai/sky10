---
created: 2026-04-25
model: gpt-5.5
---

# Sandbox Runtime Bundles

This entry records the follow-up branch that moved first-party sandbox runtime
payloads out of Lima template directories and into reusable runtime bundle
directories under `external/runtimebundles`.

The work followed the runtime drift discussion in
[`25-Lima-Sandbox-Hardening-Followup-And-Runtime-Drift.md`](25-Lima-Sandbox-Hardening-Followup-And-Runtime-Drift.md).
That earlier note proposed a broader runtime reconciler. This branch kept the
scope narrower: move the payload sources, keep guest paths stable, and defer
automatic reconcile/autoupdate policy.

Landed commits:

- `0813ea0a` `refactor(sandbox): move openclaw assets to runtime bundle`
- `bb8c7e01` `docs(work): defer sandbox runtime reconcile`
- `4bb5d562` `refactor(sandbox): move hermes assets to runtime bundle`

## What Moved

OpenClaw runtime payloads moved to:

```text
external/runtimebundles/openclaw/
  manifest.json
  docker/
    Dockerfile
    entrypoint.sh
  sky10-openclaw/
    package.json
    openclaw.plugin.json
    src/
```

Hermes runtime payloads moved to:

```text
external/runtimebundles/hermes/
  manifest.json
  bridge/
    hermes-sky10-bridge.py
  docker/
    Dockerfile
    entrypoint.sh
```

The old duplicate copies were removed from `templates/lima`, and the embedded
copies were removed from `pkg/sandbox/templates`.

## What Stayed In Lima Templates

Lima VM bootstrap assets stayed in the Lima template locations:

- `*-sky10.yaml`
- `*.dependency.sh`
- `*.system.sh`
- `*.user.sh`
- `update-lima-hosts.sh`

The split is intentionally by responsibility:

- Lima templates describe how to boot and provision the VM.
- Runtime bundles carry first-party agent runtime glue and Docker runtime
  payloads staged into the sandbox state directory.

## Guest Paths Stayed Stable

No guest-facing path migration was introduced.

OpenClaw still stages to:

- `/sandbox-state/plugins/openclaw-sky10-channel`
- `/sandbox-state/runtime/openclaw`

Hermes still stages to:

- `/sandbox-state/hermes-sky10-bridge.py`
- `/sandbox-state/runtime/hermes`

That means new VMs get their source assets from `external/runtimebundles`, but
the guest scripts and runtime expectations remain compatible with the existing
layout.

## Loader Changes

The branch added `external/runtimebundles` as a small Go package with embedded
bundle assets, local repo lookup, and remote GitHub fallback support.

Main files:

- [`external/runtimebundles/assets.go`](../../../../../external/runtimebundles/assets.go)
- [`pkg/sandbox/runtime_bundle_assets.go`](../../../../../pkg/sandbox/runtime_bundle_assets.go)
- [`pkg/sandbox/rpc_transport.go`](../../../../../pkg/sandbox/rpc_transport.go)
- [`pkg/sandbox/template_specs.go`](../../../../../pkg/sandbox/template_specs.go)
- [`commands/agent_lima.go`](../../../../../commands/agent_lima.go)

Both sandbox creation paths were updated:

- daemon-managed sandboxes use the runtime bundle loader through
  `loadSandboxAsset`
- the legacy `sky10 sandbox create` path uses the same runtime bundle package
  through `loadLimaSharedAssets`

## Deferred Reconcile Work

Runtime reconcile/autoupdate remains deferred. The branch intentionally did not
add an automatic reconcile loop, manual upgrade UI, or service restarts from
the `/agents` hot path.

The deferred scope is tracked in
[`../../../todo/sandbox-runtime-reconcile.md`](../../../todo/sandbox-runtime-reconcile.md).

## Validation

Commands run:

```sh
go test ./external/runtimebundles -count=1
go test ./pkg/sandbox -count=1
go test ./commands -count=1
go test ./pkg/agent -count=1
go test ./... -count=1
make check
git diff --check
```

No live fresh-VM smoke test was run for this branch. The tests validate the
asset loading and staging paths, but they do not boot a new Lima VM.
