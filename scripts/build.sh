#!/bin/zsh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

mkdir -p "$ROOT_DIR/bin"
go build -o "$ROOT_DIR/bin/feishu-codex-bridge" ./cmd/feishu-codex-bridge
