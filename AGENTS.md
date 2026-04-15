---
updated: 2026-04-07
---

# AGENTS.md

This file is the canonical repo guide for coding agents working in
`sky10`. `CLAUDE.md` is a thin Claude-specific overlay; other agents
should use this file directly.

## Repo Snapshot

- `sky10` is a Go CLI and daemon for encrypted storage, sync, and agent
  coordination.
- `commands/` contains Cobra command wiring.
- `pkg/` contains domain packages. Extend existing areas before adding
  new top-level ones.
- `web/` is the React/Vite UI embedded into the Go binary.
- `menu/` is the Tauri tray/menu app built and attached by CI on
  release tags.
- `docs/guides/` is human-facing documentation.
- `docs/work/` and `docs/learned/` are agent-only documentation.

## Working Agreement

- Start by reading the relevant files and follow existing patterns.
- If requirements, expected behavior, or constraints are ambiguous, ask
  clarifying questions before writing code. Clarity first usually leads
  to better code and fewer retries.
- Prefer minimal, targeted changes over opportunistic cleanup.
- Answer direct questions directly. Only edit code when the user asked
  for a change or the intent is clearly implementation.
- Do not revert unrelated work or use destructive commands unless the
  user explicitly asks.
- Call out any commands or validation steps you could not run.

## Windows Readiness

- Windows compatibility is a standing product goal. Prefer designs that
  keep the CLI, daemon, updater, and agent flows viable on Windows, not
  just macOS and Linux.
- Treat "a Windows user can download sky10 and install agents with a
  simple, mostly one-click flow" as a guiding product constraint when
  touching install, update, release, UI, or agent-bootstrap work.
- Do not introduce new Unix-only assumptions unless they are explicitly
  isolated behind platform-specific files, build tags, or well-documented
  fallbacks. Common traps include Unix sockets, signals, shell scripts,
  `/tmp`, symlink behavior, permissions, and service management.
- Prefer cross-platform APIs and paths by default. Use `filepath`,
  `os.UserConfigDir`, `os.UserCacheDir`, `os.TempDir`, and other
  portable standard-library behavior instead of hardcoded POSIX paths or
  shell-only workflows.
- When you find a Windows-specific gap or blocker, either fix it in the
  current change or record it in `docs/work/todo/windows-support.md` so
  the repo keeps converging on Windows readiness instead of regressing.

## Git And GitHub

- Commit and push after every completed task by default. Do not let work
  pile up in the local tree.
- Never commit without pushing, unless the user explicitly asks you not
  to push yet.
- If the task is still in progress, keep working until it reaches a
  sensible completed checkpoint before committing.
- If requirements or acceptance criteria are unclear, ask clarifying
  questions before coding so you do not aggressively commit the wrong
  thing.
- Use Conventional Commits: `<type>(<scope>): <description>`.
- Prefer a linear history: short-lived branches rebased onto
  `origin/main` and landed onto `main` by fast-forward only.
- The user manages worktrees. Agents must work in the current worktree
  and must not create or remove worktrees unless explicitly asked.
- Treat the current branch as a short-lived task branch for the current
  worktree.
- Keep the branch current by rebasing onto `origin/main`. Do not merge
  `main` or `origin/main` into the branch.
- Integrate completed work back to `main` with fast-forward-only
  semantics. Do not create merge commits.
- Branch landing requires an explicit user instruction such as
  `land this branch`. That authorizes fetch, rebase onto `origin/main`,
  final validation, fast-forward-only integration onto `main`, push, and
  remote branch cleanup.
- Because worktrees are user-managed, do not remove the current
  worktree by default when landing a branch. Only do worktree cleanup if
  the user explicitly asks.
- Do not tag or cut a release unless the user explicitly asks.
- After the branch lands on `main`, delete the remote branch
  immediately. Delete the local branch too when it is no longer the
  checked-out branch of an active worktree, unless the user wants to
  keep it around.

## Validation

- Before implementing non-trivial work, sanity-check that the requested
  behavior is actually clear. If success criteria are fuzzy, pause and
  ask clarifying questions instead of guessing.
- Write great tests, but be thoughtful about them. Optimize for
  confidence and signal, not raw test count.
- Test observable behavior, failure modes, state transitions,
  concurrency edges, and external boundaries. Avoid brittle tests that
  only mirror private implementation details.
- Prefer fast, deterministic tests that fail clearly. If a test is
  unusually painful to write or maintain, treat that as design feedback
  and choose the highest-value test shape.
- Bug fixes follow: regression test first, confirm the failure when
  practical, apply the fix, then rerun the relevant tests.
- Run `gofmt -w` on changed Go files before handing off Go changes.
- Preferred validation for most Go work:
  - `make check`
  - `go test ./... -count=1`
- Web changes should also get `make build-web` or the relevant frontend
  check.
- If a test depends on external services, keep it skippable behind env
  checks or build tags and say when it was skipped.

## Release Policy

- Never mutate a published release. If a release is wrong, cut a new
  patch release.
- Every release must include an empty release commit on the release
  target commit before tagging, using the shape:
  `release: v<version>`. Treat this as part of the standard release
  record, not an optional convenience.
- Build order for CLI release assets is tag, push tag, build web, build
  release binaries, create the GitHub release.
- Build the web frontend before release binaries. The Go binary embeds
  `web/dist/`.
- Release titles and notes should follow the `v0.47.0` pattern by
  default unless the user explicitly wants a different style:
  - title: `v<version> — <short summary>`
  - body: one short lead paragraph summarizing the release in prose
  - then a blank line
  - then `Commits since \`v<previous>\`:` as a literal section label
  - then flat commit bullets in the form
    ``- [`abc1234`](https://github.com/sky10ai/sky10/commit/abc1234) subject``
  - avoid ad hoc headings like `## What's Changed` unless the user asks
    for them
- `menu/` assets are built by GitHub Actions from the release tag. Do
  not hand-build or re-upload them unless the user explicitly asks.
- If your agent can use Claude skills, use `/release <version>`.
  Otherwise follow `.claude/skills/release/SKILL.md` directly.

## Explicit Signals

- `land this branch` means the coding work is done and the agent may use
  the repo's branch-landing workflow.
- `land this branch and clean up this worktree too` is a stronger
  instruction. Only treat it as permission for worktree cleanup if the
  requested cleanup is actually possible from the current checkout.

## Documentation

- `docs/guides/` is for users and contributors.
- `docs/work/current/` holds active plans and in-progress work.
- `docs/work/past/{year}/{month}/` holds completed work logs and
  indexes.
- `docs/learned/` is agent memory: gotchas, tradeoffs, and decisions
  that help future agents avoid repeating mistakes.
- In repo docs, never link to worktree-local absolute filesystem paths
  such as `/Users/...` or other machine-specific locations. Use
  repo-relative paths and markdown links that stay valid across clones
  and worktrees. Runtime paths like `/tmp/sky10/...` are fine only when
  they describe actual product behavior.
- When moving tracked work from current to past, update the relevant
  README indexes.

## Go Conventions

- This repo already uses a top-level `pkg/`; keep new code aligned with
  the existing layout instead of introducing another module structure.
- Keep packages domain-oriented. Avoid catch-all `utils` or `helpers`
  packages.
- Keep interfaces small and define them where they are consumed.
- Put `context.Context` first when a function needs one.
- Wrap returned errors with context using `%w`; do not both log and
  return the same error.
- Prefer synchronous code unless concurrency materially helps.
- Be careful with `sync.RWMutex`: it is not reentrant. Do not call
  helpers that take `RLock()` while holding the same mutex's write lock.
- Avoid `time.Sleep` for synchronization in production code.
- Prefer the standard library and table-driven tests unless the repo
  already has a better-established pattern for that area.
- Split files before they become hard to review; roughly 500 lines is a
  warning sign, not a target.

## Logging Conventions

- Use `pkg/logging` for logger construction and default installation. Do
  not create ad hoc `slog.New*Handler`, `slog.New`, or `slog.SetDefault`
  calls outside that package unless the user explicitly asks for a
  logging-system change.
- The daemon default is structured `logfmt`. JSON logs are opt-in via
  the central logging config. Do not let individual packages choose
  their own format.
- Every package-level logger should carry a `component` field such as
  `fs`, `kv`, `link`, `agent`, `rpc`, or `update`. Use `component`, not
  `namespace`, for logger identity.
- Attach stable structured fields for the thing being acted on:
  `path`, `drive`, `device`, `peer_device`, `storage_scope`, `key`,
  `method`, `socket`, and similar. Prefer explicit domain names over
  vague fields.
- If a constructor accepts `*slog.Logger`, thread it through and tag the
  package/component once near the package entry point. Avoid inventing
  custom logger wrapper types just to rename methods.
- `Debug` is for high-volume diagnostics and retry/detail noise that is
  usually off in normal operation.
- `Info` is for expected lifecycle and state transitions that help trace
  normal behavior without flooding the logs.
- `Warn` is for recoverable failures, retries, fallbacks, partial
  degradation, or data that looks wrong but does not stop the current
  operation.
- `Error` is for failures that abort the current operation, leave the
  daemon in a degraded state, or require operator attention.
- Do not both log and return the same error unless the log is at a true
  process boundary where the error would otherwise disappear. Prefer
  returning wrapped errors and logging once at the boundary.

## Remote Debugging

- Use the daemon's Unix socket at `/tmp/sky10/sky10.sock` when
  diagnosing another machine from this repo's tooling.
- Useful RPC methods: `skyfs.deviceList`, `skyfs.debugList`,
  `skyfs.debugGet`, `skyfs.debugDump`, `skyfs.s3List`,
  `skyfs.s3Delete`.
- `skyfs.debug*` and `skyfs.s3*` require S3-backed storage on the
  target daemon.
- Prefer a fresh debug dump over stale ones.
- If your agent can use Claude skills, `/debug-remote` captures the
  preferred dump-based workflow.
