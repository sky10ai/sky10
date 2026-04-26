FROM ubuntu:24.04

ARG TARGETARCH
ARG SKY10_AGENT_PACKAGES=""

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/root
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

RUN apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
  && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y \
    ca-certificates \
    curl \
    dbus-user-session \
    git \
    gnupg \
    python3 \
    xvfb \
  && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused \
    -o /tmp/nodesource_setup.sh https://deb.nodesource.com/setup_22.x \
  && bash /tmp/nodesource_setup.sh \
  && rm -f /tmp/nodesource_setup.sh \
  && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
  && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y nodejs \
  && rm -rf /var/lib/apt/lists/*

ARG OPENCLAW_VERSION=2026.4.24

RUN npm install -g "openclaw@${OPENCLAW_VERSION}" \
  && OPENCLAW_PACKAGE_ROOT="$(dirname "$(readlink -f "$(command -v openclaw)")")" \
  && rm -rf "${OPENCLAW_PACKAGE_ROOT}/dist-runtime" \
  && mkdir -p "${OPENCLAW_PACKAGE_ROOT}/dist-runtime/extensions" \
  && find "${OPENCLAW_PACKAGE_ROOT}/dist" -mindepth 1 -maxdepth 1 ! -name extensions \
    -exec ln -sfn {} "${OPENCLAW_PACKAGE_ROOT}/dist-runtime/" \; \
  && for plugin in anthropic browser speech-core memory-core image-generation-core media-understanding-core video-generation-core; do \
       cp -a "${OPENCLAW_PACKAGE_ROOT}/dist/extensions/${plugin}" "${OPENCLAW_PACKAGE_ROOT}/dist-runtime/extensions/${plugin}"; \
     done \
  && OPENCLAW_MANAGED_DEPS_ROOT="${OPENCLAW_PACKAGE_ROOT}/dist-runtime/managed-runtime-deps" \
  && mkdir -p "${OPENCLAW_MANAGED_DEPS_ROOT}" \
  && npm install --prefix "${OPENCLAW_MANAGED_DEPS_ROOT}" --ignore-scripts --package-lock=false --save=false \
    "@anthropic-ai/sdk@0.90.0" \
    "@clack/prompts@^1.2.0" \
    "@google/genai@^1.50.1" \
    "@mariozechner/pi-agent-core@0.70.2" \
    "@mariozechner/pi-ai@0.70.2" \
    "@mariozechner/pi-coding-agent@0.70.2" \
    "@modelcontextprotocol/sdk@1.29.0" \
    "ajv@^8.18.0" \
    "chokidar@^5.0.0" \
    "commander@^14.0.3" \
    "express@^5.2.1" \
    "gaxios@7.1.4" \
    "google-auth-library@10.6.2" \
    "https-proxy-agent@^9.0.0" \
    "jiti@^2.6.1" \
    "markdown-it@14.1.1" \
    "openai@^6.34.0" \
    "playwright-core@1.59.1" \
    "typebox@1.1.31" \
    "undici@8.1.0" \
    "ws@^8.20.0" \
    "yaml@^2.8.3" \
    "zod@^4.3.6" \
  && npx -y playwright install-deps chromium \
  && mkdir -p /opt/ms-playwright \
  && PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright npx -y playwright install chromium \
  && CHROME_BIN="$(find /opt/ms-playwright -path '*/chrome-linux/chrome' | head -1)" \
  && test -n "${CHROME_BIN}" \
  && ln -sf "${CHROME_BIN}" /usr/local/bin/chromium \
  && ln -sf "${CHROME_BIN}" /usr/local/bin/google-chrome

RUN if [ -n "${SKY10_AGENT_PACKAGES}" ]; then \
      apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
      && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y ${SKY10_AGENT_PACKAGES} \
      && rm -rf /var/lib/apt/lists/*; \
    fi

RUN curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused \
    -o /usr/local/bin/sky10 "https://github.com/sky10ai/sky10/releases/latest/download/sky10-linux-${TARGETARCH}" \
  && chmod 755 /usr/local/bin/sky10

COPY entrypoint.sh /usr/local/bin/openclaw-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/openclaw-docker-entrypoint"]
