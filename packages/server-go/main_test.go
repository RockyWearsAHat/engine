package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/engine/server/discord"
	gh "github.com/engine/server/github"
	"github.com/engine/server/remote"
	"github.com/engine/server/vpn"
	"github.com/engine/server/ws"
)

// mainRedirectTransport redirects all HTTP requests to target, preserving the path.
type mainRedirectTransport struct {
	target    string
	transport http.RoundTripper
}

// RoundTrip implements http.RoundTripper for mainRedirectTransport.
func (rt *mainRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	u, _ := url.Parse(rt.target + r.URL.Path)
	r2.URL = u
	r2.Host = u.Host
	return rt.transport.RoundTrip(r2)
}

// makeTriggerSSEServer returns an httptest.Server that serves a simple text SSE response.
func makeTriggerSSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"trigger-ok\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// fakeDiscordService is a mock implementation of the Discord service for testing.
type fakeDiscordService struct{}

// Start implements the Discord service Start method.
func (f *fakeDiscordService) Start() error { return nil }

// Close implements the Discord service Close method.
func (f *fakeDiscordService) Close() error { return nil }

// CurrentConfig implements the Discord service CurrentConfig method.
func (f *fakeDiscordService) CurrentConfig() discord.Config { return discord.Config{} }

// Reload implements the Discord service Reload method.
func (f *fakeDiscordService) Reload(cfg discord.Config) error { return nil }

// SearchHistory implements the Discord service SearchHistory method.
func (f *fakeDiscordService) SearchHistory(projectPath, query, since string, limit int) ([]db.DiscordSearchHit, error) {
	return []db.DiscordSearchHit{}, nil
}

// RecentHistory implements the Discord service RecentHistory method.
func (f *fakeDiscordService) RecentHistory(projectPath, threadID, since string, limit int) ([]db.DiscordMessage, error) {
	return []db.DiscordMessage{}, nil
}

// SendDMToOwner implements the Discord service SendDMToOwner method.
func (f *fakeDiscordService) SendDMToOwner(_ string) error      { return nil }
// NotifyProjectProgress implements the Discord service NotifyProjectProgress method.
func (f *fakeDiscordService) NotifyProjectProgress(_, _ string) {}

// withRunDepsReset resets all global run dependencies to their original values for isolated test execution.
func withRunDepsReset(t *testing.T) {
	t.Helper()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	origRun := runFn
	origLogFatal := logFatalFn
	origDBInit := dbInitFn
	origCreateSession := createSessionFn
	origSaveMessage := saveMessageFn
	origNewHub := newHubFn
	origLoadDiscordConfig := loadDiscordConfigFn
	origNewDiscordService := newDiscordServiceFn
	origSetDiscordBridge := setDiscordBridgeFn
	origNewWebhookReceiver := newWebhookReceiverFn
	origNewRepoMonitor := newRepoMonitorFn
	origRepoMonitorStart := repoMonitorStartFn
	origNewEventsWatcher := newEventsWatcherFn
	origEventsWatcherStart := eventsWatcherStartFn
	origNewVPNTunnel := newVPNTunnelFn
	origVPNRegister := vpnRegisterRoutesFn
	origVPNListen := vpnListenTLSFn
	origNewRemoteServer := newRemoteServerFn
	origSetPairing := setPairingManagerFn
	origRemoteListen := remoteListenTLSFn
	origAIChat := aiChatFn
	origHandleFunc := httpHandleFuncFn
	origHandle := httpHandleFn
	origListen := httpListenAndServeFn
	origRunAsync := runAsyncFn
	origTriggerScaffold := triggerScaffoldSessionFn
	origTriggerCI := triggerCIAnalysisSessionFn
	origTriggerIssue := triggerIssueSessionFn
	origTriggerIssueOpened := triggerIssueOpenedSessionFn
	origDBListSessionsForScaffold := dbListSessionsForScaffoldFn
	origScaffoldRunning := scaffoldTriggerRunning
	origScaffoldLastStart := scaffoldTriggerLastStart
	origIssueCommentLastDispatch := issueCommentLastDispatch
	origScaffoldAttemptTimeout := scaffoldAttemptTimeout
	origRunOrchestrator := runOrchestratorFn
	origPostStatus := postOrchestratorGitHubStatusFn

	// Default orchestrator stub: declare the project complete with a single
	// passing step so tests that only care about trigger plumbing don't fire
	// real AI calls. Tests that exercise orchestrator-specific behaviour
	// override runOrchestratorFn explicitly.
	runOrchestratorFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		return &ai.OrchestrationState{
			Owner:       cfg.Owner,
			Repo:        cfg.Repo,
			Brief:       cfg.Brief,
			Plan:        []ai.PlanStep{{Index: 1, Title: "stub", Done: true}},
			CompletedAt: "stub",
		}, nil
	}
	postOrchestratorGitHubStatusFn = func(owner, repo, phase, detail string) {}

	newEventsWatcherFn = func(_ *gh.RepoMonitor) *gh.EventsWatcher { return nil }
	eventsWatcherStartFn = func(_ *gh.EventsWatcher, _ context.Context) {}

	scaffoldTriggerMu.Lock()
	scaffoldTriggerRunning = make(map[string]bool)
	scaffoldTriggerLastStart = make(map[string]time.Time)
	scaffoldTriggerMu.Unlock()
	issueCommentTriggerMu.Lock()
	issueCommentLastDispatch = make(map[string]time.Time)
	issueCommentTriggerMu.Unlock()

	t.Cleanup(func() {
		runFn = origRun
		logFatalFn = origLogFatal
		dbInitFn = origDBInit
		createSessionFn = origCreateSession
		saveMessageFn = origSaveMessage
		newHubFn = origNewHub
		loadDiscordConfigFn = origLoadDiscordConfig
		newDiscordServiceFn = origNewDiscordService
		setDiscordBridgeFn = origSetDiscordBridge
		newWebhookReceiverFn = origNewWebhookReceiver
		newRepoMonitorFn = origNewRepoMonitor
		repoMonitorStartFn = origRepoMonitorStart
		newEventsWatcherFn = origNewEventsWatcher
		eventsWatcherStartFn = origEventsWatcherStart
		newVPNTunnelFn = origNewVPNTunnel
		vpnRegisterRoutesFn = origVPNRegister
		vpnListenTLSFn = origVPNListen
		newRemoteServerFn = origNewRemoteServer
		setPairingManagerFn = origSetPairing
		remoteListenTLSFn = origRemoteListen
		aiChatFn = origAIChat
		httpHandleFuncFn = origHandleFunc
		httpHandleFn = origHandle
		httpListenAndServeFn = origListen
		runAsyncFn = origRunAsync
		triggerScaffoldSessionFn = origTriggerScaffold
		triggerCIAnalysisSessionFn = origTriggerCI
		triggerIssueSessionFn = origTriggerIssue
		triggerIssueOpenedSessionFn = origTriggerIssueOpened
		dbListSessionsForScaffoldFn = origDBListSessionsForScaffold

		scaffoldTriggerMu.Lock()
		scaffoldTriggerRunning = origScaffoldRunning
		scaffoldTriggerLastStart = origScaffoldLastStart
		scaffoldTriggerMu.Unlock()
		issueCommentTriggerMu.Lock()
		issueCommentLastDispatch = origIssueCommentLastDispatch
		issueCommentTriggerMu.Unlock()
		scaffoldAttemptTimeout = origScaffoldAttemptTimeout
		runOrchestratorFn = origRunOrchestrator
		postOrchestratorGitHubStatusFn = origPostStatus
	})
}

// withIssueCommentStateMutex saves and restores the issue comment dispatch state for test isolation.
func withIssueCommentStateMutex(t *testing.T) {
	t.Helper()
	issueCommentTriggerMu.Lock()
	orig := issueCommentLastDispatch
	issueCommentLastDispatch = make(map[string]time.Time)
	issueCommentTriggerMu.Unlock()
	t.Cleanup(func() {
		issueCommentTriggerMu.Lock()
		issueCommentLastDispatch = orig
		issueCommentTriggerMu.Unlock()
	})
}

// setupScaffoldTest initializes database, AI mock, and issue comment state for scaffold/issue tests.
func setupScaffoldTest(t *testing.T, owner, repo, readme string) string {
	t.Helper()
	projectPath := t.TempDir()
	return setupScaffoldTestWithPath(t, projectPath, owner, repo, readme)
}

// setupTriggerTestWithDB initializes database, AI mock server, and scaffold repo for trigger tests.
func setupTriggerTestWithDB(t *testing.T, owner, repo, readme string) (projectPath, targetPath string) {
	t.Helper()
	projectPath = t.TempDir()
	setupBaseTestEnvironment(t, projectPath)
	targetPath = prepareScaffoldTargetRepo(t, projectPath, owner, repo, readme)
	return
}

func TestReserveIssueCommentDispatch_DedupesWithinCooldown(t *testing.T) {
	withIssueCommentStateMutex(t)

	if ok := reserveIssueCommentDispatch("/tmp/a", "owner/repo", 42, 9001, "@engine please fix"); !ok {
		t.Fatal("expected first dispatch reservation to pass")
	}
	if ok := reserveIssueCommentDispatch("/tmp/a", "owner/repo", 42, 9001, "@engine please fix"); ok {
		t.Fatal("expected duplicate dispatch reservation to be suppressed")
	}
}

func TestReserveIssueCommentDispatch_DifferentCommentIDAllowsDispatch(t *testing.T) {
	withIssueCommentStateMutex(t)

	if ok := reserveIssueCommentDispatch("/tmp/a", "owner/repo", 42, 1, "@engine please fix"); !ok {
		t.Fatal("expected first comment dispatch reservation")
	}
	if ok := reserveIssueCommentDispatch("/tmp/a", "owner/repo", 42, 2, "@engine please fix"); !ok {
		t.Fatal("expected distinct comment id to reserve separately")
	}
}

func TestIssueCommentDispatchKey_TrimsAndTruncates(t *testing.T) {
	got := issueCommentDispatchKey("  /tmp/project  ", "  owner/repo  ", 7, 0, "  "+strings.Repeat("x", 200)+"  ")
	if !strings.Contains(got, "/tmp/project#owner/repo#7#") {
		t.Fatalf("unexpected key prefix: %q", got)
	}
	if strings.HasSuffix(got, strings.Repeat("x", 200)) {
		t.Fatalf("expected body to be truncated, got %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("expected whitespace to be trimmed, got %q", got)
	}
}

func TestReserveIssueCommentDispatch_CleansUpExpiredEntries(t *testing.T) {
	withIssueCommentStateMutex(t)

	issueCommentTriggerMu.Lock()
	for i := 0; i < 4100; i++ {
		issueCommentLastDispatch[fmt.Sprintf("old-%d", i)] = time.Now().Add(-4 * issueCommentTriggerCooldown)
	}
	issueCommentTriggerMu.Unlock()

	if ok := reserveIssueCommentDispatch("/tmp/a", "owner/repo", 42, 3, "@engine please fix"); !ok {
		t.Fatal("expected reservation to pass")
	}

	issueCommentTriggerMu.Lock()
	defer issueCommentTriggerMu.Unlock()
	if len(issueCommentLastDispatch) > 2 {
		t.Fatalf("expected cleanup to prune old entries, got %d entries", len(issueCommentLastDispatch))
	}
}

// TestTriggerIssueSession_DuplicateSuppressed seeds a scaffold repo once and verifies
// duplicate comment dispatches stay in the same prepared write-backed project state.
func TestTriggerIssueSession_DuplicateSuppressed(t *testing.T) {
	projectPath := setupScaffoldTest(t, "owner", "repo", "# Demo\n@engine")

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"},"id":9001},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	triggerIssueSession(projectPath, payload)
}

// TestTriggerIssueSession_UsesActiveOrchestratorRedirect seeds repo files for the trigger,
// then verifies issue comments are redirected into the already running orchestrator.
func TestTriggerIssueSession_UsesActiveOrchestratorRedirect(t *testing.T) {
	projectPath, targetPath := setupTriggerTestWithDB(t, "owner", "repo", "# Demo\n@engine")
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_, _ = ai.RunAutonomousProject(ai.OrchestratorConfig{
			ProjectPath:        targetPath,
			Brief:              "brief",
			MaxOuterIterations: 1,
			OnPhase:            func(string, string) {},
			OnProgress:         func(string) {},
			OnError:            func(string) {},
			ChatFn: func(ctx *ai.ChatContext, userMessage string) {
				if ctx.Role == ai.RoleGriller {
					<-release
					ctx.OnChunk("# Design\nA tiny app.", false)
				}
			},
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ai.GetOrchestratorHandle(targetPath) != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ai.GetOrchestratorHandle(targetPath) == nil {
		t.Fatal("expected active orchestrator handle")
	}

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"},"id":9001},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	close(release)
	if h := ai.GetOrchestratorHandle(targetPath); h != nil {
		h.Stop()
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected orchestrator goroutine to exit")
	}
}

// setupTestDB initializes a test database at the given project path.
func setupTestDB(t *testing.T, projectPath string) {
	t.Helper()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
}

// setupRunModeLocalTest initializes a run() test with local mode (no VPN/remote).
func setupRunModeLocalTest(t *testing.T) string {
	t.Helper()
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")
	return projectPath
}

// setupTriggerTestWithAIEnv initializes a trigger test with AI environment variables.
func setupTriggerTestWithAIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
}

// setupTestDBAndScaffoldRepo initializes database and prepares a scaffold target repository for testing.
func setupTestDBAndScaffoldRepo(t *testing.T, owner, repo, readme string) (projectPath, targetPath string) {
	t.Helper()
	projectPath = t.TempDir()
	setupTestDB(t, projectPath)
	targetPath = prepareScaffoldTargetRepo(t, projectPath, owner, repo, readme)
	return
}

// assertSessionCreated verifies that a session was created by checking count increase.
func assertSessionCreated(t *testing.T, projectPath string, beforeCount int) {
	t.Helper()
	afterCount := countSessions(t, projectPath)
	if afterCount <= beforeCount {
		t.Fatalf("expected session count to increase, before=%d after=%d", beforeCount, afterCount)
	}
}

// triggerAndAssertSessionCreated is a consolidated helper for tests that trigger a payload and verify session creation.
func triggerAndAssertSessionCreated(t *testing.T, triggerFn func(string, json.RawMessage), projectPath, targetPath string, payload json.RawMessage) {
	t.Helper()
	before := countSessions(t, targetPath)
	triggerFn(projectPath, payload)
	assertSessionCreated(t, targetPath, before)
}

// countSessions returns the number of sessions in the database at projectPath.
func countSessions(t *testing.T, projectPath string) int {
	t.Helper()
	sessions, err := db.ListSessions(projectPath)
	if err != nil {
		t.Fatalf("db.ListSessions: %v", err)
	}
	return len(sessions)
}

// prepareScaffoldTargetRepo sets up a mock scaffold target repository with the given README for testing.
func prepareScaffoldTargetRepo(t *testing.T, baseProjectPath, owner, repo, readme string) string {
	t.Helper()
	if strings.TrimSpace(os.Getenv("ENGINE_CLONES_DIR")) == "" {
		t.Setenv("ENGINE_CLONES_DIR", filepath.Join(baseProjectPath, ".engine", "projects"))
	}
	targetPath := buildAutonomousRepoPath(baseProjectPath, owner, repo)

	// Reset the in-memory dedup state so tests don't interfere via the shared cooldown map.
	repoKey := strings.ToLower(owner + "/" + repo)
	scaffoldTriggerMu.Lock()
	delete(scaffoldTriggerLastStart, repoKey)
	delete(scaffoldTriggerRunning, repoKey)
	scaffoldTriggerMu.Unlock()
	t.Cleanup(func() {
		scaffoldTriggerMu.Lock()
		delete(scaffoldTriggerLastStart, repoKey)
		delete(scaffoldTriggerRunning, repoKey)
		scaffoldTriggerMu.Unlock()
	})

	if err := os.MkdirAll(filepath.Join(targetPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir scaffold target git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetPath, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatalf("write scaffold target README: %v", err)
	}

	orig := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "show" && strings.HasSuffix(args[3], ":README.md") {
			return []byte(readme), nil
		}
		return []byte("ok"), nil
	}
	t.Cleanup(func() {
		runCommandCombinedOutputFn = orig
	})

	return targetPath
}

func TestDefaultProjectPath_NonEmpty(t *testing.T) {
	path := defaultProjectPath()
	if path == "" {
		t.Error("defaultProjectPath() should never return empty string")
	}
}

func TestBuildAutonomousRepoPath_Default(t *testing.T) {
	base := "/tmp/engine-root"
	t.Setenv("ENGINE_CLONES_DIR", "")
	withPathDepsReset(t)
	osUserHomeDirFn = func() (string, error) { return "/tmp/home", nil }
	got := buildAutonomousRepoPath(base, "octo", "demo")
	want := filepath.Join("/tmp/home", ".engine", "projects", "octo-demo")
	if got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
}

func TestBuildAutonomousRepoPath_EmptyOwner(t *testing.T) {
	base := "/tmp/engine-root"
	t.Setenv("ENGINE_CLONES_DIR", "")
	withPathDepsReset(t)
	osUserHomeDirFn = func() (string, error) { return "/tmp/home", nil }
	got := buildAutonomousRepoPath(base, "", "demo")
	want := filepath.Join("/tmp/home", ".engine", "projects", "demo")
	if got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
}

// withRunCommandMocked temporarily replaces runCommandCombinedOutputFn with the given mock function for test isolation.
func withRunCommandMocked(t *testing.T, mockFn func(string, ...string) ([]byte, error)) {
	t.Helper()
	orig := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = mockFn
	t.Cleanup(func() {
		runCommandCombinedOutputFn = orig
	})
}

// setupRunModeBaseDeps initializes common run() dependencies (dbInit, discord config, discord service).
func setupRunModeBaseDeps(t *testing.T) {
	t.Helper()
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	dbInitFn = func(path string) error { return nil }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
}

// setupBaseTestEnvironment initializes database, AI mock, and issue comment state for all trigger/scaffold tests.
func setupBaseTestEnvironment(t *testing.T, projectPath string) {
	t.Helper()
	setupTestDB(t, projectPath)
	withAIMockServer(t)
	withIssueCommentStateMutex(t)
}

// setupScaffoldTestWithPath initializes database, AI mock, issue comment state, and scaffold repo at the given project path.
func setupScaffoldTestWithPath(t *testing.T, projectPath, owner, repo, readme string) string {
	t.Helper()
	setupBaseTestEnvironment(t, projectPath)
	prepareScaffoldTargetRepo(t, projectPath, owner, repo, readme)
	return projectPath
}

// setupRunModeLocalWithCommonMocks initializes a run() test for local mode (no VPN/remote)
// with all common dependency mocks already set for testing typical flows.
func setupRunModeLocalWithCommonMocks(t *testing.T) string {
	t.Helper()
	return setupRunModeLocalWithDiscordDisabled(t)
}

// setupRunModeWithHTTPMocks initializes HTTP mocking for run() tests that need to control
// the HTTP handler registration and listen flow. Caller provides functions to set handlers.
func setupRunModeWithHTTPMocks(t *testing.T,
	onHandleFunc func(pattern string, handler func(http.ResponseWriter, *http.Request)) bool,
	onHandle func(pattern string, handler http.Handler),
	onListenAndServe func(addr string, handler http.Handler) error) {
	t.Helper()
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {
		if onHandleFunc != nil {
			onHandleFunc(pattern, handler)
		}
	}
	httpHandleFn = func(pattern string, handler http.Handler) {
		if onHandle != nil {
			onHandle(pattern, handler)
		}
	}
	httpListenAndServeFn = func(addr string, handler http.Handler) error {
		if onListenAndServe != nil {
			return onListenAndServe(addr, handler)
		}
		return nil
	}
}

// setupDisabledDiscordAndDB sets up disabled Discord and no-op database initialization.
// Used when tests need Discord disabled but don't require other elaborate mocking.
func setupDisabledDiscordAndDB(t *testing.T) {
	t.Helper()
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
}

// setupRunModeLocalDefaultMocks initializes run() test for local mode with all default mocks:
// withRunDepsReset, project path setup, disabled Discord, and no-op HTTP handlers.
// This is the most common pattern for local mode tests.
func setupRunModeLocalDefaultMocks(t *testing.T) string {
	t.Helper()
	projectPath := setupRunModeLocalWithDiscordDisabled(t)
	httpHandleFuncFn = func(_ string, _ func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(_ string, _ http.Handler) {}
	httpListenAndServeFn = func(_ string, _ http.Handler) error { return nil }
	return projectPath
}

// setupRunModeLocalWithDiscordDisabled initializes a run() test with local mode (no VPN/remote)
// with Discord and database mocking disabled. Consolidates setupRunModeLocalTest + setupDisabledDiscordAndDB.
func setupRunModeLocalWithDiscordDisabled(t *testing.T) string {
	t.Helper()
	projectPath := setupRunModeLocalTest(t)
	setupDisabledDiscordAndDB(t)
	return projectPath
}

// setupRunTriggerWithHubAndDiscordConfig sets up run() with database initialized,
// hub created, and Discord enabled config loaded. Common for Discord integration tests.
// Caller should set newDiscordServiceFn to control Discord service behavior.
func setupRunTriggerWithHubAndDiscordConfig(t *testing.T) string {
	t.Helper()
	projectPath := setupRunModeLocalTest(t)
	dbInitFn = func(path string) error { return db.Init(path) }
	newHubFn = func(path string) *ws.Hub { return ws.NewHub(path) }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: true, BotToken: "tok", GuildID: "g1"}, nil
	}
	setupRunModeWithHTTPMocks(t,
		func(pattern string, handler func(http.ResponseWriter, *http.Request)) bool { return false },
		func(pattern string, handler http.Handler) {},
		func(addr string, handler http.Handler) error { return errors.New("test stop") })
	return projectPath
}

// setupRunEventWatcherWithRepoMonitor initializes run() with repo monitor and events watcher
// for testing GitHub event handling. Sets up all base dependencies plus repo monitoring.
func setupRunEventWatcherWithRepoMonitor(t *testing.T) (string, *gh.RepoMonitor) {
	t.Helper()
	projectPath := setupRunModeLocalWithDiscordDisabled(t)
	var monitor *gh.RepoMonitor
	newRepoMonitorFn = func() *gh.RepoMonitor {
		monitor = gh.NewRepoMonitor()
		return monitor
	}
	repoMonitorStartFn = func(rm *gh.RepoMonitor) {}
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(pattern string, handler http.Handler) {}
	httpListenAndServeFn = func(addr string, handler http.Handler) error { return errors.New("stop") }
	runAsyncFn = func(fn func()) { fn() }
	triggerScaffoldSessionFn = func(string, json.RawMessage) {}
	triggerCIAnalysisSessionFn = func(string, json.RawMessage) {}
	triggerIssueSessionFn = func(string, json.RawMessage) {}
	triggerIssueOpenedSessionFn = func(string, json.RawMessage) {}
	return projectPath, monitor
}

// setupRunTriggerWithAIEnv initializes run() with AI environment for tests that need model config.
func setupRunTriggerWithAIEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	return t.TempDir()
}

// setupRunTriggerWithEventsWatcher sets up run() with events watcher configuration.
// Returns projectPath and a marker to track if watcher start was called.
func setupRunTriggerWithEventsWatcher(t *testing.T) string {
	t.Helper()
	projectPath := setupRunModeLocalWithDiscordDisabled(t)
	httpHandleFuncFn = func(_ string, _ func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(_ string, _ http.Handler) {}
	httpListenAndServeFn = func(_ string, _ http.Handler) error { return errors.New("stop") }
	fakeWatcher := gh.NewEventsWatcher("fake-token", gh.NewRepoMonitor())
	newEventsWatcherFn = func(_ *gh.RepoMonitor) *gh.EventsWatcher { return fakeWatcher }
	eventsWatcherStartFn = func(_ *gh.EventsWatcher, _ context.Context) {}
	return projectPath
}

// setupTriggerWithAIMock initializes a trigger test with database and AI mock server.
func setupTriggerWithAIMock(t *testing.T) string {
	t.Helper()
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	withAIMockServer(t)
	return projectPath
}

// callAllTriggerFunctions invokes all four trigger session types with the given payloads.
// Useful for tests that validate error handling or side effects across all trigger types.
func callAllTriggerFunctions(projectPath string,
	scaffoldPayload, ciPayload, issuePayload, issueOpenedPayload json.RawMessage) {
	triggerScaffoldSession(projectPath, scaffoldPayload)
	triggerCIAnalysisSession(projectPath, ciPayload)
	triggerIssueSession(projectPath, issuePayload)
	triggerIssueOpenedSession(projectPath, issueOpenedPayload)
}

// withAIChatMocked temporarily replaces aiChatFn with the given mock for test isolation.
// Automatically restores the original via t.Cleanup.
func withAIChatMocked(t *testing.T, mockFn func(*ai.ChatContext, string)) {
	t.Helper()
	orig := aiChatFn
	aiChatFn = mockFn
	t.Cleanup(func() {
		aiChatFn = orig
	})
}

// withAIChatNoOp stubs aiChatFn with a no-op implementation for tests that trigger AI
// but only care about side effects like session creation, not the chat itself.
func withAIChatNoOp(t *testing.T) {
	t.Helper()
	withAIChatMocked(t, func(*ai.ChatContext, string) {})
}

// setupCIAnalysisTest initializes a CI analysis trigger test: temp project, database, and AI environment.
func setupCIAnalysisTest(t *testing.T) string {
	t.Helper()
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	setupTriggerTestWithAIEnv(t)
	return projectPath
}

// setupTriggerFullStack combines setupTriggerTestWithDB and setupTriggerTestWithAIEnv in one call.
// Returns (projectPath, targetPath) for tests needing database, AI mocks, and a mock scaffold repository.
func setupTriggerFullStack(t *testing.T, owner, repo, readme string) (projectPath, targetPath string) {
	t.Helper()
	projectPath, targetPath = setupTriggerTestWithDB(t, owner, repo, readme)
	setupTriggerTestWithAIEnv(t)
	return
}

// makeScaffoldPayload returns a standard scaffold trigger payload for testing.
func makeScaffoldPayload() json.RawMessage {
	return json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
}

// makeIssueCommentPayload returns a standard issue comment trigger payload for testing.
func makeIssueCommentPayload() json.RawMessage {
	return json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"},"id":9001},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
}

// makeIssueOpenedPayload returns a standard issue opened trigger payload for testing.
func makeIssueOpenedPayload() json.RawMessage {
	return json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
}

// makeCIPayload returns a standard CI analysis trigger payload for testing.
func makeCIPayload() json.RawMessage {
	return json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`)
}

// assertSessionsCreated verifies that at least one session exists in the database at projectPath.
func assertSessionsCreated(t *testing.T, projectPath string) {
	t.Helper()
	sessions, err := db.ListSessions(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Error("expected at least one session to be created")
	}
}

func TestBuildAutonomousRepoPath_HomeErrorFallsBackToProject(t *testing.T) {
	base := "/tmp/engine-root"
	t.Setenv("ENGINE_CLONES_DIR", "")
	withPathDepsReset(t)
	osUserHomeDirFn = func() (string, error) { return "", errors.New("home unavailable") }

	got := buildAutonomousRepoPath(base, "octo", "fallback")
	want := filepath.Join(base, ".engine", "projects", "octo-fallback")
	if got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
}

func TestRunCommandCombinedOutputFn_DefaultExecutes(t *testing.T) {
	out, err := runCommandCombinedOutputFn("printf", "ok")
	if err != nil {
		t.Fatalf("default command runner failed: %v", err)
	}
	if string(out) != "ok" {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestScaffoldTrigger_DedupeAndCooldown(t *testing.T) {
	withRunDepsReset(t)
	repoKey := "owner/repo"

	if !beginScaffoldTrigger(repoKey) {
		t.Fatal("expected first scaffold trigger to start")
	}
	if beginScaffoldTrigger(repoKey) {
		t.Fatal("expected concurrent scaffold trigger to be deduped")
	}

	finishScaffoldTrigger(repoKey)
	if beginScaffoldTrigger(repoKey) {
		t.Fatal("expected immediate restart to be deduped by cooldown")
	}

	scaffoldTriggerMu.Lock()
	scaffoldTriggerLastStart[repoKey] = time.Now().Add(-(scaffoldTriggerCooldown + time.Second))
	scaffoldTriggerMu.Unlock()

	if !beginScaffoldTrigger(repoKey) {
		t.Fatal("expected scaffold trigger to run after cooldown elapsed")
	}
}

// TestHasRecentScaffoldSession writes scaffold session state into the prepared repo
// and verifies only recent matching scaffold sessions are reported.
func TestHasRecentScaffoldSession(t *testing.T) {
	_, target := setupTestDBAndScaffoldRepo(t, "owner", "repo", "# Demo\n@engine")
	if err := db.WithProject(target, func() error {
		if err := db.CreateSession("scaffold-repo-123", target, "main"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("WithProject create scaffold session: %v", err)
	}

	if !hasRecentScaffoldSession(target, "repo", 2*time.Minute) {
		t.Fatal("expected recent scaffold session to be detected")
	}
	if hasRecentScaffoldSession(target, "repo", 0) {
		t.Fatal("expected zero-duration window to return false")
	}
	if hasRecentScaffoldSession(target, "other-repo", 2*time.Minute) {
		t.Fatal("expected non-matching repo prefix to return false")
	}
}

func TestTriggerScaffoldSession_DedupesWhenRecentScaffoldExists(t *testing.T) {
	projectPath, target := setupTestDBAndScaffoldRepo(t, "owner", "repo", "# Demo\n@engine")
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	if err := db.WithProject(target, func() error {
		return db.CreateSession("scaffold-repo-987", target, "main")
	}); err != nil {
		t.Fatalf("WithProject create scaffold session: %v", err)
	}

	chatCalls := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		chatCalls++
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	if chatCalls != 0 {
		t.Fatalf("expected deduped scaffold trigger to skip aiChatFn, got calls=%d", chatCalls)
	}
}

func TestEnsureAutonomousRepoWorkspace_MkdirError(t *testing.T) {
	base := t.TempDir()
	// Place a file where ENGINE_CLONES_DIR would need to create a subdirectory.
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// dest = blocker/octo-demo; filepath.Dir(dest) = blocker (a file) → MkdirAll fails.
	t.Setenv("ENGINE_CLONES_DIR", blocker)
	_, err := ensureAutonomousRepoWorkspace(base, "octo", "demo")
	if err == nil {
		t.Fatal("expected MkdirAll error, got nil")
	}
}

func TestEnsureAutonomousRepoWorkspace_FetchError(t *testing.T) {
	base := t.TempDir()
	clonesDir := filepath.Join(base, "clones")
	t.Setenv("ENGINE_CLONES_DIR", clonesDir)
	dest := filepath.Join(clonesDir, "octo-demo")
	if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	withRunCommandMocked(t, func(_ string, _ ...string) ([]byte, error) {
		return []byte("fetch failed"), fmt.Errorf("fetch error")
	})
	_, err := ensureAutonomousRepoWorkspace(base, "octo", "demo")
	if err == nil {
		t.Fatal("expected fetch error, got nil")
	}
	if !strings.Contains(err.Error(), "fetch repo update") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestEnsureAutonomousRepoWorkspace_PullError(t *testing.T) {
	base := t.TempDir()
	clonesDir := filepath.Join(base, "clones")
	t.Setenv("ENGINE_CLONES_DIR", clonesDir)
	dest := filepath.Join(clonesDir, "octo-demo")
	if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	call := 0
	withRunCommandMocked(t, func(_ string, _ ...string) ([]byte, error) {
		call++
		if call == 1 {
			return []byte("ok"), nil
		}
		return []byte("pull failed"), fmt.Errorf("pull error")
	})
	_, err := ensureAutonomousRepoWorkspace(base, "octo", "demo")
	if err == nil {
		t.Fatal("expected pull error, got nil")
	}
	if !strings.Contains(err.Error(), "pull repo update") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestBuildReadmeAutonomousBuildPrompt_ContainsFullPhases(t *testing.T) {
	prompt := buildReadmeAutonomousBuildPrompt("octo", "demo", "/tmp/demo")
	required := []string{
		"Execution contract (must complete all phases)",
		"1. Understand",
		"2. Scaffold",
		"3. Implement",
		"4. Validate",
		"5. Deliver",
		"Run the real build/test commands",
		"Commit all completed work with git_commit",
	}
	for _, fragment := range required {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("prompt missing required fragment %q", fragment)
		}
	}
}

func TestEnsureAutonomousRepoWorkspace_CloneFlow(t *testing.T) {
	base := t.TempDir()
	clonesDir := filepath.Join(base, "clones")
	t.Setenv("ENGINE_CLONES_DIR", clonesDir)

	orig := runCommandCombinedOutputFn
	defer func() { runCommandCombinedOutputFn = orig }()

	var calls [][]string
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return []byte("ok"), nil
	}

	dest, err := ensureAutonomousRepoWorkspace(base, "octo", "demo")
	if err != nil {
		t.Fatalf("ensureAutonomousRepoWorkspace returned error: %v", err)
	}
	wantDest := filepath.Join(clonesDir, "octo-demo")
	if dest != wantDest {
		t.Fatalf("unexpected destination: got %q want %q", dest, wantDest)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 git command, got %d", len(calls))
	}
	if !strings.Contains(strings.Join(calls[0], " "), "git clone https://github.com/octo/demo.git") {
		t.Fatalf("expected clone command, got %v", calls[0])
	}
}

func TestEnsureAutonomousRepoWorkspace_UpdateFlow(t *testing.T) {
	base := t.TempDir()
	clonesDir := filepath.Join(base, "clones")
	t.Setenv("ENGINE_CLONES_DIR", clonesDir)
	dest := filepath.Join(clonesDir, "octo-demo")
	if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
		t.Fatalf("create git dir: %v", err)
	}

	orig := runCommandCombinedOutputFn
	defer func() { runCommandCombinedOutputFn = orig }()

	var calls [][]string
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return []byte("ok"), nil
	}

	gotDest, err := ensureAutonomousRepoWorkspace(base, "octo", "demo")
	if err != nil {
		t.Fatalf("ensureAutonomousRepoWorkspace returned error: %v", err)
	}
	if gotDest != dest {
		t.Fatalf("unexpected destination: got %q want %q", gotDest, dest)
	}
	if len(calls) != 3 {
		t.Fatalf("expected fetch+clean+reset commands, got %d", len(calls))
	}
	fetchCmd := strings.Join(calls[0], " ")
	cleanCmd := strings.Join(calls[1], " ")
	resetCmd := strings.Join(calls[2], " ")
	if !strings.Contains(fetchCmd, "git -C "+dest+" fetch origin --prune") {
		t.Fatalf("unexpected fetch command: %s", fetchCmd)
	}
	if !strings.Contains(cleanCmd, "git -C "+dest+" clean -fdx -e .engine") {
		t.Fatalf("unexpected clean command: %s", cleanCmd)
	}
	if !strings.Contains(resetCmd, "git -C "+dest+" reset --hard origin/HEAD") {
		t.Fatalf("unexpected reset command: %s", resetCmd)
	}
}

func TestTriggerIssueSession_BadPayload(t *testing.T) {
	// Bad JSON — ParseIssueComment fails; function returns early without touching DB or AI.
	triggerIssueSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerIssueSession_ZeroIssueNumber(t *testing.T) {
	// Valid JSON but issue.number is zero — treated as unparseable, returns early.
	payload := json.RawMessage(`{"action":"created","comment":{"body":"hi","user":{"login":"bob"}},"issue":{"number":0,"title":""},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(t.TempDir(), payload)
}

func TestTriggerIssueOpenedSession_BadPayload(t *testing.T) {
	// Bad JSON — ParseIssue fails; function returns early without touching DB or AI.
	triggerIssueOpenedSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerIssueOpenedSession_ZeroIssueNumber(t *testing.T) {
	// Valid JSON but issue.number is zero — returns early.
	payload := json.RawMessage(`{"action":"opened","issue":{"number":0,"title":""},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(t.TempDir(), payload)
}

func TestTriggerScaffoldSession_BadPayload(t *testing.T) {
	// Bad JSON should return early without side effects.
	triggerScaffoldSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerScaffoldSession_BadFullName(t *testing.T) {
	// Missing owner/repo separator should return early.
	payload := json.RawMessage(`{"repository":{"full_name":"owner-only"}}`)
	triggerScaffoldSession(t.TempDir(), payload)
}

func TestReadmeContainsEngineTag_Present(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# My Project\n\n@engine please build this"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if !readmeContainsEngineTag(dir) {
		t.Error("expected readmeContainsEngineTag to return true when @engine is present")
	}
}

func TestReadmeContainsEngineTag_Absent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# My Project\n\nNo trigger here."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if readmeContainsEngineTag(dir) {
		t.Error("expected readmeContainsEngineTag to return false when @engine is absent")
	}
}

func TestReadmeContainsEngineTag_MissingFile(t *testing.T) {
	if readmeContainsEngineTag(t.TempDir()) {
		t.Error("expected readmeContainsEngineTag to return false when README.md does not exist")
	}
}

func TestReadmeContainsEngineTag_PrefersOriginHeadOverDirtyLocalReadme(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare remote: %v: %s", err, strings.TrimSpace(string(out)))
	}

	clone := filepath.Join(root, "repo")
	if out, err := exec.Command("git", "clone", remote, clone).CombinedOutput(); err != nil {
		t.Fatalf("clone remote: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", clone, "config", "user.email", "test@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config user.email: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", clone, "config", "user.name", "Test").CombinedOutput(); err != nil {
		t.Fatalf("git config user.name: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("# Remote\n\n@engine"), 0o644); err != nil {
		t.Fatalf("write remote README: %v", err)
	}
	if out, err := exec.Command("git", "-C", clone, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", clone, "commit", "-m", "seed readme").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", clone, "push", "origin", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git push: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// Dirty local working tree should not suppress the remote @engine signal.
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("# Local\n\nNo tag"), 0o644); err != nil {
		t.Fatalf("write dirty local README: %v", err)
	}

	if !readmeContainsEngineTag(clone) {
		t.Fatal("expected origin/HEAD README to remain authoritative when local README is dirty")
	}
}

func TestTriggerScaffoldSession_NoEngineTag_Skips(t *testing.T) {
	projectPath, _ := setupTestDBAndScaffoldRepo(t, "owner", "repo", "# My Project\n\nNo trigger here.")

	before := countSessions(t, projectPath)
	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	after := countSessions(t, projectPath)

	if after != before {
		t.Fatalf("expected no session created when @engine tag absent, before=%d after=%d", before, after)
	}
}

func TestEnsureAutonomousRepoWorkspace_CloneFailure(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_CLONES_DIR", filepath.Join(projectPath, ".engine", "projects"))
	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		return []byte("clone denied"), errors.New("boom")
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	_, err := ensureAutonomousRepoWorkspace(projectPath, "owner", "clonefail")
	if err == nil || !strings.Contains(err.Error(), "clone repo") {
		t.Fatalf("expected clone failure, got %v", err)
	}
}

func TestTriggerScaffoldSession_CloneFailureSkips(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_CLONES_DIR", filepath.Join(projectPath, ".engine", "projects"))
	withRunCommandMocked(t, func(name string, args ...string) ([]byte, error) {
		return []byte("clone denied"), errors.New("boom")
	})

	called := false
	withAIChatMocked(t, func(_ *ai.ChatContext, _ string) { called = true })

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/clonefail"}}`))
	if called {
		t.Fatal("AI should not run when clone/sync fails")
	}
}

func TestTriggerCIAnalysisSession_BadPayload(t *testing.T) {
	// Bad JSON should return early without side effects.
	triggerCIAnalysisSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerScaffoldSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath, targetPath := setupTriggerFullStack(t, "owner", "repo", "# Demo\n@engine")

	triggerAndAssertSessionCreated(t, triggerScaffoldSession, projectPath, targetPath, makeScaffoldPayload())
}

func TestTriggerCIAnalysisSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := setupCIAnalysisTest(t)

	before := countSessions(t, projectPath)
	triggerCIAnalysisSession(projectPath, makeCIPayload())
	assertSessionCreated(t, projectPath, before)
}

func TestTriggerIssueSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath, targetPath := setupTriggerFullStack(t, "owner", "repo", "# Demo\n@engine")

	triggerAndAssertSessionCreated(t, triggerIssueSession, projectPath, targetPath, makeIssueCommentPayload())
}

func TestTriggerIssueOpenedSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath, targetPath := setupTriggerFullStack(t, "owner", "repo", "# Demo\n@engine")

	triggerAndAssertSessionCreated(t, triggerIssueOpenedSession, projectPath, targetPath, makeIssueOpenedPayload())
}

func TestRun_DBInitError(t *testing.T) {
	withRunDepsReset(t)
	dbInitFn = func(projectPath string) error { return errors.New("db fail") }

	err := run()
	if err == nil {
		t.Fatal("expected run to return db init error")
	}
}

func TestRun_DiscordConfigError(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{}, errors.New("bad discord config")
	}

	err := run()
	if err == nil {
		t.Fatal("expected run to return discord config error")
	}
}

func TestRun_LocalMode_ListenError(t *testing.T) {
	setupRunModeLocalDefaultMocks(t)
	t.Setenv("PORT", "31337")

	listenErr := errors.New("listen failed")
	var healthHandler func(http.ResponseWriter, *http.Request)
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {
		if pattern == "/health" {
			healthHandler = handler
		}
	}
	httpListenAndServeFn = func(addr string, handler http.Handler) error {
		return listenErr
	}

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error, got %v", err)
	}
	if healthHandler == nil {
		t.Fatal("expected health handler to be registered")
	}

	optReq := httptest.NewRequest(http.MethodOptions, "/health", nil)
	optRec := httptest.NewRecorder()
	healthHandler(optRec, optReq)
	if optRec.Code != http.StatusNoContent {
		t.Fatalf("options status = %d, want %d", optRec.Code, http.StatusNoContent)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	getRec := httptest.NewRecorder()
	healthHandler(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", getRec.Code, http.StatusOK)
	}
}

func TestRun_VPNMode_TunnelInitError(t *testing.T) {
	withRunDepsReset(t)
	setupRunModeLocalTest(t)
	t.Setenv("ENGINE_VPN", "1")

	setupDisabledDiscordAndDB(t)
	newVPNTunnelFn = func(cfg vpn.Config) (*vpn.Tunnel, error) {
		return nil, errors.New("vpn init failed")
	}

	err := run()
	if err == nil {
		t.Fatal("expected VPN mode error")
	}
}

func TestRun_RemoteMode_ServerInitError(t *testing.T) {
	withRunDepsReset(t)
	setupRunModeLocalTest(t)
	t.Setenv("ENGINE_REMOTE", "1")

	setupDisabledDiscordAndDB(t)
	newRemoteServerFn = func(cfg remote.Config, wsHandler http.HandlerFunc) (*remote.Server, error) {
		return nil, errors.New("remote init failed")
	}

	err := run()
	if err == nil {
		t.Fatal("expected remote mode error")
	}
}

func TestRun_VPNMode_ListenErrorAfterRegister(t *testing.T) {
	setupRunModeLocalDefaultMocks(t)
	t.Setenv("ENGINE_VPN", "1")
	t.Setenv("ENGINE_REMOTE", "")

	listenErr := errors.New("vpn listen failed")
	registered := false
	newVPNTunnelFn = func(cfg vpn.Config) (*vpn.Tunnel, error) {
		return &vpn.Tunnel{}, nil
	}
	vpnRegisterRoutesFn = func(t *vpn.Tunnel, mux *http.ServeMux, wsHandler http.HandlerFunc) {
		registered = true
	}
	vpnListenTLSFn = func(t *vpn.Tunnel, mux *http.ServeMux) error {
		return listenErr
	}

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected vpn listen error, got %v", err)
	}
	if !registered {
		t.Fatal("expected VPN routes to be registered")
	}
}

func TestRun_RemoteMode_ListenErrorAfterPairing(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "1")

	listenErr := errors.New("remote listen failed")
	pairingSet := false
	setupDisabledDiscordAndDB(t)
	newRemoteServerFn = func(cfg remote.Config, wsHandler http.HandlerFunc) (*remote.Server, error) {
		return &remote.Server{Pairing: remote.NewPairingManager()}, nil
	}
	setPairingManagerFn = func(pm *remote.PairingManager) {
		if pm != nil {
			pairingSet = true
		}
	}
	remoteListenTLSFn = func(s *remote.Server) error {
		return listenErr
	}

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected remote listen error, got %v", err)
	}
	if !pairingSet {
		t.Fatal("expected pairing manager to be set")
	}
}

func TestMain_UsesRunFn(t *testing.T) {
	withRunDepsReset(t)
	called := false
	runFn = func() error {
		called = true
		return nil
	}

	main()
	if !called {
		t.Fatal("expected main to call runFn")
	}
}

func TestMain_RunFnError_UsesLogFatalFn(t *testing.T) {
	withRunDepsReset(t)
	called := false
	runFn = func() error { return errors.New("boom") }
	logFatalFn = func(v ...any) { called = true }

	main()
	if !called {
		t.Fatal("expected main to call logFatalFn on run error")
	}
}

// fakeDiscordServiceStartErr is a fakeDiscordService that returns an error on Start.
type fakeDiscordServiceStartErr struct {
	err error
}

func (f *fakeDiscordServiceStartErr) Start() error                    { return f.err }
func (f *fakeDiscordServiceStartErr) Close() error                    { return nil }
func (f *fakeDiscordServiceStartErr) CurrentConfig() discord.Config   { return discord.Config{} }
func (f *fakeDiscordServiceStartErr) Reload(cfg discord.Config) error { return nil }
func (f *fakeDiscordServiceStartErr) SearchHistory(pp, q, since string, limit int) ([]db.DiscordSearchHit, error) {
	return nil, nil
}
func (f *fakeDiscordServiceStartErr) RecentHistory(pp, tid, since string, limit int) ([]db.DiscordMessage, error) {
	return nil, nil
}
func (f *fakeDiscordServiceStartErr) SendDMToOwner(_ string) error      { return nil }
func (f *fakeDiscordServiceStartErr) NotifyProjectProgress(_, _ string) {}

// setupDiscordEnabledTestWithService initializes a Discord-enabled test with database, hub, and custom service factory.
func setupDiscordEnabledTestWithService(t *testing.T, newServiceFn func(discord.Config, string) (discordRuntime, error)) string {
	t.Helper()
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")
	dbInitFn = func(path string) error { return db.Init(path) }
	newHubFn = func(path string) *ws.Hub { return ws.NewHub(path) }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: true, BotToken: "tok", GuildID: "g1"}, nil
	}
	newDiscordServiceFn = newServiceFn
	return projectPath
}

// assertDiscordBridgeState verifies that the discord bridge was set/not set as expected.
func assertDiscordBridgeState(t *testing.T, bridgeSet bool, expectedSet bool, scenario string) {
	t.Helper()
	if bridgeSet != expectedSet {
		if expectedSet {
			t.Fatalf("%s: expected discord bridge to be set, but it was not", scenario)
		} else {
			t.Fatalf("%s: expected discord bridge to remain unset, but it was set", scenario)
		}
	}
}

// triggerAndAssertSessionsCreated triggers a session function and verifies sessions were created.
func triggerAndAssertSessionsCreatedHelper(t *testing.T, projectPath string, triggerFn func(string, json.RawMessage), payload json.RawMessage) {
	t.Helper()
	triggerFn(projectPath, payload)
	assertSessionsCreated(t, projectPath)
}

func TestRun_DiscordEnabled_StartError_NonFatal(t *testing.T) {
	projectPath := setupDiscordEnabledTestWithService(t, func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordServiceStartErr{err: errors.New("discord open: fake gateway error")}, nil
	})
	if projectPath == "" {
		t.Fatal("expected discord-enabled test setup to return a project path")
	}

	bridgeSet := false
	listenErr := errors.New("test stop")
	setDiscordBridgeFn = func(s ws.DiscordBridge) { bridgeSet = true }
	setupRunModeWithHTTPMocks(t,
		func(pattern string, handler func(http.ResponseWriter, *http.Request)) bool { return false },
		func(pattern string, handler http.Handler) {},
		func(addr string, handler http.Handler) error { return listenErr })

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error after non-fatal discord start failure, got %v", err)
	}
	assertDiscordBridgeState(t, bridgeSet, false, "discord start error")
}

func TestRun_DiscordEnabled_ServiceInitError_NonFatal(t *testing.T) {
	projectPath := setupDiscordEnabledTestWithService(t, func(cfg discord.Config, path string) (discordRuntime, error) {
		return nil, errors.New("init failed")
	})
	if projectPath == "" {
		t.Fatal("expected discord-enabled test setup to return a project path")
	}

	bridgeSet := false
	listenErr := errors.New("test stop")
	setDiscordBridgeFn = func(s ws.DiscordBridge) { bridgeSet = true }
	setupRunModeWithHTTPMocks(t,
		func(pattern string, handler func(http.ResponseWriter, *http.Request)) bool { return false },
		func(pattern string, handler http.Handler) {},
		func(addr string, handler http.Handler) error { return listenErr })

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error after non-fatal discord init failure, got %v", err)
	}
	assertDiscordBridgeState(t, bridgeSet, false, "discord init error")
}

func TestRun_VPNMode_RespectsVPNPortOverride(t *testing.T) {
	withRunDepsReset(t)
	setupRunModeBaseDeps(t)
	t.Setenv("ENGINE_VPN", "1")
	t.Setenv("ENGINE_REMOTE", "")
	t.Setenv("VPN_PORT", "4545")

	var seenPort string
	newVPNTunnelFn = func(cfg vpn.Config) (*vpn.Tunnel, error) {
		seenPort = cfg.Port
		return nil, errors.New("stop")
	}

	_ = run()
	if seenPort != "4545" {
		t.Fatalf("expected VPN_PORT override 4545, got %q", seenPort)
	}
}

func TestRun_RemoteMode_RespectsRemotePortOverride(t *testing.T) {
	withRunDepsReset(t)
	setupRunModeBaseDeps(t)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "1")
	t.Setenv("REMOTE_PORT", "5656")

	var seenPort string
	newRemoteServerFn = func(cfg remote.Config, wsHandler http.HandlerFunc) (*remote.Server, error) {
		seenPort = cfg.Port
		return nil, errors.New("stop")
	}

	_ = run()
	if seenPort != "5656" {
		t.Fatalf("expected REMOTE_PORT override 5656, got %q", seenPort)
	}
}

func TestRun_DiscordEnabled_Success(t *testing.T) {
	projectPath := setupDiscordEnabledTestWithService(t, func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	})
	if projectPath == "" {
		t.Fatal("expected discord-enabled test setup to return a project path")
	}

	bridgeSet := false
	setDiscordBridgeFn = func(s ws.DiscordBridge) { bridgeSet = true }
	setupRunModeWithHTTPMocks(t,
		func(pattern string, handler func(http.ResponseWriter, *http.Request)) bool { return false },
		func(pattern string, handler http.Handler) {},
		func(addr string, handler http.Handler) error { return errors.New("test stop") })

	_ = run()

	assertDiscordBridgeState(t, bridgeSet, true, "discord enabled success")
}

// withAIMockServer configures a mock OpenAI API server for testing AI chat functionality.
func withAIMockServer(t *testing.T) {
	t.Helper()
	srv := makeTriggerSSEServer(t)
	t.Cleanup(srv.Close)
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	http.DefaultTransport = &mainRedirectTransport{target: srv.URL, transport: oldTransport}
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "test-key")
}

// withPathDepsReset resets all path-related dependencies to their original values for isolated test execution.
func withPathDepsReset(t *testing.T) {
	t.Helper()
	origGetwd := osGetwdFn
	origHome := osUserHomeDirFn
	t.Cleanup(func() {
		osGetwdFn = origGetwd
		osUserHomeDirFn = origHome
	})
}

func TestDefaultProjectPath_HomeFallback(t *testing.T) {
	withPathDepsReset(t)
	osGetwdFn = func() (string, error) { return "", errors.New("cwd fail") }
	osUserHomeDirFn = func() (string, error) { return "/tmp/home", nil }
	if got := defaultProjectPath(); got != "/tmp/home" {
		t.Fatalf("defaultProjectPath home fallback = %q", got)
	}
}

func TestDefaultProjectPath_DotFallback(t *testing.T) {
	withPathDepsReset(t)
	osGetwdFn = func() (string, error) { return "", errors.New("cwd fail") }
	osUserHomeDirFn = func() (string, error) { return "", errors.New("home fail") }
	if got := defaultProjectPath(); got != "." {
		t.Fatalf("defaultProjectPath dot fallback = %q", got)
	}
}

func TestDefaultNewDiscordServiceFn_Call(t *testing.T) {
	withRunDepsReset(t)
	svc, err := newDiscordServiceFn(discord.Config{Enabled: false}, t.TempDir())
	if err != nil {
		t.Fatalf("newDiscordServiceFn: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil discord runtime")
	}
}

func TestTriggerScaffoldSession_OnChunkCalled(t *testing.T) {
	_, targetPath := setupTriggerTestWithDB(t, "owner", "repo", "# Demo\n@engine")
	triggerAndAssertSessionsCreatedHelper(t, targetPath, triggerScaffoldSession, makeScaffoldPayload())
}

func TestTriggerCIAnalysisSession_OnChunkCalled(t *testing.T) {
	projectPath := setupTriggerWithAIMock(t)
	triggerAndAssertSessionsCreatedHelper(t, projectPath, triggerCIAnalysisSession, makeCIPayload())
}

func TestTriggerIssueSession_OnChunkCalled(t *testing.T) {
	_, targetPath := setupTriggerTestWithDB(t, "owner", "repo", "# Demo\n@engine")
	triggerAndAssertSessionsCreatedHelper(t, targetPath, triggerIssueSession, makeIssueCommentPayload())
}

func TestTriggerIssueOpenedSession_OnChunkCalled(t *testing.T) {
	_, targetPath := setupTriggerTestWithDB(t, "owner", "repo", "# Demo\n@engine")
	triggerAndAssertSessionsCreatedHelper(t, targetPath, triggerIssueOpenedSession, makeIssueOpenedPayload())
}

func TestTriggerSessions_DBCreateAndSaveErrorsCovered(t *testing.T) {
	projectPath := t.TempDir()
	withRunDepsReset(t)
	withAIMockServer(t)
	createSessionFn = func(id, projectPath, branchName string) error { return errors.New("create fail") }
	saveMessageFn = func(id, sessionId, role, content string, toolCalls any) error {
		return errors.New("save fail")
	}
	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	callAllTriggerFunctions(projectPath, makeScaffoldPayload(), makeCIPayload(), makeIssueCommentPayload(), makeIssueOpenedPayload())
}

func TestTriggerSessions_SaveMessageErrorBranchesCovered(t *testing.T) {
	projectPath := t.TempDir()
	withRunDepsReset(t)
	createSessionFn = func(id, projectPath, branchName string) error { return nil }
	saveMessageFn = func(id, sessionId, role, content string, toolCalls any) error {
		return errors.New("save fail")
	}
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		ctx.OnChunk("assistant reply", false)
		ctx.OnChunk("", true)
	}
	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	callAllTriggerFunctions(projectPath, makeScaffoldPayload(), makeCIPayload(), makeIssueCommentPayload(), makeIssueOpenedPayload())
}

func TestTriggerScaffoldSession_NoOpFirstPass_RetriesThenReportsNoop(t *testing.T) {
	t.Skip("obsolete: the inline 2-attempt scaffold loop was replaced by ai.RunAutonomousProject; retry semantics now live in the orchestrator and are covered by ai/orchestrator_test.go")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	targetPath := buildAutonomousRepoPath(projectPath, "owner", "repo")
	if out, err := exec.Command("git", "-C", targetPath, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", targetPath, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", targetPath, "config", "user.name", "Test").Run()
	if out, err := exec.Command("git", "-C", targetPath, "add", "README.md").CombinedOutput(); err != nil {
		t.Skipf("git add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", targetPath, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit failed: %v: %s", err, out)
	}
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		if ctx.ProjectPath != targetPath {
			return
		}
		callCount++
		ctx.OnChunk("planned but unchanged", false)
		ctx.OnChunk("", true)
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))

	if callCount != 2 {
		t.Fatalf("expected scaffold retry after first no-op, got %d attempts", callCount)
	}

	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "First scaffold pass") {
		t.Fatalf("expected first-pass no-op warning in notifications, got: %v", notifier.notified)
	}
	if !strings.Contains(joined, "will retry") || !strings.Contains(joined, "no repository changes after two attempts") {
		t.Fatalf("expected scheduled no-op retry notification, got: %v", notifier.notified)
	}
}

func TestTriggerScaffoldSession_ErrorFirstPass_RetriesAndSucceeds(t *testing.T) {
	t.Skip("obsolete: the inline 2-attempt scaffold loop was replaced by ai.RunAutonomousProject; retry semantics now live in the orchestrator")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	targetPath := buildAutonomousRepoPath(projectPath, "owner", "repo")
	if out, err := exec.Command("git", "-C", targetPath, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", targetPath, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", targetPath, "config", "user.name", "Test").Run()
	if out, err := exec.Command("git", "-C", targetPath, "add", "README.md").CombinedOutput(); err != nil {
		t.Skipf("git add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", targetPath, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit failed: %v: %s", err, out)
	}
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		if ctx.ProjectPath != targetPath {
			return
		}
		callCount++
		if callCount == 1 {
			ctx.OnError("temporary tool failure")
			return
		}
		if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "scaffold_progress.txt"), []byte("done"), 0o644); err != nil {
			t.Fatalf("write scaffold progress file: %v", err)
		}
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "scaffold_progress.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: initial implementation").CombinedOutput()
		ctx.OnChunk("recovered and completed", false)
		ctx.OnChunk("", true)
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))

	if callCount != 2 {
		t.Fatalf("expected retry after first failed attempt, got %d attempts", callCount)
	}

	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "did not complete") || !strings.Contains(joined, "retrying automatically") {
		t.Fatalf("expected automatic retry warning, got: %v", notifier.notified)
	}
	if !strings.Contains(joined, "finished") {
		t.Fatalf("expected successful completion notification after retry, got: %v", notifier.notified)
	}
}

// TestTriggerScaffoldSession_OnlyUntrackedFirstPass_RetriesAsNoop verifies that
// creating only untracked metadata files (e.g. PROJECT_GOAL.md) without
// committing does NOT count as a successful scaffold finish.  The session must
// trigger a second attempt with the no-op retry prompt.
func TestTriggerScaffoldSession_OnlyUntrackedFirstPass_RetriesAsNoop(t *testing.T) {
	t.Skip("obsolete: the inline 2-attempt scaffold loop was replaced by ai.RunAutonomousProject; commit-progress detection no longer applies")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	targetPath := buildAutonomousRepoPath(projectPath, "owner", "repo")
	if out, err := exec.Command("git", "-C", targetPath, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", targetPath, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", targetPath, "config", "user.name", "Test").Run()
	if out, err := exec.Command("git", "-C", targetPath, "add", "README.md").CombinedOutput(); err != nil {
		t.Skipf("git add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", targetPath, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit failed: %v: %s", err, out)
	}
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	var attempt2Prompt string
	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			// Simulate AI writing only a planning doc (untracked, not committed).
			if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "PROJECT_GOAL.md"), []byte("plan"), 0o644); err != nil {
				t.Fatalf("write project goal: %v", err)
			}
			ctx.OnChunk("wrote project goal", false)
			ctx.OnChunk("", true)
			return
		}
		// Second attempt: record the prompt then commit actual code.
		attempt2Prompt = prompt
		if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "main.go"), []byte("package main"), 0o644); err != nil {
			t.Fatalf("write main.go: %v", err)
		}
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "main.go").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: add main").CombinedOutput()
		ctx.OnChunk("implemented", false)
		ctx.OnChunk("", true)
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))

	if callCount != 2 {
		t.Fatalf("expected retry when attempt 1 only created untracked files, got %d attempts", callCount)
	}
	if !strings.Contains(attempt2Prompt, "no committed repository changes") {
		t.Fatalf("expected noop retry prompt for untracked-only attempt, got: %s", attempt2Prompt)
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "finished") {
		t.Fatalf("expected finished notification after attempt 2 committed, got: %v", notifier.notified)
	}
}

func TestTriggerScaffoldSession_TimeoutThenError_ReportsBlockedAfterRetry(t *testing.T) {
	t.Skip("obsolete: the per-attempt timeout was a property of the inline 2-attempt loop, replaced by ai.RunAutonomousProject's completion-criteria loop")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	origTimeout := scaffoldAttemptTimeout
	scaffoldAttemptTimeout = 20 * time.Millisecond
	t.Cleanup(func() { scaffoldAttemptTimeout = origTimeout })

	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		if ctx.ProjectPath != targetPath {
			return
		}
		callCount++
		if callCount == 1 {
			if ctx.Cancel != nil {
				<-ctx.Cancel
			}
			return
		}
		if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "partial_progress.txt"), []byte("done"), 0o644); err != nil {
			t.Fatalf("write partial progress file: %v", err)
		}
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "partial_progress.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "partial progress").CombinedOutput()
		ctx.OnError("agent exited early")
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))

	if callCount != 2 {
		t.Fatalf("expected two attempts after timeout, got %d", callCount)
	}

	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "did not complete") || !strings.Contains(joined, "retrying automatically") {
		t.Fatalf("expected timeout retry warning, got: %v", notifier.notified)
	}
	if !strings.Contains(joined, "Scaffold blocked") {
		t.Fatalf("expected terminal blocked notification after retry, got: %v", notifier.notified)
	}
}

func TestRun_RepoMonitorCallbacks_InvokeTriggerClosures(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	var monitor *gh.RepoMonitor
	dbInitFn = func(path string) error { return nil }
	loadDiscordConfigFn = func(path string) (discord.Config, error) { return discord.Config{Enabled: false}, nil }
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) { return &fakeDiscordService{}, nil }
	newRepoMonitorFn = func() *gh.RepoMonitor {
		monitor = gh.NewRepoMonitor()
		return monitor
	}
	repoMonitorStartFn = func(rm *gh.RepoMonitor) {}
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(pattern string, handler http.Handler) {}
	httpListenAndServeFn = func(addr string, handler http.Handler) error { return errors.New("stop") }
	runAsyncFn = func(fn func()) { fn() }
	triggerScaffoldSessionFn = func(string, json.RawMessage) {}
	triggerCIAnalysisSessionFn = func(string, json.RawMessage) {}
	triggerIssueSessionFn = func(string, json.RawMessage) {}
	triggerIssueOpenedSessionFn = func(string, json.RawMessage) {}

	_ = run()
	if monitor == nil {
		t.Fatal("expected repo monitor to be constructed")
	}
	monitor.OnReadmeChange(json.RawMessage(`{"repository":{"full_name":"o/r"}}`))
	monitor.OnCIFailure(json.RawMessage(`{"workflow_run":{"name":"ci","html_url":"u","conclusion":"failure"},"repository":{"full_name":"o/r"}}`))
	monitor.OnIssueComment(json.RawMessage(`{"action":"created","comment":{"body":"x","user":{"login":"u"}},"issue":{"number":1,"title":"t"},"repository":{"full_name":"o/r"}}`))
	monitor.OnIssueOpened(json.RawMessage(`{"action":"opened","issue":{"number":2,"title":"t","body":"b"},"repository":{"full_name":"o/r"},"sender":{"login":"u"}}`))
}

func TestRunAsyncFn_DefaultImplementationRuns(t *testing.T) {
	done := make(chan struct{}, 1)
	runAsyncFn(func() {
		done <- struct{}{}
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected default runAsyncFn implementation to execute callback")
	}
}

func TestRun_EventsWatcher_NonNil_Started(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	dbInitFn = func(_ string) error { return nil }
	loadDiscordConfigFn = func(_ string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(_ discord.Config, _ string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
	httpHandleFuncFn = func(_ string, _ func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(_ string, _ http.Handler) {}
	httpListenAndServeFn = func(_ string, _ http.Handler) error { return errors.New("stop") }

	var ewStarted bool
	fakeWatcher := gh.NewEventsWatcher("fake-token", gh.NewRepoMonitor())
	newEventsWatcherFn = func(_ *gh.RepoMonitor) *gh.EventsWatcher { return fakeWatcher }
	eventsWatcherStartFn = func(_ *gh.EventsWatcher, _ context.Context) { ewStarted = true }

	_ = run()
	if !ewStarted {
		t.Error("expected eventsWatcherStartFn to be called with non-nil watcher")
	}
}

// TestTriggerScaffoldSession_WritesToProjectLocalDB verifies the trigger
// writes its session to the project's own .engine/state.db rather than a
// workspace-wide DB. With no ENGINE_STATE_DIR override, stateDir resolves
// per-project: <projectPath>/.engine/state.db.
func TestTriggerScaffoldSession_WritesToProjectLocalDB(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "")

	workspace := t.TempDir()
	// Autonomous clones now default to ~/.engine/projects. Redirect HOME to
	// the workspace tempdir so the test's path expectations match the new
	// home-based default without polluting the developer's real ~.
	t.Setenv("HOME", workspace)
	if err := db.Init(workspace); err != nil {
		t.Fatalf("workspace db.Init: %v", err)
	}

	projectPath := filepath.Join(workspace, ".engine", "projects", "owner-repo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	// Pre-create .git so ensureAutonomousRepoWorkspace takes the existing-repo
	// path and fetch/pull are stubbed below.
	if err := os.MkdirAll(filepath.Join(projectPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	// Stub git fetch/pull and aiChatFn so the trigger short-circuits.
	withRunCommandMocked(t, func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "show" && strings.HasSuffix(args[3], ":README.md") {
			return []byte("# Demo\n@engine"), nil
		}
		return []byte(""), nil
	})
	withAIChatNoOp(t)

	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
	triggerScaffoldSession(workspace, payload)

	projectDB := filepath.Join(projectPath, ".engine", "state.db")
	if _, err := os.Stat(projectDB); err != nil {
		t.Fatalf("expected project-local state.db at %q: %v", projectDB, err)
	}

	// Workspace DB should be active again after WithProject restored it.
	if got := db.CurrentProject(); got != workspace {
		t.Errorf("CurrentProject after trigger = %q, want %q", got, workspace)
	}
}

// ── scaffold + Discord auto-enroll ───────────────────────────────────────────

type mockAutoEnroller struct {
	enrolledPath  string
	enrolledOwner string
	enrolledRepo  string
}

func (m *mockAutoEnroller) SendDMToOwner(_ string) error      { return nil }
func (m *mockAutoEnroller) NotifyProjectProgress(_, _ string) {}
func (m *mockAutoEnroller) CurrentConfig() discord.Config     { return discord.Config{} }
func (m *mockAutoEnroller) Reload(_ discord.Config) error     { return nil }
func (m *mockAutoEnroller) SearchHistory(_, _, _ string, _ int) ([]db.DiscordSearchHit, error) {
	return nil, nil
}
func (m *mockAutoEnroller) RecentHistory(_, _, _ string, _ int) ([]db.DiscordMessage, error) {
	return nil, nil
}
func (m *mockAutoEnroller) AutoEnrollProject(projectPath, owner, repo string) error {
	m.enrolledPath = projectPath
	m.enrolledOwner = owner
	m.enrolledRepo = repo
	return nil
}

func TestTriggerScaffoldSession_CallsAutoEnrollProject(t *testing.T) {
	projectPath, targetProjectPath := setupTriggerTestWithDB(t, "owner", "myrepo", "# Demo\n@engine")
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	enroller := &mockAutoEnroller{}
	ws.SetDiscordBridge(enroller)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	payload := json.RawMessage(`{"repository":{"full_name":"owner/myrepo"}}`)
	triggerScaffoldSession(projectPath, payload)

	if enroller.enrolledOwner != "owner" || enroller.enrolledRepo != "myrepo" {
		t.Fatalf("expected enroll owner/myrepo, got %q/%q", enroller.enrolledOwner, enroller.enrolledRepo)
	}
	if enroller.enrolledPath != targetProjectPath {
		t.Fatalf("expected enroll path %q, got %q", targetProjectPath, enroller.enrolledPath)
	}
}

func TestTriggerScaffoldSession_DBInitFails_LogsError(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", "/dev/null/cannot-create")
	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
	// No panic expected; dbErr != nil path logs the error.
	triggerScaffoldSession(projectPath, payload)
}

func TestTriggerScaffoldSession_CloneSyncFailure_SkipsBuild(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "clone" {
			return []byte("repository not found"), errors.New("clone failed")
		}
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	before := countSessions(t, projectPath)
	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	after := countSessions(t, projectPath)

	if after != before {
		t.Fatalf("expected no scaffold session when clone/sync fails, before=%d after=%d", before, after)
	}
}

func TestTriggerCIAnalysisSession_DBInitFails_LogsError(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", "/dev/null/cannot-create")
	payload := json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`)
	triggerCIAnalysisSession(projectPath, payload)
}

func TestTriggerIssueSession_DBInitFails_LogsError(t *testing.T) {
	projectPath := t.TempDir()
	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	t.Setenv("ENGINE_STATE_DIR", "/dev/null/cannot-create")
	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine fix it","user":{"login":"bob"}},"issue":{"number":1,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
}

// mockProgressNotifier implements AutoEnrollProject AND NotifyProjectProgress so
// we can exercise both interface-assertion branches in autoEnrollDiscordProject
// and notifyDiscordProjectProgress.
type mockProgressNotifier struct {
	mockAutoEnroller
	notified  []string
	enrollErr error
}

func (m *mockProgressNotifier) NotifyProjectProgress(_ string, message string) {
	m.notified = append(m.notified, message)
}
func (m *mockProgressNotifier) AutoEnrollProject(projectPath, owner, repo string) error {
	m.enrolledPath = projectPath
	m.enrolledOwner = owner
	m.enrolledRepo = repo
	return m.enrollErr
}
func (m *mockProgressNotifier) Start() error { return nil }
func (m *mockProgressNotifier) Close() error { return nil }

func TestAutoEnrollDiscordProject_EnrollError_Logs(t *testing.T) {
	mock := &mockProgressNotifier{enrollErr: errors.New("enroll boom")}
	ws.SetDiscordBridge(mock)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })
	autoEnrollDiscordProject(t.TempDir(), "owner", "repo")
}

func TestNotifyDiscordProjectProgress_CallsNotifier(t *testing.T) {
	mock := &mockProgressNotifier{}
	ws.SetDiscordBridge(mock)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })
	notifyDiscordProjectProgress(t.TempDir(), "hello from test")
	if len(mock.notified) == 0 || mock.notified[0] != "hello from test" {
		t.Errorf("expected notification, got %v", mock.notified)
	}
}

func TestTriggerIssueSession_WorkspaceError_SkipsSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("clone fail")
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	before := countSessions(t, projectPath)
	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"}},"issue":{"number":99,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	after := countSessions(t, projectPath)
	if after != before {
		t.Fatalf("expected no session when workspace error, before=%d after=%d", before, after)
	}
}

func TestTriggerIssueOpenedSession_WorkspaceError_SkipsSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("clone fail")
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	before := countSessions(t, projectPath)
	payload := json.RawMessage(`{"action":"opened","issue":{"number":98,"title":"Feature","body":"desc"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
	after := countSessions(t, projectPath)
	if after != before {
		t.Fatalf("expected no session when workspace error, before=%d after=%d", before, after)
	}
}

func TestRun_DiscordDisabled_StubInitError_IsIgnored(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	dbInitFn = func(path string) error { return nil }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return nil, errors.New("stub init error")
	}
	httpHandleFuncFn = func(_ string, _ func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(_ string, _ http.Handler) {}
	listenErr := errors.New("stop")
	httpListenAndServeFn = func(addr string, handler http.Handler) error { return listenErr }

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error after ignored stub init error, got %v", err)
	}
}

// ── hasRepoProgress ───────────────────────────────────────────────────────────

func TestHasRepoProgress_StagedDiff(t *testing.T) {
	before := repoActivitySnapshot{head: "abc", staged: 0}
	after := repoActivitySnapshot{head: "abc", staged: 1}
	if !hasRepoProgress(before, after) {
		t.Error("expected true when staged count changes")
	}
}

func TestHasRepoProgress_UnstagedDiff(t *testing.T) {
	before := repoActivitySnapshot{head: "abc", unstaged: 0}
	after := repoActivitySnapshot{head: "abc", unstaged: 2}
	if !hasRepoProgress(before, after) {
		t.Error("expected true when unstaged count changes")
	}
}

func TestHasRepoProgress_UntrackedDiff(t *testing.T) {
	before := repoActivitySnapshot{head: "abc", untracked: 0}
	after := repoActivitySnapshot{head: "abc", untracked: 1}
	if !hasRepoProgress(before, after) {
		t.Error("expected true when untracked count changes")
	}
}

func TestHasRepoProgress_NoChange(t *testing.T) {
	s := repoActivitySnapshot{head: "abc", staged: 1, unstaged: 1, untracked: 1}
	if hasRepoProgress(s, s) {
		t.Error("expected false when snapshots are identical")
	}
}

// ── captureRepoActivity ───────────────────────────────────────────────────────

func TestCaptureRepoActivity_RealGitRepo(t *testing.T) {
	dir := t.TempDir()
	// Initialize a real git repo so GetLog can return commits.
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	testFile := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(testFile, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Skipf("git add: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit: %v: %s", err, out)
	}

	snap := captureRepoActivity(dir)
	if snap.head == "" {
		t.Error("expected non-empty head after commit")
	}
}

// ── runCommandCombinedOutputFn default body ───────────────────────────────────

func TestRunCommandCombinedOutputFn_Default(t *testing.T) {
	// The default runCommandCombinedOutputFn body wraps exec.Command.CombinedOutput.
	// Call it directly with a safe command to exercise the default body.
	out, err := runCommandCombinedOutputFn("echo", "hello")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("expected 'hello' in output, got: %s", out)
	}
}

// ── GitHubAuthSuccessHook paths in run() ─────────────────────────────────────

func TestRun_GitHubAuthSuccessHook_EmptyToken(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	dbInitFn = func(path string) error { return nil }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
	httpHandleFuncFn = func(_ string, _ func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(_ string, _ http.Handler) {}
	newEventsWatcherFn = func(_ *gh.RepoMonitor) *gh.EventsWatcher {
		return nil
	}

	listenErr := errors.New("stop")
	httpListenAndServeFn = func(addr string, handler http.Handler) error {
		// Trigger the hook with empty token — exercises the "return" branch.
		ws.TriggerGitHubAuthSuccessHook("", "")
		return listenErr
	}

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen err, got %v", err)
	}
}

func TestRun_GitHubAuthSuccessHook_NonEmptyToken(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	dbInitFn = func(path string) error { return nil }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
	httpHandleFuncFn = func(_ string, _ func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(_ string, _ http.Handler) {}
	newEventsWatcherFn = func(_ *gh.RepoMonitor) *gh.EventsWatcher {
		return nil
	}

	listenErr := errors.New("stop")
	httpListenAndServeFn = func(addr string, handler http.Handler) error {
		// Trigger the hook with non-empty token — exercises SetSecret + startEventsWatcher.
		ws.TriggerGitHubAuthSuccessHook("tok123", "secret456")
		return listenErr
	}

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen err, got %v", err)
	}
}

// ── OnChunk / OnError callback coverage ──────────────────────────────────────

func TestTriggerScaffoldSession_OnChunkContent(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo2", "# Demo\n@engine")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnChunk("scaffold output", false)
		ctx.OnChunk("", true)
	}

	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo2"}}`)
	triggerScaffoldSession(projectPath, payload)
	_ = targetPath
}

func TestTriggerScaffoldSession_OnError(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo3", "# Demo\n@engine")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnError("scaffold error msg")
	}

	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo3"}}`)
	triggerScaffoldSession(projectPath, payload)
}

func TestTriggerIssueSession_OnChunkDone(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "issuerepo", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnChunk("issue content", false)
		ctx.OnChunk("", true)
	}
	setupTestDB(t, targetPath)

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine fix it","user":{"login":"bob"}},"issue":{"number":77,"title":"Bug"},"repository":{"full_name":"owner/issuerepo"}}`)
	triggerIssueSession(projectPath, payload)
}

func TestTriggerIssueSession_OnError(t *testing.T) {
	withRunDepsReset(t)
	projectPath, _ := setupTriggerTestWithDB(t, "owner", "issuerepo2", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnError("issue error")
	}

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine fix","user":{"login":"bob"}},"issue":{"number":78,"title":"Other"},"repository":{"full_name":"owner/issuerepo2"}}`)
	triggerIssueSession(projectPath, payload)
}

func TestTriggerIssueOpenedSession_OnChunkDone(t *testing.T) {
	withRunDepsReset(t)
	projectPath, _ := setupTriggerTestWithDB(t, "owner", "openedrepo", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnChunk("opened content", false)
		ctx.OnChunk("", true)
	}

	payload := json.RawMessage(`{"action":"opened","issue":{"number":88,"title":"Feature","body":"desc"},"repository":{"full_name":"owner/openedrepo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
}

func TestTriggerIssueOpenedSession_OnError(t *testing.T) {
	withRunDepsReset(t)
	projectPath, _ := setupTriggerTestWithDB(t, "owner", "openederrrepo", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnError("opened error")
	}

	payload := json.RawMessage(`{"action":"opened","issue":{"number":89,"title":"Bug2","body":"desc2"},"repository":{"full_name":"owner/openederrrepo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
}

func TestTriggerIssueSession_RetriesRetryableErrorBeforeBlocking(t *testing.T) {
	withRunDepsReset(t)
	projectPath, _ := setupTriggerTestWithDB(t, "owner", "retryable-issue", "# Demo")

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	var prompts []string
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		prompts = append(prompts, prompt)
		if callCount == 1 {
			ctx.OnError("autonomous builder stopped after 20 turns without signal_done")
			return
		}
		ctx.OnChunk("done", false)
	}

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine fix it","user":{"login":"bob"}},"issue":{"number":9,"title":"Bug"},"repository":{"full_name":"owner/retryable-issue"}}`)
	triggerIssueSession(projectPath, payload)

	if callCount != 2 {
		t.Fatalf("expected 2 AI calls, got %d", callCount)
	}
	if len(prompts) < 2 || !strings.Contains(prompts[1], "Recovery attempt 2 of 3") {
		t.Fatalf("expected recovery prompt on second attempt, got %v", prompts)
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "Retrying automatically (2/3)") {
		t.Fatalf("expected retry notification, got: %v", notifier.notified)
	}
	if strings.Contains(joined, "Issue session blocked") {
		t.Fatalf("expected retry to avoid blocked notification, got: %v", notifier.notified)
	}
	if !strings.Contains(joined, "✅ Issue session issue-9-") {
		t.Fatalf("expected final success notification, got: %v", notifier.notified)
	}
}

func TestTriggerIssueOpenedSession_RetryableErrorStopsAfterMaxAttempts(t *testing.T) {
	withRunDepsReset(t)
	projectPath, _ := setupTriggerTestWithDB(t, "owner", "retryable-opened", "# Demo")

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		ctx.OnError("tool error: command not found")
	}

	payload := json.RawMessage(`{"action":"opened","issue":{"number":11,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/retryable-opened"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)

	if callCount != autonomousIssueMaxAttempts {
		t.Fatalf("expected %d AI calls, got %d", autonomousIssueMaxAttempts, callCount)
	}
	joined := strings.Join(notifier.notified, "\n")
	if strings.Count(joined, "Retrying automatically") != autonomousIssueMaxAttempts-1 {
		t.Fatalf("expected %d retry notifications, got: %v", autonomousIssueMaxAttempts-1, notifier.notified)
	}
	if !strings.Contains(joined, "Issue-opened session blocked") {
		t.Fatalf("expected blocked notification after retries exhausted, got: %v", notifier.notified)
	}
	if strings.Contains(joined, "finished") {
		t.Fatalf("expected no success notification on exhausted retries, got: %v", notifier.notified)
	}
}

func TestIsRetryableAutonomousIssueError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "empty", msg: "   ", want: false},
		{name: "turn limit", msg: "autonomous builder stopped after 20 turns without signal_done", want: true},
		{name: "tool error", msg: "command not found: go", want: true},
		{name: "human required", msg: "need an api key before continuing", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableAutonomousIssueError(tt.msg); got != tt.want {
				t.Fatalf("isRetryableAutonomousIssueError(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestRunAutonomousIssueAttempts_RetriesThenSucceeds(t *testing.T) {
	withRunDepsReset(t)
	ctx := &ai.ChatContext{}
	orig := aiChatFn
	defer func() { aiChatFn = orig }()

	var prompts []string
	var retries []string
	callCount := 0
	aiChatFn = func(ctx *ai.ChatContext, userMessage string) {
		callCount++
		prompts = append(prompts, userMessage)
		if callCount == 1 {
			ctx.OnError("command not found: go")
		}
	}

	err := runAutonomousIssueAttempts(ctx, "Ship the fix", func(nextAttempt int, msg string) {
		retries = append(retries, fmt.Sprintf("%d:%s", nextAttempt, msg))
	})
	if err != "" {
		t.Fatalf("expected retry flow to recover, got %q", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 attempts, got %d", callCount)
	}
	if len(retries) != 1 || retries[0] != "2:command not found: go" {
		t.Fatalf("unexpected retry callbacks: %+v", retries)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected two prompts, got %d", len(prompts))
	}
	if prompts[1] == prompts[0] {
		t.Fatal("expected retry prompt to differ from initial prompt")
	}
	if !strings.Contains(prompts[1], "Recovery attempt 2 of 3.") {
		t.Fatalf("retry prompt missing attempt metadata: %q", prompts[1])
	}
	if !strings.Contains(prompts[1], "Previous blocker: command not found: go") {
		t.Fatalf("retry prompt missing blocker context: %q", prompts[1])
	}
	if !strings.Contains(prompts[1], "Original objective:\nShip the fix") {
		t.Fatalf("retry prompt missing original objective: %q", prompts[1])
	}
}

func TestRunAutonomousIssueAttempts_UnknownFailureDoesNotRetry(t *testing.T) {
	withRunDepsReset(t)
	ctx := &ai.ChatContext{}
	orig := aiChatFn
	defer func() { aiChatFn = orig }()

	callCount := 0
	aiChatFn = func(ctx *ai.ChatContext, userMessage string) {
		callCount++
		ctx.OnError("   ")
	}

	err := runAutonomousIssueAttempts(ctx, "Ship the fix", nil)
	if err != "unknown failure" {
		t.Fatalf("expected normalized unknown failure, got %q", err)
	}
	if callCount != 1 {
		t.Fatalf("expected no retry for unknown failure, got %d attempts", callCount)
	}
}

func TestRunAutonomousIssueAttempts_RetryableErrorWithoutCallbackStillRetries(t *testing.T) {
	withRunDepsReset(t)
	ctx := &ai.ChatContext{}
	orig := aiChatFn
	defer func() { aiChatFn = orig }()

	callCount := 0
	aiChatFn = func(ctx *ai.ChatContext, userMessage string) {
		callCount++
		if callCount == 1 {
			ctx.OnError("failed to run the test suite")
		}
	}

	err := runAutonomousIssueAttempts(ctx, "Ship the fix", nil)
	if err != "" {
		t.Fatalf("expected retry flow to recover without callback, got %q", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 attempts without callback, got %d", callCount)
	}
}

// ── hasRepoProgress: head differs branch ─────────────────────────────────────

func TestHasRepoProgress_HeadDiffers(t *testing.T) {
	before := repoActivitySnapshot{head: "abc123", staged: 0, unstaged: 0, untracked: 0}
	after := repoActivitySnapshot{head: "def456", staged: 0, unstaged: 0, untracked: 0}
	if !hasRepoProgress(before, after) {
		t.Fatal("expected progress when head changes")
	}
}

// ── hasCommitProgress ────────────────────────────────────────────────────────

func TestHasCommitProgress_HeadChangedReturnsTrue(t *testing.T) {
	before := repoActivitySnapshot{head: "abc123"}
	after := repoActivitySnapshot{head: "def456"}
	if !hasCommitProgress(before, after) {
		t.Error("expected true when head changes")
	}
}

func TestHasCommitProgress_OnlyUntrackedReturnsFalse(t *testing.T) {
	before := repoActivitySnapshot{head: "abc", untracked: 0}
	after := repoActivitySnapshot{head: "abc", untracked: 3}
	if hasCommitProgress(before, after) {
		t.Error("expected false when only untracked count changes (no new commits)")
	}
}

func TestHasCommitProgress_OnlyStagedReturnsFalse(t *testing.T) {
	before := repoActivitySnapshot{head: "abc", staged: 0}
	after := repoActivitySnapshot{head: "abc", staged: 2}
	if hasCommitProgress(before, after) {
		t.Error("expected false when only staged count changes (no new commits)")
	}
}

func TestHasCommitProgress_NoChangeReturnsFalse(t *testing.T) {
	s := repoActivitySnapshot{head: "abc", staged: 1, unstaged: 1, untracked: 1}
	if hasCommitProgress(s, s) {
		t.Error("expected false when snapshots are identical")
	}
}

// ── beginScaffoldTrigger: empty repoKey branch ────────────────────────────────

func TestBeginScaffoldTrigger_EmptyRepoKey(t *testing.T) {
	if beginScaffoldTrigger("") {
		t.Fatal("expected false for empty repoKey")
	}
	if beginScaffoldTrigger("   ") {
		t.Fatal("expected false for whitespace-only repoKey")
	}
}

// ── hasRecentScaffoldSession: timestamp edge cases ────────────────────────────

func TestHasRecentScaffoldSession_EmptyUpdatedAtUsesCreatedAt(t *testing.T) {
	projectPath := t.TempDir()
	target := prepareScaffoldTargetRepo(t, projectPath, "owner", "tsrepo1", "# Demo\n@engine")
	setupTestDB(t, target)

	// Insert session with empty updated_at but valid recent created_at.
	recentTS := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	if err := db.WithProject(target, func() error {
		return db.InsertSessionWithTimestamps("scaffold-tsrepo1-empty-upd", target, "main", recentTS, "")
	}); err != nil {
		t.Fatalf("WithProject insert: %v", err)
	}

	if !hasRecentScaffoldSession(target, "tsrepo1", 2*time.Minute) {
		t.Fatal("expected recent session detected when UpdatedAt empty but CreatedAt is recent")
	}
}

func TestHasRecentScaffoldSession_BothTimestampsEmptySkipped(t *testing.T) {
	projectPath := t.TempDir()
	target := prepareScaffoldTargetRepo(t, projectPath, "owner", "tsrepo2", "# Demo\n@engine")
	setupTestDB(t, target)

	// Insert only a session with both timestamps empty — the loop hits `continue` and returns false.
	if err := db.WithProject(target, func() error {
		return db.InsertSessionWithTimestamps("scaffold-tsrepo2-both-empty", target, "main", "", "")
	}); err != nil {
		t.Fatalf("WithProject insert: %v", err)
	}

	// Both timestamps empty → session skipped → no recent session.
	if hasRecentScaffoldSession(target, "tsrepo2", 2*time.Minute) {
		t.Fatal("expected no recent session when only empty-timestamp sessions exist")
	}
}

func TestHasRecentScaffoldSession_UnparsableTimestampSkipped(t *testing.T) {
	projectPath := t.TempDir()
	target := prepareScaffoldTargetRepo(t, projectPath, "owner", "tsrepo3", "# Demo\n@engine")
	setupTestDB(t, target)

	// Insert a session with an unparsable timestamp (should be skipped).
	// Insert a second session with a valid timestamp so the loop finds something.
	recentTS := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	if err := db.WithProject(target, func() error {
		if err := db.InsertSessionWithTimestamps("scaffold-tsrepo3-bad-ts", target, "main", "not-a-date", "not-a-date"); err != nil {
			return err
		}
		return db.InsertSessionWithTimestamps("scaffold-tsrepo3-valid", target, "main", recentTS, recentTS)
	}); err != nil {
		t.Fatalf("WithProject insert: %v", err)
	}

	if !hasRecentScaffoldSession(target, "tsrepo3", 2*time.Minute) {
		t.Fatal("expected recent session detected despite one session having unparsable timestamp")
	}
}

// ── triggerScaffoldSession: beginScaffoldTrigger dedup branch ─────────────────

func TestTriggerScaffoldSession_DedupedByBeginScaffoldTrigger(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	prepareScaffoldTargetRepo(t, projectPath, "owner", "deduptest", "# Demo\n@engine")

	// Mark trigger as already running so beginScaffoldTrigger returns false.
	scaffoldTriggerMu.Lock()
	scaffoldTriggerRunning["owner/deduptest"] = true
	scaffoldTriggerMu.Unlock()

	called := false
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) { called = true })

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/deduptest"}}`))

	if called {
		t.Fatal("expected AI to NOT be called when trigger is deduped")
	}
}

// ── triggerScaffoldSession: second attempt makes progress branch ──────────────

func TestTriggerScaffoldSession_SecondAttemptMakesProgress(t *testing.T) {
	t.Skip("obsolete: 2-attempt semantics replaced by orchestrator outer-iteration loop")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	// Build a real git repo so captureRepoActivity can detect file changes.
	targetPath := buildAutonomousRepoPath(projectPath, "owner", "progressrepo")

	// Reset dedup state for this key.
	repoKey := "owner/progressrepo"
	scaffoldTriggerMu.Lock()
	delete(scaffoldTriggerLastStart, repoKey)
	delete(scaffoldTriggerRunning, repoKey)
	scaffoldTriggerMu.Unlock()
	t.Cleanup(func() {
		scaffoldTriggerMu.Lock()
		delete(scaffoldTriggerLastStart, repoKey)
		delete(scaffoldTriggerRunning, repoKey)
		scaffoldTriggerMu.Unlock()
	})

	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", targetPath, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", targetPath, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", targetPath, "config", "user.name", "Test").Run()
	if err := os.WriteFile(filepath.Join(targetPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", targetPath, "add", ".").CombinedOutput(); err != nil {
		t.Skipf("git add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", targetPath, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit failed: %v: %s", err, out)
	}
	setupTestDB(t, targetPath)

	// Mock git clone/pull to succeed (actual git ops on the real repo are fine).
	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "show" && strings.HasSuffix(args[3], ":README.md") {
			return []byte("# Demo\n@engine"), nil
		}
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 2 {
			// Create and commit a file so hasCommitProgress detects real progress.
			if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "progress.txt"), []byte("done"), 0o644); err != nil {
				t.Fatalf("write progress file: %v", err)
			}
			_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "progress.txt").CombinedOutput()
			_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: add progress").CombinedOutput()
		}
		ctx.OnChunk("response", false)
		ctx.OnChunk("", true)
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/progressrepo"}}`))

	if callCount != 2 {
		t.Fatalf("expected 2 AI calls (first no-op, second with progress), got %d", callCount)
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "no-op retry") {
		t.Fatalf("expected 'no-op retry' notification, got: %v", notifier.notified)
	}
}

// ── triggerIssueSession: bad full_name branch ─────────────────────────────────

func TestTriggerIssueSession_BadFullName(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine fix it","user":{"login":"bob"}},"issue":{"number":1,"title":"Bug"},"repository":{"full_name":"no-slash-here"}}`)
	triggerIssueSession(projectPath, payload)
}

// ── triggerIssueOpenedSession: bad full_name branch ───────────────────────────

func TestTriggerIssueOpenedSession_BadFullName(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	payload := json.RawMessage(`{"action":"opened","issue":{"number":1,"title":"Bug","body":"desc"},"repository":{"full_name":"no-slash-here"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
}

// ── triggerIssueOpenedSession: db.WithProject error branch ───────────────────

func TestTriggerIssueOpenedSession_DBInitFails_LogsError(t *testing.T) {
	projectPath := t.TempDir()
	prepareScaffoldTargetRepo(t, projectPath, "owner", "openeddbfail", "# Demo")
	t.Setenv("ENGINE_STATE_DIR", "/dev/null/cannot-create")
	payload := json.RawMessage(`{"action":"opened","issue":{"number":1,"title":"Bug","body":"desc"},"repository":{"full_name":"owner/openeddbfail"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
}

// ── triggerIssueSession: no @engine mention branch ────────────────────────────

func TestTriggerIssueSession_NoEngineMention(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	payload := json.RawMessage(`{"action":"created","comment":{"body":"just a comment","user":{"login":"bob"}},"issue":{"number":1,"title":"Bug"},"repository":{"full_name":"owner/somerepo"}}`)
	triggerIssueSession(projectPath, payload)
}

// ── triggerScaffoldSession: first attempt makes progress ──────────────────────

func TestTriggerScaffoldSession_FirstAttemptMakesProgress(t *testing.T) {
	t.Skip("obsolete: 2-attempt semantics replaced by orchestrator outer-iteration loop")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	targetPath := buildAutonomousRepoPath(projectPath, "owner", "firstprogress")
	repoKey := "owner/firstprogress"
	scaffoldTriggerMu.Lock()
	delete(scaffoldTriggerLastStart, repoKey)
	delete(scaffoldTriggerRunning, repoKey)
	scaffoldTriggerMu.Unlock()
	t.Cleanup(func() {
		scaffoldTriggerMu.Lock()
		delete(scaffoldTriggerLastStart, repoKey)
		delete(scaffoldTriggerRunning, repoKey)
		scaffoldTriggerMu.Unlock()
	})

	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", targetPath, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", targetPath, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", targetPath, "config", "user.name", "Test").Run()
	if err := os.WriteFile(filepath.Join(targetPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", targetPath, "add", ".").CombinedOutput(); err != nil {
		t.Skipf("git add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", targetPath, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit failed: %v: %s", err, out)
	}
	setupTestDB(t, targetPath)
	oldTS := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	if err := db.WithProject(targetPath, func() error {
		return db.InsertSessionWithTimestamps("scaffold-firstprogress-old", targetPath, "main", oldTS, oldTS)
	}); err != nil {
		t.Fatalf("seed prior scaffold session: %v", err)
	}

	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "show" && strings.HasSuffix(args[3], ":README.md") {
			return []byte("# Demo\n@engine"), nil
		}
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	sawPriorContext := false
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		callCount++
		sawPriorContext = strings.Contains(prompt, "Prior scaffold attempts")
		if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "first_progress.txt"), []byte("done"), 0o644); err != nil {
			t.Fatalf("write first progress file: %v", err)
		}
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "first_progress.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: first pass").CombinedOutput()
		ctx.OnChunk("response", false)
		ctx.OnChunk("", true)
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/firstprogress"}}`))

	if callCount != 1 {
		t.Fatalf("expected 1 AI call (first attempt makes progress), got %d", callCount)
	}
	if !sawPriorContext {
		t.Fatal("expected prior scaffold context in prompt")
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "Scaffold session") || strings.Contains(joined, "no-op retry") {
		t.Fatalf("expected first-attempt success notification, got: %v", notifier.notified)
	}
}

// TestScaffoldErrorRetryPrompt_EmptyReason_UsesDefault verifies that the
// empty-reason guard in scaffoldErrorRetryPrompt substitutes "unknown failure".
func TestScaffoldErrorRetryPrompt_EmptyReason_UsesDefault(t *testing.T) {
	result := scaffoldErrorRetryPrompt("owner", "repo", "")
	if !strings.Contains(result, "unknown failure") {
		t.Fatalf("expected 'unknown failure' in prompt, got: %q", result)
	}
}

// TestTriggerScaffoldSession_OnError_EmptyReason_DefaultsUnknown verifies that
// when ctx.OnError("") is called the scaffold loop defaults attemptFailureReason
// to "unknown failure" and retries attempt 1 with that reason.
func TestTriggerScaffoldSession_OnError_EmptyReason_DefaultsUnknown(t *testing.T) {
	t.Skip("obsolete: attemptFailureReason was a property of the inline 2-attempt loop, replaced by ai.RunAutonomousProject")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	prepareScaffoldTargetRepo(t, projectPath, "owner", "emptyerrrepo", "# Demo\n@engine")
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			ctx.OnError("")
			return
		}
		ctx.OnError("second failure")
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/emptyerrrepo"}}`))

	if callCount != 2 {
		t.Fatalf("expected 2 AI calls (retry after empty error), got %d", callCount)
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "did not complete") {
		t.Fatalf("expected 'did not complete' retry notification, got: %v", notifier.notified)
	}
}

// TestTriggerScaffoldSession_ErrorSecondAttemptWithRepoProgress_ReportsStoppedBeforeCompletion
// verifies that when attempt 2 errors but the repo has commit progress, the
// "stopped before completion" notification is sent (not "blocked").
func TestTriggerScaffoldSession_ErrorSecondAttemptWithRepoProgress_ReportsStoppedBeforeCompletion(t *testing.T) {
	t.Skip("obsolete: 'stopped before completion' was a property of the inline 2-attempt loop, replaced by ai.RunAutonomousProject")
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	targetPath := buildAutonomousRepoPath(projectPath, "owner", "progbeforeerr")
	repoKey := "owner/progbeforeerr"
	scaffoldTriggerMu.Lock()
	delete(scaffoldTriggerLastStart, repoKey)
	delete(scaffoldTriggerRunning, repoKey)
	scaffoldTriggerMu.Unlock()
	t.Cleanup(func() {
		scaffoldTriggerMu.Lock()
		delete(scaffoldTriggerLastStart, repoKey)
		delete(scaffoldTriggerRunning, repoKey)
		scaffoldTriggerMu.Unlock()
	})

	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", targetPath, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	_ = exec.Command("git", "-C", targetPath, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", targetPath, "config", "user.name", "Test").Run()
	if err := os.WriteFile(filepath.Join(targetPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", targetPath, "add", ".").CombinedOutput(); err != nil {
		t.Skipf("git add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", targetPath, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Skipf("git commit failed: %v: %s", err, out)
	}
	setupTestDB(t, targetPath)

	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) >= 4 && args[0] == "-C" && args[2] == "show" && strings.HasSuffix(args[3], ":README.md") {
			return []byte("# Demo\n@engine"), nil
		}
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	withAIChatMocked(t, func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			ctx.OnError("first attempt failed")
			return
		}
		if err := os.WriteFile(filepath.Join(ctx.ProjectPath, "partial.txt"), []byte("done"), 0o644); err != nil {
			t.Fatalf("write partial file: %v", err)
		}
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "partial.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "partial work").CombinedOutput()
		ctx.OnError("incomplete")
	})

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/progbeforeerr"}}`))

	if callCount != 2 {
		t.Fatalf("expected 2 AI calls, got %d", callCount)
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "stopped before completion") {
		t.Fatalf("expected 'stopped before completion' notification, got: %v", notifier.notified)
	}
}

// ── countScaffoldSessions ─────────────────────────────────────────────────────

func TestCountScaffoldSessions_Empty(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	n := countScaffoldSessions(projectPath, "myrepo")
	if n != 0 {
		t.Errorf("expected 0 sessions, got %d", n)
	}
}

func TestCountScaffoldSessions_ListError(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	orig := dbListSessionsForScaffoldFn
	dbListSessionsForScaffoldFn = func(string) ([]db.Session, error) {
		return nil, errors.New("list failed")
	}
	t.Cleanup(func() { dbListSessionsForScaffoldFn = orig })

	if n := countScaffoldSessions(projectPath, "myrepo"); n != 0 {
		t.Fatalf("expected zero sessions on list error, got %d", n)
	}
}

func TestCountScaffoldSessions_WithSessions(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if err := db.WithProject(projectPath, func() error {
		for i := range 3 {
			id := fmt.Sprintf("scaffold-myrepo-%d", i)
			if err := db.CreateSession(id, projectPath, "main"); err != nil {
				return err
			}
		}
		// Different repo — should not count.
		return db.CreateSession("scaffold-other-0", projectPath, "main")
	}); err != nil {
		t.Fatalf("db.WithProject: %v", err)
	}
	n := countScaffoldSessions(projectPath, "myrepo")
	if n != 3 {
		t.Errorf("expected 3 sessions for myrepo, got %d", n)
	}
}

// ── scaffoldPriorAttemptContext ───────────────────────────────────────────────

func TestScaffoldPriorAttemptContext_Empty(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if got := scaffoldPriorAttemptContext(projectPath, "norepo"); got != "" {
		t.Errorf("expected empty context with no prior sessions, got %q", got)
	}
}

func TestScaffoldPriorAttemptContext_ListError(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	orig := dbListSessionsForScaffoldFn
	dbListSessionsForScaffoldFn = func(string) ([]db.Session, error) {
		return nil, errors.New("list failed")
	}
	t.Cleanup(func() { dbListSessionsForScaffoldFn = orig })

	if got := scaffoldPriorAttemptContext(projectPath, "myrepo"); got != "" {
		t.Fatalf("expected empty context on list error, got %q", got)
	}
}

func TestScaffoldPriorAttemptContext_WithSessions(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if err := db.WithProject(projectPath, func() error {
		if err := db.CreateSession("scaffold-loopy-1", projectPath, "main"); err != nil {
			return err
		}
		if err := db.SaveMessage("m-1", "scaffold-loopy-1", "assistant", "tried to clone, hit auth wall", nil); err != nil {
			return err
		}
		if err := db.CreateSession("scaffold-loopy-2", projectPath, "main"); err != nil {
			return err
		}
		return db.SaveMessage("m-2", "scaffold-loopy-2", "assistant", "go.work conflict, tests failed", nil)
	}); err != nil {
		t.Fatalf("db.WithProject: %v", err)
	}
	ctx := scaffoldPriorAttemptContext(projectPath, "loopy")
	if !strings.Contains(ctx, "Prior scaffold attempts (2 total)") {
		t.Errorf("expected prior attempts header, got: %s", ctx)
	}
	if !strings.Contains(ctx, "go.work conflict") {
		t.Errorf("expected last assistant content, got: %s", ctx)
	}
	if !strings.Contains(ctx, "Do NOT restart from scratch") {
		t.Errorf("expected continuation directive, got: %s", ctx)
	}
}

func TestScaffoldPriorAttemptContext_TruncatesAndFallsBack(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	longMessage := strings.Repeat("x", 320)
	if err := db.WithProject(projectPath, func() error {
		for i := range 4 {
			id := fmt.Sprintf("scaffold-longrepo-%d", i)
			if err := db.CreateSession(id, projectPath, "main"); err != nil {
				return err
			}
			if i == 0 {
				if err := db.SaveMessage("long-msg", id, "assistant", longMessage, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("db.WithProject: %v", err)
	}

	ctx := scaffoldPriorAttemptContext(projectPath, "longrepo")
	if !strings.Contains(ctx, "Prior scaffold attempts (4 total)") {
		t.Fatalf("expected total attempt count, got: %s", ctx)
	}
	if !strings.Contains(ctx, "…") {
		t.Fatalf("expected long assistant message to be truncated, got: %s", ctx)
	}
	if !strings.Contains(ctx, "(no message recorded)") {
		t.Fatalf("expected empty-message fallback, got: %s", ctx)
	}
}

// ── scheduleScaffoldRetry ─────────────────────────────────────────────────────

func TestScheduleScaffoldRetry_MaxRetriesReached(t *testing.T) {
	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	// Seed scaffoldMaxRetries sessions so the cap is hit.
	if err := db.WithProject(projectPath, func() error {
		for i := range scaffoldMaxRetries {
			id := fmt.Sprintf("scaffold-caprepo-%d", i)
			if err := db.CreateSession(id, projectPath, "main"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("db.WithProject: %v", err)
	}
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	called := false
	withAIChatMocked(t, func(_ *ai.ChatContext, _ string) { called = true })

	scheduleScaffoldRetry(projectPath, "owner", "caprepo",
		json.RawMessage(`{"repository":{"full_name":"owner/caprepo"}}`),
		"test reason",
	)

	if called {
		t.Error("AI should not be called when max retries reached")
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "maximum") {
		t.Errorf("expected max-retries notification, got: %v", notifier.notified)
	}
}

func TestScheduleScaffoldRetry_SchedulesRetry(t *testing.T) {
	origDelay := scaffoldRetryDelay
	scaffoldRetryDelay = 50 * time.Millisecond
	t.Cleanup(func() { scaffoldRetryDelay = origDelay })

	projectPath := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	// Track when the AfterFunc calls the scaffold trigger.
	triggered := make(chan struct{}, 1)
	origTrigger := triggerScaffoldSessionFn
	triggerScaffoldSessionFn = func(_ string, _ json.RawMessage) {
		select {
		case triggered <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { triggerScaffoldSessionFn = origTrigger })

	scheduleScaffoldRetry(projectPath, "owner", "retryrepo",
		json.RawMessage(`{"repository":{"full_name":"owner/retryrepo"}}`),
		"no changes",
	)

	// Verify the notification was sent.
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "retry") && !strings.Contains(joined, "Retry") {
		t.Errorf("expected retry notification, got: %v", notifier.notified)
	}

	select {
	case <-triggered:
		// Good — the retry fired and called triggerScaffoldSessionFn.
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for scheduled retry to fire")
	}
}
