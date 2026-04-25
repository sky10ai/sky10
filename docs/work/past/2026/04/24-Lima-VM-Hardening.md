---
created: 2026-04-24
model: gpt-5
---

# Lima VM Hardening

This entry covers the Lima VM hardening work that shipped in the v0.65.0
line:
`c7882fa5`
(`feat(sandbox): allocate forwarded guest endpoints`),
`9c272f1e`
(`feat(sandbox): render lima guest rpc forwarding`),
`92166157`
(`fix(sandbox): suppress lima auto-forwards`),
`9021fa9e`
(`feat(sandbox): expose forwarded endpoint blocks`),
`abf3c225`
(`fix(sandbox): reconnect guests back to host peer`),
`160ac953`
(`fix(sandbox): reconnect through forwarded guest rpc`),
`49669d9f`
(`fix(sandbox): harden restarts and remote chat routing`),
`99a85f9a`
(`fix(web): keep agent chat send available`),
`eb6e1e5b`
(`feat(sandbox): add agent smoke command`),
`2ebe620d`
(`fix(sandbox): stop tracing docker runtime secrets`),
`7a594df5`
(`fix(sandbox): remove guest reconnect callback`),
`bb4a5745`
(`fix(rpc): bind http server to loopback`),
`1b37ce7d`
(`fix(sync): coalesce p2p anti-entropy pushes`),
`726d14d6`
(`fix(agent): refresh peer agent lists in background`),
`abd5bd6f`
(`fix(agent): list sandbox agents via forwarded endpoints`), and
`68f253e2`
(`fix(web): avoid duplicate agent chat connect`).

The work built on the earlier Lima and Docker runtime entries:

- [`14-OpenClaw-Lima-Sandboxes.md`](14-OpenClaw-Lima-Sandboxes.md)
- [`16-Hermes-Lima-And-Host-Chat.md`](16-Hermes-Lima-And-Host-Chat.md)
- [`22-Getting-Docker-Setup-With-Lima.md`](22-Getting-Docker-Setup-With-Lima.md)

The original Lima plan predated the Docker templates. This pass reconciled
the newer Docker-backed runtimes with the same host/guest boundary and made
the host web UI depend on explicit, host-owned routes instead of opportunistic
guest IPs, Lima auto-forwards, or slow peer convergence.

## Why This Happened

The Lima sandboxes were functional, but the boundary was too implicit:

- Lima could auto-forward guest ports the host did not intentionally expose.
- The host daemon's HTTP listener defaulted to a wider bind than it needed.
- The host sometimes routed sandbox chat through peer-network convergence even
  though the host already owned the VM and knew how to reach its forwarded RPC.
- Existing sandbox records did not carry a structured description of which
  guest services were intentionally exposed.
- The web `/agents` page could look empty or slow even when the sandbox IPs and
  guest daemons were known.
- Manual validation relied on ad hoc websocket pokes instead of a first-class
  smoke command.

The goal was not to claim a perfect VM security boundary. The goal was to make
the normal product path tighter and more deterministic: only declared guest
services get host forwards, the host HTTP RPC binds to loopback by default,
and the host can list and chat with sandbox agents through known forwarded
endpoints.

## What Shipped

### 1. Forwarded guest endpoints became explicit sandbox state

Sandbox records now store a forwarded endpoint model:

- `forwarded_host`
- `forwarded_port`
- `forwarded_endpoints`

The base forwarded port defaults to `39101`. The base port belongs to the
guest `sky10` daemon on guest port `9101`. Templates that expose additional
guest services reserve a contiguous block from that base.

For OpenClaw templates:

- base port: guest `sky10` (`9101`)
- base port + 1: OpenClaw gateway (`18789`)

For Hermes templates:

- base port: guest `sky10` (`9101`)

That means one OpenClaw sandbox can reserve `39101` and `39102`, the next
Hermes sandbox can start at `39103`, and so on without collisions.

Main files:

- [`pkg/sandbox/forwarded_endpoint.go`](../../../../../pkg/sandbox/forwarded_endpoint.go)
- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`pkg/sandbox/store.go`](../../../../../pkg/sandbox/store.go)
- [`templates/lima/openclaw-sky10.yaml`](../../../../../templates/lima/openclaw-sky10.yaml)
- [`templates/lima/openclaw-docker-sky10.yaml`](../../../../../templates/lima/openclaw-docker-sky10.yaml)
- [`templates/lima/hermes-sky10.yaml`](../../../../../templates/lima/hermes-sky10.yaml)
- [`templates/lima/hermes-docker-sky10.yaml`](../../../../../templates/lima/hermes-docker-sky10.yaml)

### 2. Lima auto-forwards were suppressed

The Lima templates now declare the exact host forwards `sky10` wants and then
add catch-all ignored port-forward rules for guest loopback and wildcard guest
addresses.

That keeps Lima from opportunistically publishing guest services just because a
process starts listening inside the VM. OpenClaw still gets its gateway
forward, but only through the explicit `openclaw_gateway` endpoint.

Main files:

- [`templates/lima/openclaw-sky10.yaml`](../../../../../templates/lima/openclaw-sky10.yaml)
- [`templates/lima/openclaw-docker-sky10.yaml`](../../../../../templates/lima/openclaw-docker-sky10.yaml)
- [`templates/lima/hermes-sky10.yaml`](../../../../../templates/lima/hermes-sky10.yaml)
- [`templates/lima/hermes-docker-sky10.yaml`](../../../../../templates/lima/hermes-docker-sky10.yaml)

### 3. Existing sandbox records self-backfill forwarded endpoints

No standalone migration helper was added. `sky10` is currently the only
operator of these templates, so the state loader backfills missing forwarded
endpoint fields when it reads the sandbox state file.

The important operational detail is that stored records and running VMs are
not the same thing. The state file can be backfilled automatically, but an
already-running Lima VM still needs to be stopped and restarted from the
updated template before its actual port-forward rules match the new template.

Main files:

- [`pkg/sandbox/store.go`](../../../../../pkg/sandbox/store.go)
- [`pkg/sandbox/forwarded_endpoint.go`](../../../../../pkg/sandbox/forwarded_endpoint.go)

### 4. Host HTTP now binds to loopback by default

The daemon HTTP RPC server now defaults to `127.0.0.1` instead of a broad bind.
Callers can still opt into a wider bind with `--http-bind`, but the normal
daemon path is host-local.

This matters because guest access to host services should not be an accidental
side effect of how the daemon listens.

Main files:

- [`pkg/rpc/http.go`](../../../../../pkg/rpc/http.go)
- [`commands/serve.go`](../../../../../commands/serve.go)
- [`commands/serve_network_test.go`](../../../../../commands/serve_network_test.go)
- [`pkg/rpc/http_test.go`](../../../../../pkg/rpc/http_test.go)

### 5. Reconnect became host-owned and forwarded-endpoint based

The earlier reconnect path let guest-side scripts participate in reconnecting
the guest daemon back to the host. That was too loose for the boundary this
work wanted.

The reconnect loop is now host-owned:

- the host enumerates running OpenClaw and Hermes sandboxes
- it dials the guest daemon through the sandbox's forwarded `sky10` endpoint
- it confirms the guest identity
- it connects the host and guest peers from the host side
- failures are logged and retried without breaking the daemon

The guest no longer needs a callback path into the host to make reconnect
work.

Main files:

- [`pkg/sandbox/runtime_reconnect.go`](../../../../../pkg/sandbox/runtime_reconnect.go)
- [`pkg/sandbox/runtime_guest.go`](../../../../../pkg/sandbox/runtime_guest.go)
- [`pkg/sandbox/rpc_transport.go`](../../../../../pkg/sandbox/rpc_transport.go)
- [`templates/lima/update-lima-hosts.sh`](../../../../../templates/lima/update-lima-hosts.sh)

### 6. `/agents` learned to query sandbox agents through forwarded endpoints

The host agent list now has a sandbox source. Instead of waiting for normal
peer discovery to make sandbox agents visible, the host queries each known
sandbox's forwarded guest RPC endpoint directly and merges those agents into
the host `agent.list` result.

This is what fixed the unacceptable "no agents" or "only two agents" state on
`http://localhost:9101/agents` when the host already had enough sandbox state
to know where the guest daemons were.

The source is bounded and cached:

- direct sandbox `agent.list` probes have short timeouts
- successful results are cached briefly
- stale peer-agent refreshes run in the background
- chat resolution can proxy websocket traffic through the same known target

Main files:

- [`commands/agent_sandbox_source.go`](../../../../../commands/agent_sandbox_source.go)
- [`commands/agent_sandbox_source_test.go`](../../../../../commands/agent_sandbox_source_test.go)
- [`pkg/agent/router.go`](../../../../../pkg/agent/router.go)
- [`pkg/agent/sort.go`](../../../../../pkg/agent/sort.go)

### 7. Sandbox chat smoke testing became first-class

A new `sky10 sandbox smoke` command validates the actual product path:

- list agents from the host daemon
- match agents back to sandbox records
- probe forwarded endpoint `/health` URLs
- open the agent chat websocket through the host daemon
- send a message
- report ready, send acknowledgement, first-token, and final-response timings

This became the standard verification step after each hardening milestone and
after VM restarts.

Main files:

- [`commands/sandbox_smoke.go`](../../../../../commands/sandbox_smoke.go)
- [`commands/sandbox_smoke_test.go`](../../../../../commands/sandbox_smoke_test.go)
- [`pkg/agent/smoke.go`](../../../../../pkg/agent/smoke.go)
- [`pkg/agent/smoke_test.go`](../../../../../pkg/agent/smoke_test.go)
- [`docs/guides/openclaw-lima.md`](../../../../../docs/guides/openclaw-lima.md)

### 8. Web chat stopped double-connecting and kept send enabled

The web chat page had two related problems during rollout:

- the composer could stay disabled even after the websocket was usable
- selecting an agent could create extra connect work that made the first send
  feel like another hidden connection attempt

The final UI pass made the websocket lifecycle cleaner, kept send availability
tied to the usable chat state, and avoided duplicate agent chat connects.

Main files:

- [`web/src/pages/AgentChat.tsx`](../../../../../web/src/pages/AgentChat.tsx)
- [`web/src/pages/AgentChat.test.tsx`](../../../../../web/src/pages/AgentChat.test.tsx)
- [`web/src/pages/Agents.tsx`](../../../../../web/src/pages/Agents.tsx)

### 9. Docker runtime logs stopped tracing secrets

While testing the Docker-backed templates, the entrypoints were adjusted so
shell tracing is opt-in behind `SKY10_DOCKER_DEBUG=1` instead of always being
on in sensitive runtime setup.

That prevents API keys and generated runtime state from being echoed into
sandbox logs during normal startup.

Main files:

- [`templates/lima/openclaw-docker-runtime/entrypoint.sh`](../../../../../templates/lima/openclaw-docker-runtime/entrypoint.sh)
- [`templates/lima/hermes-docker-runtime/entrypoint.sh`](../../../../../templates/lima/hermes-docker-runtime/entrypoint.sh)
- [`pkg/sandbox/templates/openclaw-docker-runtime/entrypoint.sh`](../../../../../pkg/sandbox/templates/openclaw-docker-runtime/entrypoint.sh)
- [`pkg/sandbox/templates/hermes-docker-runtime/entrypoint.sh`](../../../../../pkg/sandbox/templates/hermes-docker-runtime/entrypoint.sh)

## Rollout Notes

### Existing templates needed VM restarts

The endpoint fields can be backfilled into sandbox state, but Lima does not
rewrite a running VM's effective port-forward configuration just because the
template file changed. The live rollout stopped the existing sandboxes,
applied the template/state changes, restarted them, and then reran websocket
smoke tests across the agents.

### User-v2 is not a formal security proof

Lima `user-v2`, loopback host binds, and suppressed auto-forwards give the
normal product path a much tighter shape, but they are not a formal guarantee
that a guest can never reach anything on the host under every host-network
configuration.

The practical contract after this work is narrower:

- host services are not intentionally exposed to guests by default
- the daemon HTTP listener is loopback-only unless explicitly widened
- guest services are exposed to the host only through declared forwarded
  endpoints
- host-to-guest sandbox chat uses those declared endpoints

### OpenClaw's extra guest port is isolated in the endpoint spec

OpenClaw needed one non-`sky10` guest port for its gateway. That is represented
as an endpoint spec with offset `+1` from the sandbox base port. If the gateway
port changes later, the intended edit point is the endpoint/template spec, not
every caller that wants to open or display the sandbox.

## Validation

Validation during the branch included:

- repeated `sky10 sandbox smoke` runs against all active sandbox agents
- smoke checks after stopping and restarting the Lima VMs
- focused forwarded-endpoint unit coverage in `pkg/sandbox`
- sandbox agent source tests for direct forwarded endpoint listing
- websocket chat tests for host-to-guest proxying and UI send state
- `make check`
- `go test ./... -count=1`
- `make build-web`

The work landed on `main`, was released as `v0.65.0`, and the release was
verified with the tag release workflow. A follow-up test-only commit
(`20324853`, `test(sandbox): allow forwarded endpoint create test on linux`)
kept the new forwarded-endpoint create test portable in Linux CI while the
actual Lima templates remain macOS-only.

## Outcome

The hardened Lima path now has a clear operational model:

- sandbox guest services are exposed through explicit host-loopback forwards
- OpenClaw gets a contiguous two-port block and Hermes gets a one-port block
- existing state backfills endpoint metadata without a separate migration tool
- host reconnect and chat routing are host-owned
- `/agents` can show sandbox agents by dialing known forwarded endpoints
- web chat can send as soon as the proxied websocket is ready
- sandbox smoke tests exercise the same websocket path users rely on

The main product result is that OpenClaw, OpenClaw Docker, Hermes, and Hermes
Docker sandboxes no longer depend on implicit Lima forwarding or peer-list
luck to be visible and chat-ready from the host UI.
