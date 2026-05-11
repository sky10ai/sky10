ARG SKY10_OPENCLAW_RUNTIME_IMAGE=ghcr.io/sky10ai/sky10-openclaw-runtime:2026.5.7-ubuntu24.04
FROM ${SKY10_OPENCLAW_RUNTIME_IMAGE}

ARG TARGETARCH
ARG SKY10_AGENT_PACKAGES=""
ARG SKY10_VERSION=latest

ENV DEBIAN_FRONTEND=noninteractive

RUN if [ -n "${SKY10_AGENT_PACKAGES}" ]; then \
      apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
      && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y ${SKY10_AGENT_PACKAGES} \
      && rm -rf /var/lib/apt/lists/*; \
    fi

RUN set -eux; \
    arch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    case "${arch}" in \
      amd64|arm64) ;; \
      aarch64) arch=arm64 ;; \
      x86_64) arch=amd64 ;; \
      *) echo >&2 "unsupported sky10 architecture: ${arch}"; exit 1 ;; \
    esac; \
    if [ "${SKY10_VERSION}" = "latest" ]; then \
      sky10_url="https://github.com/sky10ai/sky10/releases/latest/download/sky10-linux-${arch}"; \
    else \
      sky10_url="https://github.com/sky10ai/sky10/releases/download/${SKY10_VERSION}/sky10-linux-${arch}"; \
    fi; \
    curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused -o /usr/local/bin/sky10 "${sky10_url}"; \
    chmod 755 /usr/local/bin/sky10

COPY entrypoint.sh /usr/local/bin/openclaw-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/openclaw-docker-entrypoint"]
