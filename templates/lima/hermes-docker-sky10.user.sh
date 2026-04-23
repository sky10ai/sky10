#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"

WORKSPACE_DIR="/shared/workspace"
SANDBOX_STATE_DIR="/sandbox-state"
RUNTIME_DIR="${SANDBOX_STATE_DIR}/runtime/hermes"
COMPOSE_FILE="${RUNTIME_DIR}/compose.yaml"
STATE_DIR="${SANDBOX_STATE_DIR}/hermes-docker"
HELPER_DIR="${HOME}/.local/bin"
HELPER="${HELPER_DIR}/hermes-shared"

mkdir -p "${WORKSPACE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}"
mkdir -p "${RUNTIME_DIR}"
mkdir -p "${STATE_DIR}"
mkdir -p "${SANDBOX_STATE_DIR}/sky10-home"
mkdir -p "${SANDBOX_STATE_DIR}/hermes-home"
mkdir -p "${HELPER_DIR}"

emit_progress() {
  local event="$1"
  local id="$2"
  local summary="$3"
  printf 'SKY10_PROGRESS {"event":"%s","id":"%s","summary":"%s"}\n' "${event}" "${id}" "${summary}"
}

docker_compose() {
  if docker info >/dev/null 2>&1; then
    docker compose "$@"
    return
  fi
  sudo -n docker compose "$@"
}

wait_for_docker() {
  timeout 120s bash -lc 'until docker info >/dev/null 2>&1 || sudo -n docker info >/dev/null 2>&1; do sleep 2; done'
}

write_compose_file() {
  cat > "${COMPOSE_FILE}" <<'EOF'
services:
  hermes:
    container_name: {{.Name}}-hermes
    build:
      context: /sandbox-state/runtime/hermes
      dockerfile: Dockerfile
    network_mode: host
    restart: unless-stopped
    environment:
      HERMES_MODEL: "{{.Param.model}}"
    volumes:
      - /shared:/shared
      - /sandbox-state:/sandbox-state
      - /sandbox-state/sky10-home:/root/.sky10
      - /sandbox-state/hermes-home:/root/.hermes
EOF
}

write_helper() {
  cat > "${HELPER}" <<'EOF'
#!/bin/bash
set -e

TTY_FLAGS=(-i)
if [ -t 0 ] && [ -t 1 ]; then
  TTY_FLAGS=(-it)
fi

exec sudo -n docker exec "${TTY_FLAGS[@]}" \
  -w /shared/workspace \
  {{.Name}}-hermes \
  env PATH=/root/.local/bin:/root/.cargo/bin:/usr/local/bin:/usr/bin:/bin \
  hermes "$@"
EOF
  chmod 755 "${HELPER}"
}

if [ ! -f "${RUNTIME_DIR}/Dockerfile" ] || [ ! -f "${RUNTIME_DIR}/entrypoint.sh" ]; then
  echo >&2 "Hermes Docker runtime assets were not staged into ${RUNTIME_DIR}"
  exit 1
fi

emit_progress begin guest.docker.configure "Configuring Docker runtime..."
wait_for_docker
write_compose_file
write_helper
emit_progress end guest.docker.configure "Docker runtime configured."

emit_progress begin guest.docker.build "Building Hermes container..."
docker_compose -f "${COMPOSE_FILE}" build hermes
emit_progress end guest.docker.build "Hermes container built."

emit_progress begin guest.docker.start "Starting Hermes containers..."
docker_compose -f "${COMPOSE_FILE}" up -d --remove-orphans
emit_progress end guest.docker.start "Hermes containers running."
