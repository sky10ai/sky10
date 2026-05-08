#!/bin/bash
set -eux -o pipefail

export HOME=/root
export DEBIAN_FRONTEND=noninteractive

STATE_DIR=/var/lib/openclaw-lima
SENTINEL="${STATE_DIR}/openclaw-system-v2"
APT_FLAGS=(-o Acquire::ForceIPv4=true -o Acquire::Retries=3)
ROUTE_OVERRIDE=/etc/netplan/99-openclaw-route-metrics.yaml
OPENCLAW_VERSION=2026.5.7

mkdir -p "${STATE_DIR}"
mkdir -p /shared
mkdir -p /shared/workspace
mkdir -p /sandbox-state

emit_progress() {
  local event="$1"
  local id="$2"
  local summary="$3"
  printf 'SKY10_PROGRESS {"event":"%s","id":"%s","summary":"%s"}\n' "${event}" "${id}" "${summary}"
}

persist_route_metrics() {
  cat > "${ROUTE_OVERRIDE}" <<'EOF'
network:
  version: 2
  ethernets:
    eth0:
      dhcp4-overrides:
        route-metric: 100
    lima0:
      dhcp4-overrides:
        route-metric: 200
EOF
  chmod 600 "${ROUTE_OVERRIDE}"
}

prefer_eth0_default_route() {
  local eth0_gateway lima0_gateway

  persist_route_metrics
  if command -v netplan >/dev/null 2>&1; then
    netplan apply || true
    sleep 2
  fi

  lima0_gateway="$(ip route show default dev lima0 | awk '/^default/ {print $3; exit}')"
  if [ -n "${lima0_gateway}" ]; then
    ip route replace default via "${lima0_gateway}" dev lima0 metric 200 || true
  fi

  eth0_gateway="$(ip route show default dev eth0 | awk '/^default/ {print $3; exit}')"
  if [ -n "${eth0_gateway}" ]; then
    ip route replace default via "${eth0_gateway}" dev eth0 metric 100
  fi
}

curl4() {
  curl -4 --retry 5 --retry-delay 2 --retry-connrefused -fsSL "$@"
}

configure_managed_openclaw_bundled_plugins() {
  local package_root dist_root runtime_root seed_root plugin

  package_root="$(dirname "$(readlink -f "$(command -v openclaw)")")"
  dist_root="${package_root}/dist"
  runtime_root="${package_root}/dist-runtime"
  seed_root="${runtime_root}/managed-runtime-deps"

  rm -rf "${runtime_root}"
  mkdir -p "${runtime_root}/extensions"
  find "${dist_root}" -mindepth 1 -maxdepth 1 ! -name extensions \
    -exec ln -sfn {} "${runtime_root}/" \;
  for plugin in anthropic browser speech-core memory-core image-generation-core media-understanding-core video-generation-core; do
    cp -a "${dist_root}/extensions/${plugin}" "${runtime_root}/extensions/${plugin}"
  done

  mkdir -p "${seed_root}"
  npm install --prefix "${seed_root}" --ignore-scripts --package-lock=false --save=false \
    @anthropic-ai/sdk@0.90.0 \
    @clack/prompts@^1.2.0 \
    @google/genai@^1.50.1 \
    @mariozechner/pi-agent-core@0.70.2 \
    @mariozechner/pi-ai@0.70.2 \
    @mariozechner/pi-coding-agent@0.70.2 \
    @modelcontextprotocol/sdk@1.29.0 \
    ajv@^8.18.0 \
    chokidar@^5.0.0 \
    commander@^14.0.3 \
    express@^5.2.1 \
    gaxios@7.1.4 \
    google-auth-library@10.6.2 \
    https-proxy-agent@^9.0.0 \
    jiti@^2.6.1 \
    markdown-it@14.1.1 \
    openai@^6.34.0 \
    playwright-core@1.59.1 \
    typebox@1.1.31 \
    undici@8.1.0 \
    ws@^8.20.0 \
    yaml@^2.8.3 \
    zod@^4.3.6
}

prefer_eth0_default_route

if [ ! -f "${SENTINEL}" ]; then
  emit_progress begin guest.system.packages "Installing system packages..."
  apt-get "${APT_FLAGS[@]}" update -y
  apt-get "${APT_FLAGS[@]}" install -y \
    apt-transport-https \
    ca-certificates \
    curl \
    dbus-user-session \
    fonts-freefont-ttf \
    fonts-ipafont-gothic \
    fonts-liberation \
    fonts-noto-color-emoji \
    fonts-tlwg-loma-otf \
    fonts-unifont \
    fonts-wqy-zenhei \
    git \
    gnupg \
    libasound2t64 \
    libatk-bridge2.0-0t64 \
    libatk1.0-0t64 \
    libatspi2.0-0t64 \
    libcairo2 \
    libcups2t64 \
    libdbus-1-3 \
    libdrm2 \
    libfontconfig1 \
    libfreetype6 \
    libgbm1 \
    libglib2.0-0t64 \
    libnspr4 \
    libnss3 \
    libpango-1.0-0 \
    libx11-6 \
    libxcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxkbcommon0 \
    libxrandr2 \
    python3 \
    xfonts-cyrillic \
    xfonts-scalable \
    xvfb
  emit_progress end guest.system.packages "System packages installed."

  emit_progress begin guest.node.install "Installing Node.js..."
  curl4 https://deb.nodesource.com/setup_22.x | bash -
  apt-get "${APT_FLAGS[@]}" install -y nodejs
  emit_progress end guest.node.install "Node.js installed."

  emit_progress begin guest.openclaw.install "Installing OpenClaw..."
  npm install -g "openclaw@${OPENCLAW_VERSION}"
  emit_progress end guest.openclaw.install "OpenClaw installed."

  emit_progress begin guest.chromium.install "Installing Chromium..."
  mkdir -p /opt/ms-playwright
  PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright npx -y playwright@1.59.1 install chromium

  CHROME_BIN="$(find /opt/ms-playwright -type f -path '*/chrome-linux*/chrome' | head -1)"
  if [ -z "${CHROME_BIN}" ]; then
    echo >&2 "playwright chromium binary not found"
    exit 1
  fi
  ln -sf "${CHROME_BIN}" /usr/local/bin/chromium
  ln -sf "${CHROME_BIN}" /usr/local/bin/google-chrome
  emit_progress end guest.chromium.install "Chromium installed."

  emit_progress begin guest.caddy.install "Installing Caddy..."
  curl4 https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl4 https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
    | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
  apt-get "${APT_FLAGS[@]}" update -y
  apt-get "${APT_FLAGS[@]}" install -y caddy

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
  emit_progress end guest.caddy.install "Caddy installed."
else
  emit_progress skip guest.system.packages "System packages already installed."
  emit_progress skip guest.node.install "Node.js already installed."
  emit_progress skip guest.openclaw.install "OpenClaw already installed."
  emit_progress skip guest.chromium.install "Chromium already installed."
  emit_progress skip guest.caddy.install "Caddy already installed."
fi

configure_managed_openclaw_bundled_plugins

CERT_PEM=/sandbox-state/certs/sb.sky10.local.pem
CERT_KEY=/sandbox-state/certs/sb.sky10.local-key.pem
if [ -f "${CERT_PEM}" ] && [ -f "${CERT_KEY}" ]; then
  cat > /etc/caddy/Caddyfile <<'EOF'
{
  auto_https off
}

:18790 {
  tls /sandbox-state/certs/sb.sky10.local.pem /sandbox-state/certs/sb.sky10.local-key.pem
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
