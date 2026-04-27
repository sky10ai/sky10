---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# x402 Catalog Auto-Update

A new x402 service should appear in sky10 within an hour of being
added to agentic.market — OFF by default and listed in the Services
UI for the user to opt in. Approved services should auto-update only
when the change is safe; risky changes pause for explicit re-approval.

## Refresh loop

Default cadence: every 60 minutes with ±10 minute jitter to avoid
synchronized fleet thundering. Configurable via daemon config.

```
1. Fetch /v1/services from agentic.market
2. Fetch user-added direct URLs from local config
3. For each entry, fetch its /.well-known/x402.json
4. Compute manifest hash
5. Diff against on-disk cache → classify each entry
6. Apply safe changes immediately
7. Queue risky changes and new services for review
8. Persist cache; emit daemon event 'x402.changes'
```

Manual refresh: `x402.refreshCatalog` RPC or `sky10 market refresh`.

## Diff classification

| Diff kind | Detection | Action |
|---|---|---|
| **New service** | `service_id` not in cache | added to registry, `tier=convenience`, `approved=false`, surfaces in "new" badge |
| **Description / category text changed** | metadata fields differ but endpoint, schema, price unchanged | auto-applied |
| **Price decreased** | new max ≤ pinned baseline | auto-applied; pin updated |
| **Price increased** | new max > pinned baseline | risky → re-approval queue; existing pin still serves calls |
| **Endpoint host or path changed** | URL fields differ | risky → re-approval queue |
| **New required scope or capability** | manifest declares new scope | risky → re-approval queue |
| **Schema breaking change** | response/request schema diff | risky → re-approval queue |
| **Service removed** | absent from `/v1/services` and `/.well-known` unreachable | marked `removed`; receipts retained; pinned services keep working until upstream actually 404s |
| **Service re-listed** | previously removed entry returns | requires fresh approval like new |

## Pinning

When a user approves a service, sky10 records:

```json
{
  "service_id": "perplexity",
  "endpoint": "https://api.perplexity.ai",
  "manifest_hash": "sha256:...",
  "max_price_usdc": "0.005",
  "approved_at": "2026-04-26T10:00:00Z"
}
```

The transport layer enforces the pin on every call:

- live `/.well-known/x402.json` hash must match `manifest_hash`, or a
  refresh has already updated it via a "safe" diff
- `endpoint` host + base path must match
- server-quoted price must be ≤ `max_price_usdc` per call

Pin updates only on:

- safe diff applied during refresh, or
- explicit re-approval after a risky diff

## Repo-curated overlay

`pkg/x402/discovery/overlay.json` ships in the sky10 binary and
carries sky10's editorial layer over the upstream catalog:

```json
{
  "deepgram":    { "tier": "primitive",   "default_on": true,  "hint": "Speech-to-text. Local tools cannot substitute." },
  "fal":         { "tier": "primitive",   "default_on": true,  "hint": "Image and video generation. Local tools cannot substitute." },
  "e2b":         { "tier": "primitive",   "default_on": true,  "hint": "Sandboxed code execution. Use when shell tool is too risky." },
  "browserbase": { "tier": "primitive",   "default_on": true,  "hint": "Residential-IP browser sessions. Use when local browser is blocked or fingerprinted." },
  "anthropic":   { "tier": "primitive",   "default_on": false, "hint": "Most users already have a direct Anthropic API key; enable this only if you specifically want to spend USDC instead." },
  "openai":      { "tier": "primitive",   "default_on": false, "hint": "Most users already have a direct OpenAI API key; enable this only if you specifically want to spend USDC instead." },
  "perplexity":  { "tier": "primitive",   "default_on": false, "hint": "Use for current-events search when no direct API key is available." },
  "exa":         { "tier": "primitive",   "default_on": false, "hint": "Use for web search and content retrieval when no direct API key is available." },
  "tripadvisor": { "tier": "convenience", "default_on": false, "hint": "Browser tool can scrape this for free. Prefer browser unless you need structured ratings." },
  "apollo":      { "tier": "convenience", "default_on": false, "hint": "Browser tool can do most queries. Use only when you need verified contact data." }
}
```

The `default_on` set is intentionally narrow: only services where the
local tool stack genuinely cannot substitute (audio I/O, image/video
generation, sandboxed code execution, residential-IP browsing). LLM
and search primitives are present in the overlay but default OFF
because most users have direct API keys for them and would not want to
route through x402 USDC by default.

- Upstream catalog provides identity, endpoint, and price; the overlay
  provides tier classification, on/off default, and routing hint.
- Overlay updates ride on sky10 releases initially.
- Future: split overlay into its own update channel so editorial
  changes do not require a binary release.

## Surfacing changes

The daemon emits `x402.changes` after every refresh with three queues:

```json
{
  "new":     [ { "service_id": "...", "summary": "..." } ],
  "review":  [ { "service_id": "...", "diff_kind": "price_increased", "old": "...", "new": "..." } ],
  "removed": [ { "service_id": "...", "last_seen": "..." } ]
}
```

- Web UI shows a "N new services available" badge driven by `new`.
- Web UI shows a "Y services need review" prompt driven by `review`.
- CLI: `sky10 market changes`.

## Failure modes

| Condition | Behavior |
|---|---|
| agentic.market unreachable | log Warn, keep serving from cache, retry at next tick |
| Per-service `/.well-known/x402.json` unreachable | mark transient; after N consecutive misses, mark `degraded` |
| Manifest hash mismatch on a pinned approved service | call fails closed with `manifest_diverged`; user prompted to re-approve |
| Catalog grew unexpectedly large | refresh paginates; cap on registry size with telemetry |
| Clock skew (nonces, signatures) | log Warn; use upstream-provided timestamps when present |
