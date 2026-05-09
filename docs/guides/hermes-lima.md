# Hermes on Lima

Run Hermes Agent inside an isolated Lima VM on macOS.

This flow uses the repo's Lima template at
[`templates/lima/hermes-sky10.yaml`](../../templates/lima/hermes-sky10.yaml).

## What You Get

- Ubuntu 24.04 VM on Lima using `vz`
- Hermes Agent installed inside the guest
- a durable agent home at `~/Sky10/Drives/Agents/<slug>`
- portable agent files at the root of `~/Sky10/Drives/Agents/<slug>`
  wired into Hermes `SOUL.md`, `MEMORY.md`, and workspace context
- sandbox-local provider env at `~/.sky10/sandboxes/<slug>/state/.env`,
  linked into `~/.hermes/.env` inside the guest
- automatic host-secret merge for `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
  and `OPENROUTER_API_KEY` when the sandbox is created through the
  running `sky10` daemon
- a `hermes-shared` helper that starts Hermes from `/shared/workspace`
- sky10 message routing through the guest-local Hermes bridge
- access to host-configured messaging platforms, such as Telegram, Slack, and
  IMAP/SMTP, when the host broker exposes those connections to the Hermes
  sandbox

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
sky10 sandbox create my-hermes --provider lima --template hermes
```

From the web UI:

1. Open `Settings -> Sandboxes`
2. Select `Hermes Sandbox`
3. Pick a name and create it

## Launch Hermes

Open a guest shell:

```bash
limactl shell my-hermes
```

Start the Hermes TUI in the shared workspace:

```bash
hermes-shared
```

Or launch it directly from the host:

```bash
limactl shell my-hermes -- bash -lc 'hermes-shared'
```

## Agent Home And Sandbox State

Each Hermes sandbox gets a durable agent home at:

```text
~/Sky10/Drives/Agents/<slug>
```

Each Hermes sandbox also gets disposable local state at:

```text
~/.sky10/sandboxes/<slug>/state
```

The mounted agent home keeps its durable agent files at the root and its tool
workspace under:

```text
~/Sky10/Drives/Agents/<slug>/workspace/
```

Hermes reads its durable identity and memory from the agent root: `SOUL.md` is
linked into `~/.hermes/SOUL.md`, `MEMORY.md` and `USER.md` are linked into
`~/.hermes/memories/`, and `/shared/workspace/AGENTS.md` points back to the
same mounted agent root so both the TUI and the gateway use the portable
files.

When the sandbox is created through the running `sky10` daemon, host
secrets named `anthropic` or `ANTHROPIC_API_KEY` are merged into the
sandbox-local `.env` automatically. The same applies to `openai` /
`OPENAI_API_KEY` and `openrouter` / `OPENROUTER_API_KEY`.

For example:

```bash
sky10 secrets put anthropic --from-env ANTHROPIC_API_KEY --kind api-key
```

You can still edit the sandbox-local `.env` file directly if you want to
override or add keys manually:

```bash
cat > ~/.sky10/sandboxes/my-hermes/state/.env <<'EOF'
OPENAI_API_KEY=your-openai-key
ANTHROPIC_API_KEY=your-anthropic-key
EOF
```

Inside Hermes, adjust the model/provider with:

```bash
hermes model
```

## Messaging Platforms

Hermes can work with messaging platforms that are connected in host sky10 under
`Settings -> Messaging`. Sky10 keeps platform credentials, broker policy,
approval workflow, and adapter processes on the host. The Hermes VM sees only
the normalized messenger bridge at:

```text
/bridge/messengers/ws
```

The flow is:

1. Connect a platform in `Settings -> Messaging` on the host, such as
   Telegram, Slack, or IMAP/SMTP.
2. Expose the connection to the Hermes agent or to the `runtime:hermes`
   runtime subject.
3. The host daemon opens the sandbox bridge into the guest.
4. `hermes-sky10-bridge.py` polls exposed conversations/events through the
   guest-local bridge, runs inbound messages through Hermes, and sends replies
   back as broker-owned drafts/send requests.

Files and media stay file-backed. Inbound platform attachments are copied into
the sandbox state mount and passed to Hermes as paths under:

```text
/sandbox-state/messengers/
```

That matters for voice notes and other large files: the bridge passes the file
name/path to Hermes instead of embedding base64 in the prompt. Outbound replies
still go through the messaging broker, so platform policy can require approval,
block attachments, or refuse new conversations before an adapter sends
anything.

Hermes does not have a native "channel app" model like OpenClaw. Sky10 treats
Hermes as a gateway-backed agent runtime: platform-specific adapters stay
southbound in the host broker, and Hermes receives normalized user messages
through the bridge.

## Notes

- This template is currently macOS-only because it uses Lima `vz`.
- The sandbox terminal gives you direct access to the guest, so you can run
  Hermes in its native TUI while the background bridge handles sky10 and
  messaging-platform traffic.
