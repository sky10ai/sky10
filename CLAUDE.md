---
updated: 2026-04-26
---

# CLAUDE.md

Read `AGENTS.md` first. It is the canonical repo guide for this
repository. This file only adds Claude-specific behavior and points to
Claude skills.

## Claude Defaults

- Keep responses concise and actionable.
- Start by reading the relevant files before editing.
- Answer direct questions directly. Only write code when the user asked
  for implementation, a fix, or a concrete change.
- Prefer minimal, targeted diffs over broad refactors.
- Do not improvise release or remote-debug workflows when a skill
  already exists for them.

## Claude Skills

- `/release <version>` — canonical release flow. Use it for any request
  to cut, ship, or publish a release. It now requires:
  - build web before release artifacts
  - `make platforms` / `make checksums`
  - release binaries for all 6 CLI targets, including:
    - `sky10-windows-amd64.exe`
    - `sky10-windows-arm64.exe`
  - tag/release publication and checksum verification
  - menu-asset verification and dogfooding.
- `/land` — canonical branch-landing flow. Use it only when the user
  explicitly says `land this branch` or otherwise clearly asks to
  integrate and clean up the current task branch.
- `/debug-remote` — canonical remote-debug workflow over
  `/tmp/sky10/sky10.sock`. Use it when diagnosing another machine from
  debug dumps or S3 state.

## Repo Notes

- General repo policy, validation expectations, documentation layout,
  and Go conventions live in `AGENTS.md`.
- Git and GitHub behavior, including the default commit/push policy,
  lives in the `Git And GitHub` section of `AGENTS.md`.
- If a Claude-specific note here conflicts with `AGENTS.md`, follow
  `AGENTS.md` for repo policy and this file only for Claude-specific
  mechanics.
