#!/bin/bash
set -eux -o pipefail

export PATH="${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
export XDG_RUNTIME_DIR="/run/user/{{.UID}}"
export HERMES_HOME="${HOME}/.hermes"
export HERMES_MODEL="{{.Param.model}}"

SHARED_DIR="/shared"
STATE_DIR="${HERMES_HOME}/.sky10-lima"
SENTINEL="${STATE_DIR}/initialized-v1"
HELPER="${HOME}/.local/bin/hermes-shared"
WELCOME="${SHARED_DIR}/HERMES.md"

mkdir -p "${STATE_DIR}"
mkdir -p "${HOME}/.local/bin"
mkdir -p "${SHARED_DIR}"

curl4() {
  curl -4 --retry 5 --retry-delay 2 --retry-connrefused -fsSL "$@"
}

ensure_shared_env() {
  if [ ! -f "${SHARED_DIR}/.env" ]; then
    cat > "${SHARED_DIR}/.env" <<'EOF'
# Optional provider keys for Hermes inside Lima.
# Host secrets named ANTHROPIC_API_KEY/anthropic, OPENAI_API_KEY/openai,
# and OPENROUTER_API_KEY/openrouter merge in automatically when available.
# Hermes reads ~/.hermes/.env, which is linked to this shared file.

OPENAI_API_KEY=
ANTHROPIC_API_KEY=
OPENROUTER_API_KEY=
EOF
    chmod 600 "${SHARED_DIR}/.env"
  fi
}

link_hermes_env() {
  mkdir -p "${HERMES_HOME}"

  if [ -f "${HERMES_HOME}/.env" ] && [ ! -L "${HERMES_HOME}/.env" ]; then
    cp "${HERMES_HOME}/.env" "${SHARED_DIR}/.env"
    chmod 600 "${SHARED_DIR}/.env"
    rm -f "${HERMES_HOME}/.env"
  fi

  ln -sfn "${SHARED_DIR}/.env" "${HERMES_HOME}/.env"
}

write_helper() {
  cat > "${HELPER}" <<'EOF'
#!/bin/bash
set -e
export PATH="${HOME}/.local/bin:${HOME}/.cargo/bin:${HOME}/.bin:/usr/local/bin:/usr/bin:/bin:${PATH}"
cd /shared
exec hermes "$@"
EOF
  chmod 755 "${HELPER}"
}

write_welcome() {
  cat > "${WELCOME}" <<'EOF'
# Hermes on Lima

This sandbox installs Hermes Agent inside the guest and keeps your working files in `/shared`.

## Start the TUI

```bash
hermes-shared
```

## Common setup

1. Host-managed provider secrets merge into `/shared/.env` automatically when available
2. Add or edit keys in `/shared/.env` directly if you need to override them
3. Run `hermes model` if you want to switch models/providers
4. Keep project files in `/shared` so Hermes starts in the shared workspace
EOF
}

ensure_shared_env

if [ ! -f "${SENTINEL}" ]; then
  curl4 https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --skip-setup
  touch "${SENTINEL}"
fi

link_hermes_env
write_helper
write_welcome

if command -v hermes >/dev/null 2>&1; then
  hermes config set terminal.backend local || true
  hermes config set terminal.cwd /shared || true
  if [ -n "${HERMES_MODEL}" ]; then
    hermes config set model "${HERMES_MODEL}" || true
  fi
fi
