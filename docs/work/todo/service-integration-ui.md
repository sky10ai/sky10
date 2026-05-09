---
created: 2026-05-05
updated: 2026-05-05
---

# Service Integration UI

Design notes for how third-party identity providers (Apple, Google,
likely Microsoft) get connected and how their sub-services surface
through sky10. Connecting these providers is a major product
capability in its own right — distinct from but comparable in
importance to the existing paid-API `Services` concept — and a
first-class place for it needs to exist regardless of which
structural option below is picked. Captured from an exploratory
discussion; direction, not a committed plan.

## Existing Settings Taxonomy

Before designing anything new, the relevant pieces already in the
tree:

- **`web/src/pages/Settings.tsx`** — index of settings sub-pages.
- **`SettingsServices.tsx`** — paid metered APIs (x402 and MPP
  protocols). Approve a service with a per-call USDC max, see daily
  caps and receipts. Catalog seeded from external sources
  (Agentic.Market, pay.sh). Tier labels: `primitive` ("hard to do
  locally") vs `convenience` ("optional paid API"). This is the
  existing "agent gets a tool, billed per call" pattern.
- **`SettingsMessaging.tsx`** — per-adapter messenger connections
  (Discord, iMessage, Signal, Slack, Telegram, WhatsApp, plus the
  generic IMAP/SMTP adapter at `pkg/messengers/adapters/imapsmtp/`).
  Each connection carries a policy (`read_inbound`, `send_messages`,
  `require_approval`, `reply_only`, etc.).
- **`SettingsCodex.tsx`** — single-purpose OAuth screen for linking a
  ChatGPT account, using the popup helper at
  `web/src/lib/oauthPopup.ts`. Precedent for "one settings page per
  identity provider."
- **`SettingsApps.tsx`** — local helper binaries (OWS, Lima, Bun,
  Zerobox). Different concept: host-side dependencies, not
  third-party services.
- Other settings pages (Identity, Visuals, Sandboxes, Wallet,
  Secrets) are unrelated to third-party connections.

The `Services` name is therefore already taken: it means **paid,
agent-callable, USDC-billed APIs**. The discussion below is about
identity-provider connections (Apple, Google, Microsoft), which are
*not* what the existing Services page is for.

## The Question

Where does an "Apple account" or "Google account" connection live,
given that:

1. `Services` is already a curated catalog of paid metered APIs and
   would lose its meaning if mixed with OAuth identity providers.
2. iMessage and Gmail-via-IMAP already live on `Messaging` as
   per-adapter connections.
3. `Codex` (ChatGPT) sets a precedent for "one settings page per
   identity provider."

## Working Model

Two-tier shape, refined from the original sketch:

- A place to authenticate the *account* once (Apple ID, Google
  account) so a single OAuth grants all that provider's capabilities
  at once. This cannot be the existing `Services` page without
  diluting that screen's meaning, so it likely needs to be either a
  new screen (`Accounts` or `Connections`) or one page per provider
  in the `SettingsCodex` style.
- Each connected provider exposes its sub-services as slices on the
  surface where they belong:
  - Gmail → `Messaging` (alongside iMessage and IMAP)
  - iMessage → `Messaging` (already there; Apple OAuth would
    *enable* it rather than replace the existing iMessage adapter)
  - Google Calendar / Apple Calendar → a calendar surface (does not
    exist as a sky10 surface yet)
  - Drive / iCloud Drive → a file surface
  - Contacts → contact pickers / autocomplete

The criterion for a provider belonging on the central account screen
is "multi-surface identity provider": one OAuth lights up two or more
primary surfaces. Single-purpose apps (Slack, Discord) connect
inside the one surface they feed — consistent with the current
arrangement, where they already live as per-adapter messenger
connections under `Messaging`.

## Forks Still Open

### Where account-level connections live

- **New `Accounts` screen.** Cleanest separation. Adds one settings
  entry, preserves `Services`' meaning, and lets `Messaging` stay
  focused on per-adapter policy.
- **One page per provider, like Codex.** Settings → Apple, Settings
  → Google, Settings → Microsoft. The pattern already exists in
  this codebase. Risk: settings sprawl as providers multiply.
- **No central screen.** Each surface initiates OAuth when the user
  wants the capability, and a registry under the hood tracks which
  account is linked. Worst for "what's connected here?"
  visibility, but lowest UI surface area.

### How Apple/Google integrations relate to existing adapters

iMessage already exists as a messenger adapter; Gmail-via-IMAP also
exists. If Apple OAuth is added, does:

- The existing iMessage adapter stay, with the Apple-account-linked
  status simply lighting it up?
- The IMAP-based Gmail adapter stay alongside a new OAuth-based
  one?

Coexistence is the safer first cut (do not break existing setups),
but needs a story for users who do not know which to pick.

### How calendars (and similar data) reach agents

Two framings — unresolved and load-bearing for what "connecting" even
means:

- **Tool.** The calendar is a thing the agent calls when it needs
  it ("check my Thursday"). Lazy, cheap on context, agent-driven.
  Mirrors the existing `Services` pattern exactly: approve, set
  caps, see receipts — minus the USDC billing.
- **Context.** Today's events are loaded into every turn so the
  agent already *knows* the user has a 3pm without being asked.
  More tokens per turn, but this is what makes an assistant feel
  like it actually knows the user. No precedent for this shape in
  sky10 today — `Services` is purely tool-shaped.
- **Hybrid.** Ambient for "today/this week," tool for arbitrary
  lookups.

The existing `SettingsServices` shape (per-call approval, per-call
caps, audit-via-receipts) is a strong template for the **tool** form
of agent-callable connections. Identity-provider tools would reuse
the approval/audit shape but skip the USDC billing piece.

There is no precedent for the **context** form. If that path is
picked, it changes the connection UX (granting an ambient signal,
not a tool) and adds a runtime story for how the signal gets injected
into agent context turns.

### Edge cases not yet thought through

- **Multi-account within one provider.** Two Google accounts (work +
  personal). Does the central screen show two Google entries, or
  one with sub-accounts? Per-surface slices need to know which
  account they are pulling from.
- **Cross-provider unified surfaces.** A unified inbox combining
  Gmail + iMessage. Surface concern, central-registry concern, or
  both?
- **Per-agent vs per-device vs per-user grants.** Mirrors the open
  question already raised in `apple-system-plugin.md`. Identity
  providers especially need this answered.

## Stance

Direction, not a plan. Two things should be settled before
implementation starts:

1. **Tool vs context vs hybrid** for how identity-provider data
   reaches agents — load-bearing for everything else.
2. **Central screen vs per-provider page vs per-surface OAuth** for
   where the connection lives.

The original instinct (a central screen, sub-services as slices on
surfaces) survives the existing-taxonomy reality check, but it
cannot just be the current `Services` page — that name is already
allocated to paid metered APIs.

## Related

- [`apple-system-plugin.md`](apple-system-plugin.md) — host-side
  plugin design for Apple integrations, including permission
  classes and audit story that any Apple OAuth would ride on.
