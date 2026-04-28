package ws

import "testing"

func TestGithubRepoOverride_AllCases(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_OWNER", "")
	t.Setenv("ENGINE_GITHUB_REPO", "")
	owner, repo, ok := githubRepoOverride()
	if owner != "" || repo != "" || ok {
		t.Fatalf("expected empty override, got owner=%q repo=%q ok=%v", owner, repo, ok)
	}

	t.Setenv("ENGINE_GITHUB_OWNER", "octo")
	t.Setenv("ENGINE_GITHUB_REPO", "")
	owner, repo, ok = githubRepoOverride()
	if owner != "octo" || repo != "" || !ok {
		t.Fatalf("expected owner-only override, got owner=%q repo=%q ok=%v", owner, repo, ok)
	}

	t.Setenv("ENGINE_GITHUB_OWNER", "")
	t.Setenv("ENGINE_GITHUB_REPO", "demo")
	owner, repo, ok = githubRepoOverride()
	if owner != "" || repo != "demo" || !ok {
		t.Fatalf("expected repo-only override, got owner=%q repo=%q ok=%v", owner, repo, ok)
	}
}

func TestDiscordBridgeAndPairingAccessors(t *testing.T) {
	SetDiscordBridge(nil)
	if GetDiscordBridge() != nil {
		t.Fatal("expected nil discord bridge after reset")
	}

	SetPairingManager(nil)
}

// ── default injectable fn bodies ──────────────────────────────────────────────

func TestGithubClientIDFn_Default(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test-id-123")
	orig := githubClientIDFn
	t.Cleanup(func() { githubClientIDFn = orig })

	got := orig() // invoke the default closure body
	if got != "test-id-123" {
		t.Fatalf("expected test-id-123, got %q", got)
	}
}
