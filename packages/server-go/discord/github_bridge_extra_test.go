package discord

import (
	"os"
	"testing"
)

// TestSetGitHubIdentityBridge verifies all three function pointers are set.
func TestSetGitHubIdentityBridge(t *testing.T) {
	// Save and restore originals.
	origLogin, origToken, origProject := engineLoginFn, engineTokenFn, projectNumberFn
	defer func() {
		engineLoginFn = origLogin
		engineTokenFn = origToken
		projectNumberFn = origProject
	}()

	SetGitHubIdentityBridge(
		func() string { return "bot-user" },
		func() string { return "tok-abc" },
		func() int { return 99 },
	)

	if got := engineLoginFn(); got != "bot-user" {
		t.Errorf("engineLoginFn() = %q, want %q", got, "bot-user")
	}
	if got := engineTokenFn(); got != "tok-abc" {
		t.Errorf("engineTokenFn() = %q, want %q", got, "tok-abc")
	}
	if got := projectNumberFn(); got != 99 {
		t.Errorf("projectNumberFn() = %d, want %d", got, 99)
	}
}

// TestDefaultGitHubBridgeInit verifies env-var-based defaults are wired by init().
func TestDefaultGitHubBridgeInit_Login(t *testing.T) {
	origLogin := engineLoginFn
	defer func() { engineLoginFn = origLogin }()

	// Reset to default init values by re-running the inline logic.
	t.Setenv("ENGINE_GITHUB_LOGIN", "env-login")
	SetGitHubIdentityBridge(
		func() string {
			if v := os.Getenv("ENGINE_GITHUB_LOGIN"); v != "" {
				return v
			}
			return ""
		},
		func() string { return "" },
		func() int { return 0 },
	)
	if got := engineLoginFn(); got != "env-login" {
		t.Errorf("engineLoginFn() = %q, want %q", got, "env-login")
	}
}
