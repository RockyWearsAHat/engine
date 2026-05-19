package discord

// github_bridge.go wires the Discord service's Engine-identity helpers to the
// github package without creating an import cycle.
//
// The github package does not import discord; discord does not import github.
// main.go is the injection site: it calls SetGitHubIdentityBridge after both
// packages are initialised.

import (
	"os"
	"strconv"
	"strings"
)

// SetGitHubIdentityBridge configures the discord package's GitHub identity
// shims. Call this from main.go after the github package is ready.
func SetGitHubIdentityBridge(loginFn func() string, tokenFn func() string, projectNumberFn_ func() int) {
	engineLoginFn = loginFn
	engineTokenFn = tokenFn
	projectNumberFn = projectNumberFn_
}

// defaultGitHubBridge wires default env-var-based implementations so the
// discord package works out of the box even when main.go skips the call.
func init() {
	applyDefaultGitHubBridge()
}

// applyDefaultGitHubBridge configures env-backed default identity callbacks.
// Kept separate from init so tests can exercise both env branches deterministically.
func applyDefaultGitHubBridge() {
	engineLoginFn = func() string {
		if v := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_LOGIN")); v != "" {
			return v
		}
		return ""
	}
	engineTokenFn = func() string {
		if v := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_BOT_TOKEN")); v != "" {
			return v
		}
		return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	projectNumberFn = func() int {
		raw := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_PROJECT_NUMBER"))
		n, _ := strconv.Atoi(raw)
		return n
	}
}
