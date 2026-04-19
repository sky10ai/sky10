---
created: 2026-04-19
updated: 2026-04-19
---

# Go Security Tooling Consideration

Tracking whether sky10 should reintroduce `govulncheck` and `gosec`
after the repo moved back to a `golangci-lint`-only Go quality setup.

## Current State

The repo currently does not wire either tool into:

- [`Makefile`](../../../Makefile)
- [`.github/workflows/test.yml`](../../../.github/workflows/test.yml)

That is intentional. The current PR/push gate is meant to stay focused
on fast, low-noise lint and test checks.

## Why These Tools Were Removed

Both tools were useful in principle, but neither was a good default
blocking check for this repo at the time they were tested.

### `govulncheck`

What it is good at:

- finding reachable Go vulnerabilities in actual dependency usage
- giving higher-confidence results than broad static scanners

Why it was not kept in the normal gate:

- the remaining reported issue was a libp2p DHT advisory without a known
  fixed version in the Go vuln DB
- that made `make vuln` fail even after the repo's Go toolchain was
  upgraded
- a failing check with no actionable fix is not a good default PR gate

### `gosec`

What it is good at:

- surfacing command execution, file permission, path handling, and other
  security-sensitive patterns

Why it was not kept in the normal gate:

- the repo produced a large backlog immediately
- the output mixed real concerns with expected patterns and likely false
  positives
- that volume made it unsuitable as a default blocking CI check

## When `govulncheck` Is Worth Reconsidering

`govulncheck` is the stronger candidate to bring back later.

It is worth reconsidering when:

- the repo wants a scheduled or manual dependency-audit workflow again
- the remaining unresolved libp2p advisory gets a fixed version or is
  otherwise clarified
- the team wants a separate "audit" job that is advisory rather than
  blocking

Possible rollout:

- [ ] re-add `govulncheck` as a manual workflow
- [ ] optionally add a weekly scheduled workflow
- [ ] keep it outside the normal PR gate unless the findings are clearly
      actionable
- [ ] document any ignored advisory IDs explicitly if the repo chooses
      to waive them

## When `gosec` Is Worth Reconsidering

`gosec` is only worth bringing back if the repo wants an explicit
security triage project.

It is worth reconsidering when:

- someone is prepared to sort true positives from noise
- the repo wants security findings tracked separately from normal lint
- the team is willing to add suppressions with justification where
  needed

Possible rollout:

- [ ] run `gosec` manually and export the findings
- [ ] group findings by rule ID and count
- [ ] fix the obvious high-signal issues first
- [ ] add narrow suppressions for accepted patterns
- [ ] only consider CI after the backlog is small enough to be useful

## Recommendation

If either tool comes back, prefer this order:

1. `govulncheck` as advisory-only
2. `gosec` only as a separate triage effort

Do not put either one back into the default PR gate unless:

- the output is actionable
- the noise is low
- the repo has a clear policy for handling failures

## Suggested Decision

For now:

- keep [`golangci-lint`](../../../.golangci.yml) as the only added Go
  quality gate
- treat `govulncheck` as a future audit candidate
- treat `gosec` as a future security-review project, not a casual
  linter toggle
