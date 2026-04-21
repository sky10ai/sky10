---
created: 2026-04-21
updated: 2026-04-21
model: gpt-5.4
---

# OpenAI Codex OAuth Checklist

## Milestone 1: Host-Owned OAuth Core

### Outcome

`sky10` owns the ChatGPT/Codex OAuth flow locally on the device instead of
depending on the Codex CLI for sign-in state.

### Checklist

- [x] Replace the CLI-only login wrapper with a host-owned OAuth service in
      [`pkg/codex/service.go`](../../../../pkg/codex/service.go).
- [x] Add PKCE generation, authorize URL assembly, token exchange, and refresh
      logic in [`pkg/codex/oauth.go`](../../../../pkg/codex/oauth.go).
- [x] Persist ChatGPT/Codex tokens locally in
      [`pkg/codex/store.go`](../../../../pkg/codex/store.go).
- [x] Decode account metadata from the OAuth access token for UI status.
- [x] Keep legacy Codex CLI status/logout compatibility as a fallback path.
- [ ] Move sensitive token storage to a platform keychain or equivalent
      encrypted store instead of plain local files.

### Exit Criteria

- [x] `sky10` can start and complete ChatGPT/Codex OAuth without shelling out
      to `codex login`.
- [x] Access and refresh tokens survive daemon restarts on the same device.
- [x] Token refresh runs through one canonical local credential sink.

## Milestone 2: Browser Callback And Manual Recovery

### Outcome

The sign-in flow feels one-click when localhost callback capture works, but it
still recovers cleanly when the browser cannot hand the code back to `sky10`.

### Checklist

- [x] Start a localhost callback listener before opening the browser.
- [x] Emit a browser authorization URL from `codex.loginStart`.
- [x] Support manual completion via a new `codex.loginComplete` RPC method.
- [x] Accept full redirect URLs, query strings, or raw codes in manual
      completion.
- [x] Support cancelling an in-flight login with `codex.loginCancel`.
- [ ] Add stronger callback telemetry for bind failures and browser callback
      errors.

### Exit Criteria

- [x] A user can click `Connect ChatGPT` and finish without an API key.
- [x] A user can still finish the flow if localhost callback capture fails.
- [x] The daemon emits live auth-state updates during login and logout.

## Milestone 3: Settings And Onboarding UX

### Outcome

The UI presents this as a first-party sky10 feature instead of a thin wrapper
around the Codex CLI.

### Checklist

- [x] Update [`web/src/pages/SettingsCodex.tsx`](../../../../web/src/pages/SettingsCodex.tsx)
      to describe browser OAuth instead of CLI device auth.
- [x] Add manual paste UI for callback fallback.
- [x] Surface whether the current session is `host_oauth` or `cli_managed`.
- [x] Update settings and header copy to reflect host-owned OAuth.
- [x] Keep the onboarding entry point as `Connect ChatGPT`.
- [ ] Add explicit success UI that explains what agents can do next with the
      linked account.

### Exit Criteria

- [x] The user can tell whether sky10 or the Codex CLI currently owns the
      linked session.
- [x] The page explains how to recover when the localhost redirect fails.
- [x] The setup flow still lets the user continue into agent creation after a
      successful ChatGPT link.

## Milestone 4: Hardening And Validation

### Outcome

The new auth path is tested around the risky boundaries OpenClaw-like flows
usually fail on: callback handling, refresh, and persistence.

### Checklist

- [x] Add Go tests for authorize URL generation.
- [x] Add Go tests for manual completion input parsing.
- [x] Add Go tests for callback-driven completion.
- [x] Add Go tests for token refresh and rotated credential persistence.
- [x] Add Go tests for logout clearing local credentials.
- [x] Run `make check`.
- [x] Run `go test ./... -count=1`.
- [x] Run `make build-web`.

### Exit Criteria

- [x] The Codex package tests cover the main login, refresh, and logout paths.
- [x] Repo-wide validation passes after the OAuth migration.
- [x] The web build completes against the new RPC contract.

## Milestone 5: Execution Broker Follow-Up

### Outcome

The linked ChatGPT/Codex account stops being just a settings surface and becomes
usable by agents through brokered daemon capabilities.

### Checklist

- [ ] Define brokered execution RPCs such as `codex.run`, `codex.models`, and
      `codex.rateLimits`.
- [ ] Ensure agents call `sky10` instead of receiving raw ChatGPT tokens.
- [ ] Add policy controls around which agents may consume the linked account.
- [ ] Add budgets or concurrency caps so multiple agents do not blindly share
      one ChatGPT/Codex entitlement.
- [ ] Surface linked-account usage in the UI.

### Exit Criteria

- [ ] A linked ChatGPT/Codex account can actually power agent work through
      `sky10`.
- [ ] Raw OAuth tokens remain device-local and out of guest runtimes.
- [ ] Shared-account limits are visible and enforceable.
