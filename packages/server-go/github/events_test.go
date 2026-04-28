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

func makeWatcher(monitor *RepoMonitor) *EventsWatcher {
	return &EventsWatcher{
		token:   "test",
		monitor: monitor,
		seen:    map[string]bool{},
		tickFn:  func(_ time.Duration) <-chan time.Time { ch := make(chan time.Time, 1); ch <- time.Now(); return ch },
		loginFn: func(_ string) (string, error) { return "testuser", nil },
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

func TestNewEventsWatcherFromEnv_NoToken_ReturnsNil(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	orig := ghCLITokenFn
	ghCLITokenFn = func() string { return "" }
	defer func() { ghCLITokenFn = orig }()
	if w := NewEventsWatcherFromEnv(NewRepoMonitor()); w != nil {
		t.Error("expected nil when GITHUB_TOKEN is absent and gh CLI returns empty")
	}
}

func TestNewEventsWatcherFromEnv_WithToken_ReturnsWatcher(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	w := NewEventsWatcherFromEnv(NewRepoMonitor())
	if w == nil {
		t.Fatal("expected non-nil EventsWatcher")
	}
}

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
		{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/myrepo"}, Payload: payload},
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
		{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/go"}, Payload: payload},
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
		{Type: "CreateEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/newrepo"}, Payload: payload},
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
		{Type: "CreateEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/existing"}, Payload: payload},
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
		{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/dup"}, Payload: pushPayload},
		{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/dup"}, Payload: pushPayload},
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
		{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/stable"}, Payload: pushPayload},
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
	ev := []eventEntry{{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/cycling"}, Payload: payload}}

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
		{Type: "PushEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/repo"}, Payload: json.RawMessage(`not json`)},
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
		{Type: "CreateEvent", Repo: struct{ Name string `json:"name"` }{Name: "alice/repo"}, Payload: json.RawMessage(`not json`)},
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
