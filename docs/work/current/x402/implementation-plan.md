---
created: 2026-04-26
updated: 2026-05-06
model: claude-opus-4-7
---

# x402 Implementation Plan

Milestones in dependency order. Each is a coherent unit of work with
its own tests; later milestones build on the contracts shipped by
earlier ones.

x402's agent-facing surface lives at `pkg/sandbox/comms/x402/` —
a per-intent websocket endpoint at `/comms/x402/ws` whose handlers
delegate to `pkg/x402`. See
[sandbox-comms](../sandbox-comms/) for the per-intent endpoint
architecture and shared transport plumbing.

## M1 — protocol core — **landed**

`pkg/x402` implements the wire protocol. Real OWS-backed signing on
Base **and** Solana mainnet works end-to-end against multiple live
facilitators — this milestone is now well past its original "Base
testnet smoke" exit criteria.

Wire shape evolved across the iteration:

- v1 + v2 dual-version support. v1 carries the challenge in the body
  with `maxAmountRequired`; v2 carries it in a `Payment-Required`
  response header with `amount` (integer base units). Per-version
  parsers live in `protocol_v1.go` / `protocol_v2.go`; transport
  detects which version a server speaks from the response shape and
  dispatches.
- Three retry header names: `X-Payment` (Coinbase / Exa),
  `Payment-Signature` (Smartflow), `X-402-Payment` (Venice). All
  three carry the same envelope; servers pick the one they read.
- v2 envelope is a hybrid: top-level `scheme`+`network` (canonical
  x402 npm shape, Venice requires it) alongside `accepted` (echoed
  verbatim via `RawWire` so vendor extensions round-trip) and an
  optional `resource` block.
- Receipt parsing is version-blind: tries plain JSON, then base64
  JSON, then a bare tx-hash fallback (Messari's wire form).

Files:

- `pkg/x402/protocol.go` — shared canonical types
- `pkg/x402/protocol_v1.go` / `protocol_v2.go` — version-specific
  parsers and encoders
- `pkg/x402/transport.go` — http round-trip with version dispatch,
  PreferAndCheapest tier-and-network selection
- `pkg/x402/sign.go` + `sign_solana.go` — OWS-backed Signer with
  EIP-3009 EVM and v0 versioned Solana signing paths
- `pkg/x402/registry.go`, `budget.go`, `pin.go`, `policy.go`
- `pkg/x402/backend.go` — single Backend used by both the host RPC
  and the comms handlers
- `pkg/x402/testdata/` — captured live wire fixtures from each
  smoked service

Live verified against (in `pkg/x402/live_smoke_test.go`):

- Exa `/contents` (v2, Base, POST $0.001)
- Blockrun (v2, Base, GET $0.001)
- Smartflow (v2, Base, GET $0.001)
- Browserbase (v1, Base, POST $0.010)
- Alchemy `/solana-mainnet/v2` (v2, Solana mainnet, $0.001)
- Coingecko (v2, Solana mainnet, GET $0.010 — third-party SVM facilitator)
- Messari (v2, both Base and Solana, GET $0.10)
- Venice top-up + chat-completions + balance (SIWX, see M9)

## M2 — discovery and refresh — **landed**

`pkg/x402/discovery` ingests the agentic.market catalog plus a
curated overlay. Daemon seeds the registry on startup, then refreshes
hourly with ±10min jitter.

Files: `pkg/x402/discovery/{client.go, refresh.go, diff.go,
overlay.go, sources.go, agentic.go, static.go}`.

## M3 — host-side RPC for Web UI — **landed (CLI is out of scope)**

`pkg/x402/rpc/handler.go` ships `x402.listServices`, `x402.setEnabled`,
`x402.budgetStatus`, `x402.receipts`. The Web UI in M6 drives all
four. Per-agent approval methods (`x402.approve`/`x402.revoke`) and
the `sky10 market` CLI are deferred — the RPC surface alone covers
every flow we have a consumer for today.

## M4 — wallet preflight — **landed**

`pkg/x402/wallet.go` defines `ErrWalletNotInstalled` and
`ErrWalletNotFunded`. The OWS signer probes the wallet's USDC
balance via `BalanceMicros` before signing the EIP-3009 (Base) or
v0 versioned (Solana) authorization; if balance is below the
requirement amount, the signer short-circuits with
`ErrWalletNotFunded`. The probe is best-effort — RPC failures fall
through to signing so a flaky balance endpoint doesn't block the
agent.

Files:

- `pkg/x402/wallet.go` — typed errors
- `pkg/x402/sign.go` — `OWSSigner.BalanceMicros` hook +
  `preflightUSDCBalance` helper invoked from `signEVMExact`
- `pkg/x402/sign_solana.go` — same hook invoked from
  `signSolanaExact`
- `pkg/x402/wallet_test.go` — unit tests covering the underfunded,
  funded, probe-error, and disabled-hook paths

## M5 — x402 endpoint on sandbox comms — **landed**

Agent-facing surface at `/comms/x402/ws`. Daemon wiring lives in
`commands/serve_x402.go`; agent identity is resolved from the
`agent` query parameter against the existing agent registry.

Files: `pkg/sandbox/comms/x402/{endpoint.go, list_services.go,
service_call.go, budget_status.go, changes.go}`.

## M6 — Web UI — **landed**

Settings → Services page covers the catalog browser, per-service
approve/revoke with explicit per-call max price, budget card,
receipts table, search, and tier+status filters. Wallet-status
banner surfaces "OWS not installed" / "no wallet yet" so the
toggle isn't misleading. Per-agent approve/revoke and the changes
panel are deferred (the user-level enable + budget caps cover
the immediate safety story).

Files: `web/src/pages/SettingsServices.tsx`, `web/src/lib/rpc.ts`,
plus the `Settings.tsx` landing card.

## M7 — telemetry and overlay tuning — *deferred*

Receipt records would carry `was_browser_attempted_first` from the
agent's tool-call log; aggregate dashboard would surface "paid
services that beat browse-it-yourself" so we can iterate
`overlay.json` defaults from real signal. Not started — needs the
OpenClaw plugin (M10) to produce the signal.

## M8 — quality and reputation — *deferred*

See [`threat-model.md`](threat-model.md). Outcome scoring,
auto-quarantine, volume anomaly detection. Stays out of scope until
we have enough live agent traffic to learn from.

## M9 — Sign-In-With-X (SIWX) integration — **landed**

Deposit-style services (Venice, Stablephone, Run402) authenticate
via SIWE-signed session headers instead of per-call x402 payments.
`pkg/x402/siwx` owns the message construction (EIP-4361 text via
canonical x402 npm shape), envelope encoding (base64 JSON of
`{address, message, signature, timestamp, chainId}`), and an
OWS-backed signer for EIP-191 personal_sign.

`ServiceManifest.SIWXDomain` opts a service into the flow:
`Backend.Call` builds a fresh SIWX header per request and attaches
it as `X-Sign-In-With-X`. The same per-call x402 path runs on top —
top-up endpoints get paid via `X-PAYMENT`, balance endpoints serve
on SIWX alone.

Live verified end-to-end against Venice — top-up settles a $5 USDC
deposit, chat-completions burns from the credit balance, balance
queries land cleanly without payment.

Files: `pkg/x402/siwx/{doc.go, siwx.go, ows_signer.go,
siwx_test.go}`, `pkg/x402/backend_siwx_test.go`.

## M10 — OpenClaw plugin — *deferred*

Agents in the VM call paid services through the comms endpoint.
Closes the loop from "user funds wallet" to "agent uses paid
service" with the safety story (per-agent caps, audit trail) the
sandbox-comms architecture provides. Not started.

## Out of scope for first cut

- Per-service typed Go clients (`pkg/x402/wrappers/...`) — driven
  later by usage signal.
- Non-x402 payment protocols (MPP, etc).
- Replacing existing API-key flows where users already have
  credentials.
- Webhooks / SSE from agentic.market for push catalog updates (poll
  is fine to start).
- A standalone MCP server for x402. Removed: agents go through
  comms, not MCP.
- `sky10 market` CLI. The host RPC covers every consumer we have;
  CLI is purely cosmetic.
- Per-(agent, service) approval UI. Today every approved agent
  shares the user-level enable; tightening this is a known safety
  hole tracked separately.
- Auto top-up. Deposit-style services require an explicit user
  action to fund credits — Backend doesn't quietly drain on
  insufficient-balance 402s.
