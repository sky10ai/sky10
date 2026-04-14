#!/bin/bash
set -eux -o pipefail

ROUTE_OVERRIDE=/etc/netplan/99-openclaw-route-metrics.yaml

persist_route_metrics() {
  cat > "${ROUTE_OVERRIDE}" <<'EOF'
network:
  version: 2
  ethernets:
    eth0:
      dhcp4-overrides:
        route-metric: 100
    lima0:
      dhcp4-overrides:
        route-metric: 200
EOF
  chmod 600 "${ROUTE_OVERRIDE}"
}

prefer_eth0_default_route() {
  local eth0_gateway lima0_gateway

  persist_route_metrics
  if command -v netplan >/dev/null 2>&1; then
    netplan apply || true
    sleep 2
  fi

  lima0_gateway="$(ip route show default dev lima0 | awk '/^default/ {print $3; exit}')"
  if [ -n "${lima0_gateway}" ]; then
    ip route replace default via "${lima0_gateway}" dev lima0 metric 200 || true
  fi

  eth0_gateway="$(ip route show default dev eth0 | awk '/^default/ {print $3; exit}')"
  if [ -n "${eth0_gateway}" ]; then
    ip route replace default via "${eth0_gateway}" dev eth0 metric 100
  fi
}

prefer_eth0_default_route

cat > /etc/apt/apt.conf.d/99-force-ipv4 <<'EOF'
Acquire::ForceIPv4 "true";
Acquire::Retries "3";
EOF
