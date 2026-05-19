package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── feedback.go gaps ──────────────────────────────────────────────────────────

func TestDefaultIfEmpty_NonEmpty(t *testing.T) {
	got := defaultIfEmpty("custom", "fallback")
	if got != "custom" {
		t.Errorf("got %q, want custom", got)
	}
}

func TestNormaliseStatusState_ErrorBranch(t *testing.T) {
	if got := normaliseStatusState("error"); got != "error" {
		t.Errorf("error → %q, want error", got)
	}
}

func TestNormaliseStatusState_InProgress(t *testing.T) {
	if got := normaliseStatusState("in_progress"); got != "pending" {
		t.Errorf("in_progress → %q, want pending", got)
	}
}

func TestNormaliseStatusState_Success(t *testing.T) {
	if got := normaliseStatusState("success"); got != "success" {
		t.Errorf("success → %q, want success", got)
	}
}

func TestNormaliseStatusState_Approved(t *testing.T) {
	if got := normaliseStatusState("approved"); got != "success" {
		t.Errorf("approved → %q, want success", got)
	}
}

func TestNormaliseStatusState_Fail(t *testing.T) {
	if got := normaliseStatusState("fail"); got != "failure" {
		t.Errorf("fail → %q, want failure", got)
	}
}

func TestNormaliseStatusState_Rejected(t *testing.T) {
	if got := normaliseStatusState("rejected"); got != "failure" {
		t.Errorf("rejected → %q, want failure", got)
	}
}

func TestNormaliseStatusState_Pending(t *testing.T) {
	if got := normaliseStatusState("pending"); got != "pending" {
		t.Errorf("pending → %q, want pending", got)
	}
}

func TestPostCommitStatus_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`internal error`)) //nolint:errcheck
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")

	err := PostCommitStatus("owner", "repo", "abc123", "pending", "", "desc", "")
	if err == nil {
		t.Error("expected error from non-2xx response")
	}
}

func TestPostIssueComment_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")

	err := PostIssueComment("owner", "repo", 5, "hello")
	if err == nil {
		t.Error("expected error from non-2xx response")
	}
}

func TestPostIssueComment_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	err := PostIssueComment("owner", "repo", 5, "hello")
	// No token → NewClient returns error → function returns nil (best-effort)
	if err != nil {
		t.Errorf("expected nil for no-token, got %v", err)
	}
}

func TestFindHeadSHA_DoGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`not found`)) //nolint:errcheck
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")

	_, err := FindHeadSHA("owner", "repo", "main")
	if err == nil {
		t.Error("expected error from 404 response")
	}
}

func TestFindHeadSHA_NoSHAField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"no_sha_here": "nope"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")

	_, err := FindHeadSHA("owner", "repo", "main")
	if err == nil {
		t.Error("expected error for missing sha field")
	}
}

func TestFindHeadSHA_TruncatedSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// sha field with no closing quote
		w.Write([]byte(`{"sha":"abc123`)) //nolint:errcheck
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")

	_, err := FindHeadSHA("owner", "repo", "main")
	if err == nil {
		t.Error("expected error for truncated sha field")
	}
}

// ── github.go client method error paths ──────────────────────────────────────

func TestAddAssignees_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.AddAssignees(1, []string{"user"}); err == nil {
		t.Error("expected error from non-2xx response")
	}
}

func TestRemoveAssignees_DoRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.RemoveAssignees(1, []string{"user"}); err == nil {
		t.Error("expected error from non-2xx DELETE response")
	}
}

func TestEditComment_DoPatchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.EditComment(99, "new body"); err == nil {
		t.Error("expected error from non-2xx PATCH response")
	}
}

func TestCreatePR_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"Validation Failed"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if _, err := c.CreatePR("title", "body", "feat", "main"); err == nil {
		t.Error("expected error from non-2xx POST response")
	}
}

// ── engagement.go gaps ────────────────────────────────────────────────────────

func TestEngageOnIssueProgress_NoStoredComment_Noop(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck
	// No comment stored → early return, no API call.
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	EngageOnIssueProgress(dir, "owner", "repo", 9, "execute", "building", 1, 3)
	if called {
		t.Error("expected no API call when no stored comment")
	}
}

func TestEngageOnIssueComplete_NoStoredComment_AddsNew(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	var addCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			addCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"id": 5555}) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck
	// No stored comment — code takes the else branch and calls AddComment.

	EngageOnIssueComplete(dir, "owner", "repo", 11, 3, "https://github.com/pr/1")
	if !addCalled {
		t.Error("expected AddComment to be called when no stored comment")
	}
}

func TestEngageOnIssueBlocked_NoStoredComment_AddsNew(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	var addCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			addCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"id": 7777}) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck

	EngageOnIssueBlocked(dir, "owner", "repo", 13, "tests failing")
	if !addCalled {
		t.Error("expected AddComment to be called when no stored comment")
	}
}

func TestEngageOnIssuePickup_NoToken_Noop(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "")

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck

	// Should not panic; EngineClient returns error → returns early.
	EngageOnIssuePickup(dir, "owner", "repo", 1, "sess-x")
}

func TestEngageOnIssuePickup_AssignEngineError_Continues(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	var sawAssignees bool
	var sawComments bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/assignees"):
			sawAssignees = true
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case strings.HasSuffix(r.URL.Path, "/comments"):
			sawComments = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":101}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck

	EngageOnIssuePickup(dir, "owner", "repo", 1, "sess-1")

	if !sawAssignees {
		t.Error("expected assignees endpoint to be called")
	}
	if !sawComments {
		t.Error("expected comments endpoint to be called")
	}
}

func TestEngageOnIssuePickup_AddCommentError_DoesNotStoreComment(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/assignees"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck

	EngageOnIssuePickup(dir, "owner", "repo", 2, "sess-2")

	store := IssueCommentStoreFor(dir)
	if _, ok := store.Get("owner", "repo", 2); ok {
		t.Fatal("expected no stored comment when AddComment fails")
	}
}

func TestEngageOnIssueProgress_EngineClientError_Returns(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck
	store := IssueCommentStoreFor(dir)
	store.Set("owner", "repo", 9, 123)

	EngageOnIssueProgress(dir, "owner", "repo", 9, "execute", "detail", 1, 2)
}

func TestEngageOnIssueComplete_EngineClientError_Returns(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck

	EngageOnIssueComplete(dir, "owner", "repo", 10, 3, "")
}

func TestEngageOnIssueBlocked_EngineClientError_Returns(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".engine"), 0o700) //nolint:errcheck

	EngageOnIssueBlocked(dir, "owner", "repo", 11, "need help")
}

// ── identity.go gaps ──────────────────────────────────────────────────────────

func TestAssignEngine_EmptyLogin_Noop(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	// EngineLogin() returns "" → AssignEngine returns nil immediately.
	if err := AssignEngine("owner", "repo", 5); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestUnassignEngine_EmptyLogin_Noop(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := UnassignEngine("owner", "repo", 5); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestEngineLogin_APIError_ReturnsEmpty(t *testing.T) {
	// Clear cache.
	engineLoginMu.Lock()
	engineLoginCached = ""
	engineLoginAt = engineLoginAt.Add(-2 * engineLoginTTL)
	engineLoginMu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

	login := EngineLogin()
	if login != "" {
		t.Errorf("expected empty login on API error, got %q", login)
	}
}
