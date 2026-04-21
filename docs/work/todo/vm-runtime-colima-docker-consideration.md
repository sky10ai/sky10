---
created: 2026-04-20
updated: 2026-04-20
---

# VM Runtime Colima Docker Consideration

Track whether sandbox and agent-bootstrap flows should move away from
direct Lima orchestration toward a more Docker-oriented runtime story,
such as Colima for local macOS development and Docker-compatible remote
VM targets.

## Why This Is Worth Evaluating

Current sandbox flows are explicitly Lima-native:

- provider and template wiring assumes `lima`
- lifecycle commands shell out to `limactl`
- guest-to-host networking and shell flows assume Lima conventions
- OpenClaw and Hermes setup is expressed as Lima templates and guest
  bootstrap scripts

That works for the current macOS VM path, but it may not be the
simplest long-term shape if the product wants:

- easier composition with standard container tooling
- simpler local setup for developers already using Docker workflows
- cleaner promotion from local dev to remote Linux VM deploys
- a runtime story that is less tied to one VM tool's CLI and directory
  layout

## What "Move To Colima Or Docker" Should Mean

This should not be treated as a blind tool swap.

Colima is primarily a Docker-first wrapper around Lima, and Docker is a
container workflow rather than a direct replacement for Lima's current
full-VM template flow.

The real question is whether sky10 should move from:

- direct Lima guest management

to something closer to:

- containerized bootstrap and lifecycle management
- Docker Compose-friendly service layout
- optional local VM wrappers like Colima
- Docker Engine-compatible remote targets for single-VM deploys

## Questions To Answer

- [ ] Which parts of the current OpenClaw and Hermes flows truly need a
      whole VM instead of a container or compose stack?
- [ ] Would Colima materially simplify the local developer experience,
      or just rename the underlying Lima dependency?
- [ ] Can the current guest-local `sky10` daemon, host RPC bridge,
      shared workspace, browser tooling, and terminal UX be expressed
      cleanly in Docker/Compose form?
- [ ] What isolation properties would be lost or changed if we move
      from whole-VM agents to containers inside a local VM?
- [ ] How would persistent agent homes and sandbox-local state migrate
      from the current Lima layout?
- [ ] Would a Docker-oriented runtime improve the Windows story by
      converging local and remote bootstrap around standard Linux Docker
      hosts?

## Suggested Approach If This Becomes Active Work

- [ ] First document the exact Lima-specific assumptions in
      `pkg/sandbox`, `commands/agent_lima.go`, and the web sandbox UI
- [ ] Define a provider/runtime abstraction before changing the current
      templates
- [ ] Prototype one sandbox path as Docker/Compose-based without
      removing the existing Lima flow
- [ ] Compare setup time, reproducibility, host integration, terminal
      UX, and security/isolation tradeoffs
- [ ] Decide whether the target is:
      - keep Lima as-is
      - add Colima as a local Docker-first option
      - move agent runtimes toward Docker/Compose and treat Lima as an
        implementation detail or legacy path

## Current Recommendation

Do not replace Lima just to change tool names.

If this work happens, the goal should be a cleaner runtime model for
agent bootstrapping and deployability, not a superficial migration from
`limactl` to another wrapper. The main value would come from better
composition and more standard deploy surfaces, not from Colima alone.
