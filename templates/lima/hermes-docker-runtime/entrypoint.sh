#!/bin/bash
set -eux -o pipefail

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
SKY10_RECONNECT_HELPER="/usr/local/bin/sky10-managed-reconnect"

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

guest_ip="${SKY10_GUEST_IP:-}"

for _ in $(seq 1 20); do
  payload="$(
    curl -fsS "${LOCAL_RPC}" -H 'Content-Type: application/json' \
      -d '{"jsonrpc":"2.0","method":"skylink.status","params":{},"id":1}' \
      | python3 - "${sandbox_slug}" "${guest_ip}" <<'PY'
import json
import sys

slug = sys.argv[1]
guest_ip = (sys.argv[2] or "").strip()
try:
    resp = json.load(sys.stdin)
except Exception:
    raise SystemExit(1)
result = resp.get("result") or {}
peer_id = (result.get("peer_id") or "").strip()
addrs = result.get("addrs") or []
if not peer_id or not addrs:
    raise SystemExit(1)

params = {
    "slug": slug,
    "peer_id": peer_id,
    "multiaddrs": addrs,
}
if guest_ip:
    params["ip_address"] = guest_ip

print(json.dumps({
    "jsonrpc": "2.0",
    "method": "sandbox.reconnectGuest",
    "params": params,
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
  if [ -f "${HERMES_HOME}/.env" ]; then
    . "${HERMES_HOME}/.env"
  fi
  if [ -f "${BRIDGE_ENV}" ]; then
    . "${BRIDGE_ENV}"
  fi
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
}

trap cleanup EXIT INT TERM

link_hermes_env
link_hermes_profile
configure_hermes
install_guest_reconnect_helper

sky10 serve >/tmp/sky10.log 2>&1 &
sky10_pid=$!
wait_for_sky10
"${SKY10_RECONNECT_HELPER}" || true

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
