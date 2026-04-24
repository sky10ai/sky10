---
created: 2026-04-24
updated: 2026-04-24
---

# Apple System Plugin

Track a possible macOS host-side plugin that exposes selected Apple app and OS
integrations to sky10 agents through a controlled capability surface.

This should be a system plugin, not guest agent code. Sandboxed Lima/OpenClaw
or Hermes agents should call sky10, and sky10 should broker access to a local
macOS helper running on the host that owns the Apple permissions.

## Product Shape

The first version should expose a narrow, auditable set of personal-productivity
capabilities:

- Calendar: list calendars, search events, create/update events
- Reminders: list lists, search reminders, create/update reminders, complete
      reminders
- Notes: list folders, search notes, read selected notes, create/update notes
- Photos, later: list albums, search assets, create albums/folders, add assets
      to albums, import/export selected assets
- Shortcuts, later: run explicitly approved shortcuts with structured inputs
- Notifications: notify the user when an agent needs review or approval

Avoid broad "control my Mac" access in the first slice. Accessibility, screen
capture, Mail, Messages, and Contacts are useful, but they need a stronger
approval and audit story before they become default agent tools.

## Permission Model

Apple app permissions are app/process scoped, not perfect per-action sandboxes.
The plugin must enforce sky10-level policy instead of assuming macOS can express
exactly what agents may do.

Initial policy classes:

- `read_only`: list/search/read metadata and selected content
- `create_only`: create new events, reminders, notes, albums, or folders
- `organize`: move or attach existing objects without deleting source data
- `approval_required`: update existing user data or perform batch writes
- `disabled`: destructive operations such as deleting notes, reminders, events,
      albums, folders, or photos

The model-facing tool contract should not include destructive verbs by default.
If delete/remove tools are ever added, they should be separate, approval-gated,
and absent from ordinary agent profiles.

## Architecture Direction

- Add a Darwin-only host helper, likely Swift for native frameworks and a small
      JSON-RPC or MCP-compatible process boundary.
- Use EventKit for Calendar and Reminders.
- Use Apple Events/JXA for Notes unless a better public Notes API becomes
      available.
- Keep sky10 as the policy and audit boundary. Agents should never receive raw
      app credentials or unmanaged local automation access.
- Register the helper as a local capability provider that sky10 can expose as
      curated model-facing tools.
- Record all mutating calls with actor, target app, target object ID, operation,
      preview summary, approval state, and result.
- Provide explicit unsupported status on non-macOS platforms to preserve the
      repo's Windows readiness posture.

## Reference Repos

- [`PsychQuant/che-ical-mcp`](https://github.com/PsychQuant/che-ical-mcp):
      strongest Calendar and Reminders reference. Native Swift/EventKit,
      recurring events, search, conflict detection, batch operations, and
      practical MCP tool shaping.
- [`zish-rob-crur/nucleus-apple-mcp`](https://github.com/zish-rob-crur/nucleus-apple-mcp):
      smaller Calendar, Reminders, Notes, and Health reference. Uses Python MCP
      orchestration with locally compiled Swift sidecars and ships
      Codex/OpenClaw-style skills.
- [`heznpc/AirMCP`](https://github.com/heznpc/airmcp):
      broad Apple ecosystem reference. Useful for design ideas around module
      coverage, approvals, audit, and cross-app workflows, but too wide to adopt
      wholesale without a security review.

`jbkjr/macos-mcp` is worth reading only as a small sketch for Calendar,
Reminders, and Notes. It currently looks dormant enough that it should not be a
primary dependency.

## Open Questions

- Should the helper speak MCP directly, sky10 JSON-RPC, or both?
- Does sky10 host the helper under managed-app supervision or package it inside
      the macOS daemon/menu app?
- How should users review the first permission grant and the first mutating
      action for each Apple app?
- Should system plugins be installable per agent, per device, or only as
      owner-approved device capabilities?
- How much local object caching is acceptable before this becomes another sync
      surface?

## Candidate First Slice

1. Create a Darwin-only proof-of-concept helper for EventKit read/list/search
   across Calendar and Reminders.
2. Add create-only tools for reminders and calendar events.
3. Add a Notes JXA bridge for list/search/create, but keep note updates behind
   approval.
4. Wire the helper behind a sky10 capability wrapper rather than exposing the
   helper directly to agents.
5. Add tests for policy classification, destructive-tool absence, and
   unsupported non-macOS behavior.
