#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"

WORKSPACE_DIR="/shared/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
RUNTIME_DIR="${SANDBOX_STATE_DIR}/runtime/openclaw"
BASE_COMPOSE_FILE="${RUNTIME_DIR}/base-compose.yaml"
SPEC_COMPOSE_FILE="/shared/compose.yaml"
CADDY_FILE="${RUNTIME_DIR}/Caddyfile"
STATE_DIR="${SANDBOX_STATE_DIR}/openclaw-docker"
SENTINEL="${STATE_DIR}/initialized-v1"
COMPOSE_FILES=(-f "${BASE_COMPOSE_FILE}")

mkdir -p "${WORKSPACE_DIR}"
mkdir -p /shared/agent /shared/input /shared/output
mkdir -p "${SANDBOX_STATE_DIR}"
mkdir -p "${RUNTIME_DIR}"
mkdir -p "${STATE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}/sky10-home"
mkdir -p "${SANDBOX_STATE_DIR}/openclaw-home"
mkdir -p "${SANDBOX_STATE_DIR}/caddy-data"
mkdir -p "${SANDBOX_STATE_DIR}/caddy-config"

emit_progress() {
  local event="$1"
  local id="$2"
  local summary="$3"
  printf 'SKY10_PROGRESS {"event":"%s","id":"%s","summary":"%s"}\n' "${event}" "${id}" "${summary}"
}

docker_compose() {
  if docker info >/dev/null 2>&1; then
    docker compose "${COMPOSE_FILES[@]}" "$@"
    return
  fi
  sudo -n docker compose "${COMPOSE_FILES[@]}" "$@"
}

wait_for_docker() {
  timeout 120s bash -lc 'until docker info >/dev/null 2>&1 || sudo -n docker info >/dev/null 2>&1; do sleep 2; done'
}

write_caddyfile() {
  local cert_pem="/sandbox-state/certs/sb.sky10.local.pem"
  local cert_key="/sandbox-state/certs/sb.sky10.local-key.pem"

  if [ -f "${cert_pem}" ] && [ -f "${cert_key}" ]; then
    cat > "${CADDY_FILE}" <<'EOF'
{
  auto_https off
}

:18790 {
  tls /sandbox-state/certs/sb.sky10.local.pem /sandbox-state/certs/sb.sky10.local-key.pem
  reverse_proxy localhost:18789
}
EOF
    return
  fi

  cat > "${CADDY_FILE}" <<'EOF'
:18790 {
  reverse_proxy localhost:18789
}
EOF
}

write_base_compose_file() {
  cat > "${BASE_COMPOSE_FILE}" <<'EOF'
services:
  openclaw:
    container_name: {{.Name}}-openclaw
    build:
      context: /sandbox-state/runtime/openclaw
      dockerfile: Dockerfile
    network_mode: host
    restart: unless-stopped
    environment:
      OPENCLAW_AGENT_NAME: "__SKY10_SANDBOX_NAME__"
      OPENCLAW_MODEL: "{{.Param.model}}"
    env_file:
      - /sandbox-state/.env
    volumes:
      - /shared:/shared
      - /sandbox-state:/sandbox-state
      - /sandbox-state/sky10-home:/root/.sky10
      - /sandbox-state/openclaw-home:/root/.openclaw

  caddy:
    container_name: {{.Name}}-openclaw-caddy
    image: caddy:2
    depends_on:
      - openclaw
    network_mode: host
    restart: unless-stopped
    volumes:
      - /sandbox-state/runtime/openclaw/Caddyfile:/etc/caddy/Caddyfile:ro
      - /sandbox-state:/sandbox-state:ro
      - /sandbox-state/caddy-data:/data
      - /sandbox-state/caddy-config:/config
EOF
}

configure_compose_files() {
  COMPOSE_FILES=(-f "${BASE_COMPOSE_FILE}")
  if [ -s "${SPEC_COMPOSE_FILE}" ]; then
    COMPOSE_FILES+=(-f "${SPEC_COMPOSE_FILE}")
  fi
}

if [ ! -f "${RUNTIME_DIR}/Dockerfile" ] || [ ! -f "${RUNTIME_DIR}/entrypoint.sh" ]; then
  echo >&2 "OpenClaw Docker runtime assets were not staged into ${RUNTIME_DIR}"
  exit 1
fi

emit_progress begin guest.docker.configure "Configuring Docker runtime..."
wait_for_docker
write_caddyfile
write_base_compose_file
configure_compose_files
emit_progress end guest.docker.configure "Docker runtime configured."

emit_progress begin guest.docker.build "Building Docker containers..."
docker_compose build
emit_progress end guest.docker.build "Docker containers built."

emit_progress begin guest.docker.start "Starting OpenClaw containers..."
docker_compose up -d --remove-orphans
emit_progress end guest.docker.start "OpenClaw containers running."

if [ ! -f "${SENTINEL}" ]; then
  touch "${SENTINEL}"
fi
