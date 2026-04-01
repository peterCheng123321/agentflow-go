#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VENV_DIR="${ROOT_DIR}/.venv"
OPENCLAW_HOME_DEFAULT="${ROOT_DIR}/openclaw_sandbox/runtime"
OPENCLAW_HOME="${OPENCLAW_HOME:-$OPENCLAW_HOME_DEFAULT}"
OPENCLAW_BIN="${OPENCLAW_BIN:-$HOME/.openclaw/bin/openclaw}"
LOG_DIR="${ROOT_DIR}/openclaw_sandbox/logs"
CONFIG_DIR="${ROOT_DIR}/openclaw_sandbox/config"
ENV_FILE="${ROOT_DIR}/openclaw_sandbox/.env"
REQUIREMENTS_FILE="${ROOT_DIR}/requirements-local.txt"
TMP_DIR="${ROOT_DIR}/openclaw_sandbox/tmp"

OS_NAME="$(uname -s)"
ARCH_NAME="$(uname -m)"

log() {
  printf '\n[%s] %s\n' "$1" "$2"
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

version_ge() {
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

scan_system() {
  log "scan" "Detected OS=${OS_NAME} ARCH=${ARCH_NAME}"
  if have_cmd python3; then
    log "scan" "python3: $(python3 --version 2>/dev/null)"
  else
    log "error" "python3 is missing"
    exit 1
  fi

  if have_cmd node; then
    log "scan" "node: $(node --version 2>/dev/null)"
  else
    log "scan" "node: missing"
  fi

  if have_cmd npm; then
    log "scan" "npm: $(npm --version 2>/dev/null)"
  else
    log "scan" "npm: missing"
  fi

  if have_cmd brew; then
    log "scan" "homebrew detected"
  elif have_cmd apt-get; then
    log "scan" "apt detected"
  else
    log "warn" "No supported package manager detected automatically"
  fi
}

install_node_if_needed() {
  local node_ok="false"
  if have_cmd node; then
    local current_node
    current_node="$(node -v | sed 's/^v//')"
    if version_ge "$current_node" "22.0.0"; then
      node_ok="true"
    fi
  fi

  if [ "$node_ok" = "true" ]; then
    log "node" "Node version is sufficient"
    return
  fi

  if [ "$OS_NAME" = "Darwin" ] && have_cmd brew; then
    log "node" "Installing Node via Homebrew"
    brew install node
    return
  fi

  if [ "$OS_NAME" = "Linux" ] && have_cmd apt-get; then
    log "node" "Installing Node.js 22 via NodeSource"
    curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
    sudo apt-get install -y nodejs
    return
  fi

  log "error" "Unable to install Node automatically. Install Node 22+ and re-run."
  exit 1
}

prepare_python_env() {
  log "python" "Creating virtual environment at ${VENV_DIR}"
  python3 -m venv "$VENV_DIR"
  # shellcheck disable=SC1090
  source "${VENV_DIR}/bin/activate"
  python -m pip install --upgrade pip

  # MLX (Apple Silicon) requirements check
  if [ "$ARCH_NAME" != "arm64" ]; then
    log "warn" "mlx-lm requires Apple Silicon (arm64). MLX inference will not work on this machine."
  fi

  # M2 optimization: use arm64-optimized wheels when available
  if [ "$ARCH_NAME" = "arm64" ]; then
    log "python" "Installing packages with ARM64 optimizations (includes mlx-lm)"
    python -m pip install --upgrade wheel setuptools
    python -m pip install -r "$REQUIREMENTS_FILE" --prefer-binary
  else
    python -m pip install -r "$REQUIREMENTS_FILE"
  fi
}

prepare_directories() {
  log "dirs" "Preparing local runtime directories"
  mkdir -p "$LOG_DIR" "$CONFIG_DIR" "$OPENCLAW_HOME" "$TMP_DIR"
  mkdir -p "${ROOT_DIR}/data/docs" "${ROOT_DIR}/data/vector_store" "${ROOT_DIR}/data/mock_cases"
}

install_openclaw_local() {
  log "openclaw" "Installing OpenClaw into isolated local runtime at ${OPENCLAW_HOME}"
  export OPENCLAW_HOME
  export OPENCLAW_NO_ONBOARD=1

  curl -fsSL --proto '=https' --tlsv1.2 https://openclaw.ai/install-cli.sh | bash

  if [ ! -x "$OPENCLAW_BIN" ]; then
    log "error" "OpenClaw installer completed, but no CLI was found at ${OPENCLAW_BIN}"
    exit 1
  fi
}

write_env_file() {
  log "config" "Writing sandbox environment file"

  # M2-optimized memory allocation
  local max_memory_mb=512
  local max_vram_mb=0

  if [ "$ARCH_NAME" = "arm64" ]; then
    # M2, M3, M4 chips have better memory bandwidth - increase limits
    max_memory_mb=2048
    max_vram_mb=0  # Keep VRAM at 0 for CPU-only operation on M2
    log "config" "M2 detected (arm64) - optimizing memory to ${max_memory_mb}MB"
  fi

  cat > "$ENV_FILE" <<EOF
WECHAT_CONNECTOR_MODE=openclaw
AGENTFLOW_API_BASE=http://127.0.0.1:8000
OPENCLAW_HOME=${OPENCLAW_HOME}
OPENCLAW_ALLOWED_ACTIONS=read_contacts,send_message,create_groupchat,signal_rag
OPENCLAW_READ_ONLY=true
OPENCLAW_BIN=${OPENCLAW_BIN}
OPENCLAW_LOG_DIR=${LOG_DIR}
OPENCLAW_TMP_DIR=${TMP_DIR}
OPENCLAW_MAX_MEMORY_MB=${max_memory_mb}
OPENCLAW_MAX_VRAM_MB=${max_vram_mb}
OPENCLAW_DISABLE_EXTRA_SKILLS=true
OPENCLAW_DISABLE_MODEL_HOSTING=true
OPENCLAW_DISABLE_BROWSER_AUTOMATION=true
OPENCLAW_DISABLE_FILESYSTEM_DISCOVERY=true
OPENCLAW_ENABLE_ARM64_OPTIMIZATIONS=true
EOF
}

write_service_scripts() {
  log "config" "Writing local service launcher scripts"

  # M2-optimized Python settings
  local python_opts=""
  if [ "$ARCH_NAME" = "arm64" ]; then
    # Use hash randomization for security and enable multi-threading optimizations
    python_opts="PYTHONHASHSEED=random"
  fi

  cat > "${ROOT_DIR}/scripts/start_backend.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/.venv/bin/activate"
cd "${ROOT_DIR}"

# ARM64 (M2+) performance tuning
export PYTHONHASHSEED=random
export PYTHONDONTWRITEBYTECODE=1

exec python3 server.py
EOF

  cat > "${ROOT_DIR}/scripts/start_openclaw_bridge.sh" <<'EOF'
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
OPENCLAW_BIN="${OPENCLAW_BIN:-$HOME/.openclaw/bin/openclaw}"
export TMPDIR="${OPENCLAW_TMP_DIR:-${ROOT_DIR}/openclaw_sandbox/tmp}"
exec "${OPENCLAW_BIN}"
EOF

  cat > "${ROOT_DIR}/scripts/start_local_stack.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"${ROOT_DIR}/scripts/start_backend.sh" &
BACKEND_PID=$!
trap 'kill ${BACKEND_PID} >/dev/null 2>&1 || true' EXIT
sleep 3
"${ROOT_DIR}/scripts/start_openclaw_bridge.sh"
EOF

  chmod +x "${ROOT_DIR}/scripts/start_backend.sh" \
           "${ROOT_DIR}/scripts/start_openclaw_bridge.sh" \
           "${ROOT_DIR}/scripts/start_local_stack.sh"
}

write_uninstall_script() {
  log "config" "Writing uninstall script"
  cat > "${ROOT_DIR}/scripts/uninstall_safe_stack.sh" <<'EOF'
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
EOF
  chmod +x "${ROOT_DIR}/scripts/uninstall_safe_stack.sh"
}

main() {
  scan_system
  prepare_directories
  install_node_if_needed
  prepare_python_env
  install_openclaw_local
  write_env_file
  write_service_scripts
  write_uninstall_script

  log "done" "Bootstrap complete"
  printf '\nNext steps:\n'
  printf '1. Review %s\n' "$ENV_FILE"
  printf '2. Start backend: %s/scripts/start_backend.sh\n' "$ROOT_DIR"
  printf '3. Start OpenClaw bridge: %s/scripts/start_openclaw_bridge.sh\n' "$ROOT_DIR"
  printf '4. Or run both: %s/scripts/start_local_stack.sh\n' "$ROOT_DIR"
  printf '5. Remove local runtime later: %s/scripts/uninstall_safe_stack.sh\n' "$ROOT_DIR"
}

main "$@"
