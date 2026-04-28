#!/usr/bin/env bash
# discord-screenshot.sh — captures Discord (app or browser tab) non-interactively on macOS
# Usage: bash scripts/discord-screenshot.sh [output.png]
# Output defaults to /tmp/discord-snap-<timestamp>.png

set -euo pipefail

PYTHON=/Users/alexwaldmann/Desktop/MyEditor/.venv/bin/python
OUT="${1:-/tmp/discord-snap-$(date +%s).png}"

# ── Step 1: Try native Discord app (all spaces, not just current) ────────────
WID=$("$PYTHON" - <<'EOF' 2>/dev/null
import Quartz
# kCGWindowListOptionAll = 0 — includes off-screen/other-space windows
wlist = Quartz.CGWindowListCopyWindowInfo(0, Quartz.kCGNullWindowID)
best = None
for w in wlist:
    owner = w.get("kCGWindowOwnerName", "")
    if owner.lower() != "discord":
        continue
    bounds = w.get("kCGWindowBounds", {})
    area = bounds.get("Width", 0) * bounds.get("Height", 0)
    if best is None or area > best[1]:
        best = (w.get("kCGWindowNumber", 0), area)
if best:
    print(best[0])
EOF
)

if [[ -n "$WID" && "$WID" != "0" ]]; then
  echo "Found Discord app window (wid=$WID) — activating and capturing..."
  osascript -e 'tell application "Discord" to activate' 2>/dev/null || true
  sleep 0.5
  screencapture -x -l "$WID" "$OUT"
  echo "Saved: $OUT"
  exit 0
fi

# ── Step 2: Discord not found as app — try launching it ─────────────────────
echo "Discord app window not found — attempting to launch Discord..."
open -a Discord 2>/dev/null || true
sleep 2.0

WID=$("$PYTHON" - <<'EOF' 2>/dev/null
import Quartz
wlist = Quartz.CGWindowListCopyWindowInfo(0, Quartz.kCGNullWindowID)
best = None
for w in wlist:
    if w.get("kCGWindowOwnerName","").lower() != "discord":
        continue
    bounds = w.get("kCGWindowBounds", {})
    area = bounds.get("Width", 0) * bounds.get("Height", 0)
    if best is None or area > best[1]:
        best = (w.get("kCGWindowNumber", 0), area)
if best:
    print(best[0])
EOF
)

if [[ -n "$WID" && "$WID" != "0" ]]; then
  echo "Discord launched (wid=$WID) — capturing..."
  screencapture -x -l "$WID" "$OUT"
  echo "Saved: $OUT"
  exit 0
fi

# ── Step 3: Check browser tabs for Discord ───────────────────────────────────
echo "Discord app not available — checking Chrome tabs..."
TAB_WID=$(osascript <<'APPLESCRIPT' 2>/dev/null
tell application "Google Chrome"
  set winIdx to 0
  repeat with w in windows
    set winIdx to winIdx + 1
    set tabIdx to 0
    repeat with t in tabs of w
      set tabIdx to tabIdx + 1
      if URL of t contains "discord.com" then
        set index of w to 1
        set active tab index of w to tabIdx
        delay 0.5
        return id of w
      end if
    end repeat
  end repeat
  return ""
end tell
APPLESCRIPT
)

if [[ -n "$TAB_WID" ]]; then
  echo "Found Discord in Chrome (window id=$TAB_WID) — capturing..."
  CHROME_WID=$("$PYTHON" - <<EOF 2>/dev/null
import Quartz
wlist = Quartz.CGWindowListCopyWindowInfo(0, Quartz.kCGNullWindowID)
best = None
for w in wlist:
    if "chrome" not in w.get("kCGWindowOwnerName","").lower():
        continue
    bounds = w.get("kCGWindowBounds", {})
    area = bounds.get("Width", 0) * bounds.get("Height", 0)
    if best is None or area > best[1]:
        best = (w.get("kCGWindowNumber", 0), area)
if best:
    print(best[0])
EOF
  )
  if [[ -n "$CHROME_WID" ]]; then
    screencapture -x -l "$CHROME_WID" "$OUT"
    echo "Saved: $OUT"
    exit 0
  fi
fi

# ── Step 4: All methods failed ───────────────────────────────────────────────
echo "ERROR: Could not find Discord in app or browser. Make sure Discord is open." >&2
exit 1
