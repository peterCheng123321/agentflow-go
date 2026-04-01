#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"${ROOT_DIR}/scripts/start_backend.sh" &
BACKEND_PID=$!
trap 'kill ${BACKEND_PID} >/dev/null 2>&1 || true' EXIT
sleep 3
"${ROOT_DIR}/scripts/start_openclaw_bridge.sh"
