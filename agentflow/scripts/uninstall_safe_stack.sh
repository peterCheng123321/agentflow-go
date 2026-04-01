#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rm -rf "${ROOT_DIR}/.venv"
rm -rf "${ROOT_DIR}/openclaw_sandbox/runtime"
rm -rf "${ROOT_DIR}/openclaw_sandbox/logs"
rm -rf "${ROOT_DIR}/openclaw_sandbox/tmp"
rm -f "${ROOT_DIR}/openclaw_sandbox/.env"
rm -rf "${HOME}/.openclaw"
echo "Removed local AgentFlow/OpenClaw runtime files."
echo "If you installed Node system-wide, remove it manually with your package manager if desired."
