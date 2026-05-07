---
created: 2026-04-18
updated: 2026-04-26
model: gpt-5.5
---

# AI-First User Flow

## Primary UX Change

The first screen should be Home, an AI workspace, instead of sending users to
drives or device onboarding.

Current shape:

- storage/network app
- `Agents` is a section in the sidebar
- `AgentChat` is a separate page with plain message bubbles

Target shape:

- Home is the center of the product
- chat is only one part of a richer "run" surface
- drives, devices, network, settings, and sandboxes support the AI workflow

## Home Screen

The home route should contain:

- a large intent composer
- recent runs
- recent agents
- approval queue
- quick context chips for drives, devices, sandboxes, and files

Example prompt:

`make me an ai agent that can process media files to change the accent to british`

## Root Agent Interaction

The root agent should not drop users into a blank wizard. It should:

1. infer the likely task decomposition
2. explain the draft plan briefly
3. ask only for missing information
4. show the resulting agent or run contract before execution

For the media example, likely missing information:

- local model or API-backed provider
- output location
- whether transcript and subtitle artifacts should also be saved
- generic British voice or cloned voice
- one-off run, batch queue, or watched folder

## Run View

The current `AgentChat` page should evolve into a run-oriented surface with:

- conversation transcript
- model tool calls
- live tool status
- approval prompts
- generated artifacts
- logs
- retry/cancel actions

This matters because the product should feel like "AI is doing work" rather
than "AI is producing text."

## Durable Agent Creation Flow

The main creation flow should be:

1. User describes the outcome.
2. Root agent drafts an `AgentSpec`.
3. User reviews the plan.
4. User approves provisioning.
5. `sky10` creates runtime, tools, secrets, workspace, and registration.
6. The new agent appears as a durable object with its own page.

The agent page should show:

- purpose and contract
- runtime location
- allowed tools and permissions
- input/output folders
- secrets and provider dependencies
- job history
- artifacts
- health and logs

## Supporting Surface Changes

### Sidebar

Shift the sidebar hierarchy:

- Home
- Agents
- Drives
- Devices
- Settings

Network, mailbox, activity, sandboxes, apps, and secrets can remain reachable
through settings or contextual drill-downs unless product priorities shift.

### Contextual entry points

Every major object view should gain "ask sky10 about this" entry points.

Examples:

- drive page: summarize sync issues for this drive
- file browser: explain why this file is conflicted
- network page: diagnose why this node looks degraded
- sandbox page: create an agent from this sandbox template

## What "AI First" Should Not Mean

It should not mean:

- one giant generic chat surface with no structure
- hiding system state and outputs behind prose
- reducing every action to free-form prompt text

The product should instead combine:

- natural-language intent entry
- explicit plans
- concrete tool activity
- durable objects
- inspectable state
