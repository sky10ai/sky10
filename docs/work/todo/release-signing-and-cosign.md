---
created: 2026-04-09
updated: 2026-04-09
---

# Release Signing And Cosign

Tracking whether sky10 should add release signing with Sigstore/cosign
on top of its existing deterministic-build and checksum workflow.

## Current State

sky10 already has meaningful supply-chain strengths:

- Deterministic Go build flags in
  [`Makefile`](../../../Makefile)
- Repeat-build reproducibility checks in
  [`Makefile`](../../../Makefile)
- Release checksums generated from built artifacts in
  [`Makefile`](../../../Makefile)
- Tag-time CI that rebuilds release binaries from source and compares
  them byte-for-byte against the published release assets in
  [`.github/workflows/verify-release.yml`](../../../.github/workflows/verify-release.yml)

That is already a strong base. It proves that a published release asset
matches the tagged source when rebuilt under the expected environment.

## What Deterministic Builds Already Give Us

- A maintainer or third party can rebuild a tagged release and confirm
  it is byte-identical
- CI can catch release drift, accidental local build differences, and
  packaging mistakes
- Checksums make corruption and accidental mismatch visible
- Reproducibility reduces trust in the release machine because the
  output can be independently checked

For sky10, this matters a lot because the current release process is
already designed around reproducibility rather than opaque one-off
builds.

## What Cosign Adds

Cosign answers a different question from reproducibility.

Deterministic builds answer:
"Can this artifact be rebuilt from source and shown to match?"

Cosign answers:
"Did a trusted publisher authorize this exact artifact?"

That extra property helps with:

- Publisher-authenticated release downloads
- Machine-verifiable trust for fleet automation and CI
- Self-update verification before swapping binaries
- Later provenance, attestation, and SBOM work if sky10 wants it
- Transparency-log-backed signing if using Sigstore keyless flows

## What Cosign Does Not Replace

- It does not make builds reproducible
- It does not prove the source itself is correct or reviewed
- It does not secure the updater unless the updater verifies signatures
- It does not remove the need for checksums or release validation CI

The right model is additive:
keep deterministic builds, keep byte-for-byte CI verification, and add
publisher verification where artifacts are consumed.

## sky10's Current Gap

The main gap is not "can we reproduce releases?" The repo already does
that well.

The main gap is "do clients verify publisher identity before install?"

Today:

- `sky10 update` fetches the latest GitHub release and downloads the
  platform binary in [`pkg/update/update.go`](../../../pkg/update/update.go)
- The updater replaces the executable after download in
  [`pkg/update/update.go`](../../../pkg/update/update.go)
- The menu updater uses `checksums-menu.txt` to decide whether a local
  update is needed, but that checksum file is itself just downloaded
  from the same release in
  [`pkg/update/update.go`](../../../pkg/update/update.go)

That means the update path is still mostly:
"trust GitHub over HTTPS and trust the release page contents"

That is not terrible, but it is weaker than signed verification.

## Is Sigstore Paid?

As of 2026-04-09, Sigstore's public-good service is not a paid SaaS in
the normal sense. Cosign is open source, and the public Fulcio/Rekor
services are available to use without a normal per-seat or per-signature
fee.

Costs only appear if sky10 self-hosts Sigstore components or adopts
adjacent infrastructure with its own pricing.

## Recommendation

Cosign is useful for sky10, but only if it closes the updater and
artifact-verification gap. It is less valuable if it is added only as a
release decoration that humans never verify.

Recommended order:

1. Keep the current deterministic-build and release-compare workflow
2. Add signed publisher verification to the updater path
3. Only then decide whether full Sigstore/cosign adoption is better
   than a simpler signed-manifest approach

## Options

### Option A: Keep current reproducibility only

Pros:

- No extra workflow complexity
- Current CI already provides strong integrity checks

Cons:

- End users and automated updaters still rely mostly on GitHub transport
- No publisher-authenticated verification before install

### Option B: Sign a release manifest or `checksums.txt`

Pros:

- Smallest change that closes the updater-authentication gap
- Updater can verify a signed manifest before applying a download
- Simpler operational model than full artifact-by-artifact verification

Cons:

- Less aligned with Sigstore ecosystem tooling
- Fewer future-proof hooks for attestations and provenance

### Option C: Adopt cosign for release assets

Pros:

- Standard ecosystem tooling
- Works well with keyless signing and transparency logs
- Good foundation for provenance, attestations, and policy later

Cons:

- More workflow and UX complexity
- Updater verification logic becomes more involved
- Might be more machinery than sky10 needs if the only goal is binary
  update verification

## Proposed Next Steps

- [ ] Decide whether the immediate goal is "signed updater verification"
  or "full Sigstore ecosystem adoption"
- [ ] Choose the artifact to verify: individual binaries, a signed
  checksums manifest, or both
- [ ] Choose trust model: keyless Sigstore identity, pinned public key,
  or both
- [ ] Update release workflows to publish signatures and any required
  verification material alongside assets
- [ ] Teach [`pkg/update/update.go`](../../../pkg/update/update.go) to
  verify signatures before replacing the current binary
- [ ] Fail closed on signature mismatch in the updater
- [ ] Keep [`.github/workflows/verify-release.yml`](../../../.github/workflows/verify-release.yml)
  as an independent reproducibility check even after signing exists
- [ ] Document manual verification steps for users who download binaries
  directly

## Suggested Decision

If the goal is practical security for sky10's current distribution model,
start by signing a release manifest and verifying it in the updater.

If the goal is broader supply-chain posture, public provenance, and
ecosystem interoperability, then cosign is worth adopting after that
decision is explicit.
