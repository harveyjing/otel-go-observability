#!/usr/bin/env bash
set -euo pipefail

FRONTEND="${FRONTEND_ADDR:-http://localhost:8080}"

echo "Generating load against $FRONTEND/rolldice (Ctrl-C to stop)"
while true; do
    result=$(curl -sf "$FRONTEND/rolldice" || echo "ERROR")
    echo "roll: $result"
    sleep 0.5
done
