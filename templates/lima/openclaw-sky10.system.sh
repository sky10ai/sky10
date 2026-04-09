#!/bin/bash
set -eux -o pipefail

export HOME=/root
export DEBIAN_FRONTEND=noninteractive

STATE_DIR=/var/lib/sky10
SENTINEL="${STATE_DIR}/openclaw-sky10-system-v1"

mkdir -p "${STATE_DIR}"
mkdir -p /shared

if [ ! -f "${SENTINEL}" ]; then
  apt-get update -y
  apt-get upgrade -y
  apt-get install -y \
    apt-transport-https \
    ca-certificates \
    curl \
    debian-archive-keyring \
    debian-keyring \
    dbus-user-session \
    git \
    gnupg \
    python3 \
    xvfb

  curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  apt-get install -y nodejs

  npm install -g openclaw@latest

  npx -y playwright install-deps chromium
  mkdir -p /opt/ms-playwright
  PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright npx -y playwright install chromium

  CHROME_BIN="$(find /opt/ms-playwright -path '*/chrome-linux/chrome' | head -1)"
  if [ -z "${CHROME_BIN}" ]; then
    echo >&2 "playwright chromium binary not found"
    exit 1
  fi
  ln -sf "${CHROME_BIN}" /usr/local/bin/chromium
  ln -sf "${CHROME_BIN}" /usr/local/bin/google-chrome

  curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
    | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
  apt-get update -y
  apt-get install -y caddy

  cat > /etc/systemd/system/xvfb.service <<'EOF'
[Unit]
Description=Xvfb virtual framebuffer for OpenClaw browser automation
After=network.target

[Service]
ExecStart=/usr/bin/Xvfb :99 -screen 0 1920x1080x24 -ac
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable xvfb
  loginctl enable-linger "{{.User}}" || true

  touch "${SENTINEL}"
fi

CERT_PEM=/shared/certs/sb.sky10.local.pem
CERT_KEY=/shared/certs/sb.sky10.local-key.pem
if [ -f "${CERT_PEM}" ] && [ -f "${CERT_KEY}" ]; then
  cat > /etc/caddy/Caddyfile <<'EOF'
{
  auto_https off
}

:18790 {
  tls /shared/certs/sb.sky10.local.pem /shared/certs/sb.sky10.local-key.pem
  reverse_proxy localhost:18789
}
EOF
else
  cat > /etc/caddy/Caddyfile <<'EOF'
:18790 {
  reverse_proxy localhost:18789
}
EOF
fi

systemctl restart xvfb
systemctl enable caddy
systemctl restart caddy
