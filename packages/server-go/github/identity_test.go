package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestEngineToken_PrefersBot verifies ENGINE_GITHUB_BOT_TOKEN takes precedence over GITHUB_TOKEN.
func TestEngineToken_PrefersBot(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "bot-tok")
	t.Setenv("GITHUB_TOKEN", "user-tok")
	if got := EngineToken(); got != "bot-tok" {
		t.Errorf("got %q, want bot-tok", got)
	}
}

// TestEngineToken_FallsBackToGitHubToken verifies fallback to GITHUB_TOKEN when bot token is unset.
func TestEngineToken_FallsBackToGitHubToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "user-tok")
	if got := EngineToken(); got != "user-tok" {
		t.Errorf("got %q, want user-tok", got)
	}
}

// TestEngineLogin_FromEnv verifies EngineLogin reads from ENGINE_GITHUB_LOGIN environment variable.
func TestEngineLogin_FromEnv(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "my-engine-bot")
	if got := EngineLogin(); got != "my-engine-bot" {
		t.Errorf("got %q, want my-engine-bot", got)
	}
}

// TestEngineLogin_ViaAPI verifies EngineLogin queries GitHub API when env var is unset.
// Tests behavioral side effect (HTTP GET to GitHub user API).
func TestEngineLogin_ViaAPI(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	resetEngineLoginCache(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login":"engine-bot"}`))
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

	if got := EngineLogin(); got != "engine-bot" {
		t.Errorf("got %q, want engine-bot", got)
	}
}

// TestEngineDisplayName_Default verifies EngineDisplayName returns "Engine" when env var is unset.
func TestEngineDisplayName_Default(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_DISPLAY_NAME", "")
	if got := EngineDisplayName(); got != "Engine" {
		t.Errorf("got %q, want Engine", got)
	}
}

// TestEngineDisplayName_Custom verifies EngineDisplayName reads from ENGINE_GITHUB_DISPLAY_NAME env var.
func TestEngineDisplayName_Custom(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_DISPLAY_NAME", "RockyBot")
	if got := EngineDisplayName(); got != "RockyBot" {
		t.Errorf("got %q, want RockyBot", got)
	}
}

// TestAssignEngine_NoToken verifies AssignEngine is best-effort when token is missing.
func TestAssignEngine_NoToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "bot")
	// No token → best-effort nil
	if err := AssignEngine("o", "r", 1); err != nil {
		t.Errorf("expected nil (best-effort), got %v", err)
	}
}

// TestAssignEngine_Posts verifies AssignEngine sends POST request with login in assignees field.
// Tests behavioral side effect (HTTP POST to GitHub API).
func TestAssignEngine_Posts(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured) //nolint:errcheck
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")

	if err := AssignEngine("owner", "repo", 42); err != nil {
		t.Fatalf("assign: %v", err)
	}
	logins, ok := captured["assignees"].([]any)
	if !ok || len(logins) == 0 || logins[0] != "engine-bot" {
		t.Errorf("assignees = %v, want [engine-bot]", captured["assignees"])
	}
}

// TestIssueCommentStore_GetSetRoundtrip verifies comment store persists to disk and reloads correctly.
func TestIssueCommentStore_GetSetRoundtrip(t *testing.T) {
	dir := newEngineDir(t)
	s := IssueCommentStoreFor(dir)

	if _, ok := s.Get("o", "r", 1); ok {
		t.Error("expected not found initially")
	}
	s.Set("o", "r", 1, 9999)
	id, ok := s.Get("o", "r", 1)
	if !ok || id != 9999 {
		t.Errorf("get after set = %d, %v, want 9999, true", id, ok)
	}
	// Reload from disk.
	s2 := IssueCommentStoreFor(dir)
	id2, ok2 := s2.Get("o", "r", 1)
	if !ok2 || id2 != 9999 {
		t.Errorf("persisted get = %d, %v", id2, ok2)
	}
}

// TestConfigureRepoIdentity_SetsConfig verifies ConfigureRepoIdentity calls git config with correct name and email.
func TestConfigureRepoIdentity_SetsConfig(t *testing.T) {
	var calls [][]string
	old := gitLocalConfigFn
	defer func() { gitLocalConfigFn = old }()
	gitLocalConfigFn = func(repoPath, key, value string) error {
		calls = append(calls, []string{repoPath, key, value})
		return nil
	}
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_DISPLAY_NAME", "Engine")
	t.Setenv("ENGINE_GITHUB_BOT_EMAIL", "")
	resetEngineLoginCache(t)

	ConfigureRepoIdentity("/fake/repo")

	if len(calls) < 2 {
		t.Fatalf("expected ≥2 git config calls, got %d", len(calls))
	}
	// Check user.name and user.email are set.
	names := map[string]string{}
	for _, c := range calls {
		names[c[1]] = c[2]
	}
	if names["user.name"] != "Engine" {
		t.Errorf("user.name = %q, want Engine", names["user.name"])
	}
	if names["user.email"] != "engine-bot@users.noreply.github.com" {
		t.Errorf("user.email = %q", names["user.email"])
	}
}

// TestEngineLogin_CacheHit verifies EngineLogin returns cached login without making API call.
func TestEngineLogin_CacheHit(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	resetEngineLoginCache(t)
	// Set cache directly after reset
	engineLoginMu.Lock()
	engineLoginCached = "cached-bot"
	engineLoginAt = time.Now()
	engineLoginMu.Unlock()
	t.Cleanup(func() {
		resetEngineLoginCache(t)
	})

	if got := EngineLogin(); got != "cached-bot" {
		t.Fatalf("got %q, want cached-bot", got)
	}
}

// TestUnassignEngine_EngineClientError_IsNil verifies UnassignEngine is best-effort when client creation fails.
func TestUnassignEngine_EngineClientError_IsNil(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := UnassignEngine("owner", "repo", 7); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestConfigureRepoIdentity_NoLogin_Noop verifies ConfigureRepoIdentity skips git config when login is unset.
func TestConfigureRepoIdentity_NoLogin_Noop(t *testing.T) {
	old := gitLocalConfigFn
	defer func() { gitLocalConfigFn = old }()
	called := 0
	gitLocalConfigFn = func(repoPath, key, value string) error {
		called++
		return nil
	}

	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_BOT_EMAIL", "")
	resetEngineLoginCache(t)

	ConfigureRepoIdentity("/fake/repo")
	if called != 0 {
		t.Fatalf("expected no git config calls, got %d", called)
	}
}

// TestConfigureRepoIdentity_GitConfigErrors_AreBestEffort verifies ConfigureRepoIdentity attempts git config even if calls fail.
func TestConfigureRepoIdentity_GitConfigErrors_AreBestEffort(t *testing.T) {
	old := gitLocalConfigFn
	defer func() { gitLocalConfigFn = old }()
	called := 0
	gitLocalConfigFn = func(repoPath, key, value string) error {
		called++
		return os.ErrPermission
	}

	t.Setenv("ENGINE_GITHUB_BOT_EMAIL", "bot@example.com")
	t.Setenv("ENGINE_GITHUB_DISPLAY_NAME", "Engine")

	ConfigureRepoIdentity("/fake/repo")
	if called != 2 {
		t.Fatalf("expected 2 git config attempts, got %d", called)
	}
}

// TestGitLocalConfigFn_Default_ReturnsErrorOnNonRepo verifies gitLocalConfigFn fails outside a git repository.
func TestGitLocalConfigFn_Default_ReturnsErrorOnNonRepo(t *testing.T) {
	err := gitLocalConfigFn(t.TempDir(), "user.name", "Engine")
	if err == nil {
		t.Fatal("expected gitLocalConfigFn to fail outside a git repo")
	}
}
