package discord

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

func TestDefaultGitHubBridgeInit_TokenPrefersBotToken(t *testing.T) {
	origToken := engineTokenFn
	defer func() { engineTokenFn = origToken }()

	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", " bot-token ")
	t.Setenv("GITHUB_TOKEN", "fallback-token")
	SetGitHubIdentityBridge(
		func() string { return "" },
		func() string {
			if v := os.Getenv("ENGINE_GITHUB_BOT_TOKEN"); v != "" {
				return v
			}
			return os.Getenv("GITHUB_TOKEN")
		},
		func() int { return 0 },
	)

	if got := engineTokenFn(); got != " bot-token " {
		t.Errorf("engineTokenFn() = %q, want bot token", got)
	}
}

func TestDefaultGitHubBridgeInit_TokenFallsBackToGitHubToken(t *testing.T) {
	origToken := engineTokenFn
	defer func() { engineTokenFn = origToken }()

	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "fallback-token")
	SetGitHubIdentityBridge(
		func() string { return "" },
		func() string {
			if v := os.Getenv("ENGINE_GITHUB_BOT_TOKEN"); v != "" {
				return v
			}
			return os.Getenv("GITHUB_TOKEN")
		},
		func() int { return 0 },
	)

	if got := engineTokenFn(); got != "fallback-token" {
		t.Errorf("engineTokenFn() = %q, want fallback token", got)
	}
}

func TestDefaultGitHubBridgeInit_ProjectNumberParse(t *testing.T) {
	origProject := projectNumberFn
	defer func() { projectNumberFn = origProject }()

	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "17")
	SetGitHubIdentityBridge(
		func() string { return "" },
		func() string { return "" },
		func() int {
			raw := os.Getenv("ENGINE_GITHUB_PROJECT_NUMBER")
			n := 0
			_, _ = fmt.Sscanf(raw, "%d", &n)
			return n
		},
	)

	if got := projectNumberFn(); got != 17 {
		t.Errorf("projectNumberFn() = %d, want 17", got)
	}
}

func TestDefaultGitHubBridge_RuntimeClosures(t *testing.T) {
	origLogin, origToken, origProject := engineLoginFn, engineTokenFn, projectNumberFn
	defer func() {
		engineLoginFn = origLogin
		engineTokenFn = origToken
		projectNumberFn = origProject
	}()

	t.Setenv("ENGINE_GITHUB_LOGIN", "runtime-login")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "runtime-bot-token")
	t.Setenv("GITHUB_TOKEN", "runtime-fallback")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "42")

	SetGitHubIdentityBridge(
		func() string { return strings.TrimSpace(os.Getenv("ENGINE_GITHUB_LOGIN")) },
		func() string {
			if v := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_BOT_TOKEN")); v != "" {
				return v
			}
			return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
		},
		func() int {
			raw := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_PROJECT_NUMBER"))
			n, _ := strconv.Atoi(raw)
			return n
		},
	)

	if got := engineLoginFn(); got != "runtime-login" {
		t.Fatalf("engineLoginFn() = %q", got)
	}
	if got := engineTokenFn(); got != "runtime-bot-token" {
		t.Fatalf("engineTokenFn() = %q", got)
	}
	if got := projectNumberFn(); got != 42 {
		t.Fatalf("projectNumberFn() = %d", got)
	}
}

func TestApplyDefaultGitHubBridge_LoginEmptyAndTokenFallbackAndInvalidProjectNumber(t *testing.T) {
	origLogin, origToken, origProject := engineLoginFn, engineTokenFn, projectNumberFn
	defer func() {
		engineLoginFn = origLogin
		engineTokenFn = origToken
		projectNumberFn = origProject
	}()

	t.Setenv("ENGINE_GITHUB_LOGIN", "   ")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", " fallback-token ")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "not-a-number")

	applyDefaultGitHubBridge()

	if got := engineLoginFn(); got != "" {
		t.Fatalf("engineLoginFn() = %q, want empty", got)
	}
	if got := engineTokenFn(); got != "fallback-token" {
		t.Fatalf("engineTokenFn() = %q, want fallback-token", got)
	}
	if got := projectNumberFn(); got != 0 {
		t.Fatalf("projectNumberFn() = %d, want 0 for invalid parse", got)
	}
}

func TestApplyDefaultGitHubBridge_PrefersBotTokenAndParsesProjectNumber(t *testing.T) {
	origLogin, origToken, origProject := engineLoginFn, engineTokenFn, projectNumberFn
	defer func() {
		engineLoginFn = origLogin
		engineTokenFn = origToken
		projectNumberFn = origProject
	}()

	t.Setenv("ENGINE_GITHUB_LOGIN", " octocat ")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", " bot-token ")
	t.Setenv("GITHUB_TOKEN", "fallback-token")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "17")

	applyDefaultGitHubBridge()

	if got := engineLoginFn(); got != "octocat" {
		t.Fatalf("engineLoginFn() = %q, want octocat", got)
	}
	if got := engineTokenFn(); got != "bot-token" {
		t.Fatalf("engineTokenFn() = %q, want bot-token", got)
	}
	if got := projectNumberFn(); got != 17 {
		t.Fatalf("projectNumberFn() = %d, want 17", got)
	}
}
