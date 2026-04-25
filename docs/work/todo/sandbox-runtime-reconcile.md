---
created: 2026-04-25
updated: 2026-04-25
---

# Sandbox Runtime Reconcile And Auto-Update

This is deferred work. It came out of the Lima hardening follow-up, but it is
too much hardening for the current stage.

Related context:

- [`../past/2026/04/24-Lima-VM-Hardening.md`](../past/2026/04/24-Lima-VM-Hardening.md)
- [`../past/2026/04/25-Lima-Sandbox-Hardening-Followup-And-Runtime-Drift.md`](../past/2026/04/25-Lima-Sandbox-Hardening-Followup-And-Runtime-Drift.md)

## Goal

Eventually, existing sandboxes should be able to converge to the runtime
bundle shipped by the current host `sky10` without recreating the VM.

That convergence should cover the pieces that need to move together:

- guest `sky10`
- OpenClaw
- `sky10-openclaw` plugin glue
- runtime files staged into the sandbox state directory
- Linux security package updates, if the chosen policy includes them

## Non-Goals For Now

- Do not add an automatic reconcile loop yet.
- Do not restart guest services automatically from `/agents` page load.
- Do not make `/agents` or individual agent detail views wait on upgrade work.
- Do not add manual "upgrade runtime" UI controls unless the product shape is
  revisited.
- Do not change the host-to-guest forwarded-port path that keeps the UI snappy.

## Possible Future Shape

If this becomes active work later, the likely shape is:

1. Define a runtime bundle manifest with expected versions and file hashes.
2. Expose or reuse guest RPCs that report actual guest `sky10`, OpenClaw, and
   plugin/runtime state.
3. Keep `sandbox.runtime.status` observational: report current versus desired
   state without changing the VM.
4. Add an explicit reconcile operation that computes the delta and applies only
   the needed steps.
5. Make reconciliation idempotent so running it twice is a no-op when the VM is
   already current.
6. Verify guest `sky10` health, OpenClaw gateway health, guest agent
   registration, and host forwarded-port reachability after changes.

## Design Constraints

- Reconcile should be a background/maintenance flow, not part of the hot path
  for loading `/agents` or an agent detail page.
- Guest paths should remain stable unless there is a deliberate migration. For
  OpenClaw, current staged paths include:
  - `/sandbox-state/plugins/openclaw-sky10-channel`
  - `/sandbox-state/runtime/openclaw`
- Updates to guest `sky10`, OpenClaw, and the plugin glue need to be treated as
  one compatibility unit.
- Existing VMs should only be modified when the user or a future product policy
  clearly opts into reconciliation.

## Open Questions

- What is the first runtime bundle manifest schema?
- Which component owns the desired bundle version: host `sky10`, the bundle
  itself, or release metadata?
- Should Linux security packages be part of this reconcile flow, or a separate
  guest maintenance policy?
- What restart boundaries are acceptable for OpenClaw, guest `sky10`, and
  Docker-backed sandboxes?
- How should partial failure be surfaced without making sandbox detail pages
  noisy?
