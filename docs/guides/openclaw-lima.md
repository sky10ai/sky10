# OpenClaw on Lima

Run an OpenClaw agent inside an isolated Lima VM on macOS and have it join
your existing sky10 network automatically.

This flow uses the repo's Lima template at
[`templates/lima/openclaw-sky10.yaml`](../../templates/lima/openclaw-sky10.yaml).

## What You Get

- Ubuntu 24.04 VM on Lima using `vz`
- guest-local `sky10` installed inside the VM
- OpenClaw installed with Chromium + Xvfb browser automation
- the `openclaw-sky10-channel` plugin installed automatically
- a staged `sky10` invite so the guest joins your existing sky10 network
- OpenClaw configured to talk to the guest-local daemon at `http://localhost:9101`

## Prerequisites

- macOS 13.5 or newer
- Lima 2.0 or newer
- a running host `sky10` daemon

Install Lima:

```bash
brew install lima
limactl --version
```

Make sure `sky10` is already running on the host:

```bash
sky10 daemon status
curl -s localhost:9101/health
```

If the daemon is not installed yet, run:

```bash
sky10 serve
```

## Fast Path

From the CLI:

```bash
sky10 sandbox create my-agent --provider lima --template openclaw
```

From the web UI:

1. Open `Agents`
2. Click `Create Agent`
3. Confirm the `OpenClaw Agent` template
4. Pick a name and create it

That flow:

- stages the Lima template locally
- creates `~/sky10/sandboxes/my-agent/.env` if it does not exist yet
- writes `~/sky10/sandboxes/my-agent/sky10-invite.txt`
- writes `~/sky10/sandboxes/my-agent/update-lima-hosts.sh`
- starts the Lima VM
- installs `sky10` and OpenClaw inside the guest
- joins the guest daemon to your existing sky10 network
- waits for the agent to appear back on the host Agents page

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

## How The sky10 Wiring Works

The host daemon generates an invite code and stages it into:

```text
~/sky10/sandboxes/<slug>/sky10-invite.txt
```

During guest provisioning:

- the VM installs `sky10`
- the guest daemon starts on `localhost:9101`
- the guest runs `sky10 join <invite-code>`
- OpenClaw installs its `sky10` plugin
- the plugin is configured with `plugins.entries.sky10.config`
- `rpcUrl` is set to `http://localhost:9101`

That means OpenClaw talks to the guest-local `sky10` daemon, while the guest
and host still communicate through the same sky10 network.

## Open The UI

OpenClaw listens on guest port `18790`.

Find the guest IP:

```bash
limactl shell my-agent -- bash -lc 'ip -4 route get 1.1.1.1'
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

Guest sky10 status:

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
- To change the default model for future instances, edit
  [`templates/lima/openclaw-sky10.yaml`](../../templates/lima/openclaw-sky10.yaml)
  and adjust `param.model`.
- To change the model on a running instance, edit
  `~/.openclaw/openclaw.json` inside the guest and restart the gateway.
