---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# AI-First Implementation Plan

## Phase 1: AI Workspace Shell

Goal: make the product feel AI-first before changing deep backend behavior.

Deliverables:

- add a new AI workspace route and make it the default home
- update the sidebar hierarchy
- add a large intent composer
- add placeholder sections for runs, approvals, and recent agents
- keep existing drives/devices/settings pages intact

Likely touchpoints:

- `web/src/App.tsx`
- `web/src/components/Sidebar.tsx`
- new `web/src/pages/AIWorkspace.tsx`
- new `web/src/components/assistant/*`

Success criteria:

- users land in an AI workspace first
- the UI clearly promises outcome-driven interaction
- existing infrastructure views remain accessible

## Phase 2: Root Assistant MVP

Goal: ship a read-only assistant that can inspect and explain the current node.

Deliverables:

- assistant session model
- streaming response support
- read-only tool set backed by current RPC calls
- tool traces in the run view

Initial tool candidates:

- `daemon.getVersion`
- `daemon.getHealth`
- `drives.list`
- `devices.list`
- `network.getStatus`
- `agents.list`
- `sandboxes.list`
- `activity.list`

Success criteria:

- user can ask "what version is my daemon?"
- user can ask "what looks wrong with this node?"
- answers are grounded in real RPC-backed inspection tools

## Phase 3: Approval Framework

Goal: make write-capable actions safe and inspectable.

Deliverables:

- approval object model
- review cards for pending actions
- audit trail for mutating tool calls
- policy labels on tools

Initial approval-gated tool candidates:

- create/remove drive
- remove file/folder
- create/start/stop/delete sandbox
- restart system
- write/delete secret
- remove device

Success criteria:

- destructive actions are never opaque
- the model can propose changes without silently applying them

## Phase 4: AgentSpec And Provisioning

Goal: let the root assistant turn natural-language requests into durable managed
agents.

Deliverables:

- `AgentSpec` schema
- draft/review/approve flow
- runtime selection logic
- workspace and storage setup
- registration into the existing agent system

`AgentSpec` should include:

- name
- purpose
- runtime target
- tools and permissions
- secrets and providers
- input/output locations
- triggers
- output artifact expectations

Success criteria:

- user can say "create me an agent that..."
- system produces a reviewable spec
- approved spec becomes a real managed agent

## Phase 5: Jobs And Artifacts

Goal: shift from chat-history product thinking to durable work product.

Deliverables:

- job records
- run records
- artifacts list
- retry and replay actions
- status transitions such as queued/running/blocked/done/failed

Success criteria:

- completed work is easier to inspect than the chat that produced it
- users can reason about repeated automation, not only ad hoc conversations

## Phase 6: Vertical Slice

Goal: validate the whole stack with one compelling example.

Candidate:

- media transcription + subtitle + British-accent dubbing agent

Flow:

1. user describes the goal in one sentence
2. root assistant asks for missing inputs
3. user approves the spec
4. system provisions runtime and tooling
5. agent processes a sample media file
6. outputs appear as transcript, subtitles, and dubbed media artifact

Success criteria:

- the flow feels like "sentence to working automation"
- the user does not need to manually assemble sandboxes, secrets, and
  registration steps

## Open Questions

- what is the first shipping runtime for the model/tool loop?
- which provider secrets become first-class in the UI?
- where should durable agent/job specs live?
- how much of the raw RPC surface should be model-addressable on day one?

## Recommended First Slice

Build only:

- AI workspace shell
- root assistant MVP
- read-only RPC-backed tools
- tool trace UI

Do not start with:

- full provisioning
- broad mutating tool exposure
- automatic recurring agents

That first slice is enough to prove the architecture without overcommitting to
packaging and policy details.
