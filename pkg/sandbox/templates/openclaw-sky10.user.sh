#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.bin:/usr/local/bin:/usr/bin:${PATH}"
export XDG_RUNTIME_DIR="/run/user/{{.UID}}"
export OPENCLAW_MODEL="{{.Param.model}}"
export OPENCLAW_AGENT_NAME="{{.Name}}"
export PLUGIN_DIR="/sandbox-state/plugins/openclaw-sky10-channel"

OPENCLAW_DIR="${HOME}/.openclaw"
WORKSPACE_DIR="/shared/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
STATE_DIR="${OPENCLAW_DIR}/.openclaw-lima"
SENTINEL="${STATE_DIR}/initialized-v2"
UNIT_DIR="${HOME}/.config/systemd/user"
GATEWAY_WRAPPER="${HOME}/.bin/openclaw-sky10-gateway"
SKY10_INVITE_PATH="/sandbox-state/join.json"

mkdir -p "${OPENCLAW_DIR}/agents/main/sessions"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}"
mkdir -p "${STATE_DIR}"
mkdir -p "${UNIT_DIR}"
mkdir -p "${HOME}/.bin"

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

prime_managed_openclaw_runtime_deps() {
  local package_root package_version path_hash install_root source_node_modules

  package_root="$(dirname "$(readlink -f "$(command -v openclaw)")")"
  package_version="$(
    node -e 'const fs=require("fs"); const path=require("path"); console.log(JSON.parse(fs.readFileSync(path.join(process.argv[1], "package.json"), "utf8")).version)' "${package_root}"
  )"
  path_hash="$(
    node -e 'const crypto=require("crypto"); const path=require("path"); console.log(crypto.createHash("sha256").update(path.resolve(process.argv[1])).digest("hex").slice(0, 12))' "${package_root}"
  )"
  install_root="${OPENCLAW_DIR}/plugin-runtime-deps/openclaw-${package_version}-${path_hash}"
  source_node_modules="${package_root}/dist-runtime/managed-runtime-deps/node_modules"
  if [ ! -d "${source_node_modules}" ]; then
    source_node_modules="${package_root}/node_modules"
  fi

  if [ ! -d "${install_root}/node_modules" ]; then
    mkdir -p "${install_root}"
    cp -a "${source_node_modules}" "${install_root}/node_modules"
  fi
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

ensure_guest_sky10() {
  ensure_guest_sky10_binary

  if curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; then
    emit_progress skip guest.sky10.start "Guest sky10 already running."
    return 0
  fi

  emit_progress begin guest.sky10.start "Starting guest sky10..."
  cat > "${UNIT_DIR}/sky10.service" <<EOF
[Unit]
Description=sky10 Daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/env sky10 serve
Restart=always
RestartSec=2
WorkingDirectory=${HOME}
Environment=HOME=${HOME}
Environment=PATH=${HOME}/.bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=default.target
EOF

  systemctl --user daemon-reload
  systemctl --user enable sky10.service
  systemctl --user restart sky10.service || systemctl --user start sky10.service

  wait_for_sky10
  emit_progress end guest.sky10.start "Guest sky10 running."
}

if ! command -v openclaw >/dev/null 2>&1; then
  echo >&2 "openclaw is not installed; system provisioning did not complete"
  exit 1
fi

if ! command -v chromium >/dev/null 2>&1; then
  echo >&2 "chromium is not installed; system provisioning did not complete"
  exit 1
fi

ensure_guest_sky10

emit_progress begin guest.openclaw.configure "Configuring OpenClaw..."
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

gateway = config.setdefault("gateway", {})
gateway["port"] = 18789
gateway["bind"] = "loopback"
gateway["mode"] = "local"
gateway["auth"] = {"mode": "none"}
gateway.setdefault("http", {}).setdefault("endpoints", {}).setdefault("responses", {})["enabled"] = True

defaults["model"] = os.environ["OPENCLAW_MODEL"]

browser = config.setdefault("browser", {})
browser["executablePath"] = "/usr/local/bin/chromium"
browser["headless"] = False
browser["noSandbox"] = True
browser["ssrfPolicy"] = {"dangerouslyAllowPrivateNetwork": True}

plugins = config.setdefault("plugins", {})
plugins["allow"] = ["sky10", "anthropic", "browser"]
plugins.setdefault("slots", {})["memory"] = "none"
load = plugins.setdefault("load", {})
paths = load.get("paths")
if not isinstance(paths, list):
    paths = []
if os.environ["PLUGIN_DIR"] not in paths:
    paths.append(os.environ["PLUGIN_DIR"])
load["paths"] = paths

entries = plugins.setdefault("entries", {})
entries.pop("acpx", None)
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

if [ ! -f "${SENTINEL}" ]; then
  touch "${SENTINEL}"
fi
emit_progress end guest.openclaw.configure "OpenClaw configured."

emit_progress begin guest.openclaw.start "Starting OpenClaw..."
prime_managed_openclaw_runtime_deps
cat > "${GATEWAY_WRAPPER}" <<'EOF'
#!/bin/bash
set -euo pipefail

openclaw_bin="$(command -v openclaw)"
package_root="$(dirname "$(readlink -f "${openclaw_bin}")")"
export OPENCLAW_BUNDLED_PLUGINS_DIR="${package_root}/dist-runtime/extensions"
export OPENCLAW_NO_RESPAWN=1
exec openclaw gateway run
EOF
chmod 755 "${GATEWAY_WRAPPER}"

cat > "${UNIT_DIR}/openclaw-gateway.service" <<EOF
[Unit]
Description=OpenClaw Gateway
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=${GATEWAY_WRAPPER}
Restart=always
RestartSec=5
WorkingDirectory=${WORKSPACE_DIR}
EnvironmentFile=-%h/.openclaw/.env
Environment=HOME=${HOME}
Environment=PATH=${HOME}/.bin:/usr/local/bin:/usr/bin:/bin
Environment=DISPLAY=:99
Environment=PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable openclaw-gateway.service
systemctl --user restart openclaw-gateway.service || systemctl --user start openclaw-gateway.service
wait_for_openclaw_agent
bootstrap_local_cli_pairing
emit_progress end guest.openclaw.start "OpenClaw ready."
