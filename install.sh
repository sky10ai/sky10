#!/bin/bash
set -euo pipefail

REPO="sky10ai/sky10"
BINARY="sky10"
INSTALL_DIR="/usr/local/bin"
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
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "${INSTALL_DIR}/${BINARY}"
else
  echo "Need sudo to install to ${INSTALL_DIR}"
  sudo mv "$TMP" "${INSTALL_DIR}/${BINARY}"
fi

echo "sky10 ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"

# On Debian/Ubuntu: install systemd service for background daemon
if [ "$OS" = "linux" ] && command -v dpkg >/dev/null 2>&1 && command -v systemctl >/dev/null 2>&1; then
  echo ""
  echo "Debian-based system detected — setting up systemd service..."

  mkdir -p "$LOG_DIR"

  SERVICE_FILE="/etc/systemd/system/sky10.service"
  SUDO=""
  if [ "$(id -u)" -ne 0 ]; then
    SUDO="sudo"
  fi

  $SUDO tee "$SERVICE_FILE" >/dev/null <<UNIT
[Unit]
Description=sky10 daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BINARY} serve
Restart=on-failure
RestartSec=5
User=$(whoami)
Environment=HOME=$(eval echo ~$(whoami))
StandardOutput=append:${LOG_DIR}/daemon.log
StandardError=append:${LOG_DIR}/daemon.log

[Install]
WantedBy=multi-user.target
UNIT

  $SUDO systemctl daemon-reload
  $SUDO systemctl enable sky10
  $SUDO systemctl restart sky10

  echo "sky10 service started"
  echo ""
  echo "Logs:    tail -f ${LOG_DIR}/daemon.log"
  echo "Status:  systemctl status sky10"
  echo "Restart: sudo systemctl restart sky10"
else
  echo ""
  echo "Get started:"
  echo "  sky10 serve    # start the daemon"
  echo "  sky10 invite   # invite another device"
fi
