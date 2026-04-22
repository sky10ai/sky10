#!/bin/bash
set -eux -o pipefail

export HOME=/root
export PATH="${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
export DISPLAY=:99
export PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

OPENCLAW_DIR="${HOME}/.openclaw"
WORKSPACE_DIR="/shared/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
PLUGIN_DIR="${SANDBOX_STATE_DIR}/plugins/openclaw-sky10-channel"
SKY10_RECONNECT_HELPER="/usr/local/bin/sky10-managed-reconnect"

export PLUGIN_DIR

mkdir -p "${OPENCLAW_DIR}/agents/main/sessions"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}"

wait_for_sky10() {
  timeout 120s bash -lc 'until curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; do sleep 2; done'
}

wait_for_openclaw_agent() {
  timeout 120s bash -lc "until curl -fsS http://127.0.0.1:9101/rpc -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"method\":\"agent.list\",\"params\":{},\"id\":1}' | grep -F '\"name\":\"${OPENCLAW_AGENT_NAME}\"' >/dev/null; do sleep 2; done"
}

bootstrap_local_cli_pairing() {
  local list_json pending_id

  list_json="$(mktemp)"
  openclaw devices list --json > "${list_json}" 2>/dev/null || true
  pending_id="$(
    python3 - "${list_json}" <<'PY'
import json
import sys

path = sys.argv[1]
try:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
except Exception:
    print("")
    raise SystemExit(0)

for item in data.get("pending", []):
    if item.get("clientId") == "cli" and item.get("clientMode") == "cli":
        print(item.get("requestId", ""))
        break
else:
    print("")
PY
  )"
  rm -f "${list_json}"

  if [ -n "${pending_id}" ]; then
    openclaw devices approve "${pending_id}" >/dev/null 2>&1 || true
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

cleanup() {
  if [ -n "${openclaw_pid:-}" ]; then
    kill "${openclaw_pid}" >/dev/null 2>&1 || true
  fi
  if [ -n "${xvfb_pid:-}" ]; then
    kill "${xvfb_pid}" >/dev/null 2>&1 || true
  fi
  if [ -n "${sky10_pid:-}" ]; then
    kill "${sky10_pid}" >/dev/null 2>&1 || true
  fi
}

if [ ! -d "${PLUGIN_DIR}" ]; then
  echo >&2 "bundled sky10 plugin not found at ${PLUGIN_DIR}"
  exit 1
fi

if [ ! -e "${OPENCLAW_DIR}/.env" ] && [ -f /sandbox-state/.env ]; then
  ln -s /sandbox-state/.env "${OPENCLAW_DIR}/.env"
fi

python3 - <<'PY'
import json
import os
from pathlib import Path

config_path = Path.home() / ".openclaw" / "openclaw.json"
if config_path.exists():
    config = json.loads(config_path.read_text())
else:
    config = {}

defaults = config.setdefault("agents", {}).setdefault("defaults", {})
defaults["workspace"] = "/shared/workspace"

model = os.environ.get("OPENCLAW_MODEL", "").strip()
if model:
    defaults["model"] = model

gateway = config.setdefault("gateway", {})
gateway["port"] = 18789
gateway["bind"] = "loopback"
gateway["mode"] = "local"
gateway["auth"] = {"mode": "none"}
gateway.setdefault("http", {}).setdefault("endpoints", {}).setdefault("responses", {})["enabled"] = True

browser = config.setdefault("browser", {})
browser["executablePath"] = "/usr/local/bin/chromium"
browser["headless"] = False
browser["noSandbox"] = True
browser["ssrfPolicy"] = {"dangerouslyAllowPrivateNetwork": True}

plugins = config.setdefault("plugins", {})
load = plugins.setdefault("load", {})
paths = load.get("paths")
if not isinstance(paths, list):
    paths = []
plugin_dir = os.environ["PLUGIN_DIR"]
if plugin_dir not in paths:
    paths.append(plugin_dir)
load["paths"] = paths

entries = plugins.setdefault("entries", {})
entries["sky10"] = {
    "enabled": True,
    "config": {
        "rpcUrl": "http://localhost:9101",
        "agentName": os.environ["OPENCLAW_AGENT_NAME"],
        "skills": ["code", "shell", "browser", "web-search", "file-ops"],
        "gatewayUrl": "http://localhost:18789",
    },
}

channels = config.setdefault("channels", {})
sky10_channel = channels.setdefault("sky10", {})
sky10_channel["enabled"] = True
sky10_channel["defaultAccount"] = "default"
sky10_channel["healthMonitor"] = {"enabled": False}
sky10_accounts = sky10_channel.setdefault("accounts", {})
sky10_accounts["default"] = {
    "enabled": True,
    "rpcUrl": "http://localhost:9101",
    "agentName": os.environ["OPENCLAW_AGENT_NAME"],
    "skills": ["code", "shell", "browser", "web-search", "file-ops"],
}

config_path.write_text(json.dumps(config, indent=2) + "\n")
PY

trap cleanup EXIT INT TERM

install_guest_reconnect_helper

sky10 serve >/tmp/sky10.log 2>&1 &
sky10_pid=$!
wait_for_sky10
"${SKY10_RECONNECT_HELPER}" || true

Xvfb :99 -screen 0 1920x1080x24 -ac >/tmp/xvfb.log 2>&1 &
xvfb_pid=$!

openclaw gateway run >/tmp/openclaw-gateway.log 2>&1 &
openclaw_pid=$!
wait_for_openclaw_agent
bootstrap_local_cli_pairing

wait -n "${sky10_pid}" "${xvfb_pid}" "${openclaw_pid}"
