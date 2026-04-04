#!/bin/bash
set -euo pipefail

REPO="sky10ai/sky10"
BINARY="sky10"
INSTALL_DIR="$HOME/.bin"
LOG_DIR="/tmp/sky10"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin) OS="darwin" ;;
  linux)  OS="linux" ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64)  ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

ASSET="${BINARY}-${OS}-${ARCH}"
echo "Installing sky10 (${OS}/${ARCH})..."

# Get latest release tag
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$LATEST" ]; then
  echo "Failed to fetch latest release"
  exit 1
fi
echo "Latest release: ${LATEST}"

URL="https://github.com/${REPO}/releases/download/${LATEST}/${ASSET}"

# Download to temp file
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

echo "Downloading ${URL}..."
if ! curl -fsSL "$URL" -o "$TMP"; then
  echo "Download failed. Binary may not exist for ${OS}/${ARCH} yet."
  exit 1
fi
chmod +x "$TMP"

# Install binary
mkdir -p "$INSTALL_DIR"
mv "$TMP" "${INSTALL_DIR}/${BINARY}"

# Add ~/.bin to PATH if not already there
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
  SHELL_NAME=$(basename "$SHELL")
  case "$SHELL_NAME" in
    zsh)  RC="$HOME/.zshrc" ;;
    bash) RC="$HOME/.bashrc" ;;
    *)    RC="" ;;
  esac
  if [ -n "$RC" ]; then
    echo "export PATH=\"\$HOME/.bin:\$PATH\"" >> "$RC"
    echo "Added ~/.bin to PATH in $RC (restart your shell or run: source $RC)"
  else
    echo "Add ~/.bin to your PATH: export PATH=\"\$HOME/.bin:\$PATH\""
  fi
  export PATH="$INSTALL_DIR:$PATH"
fi

echo "sky10 ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"

# Install daemon as a system service (launchd on macOS, systemd on Linux)
echo ""
echo "Setting up background daemon..."
if "${INSTALL_DIR}/${BINARY}" daemon install; then
  echo ""
  echo "Daemon is running. Manage with:"
  echo "  sky10 daemon status   # check status"
  echo "  sky10 daemon restart  # restart"
  echo "  sky10 daemon stop     # stop"
  echo "  sky10 ui open         # open web UI"
  echo ""
  echo "Next steps:"
  echo "  sky10 invite          # invite another device"
  echo "  sky10 join <code>     # join an existing device"
else
  echo ""
  echo "Could not install daemon service. Start manually:"
  echo "  sky10 serve           # start the daemon"
  echo "  sky10 invite          # invite another device"
  echo "  sky10 join <code>     # join an existing device"
fi
