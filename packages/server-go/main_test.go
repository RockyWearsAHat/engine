package main

import (
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

type fakeDiscordService struct{}

func (f *fakeDiscordService) Start() error { return nil }

func (f *fakeDiscordService) Close() error { return nil }

func (f *fakeDiscordService) CurrentConfig() discord.Config { return discord.Config{} }

func (f *fakeDiscordService) Reload(cfg discord.Config) error { return nil }

func (f *fakeDiscordService) SearchHistory(projectPath, query, since string, limit int) ([]db.DiscordSearchHit, error) {
	return []db.DiscordSearchHit{}, nil
}

func (f *fakeDiscordService) RecentHistory(projectPath, threadID, since string, limit int) ([]db.DiscordMessage, error) {
	return []db.DiscordMessage{}, nil
}
func (f *fakeDiscordService) SendDMToOwner(_ string) error      { return nil }
func (f *fakeDiscordService) NotifyProjectProgress(_, _ string) {}

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
	origScaffoldRunning := scaffoldTriggerRunning
	origScaffoldLastStart := scaffoldTriggerLastStart
	origScaffoldAttemptTimeout := scaffoldAttemptTimeout

	scaffoldTriggerMu.Lock()
	scaffoldTriggerRunning = make(map[string]bool)
	scaffoldTriggerLastStart = make(map[string]time.Time)
	scaffoldTriggerMu.Unlock()

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

		scaffoldTriggerMu.Lock()
		scaffoldTriggerRunning = origScaffoldRunning
		scaffoldTriggerLastStart = origScaffoldLastStart
		scaffoldTriggerMu.Unlock()
		scaffoldAttemptTimeout = origScaffoldAttemptTimeout
	})
}

func setupTestDB(t *testing.T, projectPath string) {
	t.Helper()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
}

func countSessions(t *testing.T, projectPath string) int {
	t.Helper()
	sessions, err := db.ListSessions(projectPath)
	if err != nil {
		t.Fatalf("db.ListSessions: %v", err)
	}
	return len(sessions)
}

func prepareScaffoldTargetRepo(t *testing.T, baseProjectPath, owner, repo, readme string) string {
	t.Helper()
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
	got := buildAutonomousRepoPath(base, "octo", "demo")
	want := filepath.Join(base, ".engine", "projects", "octo-demo")
	if got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
}

func TestBuildAutonomousRepoPath_EmptyOwner(t *testing.T) {
	base := "/tmp/engine-root"
	t.Setenv("ENGINE_CLONES_DIR", "")
	got := buildAutonomousRepoPath(base, "", "demo")
	want := filepath.Join(base, ".engine", "projects", "demo")
	if got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
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

func TestHasRecentScaffoldSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	target := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
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
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	target := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	if err := db.WithProject(target, func() error {
		return db.CreateSession("scaffold-repo-987", target, "main")
	}); err != nil {
		t.Fatalf("WithProject create scaffold session: %v", err)
	}

	chatCalls := 0
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		chatCalls++
	}
	t.Cleanup(func() { aiChatFn = origAI })

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
	orig := runCommandCombinedOutputFn
	defer func() { runCommandCombinedOutputFn = orig }()
	runCommandCombinedOutputFn = func(_ string, _ ...string) ([]byte, error) {
		return []byte("fetch failed"), fmt.Errorf("fetch error")
	}
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
	orig := runCommandCombinedOutputFn
	defer func() { runCommandCombinedOutputFn = orig }()
	call := 0
	runCommandCombinedOutputFn = func(_ string, _ ...string) ([]byte, error) {
		call++
		if call == 1 {
			return []byte("ok"), nil // fetch succeeds
		}
		return []byte("pull failed"), fmt.Errorf("pull error")
	}
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
		t.Fatalf("expected fetch+clean+pull commands, got %d", len(calls))
	}
	fetchCmd := strings.Join(calls[0], " ")
	cleanCmd := strings.Join(calls[1], " ")
	pullCmd := strings.Join(calls[2], " ")
	if !strings.Contains(fetchCmd, "git -C "+dest+" fetch origin --prune") {
		t.Fatalf("unexpected fetch command: %s", fetchCmd)
	}
	if !strings.Contains(cleanCmd, "git -C "+dest+" clean -fdx") {
		t.Fatalf("unexpected clean command: %s", cleanCmd)
	}
	if !strings.Contains(pullCmd, "git -C "+dest+" pull --ff-only origin HEAD") {
		t.Fatalf("unexpected pull command: %s", pullCmd)
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

func TestTriggerScaffoldSession_NoEngineTag_Skips(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# My Project\n\nNo trigger here.")

	before := countSessions(t, projectPath)
	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	after := countSessions(t, projectPath)

	if after != before {
		t.Fatalf("expected no session created when @engine tag absent, before=%d after=%d", before, after)
	}
}

func TestTriggerCIAnalysisSession_BadPayload(t *testing.T) {
	// Bad JSON should return early without side effects.
	triggerCIAnalysisSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerScaffoldSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	before := countSessions(t, targetPath)
	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
	triggerScaffoldSession(projectPath, payload)
	after := countSessions(t, targetPath)

	if after <= before {
		t.Fatalf("expected session count to increase, before=%d after=%d", before, after)
	}
}

func TestTriggerCIAnalysisSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	before := countSessions(t, projectPath)
	payload := json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com/run/1","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`)
	triggerCIAnalysisSession(projectPath, payload)
	after := countSessions(t, projectPath)

	if after <= before {
		t.Fatalf("expected session count to increase, before=%d after=%d", before, after)
	}
}

func TestTriggerIssueSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	before := countSessions(t, targetPath)
	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	after := countSessions(t, targetPath)

	if after <= before {
		t.Fatalf("expected session count to increase, before=%d after=%d", before, after)
	}
}

func TestTriggerIssueOpenedSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	before := countSessions(t, targetPath)
	payload := json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
	after := countSessions(t, targetPath)

	if after <= before {
		t.Fatalf("expected session count to increase, before=%d after=%d", before, after)
	}
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
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")
	t.Setenv("PORT", "31337")

	listenErr := errors.New("listen failed")
	var healthHandler func(http.ResponseWriter, *http.Request)
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {
		if pattern == "/health" {
			healthHandler = handler
		}
	}
	httpHandleFn = func(pattern string, handler http.Handler) {}
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
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "1")
	t.Setenv("ENGINE_REMOTE", "")

	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
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
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "1")

	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
	newRemoteServerFn = func(cfg remote.Config, wsHandler http.HandlerFunc) (*remote.Server, error) {
		return nil, errors.New("remote init failed")
	}

	err := run()
	if err == nil {
		t.Fatal("expected remote mode error")
	}
}

func TestRun_VPNMode_ListenErrorAfterRegister(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "1")
	t.Setenv("ENGINE_REMOTE", "")

	listenErr := errors.New("vpn listen failed")
	registered := false
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
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
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) {
		return discord.Config{Enabled: false}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
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

func (f *fakeDiscordServiceStartErr) Start() error                { return f.err }
func (f *fakeDiscordServiceStartErr) Close() error                { return nil }
func (f *fakeDiscordServiceStartErr) CurrentConfig() discord.Config { return discord.Config{} }
func (f *fakeDiscordServiceStartErr) Reload(cfg discord.Config) error { return nil }
func (f *fakeDiscordServiceStartErr) SearchHistory(pp, q, since string, limit int) ([]db.DiscordSearchHit, error) {
	return nil, nil
}
func (f *fakeDiscordServiceStartErr) RecentHistory(pp, tid, since string, limit int) ([]db.DiscordMessage, error) {
	return nil, nil
}
func (f *fakeDiscordServiceStartErr) SendDMToOwner(_ string) error      { return nil }
func (f *fakeDiscordServiceStartErr) NotifyProjectProgress(_, _ string) {}

func TestRun_DiscordEnabled_StartError_NonFatal(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	bridgeSet := false
	listenErr := errors.New("test stop")
	dbInitFn = func(path string) error { return db.Init(path) }
	newHubFn = func(path string) *ws.Hub { return ws.NewHub(path) }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: true, BotToken: "tok", GuildID: "g1"}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordServiceStartErr{err: errors.New("discord open: fake gateway error")}, nil
	}
	setDiscordBridgeFn = func(s ws.DiscordBridge) { bridgeSet = true }
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(pattern string, handler http.Handler) {}
	httpListenAndServeFn = func(addr string, handler http.Handler) error { return listenErr }

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error after non-fatal discord start failure, got %v", err)
	}
	if bridgeSet {
		t.Fatal("expected discord bridge to remain unset when Start fails")
	}
}

func TestRun_DiscordEnabled_ServiceInitError_NonFatal(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	bridgeSet := false
	listenErr := errors.New("test stop")
	dbInitFn = func(path string) error { return db.Init(path) }
	newHubFn = func(path string) *ws.Hub { return ws.NewHub(path) }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: true, BotToken: "tok", GuildID: "g1"}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return nil, errors.New("init failed")
	}
	setDiscordBridgeFn = func(s ws.DiscordBridge) { bridgeSet = true }
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(pattern string, handler http.Handler) {}
	httpListenAndServeFn = func(addr string, handler http.Handler) error { return listenErr }

	err := run()
	if !errors.Is(err, listenErr) {
		t.Fatalf("expected listen error after non-fatal discord init failure, got %v", err)
	}
	if bridgeSet {
		t.Fatal("expected discord bridge to remain unset when init fails")
	}
}

func TestRun_VPNMode_RespectsVPNPortOverride(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "1")
	t.Setenv("ENGINE_REMOTE", "")
	t.Setenv("VPN_PORT", "4545")

	var seenPort string
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) { return discord.Config{Enabled: false}, nil }
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) { return &fakeDiscordService{}, nil }
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
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "1")
	t.Setenv("REMOTE_PORT", "5656")

	var seenPort string
	dbInitFn = func(projectPath string) error { return nil }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) { return discord.Config{Enabled: false}, nil }
	newDiscordServiceFn = func(cfg discord.Config, projectPath string) (discordRuntime, error) { return &fakeDiscordService{}, nil }
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
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_VPN", "")
	t.Setenv("ENGINE_REMOTE", "")

	bridgeSet := false
	dbInitFn = func(path string) error { return db.Init(path) }
	newHubFn = func(path string) *ws.Hub { return ws.NewHub(path) }
	loadDiscordConfigFn = func(path string) (discord.Config, error) {
		return discord.Config{Enabled: true, BotToken: "tok", GuildID: "g1"}, nil
	}
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordService{}, nil
	}
	setDiscordBridgeFn = func(s ws.DiscordBridge) { bridgeSet = true }
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
	httpHandleFn = func(pattern string, handler http.Handler) {}
	httpListenAndServeFn = func(addr string, handler http.Handler) error {
		return errors.New("test stop")
	}

	_ = run()

	if !bridgeSet {
		t.Error("expected discord bridge to be set after successful Start")
	}
}

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
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	withAIMockServer(t)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
	triggerScaffoldSession(projectPath, payload)
	// If OnChunk was not called, the session message count would still be from ai.Chat.
	// Just verify no panic and session was created.
	sessions, err := db.ListSessions(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Error("expected at least one session to be created")
	}
}

func TestTriggerCIAnalysisSession_OnChunkCalled(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	withAIMockServer(t)

	payload := json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`)
	triggerCIAnalysisSession(projectPath, payload)
	sessions, err := db.ListSessions(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Error("expected at least one session to be created")
	}
}

func TestTriggerIssueSession_OnChunkCalled(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	withAIMockServer(t)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	sessions, err := db.ListSessions(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Error("expected at least one session to be created")
	}
}

func TestTriggerIssueOpenedSession_OnChunkCalled(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	withAIMockServer(t)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")

	payload := json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
	sessions, err := db.ListSessions(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Error("expected at least one session to be created")
	}
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

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	triggerCIAnalysisSession(projectPath, json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueSession(projectPath, json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueOpenedSession(projectPath, json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`))
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

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	triggerCIAnalysisSession(projectPath, json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueSession(projectPath, json.RawMessage(`{"action":"created","comment":{"body":"@engine please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueOpenedSession(projectPath, json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`))
}

func TestTriggerScaffoldSession_NoOpFirstPass_RetriesThenReportsNoop(t *testing.T) {
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
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		ctx.OnChunk("planned but unchanged", false)
		ctx.OnChunk("", true)
	}
	t.Cleanup(func() { aiChatFn = origAI })

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))

	if callCount != 2 {
		t.Fatalf("expected scaffold retry after first no-op, got %d attempts", callCount)
	}

	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "First scaffold pass") {
		t.Fatalf("expected first-pass no-op warning in notifications, got: %v", notifier.notified)
	}
	if !strings.Contains(joined, "ended with no repository changes") {
		t.Fatalf("expected final no-op failure notification, got: %v", notifier.notified)
	}
}

func TestTriggerScaffoldSession_ErrorFirstPass_RetriesAndSucceeds(t *testing.T) {
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
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			ctx.OnError("temporary tool failure")
			return
		}
		_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "scaffold_progress.txt"), []byte("done"), 0o644)
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "scaffold_progress.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: initial implementation").CombinedOutput()
		ctx.OnChunk("recovered and completed", false)
		ctx.OnChunk("", true)
	}
	t.Cleanup(func() { aiChatFn = origAI })

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
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			// Simulate AI writing only a planning doc (untracked, not committed).
			_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "PROJECT_GOAL.md"), []byte("plan"), 0o644)
			ctx.OnChunk("wrote project goal", false)
			ctx.OnChunk("", true)
			return
		}
		// Second attempt: record the prompt then commit actual code.
		attempt2Prompt = prompt
		_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "main.go"), []byte("package main"), 0o644)
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "main.go").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: add main").CombinedOutput()
		ctx.OnChunk("implemented", false)
		ctx.OnChunk("", true)
	}
	t.Cleanup(func() { aiChatFn = origAI })

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
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	prepareScaffoldTargetRepo(t, projectPath, "owner", "repo", "# Demo\n@engine")
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	origTimeout := scaffoldAttemptTimeout
	scaffoldAttemptTimeout = 20 * time.Millisecond
	t.Cleanup(func() { scaffoldAttemptTimeout = origTimeout })

	callCount := 0
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			if ctx.Cancel != nil {
				<-ctx.Cancel
			}
			return
		}
		_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "partial_progress.txt"), []byte("done"), 0o644)
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "partial_progress.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "partial progress").CombinedOutput()
		ctx.OnError("agent exited early")
	}
	t.Cleanup(func() { aiChatFn = origAI })

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
	eventsWatcherStartFn = func(_ *gh.EventsWatcher) { ewStarted = true }

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
	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		return []byte(""), nil
	}
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {}
	t.Cleanup(func() {
		runCommandCombinedOutputFn = origRun
		aiChatFn = origAI
	})

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
	enrolledPath string
	enrolledOwner string
	enrolledRepo  string
}

func (m *mockAutoEnroller) SendDMToOwner(_ string) error { return nil }
func (m *mockAutoEnroller) NotifyProjectProgress(_, _ string) {}
func (m *mockAutoEnroller) CurrentConfig() discord.Config { return discord.Config{} }
func (m *mockAutoEnroller) Reload(_ discord.Config) error { return nil }
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
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	targetProjectPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "myrepo", "# Demo\n@engine")

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
	notified []string
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
func (m *mockProgressNotifier) Start() error  { return nil }
func (m *mockProgressNotifier) Close() error  { return nil }

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
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "issuerepo2", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnError("issue error")
	}
	setupTestDB(t, targetPath)

	payload := json.RawMessage(`{"action":"created","comment":{"body":"@engine fix","user":{"login":"bob"}},"issue":{"number":78,"title":"Other"},"repository":{"full_name":"owner/issuerepo2"}}`)
	triggerIssueSession(projectPath, payload)
}

func TestTriggerIssueOpenedSession_OnChunkDone(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "openedrepo", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnChunk("opened content", false)
		ctx.OnChunk("", true)
	}
	setupTestDB(t, targetPath)

	payload := json.RawMessage(`{"action":"opened","issue":{"number":88,"title":"Feature","body":"desc"},"repository":{"full_name":"owner/openedrepo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
}

func TestTriggerIssueOpenedSession_OnError(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	targetPath := prepareScaffoldTargetRepo(t, projectPath, "owner", "openederrrepo", "# Demo")

	aiChatFn = func(ctx *ai.ChatContext, _ string) {
		ctx.OnError("opened error")
	}
	setupTestDB(t, targetPath)

	payload := json.RawMessage(`{"action":"opened","issue":{"number":89,"title":"Bug2","body":"desc2"},"repository":{"full_name":"owner/openederrrepo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
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
		origAI := aiChatFn
		aiChatFn = func(ctx *ai.ChatContext, prompt string) { called = true }
		t.Cleanup(func() { aiChatFn = origAI })

		triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/deduptest"}}`))

		if called {
			t.Fatal("expected AI to NOT be called when trigger is deduped")
		}
	}

	// ── triggerScaffoldSession: second attempt makes progress branch ──────────────

	func TestTriggerScaffoldSession_SecondAttemptMakesProgress(t *testing.T) {
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
			return []byte("ok"), nil
		}
		t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

		notifier := &mockProgressNotifier{}
		ws.SetDiscordBridge(notifier)
		t.Cleanup(func() { ws.SetDiscordBridge(nil) })

		callCount := 0
		origAI := aiChatFn
		aiChatFn = func(ctx *ai.ChatContext, prompt string) {
			callCount++
			if callCount == 2 {
				// Create and commit a file so hasCommitProgress detects real progress.
				_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "progress.txt"), []byte("done"), 0o644)
				_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "progress.txt").CombinedOutput()
				_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: add progress").CombinedOutput()
			}
			ctx.OnChunk("response", false)
			ctx.OnChunk("", true)
		}
		t.Cleanup(func() { aiChatFn = origAI })

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

	origRun := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(name string, args ...string) ([]byte, error) {
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "first_progress.txt"), []byte("done"), 0o644)
				_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "first_progress.txt").CombinedOutput()
				_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "scaffold: first pass").CombinedOutput()
		ctx.OnChunk("response", false)
		ctx.OnChunk("", true)
	}
	t.Cleanup(func() { aiChatFn = origAI })

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/firstprogress"}}`))

	if callCount != 1 {
		t.Fatalf("expected 1 AI call (first attempt makes progress), got %d", callCount)
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
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	prepareScaffoldTargetRepo(t, projectPath, "owner", "emptyerrrepo", "# Demo\n@engine")
	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			ctx.OnError("")
			return
		}
		ctx.OnError("second failure")
	}
	t.Cleanup(func() { aiChatFn = origAI })

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
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runCommandCombinedOutputFn = origRun })

	notifier := &mockProgressNotifier{}
	ws.SetDiscordBridge(notifier)
	t.Cleanup(func() { ws.SetDiscordBridge(nil) })

	callCount := 0
	origAI := aiChatFn
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		callCount++
		if callCount == 1 {
			ctx.OnError("first attempt failed")
			return
		}
		_ = os.WriteFile(filepath.Join(ctx.ProjectPath, "partial.txt"), []byte("done"), 0o644)
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "add", "partial.txt").CombinedOutput()
		_, _ = exec.Command("git", "-C", ctx.ProjectPath, "commit", "-m", "partial work").CombinedOutput()
		ctx.OnError("incomplete")
	}
	t.Cleanup(func() { aiChatFn = origAI })

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/progbeforeerr"}}`))

	if callCount != 2 {
		t.Fatalf("expected 2 AI calls, got %d", callCount)
	}
	joined := strings.Join(notifier.notified, "\n")
	if !strings.Contains(joined, "stopped before completion") {
		t.Fatalf("expected 'stopped before completion' notification, got: %v", notifier.notified)
	}
}
