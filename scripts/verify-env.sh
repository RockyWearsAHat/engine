#!/bin/bash
set -e

# Verify environment credentials for autonomous build pipeline.
# Required variables:
#   GITHUB_TOKEN - GitHub personal access token (step 3: GitHub API access)
#   ENGINE_GITHUB_OWNER - GitHub repository owner (step 3: repo scaffolding)
#   ENGINE_GITHUB_REPO - GitHub repository name (step 3: repo scaffolding)
#
# Optional variables for advanced features:
#   ENGINE_GITHUB_BOT_TOKEN - Bot-specific token for Discord bridge
#   ENGINE_GITHUB_LOGIN - Bot GitHub login name for commits
#   DISCORD_* - Discord bot integration (step 4+)
#   ANTHROPIC_API_KEY - Claude API key (step 5+)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

required_vars=("GITHUB_TOKEN" "ENGINE_GITHUB_OWNER" "ENGINE_GITHUB_REPO")
missing_vars=()

echo "Verifying environment credentials..."
echo ""

for var in "${required_vars[@]}"; do
  value="${!var}"
  if [ -z "$value" ]; then
    echo -e "${RED}✗${NC} $var: not set"
    missing_vars+=("$var")
  else
    echo -e "${GREEN}✓${NC} $var: set"
  fi
done

echo ""

if [ ${#missing_vars[@]} -gt 0 ]; then
  echo -e "${RED}Error: missing required variables${NC}"
  for var in "${missing_vars[@]}"; do
    echo "  - $var"
  done
  exit 1
fi

# Test GitHub credential if gh CLI is available
if command -v gh &>/dev/null; then
  if gh auth status &>/dev/null; then
    echo -e "${GREEN}✓${NC} GitHub CLI authenticated"
  else
    echo -e "${YELLOW}⚠${NC} GitHub CLI not authenticated"
    echo "  Tip: Run 'gh auth login' for full GitHub integration"
  fi
fi

echo ""
echo -e "${GREEN}Environment validated${NC}"
exit 0
