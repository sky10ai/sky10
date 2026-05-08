ARG SKY10_HERMES_RUNTIME_IMAGE=ghcr.io/sky10ai/sky10-hermes-runtime:v2026.5.7-ubuntu24.04
FROM ${SKY10_HERMES_RUNTIME_IMAGE}

COPY entrypoint.sh /usr/local/bin/hermes-docker-entrypoint

ENTRYPOINT ["/usr/local/bin/hermes-docker-entrypoint"]
