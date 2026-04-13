---
created: 2026-04-13
updated: 2026-04-13
model: gpt-5
---

# Secrets V1 Plan

## Goal

Build a usable secrets layer on top of KV for owner-controlled secrets such as
API keys, tokens, DSNs, certs, and small private artifacts shared across the
sky10 private network.

V1 should optimize for device-to-device secret sync, not wallet-specific flows
and not agent-scoped access control.

## Working decisions

- Secrets are device-scoped in V1, not agent-scoped.
- Device trust is not uniform. We need at least two device classes:
  `trusted` and `sandbox`.
- New secrets should default to the current device only.
- Secret sharing semantics should become:
  - `current`: current device only
  - `trusted`: all current and future trusted devices
  - `explicit`: pinned device list only
- Sandbox devices are never part of implicit secret sharing.
- Sharing a secret to a sandbox device should be explicit and noisy.
- Mailbox is not a blocker for V1 storage/sync. It is follow-on work for
  approvals and durable workflows.

## V1 secret classes

- API keys
- Tokens
- DSNs
- Certs
- Small files and binary blobs
- OWS backups remain supported, but are not the default path

## Stash audit

Current conclusion after reviewing `stash@{0}` and `stash@{1}`:

- `stash@{0}` is effectively a backup copy of the current secrets branch.
- `stash@{1}` is an older snapshot from before the newer KV hardening landed.
- The current worktree is the source of truth.
- We should keep the current secrets implementation and current KV transport
  integration.
- We should not preserve the current soft agent policy model as a first-class
  V1 feature.
- We should not preserve `--device all` as the long-term sharing model.

## Milestone 0: Checkpoint Current Base

- [x] Commit the current secrets worktree as the baseline.
- [x] Treat the current implementation as the source of truth, not either stash.
- [x] Drop `stash@{1}` after the baseline commit exists.
- [x] Drop `stash@{0}` after confirming the baseline commit contains the full
      secrets implementation.
- [x] Preserve the current secrets package, CLI, tests, and KV transport
      integration as the starting point.
- [x] Record explicitly that soft agent policy is present in code but not part
      of the V1 product boundary.

## Milestone 1: Clarify Product Surface

- [x] Reframe secrets as general-purpose private-network secret sync for API
      keys and similar small artifacts.
- [x] Remove wallet-specific assumptions from the CLI and docs.
- [x] Change the default secret kind away from `ows-backup`.
- [x] Keep support for binary payloads, but make string/value flows ergonomic.
- [x] Define the V1 supported secret classes:
      API keys, tokens, DSNs, certs, small files.
- [x] Keep secret names and secret payloads out of raw KV keys and values.

## Milestone 2: Introduce Device Trust Classes

- [ ] Add device role metadata with at least `trusted` and `sandbox`.
- [ ] Decide where device role lives in identity/bundle/manifest metadata.
- [ ] Keep topology separate from trust class:
      use `host_device_id` or `parent_device_id` if needed later.
- [ ] Surface device role in RPC and CLI device listings.
- [ ] Treat existing devices as `trusted` by default during migration unless a
      better migration path is defined.
- [ ] Define how new sandbox devices are created so they do not silently become
      trusted recipients.

## Milestone 3: Replace Recipient Semantics With Scopes

- [ ] Add secret scope metadata: `current`, `trusted`, `explicit`.
- [ ] Default new secrets to `current`.
- [ ] Map explicit `--device` usage to `explicit`.
- [ ] Remove or deprecate the implicit `all` behavior in favor of `trusted`.
- [ ] Keep recipient device IDs as resolved state for `explicit` secrets.
- [ ] Ensure sandbox devices are excluded from `trusted` resolution.
- [ ] Decide how scope is represented in RPC, CLI, and persisted metadata.

## Milestone 4: Fix Join and Namespace Distribution

- [ ] Extend join-time namespace-key sharing so it includes `secrets`, not just
      the default KV namespace.
- [ ] Verify a newly joined trusted device can at least sync the secrets
      transport after join.
- [ ] Verify a newly joined sandbox device does not receive implicit decrypt
      rights to existing secrets.
- [ ] Define what happens when an existing device changes role from `trusted`
      to `sandbox` or vice versa.
- [ ] Audit whether any additional namespace-specific caches or local paths need
      migration support.

## Milestone 5: Add Trusted-Scope Reconciliation

- [ ] Detect membership changes relevant to secret recipients.
- [ ] On trusted-device join, rewrap all `trusted` secrets to include the new
      trusted device.
- [ ] On trusted-device removal or downgrade, rewrap all `trusted` secrets to
      exclude that device.
- [ ] Ensure `explicit` secrets are never changed by trusted-scope
      reconciliation.
- [ ] Ensure `current` secrets are never expanded automatically.
- [ ] Make reconciliation idempotent and safe across restarts.
- [ ] Decide whether reconciliation runs inline, in a background worker, or via
      queued jobs.

## Milestone 6: Tighten CLI and RPC UX

- [ ] Add first-class scope selection in the CLI.
- [ ] Show device roles in `sky10 secrets devices`.
- [ ] Warn clearly when a sandbox device is selected as a recipient.
- [ ] Add better `--value` and env-oriented usage for small string secrets.
- [ ] Keep `get` safe by default for binary payloads and avoid casual plaintext
      printing where possible.
- [ ] Confirm list/status output exposes enough information to understand scope
      and recipient state without leaking secret material.

## Milestone 7: Hide Internal Storage Better

- [ ] Decide on the internal key naming scheme for secret transport records.
- [ ] Move toward a reserved internal prefix if we want the KV UI to hide these
      records by default.
- [ ] Prefer a consistent reserved naming scheme across the repo, likely
      `_sys/...` or `_sys:...`, instead of a one-off secrets-only convention.
- [ ] Hide the secrets namespace and/or internal-prefix records from generic KV
      UI by default.
- [ ] Keep this as a UX boundary only, not a security boundary.

## Milestone 8: Expand Tests To Match The Trust Model

- [ ] Keep the current store-level coverage as the baseline.
- [ ] Add tests for `current`, `trusted`, and `explicit` scope behavior.
- [ ] Add tests for trusted-device join inheriting trusted-scoped secrets.
- [ ] Add tests for sandbox-device join not inheriting trusted-scoped secrets.
- [ ] Add tests for trusted-device downgrade/removal triggering exclusion on
      rewrap.
- [ ] Add tests proving `explicit` secrets stay pinned across membership changes.
- [ ] Add process-level or multi-daemon tests that cover join, sync, rewrap,
      restart, and recovery.

## Milestone 9: Defer Agent Access Cleanly

- [ ] Stop presenting agent allowlists/approval flags as a real security
      boundary for V1.
- [ ] Keep any remaining agent-related code clearly marked as deferred or
      internal-only.
- [ ] Revisit agent access only after there is an authenticated caller boundary
      and a real sandbox/broker path.
- [ ] Use mailbox later for durable approvals, audit, and exception workflows
      such as sandbox-device grants.

## Success criteria for V1

- [ ] A user can store an API key or similar secret on one trusted device.
- [ ] A second trusted device in the same private network can sync and decrypt
      that secret when scope permits.
- [ ] A sandbox device does not receive implicit access.
- [ ] A new trusted device can receive trusted-scoped secrets after join and
      reconciliation.
- [ ] Secret names and payloads are not exposed as ordinary KV records.
- [ ] The CLI is oriented toward everyday secrets usage, not wallet-only flows.
