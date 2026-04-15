---
created: 2026-04-14
model: gpt-5.4
---

# OpenClaw Lima Sandboxes

This work turned the original OpenClaw-on-Lima experiment into a usable
`sky10` sandbox flow.

There was no dedicated `docs/work/current` doc tracking this thread. The
closest references were the earlier implementation note in
[`04-Agent-Support-OpenClaw.md`](04-Agent-Support-OpenClaw.md), the user guide
in [`docs/guides/openclaw-lima.md`](../../../../guides/openclaw-lima.md), and the
higher-level agent planning docs under `docs/work/current/`. This file is the
retrospective for the actual Lima sandbox integration work that landed after
that first agent/plugin milestone.

## What Shipped

### 1. OpenClaw became a first-class sandbox template

`sky10` can now create an OpenClaw agent as a Lima sandbox instead of relying
on an external manual machine setup.

Shipped pieces:

- generic sandbox template selection in [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- OpenClaw Lima assets in [`templates/lima/`](../../../../../templates/lima/)
- bundled daemon copies in [`pkg/sandbox/templates/`](../../../../../pkg/sandbox/templates/)
- web flow from [`web/src/pages/Agents.tsx`](../../../../../web/src/pages/Agents.tsx) into sandbox creation

The concrete product outcome is:

- `sky10 sandbox create my-agent --provider lima --template openclaw`
- or `Agents -> Create Agent` in the web UI

### 2. The guest now runs both OpenClaw and guest-local sky10

The sandbox is no longer just an OpenClaw VM. The guest also installs and runs
its own `sky10` daemon, and OpenClaw is configured to talk to that daemon on
`http://localhost:9101`.

That work lives primarily in:

- [`templates/lima/openclaw-sky10.yaml`](../../../../../templates/lima/openclaw-sky10.yaml)
- [`templates/lima/openclaw-sky10.system.sh`](../../../../../templates/lima/openclaw-sky10.system.sh)
- [`templates/lima/openclaw-sky10.user.sh`](../../../../../templates/lima/openclaw-sky10.user.sh)

This changed the architecture from:

- guest OpenClaw -> host `sky10`

to:

- guest OpenClaw -> guest `sky10`
- guest `sky10` -> host/private-network `sky10`

That split turned out to matter for agent registration, plugin lifecycle, and
future multi-agent isolation.

### 3. Host secrets now hydrate sandbox env automatically

Provider keys no longer have to be hand-written into every sandbox from
scratch.

The host daemon now resolves known provider secrets from `sky10 secrets` and
projects them into the sandbox shared `.env`:

- [`pkg/sandbox/openclaw_env.go`](../../../../../pkg/sandbox/openclaw_env.go)
- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)

The guest then links that file into OpenClaw and loads it through the guest
service:

- [`templates/lima/openclaw-sky10.user.sh`](../../../../../templates/lima/openclaw-sky10.user.sh)

This was the minimum viable secret propagation model. It is not yet a proper
"projected secret mount" abstraction, but it made OpenClaw usable with host
Anthropic/OpenAI keys.

### 4. Guest join is now automated

The guest sandbox no longer stops at "standalone guest daemon." The host now
issues an invite, stages it into the shared sandbox directory, the guest joins
the host identity, and the host explicitly reconnects to the guest after the
join restart.

The main codepaths are:

- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`commands/identity_rpc.go`](../../../../../commands/identity_rpc.go)
- [`commands/serve.go`](../../../../../commands/serve.go)
- [`templates/lima/openclaw-sky10.user.sh`](../../../../../templates/lima/openclaw-sky10.user.sh)

This closed the gap between:

- "the guest daemon is up"

and

- "the host can actually see the guest agent in `/agents`"

### 5. The OpenClaw bridge/plugin path was stabilized

This was the noisiest part of the project.

The initial OpenClaw bridge worked just enough to prove the concept, but it had
several lifecycle bugs:

- channel lifecycle misuse caused 5-minute restart loops
- plugin state could initialize multiple times in one process
- helper subprocesses also loaded the bridge and opened extra SSE listeners
- one prompt could fan out into repeated replies

The fixes landed across:

- [`templates/lima/openclaw-sky10-channel/src/index.js`](../../../../../templates/lima/openclaw-sky10-channel/src/index.js)
- [`pkg/sandbox/templates/openclaw-sky10-channel/src/index.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/index.js)

The end state is:

- one bridge runtime per real gateway process
- no health-monitor restart loop from the bridge lifecycle
- no helper-process fanout
- no duplicate reply bursts for a single inbound message

### 6. The browser path became actually usable

The sandbox template now provisions a real browser environment for OpenClaw:

- Chromium installed in the guest
- Xvfb running as a service
- OpenClaw browser config pointed at guest Chromium

But the browser path did not work cleanly at first. The final working setup
required:

- persistent guest route preference toward `eth0`/`vzNAT`
- OpenClaw browser SSRF relaxation for general browsing
- CLI pairing approval for local guest use
- correct browser skill advertisement

That work lives in:

- [`templates/lima/openclaw-sky10.dependency.sh`](../../../../../templates/lima/openclaw-sky10.dependency.sh)
- [`templates/lima/openclaw-sky10.system.sh`](../../../../../templates/lima/openclaw-sky10.system.sh)
- [`templates/lima/openclaw-sky10.user.sh`](../../../../../templates/lima/openclaw-sky10.user.sh)

### 7. The sandbox UI became usable during bring-up

The web UI also needed practical sandbox quality-of-life fixes while debugging
the VMs:

- logs and terminal grouped as tabs instead of two stacked pain points
- terminal kept mounted when switching tabs
- terminal status/actions moved into a saner layout
- stale log handling fixed
- stale Lima records could be deleted cleanly

Relevant files:

- [`web/src/pages/SandboxDetail.tsx`](../../../../../web/src/pages/SandboxDetail.tsx)
- [`web/src/components/SandboxTerminal.tsx`](../../../../../web/src/components/SandboxTerminal.tsx)
- [`pkg/sandbox/terminal.go`](../../../../../pkg/sandbox/terminal.go)
- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)

## Main Problems We Hit

### 1. Guest networking looked fine until it actually mattered

Early failures were misleading:

- DNS worked
- some outbound checks worked
- but `apt`, GitHub fetches, and provider HTTPS calls still stalled

The real issue was routing. Lima could prefer the wrong default route, which
made OpenClaw look "slow" when the real failure was stalled outbound provider
traffic. The fix was not just a transient `ip route` tweak. It had to be made
persistent in the guest setup so provisioning and runtime used the same good
path.

### 2. OpenClaw browser/runtime behavior was stricter than expected

The VM already had Chromium and network access, but OpenClaw still claimed the
browser was blocked. Some of that turned out to be model hallucination, but
some of it was real runtime policy:

- SSRF policy behavior was stricter than the product copy implied
- wildcard hostname allowlists were not sufficient
- general browsing required the `dangerouslyAllowPrivateNetwork` path in the
  OpenClaw browser policy for this build

The naming was confusing, but the runtime behavior was what mattered.

### 3. The bridge/plugin lifecycle was wrong in subtle ways

The bridge initially behaved like a long-lived channel account while also being
eagerly bootstrapped outside the correct lifecycle. That created:

- restart churn
- duplicate SSE subscribers
- multiple bridge instances in the same process tree
- repeated replies from a single user message

This was the deepest integration bug in the whole effort. The fix was less
about business logic and more about respecting how OpenClaw actually expects
channels and plugin services to live.

### 4. Join success was not the same thing as peer connectivity

`identity.join` succeeding did not mean the host and guest were connected.
After the guest restarted, the two daemons still needed an actual peer
connection before `agent.list` on the host could see the guest agent.

That exposed three separate problems:

- the join path cared too much about secrets bootstrap details that were not
  needed for basic connectivity
- waiting for DHT/Nostr rediscovery was too flaky for immediate sandbox
  usability
- already-running guests disappeared from the host after a daemon restart until
  they were manually reconnected

The result was a direct connect/reconnect path in the sandbox manager plus a
startup reconnect sweep for running OpenClaw sandboxes.

### 5. Secrets bootstrap order mattered more than expected

One of the nastier bugs was that a guest could start a standalone `sky10`
daemon before joining the host identity, mint its own `secrets` namespace
bootstrap, and later poison host-side secret resolution in ways that looked
random.

That made old sandboxes appear to "work" while fresh ones came up blank.
The real fix was ordering:

- stage invite first
- join first
- then start the guest `sky10` daemon as part of the host identity

## What The Product Looks Like Now

For a fresh OpenClaw sandbox:

- the host can create it from CLI or UI
- the guest provisions OpenClaw, Chromium, Xvfb, Caddy, and guest-local `sky10`
- host secrets can populate the sandbox `.env`
- the guest joins the host identity automatically
- the host reconnects to the guest automatically
- the host `/agents` view can show the guest agent
- OpenClaw can use the browser and provider API keys inside the guest

The user-facing guide for the current flow is
[`docs/guides/openclaw-lima.md`](../../../../guides/openclaw-lima.md).

## Follow-On Work

The core sandbox flow is now real, but a few things still deserve follow-up:

- replace the shared sandbox `.env` approach with a clearer projected-secrets
  mount model
- improve OpenClaw web-login and hostname/TLS ergonomics for multiple agents on
  one machine
- keep the user guide aligned with the latest guest-join and host-reconnect
  behavior
- extend reconnect logic beyond the current OpenClaw-specific startup sweep if
  more Lima sandbox types appear

The important shift is that these are now product refinements. The base flow
went from "experimental demo with manual recovery" to "templated agent sandbox
that can provision, join, reconnect, browse, and register automatically."
