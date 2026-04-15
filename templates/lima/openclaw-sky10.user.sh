#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.bin:/usr/local/bin:/usr/bin:${PATH}"
export XDG_RUNTIME_DIR="/run/user/{{.UID}}"
export OPENCLAW_MODEL="{{.Param.model}}"
export OPENCLAW_AGENT_NAME="{{.Name}}"
export PLUGIN_DIR="/shared/openclaw-sky10-channel"

OPENCLAW_DIR="${HOME}/.openclaw"
WORKSPACE_DIR="${OPENCLAW_DIR}/workspace"
STATE_DIR="${OPENCLAW_DIR}/.openclaw-lima"
SENTINEL="${STATE_DIR}/initialized-v2"
UNIT_DIR="${HOME}/.config/systemd/user"

mkdir -p "${OPENCLAW_DIR}/agents/main/sessions"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${STATE_DIR}"
mkdir -p "${UNIT_DIR}"
mkdir -p "${HOME}/.bin"

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

ensure_guest_sky10() {
  if ! command -v sky10 >/dev/null 2>&1; then
    install_sky10
  fi

  if curl -fsS http://127.0.0.1:9101/health >/dev/null 2>&1; then
    return 0
  fi

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

if [ ! -d "${PLUGIN_DIR}" ]; then
  echo >&2 "bundled sky10 plugin not found at ${PLUGIN_DIR}"
  exit 1
fi

if [ ! -f "${WORKSPACE_DIR}/IDENTITY.md" ]; then
  cat > "${WORKSPACE_DIR}/IDENTITY.md" <<EOF
---
name: {{.Name}}
theme: OpenClaw sandbox running inside Lima with local browser automation.
---
EOF
fi

if [ ! -e "${OPENCLAW_DIR}/.env" ] && [ -f /shared/.env ]; then
  ln -s /shared/.env "${OPENCLAW_DIR}/.env"
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

gateway = config.setdefault("gateway", {})
gateway["port"] = 18789
gateway["bind"] = "loopback"
gateway["mode"] = "local"
gateway["auth"] = {"mode": "none"}
gateway.setdefault("http", {}).setdefault("endpoints", {}).setdefault("responses", {})["enabled"] = True

config.setdefault("agents", {}).setdefault("defaults", {})["model"] = os.environ["OPENCLAW_MODEL"]

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
if os.environ["PLUGIN_DIR"] not in paths:
    paths.append(os.environ["PLUGIN_DIR"])
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

config_path.write_text(json.dumps(config, indent=2) + "\n")
PY

if [ ! -f "${SENTINEL}" ]; then
  touch "${SENTINEL}"
fi

cat > "${UNIT_DIR}/openclaw-gateway.service" <<EOF
[Unit]
Description=OpenClaw Gateway
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/env openclaw gateway run
Restart=always
RestartSec=5
WorkingDirectory=${HOME}
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
