#!/bin/bash
set -euo pipefail

if [ "${SKY10_DOCKER_DEBUG:-}" = "1" ]; then
  set -x
fi

export HOME=/root
export PATH="${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
export HERMES_HOME="${HOME}/.hermes"
export SKY10_HOME="${HOME}/.sky10"
export SKY10_RUNTIME_DIR="/run/sky10"

SHARED_DIR="/shared"
WORKSPACE_DIR="${SHARED_DIR}/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
STATE_DIR="${HERMES_HOME}/.sky10-lima"
BRIDGE_INSTALL="/usr/local/bin/hermes-sky10-bridge"
BRIDGE_ASSET="${SANDBOX_STATE_DIR}/hermes-sky10-bridge.py"
BRIDGE_CONFIG="${SANDBOX_STATE_DIR}/bridge.json"
BRIDGE_ENV="${STATE_DIR}/bridge.env"

mkdir -p "${STATE_DIR}"
mkdir -p "${HOME}/.bin"
mkdir -p "${HOME}/.local/bin"
mkdir -p "${SKY10_HOME}"
mkdir -p "${SKY10_RUNTIME_DIR}"
mkdir -p "${SHARED_DIR}"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}"
mkdir -p "${HERMES_HOME}/memories"

# Container restarts can leave runtime files whose PIDs collide with new processes.
rm -f "${SKY10_RUNTIME_DIR}/daemon.pid" "${SKY10_RUNTIME_DIR}/sky10.sock"

wait_for_sky10() {
  timeout 120s bash -lc 'until curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; do sleep 2; done'
}

wait_for_hermes_api() {
  timeout 180s bash -lc 'until curl -fsS http://127.0.0.1:8642/health >/dev/null 2>&1; do sleep 2; done'
}

source_env_file() {
  local env_file="$1"
  local restore_xtrace=0

  if [ ! -f "${env_file}" ]; then
    return 0
  fi

  case "$-" in
    *x*)
      restore_xtrace=1
      set +x
      ;;
  esac

  . "${env_file}"

  if [ "${restore_xtrace}" -eq 1 ]; then
    set -x
  fi
}

ensure_shared_env() {
  if [ ! -f "${SANDBOX_STATE_DIR}/.env" ]; then
    cat > "${SANDBOX_STATE_DIR}/.env" <<'EOF'
OPENAI_API_KEY=
ANTHROPIC_API_KEY=
OPENROUTER_API_KEY=
EOF
    chmod 600 "${SANDBOX_STATE_DIR}/.env"
  fi
}

link_hermes_env() {
  ensure_shared_env
  ln -sfn "${SANDBOX_STATE_DIR}/.env" "${HERMES_HOME}/.env"
}

link_hermes_profile() {
  ln -sfn "${SHARED_DIR}/SOUL.md" "${HERMES_HOME}/SOUL.md"
  ln -sfn "${SHARED_DIR}/MEMORY.md" "${HERMES_HOME}/memories/MEMORY.md"
  ln -sfn "${SHARED_DIR}/USER.md" "${HERMES_HOME}/memories/USER.md"
}

install_bridge_asset() {
  if [ ! -f "${BRIDGE_ASSET}" ]; then
    echo >&2 "bundled Hermes bridge not found at ${BRIDGE_ASSET}"
    return 1
  fi
  install -m 755 "${BRIDGE_ASSET}" "${BRIDGE_INSTALL}"
}

write_bridge_env() {
  if [ ! -f "${BRIDGE_CONFIG}" ]; then
    return 0
  fi

  python3 - "${BRIDGE_ENV}" <<'PY'
import os
import secrets
import sys

env_path = sys.argv[1]
api_key = ""

if os.path.exists(env_path):
    with open(env_path, "r", encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if line.startswith("API_SERVER_KEY="):
                api_key = line.split("=", 1)[1]
                break

if not api_key:
    api_key = secrets.token_urlsafe(32)

lines = [
    "API_SERVER_ENABLED=true",
    "API_SERVER_HOST=127.0.0.1",
    "API_SERVER_PORT=8642",
    f"API_SERVER_KEY={api_key}",
    "API_SERVER_MODEL_NAME=hermes-agent",
    "SKY10_BRIDGE_CONFIG_PATH=/sandbox-state/bridge.json",
]

with open(env_path, "w", encoding="utf-8") as fh:
    fh.write("\n".join(lines) + "\n")
PY
  chmod 600 "${BRIDGE_ENV}"
}

load_env_files() {
  set -a
  source_env_file "${HERMES_HOME}/.env"
  source_env_file "${BRIDGE_ENV}"
  set +a
  export MESSAGING_CWD=/shared/workspace
}

configure_hermes() {
  if command -v hermes >/dev/null 2>&1; then
    hermes config set terminal.backend local || true
    hermes config set terminal.cwd /shared/workspace || true
    hermes config set auxiliary.vision.provider main || true
    if [ -n "${HERMES_MODEL:-}" ]; then
      hermes config set model "${HERMES_MODEL}" || true
    fi
  fi
}

cleanup() {
  if [ -n "${bridge_pid:-}" ]; then
    kill "${bridge_pid}" >/dev/null 2>&1 || true
  fi
  if [ -n "${gateway_pid:-}" ]; then
    kill "${gateway_pid}" >/dev/null 2>&1 || true
  fi
  if [ -n "${sky10_pid:-}" ]; then
    kill "${sky10_pid}" >/dev/null 2>&1 || true
  fi
  sleep 2
  if [ -n "${bridge_pid:-}" ] && kill -0 "${bridge_pid}" >/dev/null 2>&1; then
    kill -9 "${bridge_pid}" >/dev/null 2>&1 || true
  fi
  if [ -n "${gateway_pid:-}" ] && kill -0 "${gateway_pid}" >/dev/null 2>&1; then
    kill -9 "${gateway_pid}" >/dev/null 2>&1 || true
  fi
  if [ -n "${sky10_pid:-}" ] && kill -0 "${sky10_pid}" >/dev/null 2>&1; then
    kill -9 "${sky10_pid}" >/dev/null 2>&1 || true
  fi
}

trap cleanup EXIT INT TERM

link_hermes_env
link_hermes_profile
configure_hermes

sky10 serve >/tmp/sky10.log 2>&1 &
sky10_pid=$!
wait_for_sky10

if [ -f "${BRIDGE_CONFIG}" ]; then
  install_bridge_asset
  write_bridge_env
  load_env_files

  hermes gateway run >/tmp/hermes-gateway.log 2>&1 &
  gateway_pid=$!
  wait_for_hermes_api

  "${BRIDGE_INSTALL}" >/tmp/hermes-bridge.log 2>&1 &
  bridge_pid=$!
fi

wait_pids=("${sky10_pid}")
if [ -n "${gateway_pid:-}" ]; then
  wait_pids+=("${gateway_pid}")
fi
if [ -n "${bridge_pid:-}" ]; then
  wait_pids+=("${bridge_pid}")
fi

wait -n "${wait_pids[@]}"
