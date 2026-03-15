#!/bin/zsh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_PATH="$ROOT_DIR/bin/feishu-codex-bridge"
APP_SUPPORT_DIR="${HOME}/Library/Application Support/feishu-codex-bridge"
LOG_DIR="${HOME}/Library/Logs/feishu-codex-bridge"

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"
export FEISHU_CODEX_BRIDGE_CONFIG="${FEISHU_CODEX_BRIDGE_CONFIG:-$APP_SUPPORT_DIR/config.yaml}"

load_shell_env_if_needed() {
  if [[ -n "${CODEX_API_KEY:-}" || -n "${OPENAI_API_KEY:-}" ]]; then
    return
  fi

  local shell_rc
  for shell_rc in "$HOME/.zprofile" "$HOME/.zshrc" "$HOME/.profile"; do
    if [[ -f "$shell_rc" ]]; then
      # shellcheck disable=SC1090
      source "$shell_rc"
    fi
    if [[ -n "${CODEX_API_KEY:-}" || -n "${OPENAI_API_KEY:-}" ]]; then
      return
    fi
  done
}

mkdir -p "$LOG_DIR"
cd "$ROOT_DIR"
load_shell_env_if_needed

if [[ -x "$BIN_PATH" ]]; then
  exec "$BIN_PATH"
fi

exec go run ./cmd/feishu-codex-bridge
