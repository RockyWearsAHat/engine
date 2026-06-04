package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Test gaps for feedback.go, github.go (client methods), engagement.go.
// See github_test.go for shared helpers: newServerWithStatus, setupGitHubAPI, newEngineDir, setupEngineEnv, resetEngineLoginCache.

// ── feedback.go gaps ──────────────────────────────────────────────────────────

// TestDefaultIfEmpty_NonEmpty verifies that defaultIfEmpty returns the first non-empty value.
func TestDefaultIfEmpty_NonEmpty(t *testing.T) {
	got := defaultIfEmpty("custom", "fallback")
	if got != "custom" {
		t.Errorf("got %q, want custom", got)
	}
}

// TestNormaliseStatusState_ErrorBranch verifies error state is passed through unchanged.
func TestNormaliseStatusState_ErrorBranch(t *testing.T) {
	if got := normaliseStatusState("error"); got != "error" {
		t.Errorf("error → %q, want error", got)
	}
}

// TestNormaliseStatusState_InProgress verifies in_progress maps to pending.
func TestNormaliseStatusState_InProgress(t *testing.T) {
	if got := normaliseStatusState("in_progress"); got != "pending" {
		t.Errorf("in_progress → %q, want pending", got)
	}
}

// TestNormaliseStatusState_Success verifies success state is passed through unchanged.
func TestNormaliseStatusState_Success(t *testing.T) {
	if got := normaliseStatusState("success"); got != "success" {
		t.Errorf("success → %q, want success", got)
	}
}

// TestNormaliseStatusState_Approved verifies approved maps to success.
func TestNormaliseStatusState_Approved(t *testing.T) {
	if got := normaliseStatusState("approved"); got != "success" {
		t.Errorf("approved → %q, want success", got)
	}
}

// TestNormaliseStatusState_Fail verifies fail maps to failure.
func TestNormaliseStatusState_Fail(t *testing.T) {
	if got := normaliseStatusState("fail"); got != "failure" {
		t.Errorf("fail → %q, want failure", got)
	}
}

// TestNormaliseStatusState_Rejected verifies rejected maps to failure.
func TestNormaliseStatusState_Rejected(t *testing.T) {
	if got := normaliseStatusState("rejected"); got != "failure" {
		t.Errorf("rejected → %q, want failure", got)
	}
}

// TestNormaliseStatusState_Pending verifies pending state is passed through unchanged.
func TestNormaliseStatusState_Pending(t *testing.T) {
	if got := normaliseStatusState("pending"); got != "pending" {
		t.Errorf("pending → %q, want pending", got)
	}
}

// TestPostCommitStatus_DoPostError verifies PostCommitStatus returns error on non-2xx response.
// Tests behavioral side effect (HTTP POST to commit status API).
func TestPostCommitStatus_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`internal error`)) //nolint:errcheck
	}))
	defer srv.Close()

	setupGitHubAPI(t, srv, "tok")

	err := PostCommitStatus("owner", "repo", "abc123", "pending", "", "desc", "")
	if err == nil {
		t.Error("expected error from non-2xx response")
	}
}

// TestPostIssueComment_DoPostError verifies PostIssueComment returns error on non-2xx response.
// Tests behavioral side effect (HTTP POST to issue comment API).
func TestPostIssueComment_DoPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	setupGitHubAPI(t, srv, "tok")

	err := PostIssueComment("owner", "repo", 5, "hello")
	if err == nil {
		t.Error("expected error from non-2xx response")
	}
}

// TestPostIssueComment_NoToken verifies PostIssueComment is best-effort when token is missing.
func TestPostIssueComment_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	err := PostIssueComment("owner", "repo", 5, "hello")
	// No token → NewClient returns error → function returns nil (best-effort)
	if err != nil {
		t.Errorf("expected nil for no-token, got %v", err)
	}
}

// TestFindHeadSHA_DoGetError verifies FindHeadSHA returns error on 404 response.
// Tests behavioral side effect (HTTP GET to branch API).
func TestFindHeadSHA_DoGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`not found`)) //nolint:errcheck
	}))
	defer srv.Close()

	setupGitHubAPI(t, srv, "tok")

	_, err := FindHeadSHA("owner", "repo", "main")
	if err == nil {
		t.Error("expected error from 404 response")
	}
}

// TestFindHeadSHA_NoSHAField verifies FindHeadSHA returns error when sha field is missing.
func TestFindHeadSHA_NoSHAField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"no_sha_here": "nope"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	setupGitHubAPI(t, srv, "tok")

	_, err := FindHeadSHA("owner", "repo", "main")
	if err == nil {
		t.Error("expected error for missing sha field")
	}
}

// TestFindHeadSHA_TruncatedSHA verifies FindHeadSHA returns error for truncated JSON response.
func TestFindHeadSHA_TruncatedSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// sha field with no closing quote
		w.Write([]byte(`{"sha":"abc123`)) //nolint:errcheck
	}))
	defer srv.Close()

	setupGitHubAPI(t, srv, "tok")

	_, err := FindHeadSHA("owner", "repo", "main")
	if err == nil {
		t.Error("expected error for truncated sha field")
	}
}

// ── github.go client method error paths ──────────────────────────────────────

// TestAddAssignees_DoPostError verifies AddAssignees returns error on non-2xx response.
func TestAddAssignees_DoPostError(t *testing.T) {
	srv := newServerWithStatus(t, http.StatusInternalServerError)
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.AddAssignees(1, []string{"user"}); err == nil {
		t.Error("expected error from non-2xx response")
	}
}

// TestRemoveAssignees_DoRequestError verifies RemoveAssignees returns error on DELETE failure.
func TestRemoveAssignees_DoRequestError(t *testing.T) {
	srv := newServerWithStatus(t, http.StatusInternalServerError)
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.RemoveAssignees(1, []string{"user"}); err == nil {
		t.Error("expected error from non-2xx DELETE response")
	}
}

// TestEditComment_DoPatchError verifies EditComment returns error on PATCH failure.
func TestEditComment_DoPatchError(t *testing.T) {
	srv := newServerWithStatus(t, http.StatusInternalServerError)
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	if err := c.EditComment(99, "new body"); err == nil {
		t.Error("expected error from non-2xx PATCH response")
	}
}

// TestCreatePR_DoPostError verifies CreatePR returns error on non-2xx POST response.
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

// TestEngageOnIssueProgress_NoStoredComment_Noop verifies EngageOnIssueProgress skips API call when no comment stored.
func TestEngageOnIssueProgress_NoStoredComment_Noop(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	dir := newEngineDir(t)
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

// TestEngageOnIssueComplete_NoStoredComment_AddsNew verifies EngageOnIssueComplete posts new comment when store is empty.
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

	dir := newEngineDir(t)
	// No stored comment — code takes the else branch and calls AddComment.

	EngageOnIssueComplete(dir, "owner", "repo", 11, 3, "https://github.com/pr/1")
	if !addCalled {
		t.Error("expected AddComment to be called when no stored comment")
	}
}

// TestEngageOnIssueBlocked_NoStoredComment_AddsNew verifies EngageOnIssueBlocked posts new comment when store is empty.
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

	dir := newEngineDir(t)

	EngageOnIssueBlocked(dir, "owner", "repo", 13, "tests failing")
	if !addCalled {
		t.Error("expected AddComment to be called when no stored comment")
	}
}

// TestEngageOnIssuePickup_NoToken_Noop verifies EngageOnIssuePickup is best-effort when token is missing.
func TestEngageOnIssuePickup_NoToken_Noop(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "")

	dir := newEngineDir(t)

	// Should not panic; EngineClient returns error → returns early.
	EngageOnIssuePickup(dir, "owner", "repo", 1, "sess-x")
}

// TestEngageOnIssuePickup_AssignEngineError_Continues verifies EngageOnIssuePickup continues on assignee failure.
func TestEngageOnIssuePickup_AssignEngineError_Continues(t *testing.T) {
	setupEngineEnv(t)

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

	dir := newEngineDir(t)

	EngageOnIssuePickup(dir, "owner", "repo", 1, "sess-1")

	if !sawAssignees {
		t.Error("expected assignees endpoint to be called")
	}
	if !sawComments {
		t.Error("expected comments endpoint to be called")
	}
}

// TestEngageOnIssuePickup_AddCommentError_DoesNotStoreComment verifies failed comment posts are not cached.
func TestEngageOnIssuePickup_AddCommentError_DoesNotStoreComment(t *testing.T) {
	setupEngineEnv(t)

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

	dir := newEngineDir(t)

	EngageOnIssuePickup(dir, "owner", "repo", 2, "sess-2")

	store := IssueCommentStoreFor(dir)
	if _, ok := store.Get("owner", "repo", 2); ok {
		t.Fatal("expected no stored comment when AddComment fails")
	}
}

// TestEngageOnIssueProgress_EngineClientError_Returns verifies EngageOnIssueProgress is best-effort when client creation fails.
func TestEngageOnIssueProgress_EngineClientError_Returns(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := newEngineDir(t)
	store := IssueCommentStoreFor(dir)
	store.Set("owner", "repo", 9, 123)

	EngageOnIssueProgress(dir, "owner", "repo", 9, "execute", "detail", 1, 2)
}

// TestEngageOnIssueComplete_EngineClientError_Returns verifies EngageOnIssueComplete is best-effort when client creation fails.
func TestEngageOnIssueComplete_EngineClientError_Returns(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := newEngineDir(t)

	EngageOnIssueComplete(dir, "owner", "repo", 10, 3, "")
}

// TestEngageOnIssueBlocked_EngineClientError_Returns verifies EngageOnIssueBlocked is best-effort when client creation fails.
func TestEngageOnIssueBlocked_EngineClientError_Returns(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := newEngineDir(t)

	EngageOnIssueBlocked(dir, "owner", "repo", 11, "need help")
}
