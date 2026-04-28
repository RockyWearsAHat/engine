#!/usr/bin/env bash
# Engine persistent background service management.
# Installs / uninstalls a launchd agent that keeps the Go server running at login.
#
# Usage:
#   ./scripts/engine-service.sh install     -- build binary, write plist, load service
#   ./scripts/engine-service.sh uninstall   -- stop + remove service
#   ./scripts/engine-service.sh status      -- show launchctl service status
#   ./scripts/engine-service.sh logs        -- tail live server logs
#
# Credentials are read from the environment at install time and baked into the
# plist EnvironmentVariables so the service starts correctly without a shell session.
# Sensitive keys are never written to the repository — the plist is at ~/Library.
#
# Required env vars (must be set before running install):
#   ANTHROPIC_API_KEY   — Claude API key
#   GITHUB_TOKEN        — GitHub personal access token (or rely on gh CLI auth)
#
# Optional:
#   PROJECT_PATH        — defaults to the repo root
#   PORT                — defaults to 24444
#   ENGINE_CLONES_DIR   — where autonomous project clones are stored

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="$REPO_ROOT/packages/server-go/engine-server"
SERVICE_LABEL="com.engine.server"
PLIST_PATH="$HOME/Library/LaunchAgents/$SERVICE_LABEL.plist"
LOG_DIR="$HOME/Library/Logs/Engine"
LOG_OUT="$LOG_DIR/server.log"
LOG_ERR="$LOG_DIR/server-error.log"

_project_path="${PROJECT_PATH:-$REPO_ROOT}"
_port="${PORT:-24444}"

_err() { echo "error: $*" >&2; exit 1; }

cmd_install() {
    echo "==> Building engine-server binary..."
    (cd "$REPO_ROOT" && node scripts/build-go.mjs)
    [[ -x "$BINARY" ]] || _err "binary not found after build: $BINARY"

    echo "==> Creating log directory: $LOG_DIR"
    mkdir -p "$LOG_DIR"

    if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
        echo "warning: ANTHROPIC_API_KEY not set — AI sessions will fail until you reinstall with the key set"
    fi
    if [[ -z "${GITHUB_TOKEN:-}" ]]; then
        echo "warning: GITHUB_TOKEN not set — GitHub monitoring will use gh CLI auth fallback"
    fi

    echo "==> Writing launchd plist: $PLIST_PATH"
    cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>$SERVICE_LABEL</string>

    <key>ProgramArguments</key>
    <array>
        <string>$BINARY</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
        <key>PROJECT_PATH</key>
        <string>$_project_path</string>
        <key>PORT</key>
        <string>$_port</string>
        <key>ANTHROPIC_API_KEY</key>
        <string>${ANTHROPIC_API_KEY:-}</string>
        <key>GITHUB_TOKEN</key>
        <string>${GITHUB_TOKEN:-}</string>
        <key>ENGINE_CLONES_DIR</key>
        <string>${ENGINE_CLONES_DIR:-$REPO_ROOT/.engine/projects}</string>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>

    <key>WorkingDirectory</key>
    <string>$_project_path</string>

    <key>StandardOutPath</key>
    <string>$LOG_OUT</string>

    <key>StandardErrorPath</key>
    <string>$LOG_ERR</string>

    <key>KeepAlive</key>
    <true/>

    <key>RunAtLoad</key>
    <true/>

    <key>ThrottleInterval</key>
    <integer>10</integer>
</dict>
</plist>
PLIST

    # Unload stale copy if present
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
    launchctl load "$PLIST_PATH"

    echo ""
    echo "Engine server installed and running."
    echo "  Logs:    $LOG_OUT"
    echo "  Errors:  $LOG_ERR"
    echo "  Port:    $_port"
    echo "  Project: $_project_path"
    echo ""
    echo "The service starts automatically at login. To check status:"
    echo "  ./scripts/engine-service.sh status"
}

cmd_uninstall() {
    if [[ -f "$PLIST_PATH" ]]; then
        launchctl unload "$PLIST_PATH" 2>/dev/null || true
        rm -f "$PLIST_PATH"
        echo "Engine server service removed."
    else
        echo "No service installed at $PLIST_PATH"
    fi
}

cmd_status() {
    if [[ ! -f "$PLIST_PATH" ]]; then
        echo "Not installed (no plist at $PLIST_PATH)"
        return
    fi
    echo "==> launchctl status:"
    launchctl list | grep "$SERVICE_LABEL" || echo "  (not currently loaded)"
    echo ""
    echo "==> Health check (http://localhost:$_port/health):"
    curl -sf "http://localhost:$_port/health" && echo "" || echo "  Server not responding"
}

cmd_logs() {
    tail -f "$LOG_OUT" "$LOG_ERR" 2>/dev/null || echo "No logs yet at $LOG_DIR"
}

case "${1:-}" in
    install)   cmd_install   ;;
    uninstall) cmd_uninstall ;;
    status)    cmd_status    ;;
    logs)      cmd_logs      ;;
    *)
        echo "Usage: $0 {install|uninstall|status|logs}"
        exit 1
        ;;
esac
