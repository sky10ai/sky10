ARG SKY10_HERMES_RUNTIME_IMAGE=ghcr.io/sky10ai/sky10-hermes-runtime:v2026.5.7-ubuntu24.04
FROM ${SKY10_HERMES_RUNTIME_IMAGE}

ARG TARGETARCH
ARG SKY10_VERSION=latest

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

COPY entrypoint.sh /usr/local/bin/hermes-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/hermes-docker-entrypoint"]
