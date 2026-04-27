#!/bin/bash
set -eux -o pipefail

export HOME=/root
export DEBIAN_FRONTEND=noninteractive

STATE_DIR=/var/lib/hermes-lima-docker
SENTINEL="${STATE_DIR}/docker-system-v1"
APT_FLAGS=(-o Acquire::ForceIPv4=true -o Acquire::Retries=5 -o Acquire::http::Timeout=30)

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

apt_retry() {
  local attempt
  for attempt in 1 2 3; do
    if "$@"; then
      return 0
    fi
    if [ "${attempt}" -lt 3 ]; then
      apt-get "${APT_FLAGS[@]}" update -y || true
      sleep $((attempt * 5))
    fi
  done
  "$@"
}

if [ ! -f "${SENTINEL}" ]; then
  emit_progress begin guest.system.packages "Installing Docker runtime..."
  apt_retry apt-get "${APT_FLAGS[@]}" update -y
  apt_retry apt-get "${APT_FLAGS[@]}" install -y \
    ca-certificates \
    curl \
    gnupg

  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc

  . /etc/os-release
  arch="$(dpkg --print-architecture)"
  echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list

  apt_retry apt-get "${APT_FLAGS[@]}" update -y
  apt_retry apt-get "${APT_FLAGS[@]}" install -y \
    containerd.io \
    docker-buildx-plugin \
    docker-ce \
    docker-ce-cli \
    docker-compose-plugin

  systemctl enable docker
  systemctl restart docker
  usermod -aG docker "{{.User}}" || true

  touch "${SENTINEL}"
  emit_progress end guest.system.packages "Docker runtime installed."
else
  emit_progress skip guest.system.packages "Docker runtime already installed."
fi
