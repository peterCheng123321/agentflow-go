#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SANDBOX_DIR="${ROOT_DIR}/openclaw_sandbox"
ENV_FILE="${SANDBOX_DIR}/.env.wechat"
CONFIG_DIR="${SANDBOX_DIR}/config"
CONFIG_FILE="${CONFIG_DIR}/openclaw.wechat.yaml"

mkdir -p "${CONFIG_DIR}"

prompt_if_empty() {
  local var_name="$1"
  local prompt_text="$2"
  local secret="${3:-false}"
  local current_value="${!var_name:-}"

  if [ -n "$current_value" ]; then
    return
  fi

  if [ "$secret" = "true" ]; then
    read -r -s -p "${prompt_text}: " current_value
    printf '\n'
  else
    read -r -p "${prompt_text}: " current_value
  fi
  printf -v "$var_name" "%s" "$current_value"
}

validate_required() {
  local name="$1"
  local value="$2"
  if [ -z "$value" ]; then
    echo "[error] Missing required value: ${name}" >&2
    exit 1
  fi
}

main() {
  echo "[info] Configuring OpenClaw for a WeChat Official Account"
  echo "[info] According to OpenClaw docs, you need a public URL on HTTP 80 or HTTPS 443 and an Official Account."

  prompt_if_empty WECHAT_APP_ID "Enter your WeChat Official Account AppID"
  prompt_if_empty WECHAT_APP_SECRET "Enter your WeChat Official Account AppSecret" true
  prompt_if_empty WECHAT_TOKEN "Enter the WeChat server Token you will configure in mp.weixin.qq.com"
  prompt_if_empty WECHAT_ENCODING_AES_KEY "Enter the 43-character EncodingAESKey"
  prompt_if_empty WECHAT_PUBLIC_BASE_URL "Enter the public base URL for your OpenClaw instance (example: https://wechat.example.com)"
  prompt_if_empty WECHAT_ACCOUNT_TYPE "Enter account type: service or subscription"

  validate_required "WECHAT_APP_ID" "${WECHAT_APP_ID}"
  validate_required "WECHAT_APP_SECRET" "${WECHAT_APP_SECRET}"
  validate_required "WECHAT_TOKEN" "${WECHAT_TOKEN}"
  validate_required "WECHAT_ENCODING_AES_KEY" "${WECHAT_ENCODING_AES_KEY}"
  validate_required "WECHAT_PUBLIC_BASE_URL" "${WECHAT_PUBLIC_BASE_URL}"
  validate_required "WECHAT_ACCOUNT_TYPE" "${WECHAT_ACCOUNT_TYPE}"

  if [ "${#WECHAT_ENCODING_AES_KEY}" -ne 43 ]; then
    echo "[error] WECHAT_ENCODING_AES_KEY must be 43 characters long" >&2
    exit 1
  fi

  local reply_mode="async"
  if [ "${WECHAT_ACCOUNT_TYPE}" = "subscription" ]; then
    reply_mode="deferred"
  fi

  local webhook_url="${WECHAT_PUBLIC_BASE_URL%/}/api/channels/wechat/webhook"

  cat > "${ENV_FILE}" <<EOF
WECHAT_APP_ID=${WECHAT_APP_ID}
WECHAT_APP_SECRET=${WECHAT_APP_SECRET}
WECHAT_TOKEN=${WECHAT_TOKEN}
WECHAT_ENCODING_AES_KEY=${WECHAT_ENCODING_AES_KEY}
WECHAT_PUBLIC_BASE_URL=${WECHAT_PUBLIC_BASE_URL}
WECHAT_ACCOUNT_TYPE=${WECHAT_ACCOUNT_TYPE}
WECHAT_WEBHOOK_URL=${webhook_url}
WECHAT_REPLY_MODE=${reply_mode}
EOF

  cat > "${CONFIG_FILE}" <<EOF
server:
  host: 0.0.0.0
  port: 3000

channels:
  wechat:
    enabled: true
    app_id: \${WECHAT_APP_ID}
    app_secret: \${WECHAT_APP_SECRET}
    token: \${WECHAT_TOKEN}
    encoding_aes_key: \${WECHAT_ENCODING_AES_KEY}
    encryption_mode: "safe"
    reply_mode: "${reply_mode}"

llm:
  provider: ollama
  base_url: http://127.0.0.1:11434
  model: qwen3.5:4b
EOF

  cat <<EOF

[done] Files written:
- ${ENV_FILE}
- ${CONFIG_FILE}

[wechat admin panel]
Use these values in mp.weixin.qq.com > Settings & Development > Basic Configuration:
- URL: ${webhook_url}
- Token: ${WECHAT_TOKEN}
- EncodingAESKey: ${WECHAT_ENCODING_AES_KEY}
- Message Encryption Mode: Safe Mode

[important]
- OpenClaw must be running when WeChat verifies the webhook
- The domain must be publicly reachable on port 80 or 443
- OpenClaw docs say mainland China deployments require an ICP filing

[next steps]
1. Review ${ENV_FILE}
2. Export the variables before starting OpenClaw:
   set -a && source ${ENV_FILE} && set +a
3. Validate config:
   \$HOME/.openclaw/bin/openclaw config validate
4. Start the WeChat channel:
   \$HOME/.openclaw/bin/openclaw start --channel wechat
EOF
}

main "$@"
