---
created: 2026-04-14
updated: 2026-04-20
model: gpt-5.4
---

# Agents Drive Contract

This document is the Milestone 3 design draft for
[`fs-p2p-core-and-agent-drives-plan.md`](./fs-p2p-core-and-agent-drives-plan.md).
It defines the intended contract for the `Agents` drive and the first version
of `SOUL.md`, `MEMORY.md`, and `sky10.md`.

## Purpose

The `Agents` drive is the first concrete product built on top of the durable FS
engine:

- one private-network drive named `Agents`
- one portable folder per logical agent profile
- durable synced files that preserve agent identity, memory, and recreation
  instructions across machines

This is not the open-agent-economy layer. It is private-network state sharing
for your own agents and machines.

## Scope

This contract covers:

- how the `Agents` drive should be created and discovered
- the per-agent folder naming rule
- which files are seeded initially
- which files and fields are human-owned, runtime-owned, or daemon-owned
- how another machine should recreate an agent profile from folder contents

This contract does not yet cover:

- agent-to-stranger discovery
- public work drives
- permission and ACL UX
- billing, reputation, or network marketplace state

## Core Decision: Folder Represents A Profile, Not A Live Process

An `Agents/<folder>/` directory represents a **portable agent profile**, not a
single live daemon registration.

That distinction matters:

- a live runtime instance may come and go
- the same profile may be recreated on another machine
- multiple machines may temporarily host compatible runtimes derived from the
  same profile
- the synced folder should survive runtime restarts, migrations, or runtime
  family changes

This avoids overloading one folder with assumptions that only make sense for a
single always-running process.

## Drive Identity

The first version should standardize on:

- drive name: `Agents`
- default namespace: `agents`
- intended visibility: private network only

The drive should behave like any other synced drive. It should not use a
separate storage engine or special-case sync semantics.

## Creation And Discovery

The recommended product behavior is:

1. If the user creates their first local agent and no `Agents` drive exists,
   the daemon or UI should strongly suggest creating it.
2. The user may also create it manually through the normal drive flow.
3. Once created, it is discovered the same way as any other drive: through
   normal drive list and namespace discovery, not through the agent registry.

Recommendation:

- do not silently auto-create `Agents` on daemon startup
- do allow first-agent setup to provision it with clear user intent

That keeps the feature explicit while still making it easy to adopt.

## Folder Naming Rule

Use a stable folder key, not a mutable display name.

Recommended rule:

- folder name format: `A-<profile-id>`
- example: `Agents/A-q7m2k9x4/`

Reasons:

- stable across display-name edits
- avoids collisions from common names like `hermes` or `research`
- avoids rename churn during sync
- keeps Windows-safe path behavior simple

The friendly display name belongs inside `sky10.md`, not in the directory
name.

## Seeded Layout

A newly created profile folder should start as:

```text
Agents/
  A-q7m2k9x4/
    SOUL.md
    MEMORY.md
    sky10.md
    notes/
    attachments/
```

### Seeded Files

- `SOUL.md`
  Long-lived identity, purpose, style, and operating principles.
- `MEMORY.md`
  Durable memory worth carrying between runtimes and machines.
- `sky10.md`
  Recreation contract and machine-readable metadata.

### Seeded Directories

- `notes/`
  Human and agent notes that should stay with the profile.
- `attachments/`
  Files or references that help recreate context, prompts, or capability.

No additional hidden per-agent metadata files should be required in the
working tree for v1.

## Ownership Rules

The first version should use explicit ownership boundaries.

### Human-Owned By Default

- `SOUL.md`
- display name and descriptive notes inside `sky10.md`
- bootstrap instructions that express user intent
- notes in `notes/`

### Runtime-Owned By Default

- most of `MEMORY.md`
- runtime-generated migration notes
- model-use hints or ephemeral preferences recorded in `sky10.md`

### Daemon-Owned By Default

- stable profile ID if the daemon created the profile
- last-seen device/runtime observations in `sky10.md`
- normalization of machine-readable front matter fields

### Shared / Mixed Ownership

- `sky10.md`
  This file must tolerate edits from humans, runtimes, and the daemon without
  losing unknown fields.

Rule:

- daemon and runtime writers must preserve unknown front matter fields and
  preserve markdown body content they do not own

## `sky10.md` Format

The first version should be markdown with YAML front matter.

Why:

- readable and editable by humans
- structured enough for daemon and runtime parsing
- diff-friendly and sync-friendly

Recommended shape:

```md
---
schema: sky10-agent/v1
profile_id: A-q7m2k9x4
display_name: Hermes Coder
owner_device_id: D-a1b2c3d4
runtime:
  family: claude_code
  version: 1.0.0
  command:
    - claude
    - code
model:
  provider: anthropic
  name: claude-sonnet
  settings:
    reasoning: high
bootstrap:
  repo: sky10
  working_dir: ~/src/sky10
  prompt_refs:
    - SOUL.md
    - MEMORY.md
tools:
  required:
    - shell
    - git
  optional:
    - github
connectors:
  required: []
field_ownership:
  human:
    - display_name
    - bootstrap
  runtime:
    - model
    - runtime
  daemon:
    - owner_device_id
    - observed
observed:
  last_seen_device_id: D-a1b2c3d4
  last_seen_at: 2026-04-14T12:00:00Z
---

# Hermes Coder

## Recreation Notes

Human-readable notes for recreating or migrating this profile.
```

## Required `sky10.md` Fields

The first version should require:

- `schema`
- `profile_id`
- `display_name`
- `runtime.family`
- `model.provider`
- `model.name`
- `bootstrap`
- `field_ownership`

The daemon may add `owner_device_id` and `observed.*` when known, but those
should not be required to read the file.

## Optional `sky10.md` Fields

Useful but not required in v1:

- runtime version
- launch command
- repo or working-directory assumptions
- required tools
- required connectors
- migration notes
- source profile or imported-from provenance

## `SOUL.md` Contract

Purpose:

- capture enduring behavior that should not change just because the runtime,
  model, or machine changed

Suggested sections:

- name and identity
- mission or role
- operating principles
- boundaries and preferences
- tone or collaboration style

Rules:

- humans own the file by default
- runtime should not silently rewrite the whole file
- if the runtime wants to propose changes, it should append a note or create a
  patch-like suggestion instead of replacing intent

## `MEMORY.md` Contract

Purpose:

- capture useful portable memory that the runtime can actively maintain

Suggested sections:

- durable project context
- recurring tasks or preferences
- learned constraints
- open threads worth resuming later

Rules:

- runtime may update this file
- humans may edit, trim, or curate it
- the file should not be treated as append-only forever; curation is expected

## Profile-To-Runtime Mapping

The contract should distinguish:

- `profile_id`
  stable identity of the portable folder
- `runtime instance`
  a local running agent process registered with the daemon

Recommended mapping:

- one local runtime may declare that it is backed by `profile_id=A-q7m2k9x4`
- another machine may recreate a compatible runtime from the same profile
- the recreated runtime may keep its own live agent registration ID even if it
  came from the same profile

This prevents sync confusion from pretending that one live agent instance is
simultaneously running on many machines with exactly the same runtime identity.

## Recreation On Another Machine

A second machine should interpret the folder like this:

1. Read `sky10.md` and confirm the schema version is understood.
2. Create or select a compatible runtime family.
3. Apply the bootstrap instructions and required tools.
4. Import `SOUL.md` and `MEMORY.md` into the new runtime.
5. Record new local observations in daemon-owned fields without overwriting
   human-owned sections.

Recreation intent:

- preserve personality and useful memory
- preserve enough runtime setup to get close behavior
- do not require byte-identical process state

This is "recreate the agent with nearly the same identity and working state",
not "resume the exact same in-memory process image".

## Expected Example

```text
Agents/
  A-q7m2k9x4/
    SOUL.md        # human-owned identity and mission
    MEMORY.md      # portable working memory
    sky10.md       # machine-readable recreation contract
    notes/
      migration-2026-04-14.md
    attachments/
      prompt-snippets/
      screenshots/
```

If a user opens this folder, they should understand:

- what this agent is
- how it behaves
- what memory should travel with it
- how to recreate it on another machine

## Interaction With FS Semantics

The `Agents` drive should use the same FS engine rules as every other drive:

- watcher plus periodic scan
- hidden transfer workspace for remote materialization
- atomic publish into the working tree
- explicit conflict copies when concurrent edits cannot be ordered
- optional S3 durability without changing semantics

Agent personality files should not get an agent-specific sync exception path.

## Open Questions

- Should the daemon seed empty `notes/` and `attachments/`, or only create
  them on first use?
- Should `profile_id` always equal the directory name, or do we need room for
  imported aliases later?
- Which `sky10.md` fields should a runtime be allowed to update without an
  approval flow?
- Should the first UI expose profile folders directly in `Agents`, in
  `Drives/Agents`, or both?

## Acceptance For This Draft

This design draft is useful when:

- folder naming no longer depends on mutable display names
- `sky10.md` is specific enough to recreate a profile on another machine
- field ownership is explicit enough to avoid daemon/runtime/human clobbering
- the `Agents` drive remains an ordinary synced drive rather than a special
  storage subsystem
