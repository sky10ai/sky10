---
name: release
description: Cut a new sky10 release — bump versions, tag, build, GitHub release, update Homebrew tap
allowed-tools: Read, Edit, Write, Bash, Glob, Grep
---

# Release sky10

Cut a new release of sky10 (CLI + Cirrus). The version is passed as `$ARGUMENTS` (e.g. `/release 0.5.0`).

If no version is provided, ask the user what version to release.

## CRITICAL: Order of operations

**Every step MUST happen in this exact order. Do NOT skip ahead.**

1. Bump ALL version strings first
2. Commit the bump
3. Push the commit
4. Tag the bump commit (NOT an earlier commit)
5. Push the tag
6. Build the binary (from the tagged commit)
7. Install locally
8. Create GitHub release with the binary
9. Update Homebrew tap with the correct checksum
10. Rebuild Cirrus
11. Restart Cirrus

If you tag before committing the bump, or build before tagging, or
upload before rebuilding — you will produce a broken release.

**NEVER modify a published release.** Once a tag is pushed and a GitHub
release is created with assets, that version is FINAL. Do not re-upload
binaries, do not delete and recreate tags, do not edit release assets.
If something is wrong (wrong binary, missing assets, bad checksum), cut
a new patch release (e.g. v0.26.1). Re-uploading breaks checksums for
anyone who already downloaded.

## Steps

### 1. Bump versions in source

Update the version string in these files (replace the OLD version with the NEW one):

- `cirrus/macos/project.yml` — THREE fields: `MARKETING_VERSION` (build settings), `CFBundleShortVersionString` (info.properties), and verify they match
- `cirrus/macos/cirrus/Info.plist` — `CFBundleShortVersionString` value (NOTE: xcodegen overwrites this from project.yml info.properties, so project.yml is the source of truth)

The CLI version comes from git tags via the Makefile — no hardcoded version to update.

The schema version (`pkg/fs/schema.go` `SchemaVersion`) is a DATA FORMAT version and should NOT be bumped during a release unless the storage format changed.

### 2. Commit and push

```bash
git add cirrus/macos/project.yml cirrus/macos/cirrus/Info.plist
git commit -m "Bump Cirrus to v$VERSION"
git push
```

### 3. Tag and push tag

The tag MUST point to the bump commit from step 2. Verify with
`git log --oneline -1` that HEAD is the bump commit before tagging.

```bash
git tag v$VERSION
git push origin v$VERSION
```

### 4. Build binaries (all platforms)

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
wall-clock time, so builds from the same commit are byte-identical. This
is verified by the `verify-release` GitHub Action.

Install locally immediately (BOTH paths — Cirrus finds bin/sky10 first):
```bash
cp bin/sky10-darwin-arm64 /opt/homebrew/bin/sky10
cp bin/sky10-darwin-arm64 bin/sky10
```

### 5. Create GitHub release

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

Generate release notes from `git log` since the previous tag. In the commit list, link each commit hash to its GitHub URL using markdown:

```
- [`abc1234`](https://github.com/sky10ai/sky10/commit/abc1234) Commit message here
```

### 6. Update Homebrew tap

The tap repo is at `~/Documents/projects/homebrew-tap`. If it doesn't exist, clone it:
```bash
git clone https://github.com/sky10ai/homebrew-tap.git ~/Documents/projects/homebrew-tap
```

Update these files using the checksum from step 4:

**`~/Documents/projects/homebrew-tap/Formula/sky10.rb`:**
- `version` field
- The darwin-arm64 `url` field — update the tag in the URL
- The darwin-arm64 `sha256` field — from checksums.txt
- Keep other platform entries as-is (they'll 404 but that's fine — TODO: add them later)

**`~/Documents/projects/homebrew-tap/Formula/sky10-cirrus.rb`:**
- The `tag:` value in the `url` line

Then commit and push. **NEVER force push the tap.** Force pushing
causes merge conflicts on machines that already pulled the old version.

```bash
cd ~/Documents/projects/homebrew-tap
git add Formula/sky10.rb Formula/sky10-cirrus.rb
git commit -m "Bump to v$VERSION"
git push origin main
```

### 7. Rebuild and restart local Cirrus

```bash
cd cirrus/macos
xcodegen generate
xcodebuild -project Cirrus.xcodeproj -scheme Cirrus -configuration Debug \
  -derivedDataPath .build/xcode "CODE_SIGNING_ALLOWED=NO"
```

Kill and relaunch:
```bash
pkill -f "Cirrus.app"; pkill -f "sky10 serve"; sleep 1
open .build/xcode/Build/Products/Debug/Cirrus.app
```

### 8. Summary

Print upgrade instructions for the user:
```
brew update && brew upgrade sky10
brew upgrade sky10-cirrus
cp -R /opt/homebrew/Cellar/sky10-cirrus/$VERSION/Cirrus.app /Applications/
```
