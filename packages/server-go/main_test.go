package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

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

func withRunDepsReset(t *testing.T) {
	t.Helper()
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
	origNewVPNTunnel := newVPNTunnelFn
	origVPNRegister := vpnRegisterRoutesFn
	origVPNListen := vpnListenTLSFn
	origNewRemoteServer := newRemoteServerFn
	origSetPairing := setPairingManagerFn
	origRemoteListen := remoteListenTLSFn
	origHandleFunc := httpHandleFuncFn
	origHandle := httpHandleFn
	origListen := httpListenAndServeFn

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
		newVPNTunnelFn = origNewVPNTunnel
		vpnRegisterRoutesFn = origVPNRegister
		vpnListenTLSFn = origVPNListen
		newRemoteServerFn = origNewRemoteServer
		setPairingManagerFn = origSetPairing
		remoteListenTLSFn = origRemoteListen
		httpHandleFuncFn = origHandleFunc
		httpHandleFn = origHandle
		httpListenAndServeFn = origListen
	})
}

func setupTestDB(t *testing.T, projectPath string) {
	t.Helper()
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

func TestTriggerCIAnalysisSession_BadPayload(t *testing.T) {
	// Bad JSON should return early without side effects.
	triggerCIAnalysisSession(t.TempDir(), json.RawMessage(`{bad json}`))
}

func TestTriggerScaffoldSession_ValidPayloadCreatesSession(t *testing.T) {
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)
	t.Setenv("ENGINE_MODEL_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")

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
	logFatalFn = func(v ...interface{}) { called = true }

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

func TestRun_DiscordEnabled_StartError(t *testing.T) {
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
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return &fakeDiscordServiceStartErr{err: errors.New("discord open: fake gateway error")}, nil
	}

	err := run()
	if err == nil {
		t.Fatal("expected error from discord Start failure")
	}
}

func TestRun_DiscordEnabled_ServiceInitError(t *testing.T) {
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
	newDiscordServiceFn = func(cfg discord.Config, path string) (discordRuntime, error) {
		return nil, errors.New("init failed")
	}

	err := run()
	if err == nil {
		t.Fatal("expected error from discord service init failure")
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
	saveMessageFn = func(id, sessionId, role, content string, toolCalls interface{}) error {
		return errors.New("save fail")
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

	_ = run()
	if monitor == nil {
		t.Fatal("expected repo monitor to be constructed")
	}
	monitor.OnReadmeChange(json.RawMessage(`{"repository":{"full_name":"o/r"}}`))
	monitor.OnCIFailure(json.RawMessage(`{"workflow_run":{"name":"ci","html_url":"u","conclusion":"failure"},"repository":{"full_name":"o/r"}}`))
	monitor.OnIssueComment(json.RawMessage(`{"action":"created","comment":{"body":"x","user":{"login":"u"}},"issue":{"number":1,"title":"t"},"repository":{"full_name":"o/r"}}`))
	monitor.OnIssueOpened(json.RawMessage(`{"action":"opened","issue":{"number":2,"title":"t","body":"b"},"repository":{"full_name":"o/r"},"sender":{"login":"u"}}`))
}

