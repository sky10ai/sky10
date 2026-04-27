---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# x402 Architecture

## High-level shape

```
                   agentic.market /v1/services
                   per-service /.well-known/x402.json
                   user-added direct URLs
                              │
                              ▼  poll, validate, pin
              ┌────────────────────────────────────┐
              │ pkg/x402                           │
              │   discovery → registry             │
              │   transport (RoundTripper)         │
              │   policy + budget + receipts       │
              │   wallet binding (Base + Solana)   │
              └─────┬───────────────────┬──────────┘
                    │ daemon RPC        │
                    ▼                   ▼
       ┌─────────────────────┐  ┌────────────────────────┐
       │ OpenClaw plugin     │  │ sky10-x402-mcp         │
       │ → x402 tools        │  │ MCP server →           │
       │                     │  │ hermes / codex / etc   │
       └─────────────────────┘  └────────────────────────┘
                    ▲                       ▲
                    └───────────┬───────────┘
                                │
                       Web UI + sky10 market CLI
```

The daemon owns the wallet, the registry, and the policy gate. Every
consumer goes through `x402.serviceCall` over loopback RPC. No agent
process ever sees signed authority.

## Package layout

```
pkg/x402/
├── doc.go                       package overview
├── protocol.go                  PaymentChallenge, PaymentReceipt, ServiceManifest, Tier
├── transport.go                 http.RoundTripper: 402 → sign → retry, max_price_usdc gate
├── sign.go                      SIWE (EVM) + SIWS (Solana) wrappers over pkg/wallet
├── registry.go                  in-memory catalog: list, get, search
├── registry_store.go            on-disk JSON cache under os.UserConfigDir()
├── budget.go                    daily cap, per-call max, per-service cap, receipt log
├── policy.go                    per-service approval state + risk classification
├── pin.go                       manifest hash + endpoint pin enforcement
├── discovery/
│   ├── client.go                fetch /v1/services and /.well-known/x402.json
│   ├── refresh.go               periodic loop with jittered backoff
│   ├── diff.go                  classify upstream changes
│   ├── overlay.go               repo-curated tier/hint/default-on metadata
│   ├── overlay.json             curated overlay data (data file)
│   └── sources.go               pluggable: agentic.market + user URLs
└── rpc/
    └── handler.go               Dispatch(method, params) for x402.* RPC

cmd/sky10-x402-mcp/              standalone MCP server (later milestone)
```

## Trust model

- agentic.market is treated as a directory, not the source of truth.
- Each service's own `/.well-known/x402.json` is the authoritative
  manifest; sky10 pins
  `{ endpoint, manifest_hash, max_price_baseline }` per approved
  service.
- Transport refuses to call a service whose live response no longer
  matches its pin; this fails closed.
- TLS cert chain is verified by Go stdlib defaults; no extra pinning
  at this layer.
- A compromised directory can list bad services but cannot redirect
  approved ones because pins do not follow the directory.
- The wallet that signs is a dedicated x402 subwallet capped by
  budget. See [`wallet-and-budget.md`](wallet-and-budget.md).

## Data flow: agent calls a paid service

```
1. Agent (e.g. OpenClaw) decides to call service X
2. Agent sends RPC: x402.serviceCall { service_id, path, body, max_price_usdc }
3. Daemon checks: approved? not removed? within per-call cap? budget left?
4. Daemon issues HTTP request via x402 transport
5. Upstream returns 402 with payment challenge
6. Transport signs USDC payment with the x402 subwallet (Base or Solana per challenge)
7. Transport re-issues request with X-PAYMENT header
8. Upstream returns 200 with response body and X-PAYMENT-RESPONSE receipt
9. Daemon records receipt, decrements budget, returns response to agent
```

If any step fails, the transport returns a typed error without ever
exposing payment details to the agent:

- `service_not_approved`
- `service_removed`
- `price_quote_too_high` — server-quoted price exceeds caller's
  `max_price_usdc`
- `budget_exceeded`
- `insufficient_funds` — subwallet balance below quote
- `payment_failed` — signing or settlement error
- `manifest_diverged` — pin no longer matches live `/.well-known`

## RPC surface

| Method | Purpose |
|---|---|
| `x402.serviceList` | catalog with tier, price, approval, pin |
| `x402.serviceSearch` | keyword search |
| `x402.serviceCall` | invoke a service |
| `x402.serviceApprove` / `serviceRevoke` | per-service consent |
| `x402.budgetStatus` / `budgetSet` | spend caps and current usage |
| `x402.receipts` | paginated receipt log |
| `x402.refreshCatalog` | manual refresh |
| `x402.changes` | pending review queue (new / risky / removed) |

Each method returns a typed result and well-formed JSON-RPC errors.
The handler lives in `pkg/x402/rpc/handler.go` and registers under the
existing daemon dispatcher alongside `agent.*`, `home.*`, `wallet.*`,
etc.

## Cross-platform readiness

- Pure HTTPS + stdlib JSON + filesystem; no Unix sockets, no signals,
  no shell-outs in the hot path.
- Cache lives under `os.UserConfigDir()`.
- Refresh uses `time.Ticker` cancellable via context.
- Wallet wraps the existing OWS binary install flow which is already
  cross-platform (see `pkg/wallet/install.go`).
