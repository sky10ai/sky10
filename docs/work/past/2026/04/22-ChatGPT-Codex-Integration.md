---
created: 2026-04-22
model: gpt-5.4
---

# ChatGPT Codex Integration

This entry covers the ChatGPT/Codex integration work that landed in
`a8e3b5ea` (`feat(codex): add chatgpt login flow`), `767ae0bb`
(`docs(ai-first): add openai-codex oauth plan`), `b0da23dc`
(`feat(codex): add host-owned chatgpt oauth`), `2050a2d0`
(`feat(codex): add brokered codex chat`), `85062a82`
(`fix(codex): avoid blank chatgpt reconnect popup`), `b80eed7d`
(`fix(codex): restore host oauth chat flow`), `5ac86183`
(`feat(codex): add new-tab oauth comparison flow`), `3620829e`
(`refactor(codex): remove cli auth fallback`), and `2857e200`
(`fix(web): remove codex sidebar nav item`).

The goal was to make `sky10` feel like it can connect directly to a user's
ChatGPT Codex account instead of pushing people toward API-key setup or a
visible Codex CLI dependency. The end result is a local-device-first ChatGPT
OAuth flow in `sky10`, a minimal `/codex` chat surface, and a clearer product
boundary around how ChatGPT-backed Codex access is supposed to work in the app.

## Why

Before this work, the setup UI had a `Connect ChatGPT` affordance, but it was
not honest about what the product was doing. The earlier path either routed
back into generic secret entry or depended on the local Codex CLI's auth state.

That was not good enough for the product direction we wanted:

- one-click-ish consumer onboarding
- no manual API key requirement for ChatGPT/Codex users
- no user-visible Codex CLI setup
- a daemon-owned account state that `sky10` can inspect and use directly

The target shape was closer to OpenClaw's `openai-codex/*` flow than to the
native Codex CLI/app-server path: browser OAuth, local credential storage,
direct calls to the ChatGPT Codex transport, and a simple chat surface for live
testing.

## Product Constraints

The original plan for this workstream was opinionated about a few constraints,
and those constraints still matter when reading the shipped result:

- the onboarding copy should stay simple: `Connect ChatGPT`
- the first working experience should be local-device-first
- users should not need to install, understand, or manually run Codex CLI
- ChatGPT/Codex OAuth should remain a distinct inference lane from API-key
  OpenAI access and wallet/x402-backed inference
- raw ChatGPT/Codex credentials should stay device-local and out of normal
  `sky10` secret replication
- Windows viability should be treated as a product requirement, not an afterthought

## What Shipped

### 1. `sky10` got a real ChatGPT Codex settings surface

The onboarding and settings flow now points to a dedicated Codex page instead
of a generic secrets form.

Main files:

- [`web/src/pages/StartSetup.tsx`](../../../../../web/src/pages/StartSetup.tsx)
- [`web/src/pages/SettingsCodex.tsx`](../../../../../web/src/pages/SettingsCodex.tsx)
- [`web/src/lib/rpc.ts`](../../../../../web/src/lib/rpc.ts)
- [`pkg/codex/rpc.go`](../../../../../pkg/codex/rpc.go)

The page exposes the daemon-backed RPCs for:

- `codex.status`
- `codex.loginStart`
- `codex.loginComplete`
- `codex.loginCancel`
- `codex.logout`
- `codex.chat`

That matters because ChatGPT/Codex account linking stopped being a fake CTA and
became a first-party daemon capability with explicit state, errors, pending
login metadata, and logout behavior.

### 2. The daemon now owns the host-side OAuth flow

The first implementation step wrapped the Codex CLI, but the main product work
was the shift to host-owned OAuth in `pkg/codex`.

Main files:

- [`pkg/codex/service.go`](../../../../../pkg/codex/service.go)
- [`pkg/codex/oauth.go`](../../../../../pkg/codex/oauth.go)
- [`pkg/codex/store.go`](../../../../../pkg/codex/store.go)

The daemon now handles:

- PKCE generation
- auth URL assembly against `https://auth.openai.com/oauth/authorize`
- a localhost callback on `http://localhost:1455/auth/callback`
- code exchange against `https://auth.openai.com/oauth/token`
- manual completion from a pasted redirect URL, query string, or raw code
- refresh-token persistence and rotation

This is the architectural shift that made the feature feel like `sky10`
instead of "sky10 shells out to someone else's login state."

### 3. Codex credentials now live in `sky10`'s own local store

The linked ChatGPT/Codex account is now stored under the `sky10` root instead
of inside the Codex CLI home.

Current behavior:

- default path: `~/.sky10/codex/auth.json`
- directory mode: `0700`
- file mode: `0600`
- stored fields: access token, refresh token, optional ID token, expiry,
  account id, email, timestamps

Relevant file:

- [`pkg/codex/store.go`](../../../../../pkg/codex/store.go)

This was intentionally kept device-local. Tokens are not synced through swarm
KV, mailbox, or the normal secrets flow.

It is still a V1 storage model. The credentials are in a restricted local file,
not the OS keychain yet.

### 4. `sky10` can now talk to the ChatGPT Codex responses transport

Once the daemon owns a valid ChatGPT/Codex credential, `/codex` can send prompts
through the Codex responses endpoint at
`https://chatgpt.com/backend-api/codex/responses`.

Main files:

- [`pkg/codex/chat.go`](../../../../../pkg/codex/chat.go)
- [`pkg/codex/service.go`](../../../../../pkg/codex/service.go)
- [`web/src/pages/CodexChat.tsx`](../../../../../web/src/pages/CodexChat.tsx)
- [`web/src/App.tsx`](../../../../../web/src/App.tsx)

The daemon builds the message payload, sends `stream: true`, parses the SSE
response stream, and returns a simplified chat result back to the web UI. The
web page is intentionally minimal:

- single model selector (`gpt-5.4`)
- local transcript persistence in browser `localStorage`
- simple usage display
- no daemon-side thread persistence
- no sidebar entry after follow-up cleanup

This was enough to prove that a linked ChatGPT account could do real work
through `sky10`, even before the broader agent-consumption layer exists.

### 5. Auth UX hardening followed immediately

The first working auth flow still had rough edges, so there were several quick
follow-up fixes.

Main files:

- [`web/src/lib/oauthPopup.ts`](../../../../../web/src/lib/oauthPopup.ts)
- [`web/src/pages/SettingsCodex.tsx`](../../../../../web/src/pages/SettingsCodex.tsx)
- [`pkg/codex/oauth.go`](../../../../../pkg/codex/oauth.go)
- [`pkg/codex/store.go`](../../../../../pkg/codex/store.go)

The main follow-ups were:

- fix the blank reconnect popup by opening a real placeholder window first and
  redirecting that same window once the daemon returns the authorize URL
- add a parallel new-tab launch path so popup-vs-tab behavior can be compared
- restyle the popup and localhost completion page toward an OpenAI-like neutral
  green auth theme instead of a warm placeholder page
- backfill `email` and `account_id` from nested JWT claims so linked sessions
  report usable identity metadata in the UI
- fix the brokered chat path to use the live Codex SSE response stream

These were not cosmetic-only tweaks. They were the difference between "the auth
flow exists" and "the auth flow behaves like something users can trust."

### 6. The Codex CLI fallback was removed

The host-owned OAuth path won out as the product direction.

Relevant files:

- [`pkg/codex/service.go`](../../../../../pkg/codex/service.go)
- [`web/src/pages/SettingsCodex.tsx`](../../../../../web/src/pages/SettingsCodex.tsx)

After the cleanup:

- `sky10` no longer probes `codex login status`
- `sky10` no longer treats CLI-managed auth as a linked account
- `sky10` no longer logs out through the Codex CLI
- the settings and chat pages only understand `sky10`-managed ChatGPT OAuth

This made the feature simpler and more honest. If a user wants `/codex`, they
need to link ChatGPT through `sky10` itself.

## Compatibility Boundary

This work intentionally followed the same broad shape OpenClaw documents for
`openai-codex`: PKCE, browser auth, localhost callback capture, token exchange,
local refresh-token storage, and calls to `chatgpt.com/backend-api`.

## Architecture

The working shape is:

```
Browser UI
  SettingsCodex.tsx / CodexChat.tsx
          |
          | RPC
          v
sky10 daemon
  pkg/codex/service.go
  pkg/codex/oauth.go
  pkg/codex/store.go
  pkg/codex/chat.go
          |
          | OAuth / Codex responses
          v
auth.openai.com + chatgpt.com/backend-api/codex/responses
```

Important boundaries:

- the daemon owns tokens and refresh
- the browser never talks directly to ChatGPT for the actual API call
- the linked account stays local to one device
- `/codex` is a simple test/chat surface, not the final agent-execution model
- the `openai-codex`-style lane is distinct from normal `/v1/*` API-key usage

## User-Facing Outcome

After this series, a user can:

- click `Connect ChatGPT` in `sky10`
- finish a browser OAuth flow with localhost callback or manual paste fallback
- see the linked ChatGPT account state inside `sky10`
- open `/codex`
- send a prompt through the ChatGPT Codex responses transport without entering
  an API key

That is a real product step forward. It makes `sky10` feel AI-native even when
the user's starting point is a ChatGPT subscription rather than API-platform
setup.

## Validation

The work was validated in stages as it shipped:

- `go test ./pkg/codex -count=1`
- `go test ./... -count=1`
- `make build-web`
- `make check`
- `bun test web/src/lib/oauthPopup.test.ts`

The codex-focused tests covered the risky boundaries called out in the planning
docs:

- authorize URL generation
- manual completion parsing
- callback-driven completion
- token refresh and rotated credential persistence
- logout clearing local credentials

That matters because the main failure modes in this kind of integration are not
"can we open the login page once," but restart persistence, refresh-token
rotation, callback recovery, and reconnect behavior.

## Current Boundaries

This integration is intentionally incomplete in a few important ways:

- tokens are stored in a local file, not a keychain-backed store yet
- callback bind/browser-failure telemetry could still be stronger
- `/codex` is a minimal local chat page, not a full agent-work broker
- there is no general `codex.run` / `codex.models` / `codex.rateLimits` surface
  yet
- there are no per-agent policy, budget, or concurrency controls yet
- the settings flow could still do a better job explaining what a linked
  account enables next
- the route still exists, but the sidebar entry was removed so the main path is
  through setup/settings instead of persistent nav chrome

That is the right tradeoff for the first integration. The feature now proves
that ChatGPT/Codex can be linked and exercised directly in `sky10`. The next
step is turning that into a broader brokered capability agents can consume
safely.
