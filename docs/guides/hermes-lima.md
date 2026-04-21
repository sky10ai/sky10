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

## Notes

- This template is currently macOS-only because it uses Lima `vz`.
- This milestone does not yet connect Hermes to sky10 message routing.
- The sandbox terminal gives you direct access to the guest, so you can
  run Hermes in its native TUI immediately without waiting for frontend
  integration.
