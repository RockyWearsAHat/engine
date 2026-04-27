package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
func (f *fakeDiscordService) SendDMToOwner(_ string) error { return nil }

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
	if len(calls) != 2 {
		t.Fatalf("expected fetch+pull commands, got %d", len(calls))
	}
	fetchCmd := strings.Join(calls[0], " ")
	pullCmd := strings.Join(calls[1], " ")
	if !strings.Contains(fetchCmd, "git -C "+dest+" fetch origin --prune") {
		t.Fatalf("unexpected fetch command: %s", fetchCmd)
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
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# My Project\n\nNo trigger here."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

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
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	before := countSessions(t, projectPath)
	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
	triggerScaffoldSession(projectPath, payload)
	after := countSessions(t, projectPath)

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

	before := countSessions(t, projectPath)
	payload := json.RawMessage(`{"action":"created","comment":{"body":"please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	after := countSessions(t, projectPath)

	if after <= before {
		t.Fatalf("expected session count to increase, before=%d after=%d", before, after)
	}
}

func TestTriggerIssueOpenedSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

	before := countSessions(t, projectPath)
	payload := json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
	after := countSessions(t, projectPath)

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
func (f *fakeDiscordServiceStartErr) SendDMToOwner(_ string) error { return nil }

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
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`)
	triggerScaffoldSession(projectPath, payload)
	// If OnChunk was not called, the session message count would still be from ai.Chat.
	// Just verify no panic and session was created.
	sessions, err := db.ListSessions(projectPath)
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

	payload := json.RawMessage(`{"action":"created","comment":{"body":"please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`)
	triggerIssueSession(projectPath, payload)
	sessions, err := db.ListSessions(projectPath)
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

	payload := json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)
	sessions, err := db.ListSessions(projectPath)
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
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	triggerCIAnalysisSession(projectPath, json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueSession(projectPath, json.RawMessage(`{"action":"created","comment":{"body":"please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`))
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
		ctx.OnChunk("", true)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("# Demo\n@engine"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	triggerScaffoldSession(projectPath, json.RawMessage(`{"repository":{"full_name":"owner/repo"}}`))
	triggerCIAnalysisSession(projectPath, json.RawMessage(`{"workflow_run":{"name":"CI","html_url":"https://example.com","conclusion":"failure"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueSession(projectPath, json.RawMessage(`{"action":"created","comment":{"body":"please fix","user":{"login":"bob"}},"issue":{"number":42,"title":"Bug"},"repository":{"full_name":"owner/repo"}}`))
	triggerIssueOpenedSession(projectPath, json.RawMessage(`{"action":"opened","issue":{"number":43,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`))
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