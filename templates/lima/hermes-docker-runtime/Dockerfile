FROM ubuntu:24.04

ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/root
ENV HERMES_BUILD_HOME=/opt/hermes-home
ENV HERMES_INSTALL_DIR=/opt/hermes-agent

RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    dbus-user-session \
    ffmpeg \
    git \
    python3 \
    ripgrep \
    xz-utils \
  && rm -rf /var/lib/apt/lists/*

RUN HERMES_HOME="${HERMES_BUILD_HOME}" HERMES_INSTALL_DIR="${HERMES_INSTALL_DIR}" \
    curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --skip-setup \
  && test -x "${HERMES_INSTALL_DIR}/venv/bin/hermes" \
  && ln -sf "${HERMES_INSTALL_DIR}/venv/bin/hermes" /usr/local/bin/hermes \
  && if [ -x "${HERMES_BUILD_HOME}/node/bin/node" ]; then \
       ln -sf "${HERMES_BUILD_HOME}/node/bin/node" /usr/local/bin/node; \
       ln -sf "${HERMES_BUILD_HOME}/node/bin/npm" /usr/local/bin/npm; \
       ln -sf "${HERMES_BUILD_HOME}/node/bin/npx" /usr/local/bin/npx; \
     fi

RUN curl -fsSL -o /usr/local/bin/sky10 "https://github.com/sky10ai/sky10/releases/latest/download/sky10-linux-${TARGETARCH}" \
  && chmod 755 /usr/local/bin/sky10

COPY entrypoint.sh /usr/local/bin/hermes-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/hermes-docker-entrypoint"]
