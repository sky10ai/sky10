#!/bin/bash
set -euo pipefail

REPO="sky10ai/sky10"
BINARY="sky10"
INSTALL_DIR="/usr/local/bin"

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

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "${INSTALL_DIR}/${BINARY}"
else
  echo "Need sudo to install to ${INSTALL_DIR}"
  sudo mv "$TMP" "${INSTALL_DIR}/${BINARY}"
fi

echo ""
echo "sky10 ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Get started:"
echo "  sky10 serve    # start the daemon"
echo "  sky10 invite   # invite another device"
