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
- `revive` with only `file-length-limit`
- `rowserrcheck`
- `sloglint`
- `sqlclosecheck`

Current `dupl` setting:

- `threshold: 350`

Current `revive` file-length setting:

- `file-length-limit.max: 2500`
- `skip-comments: true`
- `skip-blank-lines: true`

That threshold is intentionally loose. It is meant to catch only
obvious, large copy-paste blocks and avoid noisy failures while the repo
gets used to the rule.

The file-length limit is also intentionally loose. It adds a hard stop
for truly oversized Go files without forcing a broad split-up pass in
the same change.

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

## File Length Plan

### Stage 1: Keep `file-length-limit` at 2500

- [x] Enable `revive` only for `file-length-limit`
- [x] Ratchet to `max: 2500` while CI stays green
- [x] Confirm `make lint` passes with the new limit

### Stage 2: Inventory the path to 1000

Go files currently above `1000` lines:

- `pkg/sandbox/manager_test.go` (`2488`)
- `pkg/messaging/broker/broker_test.go` (`2039`)
- `pkg/fs/p2p.go` (`1697`)
- `pkg/fs/rpc_drive_test.go` (`1615`)
- `pkg/agent/router.go` (`1483`)
- `pkg/agent/router_test.go` (`1446`)
- `pkg/fs/daemon_v2_5_test.go` (`1419`)
- `pkg/fs/reconciler_test.go` (`1343`)
- `pkg/fs/opslog/opslog.go` (`1238`)
- `commands/agent_lima.go` (`1153`)
- `pkg/secrets/store_test.go` (`1132`)
- `commands/agent_lima_test.go` (`1029`)
- `pkg/agent/rpc_test.go` (`1024`)
- `pkg/fs/skyfs.go` (`1006`)

Follow-up tasks:

- [ ] Split the largest production files before lowering the cap
- [ ] Decide whether large test files should be split the same way or
      exempted with path-based exclusions
- [x] Dry-run `file-length-limit` at `2500`
- [ ] Dry-run `file-length-limit` at `2000`, `1500`, and `1000`
- [ ] Record whether each remaining violation is production code, test
      scaffolding, or generated-style glue that should stay together

### Stage 3: Tighten to 1000

- [x] Lower the repo-wide cap from `3000` to `2500`
- [ ] Lower from `2500` to `1500` once the largest packages are broken
      up
- [ ] Land `1000` only after the remaining violations are either fixed
      or intentionally excluded with reviewable path rules

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
- `gocritic`

`revive` is already enabled for the narrow `file-length-limit` rule.
Treat broader `revive` style bundles as a separate decision.

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
