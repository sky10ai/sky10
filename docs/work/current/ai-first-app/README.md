---
created: 2026-04-18
updated: 2026-04-26
model: gpt-5.5
---

# AI-First App Plan

## Goal

Make `sky10` feel AI-first without moving the product's source of truth out of
the Go daemon and existing HTTP/JSON-RPC capability layer.

The desired user experience is:

- land on Home as an AI workspace, not a storage or infra dashboard
- describe an outcome in plain language
- watch the system turn that request into a plan, tool calls, approvals,
  artifacts, and optionally a durable agent
- continue using drives, devices, network, settings, and sandboxes as
  supporting surfaces behind that workflow

## Scope

This workstream covers:

- AI-first information architecture and home screen
- root-agent runtime shape
- tool calling over the existing RPC surface
- approval and policy boundaries
- durable agent creation from natural-language intent
- an end-to-end proof case for an agent prompted as "make me an ai agent that
  can process media files to change the accent to british"

This workstream does not yet commit to:

- a permanent packaged JS runtime choice for desktop shipping
- a final provider/model matrix
- a final storage format for all future agent/job records

## Documents

- [Architecture](./architecture.md)
- [RPC Tool Policy](./rpc-tool-policy.md)
- [User Flow](./user-flow.md)
- [Implementation Plan](./implementation-plan.md)
- [Prompt To Agent And Optional Commerce Plan](./prompt-to-paid-agent-plan.md)
- [Milestones And Checklists](./milestones.md)
- [Media Dubbing Agent Example](./media-dubbing-agent.md)

## Related Shipped Work

- [ChatGPT Codex Integration](../../past/2026/04/22-ChatGPT-Codex-Integration.md)

## Current Repo Anchors

Relevant existing surfaces:

- [web/src/App.tsx](../../../../web/src/App.tsx)
- [web/src/components/Sidebar.tsx](../../../../web/src/components/Sidebar.tsx)
- [web/src/pages/Agents.tsx](../../../../web/src/pages/Agents.tsx)
- [web/src/pages/AgentChat.tsx](../../../../web/src/pages/AgentChat.tsx)
- [web/src/lib/rpc.ts](../../../../web/src/lib/rpc.ts)
- [docs/work/current/agent-support-plan.md](../agent-support-plan.md)

## Working Position

The current app is "agents as a section inside a storage/network app."

The target app is "AI workspace first, infrastructure second."

That does not require replacing the daemon RPC layer. It requires putting a
model/tool runtime and a better user-facing planning/provisioning layer on top
of it.
