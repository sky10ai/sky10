# Hermes on Lima

Run Hermes Agent inside an isolated Lima VM on macOS.

This flow uses the repo's Lima template at
[`templates/lima/hermes-sky10.yaml`](../../templates/lima/hermes-sky10.yaml).

## What You Get

- Ubuntu 24.04 VM on Lima using `vz`
- Hermes Agent installed inside the guest
- a shared host directory at `~/sky10/sandboxes/<slug>`
- shared provider env at `~/sky10/sandboxes/<slug>/.env`, linked into
  `~/.hermes/.env` inside the guest
- a `hermes-shared` helper that starts Hermes from `/shared`

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

## Shared Host Directory

Each Hermes sandbox gets a shared host directory at:

```text
~/sky10/sandboxes/<slug>
```

Add provider keys to the shared `.env` file before you expect Hermes to
answer real requests:

```bash
cat > ~/sky10/sandboxes/my-hermes/.env <<'EOF'
OPENAI_API_KEY=your-openai-key
ANTHROPIC_API_KEY=your-anthropic-key
EOF
```

Inside Hermes, adjust the model/provider with:

```bash
hermes setup
# or
hermes model
```

## Notes

- This template is currently macOS-only because it uses Lima `vz`.
- This milestone does not yet connect Hermes to sky10 message routing.
- The sandbox terminal gives you direct access to the guest, so you can
  run Hermes in its native TUI immediately without waiting for frontend
  integration.
