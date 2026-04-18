# OpenClaw on Lima

Run OpenClaw inside an isolated Lima VM on macOS.

This flow uses the repo's Lima template at
[`templates/lima/openclaw-sky10.yaml`](../../templates/lima/openclaw-sky10.yaml).

## What You Get

- Ubuntu 24.04 VM on Lima using `vz`
- guest-local `sky10` installed inside the VM
- OpenClaw installed with Chromium + Xvfb browser automation
- the sky10 web UI reachable on guest port `9101`
- Caddy reverse proxy for guest-local UI access on port `18790`
- a durable agent home at `~/Sky10/Drives/Agents/<slug>`
- portable mind files under `~/Sky10/Drives/Agents/<slug>/mind/`
  wired into the OpenClaw workspace bootstrap files
- sandbox-local state at `~/.sky10/sandboxes/<slug>/state`

## Prerequisites

- macOS 13.5 or newer
- Lima 2.0 or newer

Install Lima:

```bash
brew install lima
limactl --version
```

## Fast Path

From the CLI:

```bash
sky10 sandbox create my-agent --provider lima --template openclaw
```

From the web UI:

1. Open `Agents`
2. Click `Create OpenClaw`
3. Confirm the `OpenClaw Sandbox` template
4. Pick a name and create it

That flow:

- stages the Lima template locally
- creates `~/.sky10/sandboxes/my-agent/state/.env` if it does not exist yet
- merges host `sky10` secrets into that sandbox-local `.env` when known provider secrets exist
- writes `~/.sky10/sandboxes/my-agent/state/update-lima-hosts.sh`
- stages the bundled `openclaw-sky10-channel` plugin into `~/.sky10/sandboxes/my-agent/state/plugins/`
- starts the Lima VM
- installs guest-local `sky10`, OpenClaw, Chromium, Xvfb, and Caddy inside the guest
- configures OpenClaw to talk to guest-local `sky10` at `http://localhost:9101`
- waits for the guest `sky10` daemon, the OpenClaw gateway, and the guest agent registration to report healthy

## Agent Home And Sandbox State

Each sandbox gets a durable agent home at:

```text
~/Sky10/Drives/Agents/<slug>
```

Each sandbox also gets disposable local state at:

```text
~/.sky10/sandboxes/<slug>/state
```

The mounted agent home is split into:

```text
~/Sky10/Drives/Agents/<slug>/mind/
~/Sky10/Drives/Agents/<slug>/workspace/
```

OpenClaw runs with `/shared/workspace` as its configured workspace, and the
bootstrap files it reads there (`SOUL.md`, `AGENTS.md`, `MEMORY.md`,
`IDENTITY.md`, and friends) are linked back to `mind/` so durable personality
and memory edits land in the portable folder.

Provider keys are optional at boot, but the agent will need them before it can
answer real requests:

If you already store provider keys in host `sky10`, the OpenClaw sandbox will
merge them into the sandbox-local `.env` automatically. The currently recognized secret
names are:

- `OPENAI_API_KEY` or `openai`
- `ANTHROPIC_API_KEY` or `anthropic`

For example:

```bash
sky10 secrets put openai --from-env OPENAI_API_KEY --kind api-key --scope current
sky10 secrets put anthropic --from-env ANTHROPIC_API_KEY --kind api-key --scope current
```

You can still set or override the sandbox-local `.env` manually:

```bash
cat > ~/.sky10/sandboxes/my-agent/state/.env <<'EOF'
ANTHROPIC_API_KEY=your-anthropic-key
OPENAI_API_KEY=your-openai-key
EOF
```

## Scope

This milestone sets up guest-local `sky10` and OpenClaw inside the guest,
loads the bundled `sky10` OpenClaw channel plugin, and auto-registers the VM
as an agent on the guest-local daemon.

It does not yet join the guest to your existing sky10 network.

## Open The UIs

Guest-local `sky10` listens on guest port `9101`.
OpenClaw listens on guest port `18790`.

Find the guest IP:

```bash
limactl shell my-agent -- bash -lc 'ip -4 addr show dev lima0'
```

Then open:

```text
http://<guest-ip>:9101
http://<guest-ip>:18790/chat?session=main
```

Confirm the guest-local agent registration:

```bash
curl -s http://<guest-ip>:9101/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"agent.list","params":{},"id":1}'
```

The response should include an agent whose `name` matches the sandbox slug.

## Manage Instances

```bash
limactl list
limactl stop my-agent
limactl start my-agent
limactl shell my-agent
limactl delete my-agent
```

OpenClaw status inside the guest:

```bash
limactl shell my-agent -- bash -lc 'openclaw gateway status'
```

Guest sky10 status inside the guest:

```bash
limactl shell my-agent -- bash -lc 'sky10 daemon status || curl -s localhost:9101/health'
```

Recent guest logs:

```bash
limactl shell my-agent -- bash -lc 'tail -100 /tmp/openclaw-*/*.log'
```

## Notes

- This template is currently macOS-only because it uses Lima `vz`.
- The default model is `anthropic/claude-sonnet-4-6`.
- The provisioning scripts force outbound traffic over the guest `eth0`/`vzNAT` route because the default `lima0` route can lack internet reachability on this setup.
- To change the default model for future instances, edit
  [`templates/lima/openclaw-sky10.yaml`](../../templates/lima/openclaw-sky10.yaml)
  and adjust `param.model`.
- To change the model on a running instance, edit
  `~/.openclaw/openclaw.json` inside the guest and restart the gateway.
