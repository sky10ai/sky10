---
created: 2026-04-18
updated: 2026-04-25
model: gpt-5.4
---

# AI-First Milestones And Checklists

## Planning Rule

These milestones are ordered to prove the architecture in the smallest useful
increments:

1. make the app visibly AI-first
2. make the root assistant genuinely useful with read-only tools
3. add safe RPC write paths and approvals
4. turn natural-language requests into durable agent specs
5. add jobs and artifacts so work persists beyond chat
6. prove the whole flow with a vertical slice

Each milestone should land at a sensible checkpoint with a working UI and a
clean story for what users can now do.

## Milestone 1: Home Workspace Shell

### Outcome

Users should land on Home, an AI workspace, instead of dropping into drives or
device onboarding.

### Checklist

- [ ] Add a new Home page under `web/src/pages/Home.tsx`.
- [ ] Change the home redirect in `web/src/App.tsx` to route to the AI
      workspace by default at `/home`.
- [ ] Update the sidebar hierarchy in `web/src/components/Sidebar.tsx` so the
      Home workspace is primary.
- [ ] Add a large intent composer that invites outcome-driven prompts.
- [ ] Add sections for recent runs, approvals, and recent agents.
- [ ] Preserve access to existing drives, devices, and settings flows.
- [ ] Keep the page usable on desktop and mobile layouts.

### Exit Criteria

- [ ] The first screen clearly presents `sky10` as an AI-first product.
- [ ] A user can see where to ask for work, where to review work, and where to
      find their agents.
- [ ] Existing non-AI surfaces remain reachable without regression.

## Milestone 2: Root Assistant MVP

### Outcome

The root assistant can inspect and explain the current node using the existing
RPC surface through a model/tool runtime.

### Checklist

- [ ] Add an assistant session model for the root assistant.
- [ ] Add streaming response support for assistant runs.
- [ ] Add tool trace UI so users can see what the assistant inspected.
- [ ] Implement read-only model-facing tools backed by current RPC methods.
- [ ] Add at least these first tools:
      `daemon.getVersion`, `daemon.getHealth`, `drives.list`,
      `devices.list`, `network.getStatus`, `agents.list`, `sandboxes.list`.
- [ ] Keep tool handlers thin and map them cleanly to existing RPC methods.
- [ ] Add basic failure handling for unavailable RPC responses or empty state.

### Exit Criteria

- [ ] A user can ask "what version is my daemon?" and get a correct answer.
- [ ] A user can ask "what looks wrong with this node?" and get a grounded
      response based on real inspection tools.
- [ ] The run view shows both assistant text and tool activity.

## Milestone 3: Approval Framework And Safe Writes

### Outcome

The assistant can propose mutating actions without silently applying them.

### Checklist

- [ ] Define tool policy classes such as `read_only`,
      `approval_required`, and `admin_debug`.
- [ ] Add an approval object model for pending assistant actions.
- [ ] Add review cards in the UI for approval-gated tool calls.
- [ ] Add audit logging for mutating tool executions.
- [ ] Add first approval-gated tools for file removal, drive creation/removal,
      sandbox lifecycle actions, secret writes, and system restart.
- [ ] Extend approval-gated coverage toward ordinary RPC-backed configuration
      workflows: devices, managed apps, wallet setup, and updates.
- [ ] Keep debug, internal repair, raw mailbox queue mutation, unsupported
      platform actions, and destructive Apple-system verbs out of the default
      assistant profile.
- [ ] Require explicit user confirmation before any destructive or costly
      action executes.
- [ ] Handle approval rejection cleanly in the run state and transcript.

### Exit Criteria

- [ ] No destructive assistant action executes without a visible approval step.
- [ ] Users can understand exactly what the assistant wants to change.
- [ ] The system records what was proposed, approved, rejected, or applied.

## Milestone 4: AgentSpec Drafting And Provisioning

### Outcome

The root assistant can turn "create me an agent that..." into a reviewable and
provisionable durable agent spec.

### Checklist

- [ ] Define an `AgentSpec` schema covering name, purpose, runtime, tools,
      permissions, secrets, inputs, outputs, and triggers.
- [ ] Add a draft/review/approve UI for agent creation.
- [ ] Teach the root assistant to ask only for missing specification details.
- [ ] Add runtime selection logic for local device, sandbox, or remote target
      where appropriate.
- [ ] Add provisioning steps for workspace creation, secret binding, and agent
      registration.
- [ ] Reuse the existing agent registration surface instead of inventing a new
      parallel system.
- [ ] Persist the resulting managed agent metadata in a form the UI can browse.

### Exit Criteria

- [ ] A user can describe a desired agent in plain language.
- [ ] The system produces a concrete, reviewable spec rather than a vague chat
      response.
- [ ] Approving the spec results in a real managed agent in `sky10`.

## Milestone 5: Jobs, Runs, And Artifacts

### Outcome

The product stops thinking in chat-history terms and starts thinking in durable
work products.

### Checklist

- [ ] Define job and run record types.
- [ ] Track run states such as queued, running, blocked, done, and failed.
- [ ] Persist artifacts generated by runs.
- [ ] Add retry, replay, and inspect actions for prior runs.
- [ ] Add agent pages that show current contract, recent jobs, outputs, and
      failures.
- [ ] Make artifact access easier than scrolling chat history.
- [ ] Keep job records compatible with multi-device and remote-agent flows.

### Exit Criteria

- [ ] Users can inspect completed work without reconstructing it from chat.
- [ ] Agents feel durable and operational, not like disposable conversations.
- [ ] A prior run can be retried or audited without ambiguity.

## Milestone 6: Media Dubbing Vertical Slice

### Outcome

One compelling end-to-end example proves the AI-first architecture with a real
user goal.

### Checklist

- [ ] Use the prompt:
      `make me an ai agent that can process media files to change the accent to british`
- [ ] Ensure the root assistant decomposes the request into media ingestion,
      voice/accent conversion, ffmpeg rendering, optional transcription, and
      output artifacts rather than getting stuck on the wording.
- [ ] Ask only for the missing details: provider choice, output location,
      subtitle requirement, voice choice, and trigger mode.
- [ ] Draft a concrete `AgentSpec` for the media agent.
- [ ] Provision the required runtime and tool stack.
- [ ] Register the managed agent in `sky10`.
- [ ] Run a sample file through the new agent.
- [ ] Produce visible transcript, subtitles, and dubbed media artifacts.
- [ ] Surface job logs, failures, and approvals in the run view.

### Exit Criteria

- [ ] The experience feels like sentence -> spec -> approval -> provisioned
      agent -> completed artifacts.
- [ ] The user does not need to manually stitch together sandbox, secret, and
      registration steps.
- [ ] The demo is strong enough to validate the AI-first direction.

## Recommended Execution Order

### First checkpoint

- [ ] Complete Milestone 1.
- [ ] Complete the read-only subset of Milestone 2.

This is the minimum useful proof that the app can become AI-first without yet
taking on provisioning or policy complexity.

### Second checkpoint

- [ ] Complete Milestone 3.
- [ ] Start Milestone 4 with spec drafting before full provisioning.

This is the point where the assistant becomes trusted for real work instead of
just inspection.

### Final checkpoint

- [ ] Complete Milestones 4 through 6.

This is the point where `sky10` genuinely supports sentence-to-agent creation
instead of just AI-assisted navigation.
