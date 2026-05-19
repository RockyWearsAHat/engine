package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
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

func TestExtractGitHubURL_FallsBackToRepoNameOnGitError(t *testing.T) {
	orig := gitRemoteOriginFn
	defer func() { gitRemoteOriginFn = orig }()

	gitRemoteOriginFn = func(repoPath string) (string, error) {
		return "", fmt.Errorf("git failure")
	}

	if got := extractGitHubURL("/tmp/repo", "owner/repo"); got != "owner/repo" {
		t.Fatalf("extractGitHubURL fallback = %q", got)
	}
}

func TestDefaultIssueClient_ListIssues_ForbiddenReturnsNil(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
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
		t.Fatalf("expected nil issues for forbidden, got %+v", issues)
	}
}

func TestDefaultIssueClient_ListIssues_BadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{"))
	}))
	defer ts.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}
	defer func() { http.DefaultTransport = origTransport }()

	c := &defaultIssueClient{owner: "o", repo: "r", token: "tok"}
	_, err := c.listIssuesAssignedTo("login")
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestHandleIdentityCommand_DefaultIdentityMessage(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }

	oldLogin := githubEngineLoginFn
	oldToken := githubEngineTokenFn
	oldProject := githubProjectNumberFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubEngineTokenFn = oldToken
		githubProjectNumberFn = oldProject
	}()

	githubEngineLoginFn = func() string { return "" }
	githubEngineTokenFn = func() string { return "" }
	githubProjectNumberFn = func() int { return 0 }

	svc.handleIdentityCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}})
	if len(sent) == 0 {
		t.Fatal("expected identity response")
	}
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "not set") || !strings.Contains(joined, "not resolved") {
		t.Fatalf("unexpected identity output: %q", joined)
	}
}

func TestHandleIssuesCommand_NoProjects(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }

	oldLogin := githubEngineLoginFn
	defer func() { githubEngineLoginFn = oldLogin }()
	githubEngineLoginFn = func() string { return "engine-bot" }

	svc.handleIssuesCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}}, nil)
	if len(sent) == 0 || !strings.Contains(strings.Join(sent, "\n"), "No projects enrolled") {
		t.Fatalf("unexpected response: %+v", sent)
	}
}

func TestHandleIssuesCommand_SkipsInvalidRepoPathAndNoFindings(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }

	oldLogin := githubEngineLoginFn
	oldList := githubListAssignedFn
	oldRemote := gitRemoteOriginFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubListAssignedFn = oldList
		gitRemoteOriginFn = oldRemote
	}()

	githubEngineLoginFn = func() string { return "engine-bot" }
	githubListAssignedFn = func(owner, repo, login string) ([]githubIssue, error) {
		t.Fatalf("githubListAssignedFn should not be called when owner is empty")
		return nil, nil
	}
	gitRemoteOriginFn = func(repoPath string) (string, error) { return "", fmt.Errorf("no remote") }

	addProject(svc, "/tmp/proj", "proj-ch", "not-a-github-repo")
	svc.handleIssuesCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}}, nil)

	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "(none found)") {
		t.Fatalf("expected no findings footer, got %q", joined)
	}
}

func TestHandleIssuesCommand_ListsFetchErrorLine(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }

	oldLogin := githubEngineLoginFn
	oldList := githubListAssignedFn
	oldRemote := gitRemoteOriginFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubListAssignedFn = oldList
		gitRemoteOriginFn = oldRemote
	}()

	githubEngineLoginFn = func() string { return "engine-bot" }
	githubListAssignedFn = func(owner, repo, login string) ([]githubIssue, error) {
		return nil, fmt.Errorf("rate limited")
	}
	gitRemoteOriginFn = func(repoPath string) (string, error) { return "https://github.com/o/r.git", nil }

	addProject(svc, "/tmp/proj", "proj-ch", "r")
	svc.handleIssuesCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}}, nil)

	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "error fetching issues") {
		t.Fatalf("expected fetch error line, got %q", joined)
	}
}

func TestHandleIssuesCommand_ValidRepoNoIssues(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	var sent []string
	svc.testSendHook = func(_ string, msg string) { sent = append(sent, msg) }

	oldLogin := githubEngineLoginFn
	oldList := githubListAssignedFn
	oldRemote := gitRemoteOriginFn
	defer func() {
		githubEngineLoginFn = oldLogin
		githubListAssignedFn = oldList
		gitRemoteOriginFn = oldRemote
	}()

	githubEngineLoginFn = func() string { return "engine-bot" }
	githubListAssignedFn = func(owner, repo, login string) ([]githubIssue, error) { return []githubIssue{}, nil }
	gitRemoteOriginFn = func(repoPath string) (string, error) { return "https://github.com/o/r.git", nil }

	addProject(svc, "/tmp/proj", "proj-ch", "r")
	svc.handleIssuesCommand(&discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "ch"}}, nil)

	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "(none found)") {
		t.Fatalf("expected none-found footer for zero issues, got %q", joined)
	}
}

func TestGitHubBridgeDefaultWrappers(t *testing.T) {
	oldEngineLogin := engineLoginFn
	oldEngineToken := engineTokenFn
	oldProjectNum := projectNumberFn
	defer func() {
		engineLoginFn = oldEngineLogin
		engineTokenFn = oldEngineToken
		projectNumberFn = oldProjectNum
	}()

	engineLoginFn = func() string { return "bridge-login" }
	engineTokenFn = func() string { return "bridge-token" }
	projectNumberFn = func() int { return 77 }

	if got := githubEngineLoginFn(); got != "bridge-login" {
		t.Fatalf("githubEngineLoginFn = %q", got)
	}
	if got := githubEngineTokenFn(); got != "bridge-token" {
		t.Fatalf("githubEngineTokenFn = %q", got)
	}
	if got := githubProjectNumberFn(); got != 77 {
		t.Fatalf("githubProjectNumberFn = %d", got)
	}
}

func TestGitRemoteOrigin_DefaultClosureError(t *testing.T) {
	defaultFn := gitRemoteOriginFn
	out, err := defaultFn(t.TempDir())
	if err == nil {
		t.Fatalf("expected git remote error, got output %q", out)
	}
}

func TestDefaultIssueClient_ListIssues_RequestAndTransportErrors(t *testing.T) {
	c := &defaultIssueClient{owner: "bad\nowner", repo: "r", token: "tok"}
	if _, err := c.listIssuesAssignedTo("login"); err == nil {
		t.Fatal("expected request build error for invalid owner")
	}

	origTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("transport down")
	})
	defer func() { http.DefaultTransport = origTransport }()

	c2 := &defaultIssueClient{owner: "o", repo: "r", token: "tok"}
	if _, err := c2.listIssuesAssignedTo("login"); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestLoadState_ParseError(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.cfg.StoragePath = t.TempDir()
	if err := os.MkdirAll(svc.cfg.StoragePath, 0o755); err != nil {
		t.Fatalf("mkdir storage: %v", err)
	}
	statePath := filepath.Join(svc.cfg.StoragePath, defaultStateFileName)
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write bad state: %v", err)
	}

	err := svc.loadState()
	if err == nil || !strings.Contains(err.Error(), "parse discord state") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestLoadState_EmptyStoragePathNoop(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.cfg.StoragePath = ""
	if err := svc.loadState(); err != nil {
		t.Fatalf("expected nil error for empty storage path, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
