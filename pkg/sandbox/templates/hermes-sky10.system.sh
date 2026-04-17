#!/bin/bash
set -eux -o pipefail

export HOME=/root
export DEBIAN_FRONTEND=noninteractive

STATE_DIR=/var/lib/hermes-lima
SENTINEL="${STATE_DIR}/hermes-system-v1"
APT_FLAGS=(-o Acquire::ForceIPv4=true -o Acquire::Retries=3)

mkdir -p "${STATE_DIR}"
mkdir -p /shared
mkdir -p /shared/workspace
mkdir -p /sandbox-state

emit_progress() {
  local event="$1"
  local id="$2"
  local summary="$3"
  printf 'SKY10_PROGRESS {"event":"%s","id":"%s","summary":"%s"}\n' "${event}" "${id}" "${summary}"
}

if [ ! -f "${SENTINEL}" ]; then
  emit_progress begin guest.system.packages "Installing system packages..."
  apt-get "${APT_FLAGS[@]}" update -y
  apt-get "${APT_FLAGS[@]}" install -y \
    ca-certificates \
    curl \
    dbus-user-session \
    ffmpeg \
    git \
    python3 \
    ripgrep \
    xz-utils

  loginctl enable-linger "{{.User}}" || true

  touch "${SENTINEL}"
  emit_progress end guest.system.packages "System packages installed."
else
  emit_progress skip guest.system.packages "System packages already installed."
fi
