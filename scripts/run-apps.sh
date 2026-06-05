#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
APP="$HERE/app"

echo "==> Building binaries"
(cd "$APP" && go build -o /tmp/dice-backend ./cmd/backend)
(cd "$APP" && go build -o /tmp/dice-frontend ./cmd/frontend)

echo "==> Starting backend (:8081)"
/tmp/dice-backend &
BACKEND_PID=$!

echo "==> Starting frontend (:8080)"
/tmp/dice-frontend &
FRONTEND_PID=$!

printf 'BACKEND_PID=%s\nFRONTEND_PID=%s\n' "$BACKEND_PID" "$FRONTEND_PID" > /tmp/dice-pids

wait_for() {
    local url=$1 name=$2
    for i in $(seq 1 10); do
        if curl -sf "$url" >/dev/null 2>&1; then
            echo "  $name ready"
            return 0
        fi
        sleep 0.5
    done
    echo "  ERROR: $name did not become ready after 5s" >&2
    return 1
}

echo "==> Waiting for services"
wait_for "http://localhost:8081/roll"      "backend"
wait_for "http://localhost:8080/rolldice"  "frontend"

echo ""
echo "Services running:"
echo "  frontend: http://localhost:8080/rolldice  (PID $FRONTEND_PID)"
echo "  backend:  http://localhost:8081/roll       (PID $BACKEND_PID)"
echo ""
echo "Test:  curl localhost:8080/rolldice"
echo "Load:  ./scripts/load.sh"
echo "Stop:  ./scripts/teardown.sh"
