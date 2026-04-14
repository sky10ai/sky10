#!/bin/bash
set -eux -o pipefail

export HOME=/root
export DEBIAN_FRONTEND=noninteractive

STATE_DIR=/var/lib/hermes-lima
SENTINEL="${STATE_DIR}/hermes-system-v1"
APT_FLAGS=(-o Acquire::ForceIPv4=true -o Acquire::Retries=3)

mkdir -p "${STATE_DIR}"
mkdir -p /shared

if [ ! -f "${SENTINEL}" ]; then
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
fi
