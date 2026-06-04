package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// makeWatcher creates a test EventsWatcher with injectable functions for testing.
func makeWatcher(monitor *RepoMonitor) *EventsWatcher {
	return &EventsWatcher{
		token:                  "test",
		monitor:                monitor,
		startedAt:              time.Now().UTC(),
		seen:                   map[string]bool{},
		processed:              map[string]bool{},
		processedIssueComments: map[int64]bool{},
		tickFn:                 func(_ time.Duration) <-chan time.Time { ch := make(chan time.Time, 1); ch <- time.Now(); return ch },
		loginFn:                func(_ string) (string, error) { return "testuser", nil },
		listReposFn: func(_ string, _ int) ([]UserRepo, error) {
			return nil, nil // empty initial scan by default
		},
		fetchEventsFn: func(_, _, _ string) ([]eventEntry, string, int, bool, error) {
			return nil, "etag1", 60, false, nil
		},
		fetchReadmeFn: func(_, _, _ string) ([]byte, error) {
			return []byte("no tag"), nil
		},
	}
}

// ── NewEventsWatcherFromEnv ───────────────────────────────────────────────────

// TestNewEventsWatcherFromEnv_NoToken_ReturnsNil verifies watcher is not created without token source.
func TestNewEventsWatcherFromEnv_NoToken_ReturnsNil(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	orig := ghCLITokenFn
	ghCLITokenFn = func() string { return "" }
	defer func() { ghCLITokenFn = orig }()
	if w := NewEventsWatcherFromEnv(NewRepoMonitor()); w != nil {
		t.Error("expected nil when GITHUB_TOKEN is absent and gh CLI returns empty")
	}
}

// TestNewEventsWatcherFromEnv_WithToken_ReturnsWatcher verifies watcher creation with GITHUB_TOKEN set.
func TestNewEventsWatcherFromEnv_WithToken_ReturnsWatcher(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	w := NewEventsWatcherFromEnv(NewRepoMonitor())
	if w == nil {
		t.Fatal("expected non-nil EventsWatcher")
	}
}

// TestNewEventsWatcherFromEnv_CLIFallback_ReturnsWatcher verifies watcher creation when gh CLI supplies token.
func TestNewEventsWatcherFromEnv_CLIFallback_ReturnsWatcher(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	orig := ghCLITokenFn
	ghCLITokenFn = func() string { return "gho_cli_token" }
	defer func() { ghCLITokenFn = orig }()
	w := NewEventsWatcherFromEnv(NewRepoMonitor())
	if w == nil {
		t.Fatal("expected non-nil EventsWatcher when gh CLI supplies token")
	}
}

// TestNewEventsWatcherFromEnv_EnvTakesPrecedenceOverCLI verifies GITHUB_TOKEN overrides gh CLI token.
func TestNewEventsWatcherFromEnv_EnvTakesPrecedenceOverCLI(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_env_token")
	orig := ghCLITokenFn
	ghCLITokenFn = func() string { return "gho_cli_should_not_be_used" }
	defer func() { ghCLITokenFn = orig }()
	w := NewEventsWatcherFromEnv(NewRepoMonitor())
	if w == nil {
		t.Fatal("expected non-nil EventsWatcher")
	}
	if w.token != "ghp_env_token" {
		t.Errorf("expected env token to take precedence, got %q", w.token)
	}
}

// ── initial scan ─────────────────────────────────────────────────────────────

// TestEventsWatcher_InitialScan_FiresOnEngineTag verifies README @engine tag triggers callback.
func TestEventsWatcher_InitialScan_FiresOnEngineTag(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired []json.RawMessage
	monitor.OnReadmeChange = func(p json.RawMessage) { fired = append(fired, p) }

	w := makeWatcher(monitor)
	w.listReposFn = func(_ string, _ int) ([]UserRepo, error) {
		return []UserRepo{{FullName: "alice/proj", DefaultBranch: "main"}}, nil
	}
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("# Proj\n@engine please build"), nil
	}

	w.initialScan()

	if len(fired) != 1 {
		t.Fatalf("expected 1 OnReadmeChange, got %d", len(fired))
	}
}

// TestEventsWatcher_InitialScan_NoFireWithoutTag verifies callback is not fired without @engine tag.
func TestEventsWatcher_InitialScan_NoFireWithoutTag(t *testing.T) {
	monitor := NewRepoMonitor()
	var count int
	monitor.OnReadmeChange = func(_ json.RawMessage) { count++ }

	w := makeWatcher(monitor)
	w.listReposFn = func(_ string, _ int) ([]UserRepo, error) {
		return []UserRepo{{FullName: "alice/quiet", DefaultBranch: "main"}}, nil
	}

	w.initialScan()

	if count != 0 {
		t.Fatalf("expected no OnReadmeChange, got %d", count)
	}
}

// TestEventsWatcher_InitialScan_ListError_NoFire verifies callback is not fired on list error.
func TestEventsWatcher_InitialScan_ListError_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var count int
	monitor.OnReadmeChange = func(_ json.RawMessage) { count++ }

	w := makeWatcher(monitor)
	w.listReposFn = func(_ string, _ int) ([]UserRepo, error) {
		return nil, errors.New("rate limited")
	}

	w.initialScan()

	if count != 0 {
		t.Fatalf("expected no OnReadmeChange, got %d", count)
	}
}

// ── processEvents ─────────────────────────────────────────────────────────────

// TestEventsWatcher_PushEvent_TouchesReadme_Fires verifies push event with README change triggers callback.
// Tests behavioral side effects (monitoring repository push events).
func TestEventsWatcher_PushEvent_TouchesReadme_Fires(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	payload, _ := json.Marshal(map[string]any{
		"ref": "refs/heads/main",
		"commits": []map[string]any{
			{"added": []string{}, "modified": []string{"README.md"}},
		},
	})
	w.processEvents([]eventEntry{
		{Type: "PushEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/myrepo"}, Payload: payload},
	})

	if fired != 1 {
		t.Fatalf("expected 1 fire, got %d", fired)
	}
}

func TestEventsWatcher_PushEvent_NoReadmeTouched_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	payload, _ := json.Marshal(map[string]any{
		"ref": "refs/heads/main",
		"commits": []map[string]any{
			{"added": []string{}, "modified": []string{"src/main.go"}},
		},
	})
	w.processEvents([]eventEntry{
		{Type: "PushEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/go"}, Payload: payload},
	})

	if fired != 0 {
		t.Fatalf("expected no fire, got %d", fired)
	}
}

func TestEventsWatcher_CreateEvent_Repository_Fires(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("# New @engine repo"), nil
	}

	payload, _ := json.Marshal(map[string]string{"ref_type": "repository"})
	w.processEvents([]eventEntry{
		{Type: "CreateEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/newrepo"}, Payload: payload},
	})

	if fired != 1 {
		t.Fatalf("expected 1 fire, got %d", fired)
	}
}

func TestEventsWatcher_CreateEvent_Branch_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)

	payload, _ := json.Marshal(map[string]string{"ref_type": "branch"})
	w.processEvents([]eventEntry{
		{Type: "CreateEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/existing"}, Payload: payload},
	})

	if fired != 0 {
		t.Fatalf("expected no fire, got %d", fired)
	}
}

// ── deduplication & edge-triggering ──────────────────────────────────────────

func TestEventsWatcher_DeduplicatesWithinBatch(t *testing.T) {
	monitor := NewRepoMonitor()
	var count int
	monitor.OnReadmeChange = func(_ json.RawMessage) { count++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	pushPayload, _ := json.Marshal(map[string]any{
		"ref":     "refs/heads/main",
		"commits": []map[string]any{{"added": []string{"README.md"}, "modified": []string{}}},
	})
	// Two push events for the same repo in one batch.
	w.processEvents([]eventEntry{
		{Type: "PushEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/dup"}, Payload: pushPayload},
		{Type: "PushEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/dup"}, Payload: pushPayload},
	})

	if count != 1 {
		t.Fatalf("expected 1 OnReadmeChange (deduped), got %d", count)
	}
}

func TestEventsWatcher_DoesNotRefireIfTagAlreadySeen(t *testing.T) {
	monitor := NewRepoMonitor()
	var count int
	monitor.OnReadmeChange = func(_ json.RawMessage) { count++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	pushPayload, _ := json.Marshal(map[string]any{
		"ref":     "refs/heads/main",
		"commits": []map[string]any{{"added": []string{"README.md"}, "modified": []string{}}},
	})
	ev := []eventEntry{
		{Type: "PushEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/stable"}, Payload: pushPayload},
	}

	w.processEvents(ev) // fires
	w.processEvents(ev) // already seen — no second fire

	if count != 1 {
		t.Fatalf("expected 1 fire, got %d", count)
	}
}

func TestEventsWatcher_RefiresWhenTagReappears(t *testing.T) {
	monitor := NewRepoMonitor()
	var count int
	monitor.OnReadmeChange = func(_ json.RawMessage) { count++ }

	w := makeWatcher(monitor)
	hasTag := true
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		if hasTag {
			return []byte("@engine"), nil
		}
		return []byte("no tag"), nil
	}

	payload, _ := json.Marshal(map[string]any{
		"ref":     "refs/heads/main",
		"commits": []map[string]any{{"added": []string{"README.md"}, "modified": []string{}}},
	})
	ev := []eventEntry{{Type: "PushEvent", Repo: struct {
		Name string `json:"name"`
	}{Name: "alice/cycling"}, Payload: payload}}

	w.processEvents(ev) // fires — tag present
	hasTag = false
	w.processEvents(ev) // tag removed — no fire but clears seen
	hasTag = true
	w.processEvents(ev) // tag back — fires again

	if count != 2 {
		t.Fatalf("expected 2 fires, got %d", count)
	}
}

// ── checkRepo: nil handler safe ───────────────────────────────────────────────

func TestEventsWatcher_NilOnReadmeChange_NoPanic(t *testing.T) {
	monitor := NewRepoMonitor() // OnReadmeChange is nil

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	// Must not panic.
	w.checkRepo("alice/safe", "main")
}

// ── ETag / poll-interval ──────────────────────────────────────────────────────

func TestEventsWatcher_ETag304_NoProcessing(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	callCount := 0
	w.fetchEventsFn = func(_, _, _ string) ([]eventEntry, string, int, bool, error) {
		callCount++
		return nil, "etag1", 30, true, nil // 304
	}
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	loginCalled := make(chan struct{}, 1)
	w.loginFn = func(_ string) (string, error) {
		close(loginCalled)
		return "u", nil
	}

	w.Start(ctx)
	<-loginCalled
	time.Sleep(50 * time.Millisecond)
	cancel()

	if fired != 0 {
		t.Fatalf("304 should not fire OnReadmeChange, got %d", fired)
	}
}

func TestEventsWatcher_LoginError_Exits(t *testing.T) {
	monitor := NewRepoMonitor()

	w := makeWatcher(monitor)
	w.loginFn = func(_ string) (string, error) { return "", errors.New("bad creds") }

	fetchCalled := false
	w.fetchEventsFn = func(_, _, _ string) ([]eventEntry, string, int, bool, error) {
		fetchCalled = true
		return nil, "", 60, false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	w.Start(ctx)
	<-ctx.Done()

	if fetchCalled {
		t.Error("fetchEvents must not be called when login fails")
	}
}

// ── eventPushTouchesReadme ────────────────────────────────────────────────────

func TestEventPushTouchesReadme_Modified(t *testing.T) {
	commits := []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
	}{{Modified: []string{"README.md"}}}
	if !eventPushTouchesReadme(commits) {
		t.Error("expected true for modified README.md")
	}
}

func TestEventPushTouchesReadme_Added(t *testing.T) {
	commits := []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
	}{{Added: []string{"readme"}}}
	if !eventPushTouchesReadme(commits) {
		t.Error("expected true for added readme")
	}
}

func TestEventPushTouchesReadme_NoReadme(t *testing.T) {
	commits := []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
	}{{Modified: []string{"src/main.go"}}}
	if eventPushTouchesReadme(commits) {
		t.Error("expected false for non-README file")
	}
}

// ── run() loop paths ──────────────────────────────────────────────────────────

func TestEventsWatcher_Run_FetchError_Continues(t *testing.T) {
	monitor := NewRepoMonitor()
	w := makeWatcher(monitor)

	callCount := 0
	reached2 := make(chan struct{}, 1)
	var once sync.Once
	w.fetchEventsFn = func(_, _, _ string) ([]eventEntry, string, int, bool, error) {
		callCount++
		if callCount == 1 {
			return nil, "", 0, false, errors.New("network error")
		}
		once.Do(func() { close(reached2) })
		return nil, "etag2", 1, false, nil
	}
	w.tickFn = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	select {
	case <-reached2:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second fetchEvents call")
	}
	cancel()
}

func TestEventsWatcher_Run_EtagUpdated(t *testing.T) {
	monitor := NewRepoMonitor()
	w := makeWatcher(monitor)

	callCount := 0
	reached2 := make(chan struct{}, 1)
	var once sync.Once
	w.fetchEventsFn = func(_, _, etag string) ([]eventEntry, string, int, bool, error) {
		callCount++
		if callCount == 1 {
			return nil, "new-etag", 1, false, nil
		}
		if etag != "new-etag" {
			t.Errorf("expected etag 'new-etag' on second call, got %q", etag)
		}
		once.Do(func() { close(reached2) })
		return nil, "new-etag", 1, true, nil // 304
	}
	w.tickFn = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	select {
	case <-reached2:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for etag update verification")
	}
	cancel()
}

func TestEventsWatcher_CheckRepo_NoSlashInName(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	// fullName without a slash — covers the len(parts) != 2 branch
	w.checkRepo("noslash", "HEAD")
	if fired != 1 {
		t.Fatalf("expected 1 fire, got %d", fired)
	}
}

func TestEventsWatcher_CheckRepo_HeadBranch_DefaultsToMain(t *testing.T) {
	monitor := NewRepoMonitor()
	var gotPayload json.RawMessage
	monitor.OnReadmeChange = func(p json.RawMessage) { gotPayload = p }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return []byte("@engine"), nil
	}

	w.checkRepo("owner/repo", "HEAD")

	var data map[string]any
	if err := json.Unmarshal(gotPayload, &data); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	repo, _ := data["repository"].(map[string]any)
	if repo["default_branch"] != "main" {
		t.Errorf("expected default_branch 'main' when branch is HEAD, got %v", repo["default_branch"])
	}
}

// ── default* functions ────────────────────────────────────────────────────────

func TestDefaultEventsReadmeFn_EmptyBranch(t *testing.T) {
	old := profileHTTPGet
	defer func() { profileHTTPGet = old }()
	var gotURL string
	profileHTTPGet = func(url, _ string) ([]byte, error) {
		gotURL = url
		return []byte("ok"), nil
	}
	_, _ = defaultEventsReadmeFn("owner/repo", "", "tok")
	if !strings.Contains(gotURL, "/HEAD/") {
		t.Errorf("expected HEAD in URL when branch empty, got %q", gotURL)
	}
}

func TestDefaultEventsReadmeFn_WithBranch(t *testing.T) {
	old := profileHTTPGet
	defer func() { profileHTTPGet = old }()
	var gotURL string
	profileHTTPGet = func(url, _ string) ([]byte, error) {
		gotURL = url
		return []byte("ok"), nil
	}
	_, _ = defaultEventsReadmeFn("owner/repo", "develop", "tok")
	if !strings.Contains(gotURL, "/develop/") {
		t.Errorf("expected 'develop' in URL, got %q", gotURL)
	}
}

func TestDefaultFetchEventsFn_NotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	_, _, _, unchanged, err := defaultFetchEventsFn("tok", "login", "etag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !unchanged {
		t.Error("expected unchanged=true for 304")
	}
}

func TestDefaultFetchEventsFn_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "forbidden")
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	_, _, _, _, err := defaultFetchEventsFn("tok", "login", "")
	if err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestDefaultFetchEventsFn_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	_, _, _, _, err := defaultFetchEventsFn("tok", "login", "")
	if err == nil {
		t.Error("expected parse error for invalid JSON")
	}
}

func TestDefaultFetchEventsFn_PollIntervalHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Poll-Interval", "30")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	_, _, pollSecs, _, err := defaultFetchEventsFn("tok", "login", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pollSecs != 30 {
		t.Errorf("expected pollSecs=30, got %d", pollSecs)
	}
}

func TestDefaultFetchEventsFn_WithEtag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "" {
			t.Error("expected If-None-Match header")
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	_, _, _, unchanged, err := defaultFetchEventsFn("tok", "login", "my-etag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !unchanged {
		t.Error("expected unchanged=true for 304")
	}
}

func TestDefaultEventsLoginFn_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"login":"testuser"}`)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	login, err := defaultEventsLoginFn("tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "testuser" {
		t.Errorf("expected login 'testuser', got %q", login)
	}
}

func TestDefaultEventsListReposFn_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"full_name":"owner/repo","default_branch":"main"}]`)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_BASE", srv.URL)

	repos, err := defaultEventsListReposFn("tok", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "owner/repo" {
		t.Errorf("unexpected repos: %v", repos)
	}
}

// ── processEvents bad JSON ────────────────────────────────────────────────────

func TestProcessEvents_PushEvent_BadJSON(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.processEvents([]eventEntry{
		{Type: "PushEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/repo"}, Payload: json.RawMessage(`not json`)},
	})
	if fired != 0 {
		t.Fatalf("bad JSON PushEvent should not fire OnReadmeChange, got %d", fired)
	}
}

func TestProcessEvents_CreateEvent_BadJSON(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.processEvents([]eventEntry{
		{Type: "CreateEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/repo"}, Payload: json.RawMessage(`not json`)},
	})
	if fired != 0 {
		t.Fatalf("bad JSON CreateEvent should not fire OnReadmeChange, got %d", fired)
	}
}

// ── checkRepo error path ──────────────────────────────────────────────────────

func TestCheckRepo_FetchError(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnReadmeChange = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, _, _ string) ([]byte, error) {
		return nil, fmt.Errorf("connection refused")
	}
	w.checkRepo("owner/repo", "main")
	if fired != 0 {
		t.Fatalf("fetch error should not fire OnReadmeChange, got %d", fired)
	}
}

func TestCheckRepo_EmptyBranch_SetsHEAD(t *testing.T) {
	monitor := NewRepoMonitor()
	var gotBranch string
	monitor.OnReadmeChange = func(_ json.RawMessage) {}

	w := makeWatcher(monitor)
	w.fetchReadmeFn = func(_, branch, _ string) ([]byte, error) {
		gotBranch = branch
		return []byte("@engine"), nil
	}
	w.checkRepo("owner/repo", "")
	if gotBranch != "HEAD" {
		t.Errorf("expected branch 'HEAD' when empty, got %q", gotBranch)
	}
}

func TestDefaultFetchEventsFn_RequestError(t *testing.T) {
	t.Setenv("GITHUB_API_BASE", "://invalid-url")
	_, _, _, _, err := defaultFetchEventsFn("tok", "login", "")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestDefaultFetchEventsFn_HTTPDoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close immediately so Do() fails with connection refused
	t.Setenv("GITHUB_API_BASE", srv.URL)

	_, _, _, _, err := defaultFetchEventsFn("tok", "login", "")
	if err == nil {
		t.Error("expected error when server is closed")
	}
}

func TestGhTokenFromCLI_NoBinary_ReturnsEmpty(t *testing.T) {
	orig := ghCandidatePaths
	t.Cleanup(func() { ghCandidatePaths = orig })
	ghCandidatePaths = []string{"/no-such-binary-xyz"}
	tok := ghTokenFromCLI()
	if tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}
}

func TestGhTokenFromCLI_EchoPath_ReturnsToken(t *testing.T) {
	orig := ghCandidatePaths
	t.Cleanup(func() { ghCandidatePaths = orig })
	// Use echo as a stand-in: "echo auth token" outputs "auth token"
	ghCandidatePaths = []string{"echo"}
	tok := ghTokenFromCLI()
	if tok == "" {
		t.Error("expected non-empty token from echo stand-in")
	}
}

// ── IssuesEvent / IssueCommentEvent dispatch ─────────────────────────────────

func TestEventsWatcher_IssuesEvent_OpenedOnTaggedRepo_FiresOpenedHandler(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	var got json.RawMessage
	monitor.OnIssueOpened = func(p json.RawMessage) { fired++; got = p }

	w := makeWatcher(monitor)
	// Mark the repo as tagged so issues for it are eligible to dispatch.
	w.mu.Lock()
	w.seen["alice/myrepo"] = true
	w.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{
		"action": "opened",
		"issue":  map[string]any{"number": 42, "title": "broken"},
	})
	w.processEvents([]eventEntry{
		{Type: "IssuesEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/myrepo"}, Payload: payload},
	})

	if fired != 1 {
		t.Fatalf("expected OnIssueOpened to fire once, got %d", fired)
	}
	parsed, err := ParseIssue(&WebhookEvent{Type: "issues", Payload: got})
	if err != nil {
		t.Fatalf("ParseIssue on dispatched payload: %v", err)
	}
	if parsed.Issue.Number != 42 {
		t.Errorf("expected issue #42, got #%d", parsed.Issue.Number)
	}
	if parsed.Repository.FullName != "alice/myrepo" {
		t.Errorf("expected repository.full_name alice/myrepo, got %q", parsed.Repository.FullName)
	}
}

func TestEventsWatcher_IssuesEvent_UntaggedRepo_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnIssueOpened = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	// Do NOT mark the repo as tagged.

	payload, _ := json.Marshal(map[string]any{
		"action": "opened",
		"issue":  map[string]any{"number": 1},
	})
	w.processEvents([]eventEntry{
		{Type: "IssuesEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/other"}, Payload: payload},
	})

	if fired != 0 {
		t.Fatalf("expected no dispatch for untagged repo, got %d fires", fired)
	}
}

func TestEventsWatcher_IssuesEvent_NonOpenedAction_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnIssueOpened = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.mu.Lock()
	w.seen["alice/myrepo"] = true
	w.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{
		"action": "edited",
		"issue":  map[string]any{"number": 7},
	})
	w.processEvents([]eventEntry{
		{Type: "IssuesEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/myrepo"}, Payload: payload},
	})

	if fired != 0 {
		t.Fatalf("expected no dispatch for non-opened action, got %d fires", fired)
	}
}

func TestEventsWatcher_DispatchIssueEvent_Guards(t *testing.T) {
	makeWatcher(nil).dispatchIssueEvent("alice/myrepo", json.RawMessage(`{"action":"opened"}`))

	monitor := NewRepoMonitor()
	makeWatcher(monitor).dispatchIssueEvent("alice/myrepo", json.RawMessage(`{"action":"opened"}`))

	fired := false
	monitor.OnIssueOpened = func(_ json.RawMessage) { fired = true }
	makeWatcher(monitor).dispatchIssueEvent("alice/myrepo", json.RawMessage(`{`))
	if fired {
		t.Fatal("invalid IssuesEvent payload should not fire handler")
	}

	makeWatcher(monitor).dispatchIssueEvent("myrepo", json.RawMessage(`{"action":"opened","issue":{"number":1}}`))
	if !fired {
		t.Fatal("single-segment repo name should still dispatch")
	}
}

func TestEventsWatcher_IssueCommentEvent_CreatedOnTaggedRepo_FiresHandler(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	var got json.RawMessage
	monitor.OnIssueComment = func(p json.RawMessage) { fired++; got = p }

	w := makeWatcher(monitor)
	w.mu.Lock()
	w.seen["alice/myrepo"] = true
	w.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{
		"action":  "created",
		"issue":   map[string]any{"number": 9},
		"comment": map[string]any{"body": "@engine please look"},
	})
	w.processEvents([]eventEntry{
		{Type: "IssueCommentEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/myrepo"}, Payload: payload},
	})

	if fired != 1 {
		t.Fatalf("expected OnIssueComment to fire once, got %d", fired)
	}
	if !strings.Contains(string(got), "alice/myrepo") {
		t.Errorf("expected dispatched payload to include repo full_name, got %s", got)
	}
}

func TestEventsWatcher_IssueCommentEvent_UntaggedRepo_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnIssueComment = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	payload, _ := json.Marshal(map[string]any{
		"action":  "created",
		"issue":   map[string]any{"number": 1},
		"comment": map[string]any{"body": "hi"},
	})
	w.processEvents([]eventEntry{
		{Type: "IssueCommentEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/random"}, Payload: payload},
	})

	if fired != 0 {
		t.Fatalf("expected no dispatch for untagged repo, got %d fires", fired)
	}
}

func TestEventsWatcher_IssueCommentEvent_NonCreatedAction_NoFire(t *testing.T) {
	monitor := NewRepoMonitor()
	var fired int
	monitor.OnIssueComment = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.mu.Lock()
	w.seen["alice/myrepo"] = true
	w.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{
		"action":  "edited",
		"issue":   map[string]any{"number": 1},
		"comment": map[string]any{"body": "changed"},
	})
	w.processEvents([]eventEntry{{Type: "IssueCommentEvent", Repo: struct {
		Name string `json:"name"`
	}{Name: "alice/myrepo"}, Payload: payload}})

	if fired != 0 {
		t.Fatalf("expected no dispatch for non-created action, got %d", fired)
	}
}

func TestEventsWatcher_DispatchIssueCommentEvent_Guards(t *testing.T) {
	makeWatcher(nil).dispatchIssueCommentEvent("alice/myrepo", json.RawMessage(`{"action":"created"}`))

	monitor := NewRepoMonitor()
	makeWatcher(monitor).dispatchIssueCommentEvent("alice/myrepo", json.RawMessage(`{"action":"created"}`))

	fired := false
	monitor.OnIssueComment = func(_ json.RawMessage) { fired = true }
	makeWatcher(monitor).dispatchIssueCommentEvent("alice/myrepo", json.RawMessage(`{`))
	if fired {
		t.Fatal("invalid IssueCommentEvent payload should not fire handler")
	}

	makeWatcher(monitor).dispatchIssueCommentEvent("myrepo", json.RawMessage(`{"action":"created","issue":{"number":1},"comment":{"body":"ok"}}`))
	if !fired {
		t.Fatal("single-segment repo name should still dispatch comment")
	}
}

func TestEventsWatcher_ProcessEvents_DuplicateIDInBatch_SkipsSecond(t *testing.T) {
	monitor := NewRepoMonitor()
	fired := 0
	monitor.OnIssueOpened = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.mu.Lock()
	w.seen["alice/myrepo"] = true
	w.mu.Unlock()

	payload := json.RawMessage(`{"action":"opened","issue":{"number":1,"user":{"login":"someone"}}}`)
	w.processEvents([]eventEntry{
		{ID: "dup-1", Type: "IssuesEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/myrepo"}, Payload: payload},
		{ID: "dup-1", Type: "IssuesEvent", Repo: struct {
			Name string `json:"name"`
		}{Name: "alice/myrepo"}, Payload: payload},
	})

	if fired != 1 {
		t.Fatalf("expected one dispatch for duplicate event id, got %d", fired)
	}
}

func TestEventsWatcher_DispatchIssueEvent_UserFilterParseError(t *testing.T) {
	monitor := NewRepoMonitor()
	fired := false
	monitor.OnIssueOpened = func(_ json.RawMessage) { fired = true }

	makeWatcher(monitor).dispatchIssueEvent("alice/myrepo", json.RawMessage(`{"action":"opened","issue":{"user":{"login":123}}}`))
	if fired {
		t.Fatal("expected no dispatch when user filter decode fails")
	}
}

func TestEventsWatcher_DispatchIssueEvent_SelfOpenedIssue_Skips(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	engineLoginMu.Lock()
	engineLoginCached = ""
	engineLoginAt = time.Time{}
	engineLoginMu.Unlock()

	monitor := NewRepoMonitor()
	fired := false
	monitor.OnIssueOpened = func(_ json.RawMessage) { fired = true }

	makeWatcher(monitor).dispatchIssueEvent("alice/myrepo", json.RawMessage(`{"action":"opened","issue":{"user":{"login":"engine-bot"}}}`))
	if fired {
		t.Fatal("expected no dispatch for self-opened issue")
	}
}

func TestEventsWatcher_DispatchIssueCommentEvent_SelfComment_Skips(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_LOGIN", "engine-bot")
	engineLoginMu.Lock()
	engineLoginCached = ""
	engineLoginAt = time.Time{}
	engineLoginMu.Unlock()

	monitor := NewRepoMonitor()
	fired := false
	monitor.OnIssueComment = func(_ json.RawMessage) { fired = true }

	makeWatcher(monitor).dispatchIssueCommentEvent("alice/myrepo", json.RawMessage(`{"action":"created","issue":{"number":1},"comment":{"body":"x","user":{"login":"engine-bot"}}}`))
	if fired {
		t.Fatal("expected no dispatch for self-authored comment")
	}
}

func TestEventsWatcher_DispatchIssueCommentEvent_DedupesCommentID(t *testing.T) {
	monitor := NewRepoMonitor()
	fired := 0
	monitor.OnIssueComment = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	payload := json.RawMessage(`{"action":"created","issue":{"number":1},"comment":{"id":98765,"body":"@engine look","created_at":"2099-01-01T00:00:00Z","user":{"login":"alice"}}}`)
	w.dispatchIssueCommentEvent("alice/myrepo", payload)
	w.dispatchIssueCommentEvent("alice/myrepo", payload)

	if fired != 1 {
		t.Fatalf("expected exactly one dispatch for duplicate comment id, got %d", fired)
	}
}

func TestEventsWatcher_DispatchIssueCommentEvent_StaleCommentSkipped(t *testing.T) {
	monitor := NewRepoMonitor()
	fired := 0
	monitor.OnIssueComment = func(_ json.RawMessage) { fired++ }

	w := makeWatcher(monitor)
	w.startedAt = time.Now().UTC()
	payload := json.RawMessage(`{"action":"created","issue":{"number":1},"comment":{"id":1234,"body":"@engine old","created_at":"2000-01-01T00:00:00Z","user":{"login":"alice"}}}`)
	w.dispatchIssueCommentEvent("alice/myrepo", payload)

	if fired != 0 {
		t.Fatalf("expected stale comment to be skipped, got %d dispatches", fired)
	}
}

func TestEventsWatcher_MarkIssueCommentProcessed_ZeroIDAndLazyInit(t *testing.T) {
	w := &EventsWatcher{}
	if !w.markIssueCommentProcessed(0) {
		t.Fatal("expected zero comment ID to be accepted")
	}
	if !w.markIssueCommentProcessed(123) {
		t.Fatal("expected first non-zero comment ID to be accepted")
	}
	if w.processedIssueComments == nil || !w.processedIssueComments[123] {
		t.Fatalf("expected lazy map init to store comment ID, got %#v", w.processedIssueComments)
	}
}

func TestEventsWatcher_MarkIssueCommentProcessed_EvictsOldestWhenFull(t *testing.T) {
	w := &EventsWatcher{
		processedIssueComments: make(map[int64]bool),
	}
	for i := 0; i < maxProcessedIssueCommentIDs; i++ {
		id := int64(i + 1)
		w.processedIssueComments[id] = true
		w.processedIssueCommentOrder = append(w.processedIssueCommentOrder, id)
	}
	if !w.markIssueCommentProcessed(999999) {
		t.Fatal("expected new comment ID to be accepted")
	}
	if w.processedIssueComments[1] {
		t.Fatal("expected oldest comment ID to be evicted")
	}
	if !w.processedIssueComments[999999] {
		t.Fatal("expected newest comment ID to be retained")
	}
}

func TestEventsWatcher_IsFreshIssueComment_ZeroStartAndBadTimestamp(t *testing.T) {
	w := &EventsWatcher{}
	if !w.isFreshIssueComment("2000-01-01T00:00:00Z") {
		t.Fatal("expected zero startedAt to treat comment as fresh")
	}
	w.startedAt = time.Now().UTC()
	if !w.isFreshIssueComment("not-a-timestamp") {
		t.Fatal("expected invalid timestamp to be treated as fresh")
	}
}
