---
created: 2026-04-26
updated: 2026-05-07
model: claude-opus-4-7
---

# x402 Threat Model

A list of threats from x402 services that pay-per-request integration
exposes us to, what is mitigated by the first cut, and what is
deferred. The first cut **bounds** the damage but does not **detect**
all the ways a malicious service can take payment without delivering
value. Capturing this here so it is not forgotten between milestones.

## Threats

### T1 — Quote-and-rip: charge but return garbage

Service accepts payment, returns 200 with empty / nonsense / error
body. Agent thinks it succeeded; user is out money. **The user's
direct concern.**

- **Bounded by:** per-call cap, per-service daily cap, daily total cap.
- **Not detected by:** anything in the first cut.
- **Future mitigations (deferred):**
  - **Outcome scoring.** Caller (OpenClaw, Hermes, or MCP surface) reports
    whether the response was usable (parsed cleanly, non-empty,
    matched expected schema). Aggregate into a per-service quality
    score persisted with receipts.
  - **Auto-quarantine.** Services with sustained quality below a
    threshold get auto-revoked (or flagged "needs review").
  - **Reputation feed.** Optional opt-in shared scorecard so users
    benefit from each other's observations. Sybil-resistant design
    is its own problem.

### T2 — Quote inflation between approval and call

Service quotes one price at approval time, a higher price at call
time. Pinned `max_price_usdc` from the manifest is the contract; live
quote may exceed it.

- **Mitigated.** Transport refuses to sign if the live quote exceeds
  the caller's `max_price_usdc`. Returns `price_quote_too_high`.

### T3 — Slow upward drift in pricing

Service raises price gradually over time, each step under the
risky-diff threshold.

- **Mitigated.** Diff classifier compares against the **pinned
  baseline**, not the previous refresh. Any increase over the pinned
  number is risky → re-approval queue. No drift window.

### T4 — Endpoint or schema swap on an approved service

Service points its `/.well-known` at a different host or changes
required scopes.

- **Mitigated.** Manifest hash pin + endpoint pin. Live mismatch fails
  closed with `manifest_diverged`.

### T5 — Many cheap calls drain via volume

Service is ostensibly cheap; agent loop is induced into calling it
repeatedly, each call below the per-call cap, summing to the daily
cap.

- **Bounded by:** per-service daily cap and total daily cap.
- **Not detected by:** anything in the first cut.
- **Future mitigations (deferred):**
  - **Volume anomaly detection.** Receipt log already tracks per-service
    call counts; daemon can warn when a previously-low-traffic service
    spikes.
  - **Caller-attributed limits.** Cap per-agent-per-service so one
    runtime cannot dominate spend.

### T6 — Replay or double-charge

Service charges twice for one call by replaying the payment, or by
crafting overlapping challenges.

- **Mostly mitigated.** x402 challenges carry a nonce bound to the
  request; transport must enforce nonce uniqueness within a window
  and refuse to sign two payments for the same logical call.
- **Action item for M1:** explicit nonce uniqueness test in
  `pkg/x402/transport.go`.

### T7 — Directory typosquat / lookalike services

agentic.market lists a service named `openai-x402` that is not
OpenAI; user approves the wrong one.

- **Partially mitigated** by overlay (sky10-curated entries vouch for
  identity) and by human-readable hints in the Web UI.
- **Not mitigated** for services not in the overlay.
- **Future mitigations (deferred):**
  - Show the live `endpoint` host prominently in the approval prompt.
  - Show "this service is in sky10's curated list" or "unknown
    service" badge.
  - Allow users to scope-lock a service to a specific endpoint host
    on first approval.

### T8 — Compromised directory injects malicious entries

agentic.market itself is compromised and lists hostile services.

- **Bounded** because pins do not follow the directory: approved
  services keep their original endpoint and hash. New services from
  the directory default OFF and require explicit approval.
- **Not mitigated** for users who approve a malicious new entry
  before noticing.
- **Future mitigations (deferred):**
  - Multiple directory sources with cross-checking.
  - Optional manual-only mode that disables auto-ingest of new
    directory entries.

### T9 — Wallet exfiltration via agent compromise

A compromised agent runtime tries to extract the signing key.

- **Mitigated by design.** No wallet delegation. Daemon holds keys;
  agents call `x402.serviceCall` over loopback RPC; no signed
  authority ever leaves the daemon process.

### T10 — Network-level censorship / MITM

Attacker on the path between sky10 and the service tampers with
quotes or responses.

- **Mitigated.** TLS via Go stdlib defaults verifies the cert chain.
  Manifest pin catches deeper schema-level tampering at refresh time.

## What this means for milestones

The first cut (M1–M7) covers T2–T4, T6, T9, T10 and **bounds** T1, T5,
T7, T8 via budget caps. It does not detect quote-and-rip, slow drain,
or typosquatting. Those are deferred to a later milestone:

> **M9 — quality and reputation.** Outcome scoring, per-service quality
> scorecards, auto-quarantine, anomaly detection, optional shared
> reputation feed.

This is acceptable for an initial release because the budget caps
ensure no single bad service can do more than the cap's worth of
damage in a day, and receipts make every charge auditable after the
fact. But quality detection should not slip indefinitely — once
spending volume rises, undetected bad actors become the dominant
failure mode.
