---
name: release
description: Cut a new sky10 release — tag, build, GitHub release
allowed-tools: Read, Edit, Write, Bash, Glob, Grep
---

# Release sky10

Cut a new release of sky10. The version is passed as `$ARGUMENTS` (e.g. `/release 0.5.0`).

If no version is provided, ask the user what version to release.

## Installation model

sky10 installs to `~/.bin/sky10` via `install.sh` (curl pipe). No
Homebrew, no sudo. Users upgrade by re-running the install script or
downloading the binary from GitHub releases.

## CRITICAL: Order of operations

**Every step MUST happen in this exact order. Do NOT skip ahead.**

1. Tag the current commit
2. Push the tag
3. Build binaries (from the tagged commit)
4. Install locally
5. Create GitHub release with binaries
6. Restart daemon

If you build before tagging or upload before building — you will
produce a broken release.

**NEVER modify a published release.** Once a tag is pushed and a GitHub
release is created with assets, that version is FINAL. Do not re-upload
binaries, do not delete and recreate tags, do not edit release assets.
If something is wrong (wrong binary, missing assets, bad checksum), cut
a new patch release (e.g. v0.26.1). Re-uploading breaks checksums for
anyone who already downloaded.

## Steps

### 1. Tag and push tag

The CLI version comes from git tags via the Makefile — no hardcoded
version to update. The schema version (`pkg/fs/schema.go`
`SchemaVersion`) is a DATA FORMAT version and should NOT be bumped
during a release unless the storage format changed.

```bash
git tag v$VERSION
git push origin v$VERSION
```

### 2. Build binaries (all platforms)

**Build the web frontend first.** The Go binary embeds `web/dist/` via
`go:embed`. If you skip this step the web UI will not be served.

```bash
cd web && bun install --frozen-lockfile && bun run build && cd ..
```

Then build all four platform binaries:

```bash
rm -f bin/sky10-*
COMMIT=$(git rev-parse --short HEAD)
DATE=$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X 'main.version=v$VERSION' -X 'main.commit=$COMMIT' -X 'main.buildDate=$DATE'"
GOFLAGS="-trimpath -buildvcs=false"

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $GOFLAGS -ldflags "$LDFLAGS" -o bin/sky10-darwin-arm64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $GOFLAGS -ldflags "$LDFLAGS" -o bin/sky10-darwin-amd64 .
CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build $GOFLAGS -ldflags "$LDFLAGS" -o bin/sky10-linux-arm64 .
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build $GOFLAGS -ldflags "$LDFLAGS" -o bin/sky10-linux-amd64 .

cd bin && shasum -a 256 sky10-* > checksums.txt && cat checksums.txt
```

The date uses `git log -1 --format=%cI` (committer timestamp) instead of
wall-clock time, so builds from the same commit are byte-identical.

Install locally:
```bash
mkdir -p ~/.bin
cp bin/sky10-darwin-arm64 ~/.bin/sky10
```

### 3. Create GitHub release

```bash
gh release create v$VERSION \
  bin/sky10-darwin-arm64 \
  bin/sky10-darwin-amd64 \
  bin/sky10-linux-arm64 \
  bin/sky10-linux-amd64 \
  bin/checksums.txt \
  --title "v$VERSION — <short summary>" \
  --notes "<release notes>"
```

Generate release notes from `git log` since the previous tag. In the
commit list, link each commit hash to its GitHub URL using markdown:

```
- [`abc1234`](https://github.com/sky10ai/sky10/commit/abc1234) Commit message here
```

### 4. Restart daemon

```bash
sky10 daemon restart
```

### 5. Summary

Print upgrade instructions for the user:
```
curl -fsSL https://raw.githubusercontent.com/sky10ai/sky10/main/install.sh | bash
```

Or manual download:
```
# Download from GitHub releases
curl -fsSL https://github.com/sky10ai/sky10/releases/download/v$VERSION/sky10-darwin-arm64 -o ~/.bin/sky10
chmod +x ~/.bin/sky10
sky10 daemon restart
```
