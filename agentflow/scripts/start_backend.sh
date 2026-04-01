#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/.venv/bin/activate"
cd "${ROOT_DIR}"
PORT="${AGENTFLOW_PORT:-8000}"
echo "[agentflow] Starting API + web UI on http://127.0.0.1:${PORT}/"
echo "[agentflow] WebSocket: ws://127.0.0.1:${PORT}/ws"
# Uvicorn + longer keep-alive avoids “stuck” browser tabs during slow OCR/uploads.
exec python3 -m uvicorn server:app --host 0.0.0.0 --port "${PORT}" --timeout-keep-alive 120
