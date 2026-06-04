package github

import (
	"net/http"
	"testing"
)

// ── Test Coverage: github.go client methods (RemoveAssignees, AddAssignees, CreatePR) ────────
//
// Shared public test utilities (defined in github_test.go):
//   • newServerWithStatus(t, status) - Returns httptest.Server with fixed status code
//   • newClientWithBase(base) - Creates *Client with custom test server base URL
//     (uses rebaseTransport to rewrite api.github.com → test server)

// TestRemoveAssignees_EmptyNoop verifies RemoveAssignees is a no-op on empty assignee list.
func TestRemoveAssignees_EmptyNoop(t *testing.T) {
	c := newClientWithBase("http://example.com")
	err := c.RemoveAssignees(1, nil)
	assertHTTPSuccess(t, err, "RemoveAssignees empty list")
}

// TestAddAssignees_EmptyNoop verifies AddAssignees is a no-op on empty assignee list.
func TestAddAssignees_EmptyNoop(t *testing.T) {
	c := newClientWithBase("http://example.com")
	err := c.AddAssignees(1, nil)
	assertHTTPSuccess(t, err, "AddAssignees empty list")
}

// TestRemoveAssignees_Success verifies RemoveAssignees sends DELETE request with correct payload.
// Tests behavioral side effect (HTTP DELETE to GitHub API).
func TestRemoveAssignees_Success(t *testing.T) {
	var method string
	payload := make(map[string]any)
	srv := newTestServer(t, captureHTTPRequestHandler(t, &method, &payload))
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	err := c.RemoveAssignees(9, []string{"engine-bot"})
	assertHTTPSuccess(t, err, "RemoveAssignees")
	assertHTTPMethod(t, method, http.MethodDelete)
	assertHTTPPayloadSlice(t, payload, "assignees", []string{"engine-bot"})
}

// TestRemoveAssignees_DoRequestError (behavior tested in github_gaps_test.go).
// TestAddAssignees_DoPostError (behavior tested in github_gaps_test.go).

// TestRemoveAssignees_InvalidBase_RequestError verifies error handling for invalid API base URL.
func TestRemoveAssignees_InvalidBase_RequestError(t *testing.T) {
	t.Setenv("GITHUB_API_BASE", "://bad")
	c := NewClientWithToken("owner", "repo", "tok")

	if err := c.RemoveAssignees(1, []string{"engine-bot"}); err == nil {
		t.Fatal("expected request construction error")
	}
}

// TestCreatePR_Success verifies CreatePR successfully parses PR response from GitHub API.
// Tests behavioral side effect (HTTP POST to GitHub PR creation endpoint).
func TestCreatePR_Success(t *testing.T) {
	srv := newJSONResponseServer(t, map[string]any{
		"number":   17,
		"html_url": "https://example/pr/17",
		"state":    "open",
	}, http.StatusCreated)
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	pr, err := c.CreatePR("title", "body", "feature", "main")
	assertHTTPSuccess(t, err, "CreatePR")
	if pr.Number != 17 || pr.State != "open" {
		t.Fatalf("unexpected PR: %+v", pr)
	}
}

// TestCreatePR_ParseError verifies CreatePR returns error when response is not valid JSON.
func TestCreatePR_ParseError(t *testing.T) {
	srv := newResponseServer(t, http.StatusCreated, "not-json")
	defer srv.Close()

	c := newClientWithBase(srv.URL)
	_, err := c.CreatePR("title", "body", "feature", "main")
	assertHTTPError(t, err, "CreatePR parse error")
}

// TestUnassignEngine_Success verifies UnassignEngine sends DELETE request to GitHub API.
// Tests behavioral side effect (HTTP DELETE to remove assignee).
func TestUnassignEngine_Success(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

	srv := newServerWithStatus(t, http.StatusOK)
	defer srv.Close()

	setupGitHubAPI(t, srv, "")
	err := UnassignEngine("owner", "repo", 22)
	assertHTTPSuccess(t, err, "UnassignEngine")
}
