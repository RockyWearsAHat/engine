package ws

import (
	"encoding/json"
	"errors"
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
	"github.com/engine/server/remote"
	"github.com/gorilla/websocket"
)

type capturedAIInvocation struct {
	projectPath string
	sessionID   string
	message     string
	openTabs    []ai.TabInfo
	provider    string
	model       string
	ollamaURL   string
}

func setupWSProject(t *testing.T) string {
	t.Helper()

	projectDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".github", "references"), 0755); err != nil {
		t.Fatalf("mkdir refs: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "PROJECT_GOAL.md"),
		[]byte("Engine should route chat messages into the AI pipeline reliably."),
		0644,
	); err != nil {
		t.Fatalf("write project goal: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, ".github", "references", "architecture.md"),
		[]byte("Chat messages should preserve open-tab context and runtime provider configuration."),
		0644,
	); err != nil {
		t.Fatalf("write architecture doc: %v", err)
	}

	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectDir); err != nil {
		t.Fatalf("db init: %v", err)
	}

	return projectDir
}

func openWSTestConnection(t *testing.T, projectDir string) (*websocket.Conn, func()) {
	t.Helper()
	return openWSTestConnectionWithToken(t, projectDir, "")
}

func openWSTestConnectionWithToken(t *testing.T, projectDir, token string) (*websocket.Conn, func()) {
	t.Helper()

	hub := NewHub(projectDir)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if token != "" {
		wsURL += "?token=" + url.QueryEscape(token)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial websocket: %v", err)
	}

	cleanup := func() {
		conn.Close() //nolint:errcheck
		server.Close()
	}
	return conn, cleanup
}

func TestServeWS_RejectsMissingTokenWhenLocalAuthEnabled(t *testing.T) {
	projectDir := setupWSProject(t)
	t.Setenv("ENGINE_LOCAL_WS_TOKEN", "desktop-secret")
	hub := NewHub(projectDir)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	_, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected websocket dial to fail without auth token")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized response, got %#v", response)
	}
}

func TestServeWS_AllowsTokenWhenLocalAuthEnabled(t *testing.T) {
	projectDir := setupWSProject(t)
	t.Setenv("ENGINE_LOCAL_WS_TOKEN", "desktop-secret")
	conn, cleanup := openWSTestConnectionWithToken(t, projectDir, "desktop-secret")
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	message := readWSMessageOfType(t, conn, "session.created")
	if message["type"] != "session.created" {
		t.Fatalf("expected authenticated websocket to load session, got %+v", message)
	}
}

func writeWSMessage(t *testing.T, conn *websocket.Conn, payload map[string]any) {
	t.Helper()
	if err := conn.WriteJSON(payload); err != nil {
		t.Fatalf("write websocket json: %v", err)
	}
}

func readWSMessageOfType(t *testing.T, conn *websocket.Conn, expectedType string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for time.Now().Before(deadline) {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}

		var message map[string]any
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("decode websocket message: %v", err)
		}
		if message["type"] == expectedType {
			return message
		}
	}

	t.Fatalf("timed out waiting for websocket message type %q", expectedType)
	return nil
}

func TestHandler_ChatMessage_InvokesAIRunnerWithTabsAndRuntimeConfig(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	originalRunAIChat := runAIChat
	defer func() { runAIChat = originalRunAIChat }()

	invocations := make(chan capturedAIInvocation, 1)
	runAIChat = func(ctx *ai.ChatContext, userMessage string) {
		tabs := ctx.GetOpenTabs()
		tabCopy := append([]ai.TabInfo(nil), tabs...)
		invocations <- capturedAIInvocation{
			projectPath: ctx.ProjectPath,
			sessionID:   ctx.SessionID,
			message:     userMessage,
			openTabs:    tabCopy,
			provider:    os.Getenv("ENGINE_MODEL_PROVIDER"),
			model:       os.Getenv("ENGINE_MODEL"),
			ollamaURL:   os.Getenv("OLLAMA_BASE_URL"),
		}
		ctx.OnToolCall("list_open_tabs", map[string]any{})
		ctx.OnToolResult("list_open_tabs", "[]", false)
		ctx.OnChunk("pong", false)
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "config.sync",
		"config": map[string]any{
			"modelProvider": "ollama",
			"ollamaBaseUrl": "http://127.0.0.1:11434",
			"model":         "gemma4:31b",
		},
	})
	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})

	sessionCreated := readWSMessageOfType(t, conn, "session.created")
	session, _ := sessionCreated["session"].(map[string]any)
	sessionID, _ := session["id"].(string)
	if sessionID == "" {
		t.Fatalf("expected session id in session.created message, got %+v", sessionCreated)
	}

	openTabPath := filepath.Join(projectDir, "PROJECT_GOAL.md")
	writeWSMessage(t, conn, map[string]any{
		"type": "editor.tabs.sync",
		"tabs": []map[string]any{
			{"path": openTabPath, "isActive": true, "isDirty": false},
		},
	})
	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": sessionID,
		"content":   "hello cave ai",
	})

	invocation := <-invocations
	if invocation.projectPath != projectDir {
		t.Fatalf("expected project path %q, got %q", projectDir, invocation.projectPath)
	}
	if invocation.sessionID != sessionID {
		t.Fatalf("expected session id %q, got %q", sessionID, invocation.sessionID)
	}
	if invocation.message != "hello cave ai" {
		t.Fatalf("expected forwarded message, got %q", invocation.message)
	}
	if invocation.provider != "ollama" || invocation.model != "gemma4:31b" || invocation.ollamaURL != "http://127.0.0.1:11434" {
		t.Fatalf("expected runtime config to reach AI boundary, got %+v", invocation)
	}
	if len(invocation.openTabs) != 1 || invocation.openTabs[0].Path != openTabPath || !invocation.openTabs[0].IsActive {
		t.Fatalf("expected open tab context to reach AI boundary, got %+v", invocation.openTabs)
	}
	started := readWSMessageOfType(t, conn, "chat.started")
	if started["sessionId"] != sessionID {
		t.Fatalf("expected chat.started for session %q, got %+v", sessionID, started)
	}

	if got := readWSMessageOfType(t, conn, "chat.tool_call"); got["name"] != "list_open_tabs" {
		t.Fatalf("expected tool call to flow back to websocket, got %+v", got)
	}
	if got := readWSMessageOfType(t, conn, "chat.tool_result"); got["name"] != "list_open_tabs" {
		t.Fatalf("expected tool result to flow back to websocket, got %+v", got)
	}
	finalChunk := readWSMessageOfType(t, conn, "chat.chunk")
	if done, _ := finalChunk["done"].(bool); !done {
		nextChunk := readWSMessageOfType(t, conn, "chat.chunk")
		if done, _ = nextChunk["done"].(bool); !done {
			t.Fatalf("expected final chat chunk with done=true, got %+v then %+v", finalChunk, nextChunk)
		}
	}
}

func TestHandler_ChatMessage_WithoutSession_ReturnsChatError(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": "missing-session",
		"content":   "hello?",
	})

	message := readWSMessageOfType(t, conn, "chat.error")
	errorText, _ := message["error"].(string)
	if !strings.Contains(errorText, "Session not found") {
		t.Fatalf("expected missing session chat error, got %+v", message)
	}
}

func TestHandler_ChatMessage_UsesPayloadSessionWhenConnectionStateWasLost(t *testing.T) {
	projectDir := setupWSProject(t)
	sessionID := "session-reconnected"
	if err := db.CreateSession(sessionID, projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	originalRunAIChat := runAIChat
	defer func() { runAIChat = originalRunAIChat }()

	invocations := make(chan capturedAIInvocation, 1)
	runAIChat = func(ctx *ai.ChatContext, userMessage string) {
		invocations <- capturedAIInvocation{
			projectPath: ctx.ProjectPath,
			sessionID:   ctx.SessionID,
			message:     userMessage,
		}
		ctx.OnChunk("still alive", false)
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": sessionID,
		"content":   "reconnect me",
	})

	invocation := <-invocations
	if invocation.sessionID != sessionID {
		t.Fatalf("expected payload session id %q, got %+v", sessionID, invocation)
	}
	if invocation.projectPath != projectDir {
		t.Fatalf("expected project path %q, got %+v", projectDir, invocation)
	}

	started := readWSMessageOfType(t, conn, "chat.started")
	if started["sessionId"] != sessionID {
		t.Fatalf("expected chat.started for recovered session, got %+v", started)
	}
}

func TestHandler_ChatMessage_CanWriteAndOpenFileThroughAITools(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	originalRunAIChat := runAIChat
	defer func() { runAIChat = originalRunAIChat }()

	targetPath := filepath.Join(projectDir, "generated", "engine-note.ts")
	runAIChat = func(ctx *ai.ChatContext, userMessage string) {
		ctx.OnToolCall("write_file", map[string]any{"path": targetPath})
		if result, isError := ai.ExecuteToolForTest("write_file", map[string]any{
			"path":    targetPath,
			"content": "export const engineNote = 'cave';\n",
		}, ctx); isError {
			ctx.OnToolResult("write_file", result, true)
			ctx.OnError(result)
			return
		} else {
			ctx.OnToolResult("write_file", result, false)
		}

		ctx.OnToolCall("open_file", map[string]any{"path": targetPath})
		if result, isError := ai.ExecuteToolForTest("open_file", map[string]any{"path": targetPath}, ctx); isError {
			ctx.OnToolResult("open_file", result, true)
			ctx.OnError(result)
			return
		} else {
			ctx.OnToolResult("open_file", result, false)
		}

		ctx.OnChunk("done", false)
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	sessionCreated := readWSMessageOfType(t, conn, "session.created")
	session, _ := sessionCreated["session"].(map[string]any)
	sessionID, _ := session["id"].(string)

	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": sessionID,
		"content":   "create and open a note",
	})

	readWSMessageOfType(t, conn, "chat.started")
	readWSMessageOfType(t, conn, "chat.tool_call")
	readWSMessageOfType(t, conn, "chat.tool_result")
	readWSMessageOfType(t, conn, "chat.tool_call")
	opened := readWSMessageOfType(t, conn, "editor.open")
	if opened["path"] != targetPath {
		t.Fatalf("expected editor.open for %q, got %+v", targetPath, opened)
	}
	readWSMessageOfType(t, conn, "chat.tool_result")

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("expected AI tool path to write file: %v", err)
	}
	if string(content) != "export const engineNote = 'cave';\n" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestHandler_RemotePairCodeGenerate_WhenNotEnabled_ReturnsError(t *testing.T) {
	SetPairingManager(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "remote.pair.code.generate"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "PAIRING_DISABLED" {
		t.Fatalf("expected PAIRING_DISABLED error, got %+v", msg)
	}
}

func TestHandler_RemotePairCodeGenerate_ReturnsCode(t *testing.T) {
	pm := remote.NewPairingManager()
	SetPairingManager(pm)
	defer SetPairingManager(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "remote.pair.code.generate"})
	msg := readWSMessageOfType(t, conn, "remote.pair.code")

	code, ok := msg["code"].(string)
	if !ok || len(code) != 6 {
		t.Fatalf("expected 6-digit code string, got %+v", msg)
	}
	expiresIn, ok := msg["expiresIn"].(float64)
	if !ok || expiresIn != 300 {
		t.Fatalf("expected expiresIn:300, got %+v", msg)
	}
}

// wsRedirectTransport redirects HTTP requests to a test server.
type wsRedirectTransport struct {
	target string
	real   http.RoundTripper
}

func (t *wsRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	u, _ := url.Parse(t.target + r.URL.Path)
	r2.URL = u
	r2.Host = u.Host
	return t.real.RoundTrip(r2)
}

func TestFetchGitHubUser_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	_, err := fetchGitHubUser()
	if err == nil {
		t.Error("expected error when GitHub token not set")
	}
}

func TestFetchGitHubUser_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "fake-token")
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: srv.URL, real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()
	_, err := fetchGitHubUser()
	if err == nil {
		t.Error("expected error on non-200 response")
	}
}

func TestFetchGitHubIssues_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "fake-token")
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: srv.URL, real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()
	_, err := fetchGitHubIssues("owner", "repo")
	if err == nil {
		t.Error("expected error on non-200 response")
	}
}

func TestFetchGitHubIssues_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json")) //nolint:errcheck
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "fake-token")
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: srv.URL, real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()
	_, err := fetchGitHubIssues("owner", "repo")
	if err == nil {
		t.Error("expected error on bad JSON")
	}
}

// ── additional coverage tests ─────────────────────────────────────────────────

// mockDiscordBridge implements DiscordBridge for tests.
type mockDiscordBridge struct {
	cfg discord.Config
}

func (m *mockDiscordBridge) CurrentConfig() discord.Config { return m.cfg }
func (m *mockDiscordBridge) Reload(_ discord.Config) error { return nil }
func (m *mockDiscordBridge) SearchHistory(_, _, _ string, _ int) ([]db.DiscordSearchHit, error) {
	return nil, nil
}
func (m *mockDiscordBridge) RecentHistory(_, _, _ string, _ int) ([]db.DiscordMessage, error) {
	return nil, nil
}
func (m *mockDiscordBridge) SendDMToOwner(_ string) error    { return nil }
func (m *mockDiscordBridge) NotifyProjectProgress(_, _ string) {}

// panicDiscordBridge panics on CurrentConfig, used to trigger the run() panic handler.
type panicDiscordBridge struct{}

func (p *panicDiscordBridge) CurrentConfig() discord.Config { panic("test panic for coverage") }
func (p *panicDiscordBridge) Reload(_ discord.Config) error { return nil }
func (p *panicDiscordBridge) SearchHistory(_, _, _ string, _ int) ([]db.DiscordSearchHit, error) {
	return nil, nil
}
func (p *panicDiscordBridge) RecentHistory(_, _, _ string, _ int) ([]db.DiscordMessage, error) {
	return nil, nil
}
func (p *panicDiscordBridge) SendDMToOwner(_ string) error    { return nil }
func (p *panicDiscordBridge) NotifyProjectProgress(_, _ string) {}

func TestServeWS_UpgradeError(t *testing.T) {
	projectDir := setupWSProject(t)
	hub := NewHub(projectDir)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer server.Close()
	// Plain HTTP GET → upgrader fails
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
}

func TestSend_MarshalError(t *testing.T) {
	c := &conn{done: make(chan struct{})}
	// chan cannot be JSON-marshaled → triggers log.Printf("ws marshal error")
	c.send(make(chan int))
}

func TestRun_PanicHandler(t *testing.T) {
	SetDiscordBridge(&panicDiscordBridge{})
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// discord.config.get will call discordBridge.CurrentConfig() → panics → recovered by run()
	conn.WriteJSON(map[string]any{"type": "discord.config.get"}) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
}

func TestHandler_ProjectOpen_NoPath(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_FileRead_Error(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "file.read", "path": "/nonexistent/xyz/file.txt"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR, got %+v", msg)
	}
}

func TestHandler_FileSave_Error(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "file.save", "path": "/dev/null/nope/file.txt", "content": "hi"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR, got %+v", msg)
	}
}

func TestHandler_FileCreate_Error(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "file.create", "path": "/dev/null/nope/file.txt"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR, got %+v", msg)
	}
}

func TestHandler_FolderCreate_Error(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "folder.create", "path": "/dev/null/nope"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR, got %+v", msg)
	}
}

func TestHandler_FileTree_Error(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "file.tree", "path": "/nonexistent/xyz/dir"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR, got %+v", msg)
	}
}

func TestHandler_GitStatus_NonGitDir(t *testing.T) {
	projectDir := setupWSProject(t) // not a git repo
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "git.status"})
	msg := readWSMessageOfType(t, conn, "git.status")
	if msg["status"] == nil {
		t.Fatalf("expected status field, got %+v", msg)
	}
}

func TestHandler_GitCommit_Error(t *testing.T) {
	projectDir := setupWSProject(t) // not a git repo
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "git.commit", "message": "test commit"})
	msg := readWSMessageOfType(t, conn, "git.commit.result")
	if ok, _ := msg["ok"].(bool); ok {
		t.Fatalf("expected ok:false for non-git dir, got %+v", msg)
	}
}

func TestHandler_FileSearch_UsesProjectPath(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Bad regex with no root → uses projectPath, returns error in search.results
	writeWSMessage(t, conn, map[string]any{"type": "file.search", "query": "(?P<bad"})
	msg := readWSMessageOfType(t, conn, "search.results")
	if msg["error"] == nil {
		t.Fatalf("expected error in search.results, got %+v", msg)
	}
}

func TestHandler_WorkspaceTasks_WithPath(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "workspace.tasks", "path": projectDir})
	msg := readWSMessageOfType(t, conn, "workspace.tasks")
	if msg["tasks"] == nil {
		t.Fatalf("expected tasks, got %+v", msg)
	}
}

func TestHandler_TerminalCreate_NoCwd(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "terminal.create"})
	msg := readWSMessageOfType(t, conn, "terminal.created")
	if cwd, _ := msg["cwd"].(string); cwd != projectDir {
		t.Fatalf("expected cwd=%s, got %v", projectDir, msg["cwd"])
	}
}

func TestHandler_TestSummaryGet_NoObserver(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "test.summary.get", "sessionId": "nosuch-session"})
	msg := readWSMessageOfType(t, conn, "test.summary")
	summary, _ := msg["summary"].(map[string]any)
	if ok, _ := summary["success"].(bool); !ok {
		t.Fatalf("expected success:true for empty observer, got %+v", summary)
	}
}

func TestFetchGitHubUser_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"login": 42}`)) //nolint:errcheck // number → decode error
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "fake-token")
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: srv.URL, real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()
	_, err := fetchGitHubUser()
	if err == nil {
		t.Error("expected decode error for wrong field type")
	}
}

func TestFetchGitHubIssues_TransportError(t *testing.T) {
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: "http://127.0.0.1:1", real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()
	_, err := fetchGitHubIssues("owner", "repo")
	if err == nil {
		t.Error("expected transport error")
	}
}

func TestFetchGitHubIssues_PRFiltered(t *testing.T) {
	// All results are PRs → filtered → nil → nil guard → []githubIssue{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"number":1,"title":"PR","pull_request":{"url":"https://github.com/pr/1"}}]`)) //nolint:errcheck
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "fake-token")
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: srv.URL, real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()
	issues, err := fetchGitHubIssues("owner", "repo")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if issues == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues (PRs filtered), got %d", len(issues))
	}
}

func TestHandler_Chat_CancelPrevious(t *testing.T) {
	projectDir := setupWSProject(t)

	blocked := make(chan struct{})
	unblock := make(chan struct{})
	originalRunAIChat := runAIChat
	runAIChat = func(ctx *ai.ChatContext, content string) {
		if content == "first" {
			close(blocked)
			<-ctx.Cancel // wait for cancel
		}
	}
	defer func() { runAIChat = originalRunAIChat }()

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Open project to get a session
	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	// Send first chat message (will block in runAIChat)
	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": "first"})
	readWSMessageOfType(t, conn, "chat.started")
	<-blocked // wait for runAIChat to start

	// Send second chat message → old() is called to cancel first
	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": "second"})
	readWSMessageOfType(t, conn, "chat.started")
	_ = unblock
}

func TestHandler_GithubIssues_ResolvesRepo(t *testing.T) {
	// When no override is configured and project is a git repo, resolves owner/repo from remote.
	// With non-git dir, the resolve fails and we get github.issues with error.
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{"type": "github.issues"})
	msg := readWSMessageOfType(t, conn, "github.issues")
	_ = msg
}

// ─── discord.validate with active bridge ──────────────────────────────────────

func TestHandler_DiscordValidate_WithBridgeNoOverride(t *testing.T) {
	SetDiscordBridge(&mockDiscordBridge{cfg: discord.Config{Enabled: true}})
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// No override → uses discordBridge.CurrentConfig() path (else if discordBridge != nil)
	writeWSMessage(t, conn, map[string]any{"type": "discord.validate"})
	msg := readWSMessageOfType(t, conn, "discord.validate.result")
	if msg["result"] == nil {
		t.Fatalf("expected result, got %+v", msg)
	}
}

func TestHandler_DiscordValidate_WithBridgeAndOverride(t *testing.T) {
	SetDiscordBridge(&mockDiscordBridge{cfg: discord.Config{Enabled: true}})
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Override + active bridge → base = discordBridge.CurrentConfig()
	writeWSMessage(t, conn, map[string]any{
		"type": "discord.validate",
		"config": map[string]any{
			"enabled": true,
			"token":   "override-token",
			"guildId": "override-guild",
		},
	})
	msg := readWSMessageOfType(t, conn, "discord.validate.result")
	if msg["result"] == nil {
		t.Fatalf("expected result, got %+v", msg)
	}
}

// ─── discord.config.set: nil bridge + enabled (NewService paths) ──────────────

func TestHandler_DiscordConfigSet_NilBridge_EnabledInitFails(t *testing.T) {
	SetDiscordBridge(nil)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)

	// Write invalid discord-state.json to ENGINE_STATE_DIR so NewService's loadState() fails.
	engineDir := os.Getenv("ENGINE_STATE_DIR")
	if engineDir == "" {
		engineDir = filepath.Join(projectDir, ".engine")
		if err := os.MkdirAll(engineDir, 0755); err != nil {
			t.Fatalf("mkdir .engine: %v", err)
		}
	}
	stateFile := filepath.Join(engineDir, "discord-state.json")
	if err := os.WriteFile(stateFile, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("write bad state: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"enabled": true,
			"token":   "fake-token",
			"guildId": "fake-guild",
		},
	})
	msg := drainWSUntilType(t, conn, "discord.config.saved")
	if msg["warning"] == nil {
		t.Fatalf("expected init-failed warning, got %+v", msg)
	}
}

func TestHandler_DiscordConfigSet_NilBridge_EnabledSuccess(t *testing.T) {
	SetDiscordBridge(nil)
	defer SetDiscordBridge(nil)

	// Inject no-op Start so the service "starts" without real Discord.
	orig := discordServiceStartFn
	discordServiceStartFn = func(s *discord.Service) error { return nil }
	defer func() { discordServiceStartFn = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"enabled": true,
			"token":   "fake-token",
			"guildId": "fake-guild",
		},
	})
	msg := drainWSUntilType(t, conn, "discord.config.saved")
	if active, _ := msg["active"].(bool); !active {
		t.Fatalf("expected active:true after successful start, got %+v", msg)
	}
}

// ─── github.issues partial override ──────────────────────────────────────────

func TestHandler_GitHubIssues_PartialOverride(t *testing.T) {
	// ENGINE_GITHUB_OWNER set but ENGINE_GITHUB_REPO empty → partial override error.
	t.Setenv("ENGINE_GITHUB_OWNER", "myorg")
	t.Setenv("ENGINE_GITHUB_REPO", "")

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.issues"})
	msg := readWSMessageOfType(t, conn, "github.issues")
	if msg["error"] == nil {
		t.Fatalf("expected error for partial override, got %+v", msg)
	}
}

func TestHandler_GitHubIssues_ResolvedFromGitRemote(t *testing.T) {
	// Init a git repo with a github remote so ResolveGitHubRepo succeeds.
	projectDir := setupWSProject(t)
	for _, args := range [][]string{
		{"init", projectDir},
		{"-C", projectDir, "remote", "add", "origin", "https://github.com/testowner/testrepo.git"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Mock HTTP to return empty issues list (avoid real GitHub call).
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{
		target: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte("[]")) //nolint:errcheck
		})).URL,
		real: http.DefaultTransport,
	}}
	defer func() { wsHTTPClient = orig }()

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.issues"})
	msg := readWSMessageOfType(t, conn, "github.issues")
	_ = msg
}

func TestHandler_DiscordConfigSet_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// config as string instead of object → json.Unmarshal error → BAD_PAYLOAD
	writeWSMessage(t, conn, map[string]any{"type": "discord.config.set", "config": "bad"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

// ─── fetchGitHubUser transport error ─────────────────────────────────────────

func TestFetchGitHubUser_TransportError(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fake-token")
	orig := wsHTTPClient
	wsHTTPClient = &http.Client{Transport: &wsRedirectTransport{target: "http://127.0.0.1:1", real: http.DefaultTransport}}
	defer func() { wsHTTPClient = orig }()

	_, err := fetchGitHubUser()
	if err == nil {
		t.Error("expected transport error")
	}
}

// ─── remote.pair.code.generate ────────────────────────────────────────────────

type errPairingManager struct{}

func (e *errPairingManager) GenerateCode() (string, error) {
	return "", errors.New("rng failure")
}

func TestHandler_RemotePairCodeGenerate_Error(t *testing.T) {
	localPairingManager = &errPairingManager{}
	defer func() { localPairingManager = nil }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "remote.pair.code.generate"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "PAIRING_ERROR" {
		t.Fatalf("expected PAIRING_ERROR, got %+v", msg)
	}
}

// ─── chat.stop no sessionId ───────────────────────────────────────────────────

func TestHandler_ChatStop_NoSessionID(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Send chat.stop with no sessionId → handler returns immediately (no response)
	writeWSMessage(t, conn, map[string]any{"type": "chat.stop"})
	// Follow up with a known-response message to verify the connection is still alive.
	writeWSMessage(t, conn, map[string]any{"type": "git.status"})
	readWSMessageOfType(t, conn, "git.status")
}

// ─── session.create / session.load / chat bad JSON ────────────────────────────

func TestHandler_SessionCreate_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// projectPath as number → json.Unmarshal error → BAD_PAYLOAD
	writeWSMessage(t, conn, map[string]any{"type": "session.create", "projectPath": 42})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_SessionLoad_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// sessionId as number → json.Unmarshal error → BAD_PAYLOAD
	writeWSMessage(t, conn, map[string]any{"type": "session.load", "sessionId": 42})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_Chat_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// sessionId as number → json.Unmarshal error → BAD_PAYLOAD
	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": 42})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

// ─── DB error injection paths ────────────────────────────────────────────────

func TestHandler_SessionList_DBError(t *testing.T) {
	orig := dbListSessions
	dbListSessions = func(string) ([]db.Session, error) { return nil, errors.New("db error") }
	defer func() { dbListSessions = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "session.list"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DB_ERROR" {
		t.Fatalf("expected DB_ERROR, got %+v", msg)
	}
}

func TestHandler_SessionCreate_DBError(t *testing.T) {
	origCreate := dbCreateSession
	dbCreateSession = func(string, string, string) error { return errors.New("db error") }
	defer func() { dbCreateSession = origCreate }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "session.create"})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DB_ERROR" {
		t.Fatalf("expected DB_ERROR, got %+v", msg)
	}
}

func TestHandler_SessionCreate_WithSummary(t *testing.T) {
	origSummary := aiBuildInitialSummary
	aiBuildInitialSummary = func(string) string { return "test summary" }
	defer func() { aiBuildInitialSummary = origSummary }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "session.create"})
	msg := readWSMessageOfType(t, conn, "session.created")
	if msg["session"] == nil {
		t.Fatalf("expected session, got %+v", msg)
	}
}

func TestHandler_SessionCreate_WorktreeUpdate(t *testing.T) {
	altPath := t.TempDir()
	origWT := aiEnsureSessionWorktree
	aiEnsureSessionWorktree = func(id, projectPath string) (string, error) { return altPath, nil }
	defer func() { aiEnsureSessionWorktree = origWT }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "session.create"})
	readWSMessageOfType(t, conn, "session.created")
}

func TestHandler_SessionCleanup_WorktreeError(t *testing.T) {
	origClean := aiCleanupSessionWorktreeDB
	aiCleanupSessionWorktreeDB = func(id, path string, merge bool) error { return errors.New("cleanup error") }
	defer func() { aiCleanupSessionWorktreeDB = origClean }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// First create a session to cleanup.
	writeWSMessage(t, conn, map[string]any{"type": "session.create"})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	writeWSMessage(t, conn, map[string]any{"type": "session.cleanup", "sessionId": sessionID})
	readWSMessageOfType(t, conn, "session.cleanup.started")
	// Give goroutine time to run and log the error.
	time.Sleep(100 * time.Millisecond)
}

// ─── chat goroutine panic recovery ───────────────────────────────────────────

func TestHandler_Chat_GoroutinePanic(t *testing.T) {
	orig := runAIChat
	runAIChat = func(ctx *ai.ChatContext, content string) {
		panic("test panic in chat goroutine")
	}
	defer func() { runAIChat = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": "panic me"})
	readWSMessageOfType(t, conn, "chat.started")
	// Panic recovery sends chat.error
	msg := readWSMessageOfType(t, conn, "chat.error")
	if msg["error"] == nil {
		t.Fatalf("expected error in chat.error, got %+v", msg)
	}
}

// ─── SendToClient with map[string]string ─────────────────────────────────────

func TestHandler_Chat_SendToClient_MapStringString(t *testing.T) {
	orig := runAIChat
	runAIChat = func(ctx *ai.ChatContext, content string) {
		ctx.SendToClient("custom.event", map[string]string{"key": "value"})
	}
	defer func() { runAIChat = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": "send map"})
	readWSMessageOfType(t, conn, "chat.started")
	msg := readWSMessageOfType(t, conn, "custom.event")
	if msg["key"] != "value" {
		t.Fatalf("expected key=value in custom.event, got %+v", msg)
	}
}

func TestHandler_Chat_OnError(t *testing.T) {
	orig := runAIChat
	runAIChat = func(ctx *ai.ChatContext, content string) {
		ctx.OnError("something went wrong")
	}
	defer func() { runAIChat = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": "error"})
	readWSMessageOfType(t, conn, "chat.started")
	msg := readWSMessageOfType(t, conn, "chat.error")
	if msg["error"] != "something went wrong" {
		t.Fatalf("expected error message, got %+v", msg)
	}
}

func TestHandler_Chat_OnSessionUpdated(t *testing.T) {
	orig := runAIChat
	runAIChat = func(ctx *ai.ChatContext, content string) {
		if ctx.OnSessionUpdated != nil {
			ctx.OnSessionUpdated(&db.Session{ID: ctx.SessionID})
		}
	}
	defer func() { runAIChat = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": "update"})
	readWSMessageOfType(t, conn, "chat.started")
	msg := readWSMessageOfType(t, conn, "session.updated")
	if msg["session"] == nil {
		t.Fatalf("expected session in session.updated, got %+v", msg)
	}
}

// ─── git.commit success ───────────────────────────────────────────────────────

func TestHandler_GitCommit_Success(t *testing.T) {
	projectDir := setupWSProject(t)

	// Init a git repo so commit succeeds.
	for _, args := range [][]string{
		{"init", projectDir},
		{"-C", projectDir, "config", "user.email", "test@test.com"},
		{"-C", projectDir, "config", "user.name", "Test User"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "git.commit", "message": "initial commit"})
	msg := readWSMessageOfType(t, conn, "git.commit.result")
	if ok, _ := msg["ok"].(bool); !ok {
		t.Fatalf("expected ok:true for git commit in real repo, got %+v", msg)
	}
}

// ─── project.open DB error ────────────────────────────────────────────────────

func TestHandler_ProjectOpen_DBError(t *testing.T) {
	origCreate := dbCreateSession
	dbCreateSession = func(string, string, string) error { return errors.New("db error") }
	defer func() { dbCreateSession = origCreate }()

	// Use a fresh project path with no existing sessions so session creation is attempted.
	projectDir := filepath.Join(t.TempDir(), "fresh-proj")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectDir); err != nil {
		t.Fatalf("db init: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DB_ERROR" {
		t.Fatalf("expected DB_ERROR, got %+v", msg)
	}
}

func TestHandler_ProjectOpen_WithSummary(t *testing.T) {
	origSummary := aiBuildInitialSummary
	aiBuildInitialSummary = func(string) string { return "test project summary" }
	defer func() { aiBuildInitialSummary = origSummary }()

	// Fresh project so it creates a new session.
	projectDir := filepath.Join(t.TempDir(), "fresh-proj2")
	if err := os.MkdirAll(filepath.Join(projectDir, ".github", "references"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectDir); err != nil {
		t.Fatalf("db init: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	msg := readWSMessageOfType(t, conn, "session.created")
	if msg["session"] == nil {
		t.Fatalf("expected session, got %+v", msg)
	}
}

func TestHandler_UsageDashboard_Get_SuccessAndFallbackProjectPath(t *testing.T) {
	projectDir := setupWSProject(t)
	if err := db.CreateSession("usage-session", projectDir, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := db.LogUsageEvent("usage-1", "usage-session", projectDir, "openai", "gpt-4o", 12, 8, 20, 0.002, 400); err != nil {
		t.Fatalf("LogUsageEvent: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Request without explicit projectPath to cover fallback-to-connection project path.
	writeWSMessage(t, conn, map[string]any{
		"type":       "usage.dashboard.get",
		"scope":      "",
		"projectPath": "",
	})

	msg := readWSMessageOfType(t, conn, "usage.dashboard")
	rawDashboard, ok := msg["dashboard"].(map[string]any)
	if !ok {
		t.Fatalf("expected dashboard payload, got %+v", msg)
	}
	rawTotals, ok := rawDashboard["totals"].(map[string]any)
	if !ok {
		t.Fatalf("expected totals payload, got %+v", rawDashboard)
	}
	if requests, _ := rawTotals["requests"].(float64); requests < 1 {
		t.Fatalf("expected at least one request, got %+v", rawTotals)
	}
}

func TestHandler_UsageDashboard_Get_ErrorResponses(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":       "usage.dashboard.get",
		"scope":      "project",
		"projectPath": 17,
	})
	badPayload := readWSMessageOfType(t, conn, "usage.dashboard")
	if badPayload["error"] != "Bad payload" {
		t.Fatalf("expected bad payload error, got %+v", badPayload)
	}

	writeWSMessage(t, conn, map[string]any{
		"type":       "usage.dashboard.get",
		"scope":      "invalid",
		"projectPath": projectDir,
	})
	badScope := readWSMessageOfType(t, conn, "usage.dashboard")
	if _, ok := badScope["error"].(string); !ok {
		t.Fatalf("expected usage scope error, got %+v", badScope)
	}
}

func TestHandler_RepoAddRemove_BadPayloadErrors(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "repo.add",
		"urlOrPath": 5,
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD for repo.add, got %+v", msg)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "repo.remove",
		"name": 12,
	})
	msg = readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD for repo.remove, got %+v", msg)
	}
}

