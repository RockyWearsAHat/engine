package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineToken_PrefersBot(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "bot-tok")
	t.Setenv("GITHUB_TOKEN", "user-tok")
	if got := EngineToken(); got != "bot-tok" {
		t.Errorf("got %q, want bot-tok", got)
	}
}

func TestEngineToken_FallsBackToGitHubToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "user-tok")
	if got := EngineToken(); got != "user-tok" {
		t.Errorf("got %q, want user-tok", got)
	}
}

func TestEngineLogin_FromEnv(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "my-engine-bot")
	if got := EngineLogin(); got != "my-engine-bot" {
		t.Errorf("got %q, want my-engine-bot", got)
	}
}

func TestEngineLogin_ViaAPI(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	engineLoginCached = "" // reset cache

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

func TestEngineDisplayName_Default(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_DISPLAY_NAME", "")
	if got := EngineDisplayName(); got != "Engine" {
		t.Errorf("got %q, want Engine", got)
	}
}

func TestEngineDisplayName_Custom(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_DISPLAY_NAME", "RockyBot")
	if got := EngineDisplayName(); got != "RockyBot" {
		t.Errorf("got %q, want RockyBot", got)
	}
}

func TestAssignEngine_NoToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "bot")
	// No token → best-effort nil
	if err := AssignEngine("o", "r", 1); err != nil {
		t.Errorf("expected nil (best-effort), got %v", err)
	}
}

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

func TestIssueCommentStore_GetSetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".engine"), 0700)
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
	engineLoginCached = "" // ensure we use the env

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

func TestEngineLogin_CacheHit(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "")

	engineLoginMu.Lock()
	engineLoginCached = "cached-bot"
	engineLoginAt = time.Now()
	engineLoginMu.Unlock()
	t.Cleanup(func() {
		engineLoginMu.Lock()
		engineLoginCached = ""
		engineLoginAt = time.Time{}
		engineLoginMu.Unlock()
	})

	if got := EngineLogin(); got != "cached-bot" {
		t.Fatalf("got %q, want cached-bot", got)
	}
}

func TestUnassignEngine_EngineClientError_IsNil(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := UnassignEngine("owner", "repo", 7); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

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

	engineLoginMu.Lock()
	engineLoginCached = ""
	engineLoginAt = time.Time{}
	engineLoginMu.Unlock()

	ConfigureRepoIdentity("/fake/repo")
	if called != 0 {
		t.Fatalf("expected no git config calls, got %d", called)
	}
}

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

func TestGitLocalConfigFn_Default_ReturnsErrorOnNonRepo(t *testing.T) {
	err := gitLocalConfigFn(t.TempDir(), "user.name", "Engine")
	if err == nil {
		t.Fatal("expected gitLocalConfigFn to fail outside a git repo")
	}
}
