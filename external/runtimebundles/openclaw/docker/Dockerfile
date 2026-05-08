ARG SKY10_OPENCLAW_RUNTIME_IMAGE=ghcr.io/sky10ai/sky10-openclaw-runtime:2026.5.7-ubuntu24.04
FROM ${SKY10_OPENCLAW_RUNTIME_IMAGE}

ARG SKY10_AGENT_PACKAGES=""

ENV DEBIAN_FRONTEND=noninteractive

RUN if [ -n "${SKY10_AGENT_PACKAGES}" ]; then \
      apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
      && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y ${SKY10_AGENT_PACKAGES} \
      && rm -rf /var/lib/apt/lists/*; \
    fi

COPY entrypoint.sh /usr/local/bin/openclaw-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/openclaw-docker-entrypoint"]
