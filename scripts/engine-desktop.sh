#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_DIR="${ENGINE_APP_INSTALL_DIR:-$HOME/Applications}"
APP_NAME="Engine.app"
TARGET_APP="$INSTALL_DIR/$APP_NAME"
TAURI_BUNDLE_ROOT="$REPO_ROOT/packages/desktop-tauri/src-tauri/target/release/bundle/macos"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "error: missing required command: $1" >&2
    exit 1
  }
}

stop_running_app() {
  osascript -e 'tell application "Engine" to quit' >/dev/null 2>&1 || true
  pkill -x Engine >/dev/null 2>&1 || true
  pkill -x engine >/dev/null 2>&1 || true

  local attempt
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if ! pgrep -x Engine >/dev/null 2>&1 && ! pgrep -x engine >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
}

build_bundle() {
  need_cmd pnpm
  need_cmd find
  need_cmd ditto
  need_cmd open

  pnpm build:tauri

  local bundle_dir
  bundle_dir="$(find "$TAURI_BUNDLE_ROOT" -type d -name '*.app' | head -n 1 || true)"
  if [[ -z "$bundle_dir" ]]; then
    echo "error: could not find macOS app bundle under $TAURI_BUNDLE_ROOT" >&2
    exit 1
  fi

  mkdir -p "$INSTALL_DIR"
  rm -rf "$TARGET_APP"
  ditto "$bundle_dir" "$TARGET_APP"
}

launch_bundle() {
  if [[ ! -d "$TARGET_APP" ]]; then
    echo "error: app bundle not installed at $TARGET_APP" >&2
    exit 1
  fi
  open "$TARGET_APP"
}

case "${1:-}" in
  install)
    build_bundle
    launch_bundle
    ;;
  reinstall)
    stop_running_app
    build_bundle
    launch_bundle
    ;;
  *)
    echo "Usage: $0 {install|reinstall}" >&2
    exit 1
    ;;
esac