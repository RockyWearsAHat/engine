package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupFakeGitHub registers a test server that handles both assign (POST
// /assignees) and comment (POST /issues/N/comments, PATCH /comments/N) calls.
func setupFakeGitHub(t *testing.T) (serverURL string, assignCalled *bool, commentBody *string, editBody *string) {
	t.Helper()
	assigned := false
	var lastComment, lastEdit string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		path := r.URL.Path

		switch {
		case strings.Contains(path, "/assignees"):
			assigned = true
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))

		case r.Method == "POST" && strings.Contains(path, "/comments"):
			var p map[string]string
			json.Unmarshal(body, &p)
			lastComment = p["body"]
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":1001}`))

		case r.Method == "PATCH" && strings.Contains(path, "/comments/"):
			var p map[string]string
			json.Unmarshal(body, &p)
			lastEdit = p["body"]
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))

		default:
			// GetAuthenticatedLogin
			if strings.HasSuffix(path, "/user") {
				w.Write([]byte(`{"login":"engine-bot"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)

	return srv.URL, &assigned, &lastComment, &lastEdit
}

func TestEngageOnIssuePickup_AssignsAndComments(t *testing.T) {
	url, assigned, comment, _ := setupFakeGitHub(t)
	t.Setenv("GITHUB_API_BASE", url)
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "") // disable project board

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".engine"), 0700)

	EngageOnIssuePickup(dir, "owner", "repo", 7, "sess-001")

	if !*assigned {
		t.Error("expected assignee call")
	}
	if !strings.Contains(*comment, "sess-001") {
		t.Errorf("kickoff comment missing session id, got: %q", *comment)
	}
	if !strings.Contains(*comment, "engine-bot") {
		t.Errorf("kickoff comment missing login, got: %q", *comment)
	}

	// Comment ID should have been stored.
	store := IssueCommentStoreFor(dir)
	id, ok := store.Get("owner", "repo", 7)
	if !ok || id != 1001 {
		t.Errorf("stored comment id = %d, %v, want 1001, true", id, ok)
	}
}

func TestEngageOnIssueProgress_EditsComment(t *testing.T) {
	url, _, _, editBody := setupFakeGitHub(t)
	t.Setenv("GITHUB_API_BASE", url)
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".engine"), 0700)
	store := IssueCommentStoreFor(dir)
	store.Set("owner", "repo", 5, 2002)

	EngageOnIssueProgress(dir, "owner", "repo", 5, "execute", "building", 2, 5)

	if !strings.Contains(strings.ToLower(*editBody), "execute") {
		t.Errorf("progress edit missing phase, got: %q", *editBody)
	}
	if !strings.Contains(*editBody, "2 / 5") {
		t.Errorf("progress edit missing step count, got: %q", *editBody)
	}
}

func TestEngageOnIssueComplete_EditsComment(t *testing.T) {
	url, _, _, editBody := setupFakeGitHub(t)
	t.Setenv("GITHUB_API_BASE", url)
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".engine"), 0700)
	store := IssueCommentStoreFor(dir)
	store.Set("owner", "repo", 3, 3003)

	EngageOnIssueComplete(dir, "owner", "repo", 3, 4, "https://github.com/o/r/pull/99")

	if !strings.Contains(*editBody, "Done") {
		t.Errorf("completion edit missing 'Done', got: %q", *editBody)
	}
	if !strings.Contains(*editBody, "pull/99") {
		t.Errorf("completion edit missing PR URL, got: %q", *editBody)
	}
}

func TestEngageOnIssueBlocked_EditsComment(t *testing.T) {
	url, _, _, editBody := setupFakeGitHub(t)
	t.Setenv("GITHUB_API_BASE", url)
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".engine"), 0700)
	store := IssueCommentStoreFor(dir)
	store.Set("owner", "repo", 8, 4004)

	EngageOnIssueBlocked(dir, "owner", "repo", 8, "missing API key")

	if !strings.Contains(*editBody, "Blocked") {
		t.Errorf("blocked edit missing 'Blocked', got: %q", *editBody)
	}
	if !strings.Contains(*editBody, "missing API key") {
		t.Errorf("blocked edit missing reason, got: %q", *editBody)
	}
}
