---
created: 2026-04-26
updated: 2026-05-07
model: claude-opus-4-7
---

# x402 Wallet and Budget

## No wallet delegation

The daemon holds the keys, signs every payment, and never hands
signed authority to any agent process. Sandboxed agents call the
guest-local metered-services bridge, which forwards to the host daemon;
host UI and host-local tools use `x402.*` RPC. The wallet is not visible
in agent process memory, agent logs, or agent disk state.

This rules out a class of failure: a buggy agent or a compromised
runtime cannot exfiltrate signing capability. It also keeps the rule
simple — there is exactly one place in the system that can spend, and
it is the daemon.

## Subwallet for x402

A dedicated x402 subwallet is derived from the user's main identity
but funded separately and capped explicitly. Structurally:

- HD-derived (or labeled) child of the main identity, persisted
  alongside other wallet state in `pkg/wallet`.
- The daemon refuses `x402.serviceCall` if the bound wallet is the
  main one — the structural answer to "no wallet delegation".
- The user funds the subwallet explicitly (UI button or
  `sky10 market wallet fund`). First spend should be deliberate.
- Refill is also explicit: the subwallet does not auto-pull from the
  main wallet.

The blast radius is bounded by what is in the subwallet at any
moment. Drained ≠ catastrophic; user refills if and when they choose.

## Budget caps

Three layers, evaluated bottom-up on every call:

| Layer | Default | Configurable |
|---|---|---|
| Per-call max | $0.10 USDC | yes |
| Per-service daily cap | $1.00 USDC | per service |
| Daily total cap | $5.00 USDC | yes |

A call passes only if all three layers allow it. The most restrictive
wins. UI surfaces all three with progress bars.

The caller may set `max_price_usdc` per call below the cap; this is
the `price_quote_too_high` backpressure signal the routing policy
uses to force a fall-back to free local tools when budget is tight.

## Receipt log

Every settled call writes one receipt record to disk under
`os.UserConfigDir()/sky10/x402/receipts.jsonl`:

```json
{
  "ts": "2026-04-26T10:15:00Z",
  "service_id": "perplexity",
  "path": "/search",
  "tx": "0x...",
  "network": "base",
  "amount_usdc": "0.003",
  "challenge_hash": "sha256:...",
  "manifest_hash": "sha256:...",
  "caller": "openclaw:agent-A-abcdef01",
  "max_price_usdc": "0.005"
}
```

Receipts are append-only, exposed via `x402.receipts`, and survive
service removal. They aggregate to the spend totals shown in the
budget UI.

## Refusing to send

The daemon refuses to call a service when any of these are true:

- subwallet not funded
- subwallet balance below the server-quoted price
- per-call, per-service, or daily cap would be exceeded
- service is not approved (or is removed)
- pinned manifest hash diverges from the live one
- caller did not supply `max_price_usdc` in non-permissive mode

Refusals return typed errors so the calling agent (or human) gets a
clear, mechanical reason and the routing rubric can fall back. See
[`agent-integration.md`](agent-integration.md#error-handling-for-callers)
for the error vocabulary.

## Network selection

Each service's manifest declares which networks it accepts. The
transport prefers in this order:

1. Network the user has explicitly preferred for that service
2. Base mainnet (USDC) — default for most agentic.market services
3. Solana mainnet (USDC) — fallback when Base is not offered

Funding is per-network. The subwallet has both an EVM and a Solana
address derived from the same identity; the user funds whichever
networks they intend to use. Calls fail with `insufficient_funds` if
the chosen network's balance is below the quote.
