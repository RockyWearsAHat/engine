package ws

// Test helpers for websocket handler testing.
// Consolidates shared setup, connection, and message patterns.

import (
	"encoding/json"
	"io"
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
	"github.com/engine/server/quality"
	"github.com/gorilla/websocket"
)

// setupWSProject creates a temporary project directory with required files for websocket testing.
// Returns the project directory path.
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

// openWSTestConnection opens a websocket connection to a test hub for the given project.
// Returns the connection and a cleanup function.
func openWSTestConnection(t *testing.T, projectDir string) (*websocket.Conn, func()) {
	t.Helper()
	return openWSTestConnectionWithToken(t, projectDir, "")
}

// openWSTestConnectionWithToken opens a websocket connection with an optional auth token.
// Returns the connection and a cleanup function.
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

// writeWSMessage sends a JSON message to the websocket connection.
func writeWSMessage(t *testing.T, conn *websocket.Conn, payload map[string]any) {
	t.Helper()
	if err := conn.WriteJSON(payload); err != nil {
		t.Fatalf("write websocket json: %v", err)
	}
}

// sendAndReceive writes a message and reads the first response of the expected type.
func sendAndReceive(t *testing.T, conn *websocket.Conn, payload map[string]any, expectedType string) map[string]any {
	t.Helper()
	writeWSMessage(t, conn, payload)
	return readWSMessageOfType(t, conn, expectedType)
}

// readWSMessageOfType reads messages from the websocket until one with the specified type is found.
// Uses a 5-second timeout; fails the test if timeout is reached.
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

// readWSMessageOfAnyType reads one message from the websocket and returns it, regardless of type.
// Uses a 2-second timeout.
func readWSMessageOfAnyType(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return msg
}

// drainWSUntilType reads messages from the websocket until one with the target type is found.
// Uses a 3-second overall timeout with 500ms per-message deadline.
func drainWSUntilType(t *testing.T, conn *websocket.Conn, targetType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			break
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg["type"] == targetType {
			return msg
		}
	}
	t.Fatalf("timed out waiting for %q", targetType)
	return nil
}
// ─── Discord bridge test mocks ───────────────────────────────────────────────

// testDiscordBridge provides a basic DiscordBridge implementation for testing.
type testDiscordBridge struct {
	cfg        discord.Config
	reloadErr  error
	searchHits []db.DiscordSearchHit
	searchErr  error
	recentRows []db.DiscordMessage
	recentErr  error
}

func (t *testDiscordBridge) CurrentConfig() discord.Config { return t.cfg }
func (t *testDiscordBridge) Reload(_ discord.Config) error { return t.reloadErr }
func (t *testDiscordBridge) SearchHistory(_, _, _ string, _ int) ([]db.DiscordSearchHit, error) {
	return t.searchHits, t.searchErr
}
func (t *testDiscordBridge) RecentHistory(_, _, _ string, _ int) ([]db.DiscordMessage, error) {
	return t.recentRows, t.recentErr
}
func (t *testDiscordBridge) SendDMToOwner(_ string) error      { return nil }
func (t *testDiscordBridge) NotifyProjectProgress(_, _ string) {}

// ─── HTTP transport test mocks ───────────────────────────────────────────────

// fixedHTTPTransport returns a fixed HTTP response for testing.
type fixedHTTPTransport struct {
	statusCode int
	body       string
}

func (t *fixedHTTPTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.statusCode,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     make(http.Header),
	}, nil
}

// ─── AI invocation capture helpers ──────────────────────────────────────────

// CapturedAIInvocation records AI chat invocation details for verification in tests.
type CapturedAIInvocation struct {
	ProjectPath string
	SessionID   string
	Message     string
	OpenTabs    []ai.TabInfo
	Provider    string
	Model       string
	OllamaURL   string
}

// setupAIChatCapture sets up runAIChat to capture invocations into the provided channel.
// Returns the original runAIChat function to restore in cleanup.
func setupAIChatCapture(invocations chan<- CapturedAIInvocation) func(*ai.ChatContext, string) {
	original := runAIChat
	runAIChat = func(ctx *ai.ChatContext, userMessage string) {
		tabs := ctx.GetOpenTabs()
		tabCopy := append([]ai.TabInfo(nil), tabs...)
		invocations <- CapturedAIInvocation{
			ProjectPath: ctx.ProjectPath,
			SessionID:   ctx.SessionID,
			Message:     userMessage,
			OpenTabs:    tabCopy,
			Provider:    os.Getenv("ENGINE_MODEL_PROVIDER"),
			Model:       os.Getenv("ENGINE_MODEL"),
			OllamaURL:   os.Getenv("OLLAMA_BASE_URL"),
		}
	}
	return original
}

// setupAIOrchestratorRunDefault sets up runAutonomousProject with interactive mode.
// Returns the original runAutonomousProject function to restore in cleanup.
func setupAIOrchestratorRunDefault() func(ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
	original := runAutonomousProject
	runAutonomousProject = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		if cfg.InteractiveChat != nil {
			cfg.InteractiveChat(cfg.Brief, cfg.Cancel)
		}
		return &ai.OrchestrationState{Conversational: true}, nil
	}
	return original
}

// setupAIChatFunc sets up runAIChat with a custom handler and returns the original.
func setupAIChatFunc(handler func(*ai.ChatContext, string)) func(*ai.ChatContext, string) {
	original := runAIChat
	runAIChat = handler
	return original
}

// setupAIChatAndOrchestratorScoped sets up both runAIChat and runAutonomousProject with provided handlers.
// Returns a cleanup function that restores the original functions when called.
// Designed for use with defer or t.Cleanup().
func setupAIChatAndOrchestratorScoped(chatHandler func(*ai.ChatContext, string), orchHandler func(ai.OrchestratorConfig) (*ai.OrchestrationState, error)) func() {
	originalChat := runAIChat
	originalOrch := runAutonomousProject
	runAIChat = chatHandler
	runAutonomousProject = orchHandler
	return func() {
		runAIChat = originalChat
		runAutonomousProject = originalOrch
	}
}

// ─── Consolidated test flow helpers ────────────────────────────────────────

// openProjectAndGetSession is a helper that consolidates the pattern:
// project.open → session.created. Returns projectDir, connection, cleanup, and sessionID.
func openProjectAndGetSession(t *testing.T, projectDir string) (string, *websocket.Conn, func(), string) {
	t.Helper()
	conn, cleanup := openWSTestConnection(t, projectDir)

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	return projectDir, conn, cleanup, sessionID
}

// testFileOperationError consolidates error-checking pattern for file operations.
// Sends a message with the given operation type and bad path, expects FILE_ERROR response.
func testFileOperationError(t *testing.T, conn *websocket.Conn, opType string) {
	t.Helper()
	badPath := "/dev/null/nope/file.txt"
	if opType == "folder.create" {
		badPath = "/dev/null/nope"
	}

	msg := map[string]any{"type": opType, "path": badPath}
	if opType == "file.save" {
		msg["content"] = "hi"
	}
	writeWSMessage(t, conn, msg)

	response := readWSMessageOfType(t, conn, "error")
	if response["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR for %s, got %+v", opType, response)
	}
}

// setupMockHTTPClient replaces wsHTTPClient with a fixed transport and returns cleanup function.
// Useful for mocking GitHub API responses.
func setupMockHTTPClient(t *testing.T, statusCode int, body string) func() {
	t.Helper()
	original := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &fixedHTTPTransport{statusCode: statusCode, body: body},
	}
	return func() { wsHTTPClient = original }
}

// setupChatFlowWithApproval sets up a chat flow that requests approval.
// Returns projectDir, connection, cleanup, and channels for result/error reporting.
// The approval request message will be read automatically.
func setupChatFlowWithApproval(t *testing.T, chatHandler func(*ai.ChatContext, string)) (string, *websocket.Conn, func(), <-chan bool, <-chan error) {
	t.Helper()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)

	allowedResult := make(chan bool, 1)
	errResult := make(chan error, 1)

	defer setupAIChatAndOrchestratorScoped(
		chatHandler,
		func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
			if cfg.InteractiveChat != nil {
				cfg.InteractiveChat(cfg.Brief, cfg.Cancel)
			}
			return &ai.OrchestrationState{Conversational: true}, nil
		},
	)()

	// Project setup
	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	// Trigger chat with approval request
	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": sessionID,
		"content":   "request approval",
	})
	readWSMessageOfType(t, conn, "chat.started")

	// Read the approval request
	approvalMsg := readWSMessageOfType(t, conn, "approval.request")
	req, _ := approvalMsg["request"].(map[string]any)
	_, _ = req["id"].(string) // approval ID exists but caller reads from msg directly

	return projectDir, conn, cleanup, allowedResult, errResult
}

// ─── Consolidated test flow helpers (DRY) ────────────────────────────────────

// setupWSProjectAndConnection consolidates the common pattern of creating a test project and opening a websocket connection.
// Returns projectDir, connection, and cleanup function.
func setupWSProjectAndConnection(t *testing.T) (string, *websocket.Conn, func()) {
	t.Helper()
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	return projectDir, conn, cleanup
}

// setupDiscordBridgeScopedTest sets up a Discord bridge with automatic cleanup via deferred restoration.
// Manages SetDiscordBridge state for the duration of the test.
// Returns a cleanup function that restores the original nil state.
func setupDiscordBridgeScopedTest(t *testing.T, bridge DiscordBridge) func() {
	t.Helper()
	SetDiscordBridge(bridge)
	return func() {
		SetDiscordBridge(nil)
	}
}

// testDiscordFeatureWithSetup consolidates the pattern: setup bridge, project, connection, send message, receive response.
// Caller provides the message payload and expected response type.
// Returns projectDir, connection, cleanup, and the response message.
func testDiscordFeatureWithSetup(t *testing.T, bridge DiscordBridge, payload map[string]any, expectedResponseType string) (string, *websocket.Conn, func(), map[string]any) {
	t.Helper()
	SetDiscordBridge(bridge)
	projectDir, conn, cleanup := setupWSProjectAndConnection(t)
	response := sendAndReceive(t, conn, payload, expectedResponseType)
	return projectDir, conn, cleanup, response
}

// testGitHubAPIWithMockHTTP consolidates the pattern: setup mock HTTP client, project, connection, send message, receive response.
// Caller provides HTTP status code, response body, message payload, and expected response type.
// Returns projectDir, connection, cleanup, and the response message.
func testGitHubAPIWithMockHTTP(t *testing.T, statusCode int, responseBody string, payload map[string]any, expectedResponseType string) (string, *websocket.Conn, func(), map[string]any) {
	t.Helper()
	httpCleanup := setupMockHTTPClient(t, statusCode, responseBody)
	projectDir, conn, cleanup := setupWSProjectAndConnection(t)
	response := sendAndReceive(t, conn, payload, expectedResponseType)
	fullCleanup := func() {
		cleanup()
		httpCleanup()
	}
	return projectDir, conn, fullCleanup, response
}

// testSimpleWSMessageFlow sends a message and reads a response of the expected type.
// Useful for tests that just need to exercise a simple request-response pattern.
func testSimpleWSMessageFlow(t *testing.T, payload map[string]any, expectedResponseType string) map[string]any {
	t.Helper()
	_, conn, cleanup := setupWSProjectAndConnection(t)
	defer cleanup()
	return sendAndReceive(t, conn, payload, expectedResponseType)
}

// setupQualityScanFunc sets up qualityScanWithProgressFn with a custom handler and returns cleanup.
// Handles the common pattern of: original := qualityScanWithProgressFn; qualityScanWithProgressFn = handler; defer restore
func setupQualityScanFunc(t *testing.T, handler func(string, int, quality.ProgressCallback) (quality.Report, error)) func() {
	t.Helper()
	original := qualityScanWithProgressFn
	qualityScanWithProgressFn = handler
	return func() { qualityScanWithProgressFn = original }
}

// readApprovalRequest reads an approval.request message from the websocket and returns the approval ID.
// Extracts the approval ID from the request object for convenience.
func readApprovalRequest(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	approvalMsg := readWSMessageOfType(t, conn, "approval.request")
	req, _ := approvalMsg["request"].(map[string]any)
	approvalID, _ := req["id"].(string)
	if approvalID == "" {
		t.Fatalf("expected approval id in request, got %+v", approvalMsg)
	}
	return approvalID
}

// setupAIChatAndOrchestratorWithDefaults is a convenience wrapper around setupAIChatAndOrchestratorScoped
// that pre-fills the orchestrator handler with default interactive mode if handler is nil.
// Returns cleanup function.
func setupAIChatAndOrchestratorWithDefaults(t *testing.T, chatHandler func(*ai.ChatContext, string)) func() {
	t.Helper()
	return setupAIChatAndOrchestratorScoped(
		chatHandler,
		func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
			if cfg.InteractiveChat != nil {
				cfg.InteractiveChat(cfg.Brief, cfg.Cancel)
			}
			return &ai.OrchestrationState{Conversational: true}, nil
		},
	)
}

// testProjectOpenAndChat consolidates the pattern: open project → read session → send chat → read chat.started.
// Returns projectDir, connection, cleanup, sessionID, and nothing else is expected to be read.
// Caller provides chatHandler for AI chat execution.
func testProjectOpenAndChat(t *testing.T, chatHandler func(*ai.ChatContext, string), content string) (string, *websocket.Conn, func(), string) {
	t.Helper()
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)

	defer setupAIChatAndOrchestratorWithDefaults(t, chatHandler)()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sessionID := sessionMsg["session"].(map[string]any)["id"].(string)

	writeWSMessage(t, conn, map[string]any{"type": "chat", "sessionId": sessionID, "content": content})
	readWSMessageOfType(t, conn, "chat.started")

	return projectDir, conn, cleanup, sessionID
}