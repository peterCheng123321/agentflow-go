#!/usr/bin/env bash
set -euo pipefail
# Stop whatever is listening on CRM port (default 8000).
PORT="${AGENTFLOW_PORT:-8000}"
if pids=$(lsof -ti "TCP:${PORT}" -sTCP:LISTEN 2>/dev/null); then
  echo "Stopping PID(s) on port ${PORT}: ${pids}"
  kill -9 ${pids} 2>/dev/null || true
else
  echo "No listener on port ${PORT}"
fi
