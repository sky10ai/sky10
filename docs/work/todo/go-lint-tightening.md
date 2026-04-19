---
created: 2026-04-19
updated: 2026-04-19
---

# Go Lint Tightening

Tracking follow-up work for tightening the repo's Go lint coverage
without turning CI red from existing backlog.

## Current State

The branch currently uses:

- [`golangci-lint`](../../../.golangci.yml) as the only added Go quality
  tool
- [`make check`](../../../Makefile) as the local lint gate
- [`.github/workflows/test.yml`](../../../.github/workflows/test.yml)
  for PR and push CI

Enabled linters today:

- `bodyclose`
- `dupl`
- `nolintlint`
- `rowserrcheck`
- `sloglint`
- `sqlclosecheck`

Current `dupl` setting:

- `threshold: 500`

That threshold is intentionally loose. It is meant to catch only
obvious, large copy-paste blocks and avoid noisy failures while the repo
gets used to the rule.

## Why Tighten Later

The current setup is conservative on purpose:

- it already passes in CI
- it adds some real signal beyond `gofmt` and `go vet`
- it avoids landing a cleanup project disguised as a tooling change

The next step should be gradual ratcheting, not enabling every linter at
once.

## Dupl Plan

### Stage 1: Keep `dupl` at 500

- [x] Enable `dupl`
- [x] Keep the threshold loose enough to avoid current failures
- [x] Confirm [`make check`](../../../Makefile) passes with it enabled

### Stage 2: Dry-run lower thresholds

- [ ] Run `dupl` locally at `400`
- [ ] Run `dupl` locally at `300`
- [ ] Run `dupl` locally at `250`
- [ ] Record which findings are production-code duplicates versus test noise

Suggested dry-run command:

```sh
./.tools/bin/golangci-lint run --no-config --default=none --enable=dupl ./...
```

To test a lower threshold, temporarily adjust
[`dupl.threshold`](../../../.golangci.yml) and rerun `make check`.

### Stage 3: Decide how much test duplication should count

- [ ] Decide whether `dupl` should report on `*_test.go`
- [ ] If test duplication is too noisy, add path-based exclusions for
      tests instead of turning `dupl` off entirely
- [ ] Keep production code under tighter duplication pressure than test
      scaffolding

### Stage 4: Ratchet down

- [ ] Lower `dupl` from `500` to `400` once the output stays low-noise
- [ ] Lower `dupl` from `400` to `300` only after fixing the obvious
      large duplicates
- [ ] Avoid dropping lower unless the team wants duplication cleanup to
      become active PR pressure

## Other Linters Worth Considering

These are the next useful candidates inside `golangci-lint`, ordered by
practical value rather than popularity.

### 1. `gocyclo`

Good for:

- large branch-heavy functions
- handlers and orchestration code that are getting hard to change

Why consider it:

- low ceremony
- easy to explain
- good at catching functions that need splitting

Suggested rollout:

- [ ] Dry-run `gocyclo`
- [ ] Start with a loose threshold such as `20` or `25`
- [ ] Only tighten if the output is obviously useful

### 2. `gocognit`

Good for:

- code that is difficult to follow even when cyclomatic complexity is
  not extreme
- nested or stateful control flow

Why consider it:

- often more aligned with review pain than raw branch count

Suggested rollout:

- [ ] Dry-run `gocognit`
- [ ] Compare its output to `gocyclo`
- [ ] Keep only one of the two if they mostly flag the same code

### 3. `nestif`

Good for:

- deeply nested conditional trees

Why consider it:

- very targeted
- easier to act on than broad style linters

Suggested rollout:

- [ ] Dry-run `nestif`
- [ ] Use only if the findings are small in number and clearly readable

### 4. `goconst`

Good for:

- repeated string and literal values

Why consider it:

- can reduce drift in repeated command names, RPC methods, event names,
  and sentinel strings

Risk:

- can become annoying if it pushes pointless constants for values that
  are clearer inline

Suggested rollout:

- [ ] Dry-run `goconst`
- [ ] Keep it only if the findings are obviously maintainability wins

## Linters To Treat Carefully

These are not bad tools, but they are more likely to create churn than
early value in this repo.

- `funlen`
- `maintidx`
- `revive`
- `gocritic`

If these are reconsidered later:

- [ ] enable one at a time
- [ ] start in dry-run mode
- [ ] do not turn on broad style bundles all at once

## Rollout Rule

When adding any new linter:

1. dry-run it first
2. confirm whether the findings are mostly real signal
3. pick a loose starting threshold if the linter supports one
4. land it only when [`make check`](../../../Makefile) still passes

The goal is not "maximum lint coverage." The goal is "extra bug and
maintainability signal without making CI annoying."
