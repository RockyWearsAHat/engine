package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────────
// Public Test Utilities — GraphQL and Project Board Testing
// ────────────────────────────────────────────────────────────────────────────────

// errReadCloser is a mock io.ReadCloser that fails on Read with io.ErrUnexpectedEOF.
// Used to test error paths in response body parsing.
type errReadCloser struct{}

func (errReadCloser) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReadCloser) Close() error               { return nil }

// roundTripperFunc is a functional adapter for http.RoundTripper.
// Allows inline http.Transport mock for testing custom response scenarios.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// decodeGraphQLRequest unmarshals a GraphQL query JSON from the request body.
// Extracts the 'query' field from GitHub GraphQL API request payloads.
func decodeGraphQLRequest(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// setupProjectEnv sets ENGINE_GITHUB_{PROJECT_NUMBER,PROJECT_OWNER,BOT_TOKEN,LOGIN} variables.
// Centralizes environment setup pattern for project board integration tests.
// Non-positive projectNumber and empty strings are skipped (no-op).
func setupProjectEnv(t *testing.T, projectNumber int, projectOwner, token, login string) {
	t.Helper()
	if projectNumber > 0 {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", string(rune('0'+projectNumber/10))+""+string(rune('0'+projectNumber%10)))
	}
	if projectOwner != "" {
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", projectOwner)
	}
	if token != "" {
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", token)
	}
	if login != "" {
		t.Setenv("ENGINE_GITHUB_LOGIN", login)
	}
}

// setupGraphQLServer creates a test HTTP server with custom handler and wires eventsHTTPClient.
// Sets GITHUB_API_BASE to the server URL and restores original eventsHTTPClient via t.Cleanup.
// Use with setupProjectEnv for full GraphQL mutation/query testing.
func setupGraphQLServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	t.Cleanup(func() {
		eventsHTTPClient = oldHTTP
		srv.Close()
	})
	return srv
}

// TestProjectOwnerAndProjectNumber verifies project configuration parsing from environment.
func TestProjectOwnerAndProjectNumber(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "  octo-org  ")
	if got := projectOwner(); got != "octo-org" {
		t.Fatalf("projectOwner() = %q, want octo-org", got)
	}

	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	if got := projectOwner(); got != "engine-bot" {
		t.Fatalf("projectOwner() fallback = %q, want engine-bot", got)
	}

	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", " 42 ")
	if got := projectNumber(); got != 42 {
		t.Fatalf("projectNumber() = %d, want 42", got)
	}

	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "not-a-number")
	if got := projectNumber(); got != 0 {
		t.Fatalf("projectNumber() invalid = %d, want 0", got)
	}
}

// TestGraphqlDo_ErrorPaths verifies error handling for malformed GraphQL requests.
func TestGraphqlDo_ErrorPaths(t *testing.T) {
	t.Setenv("GITHUB_API_BASE", "http://127.0.0.1:1")
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = &http.Client{}
	t.Cleanup(func() { eventsHTTPClient = oldHTTP })

	if err := graphqlDo("tok", "query", map[string]any{"bad": make(chan int)}, nil); err == nil {
		t.Fatal("expected marshal error")
	}

	if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
		t.Fatal("expected request/do error")
	}
}

// TestGetProjectV2ID_FallsBackToOrg verifies project lookup falls back from user to organization scope.
// Tests behavioral side effect (HTTP POST to GitHub GraphQL API).
func TestGetProjectV2ID_FallsBackToOrg(t *testing.T) {
	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		graphQLMatch("user(login", `{"errors":[{"message":"user not found"}]}`),
		graphQLMatch("organization", `{"data":{"organization":{"projectV2":{"id":"P_ORG"}}}}`),
	))

	id, err := getProjectV2ID("tok", "octo", 3)
	if err != nil {
		t.Fatalf("getProjectV2ID: %v", err)
	}
	if id != "P_ORG" {
		t.Fatalf("id = %q, want P_ORG", id)
	}
}

// TestGetIssueNodeID_NotFound verifies error when issue node ID cannot be resolved.
// Tests behavioral side effect (HTTP POST to GitHub GraphQL API).
func TestGetIssueNodeID_NotFound(t *testing.T) {
	setupGraphQLServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"repository":{"issue":{"id":""}}}}`)
	}))

	_, err := getIssueNodeID("tok", "owner", "repo", 99)
	if err == nil {
		t.Fatal("expected not found error")
	}
}

// TestAddIssueToEngineProject_BestEffortWhenNotConfigured verifies function gracefully handles unconfigured project.
func TestAddIssueToEngineProject_BestEffortWhenNotConfigured(t *testing.T) {
	setupProjectEnv(t, 0, "", "", "")

	itemID, err := AddIssueToEngineProject("owner", "repo", 1)
	if err != nil {
		t.Fatalf("expected nil error when not configured, got %v", err)
	}
	if itemID != "" {
		t.Fatalf("expected empty item id, got %q", itemID)
	}
}

// TestAddIssueToEngineProject_Success verifies issue is successfully added to GitHub project.
// Tests behavioral side effect (HTTP POST to GitHub GraphQL mutations).
func TestAddIssueToEngineProject_Success(t *testing.T) {
	setupProjectEnv(t, 3, "octo", "tok", "engine-bot")
	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		graphQLMatch("projectV2(number", `{"data":{"user":{"projectV2":{"id":"P1"}}}}`),
		graphQLMatch("issue(number", `{"data":{"repository":{"issue":{"id":"I1"}}}}`),
		graphQLMatch("addProjectV2ItemById", `{"data":{"addProjectV2ItemById":{"item":{"id":"ITEM1"}}}}`),
	))

	itemID, err := AddIssueToEngineProject("owner", "repo", 7)
	if err != nil {
		t.Fatalf("AddIssueToEngineProject: %v", err)
	}
	if itemID != "ITEM1" {
		t.Fatalf("itemID = %q, want ITEM1", itemID)
	}
}

func TestUpdateProjectItemStatus_SuccessAndSkip(t *testing.T) {
	setupProjectEnv(t, 9, "octo", "tok", "engine-bot")

	calls := 0
	counter := func(m graphQLQueryMatcher) graphQLQueryMatcher {
		return func(query string) (bool, string) {
			calls++
			return m(query)
		}
	}

	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		counter(graphQLMatch("projectV2(number", `{"data":{"user":{"projectV2":{"id":"P1"}}}}`)),
		counter(graphQLMatch("fields(first", `{"data":{"node":{"fields":{"nodes":[{"id":"F1","name":"Status","options":[{"id":"OPT1","name":"Done"},{"id":"OPT2","name":"In Progress"}]}]}}}}`)),
		counter(graphQLMatch("updateProjectV2ItemFieldValue", `{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"ITEM1"}}}}`)),
	))

	if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err != nil {
		t.Fatalf("UpdateProjectItemStatus: %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected at least 3 graphql calls, got %d", calls)
	}

	// Skip path: no item ID should no-op.
	if err := UpdateProjectItemStatus("owner", "", "Done"); err != nil {
		t.Fatalf("expected nil on skip path, got %v", err)
	}
}

func TestUpdateProjectItemStatus_FieldOrOptionMissingSkips(t *testing.T) {
	setupProjectEnv(t, 9, "octo", "tok", "")

	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		graphQLMatch("projectV2(number", `{"data":{"user":{"projectV2":{"id":"P1"}}}}`),
		graphQLMatch("fields(first", `{"data":{"node":{"fields":{"nodes":[{"id":"X","name":"Priority","options":[{"id":"P1","name":"High"}]}]}}}}`),
		graphQLMatchFallthrough(`{"data":{}}`),
	))

	if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err != nil {
		t.Fatalf("expected nil when status option missing, got %v", err)
	}
}

func TestGraphqlDo_StatusAndParseErrors(t *testing.T) {
	t.Run("non-2xx status", func(t *testing.T) {
		t.Setenv("GITHUB_API_BASE", "http://127.0.0.1:1")
		oldHTTP := eventsHTTPClient
		eventsHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`boom`)),
				Header:     make(http.Header),
			}, nil
		})}
		t.Cleanup(func() { eventsHTTPClient = oldHTTP })

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected status error")
		}
	})

	t.Run("invalid json response", func(t *testing.T) {
		setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
			graphQLMatchFallthrough(`not-json`),
		))

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestGetProjectV2ID_BothLookupsFail(t *testing.T) {
	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		graphQLMatchFallthrough(`{"errors":[{"message":"nope"}]}`),
	))

	if _, err := getProjectV2ID("tok", "octo", 3); err == nil {
		t.Fatal("expected both-lookups-failed error")
	}
}

func TestGetProjectV2ID_NotFound(t *testing.T) {
	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		graphQLMatchFallthrough(`{"data":{"user":{"projectV2":{"id":""}}}}`),
	))

	if _, err := getProjectV2ID("tok", "octo", 3); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestGetIssueNodeID_GraphqlError(t *testing.T) {
	setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
		graphQLMatchFallthrough(`boom`),
	))
	// Override to return error status
	oldHTTP := eventsHTTPClient
	t.Cleanup(func() { eventsHTTPClient = oldHTTP })
	eventsHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`boom`)),
			Header:     make(http.Header),
		}, nil
	})}

	if _, err := getIssueNodeID("tok", "owner", "repo", 1); err == nil {
		t.Fatal("expected graphql error")
	}
}

func TestAddIssueToEngineProject_ErrorBranches(t *testing.T) {
	t.Run("project lookup error", func(t *testing.T) {
		setupProjectEnv(t, 3, "octo", "tok", "")
		setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
			graphQLMatchFallthrough(`{"errors":[{"message":"no project"}]}`),
		))

		if _, err := AddIssueToEngineProject("owner", "repo", 1); err == nil {
			t.Fatal("expected project lookup error")
		}
	})

	t.Run("issue lookup error", func(t *testing.T) {
		setupProjectEnv(t, 3, "octo", "tok", "")
		setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
			graphQLMatch("projectV2(number", `{"data":{"user":{"projectV2":{"id":"P1"}}}}`),
			graphQLMatch("issue(number", `{"errors":[{"message":"no issue"}]}`),
			graphQLMatchFallthrough(`{"data":{}}`),
		))

		if _, err := AddIssueToEngineProject("owner", "repo", 1); err == nil {
			t.Fatal("expected issue lookup error")
		}
	})

	t.Run("mutation error", func(t *testing.T) {
		setupProjectEnv(t, 3, "octo", "tok", "")
		setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
			graphQLMatch("projectV2(number", `{"data":{"user":{"projectV2":{"id":"P1"}}}}`),
			graphQLMatch("issue(number", `{"data":{"repository":{"issue":{"id":"I1"}}}}`),
			graphQLMatch("addProjectV2ItemById", `{"errors":[{"message":"mutation fail"}]}`),
			graphQLMatchFallthrough(`{"data":{}}`),
		))

		if _, err := AddIssueToEngineProject("owner", "repo", 1); err == nil {
			t.Fatal("expected mutation error")
		}
	})
}

func TestUpdateProjectItemStatus_EarlyAndErrorPaths(t *testing.T) {
	t.Run("missing token or owner is no-op", func(t *testing.T) {
		setupProjectEnv(t, 9, "", "", "")

		if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err != nil {
			t.Fatalf("expected nil no-op, got %v", err)
		}
	})

	t.Run("project lookup error", func(t *testing.T) {
		setupProjectEnv(t, 9, "octo", "tok", "")
		setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
			graphQLMatchFallthrough(`{"errors":[{"message":"no project"}]}`),
		))

		if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err == nil {
			t.Fatal("expected project lookup error")
		}
	})

	t.Run("field list error", func(t *testing.T) {
		setupProjectEnv(t, 9, "octo", "tok", "")
		setupGraphQLServer(t, newGraphQLHandlerWithQueries(t,
			graphQLMatch("projectV2(number", `{"data":{"user":{"projectV2":{"id":"P1"}}}}`),
			graphQLMatchFallthrough(`{"errors":[{"message":"field query failed"}]}`),
		))

		if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err == nil {
			t.Fatal("expected field list error")
		}
	})
}

func TestGraphqlDo_RequestAndReadErrors(t *testing.T) {
	t.Run("request construction error", func(t *testing.T) {
		t.Setenv("GITHUB_API_BASE", "://bad")
		oldHTTP := eventsHTTPClient
		eventsHTTPClient = &http.Client{}
		t.Cleanup(func() { eventsHTTPClient = oldHTTP })

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected request construction error")
		}
	})

	t.Run("response body read error", func(t *testing.T) {
		t.Setenv("GITHUB_API_BASE", "https://example.invalid")
		oldHTTP := eventsHTTPClient
		eventsHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}}, nil
		})}
		t.Cleanup(func() { eventsHTTPClient = oldHTTP })

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected body read error")
		}
	})
}

func TestAddIssueToEngineProject_MissingOwnerOrToken_NoOp(t *testing.T) {
	setupProjectEnv(t, 3, "", "", "")

	itemID, err := AddIssueToEngineProject("owner", "repo", 1)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if itemID != "" {
		t.Fatalf("expected empty item id, got %q", itemID)
	}
}
