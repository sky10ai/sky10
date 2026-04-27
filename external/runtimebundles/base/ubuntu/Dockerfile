FROM ubuntu:24.04

ARG SKY10_AGENT_PACKAGES=""

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
  && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y \
    ca-certificates \
    curl \
    git \
    gnupg \
    python3 \
  && rm -rf /var/lib/apt/lists/*

RUN if [ -n "${SKY10_AGENT_PACKAGES}" ]; then \
      apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 update \
      && apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y ${SKY10_AGENT_PACKAGES} \
      && rm -rf /var/lib/apt/lists/*; \
    fi
