---
created: 2026-04-25
updated: 2026-04-25
---

# RPC Tool Policy

## Product Rule

The AI-first app should make user-configurable daemon capabilities available
from the RootAgent by default.

The source of truth remains the Go daemon and its JSON-RPC surface. The root
assistant should expose curated model-facing tools over those RPC methods using
the Vercel AI SDK `tool()` configuration, not raw backend method names. The user
experience should be:

1. user describes an outcome
2. assistant inspects live RPC state
3. assistant drafts the exact change plan
4. user approves mutating calls
5. assistant executes the approved RPC-backed tools
6. resulting state remains visible in the normal app surfaces

## Default Exposure

If a user can configure it through an ordinary UI page or CLI path backed by
RPC, it should be eligible for the root agent.

Examples that should become assistant-addressable:

- drives: create, start, stop, remove, inspect sync health
- files: create folders, remove files, diagnose conflicts and path issues
- devices: invite, join, approve, remove, inspect membership
- secrets: write, rewrap, sync, delete, inspect summaries without leaking values
- sandboxes: create, start, stop, delete, inspect logs and runtime health
- apps and wallets: install, uninstall, update, inspect status, create wallets
- system updates: check, stage, install, restart
- agents: create a durable plan, provision runtime, route messages, inspect
  health and delivery

Read-only tools can execute without confirmation. Mutating tools must be
approval-gated with visible parameters, expected effects, and audit records.

## Not Enabled By Default

These surfaces should not become ordinary root-agent tools just because an
RPC method exists:

- raw debug and repair endpoints, including low-level S3 debug list/delete
- broad KV internal mutation, especially internal namespaces or pattern deletes
- raw mailbox queue mutation such as claim, release, retry, or internal ack
- destructive Apple-system operations such as deleting notes, reminders,
  calendar events, albums, folders, or photos
- wallet transfers or external spend without explicit high-friction approval
- unrestricted local host control, accessibility, screen capture, Mail,
  Messages, or Contacts access
- platform-specific actions that are unavailable on the current OS

## Todo Findings

The current `docs/work/todo/` set leaves these AI-tool boundaries:

- [`device-subsystem-followup.md`](../../todo/device-subsystem-followup.md):
  device history and first-class `device.*` RPCs are not done yet. Use current
  `identity.*` compatibility RPCs for membership workflows until the new device
  surface lands.
- [`fs-sync-experience-improvements.md`](../../todo/fs-sync-experience-improvements.md):
  richer transfer sessions, cancel, retry, and detailed phase control are not
  available yet. The assistant can inspect current sync activity, but should
  not promise transfer-session control until those RPCs exist.
- [`s3-optional-remaining.md`](../../todo/s3-optional-remaining.md): hot-adding S3
  storage and content-addressed pinning are not implemented. Low-level S3 debug
  operations stay out of the default assistant profile.
- [`windows-support.md`](../../todo/windows-support.md): Windows RPC transport and
  the Windows agent runtime/hypervisor are unfinished. Assistant tools must
  report unavailable runtime actions clearly on Windows rather than assuming
  Lima or Unix socket behavior.
- [`apple-system-plugin.md`](../../todo/apple-system-plugin.md): Apple helper tools
  should start narrow. Destructive delete/remove verbs are disabled by default,
  and updates or batch writes require approval and audit.

## Implementation Shape

Keep model-facing tools domain-oriented. A user should ask for "create a drive
for agent outputs", not "call `skyfs.driveCreate`".

Tool definitions should carry:

- category: read-only, approval-required, admin/debug, or disabled
- RPC methods used
- required inputs
- preview text for approval cards
- risk level
- platform availability
- audit fields

In code, the default shape is `web/src/lib/rootAgentTools.ts`: AI SDK tool
definitions with `inputSchema`, `execute`, and `needsApproval` for mutating
RPCs. Assistant flows should depend on that registry instead of importing raw
RPC clients directly.

The approval framework should be the boundary between "AI can plan almost every
configurable RPC workflow" and "AI silently changed my system".
