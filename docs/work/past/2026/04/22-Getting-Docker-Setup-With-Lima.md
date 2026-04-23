---
created: 2026-04-22
model: gpt-5.4
---

# Getting Docker Setup With Lima

This entry covers the Lima + Docker runtime work that landed in
`991317b6`
(`feat(sandbox): add lima docker templates`),
`d519312a`
(`fix(rpc): refresh embedded web ui after local install`),
`ebb6d6ad`
(`fix(sandbox): persist docker guest sky10 state`),
`3955ab49`
(`fix(sandbox): monitor docker openclaw health`),
`add8ea58`
(`fix(sandbox): relax docker openclaw watchdog`), and
`9f1355ee`
(`fix(sandbox): restore hermes docker runtime`).

This work closed out the earlier planning note formerly tracked as
`docs/work/todo/vm-runtime-colima-docker-consideration.md`,
which asked whether `sky10` should move from pure Lima guest bootstrap
toward a Docker-oriented runtime model. The result was not "replace Lima
with Colima." The result was "keep Lima as the VM substrate, run Docker
inside the guest, and move OpenClaw/Hermes packaging into Docker-owned
runtime assets."

This built directly on the earlier Lima sandbox work in
[`14-OpenClaw-Lima-Sandboxes.md`](14-OpenClaw-Lima-Sandboxes.md) and
[`16-Hermes-Lima-And-Host-Chat.md`](16-Hermes-Lima-And-Host-Chat.md).
Those entries established the guest-local `sky10`, join/reconnect flow,
host-secret hydration, and guest chat path. This follow-on kept that
Lima VM boundary but replaced a large chunk of the guest bootstrap
story with Docker images and container lifecycle management.

## Why This Happened

The old Lima templates were doing two jobs at once:

- define the VM shape
- mutate the guest into an app environment with `apt`, `npm`, config
  writes, service units, and runtime scripts

That was workable, but it made OpenClaw and Hermes packaging look more
like "boot Ubuntu and keep editing it" than "run a packaged runtime."

The goal of this work was not to abandon Lima. The goal was to split the
layers more cleanly:

- Lima owns VM lifecycle, mounts, and networking
- Docker owns guest software packaging and process composition
- `sky10` still owns the host/guest join, state staging, readiness, and
  UI wiring

## What Shipped

### 1. Lima stayed the provider, but Docker became a guest runtime option

Two new Lima-backed sandbox templates were added:

- `openclaw-docker`
- `hermes-docker`

The key point is that these are still Lima sandboxes. The host still
creates and manages a Lima VM, but the guest now installs Docker Engine
and runs the agent runtime in containers instead of expressing the full
software stack as boot-time guest mutation.

Main files:

- [`commands/agent_lima.go`](../../../../../commands/agent_lima.go)
- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`templates/lima/openclaw-docker-sky10.yaml`](../../../../../templates/lima/openclaw-docker-sky10.yaml)
- [`templates/lima/hermes-docker-sky10.yaml`](../../../../../templates/lima/hermes-docker-sky10.yaml)

### 2. Docker runtime assets were staged as first-class sandbox payloads

The sandbox manager now stages Docker runtime assets into sandbox state,
not just Lima shell assets.

OpenClaw runtime assets land under:

- `/sandbox-state/runtime/openclaw/Dockerfile`
- `/sandbox-state/runtime/openclaw/entrypoint.sh`
- generated `/sandbox-state/runtime/openclaw/compose.yaml`

Hermes runtime assets land under:

- `/sandbox-state/runtime/hermes/Dockerfile`
- `/sandbox-state/runtime/hermes/entrypoint.sh`
- generated `/sandbox-state/runtime/hermes/compose.yaml`

That made Docker part of the real packaged runtime surface instead of a
side experiment.

Main files:

- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`pkg/sandbox/templates/openclaw-docker-runtime/Dockerfile`](../../../../../pkg/sandbox/templates/openclaw-docker-runtime/Dockerfile)
- [`pkg/sandbox/templates/openclaw-docker-runtime/entrypoint.sh`](../../../../../pkg/sandbox/templates/openclaw-docker-runtime/entrypoint.sh)
- [`pkg/sandbox/templates/hermes-docker-runtime/Dockerfile`](../../../../../pkg/sandbox/templates/hermes-docker-runtime/Dockerfile)
- [`pkg/sandbox/templates/hermes-docker-runtime/entrypoint.sh`](../../../../../pkg/sandbox/templates/hermes-docker-runtime/entrypoint.sh)

### 3. OpenClaw packaging moved into a Docker image

The OpenClaw Docker image now owns:

- Ubuntu package install
- Node installation
- OpenClaw installation
- Chromium / Playwright browser install
- guest `sky10` binary fetch

The Lima guest scripts still do some orchestration, but the big software
environment is now image-defined instead of "install everything on first
boot."

Main files:

- [`templates/lima/openclaw-docker-runtime/Dockerfile`](../../../../../templates/lima/openclaw-docker-runtime/Dockerfile)
- [`templates/lima/openclaw-docker-runtime/entrypoint.sh`](../../../../../templates/lima/openclaw-docker-runtime/entrypoint.sh)
- [`templates/lima/openclaw-docker-sky10.system.sh`](../../../../../templates/lima/openclaw-docker-sky10.system.sh)
- [`templates/lima/openclaw-docker-sky10.user.sh`](../../../../../templates/lima/openclaw-docker-sky10.user.sh)

### 4. Hermes packaging moved into a Docker image too

Hermes followed the same split, but needed different runtime glue
because the guest path still includes:

- guest-local `sky10`
- Hermes gateway
- the `hermes-sky10-bridge`
- the shared `hermes-shared` terminal helper

This was the first time the Hermes stack was expressed as a Docker-owned
runtime instead of a purely guest-mutating Lima bootstrap.

Main files:

- [`templates/lima/hermes-docker-runtime/Dockerfile`](../../../../../templates/lima/hermes-docker-runtime/Dockerfile)
- [`templates/lima/hermes-docker-runtime/entrypoint.sh`](../../../../../templates/lima/hermes-docker-runtime/entrypoint.sh)
- [`templates/lima/hermes-docker-sky10.system.sh`](../../../../../templates/lima/hermes-docker-sky10.system.sh)
- [`templates/lima/hermes-docker-sky10.user.sh`](../../../../../templates/lima/hermes-docker-sky10.user.sh)

### 5. The web UI and sandbox picker were updated for the new templates

The new Docker templates were surfaced in the sandbox creation and
detail UI, and the embedded web app got cache-header hardening so a
local reinstall would not keep serving a stale SPA bundle that hid the
new template names.

Without that cache fix, the backend could already know about
`openclaw-docker` and `hermes-docker` while the browser still rendered
the old template list.

Main files:

- [`web/src/lib/sandboxes.ts`](../../../../../web/src/lib/sandboxes.ts)
- [`web/src/pages/AgentCreate.tsx`](../../../../../web/src/pages/AgentCreate.tsx)
- [`web/src/pages/Sandboxes.tsx`](../../../../../web/src/pages/Sandboxes.tsx)
- [`web/src/pages/SandboxDetail.tsx`](../../../../../web/src/pages/SandboxDetail.tsx)
- [`pkg/rpc/http.go`](../../../../../pkg/rpc/http.go)
- [`pkg/rpc/webui.go`](../../../../../pkg/rpc/webui.go)
- [`pkg/rpc/webui_test.go`](../../../../../pkg/rpc/webui_test.go)

## Runtime Failures We Hit

### 1. OpenClaw initially registered and then disappeared

The first OpenClaw Docker runtime bug was not packaging. It was
supervision.

The entrypoint was treating short-lived launcher processes and a flaky
gateway health probe as if they represented the long-lived OpenClaw
service. That caused the container watchdog to kill and restart the
container after the agent had already appeared.

The fixes were:

- persist guest `sky10` state across container restarts
- stop supervising transient launcher PIDs
- monitor the actual gateway process
- use `pgrep -f` instead of `pgrep -x` because the Linux process-name
  truncation broke the exact-name check

Main files:

- [`templates/lima/openclaw-docker-runtime/entrypoint.sh`](../../../../../templates/lima/openclaw-docker-runtime/entrypoint.sh)
- [`pkg/sandbox/templates/openclaw-docker-runtime/entrypoint.sh`](../../../../../pkg/sandbox/templates/openclaw-docker-runtime/entrypoint.sh)

### 2. Hermes initially failed because the runtime home mount hid the install

The first Hermes Docker failure looked like a guest `sky10` startup
problem, but the real break was that `hermes` was missing at runtime.

The image had run the upstream Hermes installer under `/root/.hermes`,
and then the sandbox runtime bind-mounted `/sandbox-state/hermes-home`
onto `/root/.hermes`. That mount hid the baked install, leaving a broken
`~/.local/bin/hermes` symlink at runtime.

The fix was to install Hermes into `/opt` at image build time and
symlink the executable into `/usr/local/bin/hermes`, so the mutable
runtime home no longer shadowed the baked binaries.

Main files:

- [`templates/lima/hermes-docker-runtime/Dockerfile`](../../../../../templates/lima/hermes-docker-runtime/Dockerfile)
- [`pkg/sandbox/templates/hermes-docker-runtime/Dockerfile`](../../../../../pkg/sandbox/templates/hermes-docker-runtime/Dockerfile)

### 3. Hermes bridge warm-up blocked registration

After the binary-path fix, Hermes still took too long to appear because
the bridge performed a model warm-up call before registering the agent.

That meant a slow or stalled warm-up made the sandbox look dead even
though the gateway was already up.

The fix was to register first and move warm-up into a background thread,
so agent discovery and websocket chat readiness no longer depend on the
warm-up round-trip.

Main files:

- [`templates/lima/hermes-sky10-bridge.py`](../../../../../templates/lima/hermes-sky10-bridge.py)
- [`pkg/sandbox/templates/hermes-sky10-bridge.py`](../../../../../pkg/sandbox/templates/hermes-sky10-bridge.py)

### 4. Large Docker rebuilds can exhaust guest builder cache

Live rebuild testing also showed that Docker image debugging inside the
guest can hit storage issues even when the VM still has free disk.

The practical failure was BuildKit/containerd cache growth inside the
guest while rebuilding the large Hermes image. A `docker builder prune`
was enough to recover the live sandbox, but the operational lesson is
that "VM has free space" and "Docker image export can succeed" are not
the same condition.

That was not a product-code change by itself, but it is a real part of
operating these guest Docker runtimes during development.

## What This Bought Us

The main win was architectural, not cosmetic.

Before this series:

- Lima YAML defined the VM
- guest shell scripts installed the software environment
- the packaging boundary was blurry

After this series:

- Lima still defines the VM
- Docker images define the OpenClaw and Hermes software stacks
- guest shell is thinner orchestration glue for mounts, joins, env,
  readiness, and runtime wiring

That bought:

- a clearer split between VM config and app packaging
- a more standard authoring surface for AI-generated packaging
- a realistic path to prebuilt images and faster sandbox bring-up
- Docker/Compose tooling inside the guest without requiring Docker
  Desktop on the host

It did **not** buy a new substrate. This is still Lima. The point was to
turn Docker into the guest packaging boundary.

## Outcome

`sky10` can now create both OpenClaw and Hermes as Lima-backed Docker
runtime sandboxes:

- `sky10 sandbox create my-openclaw --provider lima --template openclaw-docker`
- `sky10 sandbox create my-hermes --provider lima --template hermes-docker`

After the rollout hardening:

- both templates appear in the sandbox UI
- both guest agents stay registered
- both guest websocket chat paths work end to end
- local installs correctly expose the new templates after daemon/web UI
  restart

The original "Colima or Docker?" planning question ended with a more
useful answer: keep Lima, add Docker where packaging belongs, and only
then consider whether any outer wrapper is still worth it.
