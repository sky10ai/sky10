# OpenClaw on Lima

Run OpenClaw inside an isolated Lima VM on macOS.

This flow uses the repo's Lima template at
[`templates/lima/openclaw-sky10.yaml`](../../templates/lima/openclaw-sky10.yaml).

## What You Get

- Ubuntu 24.04 VM on Lima using `vz`
- OpenClaw installed with Chromium + Xvfb browser automation
- Caddy reverse proxy for guest-local UI access on port `18790`
- a shared host directory at `~/sky10/sandboxes/<slug>`

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
- creates `~/sky10/sandboxes/my-agent/.env` if it does not exist yet
- writes `~/sky10/sandboxes/my-agent/update-lima-hosts.sh`
- starts the Lima VM
- installs OpenClaw, Chromium, Xvfb, and Caddy inside the guest
- waits for the OpenClaw gateway to report healthy

## Shared Host Directory

Each sandbox gets a shared host directory at:

```text
~/sky10/sandboxes/<slug>
```

Provider keys are optional at boot, but the agent will need them before it can
answer real requests:

```bash
cat > ~/sky10/sandboxes/my-agent/.env <<'EOF'
ANTHROPIC_API_KEY=your-anthropic-key
OPENAI_API_KEY=your-openai-key
EOF
```

## Scope

This milestone only sets up OpenClaw inside the guest.

It does not yet:

- install `sky10` in the guest
- join the guest to your existing sky10 network
- install the OpenClaw sky10 plugin
- auto-register the VM as a sky10 agent

## Open The UI

OpenClaw listens on guest port `18790`.

Find the guest IP:

```bash
limactl shell my-agent -- bash -lc 'ip -4 addr show dev lima0'
```

Then open:

```text
http://<guest-ip>:18790/chat?session=main
```

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
