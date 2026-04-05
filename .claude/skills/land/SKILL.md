---
name: land
description: Land the current short-lived branch onto main with a rebase onto origin/main, fast-forward-only integration, push, and branch cleanup. Use only when the user explicitly says "land this branch" or otherwise clearly asks to integrate the current task branch.
allowed-tools: Read, Edit, Write, Bash, Glob, Grep
---

# Land the current branch

Use this skill only when the user explicitly authorizes branch landing,
for example by saying `land this branch`.

The repo workflow is:

- user-managed worktrees
- short-lived task branches
- rebase onto `origin/main`
- fast-forward-only integration onto `main`
- no merge commits

## Default boundaries

- Work in the current worktree. Do not create a new worktree.
- Do not remove the current worktree unless the user explicitly asked
  for worktree cleanup too.
- Do not tag or release as part of landing unless the user explicitly
  asked for that separately.

## 1. Confirm the branch is landable

Capture the branch name and inspect the worktree:

```bash
BRANCH="$(git branch --show-current)"
git status --short
```

If `BRANCH` is empty or already `main`, stop and clarify.

If the tree is dirty, decide whether the changes are the final state of
the completed task. If yes, commit and push them before continuing. If
no, stop and clarify instead of landing partial work.

## 2. Update from origin and rebase

Always rebase onto `origin/main`, not local `main`:

```bash
git fetch origin
git rebase origin/main
```

Do not merge `main` into the branch.

## 3. Run final validation

Run the strongest reasonable validation for the scope of the change.
Default for most Go changes:

```bash
make check
go test ./... -count=1
```

If web assets changed, also run:

```bash
make build-web
```

If validation fails, fix it before landing.

## 4. Push the rebased branch

Because rebasing rewrites history, update the remote branch with
`--force-with-lease`:

```bash
git push --force-with-lease origin "$BRANCH"
```

## 5. Land onto main without a merge commit

Push the current `HEAD` directly to remote `main`:

```bash
git push origin HEAD:main
```

This must be fast-forward only. If the push is rejected because `main`
moved, fetch again, rebase onto the new `origin/main`, rerun the
relevant final validation, push the branch with `--force-with-lease`,
and retry `git push origin HEAD:main`.

Do not use a merge commit.

## 6. Clean up the branch

Delete the remote branch immediately after `main` is updated:

```bash
git push origin --delete "$BRANCH"
```

For the local branch:

- If the current worktree still has the branch checked out, do not try
  to delete it in place. Say that the branch is landed and the worktree
  still owns the local branch.
- If the branch is not the checked-out branch of an active worktree,
  delete it:

```bash
git branch -d "$BRANCH"
```

## 7. Optional worktree cleanup

Only attempt this if the user explicitly asked to clean up the worktree
too.

Because worktrees are user-managed in this repo, prefer to report what
is ready for cleanup rather than assuming you should remove it. If local
cleanup is blocked by the current checkout or another worktree
ownership constraint, say so clearly.

## Closeout

Report:

- the branch that was landed
- that it was rebased onto `origin/main`
- which validation ran
- that `main` was updated without a merge commit
- whether the remote branch was deleted
- whether the local branch and worktree still need manual cleanup
