---
name: release
description: Cut a new sky10 release — tag, build CLI assets, publish the GitHub release, verify CI-built menu assets, and dogfood the upgrade path
allowed-tools: Read, Edit, Write, Bash, Glob, Grep
---

# Release sky10

Cut a new release of `sky10`. The version is passed as `$ARGUMENTS`
(for example `/release 0.5.0`).

If no version is provided, ask the user which version to release.

Before running the commands below, set:

```bash
VERSION="$ARGUMENTS"
```

## Preconditions

- Confirm the target commit is the one the user wants to ship.
- Confirm the working tree is clean enough for a release commit and tag.
- Confirm `gh auth status` succeeds before you start.
- Release builds need `go`, `bun`, and `shasum` available locally.

## Non-negotiable rules

- Never modify a published release. If anything is wrong after
  publication, cut a new patch release.
- Every release must include an empty release commit on the release
  target commit before tagging, using:
  `release: v$VERSION`
- Tag before building. The Makefile derives `VERSION` from git tags.
- Build the web frontend before building CLI release binaries. The Go
  binary embeds `web/dist/`.
- `sky10-menu` assets are attached later by
  `.github/workflows/build-menu.yml`. The release is not fully complete
  until those assets and `checksums-menu.txt` show up.

## 1. Create the release commit, tag, and push

Create the required empty release commit so releases are obvious in
`git log` and the tag always points at an explicit release record:

```bash
git commit --allow-empty -m "release: v$VERSION"
git tag "v$VERSION"
git push
git push origin "v$VERSION"
```

The schema version in `pkg/fs/schema.go` is a data-format version. Do
not bump it unless the storage format changed.

## 2. Build the release assets

Use the Makefile instead of hand-copying ldflags. The Makefile and
`.github/workflows/verify-release.yml` must stay in lockstep.

```bash
make clean
make build-web
make checksums
ls -la bin
cat bin/checksums.txt
```

This produces the four CLI release assets plus `bin/checksums.txt`:

- `sky10-darwin-arm64`
- `sky10-darwin-amd64`
- `sky10-linux-arm64`
- `sky10-linux-amd64`

Do not use `make build` for release assets. It only builds the local
binary.

## 3. Create the GitHub release

Generate concise notes from commits since the previous tag. By default,
match the `v0.47.0` release format unless the user explicitly asks for
something different.

Default release-note shape:

1. Title:
   `v$VERSION — <short summary>`
2. Body:
   - one short opening paragraph summarizing the release in prose
   - blank line
   - literal section label:
     `Commits since \`v<previous>\`:`
   - flat commit bullets, one per line

Do not default to ad hoc headings like `## What's Changed`.

When listing commits, link each short hash to its GitHub commit URL.

Example commit line:

```markdown
- [`abc1234`](https://github.com/sky10ai/sky10/commit/abc1234) short summary
```

Create the release with the CLI assets:

```bash
gh release create "v$VERSION" \
  bin/sky10-darwin-arm64 \
  bin/sky10-darwin-amd64 \
  bin/sky10-linux-arm64 \
  bin/sky10-linux-amd64 \
  bin/checksums.txt \
  --title "v$VERSION — <short summary>" \
  --notes "<release notes>"
```

## 4. Verify the published assets

Inspect the release assets after publication:

```bash
gh release view "v$VERSION" --json assets -q '.assets[].name'
```

Immediately after `gh release create`, you should see the four CLI
binaries and `checksums.txt`.

Then wait for GitHub Actions to finish attaching the menu assets from
`.github/workflows/build-menu.yml`. The release is only complete when
the asset list also includes:

- `sky10-menu-darwin-arm64`
- `sky10-menu-darwin-amd64`
- `sky10-menu-linux-arm64`
- `sky10-menu-linux-amd64`
- `checksums-menu.txt`

Also watch `.github/workflows/verify-release.yml`. It rebuilds the CLI
binaries from source and fails if the uploaded assets are not
byte-identical.

## 5. Dogfood the upgrade path

Once the release assets are available, verify the self-update path:

```bash
~/.bin/sky10 update
~/.bin/sky10 --version
~/.bin/sky10 daemon status
```

`sky10 update` already tries to update `sky10-menu`, restart the menu,
and restart the daemon. Do not kill or restart those processes manually
unless the command reports that restart failed.

If `sky10 update` warns that `sky10-menu` assets are missing, the menu
workflow has not finished yet. Wait for the assets to appear, then
retry.

## 6. Closeout

Share:

- the released version
- the install command:
  `curl -fsSL https://raw.githubusercontent.com/sky10ai/sky10/main/install.sh | bash`
- whether `sky10 update` dogfooding succeeded
- any CI failures, missing assets, or follow-up work
