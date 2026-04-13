#!/bin/bash
set -eux -o pipefail

gateway="$(ip route show default dev eth0 | awk '/^default/ {print $3; exit}')"
if [ -n "${gateway}" ]; then
  ip route del default dev lima0 2>/dev/null || true
  ip route replace default via "${gateway}" dev eth0 metric 100
fi

cat > /etc/apt/apt.conf.d/99-force-ipv4 <<'EOF'
Acquire::ForceIPv4 "true";
Acquire::Retries "3";
EOF
