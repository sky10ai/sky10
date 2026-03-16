---
name: release
description: Cut a new sky10 release — bump versions, tag, build, GitHub release, update Homebrew tap
allowed-tools: Read, Edit, Write, Bash, Glob, Grep
---

# Release sky10

Cut a new release of sky10 (CLI + Cirrus). The version is passed as `$ARGUMENTS` (e.g. `/release 0.5.0`).

If no version is provided, ask the user what version to release.

## Steps

### 1. Bump versions in source

Update the version string in these files (replace the OLD version with the NEW one):

- `cirrus/macos/project.yml` — `MARKETING_VERSION` and `CFBundleShortVersionString` fields
- `cirrus/macos/cirrus/Info.plist` — `CFBundleShortVersionString` value

The CLI version comes from git tags via the Makefile — no hardcoded version to update.

The schema version (`pkg/fs/schema.go` `SchemaVersion`) is a DATA FORMAT version and should NOT be bumped during a release unless the storage format changed.

### 2. Commit and push

```bash
git add cirrus/macos/project.yml cirrus/macos/cirrus/Info.plist
git commit -m "Bump Cirrus to v$VERSION"
git push
```

### 3. Tag and push tag

```bash
git tag v$VERSION
git push origin v$VERSION
```

### 4. Build cross-platform binaries

```bash
rm -f bin/sky10-*
make platforms checksums
```

Verify checksums output shows 4 binaries with unique hashes.

### 5. Create GitHub release

```bash
gh release create v$VERSION \
  bin/sky10-darwin-amd64 bin/sky10-darwin-arm64 \
  bin/sky10-linux-amd64 bin/sky10-linux-arm64 \
  bin/checksums.txt \
  --title "v$VERSION — <short summary>" \
  --notes "<release notes>"
```

Generate release notes from `git log` since the previous tag.

### 6. Update Homebrew tap

The tap repo is at `/tmp/homebrew-tap`. If it doesn't exist, clone it:
```bash
git clone https://github.com/sky10ai/homebrew-tap.git /tmp/homebrew-tap
```

Update these files using the checksums from step 4:

**`/tmp/homebrew-tap/Formula/sky10.rb`:**
- `version` field
- All 4 `url` fields (darwin-arm64, darwin-amd64, linux-arm64, linux-amd64) — update the tag in the URL
- All 4 `sha256` fields — from checksums.txt

**`/tmp/homebrew-tap/Formula/sky10-cirrus.rb`:**
- The `tag:` value in the `url` line

Then commit and push:
```bash
cd /tmp/homebrew-tap
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
pkill -f "Cirrus.app"; pkill -f "sky10 fs serve"; sleep 1
open .build/xcode/Build/Products/Debug/Cirrus.app
```

### 8. Summary

Print upgrade instructions for the user:
```
brew update && brew upgrade sky10
brew upgrade sky10-cirrus
cp -R /opt/homebrew/Cellar/sky10-cirrus/$VERSION/Cirrus.app /Applications/
```
