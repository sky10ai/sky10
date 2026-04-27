#!/usr/bin/env bash
# Vendors stablyai/agent-slack into external/agent-slack/<version>/.
#
# We don't download a release binary — we clone the source at the requested
# tag, run `bun build` to produce two single-file JS bundles (the agent-slack
# CLI plus a small hydrator that prints credentials.json with macOS Keychain
# values resolved), and copy the bundles into the repo. At runtime sky10 runs
# both via its managed bun.
#
# Usage:
#   scripts/vendor-agent-slack.sh                  # latest GitHub release
#   scripts/vendor-agent-slack.sh v0.8.5           # explicit tag

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR_ROOT="$REPO_ROOT/external/agent-slack"
WORK_ROOT="${TMPDIR:-/tmp}/agent-slack"

if ! command -v bun >/dev/null 2>&1; then
  echo "bun is required (https://bun.sh)" >&2
  exit 1
fi

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "Resolving latest release..."
  VERSION="$(
    curl -fsSL https://api.github.com/repos/stablyai/agent-slack/releases/latest \
      | grep -oE '"tag_name":[^,]*' \
      | head -1 \
      | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/'
  )"
  if [ -z "$VERSION" ]; then
    echo "Failed to resolve latest release" >&2
    exit 1
  fi
fi

case "$VERSION" in
  v*) ;;
  *) VERSION="v$VERSION" ;;
esac

WORK_DIR="$WORK_ROOT/$VERSION"
DEST_DIR="$VENDOR_ROOT/$VERSION"

echo "Vendoring agent-slack $VERSION"
echo "  source:  https://github.com/stablyai/agent-slack"
echo "  workdir: $WORK_DIR"
echo "  dest:    $DEST_DIR"

mkdir -p "$WORK_ROOT"

if [ ! -d "$WORK_DIR/.git" ]; then
  rm -rf "$WORK_DIR"
  git clone --depth 1 --branch "$VERSION" \
    https://github.com/stablyai/agent-slack.git "$WORK_DIR"
else
  echo "Reusing existing checkout at $WORK_DIR"
fi

cd "$WORK_DIR"
echo "Installing deps (bun install --frozen-lockfile)..."
bun install --frozen-lockfile

echo "Writing hydrator companion..."
cat > "$WORK_DIR/src/sky10-dump-credentials.ts" <<'EOF'
// Companion to agent-slack vendored by sky10.
// Imports agent-slack's own loadCredentials() so xoxc/xoxd hydrated from
// the macOS Keychain are returned to stdout as JSON. The agent-slack CLI
// itself never prints raw credentials; this hydrator does.

import { loadCredentials } from "./auth/store.ts";

async function main(): Promise<void> {
  const creds = await loadCredentials();
  process.stdout.write(JSON.stringify(creds));
}

main().catch((error) => {
  process.stderr.write(
    `sky10-dump-credentials failed: ${error instanceof Error ? error.message : String(error)}\n`,
  );
  process.exit(1);
});
EOF

mkdir -p "$WORK_DIR/_sky10_bundle"

echo "Bundling agent-slack CLI..."
bun build src/index.ts \
  --target=bun \
  --outfile "$WORK_DIR/_sky10_bundle/agent-slack.js"

echo "Bundling hydrator..."
bun build src/sky10-dump-credentials.ts \
  --target=bun \
  --outfile "$WORK_DIR/_sky10_bundle/dump-credentials.js"

echo "Copying to $DEST_DIR..."
rm -rf "$DEST_DIR"
mkdir -p "$DEST_DIR"
cp "$WORK_DIR/_sky10_bundle/agent-slack.js"     "$DEST_DIR/agent-slack.js"
cp "$WORK_DIR/_sky10_bundle/dump-credentials.js" "$DEST_DIR/dump-credentials.js"
[ -f "$WORK_DIR/LICENSE" ] && cp "$WORK_DIR/LICENSE" "$DEST_DIR/LICENSE"

cat > "$DEST_DIR/PROVENANCE.txt" <<EOF
Source:    https://github.com/stablyai/agent-slack
Tag:       $VERSION
Built:     $(date -u +"%Y-%m-%dT%H:%M:%SZ")
Bundler:   bun build --target=bun
By:        scripts/vendor-agent-slack.sh
License:   MIT (see LICENSE)

agent-slack.js     full CLI bundle, runs with managed bun
dump-credentials.js  sky10 hydrator that prints loadCredentials() output
EOF

echo
echo "Done."
echo "  $DEST_DIR/agent-slack.js"
echo "  $DEST_DIR/dump-credentials.js"
echo
echo "Smoke test:"
echo "  bun $DEST_DIR/agent-slack.js auth --help"
echo "  bun $DEST_DIR/dump-credentials.js   # prints credentials.json (or {version:1, workspaces:[]} when empty)"
