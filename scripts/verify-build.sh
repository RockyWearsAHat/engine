#!/bin/bash
set -e

# Build functions - reusable per-language steps
build_go() {
  echo "Building Go..."
  (cd packages/server-go && GOWORK=off go mod tidy)
  (cd packages/server-go && GOWORK=off go build ./...)
}

build_node() {
  echo "Building Node..."
  pnpm install
  pnpm build
}

clean() {
  echo "Cleaning build artifacts..."
  rm -rf node_modules
  (cd packages/server-go && GOWORK=off go clean -cache -modcache)
}

# Main execution
echo "Verifying builds in isolation..."
clean
build_go
build_node

echo "Build verified"
exit 0
