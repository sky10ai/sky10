#!/bin/bash
set -eux -o pipefail

export PATH="/usr/local/bin:/usr/bin:${PATH}"
export XDG_RUNTIME_DIR="/run/user/{{.UID}}"
export SKY10_RPC_URL="{{.Param.sky10RPCURL}}"
export SKY10_AGENT_NAME="{{.Name}}"
export OPENCLAW_MODEL="{{.Param.model}}"

OPENCLAW_DIR="${HOME}/.openclaw"
WORKSPACE_DIR="${OPENCLAW_DIR}/workspace"
PLUGIN_DIR="${OPENCLAW_DIR}/plugins/sky10"
STATE_DIR="${OPENCLAW_DIR}/.sky10-lima"
SENTINEL="${STATE_DIR}/initialized-v1"
UNIT_DIR="${HOME}/.config/systemd/user"

mkdir -p "${OPENCLAW_DIR}/agents/main/sessions"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${STATE_DIR}"
mkdir -p "${UNIT_DIR}"

if [ ! -d "${PLUGIN_DIR}" ]; then
  openclaw plugins install github:sky10ai/openclaw-sky10-channel
fi

if [ ! -d "${PLUGIN_DIR}/node_modules/eventsource" ]; then
  (
    cd "${PLUGIN_DIR}"
    npm install --no-save eventsource
  )
fi

if [ ! -f "${WORKSPACE_DIR}/IDENTITY.md" ]; then
  cat > "${WORKSPACE_DIR}/IDENTITY.md" <<EOF
---
name: {{.Name}}
theme: Helpful software agent running inside Lima and connected to sky10.
---
EOF
fi

if [ ! -e "${OPENCLAW_DIR}/.env" ] && [ -f /shared/.env ]; then
  ln -s /shared/.env "${OPENCLAW_DIR}/.env"
fi

if [ ! -f "${SENTINEL}" ]; then
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

entries = config.setdefault("plugins", {}).setdefault("entries", {})
entries["sky10"] = {
    "enabled": True,
    "config": {
        "rpcUrl": os.environ["SKY10_RPC_URL"],
        "agentName": os.environ["SKY10_AGENT_NAME"],
        "skills": ["code", "shell", "web-search", "file-ops"],
        "gatewayUrl": "http://localhost:18789",
    },
}

config_path.write_text(json.dumps(config, indent=2) + "\n")
PY

  touch "${SENTINEL}"
fi

cat > "${UNIT_DIR}/openclaw-gateway.service" <<EOF
[Unit]
Description=OpenClaw Gateway
After=network-online.target

[Service]
ExecStart=/usr/bin/env openclaw gateway run
Restart=always
RestartSec=5
WorkingDirectory=%h
Environment=HOME=%h
Environment=DISPLAY=:99
Environment=PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable openclaw-gateway.service
systemctl --user restart openclaw-gateway.service || systemctl --user start openclaw-gateway.service
