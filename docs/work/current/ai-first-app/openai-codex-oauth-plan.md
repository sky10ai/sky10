---
created: 2026-04-21
updated: 2026-04-21
model: gpt-5.4
---

# OpenAI Codex OAuth Plan

## Goal

Implement a host-owned ChatGPT/Codex OAuth lane in `sky10` that feels like
OpenClaw's `openai-codex/*` flow:

- click `Connect ChatGPT`
- complete browser sign-in
- store and refresh credentials locally on the device
- route Codex-backed requests through `sky10` without requiring a manual API key

This workstream is about the `openai-codex`-style path, not the native
`codex/*` app-server path.

## Why This Exists

`sky10` already has a thin ChatGPT/Codex link page, but the current
implementation shells out to the local `codex` CLI and delegates credential
storage to Codex.

That is a reasonable bootstrap, but it does not satisfy the longer-term
product goal:

- one-click consumer onboarding
- no visible dependency on the Codex CLI
- `sky10`-owned account state and reconnect UX
- a first-class `openai-codex` provider lane that agents can consume through
  `sky10`

## Current State

Today:

- [`web/src/pages/SettingsCodex.tsx`](../../../../web/src/pages/SettingsCodex.tsx)
  exposes `Connect ChatGPT`
- [`pkg/codex/service.go`](../../../../pkg/codex/service.go) now owns the
  browser OAuth state machine
- [`pkg/codex/oauth.go`](../../../../pkg/codex/oauth.go) builds the authorize
  URL, runs the localhost callback listener, and exchanges codes for tokens
- [`pkg/codex/store.go`](../../../../pkg/codex/store.go) stores and refreshes
  local ChatGPT/Codex credentials under the `sky10` root directory
- the daemon can still inspect and clear a legacy Codex CLI login as a fallback
  compatibility path

This plan replaces that implementation over time, but does not require
removing the CLI-backed path on day one.

## Current Repo Anchors

- [`pkg/codex/service.go`](../../../../pkg/codex/service.go)
- [`pkg/codex/oauth.go`](../../../../pkg/codex/oauth.go)
- [`pkg/codex/store.go`](../../../../pkg/codex/store.go)
- [`pkg/codex/rpc.go`](../../../../pkg/codex/rpc.go)
- [`commands/serve.go`](../../../../commands/serve.go)
- [`web/src/pages/SettingsCodex.tsx`](../../../../web/src/pages/SettingsCodex.tsx)
- [`web/src/pages/StartSetup.tsx`](../../../../web/src/pages/StartSetup.tsx)
- [`web/src/lib/rpc.ts`](../../../../web/src/lib/rpc.ts)
- [`web/src/lib/events.ts`](../../../../web/src/lib/events.ts)

## Product Requirements

- Keep the onboarding copy simple: `Connect ChatGPT`
- Make the first working experience local-device-first
- Do not require the user to install, understand, or manually run Codex CLI
- Preserve separate inference lanes:
  - ChatGPT/Codex OAuth
  - API key
  - wallet/x402 provider-backed inference
- Keep raw ChatGPT/Codex credentials device-local
- Do not sync refresh tokens through swarm KV, mailbox, or normal secrets flows
- Keep Windows viable from the start

## Protocol Assumptions

The implementation should mirror the publicly documented OpenClaw flow for
`openai-codex`:

1. generate PKCE verifier/challenge and a random `state`
2. open `https://auth.openai.com/oauth/authorize?...`
3. receive the redirect on a localhost callback
4. if callback capture fails, allow a manual paste fallback
5. exchange the code at `https://auth.openai.com/oauth/token`
6. persist `{ access, refresh, expires, accountId }`

OpenClaw documents this flow explicitly in its OAuth docs and OpenAI provider
docs.

Related references:

- OpenClaw OAuth docs: <https://docs.openclaw.ai/concepts/oauth>
- OpenClaw OpenAI provider docs: <https://docs.openclaw.ai/providers/openai>
- OpenAI Codex auth docs: <https://developers.openai.com/codex/auth>
- OpenAI Codex app-server docs: <https://developers.openai.com/codex/app-server>

## Important Constraint

OpenAI publicly documents:

- Codex app-server managed ChatGPT login
- host-supplied `chatgptAuthTokens`

OpenAI does not currently document a generic host-side SDK for minting those
tokens directly.

So this plan is based on a flow that OpenClaw documents and ships today, but it
should be treated as an explicit compatibility and maintenance risk rather than
a zero-risk platform guarantee.

## Architecture

### 1. Host-owned OAuth service

Add a new daemon package, likely `pkg/codexoauth`, responsible for:

- PKCE generation
- authorization URL assembly
- localhost callback capture
- code exchange
- refresh
- logout
- status inspection

This service should be entirely independent from the current `pkg/codex`
CLI-wrapper package.

### 2. Credential store

Add a dedicated local credential store for ChatGPT/Codex OAuth profiles.

Requirements:

- prefer OS credential storage when available
- use a file-backed fallback under `os.UserConfigDir()` for platforms without a
  usable keyring
- store metadata separately from sensitive token material when practical
- never depend on POSIX-only paths like `~/.codex`

Suggested split:

- keyring or encrypted secret blob for `access`, `refresh`, and `idToken` if
  present
- local metadata file for:
  - profile id
  - account id
  - auth mode
  - expires at
  - last refresh result
  - created/updated timestamps

### 3. Token sink and profile model

OpenClaw's docs are right about the main operational issue: refresh tokens are
often single-use or rotation-sensitive.

`sky10` should have one canonical token sink per device for this auth lane.

The first profile model should support:

- `default`
- future `personal` / `work` profile ids
- explicit source metadata, such as `host_oauth`

Even in V1, design the storage format so a second profile does not force a
migration.

### 4. Refresh manager

Refresh logic must be a first-class subsystem, not a helper function.

Requirements:

- always use a valid non-expired access token before attempting refresh
- serialize refreshes per profile with a mutex or single-flight guard
- write rotated refresh tokens atomically
- surface a clean re-auth-required state when refresh fails
- emit UI-visible auth-state events

This is the place where OpenClaw has already hit real bugs, so `sky10` should
optimize for correctness over cleverness.

### 5. Brokered execution surface

Agents should not receive raw ChatGPT/Codex OAuth tokens.

Instead, `sky10` should expose brokered capability surfaces such as:

- `codex.status`
- `codex.models`
- `codex.rateLimits`
- `codex.run`

The host-owned OAuth lane should feed those brokered calls.

The architecture line is:

- auth lives in device-local daemon state
- execution requests go through `sky10`
- raw credentials do not enter sandboxes, guest VMs, or third-party runtimes

### 6. Transport and provider routing

For the `openai-codex` lane, treat the transport as distinct from the normal
OpenAI API-key lane.

Working assumption based on OpenClaw's current docs:

- `openai/*` -> `api.openai.com`
- `openai-codex/*` -> `chatgpt.com/backend-api`

This should be modeled as a separate provider implementation and separate auth
mode, not as "just another bearer token for `/v1/*`".

## Implementation Phases

### Phase 1: Design And Data Model

Deliverables:

- `CodexOAuthProfile` and `CodexOAuthStatus` schemas
- local storage layout
- event vocabulary for auth updates
- feature flag for host-owned OAuth

Suggested touchpoints:

- new `pkg/codexoauth/*`
- [`web/src/lib/rpc.ts`](../../../../web/src/lib/rpc.ts)
- [`web/src/lib/events.ts`](../../../../web/src/lib/events.ts)

Exit criteria:

- data model reviewed
- storage paths are Windows-safe
- the current CLI-backed flow remains available as fallback

### Phase 2: Browser Login And Callback Capture

Deliverables:

- PKCE generator
- auth URL builder
- localhost callback server
- manual paste fallback for headless/bind-failure cases
- `loginStart`, `loginComplete`, `loginCancel`, `status` RPC methods

Details:

- use `127.0.0.1`, not implicit hostname assumptions
- support random available ports instead of hardcoding one port forever
- keep the callback lifetime short and bounded

Exit criteria:

- user can start the flow from `Connect ChatGPT`
- daemon can complete the token exchange without Codex CLI
- failed callback capture still offers a recovery path

### Phase 3: Secure Persistence And Status UX

Deliverables:

- device-local token persistence
- linked-account metadata UI
- reconnect and disconnect actions
- last-error and re-auth-needed states

UI changes:

- evolve [`web/src/pages/SettingsCodex.tsx`](../../../../web/src/pages/SettingsCodex.tsx)
  from "local Codex CLI status" to "ChatGPT/Codex account status"
- keep copy honest about whether auth is `cli_managed` or `host_oauth`

Exit criteria:

- linked state survives daemon restart
- reconnect/logout work cleanly
- users can tell whether they are using the legacy CLI path or host-owned OAuth

### Phase 4: Refresh And Rotation Hardening

Deliverables:

- proactive refresh window
- on-demand refresh on 401
- per-profile refresh lock
- atomic persistence of rotated credentials
- auth error telemetry and user-visible reconnect prompts

Exit criteria:

- valid access tokens are used without unnecessary refresh
- concurrent requests do not race refresh token rotation
- expired or invalid sessions degrade to a clear reconnect flow

### Phase 5: Provider Runtime

Deliverables:

- first `openai-codex` provider/client in `sky10`
- model enumeration for the linked account
- request signing from host-owned credentials
- clean separation from API-key-backed `openai/*`

This phase should stop short of broad agent exposure until basic request and
error behavior are proven.

Exit criteria:

- `sky10` can make at least one authenticated `openai-codex` request through
  the host-owned lane
- model and auth failures surface distinctly

### Phase 6: Brokered Agent Access

Deliverables:

- agent-safe `codex.run` style RPC or MCP tool
- approval and audit hooks
- per-agent policy gates
- rate-limit and budget visibility

Exit criteria:

- agents can use the linked account through `sky10`
- agents still do not receive raw credentials

### Phase 7: Multi-Profile And Routing

Deliverables:

- `personal` / `work` profiles
- explicit profile selection in settings and sessions
- default profile policy for root assistant and managed agents

Exit criteria:

- the storage model supports multiple linked accounts on one device
- routing stays deterministic and reviewable

## Rollout Plan

1. Ship host-owned OAuth behind an experimental flag.
2. Keep the current Codex CLI-backed flow as the default fallback.
3. Validate local login, restart persistence, refresh, and logout stability.
4. Enable brokered `openai-codex` requests for internal testing.
5. Only then make host-owned OAuth the primary `Connect ChatGPT` path.

This rollout order matters. Auth that "works once" is not good enough.

## Risks

- OpenAI may change undocumented auth details that OpenClaw currently relies on.
- Refresh token rotation bugs can silently strand accounts.
- Storing tokens in plain files would be an avoidable security regression.
- Syncing credentials through normal `sky10` secret replication would be a
  serious design mistake.
- Treating this auth lane as equivalent to `/v1/*` API-key auth would create
  wrong assumptions in the runtime layer.

## Test Plan

### Go unit tests

- PKCE generation
- callback validation and `state` checking
- token parsing and expiry calculation
- refresh path using valid-access-token-first semantics
- profile lock behavior under concurrent refresh attempts
- Windows-safe path handling

### Go integration tests

- localhost callback flow with fake auth server
- manual paste fallback flow
- refresh rotation persistence
- daemon restart with persisted credentials

### Web tests

- `Connect ChatGPT` happy path
- callback failure and manual recovery
- reconnect after refresh failure
- logout and linked-state clearing

### Manual validation

- macOS
- Linux
- Windows

Windows validation is not optional for this feature.

## Recommendation

Build this as a new host-owned OAuth lane, but keep the current CLI-backed lane
alive until the new flow proves stable across restart, refresh, and reconnect.

The failure mode to avoid is replacing a working but clunky implementation with
a sleek login button that silently rots after ten days.
