---
created: 2026-04-16
model: gpt-5.4
---

# Hermes Lima Sandboxes And Host Chat

This entry covers the Hermes work that landed in `72b83c0`
(`feat(sandbox): add hermes lima template`), `bac9fa6`
(`feat(sandbox): auto-configure hermes provider secrets`), `e23e025`
(`feat(sandbox): launch hermes directly in terminal`), `3471e0e`
(`fix(sandbox): allow tauri terminal origins`), `87b262d`
(`fix(sandbox): stop hermes env bootstrap clobber`), `5e2089a`
(`fix(sandbox): relink hermes shared env after config`), `7d83949`
(`feat(sandbox): route hermes chat through guest sky10`), `21367a3`
(`fix(sandbox): heal reconnected hermes status`), and `7a4b74a`
(`fix(web): keep long hermes chats visibly pending`).

This was the Hermes follow-on to the OpenClaw Lima sandbox work in
[`14-OpenClaw-Lima-Sandboxes.md`](14-OpenClaw-Lima-Sandboxes.md). The goal was
not to replace that path, but to make Hermes a first-class sandbox option in
`sky10` with the same basic product shape:

- create from the UI or CLI
- inherit provider secrets from the host
- open directly into a usable guest runtime
- appear as a real agent in host-side `sky10` chat

## Why

Before this series, Hermes only existed as a research direction. `sky10`
already had real OpenClaw sandbox support, but Hermes had three product gaps:

- no dedicated Lima template
- no zero-touch host secret/bootstrap path
- no host-side chat path through the guest sandbox

That meant Hermes could be evaluated only as an isolated guest experiment. The
work here closed the gap enough that a user can now create Hermes from the same
agent/sandbox flows used for OpenClaw and talk to it from the host UI instead
of only inside the VM.

## What Shipped

### 1. Hermes became a first-class Lima template

`72b83c0` added the Hermes template assets, daemon-bundled template copies,
sandbox-manager wiring, and web/CLI surfaces needed to create a Hermes sandbox.

The main pieces are:

- [`templates/lima/hermes-sky10.yaml`](../../../../../templates/lima/hermes-sky10.yaml)
- [`templates/lima/hermes-sky10.system.sh`](../../../../../templates/lima/hermes-sky10.system.sh)
- [`templates/lima/hermes-sky10.user.sh`](../../../../../templates/lima/hermes-sky10.user.sh)
- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`commands/agent_lima.go`](../../../../../commands/agent_lima.go)
- [`web/src/pages/Sandboxes.tsx`](../../../../../web/src/pages/Sandboxes.tsx)

That commit existed because Hermes needed to stop being "something you install
manually in a VM" and start being a supported sandbox template in the same
creation flow as other agents.

### 2. Host provider secrets now flow into Hermes automatically

`bac9fa6` taught the daemon to resolve known provider secrets for Hermes and
project them into the sandbox's shared `.env`, with Hermes config also setting a
usable default model.

Key files:

- [`pkg/sandbox/hermes_env.go`](../../../../../pkg/sandbox/hermes_env.go)
- [`commands/serve.go`](../../../../../commands/serve.go)
- [`templates/lima/hermes-sky10.user.sh`](../../../../../templates/lima/hermes-sky10.user.sh)

This commit existed because Hermes should not require a second manual setup step
after sandbox creation just to access Anthropic or OpenAI credentials already
stored in host `sky10`.

### 3. The default sandbox terminal now opens into Hermes directly

`e23e025` changed the default Hermes shell command to `hermes-shared`, adjusted
the terminal path, and added a dedicated `Agents -> Create... -> Hermes` flow.

Relevant files:

- [`pkg/sandbox/terminal.go`](../../../../../pkg/sandbox/terminal.go)
- [`web/src/pages/AgentCreate.tsx`](../../../../../web/src/pages/AgentCreate.tsx)
- [`web/src/pages/SandboxDetail.tsx`](../../../../../web/src/pages/SandboxDetail.tsx)

This commit existed because the first Hermes version still had too much shell
friction. A user could create the VM, but still had to know the right guest
command to get into the Hermes TUI.

### 4. The menu app terminal path needed a Tauri-specific fix

`3471e0e` broadened the sandbox terminal websocket origin checks so the Tauri
menu app could connect to the embedded terminal.

Relevant files:

- [`pkg/sandbox/terminal.go`](../../../../../pkg/sandbox/terminal.go)
- [`pkg/sandbox/terminal_test.go`](../../../../../pkg/sandbox/terminal_test.go)

This commit existed because Hermes made terminal usability much more important,
and the menu app was getting `403` websocket failures even though browser-based
localhost origins already worked.

### 5. Hermes bootstrap had to stop clobbering shared env

`87b262d` fixed the guest bootstrap path so Hermes's stock `.env` template no
longer overwrote the sandbox shared `.env` file that had host-injected secrets.

`5e2089a` followed immediately after because Hermes config writes could still
replace the `~/.hermes/.env -> /shared/.env` symlink, so the bootstrap now
re-links the shared env after the config step as well.

Relevant files:

- [`templates/lima/hermes-sky10.user.sh`](../../../../../templates/lima/hermes-sky10.user.sh)
- [`pkg/sandbox/templates/hermes-sky10.user.sh`](../../../../../pkg/sandbox/templates/hermes-sky10.user.sh)

These commits existed because "Hermes has the right key in host `sky10`" and
"Hermes sees the key during first boot" turned out to be different things. The
guest bootstrap was wiping or drifting away from the shared env file, which made
fresh sandboxes fall back into `hermes setup`.

### 6. Host chat now routes through guest-local sky10 and a Hermes bridge

`7d83949` was the architectural step that made Hermes a real host-visible
agent.

The guest now:

- runs its own `sky10`
- joins the host identity
- runs the Hermes gateway/API server
- runs a small bridge process that listens to guest `sky10` events, sends user
  prompts to Hermes, and replies back through `agent.send`

The main implementation lives in:

- [`templates/lima/hermes-sky10.user.sh`](../../../../../templates/lima/hermes-sky10.user.sh)
- [`templates/lima/hermes-sky10-bridge.py`](../../../../../templates/lima/hermes-sky10-bridge.py)
- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)

This commit existed because the earlier Hermes sandbox only solved guest-local
TUI usage. It did not make Hermes show up in `/agents` or respond through the
same host-side chat flow used by the rest of the product.

### 7. Reconnect and chat UX both needed follow-up hardening

`21367a3` fixed the reconnect sweep so a recovered Hermes sandbox is marked back
to `ready` after the host can actually see the guest agent again. Before that,
chat could work while the sandbox record still showed a stale `error`.

`7a4b74a` fixed the host chat UI so long Hermes replies remain visibly pending.
Hermes search/reasoning prompts can take roughly a minute, and the old UI
stopped showing the waiting state after 30 seconds, which made successful long
responses look dropped.

Relevant files:

- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`web/src/pages/AgentChat.tsx`](../../../../../web/src/pages/AgentChat.tsx)

These commits existed because the first end-to-end Hermes chat path was
functional but still felt broken in practice:

- stale sandbox error state made the product look unhealthy after recovery
- long-running Hermes prompts looked like they disappeared even when they later
  answered normally

## User-Facing Outcome

After this series, `sky10` can provision Hermes as a real Lima-backed agent path
instead of a manual guest experiment.

That means:

- Hermes appears as a first-class create option
- host provider secrets are projected into the guest automatically
- the sandbox terminal can open directly into Hermes
- the menu/Tauri app can connect to that terminal
- the guest can register Hermes back to the host as a visible agent
- host-side chat can reach Hermes through the guest bridge
- reconnect and long-reply behavior are now sane enough for real testing

This is still not "Hermes is the native sky10 runtime." It is a guest-local
Hermes deployment bridged back into host `sky10`. But that was the right shape
for the first integration, because it reused the existing sandbox/agent model
instead of trying to absorb Hermes into the host daemon directly.
