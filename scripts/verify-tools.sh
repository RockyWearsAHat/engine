#!/bin/bash

# Verify system toolchain completeness
# Checks for required tools and their versions

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

MISSING=()
FOUND=0
TOTAL=0

check_tool() {
  local tool=$1
  local name=${2:-$tool}
  local version_flag=${3:---version}
  TOTAL=$((TOTAL + 1))

  if command -v "$tool" &> /dev/null; then
    if [ "$version_flag" != "none" ]; then
      # Try the specified flag first, then fallback to common alternatives
      version=$("$tool" $version_flag 2>&1 | head -1)
      if [ $? -ne 0 ] || [[ "$version" =~ "flag provided but not defined" ]]; then
        # Fallback 1: no flag (for go, gofmt)
        version=$("$tool" 2>&1 | head -1) || version="found"
        if [[ "$version" =~ "flag provided" ]] || [ $? -ne 0 ]; then
          version="found"
        fi
      fi
      [ -z "$version" ] && version="found"
      echo -e "${GREEN}✓${NC} $name: $version"
    else
      echo -e "${GREEN}✓${NC} $name: found"
    fi
    FOUND=$((FOUND + 1))
  else
    echo -e "${RED}✗${NC} $name: not found"
    MISSING+=("$name")
  fi
}

echo "Checking required tools..."
echo ""

check_tool "pnpm" "pnpm"
check_tool "node" "node"
check_tool "go" "go"
check_tool "git" "git"
check_tool "gh" "GitHub CLI"
check_tool "gofmt" "gofmt"
check_tool "sqlite3" "sqlite3"
check_tool "python3" "python3"
check_tool "cargo" "Cargo" "--version"

# Check for Tauri CLI
if command -v cargo &> /dev/null && cargo install --list 2>/dev/null | grep -q "tauri"; then
  echo -e "${GREEN}✓${NC} Tauri CLI: found"
  FOUND=$((FOUND + 1))
else
  echo -e "${YELLOW}⚠${NC} Tauri CLI: not installed (run: cargo install tauri-cli)"
  MISSING+=("Tauri CLI")
fi
TOTAL=$((TOTAL + 1))

echo ""
echo "Summary: $FOUND/$TOTAL tools found"

if [ ${#MISSING[@]} -eq 0 ]; then
  echo -e "${GREEN}All required tools found${NC}"
  exit 0
else
  echo -e "${RED}Missing tools:${NC}"
  for tool in "${MISSING[@]}"; do
    echo "  - $tool"
  done
  exit 1
fi
