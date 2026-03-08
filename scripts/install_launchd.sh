#!/bin/zsh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PLIST_TEMPLATE="$ROOT_DIR/launchd/local.feishu-codex-bridge.plist.template"
PLIST_TARGET="$HOME/Library/LaunchAgents/local.feishu-codex-bridge.plist"
LOG_DIR="$HOME/Library/Logs/feishu-codex-bridge"

mkdir -p "$HOME/Library/LaunchAgents"
mkdir -p "$LOG_DIR"
sed \
  -e "s|__ROOT_DIR__|$ROOT_DIR|g" \
  -e "s|__LOG_DIR__|$LOG_DIR|g" \
  "$PLIST_TEMPLATE" >"$PLIST_TARGET"

launchctl unload "$PLIST_TARGET" >/dev/null 2>&1 || true
launchctl load -w "$PLIST_TARGET"
