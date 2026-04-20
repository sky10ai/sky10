---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# AI-First App Architecture

## Core Decision

`sky10` should remain the source of truth for system capabilities.

The AI layer should orchestrate those capabilities. It should not become the
place where core product behavior lives.

## Layers

### 1. Canonical capability layer

Existing Go HTTP/JSON-RPC methods remain the canonical backend contract:

- `skyfs.*`
- `skylink.*`
- `identity.*`
- `agent.*`
- `sandbox.*`
- `system.*`
- `secrets.*`
- `apps.*`

These methods already represent most of the real work the system can perform.

### 2. Model/runtime layer

An AI runtime hosts the model loop and tool execution:

- sends prompt + tool schemas to the model
- receives tool calls
- executes tool handlers
- returns structured results to the model
- streams text and tool activity back to the UI

The AI SDK is a valid fit for this layer.

### 3. Tool wrapper layer

Model-facing tools should be thin wrappers over RPC methods or small
compositions of them.

Examples:

- `daemon.getVersion` -> `skyfs.health`
- `drives.list` -> `skyfs.driveList`
- `network.getStatus` -> `skylink.status`
- `devices.list` -> `identity.deviceList`
- `agents.list` -> `agent.list`

The point of this wrapper is not to duplicate backend logic. The point is to
present tool names and schemas that are clear for both users and models.

### 4. Product orchestration layer

The root assistant should add product-level behavior on top of tools:

- ask only for missing information
- produce a plan before destructive actions
- request approval when needed
- create durable agents from high-level intent
- surface jobs, artifacts, and failures coherently

This layer is where "create me an agent that..." becomes a real product
experience instead of a pile of raw tool calls.

## Why Not Expose Only Raw RPC Names

The backend surface is broad and useful, but raw RPC methods are not a good
final model-facing interface because:

- method naming is backend-oriented, not task-oriented
- some methods are too low-level or implementation-shaped
- approval policy should be attached to tools, not inferred from names
- a curated tool contract gives the model fewer ways to make bad decisions

The right posture is:

- broad internal capability coverage
- curated tool exposure per assistant/session/policy

## Tool Classes

### Read-only

Safe default inspection tools. No user confirmation required.

Examples:

- version and health lookup
- drive, device, sandbox, and agent listing
- network status inspection
- activity and mailbox status inspection

### Approval-required

Mutating actions that should present a reviewable plan first.

Examples:

- create/remove drive
- delete files
- create/delete sandbox
- restart system
- write or delete secrets
- connect devices or remove devices

### Admin/debug

Sharp tools that should not be casually placed in the root assistant's default
tool list.

Examples:

- raw mailbox queue mutation
- low-level S3 debug operations
- repair/retry and internal maintenance flows

## Assistant Types

### Root assistant

The top-level user-facing assistant embedded in the app.

Responsibilities:

- understand intent
- inspect current node state
- propose plans
- create durable managed agents
- route work to local or remote execution surfaces

### Durable managed agents

Provisioned agents with a persistent spec, runtime, workspace, and job history.

Responsibilities:

- perform repeated or domain-specific work
- operate on watched folders or queued tasks
- produce durable outputs and artifacts
- expose their own logs, policies, and failures

## Request Flow

For a simple request like "what version is the daemon?":

1. User asks in the AI workspace.
2. Model selects `daemon.getVersion`.
3. Tool handler calls `skyfs.health`.
4. Tool handler returns `{ version, status }`.
5. Model answers in natural language.

For a creation request like "create me an agent that transcribes audio and
video and redubs it with a British accent":

1. Root assistant parses the outcome.
2. It asks only the missing questions.
3. It drafts an `AgentSpec`.
4. User approves.
5. Provisioning tools create runtime, secrets, workspace, and registration.
6. The result becomes a durable agent with jobs and artifacts.

## Windows And Packaging Constraints

The repo has an explicit Windows readiness goal. That affects the AI layer.

Any chosen runtime must avoid quietly assuming:

- Unix-only process supervision
- shell-specific command behavior
- hardcoded POSIX paths
- symlink semantics that fail on Windows

If the first AI runtime uses a local Node/Bun service, that is acceptable for a
prototype, but the shipping plan needs an explicit Windows-ready packaging and
update story.
