#!/bin/bash
set -euo pipefail

if [ "${SKY10_DOCKER_DEBUG:-}" = "1" ]; then
  set -x
fi

export HOME=/root
export PATH="${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
export SKY10_HOME="${HOME}/.sky10"
export SKY10_RUNTIME_DIR="/run/sky10"
export DISPLAY=:99
export OPENCLAW_NO_RESPAWN=1
export PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

OPENCLAW_DIR="${HOME}/.openclaw"
WORKSPACE_DIR="/shared/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
PLUGIN_DIR="${SANDBOX_STATE_DIR}/plugins/openclaw-sky10-channel"

export PLUGIN_DIR

mkdir -p "${OPENCLAW_DIR}/agents/main/sessions"
mkdir -p "${SKY10_HOME}"
mkdir -p "${SKY10_RUNTIME_DIR}"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}"

# Container restarts can leave runtime files whose PIDs collide with new processes.
rm -f "${SKY10_RUNTIME_DIR}/daemon.pid" "${SKY10_RUNTIME_DIR}/sky10.sock"

wait_for_sky10() {
  timeout 120s bash -lc 'until curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; do sleep 2; done'
}

wait_for_openclaw_agent() {
  timeout 120s bash -lc "until curl -fsS http://127.0.0.1:9101/rpc -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"method\":\"agent.list\",\"params\":{},\"id\":1}' | grep -F '\"name\":\"${OPENCLAW_AGENT_NAME}\"' >/dev/null; do sleep 2; done"
}

wait_for_openclaw_gateway() {
  timeout 120s bash -lc 'until pgrep -f "[o]penclaw-gateway" >/dev/null 2>&1; do sleep 2; done'
}

configure_managed_openclaw_bundled_plugins() {
  local package_root dist_root runtime_root plugin

  package_root="$(dirname "$(readlink -f "$(command -v openclaw)")")"
  dist_root="${package_root}/dist"
  runtime_root="${package_root}/dist-runtime"

  rm -rf "${runtime_root}"
  mkdir -p "${runtime_root}/extensions"
  find "${dist_root}" -mindepth 1 -maxdepth 1 ! -name extensions \
    -exec ln -sfn {} "${runtime_root}/" \;
  for plugin in anthropic browser speech-core memory-core image-generation-core media-understanding-core video-generation-core; do
    cp -a "${dist_root}/extensions/${plugin}" "${runtime_root}/extensions/${plugin}"
  done

  export OPENCLAW_BUNDLED_PLUGINS_DIR="${runtime_root}/extensions"
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

cleanup() {
  openclaw gateway stop >/dev/null 2>&1 || true
  pkill -x sky10 >/dev/null 2>&1 || true
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

configure_managed_openclaw_bundled_plugins
prime_managed_openclaw_runtime_deps

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
plugins["allow"] = ["sky10", "anthropic", "browser"]
plugins.setdefault("slots", {})["memory"] = "none"
load = plugins.setdefault("load", {})
paths = load.get("paths")
if not isinstance(paths, list):
    paths = []
plugin_dir = os.environ["PLUGIN_DIR"]
if plugin_dir not in paths:
    paths.append(plugin_dir)
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

trap cleanup EXIT INT TERM

sky10 serve >/tmp/sky10.log 2>&1 &
sky10_pid=$!
wait_for_sky10

Xvfb :99 -screen 0 1920x1080x24 -ac >/tmp/xvfb.log 2>&1 &
xvfb_pid=$!

openclaw gateway run >/tmp/openclaw-gateway.log 2>&1 &
openclaw_pid=$!
wait_for_openclaw_gateway
wait_for_openclaw_agent
bootstrap_local_cli_pairing

while true; do
  if ! curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; then
    echo >&2 "sky10 is not healthy"
    exit 1
  fi
  if ! kill -0 "${xvfb_pid}" >/dev/null 2>&1; then
    echo >&2 "Xvfb exited unexpectedly"
    exit 1
  fi
  if ! pgrep -f "[o]penclaw-gateway" >/dev/null 2>&1; then
    echo >&2 "OpenClaw gateway process is not running"
    exit 1
  fi
  sleep 2
done
