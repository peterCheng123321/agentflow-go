#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export OPENCLAW_HOME="${OPENCLAW_HOME:-${ROOT_DIR}/openclaw_sandbox/runtime}"
if [ -f "${ROOT_DIR}/openclaw_sandbox/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  source "${ROOT_DIR}/openclaw_sandbox/.env"
  set +a
fi
export PATH="${OPENCLAW_HOME}/bin:${PATH}"
export TMPDIR="${OPENCLAW_TMP_DIR:-${ROOT_DIR}/openclaw_sandbox/tmp}"
exec openclaw
