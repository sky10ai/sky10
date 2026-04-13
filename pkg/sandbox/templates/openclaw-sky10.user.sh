#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.bin:/usr/local/bin:/usr/bin:${PATH}"
export XDG_RUNTIME_DIR="/run/user/{{.UID}}"
export OPENCLAW_MODEL="{{.Param.model}}"

OPENCLAW_DIR="${HOME}/.openclaw"
WORKSPACE_DIR="${OPENCLAW_DIR}/workspace"
STATE_DIR="${OPENCLAW_DIR}/.openclaw-lima"
SENTINEL="${STATE_DIR}/initialized-v1"
UNIT_DIR="${HOME}/.config/systemd/user"

mkdir -p "${OPENCLAW_DIR}/agents/main/sessions"
mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${STATE_DIR}"
mkdir -p "${UNIT_DIR}"

if ! command -v openclaw >/dev/null 2>&1; then
  echo >&2 "openclaw is not installed; system provisioning did not complete"
  exit 1
fi

if ! command -v chromium >/dev/null 2>&1; then
  echo >&2 "chromium is not installed; system provisioning did not complete"
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
