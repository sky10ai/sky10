#!/bin/bash
set -euo pipefail

MARKER="# sky10 lima managed"
HOSTS_FILE="/etc/hosts"
TMP_FILE="$(mktemp /tmp/sky10-lima-hosts.XXXXXX)"
trap 'rm -f "${TMP_FILE}"' EXIT

LIMACTL_BIN="$(command -v limactl || true)"
if [ -z "${LIMACTL_BIN}" ] && [ -x "${HOME}/.sky10/bin/limactl" ]; then
  LIMACTL_BIN="${HOME}/.sky10/bin/limactl"
fi
if [ -z "${LIMACTL_BIN}" ]; then
  echo >&2 "limactl not found in PATH or ~/.sky10/bin/limactl"
  exit 1
fi

grep -vF "${MARKER}" "${HOSTS_FILE}" > "${TMP_FILE}" || true
grep -Ev '(lima\.local|\.sb\.sky10\.local)' "${TMP_FILE}" > "${TMP_FILE}.clean" || true
mv "${TMP_FILE}.clean" "${TMP_FILE}"

"${LIMACTL_BIN}" list --json 2>/dev/null | python3 -c '
import json
import sys

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    vm = json.loads(line)
    print(f"{vm[\"name\"]}\t{vm.get(\"status\", \"\")}")
' | while IFS=$'\t' read -r name status; do
  if [ "${status}" != "Running" ]; then
    continue
  fi

  ip="$("${LIMACTL_BIN}" shell "${name}" -- bash -lc "ip -4 route get 1.1.1.1 | awk '{for (i = 1; i <= NF; i++) if (\$i == \"src\") {print \$(i + 1); exit}}'" 2>/dev/null | tail -n1 | tr -d '\r')"
  if [ -z "${ip}" ]; then
    continue
  fi

  echo "${ip} ${name}.sb.sky10.local ${MARKER}" >> "${TMP_FILE}"
  echo "mapped ${name}.sb.sky10.local -> ${ip}"
done

sudo install -m 0644 "${TMP_FILE}" "${HOSTS_FILE}"
echo "updated ${HOSTS_FILE}"
