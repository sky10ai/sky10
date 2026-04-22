FROM ubuntu:24.04

ARG TARGETARCH
ARG OPENCLAW_VERSION=2026.4.14

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/root
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    dbus-user-session \
    git \
    gnupg \
    python3 \
    xvfb \
  && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
  && apt-get update && apt-get install -y nodejs \
  && rm -rf /var/lib/apt/lists/*

RUN npm install -g "openclaw@${OPENCLAW_VERSION}" \
  && npx -y playwright install-deps chromium \
  && mkdir -p /opt/ms-playwright \
  && PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright npx -y playwright install chromium \
  && CHROME_BIN="$(find /opt/ms-playwright -path '*/chrome-linux/chrome' | head -1)" \
  && test -n "${CHROME_BIN}" \
  && ln -sf "${CHROME_BIN}" /usr/local/bin/chromium \
  && ln -sf "${CHROME_BIN}" /usr/local/bin/google-chrome

RUN curl -fsSL -o /usr/local/bin/sky10 "https://github.com/sky10ai/sky10/releases/latest/download/sky10-linux-${TARGETARCH}" \
  && chmod 755 /usr/local/bin/sky10

COPY entrypoint.sh /usr/local/bin/openclaw-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/openclaw-docker-entrypoint"]
