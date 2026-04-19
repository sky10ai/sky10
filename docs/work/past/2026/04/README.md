# April 2026

| Date | Entry |
|------|-------|
| 19 | [Host-Guest WebSocket Comms Foundation](19-Host-Guest-WebSocket-Comms-Foundation.md) — dedicated host-initiated guest chat websocket transport, streaming event vocabulary, two-daemon integration coverage, and groundwork for streaming guest agents back into the host web UI with a tighter VM boundary |
| 16 | [Web Dark Theme And Selector](16-Web-Dark-Theme-And-Selector.md) — system-aware `System`/`Light`/`Dark` preference model, top-right header selector, CSS-variable-backed token palette, and dark-mode cleanup across the main command-center surfaces |
| 16 | [Syncthing-Informed FS P2P Hardening](16-Syncthing-Informed-FS-P2P-Hardening.md) — forty-commit FS branch: hidden transfer workspace, peer metadata sync, peer chunk fetch, unified pull planner, conflict/health surfaces, Windows path policy, and the practical Syncthing lessons that shaped the implementation |
| 16 | [Live Relay Productization And Health Clarity](16-Live-Relay-Productization-And-Health-Clarity.md) — explicit direct→relay→Nostr delivery, sticky relay preference in normal resolution, mailbox retry isolation, managed live-relay requirement, and transport-vs-coordination UI clarity |
| 16 | [Hermes Lima Sandboxes And Host Chat](16-Hermes-Lima-And-Host-Chat.md) — Hermes Lima template, host secret hydration, direct terminal launch, guest-local bridge back into host chat, reconnect and long-reply hardening |
| 15 | [Base Wallet Support](15-Base-Wallet-Support.md) — Base balances, explorer links, ETH/USDC sends, EIP-1559 tx building, fixture-backed regression coverage |
| 14 | [OpenClaw Lima Sandboxes](14-OpenClaw-Lima-Sandboxes.md) — OpenClaw-on-Lima provisioning, guest-local `sky10`, host secret hydration, auto-join, reconnect sweep, plugin and browser hardening |
| 14 | [Secrets V1](14-Secrets-V1.md) — device-scoped secrets, `current`/`trusted`/`explicit` sharing, CLI/RPC/web UI, bootstrap repair, delete |
| 12 | [Private Network Robustness](12-Private-Network-Robustness.md) — Tailscale/headscale-informed transport hardening: unified health, convergence control, path memory, active Nostr coordination, managed live relay bootstrap, sticky home-relay selection, and remaining relay/productization work |
| 11 | [Mailbox](11-Mailbox.md) — durable mailbox architecture, private-network and sky10-network backends, queue/lease semantics, payment and approval workflows, principal-scoped UI, and debug/repair tooling |
| 08 | [Managed Apps Foundation](08-Managed-Apps-Foundation.md) — `pkg/apps`, hidden `/settings/apps`, `sky10 apps` CLI, versioned helper-binary installs under `~/.sky10/apps/` |
| 07 | [Multi-Instance E2E Foundation](07-Multi-Instance-E2E-Foundation.md) — per-instance roots, hermetic network controls, real 3-process KV coverage, real 3-process MinIO-backed FS coverage, CI integration |
| 07 | [Invite & Join Bootstrap Hardening](07-Invite-Join-Bootstrap-Hardening.md) — richer invite payloads, direct-dial-first join, stronger bootstrap correctness, but post-join usability still lags |
| 07 | [KV CRDT Reliability Hardening](07-KV-CRDT-Reliability-Hardening.md) — causal metadata, tombstones, summary-first anti-entropy, loud sync health, fresh-join KV startup fix |
| 06 | [Private Network Discovery Hardening](06-Private-Network-Discovery-Hardening.md) — DHT-provider membership/presence, daemon-owned join, correct device surfaces, KV sync scoped to the private network, faster device/agent RPCs |
| 06 | [Staged Update Lifecycle](06-Staged-Update-Lifecycle.md) — Split updater into check/download/status/install, persist staged artifacts, move tray to CLI-driven updates, replace broken v0.39.0 with v0.39.1 |
| 05 | [Agent Wallet & Payments](05-Agent-Wallet-Payments.md) — OWS wallet integration, Solana tx building, SOL + USDC transfers, Settings UI |
| 04 | [Agent Support + OpenClaw](04-Agent-Support-OpenClaw.md) — Agent registry, cross-device routing, OpenClaw channel plugin, web chat UI |
| 04 | [Remove Cirrus](04-Remove-Cirrus.md) — Replace SwiftUI app with web UI, CLI daemon management, Tauri menu bar app |
| 04 | [S3 Optional — Complete](04-S3-Optional-Complete.md) — Full P2P-first architecture: KV sync, join, 3-layer discovery, debugging saga, CI fixes |
| 04 | [Self-Update](04-Self-Update.md) — CLI + RPC self-update with SSE progress, periodic background check |
| 03 | [S3 Optional (Phase 1-2)](03-S3-Optional.md) — Initial P2P architecture, KV sync over libp2p, Nostr discovery, P2P join |
| 01 | [Identity Layer](01-Identity-Layer.md) — Separate identity from device keys, pkg/id/, manifest signing |
