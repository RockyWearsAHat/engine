package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

// mockIssueClient implements issueListClient for testing.
type mockIssueClient struct {
	issues []githubIssue
	err    error
}

func (m *mockIssueClient) listIssuesAssignedTo(_ string) ([]githubIssue, error) {
	return m.issues, m.err
}

// TestListAssignedIssues_MockClient verifies injectable function is used.
func TestListAssignedIssues_MockClient(t *testing.T) {
	orig := newGitHubClientFn
	defer func() { newGitHubClientFn = orig }()

	want := []githubIssue{{Number: 42, Title: "Bug in auth"}}
	newGitHubClientFn = func(owner, repo string) (issueListClient, error) {
		return &mockIssueClient{issues: want}, nil
	}

	got, err := listAssignedIssues("o", "r", "bot-user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Number != 42 {
		t.Errorf("unexpected issues: %+v", got)
	}
}

// TestListAssignedIssues_ClientError verifies client-creation error propagates.
func TestListAssignedIssues_ClientError(t *testing.T) {
	orig := newGitHubClientFn
	defer func() { newGitHubClientFn = orig }()

	newGitHubClientFn = func(_, _ string) (issueListClient, error) {
		return nil, fmt.Errorf("no token")
	}

	_, err := listAssignedIssues("o", "r", "login")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestDefaultGitHubClientFn_NoToken verifies error when no token env vars set.
func TestDefaultGitHubClientFn_NoToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	_, err := defaultGitHubClientFn("o", "r")
	if err == nil {
		t.Error("expected error when no token configured")
	}
}

// TestDefaultGitHubClientFn_WithToken verifies client is returned when token is set.
func TestDefaultGitHubClientFn_WithToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "test-token-abc")
	c, err := defaultGitHubClientFn("myowner", "myrepo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dc, ok := c.(*defaultIssueClient)
	if !ok {
		t.Fatalf("expected *defaultIssueClient, got %T", c)
	}
	if dc.owner != "myowner" || dc.repo != "myrepo" || dc.token != "test-token-abc" {
		t.Errorf("unexpected client fields: %+v", dc)
	}
}

// TestDefaultGitHubClientFn_FallbackToken verifies GITHUB_TOKEN fallback.
func TestDefaultGitHubClientFn_FallbackToken(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_BOT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "fallback-tok")
	c, err := defaultGitHubClientFn("o", "r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dc := c.(*defaultIssueClient)
	if dc.token != "fallback-tok" {
		t.Errorf("expected fallback token, got %q", dc.token)
	}
}

// redirectTransport rewrites requests to point at a test server URL.
type redirectTransport struct {
	base    http.RoundTripper
	testURL string
}

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(r.testURL)
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = u.Scheme
	cloned.URL.Host = u.Host
	cloned.Host = u.Host
	return r.base.RoundTrip(cloned)
}

// TestDefaultIssueClient_ListIssues_Success verifies JSON decoding and PR filtering.
func TestDefaultIssueClient_ListIssues_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		issues := []map[string]any{
			{
				"number":   1,
				"title":    "Real issue",
				"html_url": "https://github.com/o/r/issues/1",
				"labels":   []map[string]string{{"name": "bug"}},
			},
			{
				"number":       2,
				"title":        "A pull request",
				"html_url":     "https://github.com/o/r/pull/2",
				"pull_request": map[string]any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(issues)
	}))
	defer ts.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}
	defer func() { http.DefaultTransport = origTransport }()

	c := &defaultIssueClient{owner: "o", repo: "r", token: "tok"}
	issues, err := c.listIssuesAssignedTo("login")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// PR should be filtered out
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (PR filtered), got %d", len(issues))
	}
	if issues[0].Number != 1 || issues[0].Title != "Real issue" {
		t.Errorf("unexpected issue: %+v", issues[0])
	}
}

// TestDefaultIssueClient_ListIssues_404 verifies 404 returns nil.
func TestDefaultIssueClient_ListIssues_404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}
	defer func() { http.DefaultTransport = origTransport }()

	c := &defaultIssueClient{owner: "o", repo: "r", token: "tok"}
	issues, err := c.listIssuesAssignedTo("login")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issues != nil {
		t.Errorf("expected nil issues for 404, got %+v", issues)
	}
}

// TestDefaultIssueClient_ListIssues_500 verifies non-2xx returns error.
func TestDefaultIssueClient_ListIssues_500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}
	defer func() { http.DefaultTransport = origTransport }()

	c := &defaultIssueClient{owner: "o", repo: "r", token: "tok"}
	_, err := c.listIssuesAssignedTo("login")
	if err == nil {
		t.Error("expected error for 500")
	}
}

// Ensure os is used (via t.Setenv, os.Getenv is tested elsewhere).
var _ = os.Getenv
