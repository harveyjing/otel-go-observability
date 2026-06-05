#!/usr/bin/env bash
set -euo pipefail

echo "==> Stopping app processes"
if [ -f /tmp/dice-pids ]; then
    # shellcheck source=/dev/null
    source /tmp/dice-pids
    kill "${BACKEND_PID:-}" "${FRONTEND_PID:-}" 2>/dev/null || true
    rm /tmp/dice-pids
    echo "  Stopped (PIDs from /tmp/dice-pids)"
else
    pkill -f dice-backend  2>/dev/null || true
    pkill -f dice-frontend 2>/dev/null || true
    echo "  Stopped (matched by name)"
fi

if [[ "${1:-}" == "--cluster" ]]; then
    echo "==> Deleting kind cluster otelpoc"
    kind delete cluster --name otelpoc
else
    echo "  Tip: pass --cluster to also delete the kind cluster"
fi

echo "Done."
