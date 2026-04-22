FROM ubuntu:24.04

ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/root

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

RUN curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --skip-setup

RUN curl -fsSL -o /usr/local/bin/sky10 "https://github.com/sky10ai/sky10/releases/latest/download/sky10-linux-${TARGETARCH}" \
  && chmod 755 /usr/local/bin/sky10

COPY entrypoint.sh /usr/local/bin/hermes-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/hermes-docker-entrypoint"]
