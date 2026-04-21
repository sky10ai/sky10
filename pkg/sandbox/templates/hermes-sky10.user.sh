#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
export XDG_RUNTIME_DIR="/run/user/{{.UID}}"
export HERMES_HOME="${HOME}/.hermes"
export HERMES_MODEL="{{.Param.model}}"

SHARED_DIR="/shared"
WORKSPACE_DIR="${SHARED_DIR}/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
STATE_DIR="${HERMES_HOME}/.sky10-lima"
SENTINEL="${STATE_DIR}/initialized-v1"
HELPER="${HOME}/.local/bin/hermes-shared"
BRIDGE_INSTALL="${HOME}/.local/bin/hermes-sky10-bridge"
BRIDGE_ASSET="${SANDBOX_STATE_DIR}/hermes-sky10-bridge.py"
BRIDGE_CONFIG="${SANDBOX_STATE_DIR}/bridge.json"
BRIDGE_ENV="${STATE_DIR}/bridge.env"
SKY10_INVITE_PATH="${SANDBOX_STATE_DIR}/join.json"
SKY10_RECONNECT_HELPER="${HOME}/.bin/sky10-managed-reconnect"
UNIT_DIR="${HOME}/.config/systemd/user"
SKY10_UNIT="${UNIT_DIR}/sky10.service"
GATEWAY_UNIT="${UNIT_DIR}/sky10-hermes-gateway.service"
BRIDGE_UNIT="${UNIT_DIR}/sky10-hermes-bridge.service"

mkdir -p "${STATE_DIR}"
mkdir -p "${HOME}/.bin"
mkdir -p "${HOME}/.local/bin"
mkdir -p "${UNIT_DIR}"
mkdir -p "${SHARED_DIR}"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}"

emit_progress() {
  local event="$1"
  local id="$2"
  local summary="$3"
  printf 'SKY10_PROGRESS {"event":"%s","id":"%s","summary":"%s"}\n' "${event}" "${id}" "${summary}"
}

curl4() {
  curl -4 --retry 5 --retry-delay 2 --retry-connrefused -fsSL "$@"
}

wait_for_sky10() {
  timeout 120s bash -lc 'until curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; do sleep 2; done'
}

install_sky10() {
  local arch latest asset tmp

  case "$(uname -m)" in
    x86_64|amd64)
      arch="amd64"
      ;;
    arm64|aarch64)
      arch="arm64"
      ;;
    *)
      echo >&2 "unsupported sky10 guest architecture: $(uname -m)"
      return 1
      ;;
  esac

  latest="$(
    curl4 https://api.github.com/repos/sky10ai/sky10/releases/latest \
      | python3 -c 'import json, sys; print(json.load(sys.stdin)["tag_name"])'
  )"
  if [ -z "${latest}" ]; then
    echo >&2 "failed to resolve latest sky10 release"
    return 1
  fi

  asset="sky10-linux-${arch}"
  tmp="$(mktemp)"
  curl4 -o "${tmp}" "https://github.com/sky10ai/sky10/releases/download/${latest}/${asset}"
  install -m 755 "${tmp}" "${HOME}/.bin/sky10"
  rm -f "${tmp}"
}

ensure_guest_sky10_binary() {
  if ! command -v sky10 >/dev/null 2>&1; then
    install_sky10
  fi
}

install_guest_reconnect_helper() {
  cat > "${SKY10_RECONNECT_HELPER}" <<'EOF'
#!/bin/bash
set -u

JOIN_PATH="/sandbox-state/join.json"
LOCAL_RPC="http://127.0.0.1:9101/rpc"

if [ ! -f "${JOIN_PATH}" ]; then
  exit 0
fi

mapfile -t join_info < <(
  python3 - "${JOIN_PATH}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as fh:
    payload = json.load(fh)

print((payload.get("host_rpc_url") or "").strip())
print((payload.get("sandbox_slug") or "").strip())
PY
)
host_rpc_url="${join_info[0]:-}"
sandbox_slug="${join_info[1]:-}"

if [ -z "${host_rpc_url}" ] || [ -z "${sandbox_slug}" ]; then
  exit 0
fi

guest_ip="$(ip -4 addr show dev lima0 | awk '/inet / {sub(/\/.*/, "", $2); print $2; exit}')"

for _ in $(seq 1 20); do
  payload="$(
    curl -fsS "${LOCAL_RPC}" -H 'Content-Type: application/json' \
      -d '{"jsonrpc":"2.0","method":"skylink.status","params":{},"id":1}' \
      | python3 - "${sandbox_slug}" "${guest_ip}" <<'PY'
import json
import sys

slug = sys.argv[1]
guest_ip = sys.argv[2]
resp = json.load(sys.stdin)
result = resp.get("result") or {}
peer_id = (result.get("peer_id") or "").strip()
addrs = result.get("addrs") or []
if not peer_id or not addrs:
    raise SystemExit(1)

print(json.dumps({
    "jsonrpc": "2.0",
    "method": "sandbox.reconnectGuest",
    "params": {
        "slug": slug,
        "ip_address": guest_ip,
        "peer_id": peer_id,
        "multiaddrs": addrs,
    },
    "id": 1,
}))
PY
  )" && curl -fsS "${host_rpc_url}" -H 'Content-Type: application/json' -d "${payload}" >/dev/null 2>&1 && exit 0
  sleep 2
done

exit 0
EOF
  chmod 755 "${SKY10_RECONNECT_HELPER}"
}

ensure_guest_sky10() {
  ensure_guest_sky10_binary
  install_guest_reconnect_helper

  if curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; then
    emit_progress skip guest.sky10.start "Guest sky10 already running."
    return 0
  fi

  emit_progress begin guest.sky10.start "Starting guest sky10..."
  cat > "${SKY10_UNIT}" <<EOF
[Unit]
Description=sky10 Daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/env sky10 serve
ExecStartPost=%h/.bin/sky10-managed-reconnect
Restart=always
RestartSec=2
WorkingDirectory=${HOME}
Environment=HOME=${HOME}
Environment=PATH=${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=default.target
EOF

  systemctl --user daemon-reload
  systemctl --user enable sky10.service
  systemctl --user restart sky10.service || systemctl --user start sky10.service

  wait_for_sky10
  emit_progress end guest.sky10.start "Guest sky10 running."
}

ensure_shared_env() {
  if [ ! -f "${SANDBOX_STATE_DIR}/.env" ]; then
    cat > "${SANDBOX_STATE_DIR}/.env" <<'EOF'
# Optional provider keys for Hermes inside Lima.
# Host secrets named ANTHROPIC_API_KEY/anthropic, OPENAI_API_KEY/openai,
# and OPENROUTER_API_KEY/openrouter merge in automatically when available.
# Hermes reads ~/.hermes/.env, which is linked to this sandbox state file.

OPENAI_API_KEY=
ANTHROPIC_API_KEY=
OPENROUTER_API_KEY=
EOF
    chmod 600 "${SANDBOX_STATE_DIR}/.env"
  fi
}

shared_env_has_value() {
  local key="$1"
  awk -F= -v key="$key" '
    index($0, key "=") == 1 {
      value = substr($0, index($0, "=") + 1)
      if (length(value) > 0) {
        found = 1
      }
    }
    END {
      exit found ? 0 : 1
    }
  ' "${SANDBOX_STATE_DIR}/.env"
}

set_shared_env_value() {
  local key="$1"
  local value="$2"
  local tmp

  tmp="$(mktemp "${SANDBOX_STATE_DIR}/.env.tmp.XXXXXX")"
  awk -v key="$key" -v value="$value" '
    BEGIN { replaced = 0 }
    index($0, key "=") == 1 {
      if (!replaced) {
        print key "=" value
        replaced = 1
      }
      next
    }
    { print }
    END {
      if (!replaced) {
        print key "=" value
      }
    }
  ' "${SANDBOX_STATE_DIR}/.env" > "${tmp}"
  chmod 600 "${tmp}"
  mv "${tmp}" "${SANDBOX_STATE_DIR}/.env"
}

guest_env_line_is_example_default() {
  local line="$1"
  local example_env="${HERMES_HOME}/hermes-agent/.env.example"

  if [ ! -f "${example_env}" ]; then
    return 1
  fi

  grep -Fx -- "${line}" "${example_env}" >/dev/null 2>&1
}

merge_guest_env_into_shared() {
  local guest_env="${HERMES_HOME}/.env"

  if [ ! -f "${guest_env}" ] || [ -L "${guest_env}" ]; then
    return 0
  fi

  while IFS= read -r line || [ -n "${line}" ]; do
    local key
    local value

    case "${line}" in
      ""|\#*)
        continue
        ;;
    esac

    if [[ "${line}" != *=* ]]; then
      continue
    fi

    key="${line%%=*}"
    value="${line#*=}"

    if [ -z "${key}" ] || [ -z "${value}" ]; then
      continue
    fi

    if guest_env_line_is_example_default "${line}"; then
      continue
    fi

    if shared_env_has_value "${key}"; then
      continue
    fi

    set_shared_env_value "${key}" "${value}"
  done < "${guest_env}"
}

link_hermes_env() {
  mkdir -p "${HERMES_HOME}"

  if [ -f "${HERMES_HOME}/.env" ] && [ ! -L "${HERMES_HOME}/.env" ]; then
    merge_guest_env_into_shared
    rm -f "${HERMES_HOME}/.env"
  fi

  ln -sfn "${SANDBOX_STATE_DIR}/.env" "${HERMES_HOME}/.env"
}

shared_agent_file_is_seed() {
  local source="$1"
  local base

  if [ ! -f "${source}" ]; then
    return 1
  fi

  base="$(basename "${source}")"
  case "${base}" in
    MEMORY.md)
      grep -Fqx -- "# Memory" "${source}" &&
        grep -Fqx -- "Use this file for durable facts that should survive model, runtime, and machine changes." "${source}" &&
        grep -Fqx -- "- Project conventions worth carrying forward" "${source}" &&
        grep -Fqx -- "- Recurring tasks or preferences" "${source}" &&
        grep -Fqx -- "- Useful environment facts" "${source}"
      ;;
    USER.md)
      ! grep -q -- '[^[:space:]]' "${source}"
      ;;
    SOUL.md)
      grep -Fqx -- "# Soul" "${source}" &&
        grep -Fq -- "This file defines the durable identity for " "${source}" &&
        grep -Fqx -- "## Role" "${source}" &&
        grep -Fq -- "Describe who this agent is and what it should optimize for in the " "${source}" &&
        grep -Fqx -- "## Tone" "${source}" &&
        grep -Fqx -- "Describe how the agent should communicate." "${source}" &&
        grep -Fqx -- "## Boundaries" "${source}" &&
        grep -Fqx -- "Describe what the agent should avoid, when it should escalate, and what humans own." "${source}"
      ;;
    *)
      return 1
      ;;
  esac
}

preserve_guest_agent_path() {
  local source="$1"
  local target="$2"
  local rel_path
  local backup_path

  if [ ! -e "${target}" ] || [ -L "${target}" ]; then
    return 0
  fi

  if [ -f "${target}" ]; then
    if [ -f "${source}" ] && cmp -s "${source}" "${target}" >/dev/null 2>&1; then
      rm -f "${target}"
      return 0
    fi

    if [ -f "${source}" ] && shared_agent_file_is_seed "${source}"; then
      cp "${target}" "${source}"
      rm -f "${target}"
      return 0
    fi
  fi

  rel_path="${target#${HERMES_HOME}/}"
  if [ "${rel_path}" = "${target}" ]; then
    rel_path="$(basename "${target}")"
  fi
  backup_path="${SANDBOX_STATE_DIR}/guest-profile-backup/${rel_path}"
  mkdir -p "$(dirname "${backup_path}")"
  rm -rf "${backup_path}"
  cp -R "${target}" "${backup_path}"
  rm -rf "${target}"
}

link_agent_file() {
  local source="$1"
  local target="$2"

  mkdir -p "$(dirname "${target}")"
  preserve_guest_agent_path "${source}" "${target}"
  ln -sfn "${source}" "${target}"
}

link_hermes_profile() {
  mkdir -p "${HERMES_HOME}/memories"
  link_agent_file "${SHARED_DIR}/SOUL.md" "${HERMES_HOME}/SOUL.md"
  link_agent_file "${SHARED_DIR}/MEMORY.md" "${HERMES_HOME}/memories/MEMORY.md"
  link_agent_file "${SHARED_DIR}/USER.md" "${HERMES_HOME}/memories/USER.md"
}

write_helper() {
  cat > "${HELPER}" <<'EOF'
#!/bin/bash
set -e
export PATH="${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
cd /shared/workspace
exec hermes "$@"
EOF
  chmod 755 "${HELPER}"
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

write_gateway_unit() {
  cat > "${GATEWAY_UNIT}" <<EOF
[Unit]
Description=sky10 Hermes Gateway
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/env hermes gateway run
Restart=always
RestartSec=5
WorkingDirectory=/shared/workspace
EnvironmentFile=-%h/.hermes/.env
EnvironmentFile=-%h/.hermes/.sky10-lima/bridge.env
Environment=HOME=${HOME}
Environment=MESSAGING_CWD=/shared/workspace
Environment=PATH=${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=default.target
EOF
}

write_bridge_unit() {
  cat > "${BRIDGE_UNIT}" <<EOF
[Unit]
Description=sky10 Hermes Host Bridge
After=network-online.target sky10-hermes-gateway.service
Wants=network-online.target sky10-hermes-gateway.service

[Service]
ExecStart=%h/.local/bin/hermes-sky10-bridge
Restart=always
RestartSec=5
WorkingDirectory=/shared/workspace
EnvironmentFile=-%h/.hermes/.sky10-lima/bridge.env
Environment=HOME=${HOME}
Environment=PATH=${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin
Environment=PYTHONUNBUFFERED=1

[Install]
WantedBy=default.target
EOF
}

wait_for_hermes_api() {
  timeout 180s bash -lc 'until curl -fsS http://127.0.0.1:8642/health >/dev/null 2>&1; do sleep 2; done'
}

enable_host_chat_bridge() {
  if [ ! -f "${BRIDGE_CONFIG}" ]; then
    emit_progress skip guest.hermes.bridge.start "Hermes bridge not configured."
    return 0
  fi

  emit_progress begin guest.hermes.bridge.start "Starting Hermes bridge..."
  install_bridge_asset
  write_bridge_env
  write_gateway_unit
  write_bridge_unit

  systemctl --user daemon-reload
  systemctl --user enable sky10-hermes-gateway.service sky10-hermes-bridge.service
  systemctl --user restart sky10-hermes-gateway.service || systemctl --user start sky10-hermes-gateway.service
  wait_for_hermes_api
  systemctl --user restart sky10-hermes-bridge.service || systemctl --user start sky10-hermes-bridge.service
  emit_progress end guest.hermes.bridge.start "Hermes bridge ready."
}

ensure_shared_env

if [ ! -f "${SENTINEL}" ]; then
  emit_progress begin guest.hermes.install "Installing Hermes..."
  curl4 https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --skip-setup
  touch "${SENTINEL}"
  emit_progress end guest.hermes.install "Hermes installed."
else
  emit_progress skip guest.hermes.install "Hermes already installed."
fi

emit_progress begin guest.hermes.configure "Configuring Hermes..."
link_hermes_env
link_hermes_profile
write_helper
ensure_guest_sky10

if command -v hermes >/dev/null 2>&1; then
  hermes config set terminal.backend local || true
  hermes config set terminal.cwd /shared/workspace || true
  if [ -n "${HERMES_MODEL}" ]; then
    hermes config set model "${HERMES_MODEL}" || true
  fi
fi

link_hermes_env
link_hermes_profile
emit_progress end guest.hermes.configure "Hermes configured."
enable_host_chat_bridge
