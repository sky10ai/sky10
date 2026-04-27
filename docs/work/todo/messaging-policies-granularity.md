---
created: 2026-04-27
updated: 2026-04-27
---

# Messaging Policies — Per-Adapter Granularity

The current messaging policies UI in
[`web/src/pages/SettingsMessaging.tsx`](../../../web/src/pages/SettingsMessaging.tsx)
exposes only the generic `PolicyRules` booleans (read, draft, send,
require approval, reply only, etc.) plus per-section grouping. That is
deliberately the lowest common denominator across messengers.

Real-world policies need more sophistication, and the right shape of
that sophistication varies per messenger. We pulled an early attempt at
generic scope inputs (channel and identity allowlists) because the
"right" scoping language is adapter-specific and shoving a single shape
in the UI hides important distinctions.

## Examples to Support

- Slack
  - "Read messages in `#watercooler` only" — channel-scoped read.
  - "Respond inside threads but never start new conversations" —
    distinct respond vs initiate axis with channel scope.
  - "Only DM people in `#leads` channel members" — derived identity
    scope from a container.
- Email (IMAP / SMTP)
  - "Do X when any email comes from the board" — needs a saved
    identity group (the board) as a trigger, plus an automation
    primitive that says what X is.
  - "Auto-archive newsletters" — sender pattern + action.
- Telegram
  - Group / channel scoping is its own model.

## Concepts We Probably Need

- Per-adapter scope object: each adapter declares the shape of its
  scope (channel pickers for Slack, sender groups for email, etc.).
- Saved groups: named, reusable allowlists/denylists ("the board",
  "support team"). These outlive any one policy.
- Triggers and automations: a policy is "what an agent is allowed to
  do"; an automation is "what should happen when X arrives". These
  should not be conflated.
- Per-rule scope override: e.g. read everywhere but only respond in a
  subset.

## What Exists Today

- Backend `PolicyRules` in
  [`pkg/messaging/types.go`](../../../pkg/messaging/types.go) already
  has `AllowedContainerIDs []ContainerID` and
  `AllowedIdentityIDs []IdentityID` on the rules struct, but they are
  globally applied to the whole policy and the UI does not surface
  them.
- No backend RPCs for policy CRUD yet
  (`messaging.listPolicies` / `messaging.upsertPolicy` /
  `messaging.deletePolicy` are not in
  [`pkg/messaging/rpc/handler.go`](../../../pkg/messaging/rpc/handler.go)).
- No concept of saved identity / container groups.
- No automation / trigger layer.

## Suggested Next Steps (when we revisit)

1. Land the basic policy CRUD RPCs and persistence so the existing
   editor can save real policies.
2. Decide whether per-adapter scope shapes go in the adapter manifest
   (declarative) or in adapter-specific React renderers (imperative).
3. Prototype the saved-groups concept against email first, since the
   "group of board emails" example is the most concrete.
4. Treat triggers / automations as a separate object from policies and
   sketch the storage shape before any UI work.
