package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type errReadCloser struct{}

func (errReadCloser) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReadCloser) Close() error               { return nil }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
	defer func() { eventsHTTPClient = oldHTTP }()

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode graphql request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "user(login") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"user not found"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"organization":{"projectV2":{"id":"P_ORG"}}}}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"repository":{"issue":{"id":""}}}}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

	_, err := getIssueNodeID("tok", "owner", "repo", 99)
	if err == nil {
		t.Fatal("expected not found error")
	}
}

// TestAddIssueToEngineProject_BestEffortWhenNotConfigured verifies function gracefully handles unconfigured project.
func TestAddIssueToEngineProject_BestEffortWhenNotConfigured(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "")

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
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "3")
	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode add-project request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(req.Query, "projectV2(number"):
			_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":"P1"}}}}`))
		case strings.Contains(req.Query, "issue(number"):
			_, _ = w.Write([]byte(`{"data":{"repository":{"issue":{"id":"I1"}}}}`))
		case strings.Contains(req.Query, "addProjectV2ItemById"):
			_, _ = w.Write([]byte(`{"data":{"addProjectV2ItemById":{"item":{"id":"ITEM1"}}}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected query"}]}`))
		}
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

	itemID, err := AddIssueToEngineProject("owner", "repo", 7)
	if err != nil {
		t.Fatalf("AddIssueToEngineProject: %v", err)
	}
	if itemID != "ITEM1" {
		t.Fatalf("itemID = %q, want ITEM1", itemID)
	}
}

func TestUpdateProjectItemStatus_SuccessAndSkip(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "9")
	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode update-status request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(req.Query, "projectV2(number"):
			_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":"P1"}}}}`))
		case strings.Contains(req.Query, "fields(first"):
			_, _ = w.Write([]byte(`{"data":{"node":{"fields":{"nodes":[{"id":"F1","name":"Status","options":[{"id":"OPT1","name":"Done"},{"id":"OPT2","name":"In Progress"}]}]}}}}`))
		case strings.Contains(req.Query, "updateProjectV2ItemFieldValue"):
			_, _ = w.Write([]byte(`{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"ITEM1"}}}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected query"}]}`))
		}
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

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
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "9")
	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode field/option request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "projectV2(number") {
			_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":"P1"}}}}`))
			return
		}
		if strings.Contains(req.Query, "fields(first") {
			_, _ = w.Write([]byte(`{"data":{"node":{"fields":{"nodes":[{"id":"X","name":"Priority","options":[{"id":"P1","name":"High"}]}]}}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

	if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err != nil {
		t.Fatalf("expected nil when status option missing, got %v", err)
	}
}

func TestGraphqlDo_StatusAndParseErrors(t *testing.T) {
	t.Run("non-2xx status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
		}))
		defer srv.Close()

		t.Setenv("GITHUB_API_BASE", srv.URL)
		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected status error")
		}
	})

	t.Run("invalid json response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer srv.Close()

		t.Setenv("GITHUB_API_BASE", srv.URL)
		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestGetProjectV2ID_BothLookupsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"nope"}]}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

	if _, err := getProjectV2ID("tok", "octo", 3); err == nil {
		t.Fatal("expected both-lookups-failed error")
	}
}

func TestGetProjectV2ID_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":""}}}}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

	if _, err := getProjectV2ID("tok", "octo", 3); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestGetIssueNodeID_GraphqlError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_BASE", srv.URL)
	oldHTTP := eventsHTTPClient
	eventsHTTPClient = srv.Client()
	defer func() { eventsHTTPClient = oldHTTP }()

	if _, err := getIssueNodeID("tok", "owner", "repo", 1); err == nil {
		t.Fatal("expected graphql error")
	}
}

func TestAddIssueToEngineProject_ErrorBranches(t *testing.T) {
	t.Run("project lookup error", func(t *testing.T) {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "3")
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":[{"message":"no project"}]}`))
		}))
		defer srv.Close()
		t.Setenv("GITHUB_API_BASE", srv.URL)

		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

		if _, err := AddIssueToEngineProject("owner", "repo", 1); err == nil {
			t.Fatal("expected project lookup error")
		}
	})

	t.Run("issue lookup error", func(t *testing.T) {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "3")
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode issue lookup request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(req.Query, "projectV2(number"):
				_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":"P1"}}}}`))
			case strings.Contains(req.Query, "issue(number"):
				_, _ = w.Write([]byte(`{"errors":[{"message":"no issue"}]}`))
			default:
				_, _ = w.Write([]byte(`{"data":{}}`))
			}
		}))
		defer srv.Close()
		t.Setenv("GITHUB_API_BASE", srv.URL)

		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

		if _, err := AddIssueToEngineProject("owner", "repo", 1); err == nil {
			t.Fatal("expected issue lookup error")
		}
	})

	t.Run("mutation error", func(t *testing.T) {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "3")
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode mutation request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(req.Query, "projectV2(number"):
				_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":"P1"}}}}`))
			case strings.Contains(req.Query, "issue(number"):
				_, _ = w.Write([]byte(`{"data":{"repository":{"issue":{"id":"I1"}}}}`))
			case strings.Contains(req.Query, "addProjectV2ItemById"):
				_, _ = w.Write([]byte(`{"errors":[{"message":"mutation fail"}]}`))
			default:
				_, _ = w.Write([]byte(`{"data":{}}`))
			}
		}))
		defer srv.Close()
		t.Setenv("GITHUB_API_BASE", srv.URL)

		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

		if _, err := AddIssueToEngineProject("owner", "repo", 1); err == nil {
			t.Fatal("expected mutation error")
		}
	})
}

func TestUpdateProjectItemStatus_EarlyAndErrorPaths(t *testing.T) {
	t.Run("missing token or owner is no-op", func(t *testing.T) {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "9")
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "")
		t.Setenv("ENGINE_GITHUB_LOGIN", "")
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")

		if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err != nil {
			t.Fatalf("expected nil no-op, got %v", err)
		}
	})

	t.Run("project lookup error", func(t *testing.T) {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "9")
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":[{"message":"no project"}]}`))
		}))
		defer srv.Close()
		t.Setenv("GITHUB_API_BASE", srv.URL)

		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

		if err := UpdateProjectItemStatus("owner", "ITEM1", "Done"); err == nil {
			t.Fatal("expected project lookup error")
		}
	})

	t.Run("field list error", func(t *testing.T) {
		t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "9")
		t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "octo")
		t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "tok")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode field list error request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(req.Query, "projectV2(number") {
				_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":{"id":"P1"}}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"errors":[{"message":"field query failed"}]}`))
		}))
		defer srv.Close()
		t.Setenv("GITHUB_API_BASE", srv.URL)

		oldHTTP := eventsHTTPClient
		eventsHTTPClient = srv.Client()
		defer func() { eventsHTTPClient = oldHTTP }()

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
		defer func() { eventsHTTPClient = oldHTTP }()

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
		defer func() { eventsHTTPClient = oldHTTP }()

		if err := graphqlDo("tok", "query { ping }", nil, nil); err == nil {
			t.Fatal("expected body read error")
		}
	})
}

func TestAddIssueToEngineProject_MissingOwnerOrToken_NoOp(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_PROJECT_NUMBER", "3")
	t.Setenv("ENGINE_GITHUB_PROJECT_OWNER", "")
	t.Setenv("ENGINE_GITHUB_LOGIN", "")
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	itemID, err := AddIssueToEngineProject("owner", "repo", 1)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if itemID != "" {
		t.Fatalf("expected empty item id, got %q", itemID)
	}
}
