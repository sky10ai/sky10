---
created: 2026-05-06
model: claude-opus-4-7
---

# x402 Market Services Integration

This entry archives the completed slice of x402 work on the
`integrate-market-services` branch. It does not retire
`docs/work/current/x402/`; the architecture, threat model, agent
integration, wallet-and-budget, and auto-update docs remain canonical,
and the implementation plan stays trimmed to the milestones that have
not yet shipped (M7 telemetry, M8 quality/reputation, M10 OpenClaw
plugin).

What landed: a working x402 client that signs real EIP-3009 (Base) and
v0 versioned (Solana) authorizations via OWS, dispatches v1 and v2
wire shapes, picks the cheapest tier within preferred networks, and
has been live-verified against seven facilitators across both chains
plus a native Venice integration via Sign-In-With-X. The Web UI
surfaces the catalog, per-service approval, budget, and receipts.

## Why This Matters

The goal of this work was the same as the broader x402 motivation: an
agent that can browse the web and call APIs needs to pay for things
the user has authorized, without the user wiring API keys for every
service or rebuilding payment logic per vendor. x402 turns
HTTP `402 Payment Required` into a real handshake the agent can sign
against once the user has approved the service and a budget.

For sky10 specifically:

- The user funds one wallet, approves services from a catalog, and
  the daemon takes care of signing each per-call authorization on
  the right chain.
- Agents in sandboxes call paid services through the same comms
  endpoint they use for any host capability — the payment surface is
  not a separate auth/credentials concept they have to learn.
- Per-service enable + budget caps + a receipts log give the user a
  legible safety story before agents spend live USDC.

This slice landed everything from "no x402 surface at all" to "Venice
top-up of $5 plus a real chat-completions call settled out of the
deposited credit balance." The remaining work is signal collection
(M7) and feeding live agent traffic (M10), not protocol mechanics.

## Landed Commits

In chronological order on the branch:

- `a43e32fd` `docs(x402): add service integration plan`
- `8fb0c570` `docs(x402): add threat model and M9 quality/reputation milestone`
- `ba412027` `docs(agent-bus): add foundational typed-envelope bus plan`
- `381d11c0` `docs(sandbox-comms): per-intent endpoints replace unified bus`
- `13c74f7a` `feat(sandbox/comms): add M1 transport plumbing`
- `05ce4096` `feat(sandbox/comms/x402): add x402 capability endpoint and handlers`
- `c737af32` `feat(x402): add protocol core, registry, budget, transport, Backend`
- `bbbcc155` `feat(commands): wire x402 endpoint into daemon serve`
- `c21f5479` `feat(x402): add discovery package and seed registry on daemon start`
- `5c1b572c` `feat(x402,web): add Settings → Services page with Agentic.Market section`
- `3c8e47ae` `feat(x402): live agentic.market source, refresh ticker, wallet banner`
- `fb1ebd78` `feat(x402): real protocol wire shape + OWS-backed signer`
- `779c5463` `feat(x402): OWS Solana signing path`
- `459c6f72` `feat(x402,web): budget + receipts panel; per-service approval flow`
- `75c49733` `feat(x402,web): persistent receipts log + search/filter on Services page`
- `084be105` `feat(x402): support both v1 and v2 wire shapes`
- `4742debd` `feat(x402): split v1 and v2 wire protocols, verified live against Exa`
- `68d45e42` `test(x402): capture live wire fixtures, fixture-driven structural tests`
- `81ee0cdf` `test(x402): live-verified against 4 services (3 v2 + 1 v1) on Base`
- `f93f0213` `feat(x402): pick cheapest within preferred networks; Solana smoke target`
- `39fbdcfb` `test(x402): smoke against Quicknode Solana mainnet (envelope OK, settle TBD)`
- `935cdd06` `feat(x402): live Solana mainnet payment via Alchemy`
- `6297ff7f` `test(x402): live Solana smoke against Coingecko (third-party SVM facilitator)`
- `52331295` `test(x402): live Messari smokes on both Base and Solana mainnet`
- `c48c7b8e` `fix(sandbox/comms): satisfy bodyclose + sloglint on CI`
- `f56195e9` `test(x402): tolerate Venice's missing-scheme variant; static fixtures`
- `4a9b98fc` `feat(x402): native Venice integration via Sign-In-With-X (SIWX)`
- `0324d980` `feat(x402): live Venice top-up + chat-completions via SIWX + canonical envelope`
- `fbbe1dbd` `test(x402): load .env for live smoke; document wallet-as-key model`
- `a0a6d45b` `feat(x402): wallet preflight + plan-doc refresh (M4)`

## Architecture

```
agent in sandbox            host daemon                facilitator
+-----------------+        +-----------------+        +-----------+
|                 |  ws    |  comms/x402/ws  |  http  |  service  |
|  tool call      |<------>|  endpoint       |<------>|  endpoint |
|                 |        |        |        |        |  + 402    |
+-----------------+        |        v        |        +-----------+
                           |   pkg/x402      |              |
                           |   Backend.Call  |              |
                           |        |        |              |
                           |        v        |        OWS   |
                           |   sign.go +     |  hex sign    |
                           |   sign_solana.go|------ows-----+
                           |        |        |
                           |        v        |
                           |  retry with     |
                           |  X-PAYMENT /    |
                           |  Payment-Sig /  |
                           |  X-402-Payment  |
                           +-----------------+
```

The `pkg/x402` package owns the wire protocol (v1 + v2 dispatch,
PreferAndCheapest tier-and-network selection, signed-payload
construction, retry header dance). `pkg/x402/discovery` ingests the
agentic.market catalog. `pkg/sandbox/comms/x402/` exposes the
agent-facing surface at `/comms/x402/ws`. The Web UI in
`web/src/pages/SettingsServices.tsx` is the user-facing approval and
audit surface.

## What Was Built

### Protocol core (M1)

`pkg/x402` implements the wire protocol end-to-end. The wire shape
evolved across the iteration:

- **v1 + v2 dual-version support.** v1 carries the challenge in the
  body with `maxAmountRequired`; v2 carries it in a `Payment-Required`
  response header with `amount` (integer base units). Per-version
  parsers live in `protocol_v1.go` / `protocol_v2.go`; transport
  detects which version a server speaks from the response shape and
  dispatches.
- **Three retry header names.** `X-Payment` (Coinbase / Exa),
  `Payment-Signature` (Smartflow), `X-402-Payment` (Venice). All
  three carry the same envelope; servers pick the one they read.
- **Hybrid v2 envelope.** Top-level `scheme`+`network` (canonical
  x402 npm shape, Venice requires it) alongside `accepted` (echoed
  verbatim via `RawWire` so vendor extensions round-trip) and an
  optional `resource` block.
- **Receipt parsing is version-blind.** Tries plain JSON, then
  base64 JSON, then a bare tx-hash fallback (Messari's wire form).
- **PreferAndCheapest selection.** Filters offered tiers by manifest
  networks, then picks the cheapest within the preferred set instead
  of the first matching entry.

Files:

- `pkg/x402/protocol.go` — shared canonical types
- `pkg/x402/protocol_v1.go` / `protocol_v2.go` — version-specific
  parsers and encoders
- `pkg/x402/transport.go` — http round-trip with version dispatch
- `pkg/x402/sign.go` + `sign_solana.go` — OWS-backed Signer with
  EIP-3009 EVM and v0 versioned Solana signing paths
- `pkg/x402/registry.go`, `budget.go`, `pin.go`, `policy.go`
- `pkg/x402/backend.go` — single Backend used by both the host RPC
  and the comms handlers
- `pkg/x402/testdata/` — captured live wire fixtures from each
  smoked service

### Discovery and refresh (M2)

`pkg/x402/discovery` ingests the agentic.market catalog plus a
curated overlay. Daemon seeds the registry on startup, then refreshes
hourly with ±10min jitter.

Files: `pkg/x402/discovery/{client.go, refresh.go, diff.go,
overlay.go, sources.go, agentic.go, static.go}`.

### Host-side RPC (M3)

`pkg/x402/rpc/handler.go` ships `x402.listServices`,
`x402.setEnabled`, `x402.budgetStatus`, `x402.receipts`. The Web UI
in M6 drives all four. Per-agent approval methods
(`x402.approve` / `x402.revoke`) and a `sky10 market` CLI were
explicitly skipped — the RPC surface alone covered every consumer
this slice had.

### Wallet preflight (M4)

`pkg/x402/wallet.go` defines `ErrWalletNotInstalled` and
`ErrWalletNotFunded`. The OWS signer probes the wallet's USDC
balance via a `BalanceMicros` hook before signing the EIP-3009 (Base)
or v0 versioned (Solana) authorization; if balance is below the
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

### Sandbox comms endpoint (M5)

Agent-facing surface at `/comms/x402/ws`. Daemon wiring lives in
`commands/serve_x402.go`; agent identity is resolved from the
`agent` query parameter against the existing agent registry.

Files: `pkg/sandbox/comms/x402/{endpoint.go, list_services.go,
service_call.go, budget_status.go, changes.go}`.

### Web UI (M6)

Settings → Services page covers the catalog browser, per-service
approve/revoke with explicit per-call max price, budget card,
receipts table, search, and tier+status filters. Wallet-status
banner surfaces "OWS not installed" / "no wallet yet" so the
toggle isn't misleading. Per-agent approve/revoke and the changes
panel were explicitly out of scope (the user-level enable + budget
caps cover the immediate safety story).

Files: `web/src/pages/SettingsServices.tsx`, `web/src/lib/rpc.ts`,
plus the `Settings.tsx` landing card.

### Sign-In-With-X (SIWX) integration (M9)

Deposit-style services (Venice, Stablephone, Run402) authenticate via
SIWE-signed session headers instead of per-call x402 payments.
`pkg/x402/siwx` owns the message construction (EIP-4361 text via
canonical x402 npm shape), envelope encoding (base64 JSON of
`{address, message, signature, timestamp, chainId}`), and an
OWS-backed signer for EIP-191 personal_sign.

`ServiceManifest.SIWXDomain` opts a service into the flow:
`Backend.Call` builds a fresh SIWX header per request and attaches
it as `X-Sign-In-With-X`. The same per-call x402 path runs on top —
top-up endpoints get paid via `X-PAYMENT`, balance endpoints serve
on SIWX alone. The Venice "API key" is effectively the wallet
itself: any address that can SIWX-sign for the same domain has the
same access, so the wallet binding is the credential.

Files: `pkg/x402/siwx/{doc.go, siwx.go, ows_signer.go,
siwx_test.go}`, `pkg/x402/backend_siwx_test.go`.

## Live Verification

Live smokes live in `pkg/x402/live_smoke_test.go`, gated on
`X402_LIVE=1`. Each smoke captures a wire fixture under
`pkg/x402/testdata/` so future structural tests run without burning
real USDC. Sensitive headers (`X-PAYMENT`, `X-Sign-In-With-X`) are
redacted in saved fixtures.

| Service | Version | Network | Price | Mode |
|---|---|---|---|---|
| Exa `/contents` | v2 | Base | $0.001 | POST |
| Blockrun | v2 | Base | $0.001 | GET |
| Smartflow | v2 | Base | $0.001 | GET |
| Browserbase | v1 | Base | $0.010 | POST |
| Alchemy `/solana-mainnet/v2` | v2 | Solana mainnet | $0.001 | POST |
| Coingecko | v2 | Solana mainnet | $0.010 | GET (third-party SVM facilitator) |
| Messari | v2 | Base + Solana | $0.10 | GET (covers both chains) |
| Venice top-up | SIWX + v2 | Base | $5.00 | POST (deposit) |
| Venice chat-completions | SIWX | — | — | POST (settles from credit balance) |
| Venice balance | SIWX | — | — | GET |

Real bugs uncovered along the way and fixed in this slice:

- `canonicalizeNetwork` was tightened to only accept the Solana
  mainnet genesis hash; previously it treated any `solana:<cluster>`
  as `NetworkSolana` and PreferAndCheapest could pick a Quicknode
  devnet entry over a real mainnet target.
- The Solana payload moved from raw `Transfer` to `TransferChecked`
  with mint + decimals, plus a Compute Budget instruction sequence
  (`SetComputeUnitLimit`, `SetComputeUnitPrice`, `TransferChecked`,
  optional Memo) before signing — facilitators rejected the
  earlier shape with `transaction could not be decoded`.
- Smartflow rejected the standard envelope until `Payment-Signature`
  was added as an alternate retry header and `mimeType` was made
  non-omitempty.
- Messari returns the receipt as a bare hex tx hash rather than
  JSON; `parseReceiptBareTx` covers it without a vendor branch.
- Venice rejected the strict v2 envelope until top-level `scheme` +
  `network` were added alongside `accepted`/`resource` — the hybrid
  shape is what the canonical x402 npm client emits.
- Venice expects `X-402-Payment` rather than `X-PAYMENT` and
  surfaces nonce reuse on retry as `INVALID_PAYMENT_FORMAT`; a 402
  from Venice means "go top up your credit balance," not "pay this
  individual call."

Quicknode's Solana mainnet endpoint returns an opaque
"Unexpected error verifying payment" against an envelope every other
facilitator accepts; documented as a service-specific quirk and
dropped from the smoke matrix.

## Wallet As Key

The Venice work surfaced a useful generalization: for SIWX-based
services, the wallet *is* the credential. A 64-character signing key
that can produce a SIWE for the service domain has the same
authority as any API key the service might issue. That makes
per-service "API keys" derivable from the wallet rather than
separately stored, and the x402 `.env` saved on the branch is just a
recipe for re-deriving them, not a secret in itself. This keeps the
single-wallet user story intact across deposit-style services.

## What Is Still Current Work

`docs/work/current/x402/implementation-plan.md` keeps the deferred
milestones:

- **M7 — telemetry and overlay tuning.** Receipt records carrying
  `was_browser_attempted_first` from the agent's tool-call log; an
  aggregate dashboard surfacing "paid services that beat browse-it-
  yourself" so we can iterate `overlay.json` defaults from real
  signal. Needs M10 first.
- **M8 — quality and reputation.** Outcome scoring,
  auto-quarantine, volume anomaly detection. Stays out of scope
  until enough live agent traffic exists to learn from.
- **M10 — OpenClaw plugin.** Agents in the VM call paid services
  through the comms endpoint. Closes the loop from "user funds
  wallet" to "agent uses paid service" with the safety story
  (per-agent caps, audit trail) the sandbox-comms architecture
  provides.

Explicitly out of scope and not on the plan:

- per-service typed Go clients (`pkg/x402/wrappers/...`)
- non-x402 payment protocols (MPP, etc.)
- replacing existing API-key flows where users already have
  credentials
- a standalone MCP server for x402 (agents go through comms)
- `sky10 market` CLI (host RPC covers every consumer)
- per-(agent, service) approval UI (today every approved agent
  shares the user-level enable; tightening tracked separately)
- auto top-up (deposit-style services require explicit user action
  to fund credits)

## Validation

`go test ./... -count=1` was green at each landed commit. The live
smoke matrix in the table above ran end-to-end across the seven
external facilitators plus Venice; saved wire fixtures in
`pkg/x402/testdata/` give structural regression coverage without
burning USDC on every test run.

CI lint (`golangci-lint`) passed after `c48c7b8e` (bodyclose on
websocket dial responses, sloglint switching to
`slog.DiscardHandler`). Branch is rebased on `origin/main` and ready
to land via fast-forward.
