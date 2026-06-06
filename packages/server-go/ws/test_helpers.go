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

// testSendAndAssertErrorCode consolidates: send message → read response → assert error code.
// Requires an already-open connection and project.
// Fails test if response is not an error message or if code doesn't match.
func testSendAndAssertErrorCode(t *testing.T, conn *websocket.Conn, payload map[string]any, expectedCode string) {
	t.Helper()
	writeWSMessage(t, conn, payload)
	msg := readWSMessageOfType(t, conn, "error")
	assertErrorCode(t, msg, expectedCode)
}

// testSendAndReadMessage consolidates: send message → read response of expected type.
// Requires an already-open connection.
// Returns the full response message for caller inspection.
func testSendAndReadMessage(t *testing.T, conn *websocket.Conn, payload map[string]any, expectedResponseType string) map[string]any {
	t.Helper()
	writeWSMessage(t, conn, payload)
	return readWSMessageOfType(t, conn, expectedResponseType)
}

// testGitHubAPIWithMockHTTP consolidates the pattern: setup mock HTTP client, project, connection, send message, receive response.
// Caller provides HTTP status code, response body, message payload, and expected response type.
// Returns projectDir, connection, cleanup, and the response message.
func testGitHubAPIWithMockHTTP(t *testing.T, statusCode int, responseBody string, payload map[string]any, expectedResponseType string) (string, *websocket.Conn, func(), map[string]any) {
	t.Helper()
	original := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &fixedHTTPTransport{statusCode: statusCode, body: responseBody},
	}
	projectDir, conn, cleanup := setupWSProjectAndConnection(t)
	response := sendAndReceive(t, conn, payload, expectedResponseType)
	fullCleanup := func() {
		cleanup()
		wsHTTPClient = original
	}
	return projectDir, conn, fullCleanup, response
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

// readSessionID reads a session.created message from the websocket and returns the session ID.
// Extracts the session ID from the session object for convenience.
func readSessionID(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	sessionMsg := readWSMessageOfType(t, conn, "session.created")
	sess, _ := sessionMsg["session"].(map[string]any)
	sessionID, _ := sess["id"].(string)
	if sessionID == "" {
		t.Fatalf("expected session id in session.created, got %+v", sessionMsg)
	}
	return sessionID
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

// ─── Assertion helpers for cleaner test code ──────────────────────────────────

// assertErrorCode verifies that a message is an error with the expected error code.
// Fails the test with detailed message if code does not match.
func assertErrorCode(t *testing.T, msg map[string]any, expectedCode string) {
	t.Helper()
	if msg["type"] != "error" {
		t.Fatalf("expected error message, got type=%q in %+v", msg["type"], msg)
	}
	code, _ := msg["code"].(string)
	if code != expectedCode {
		t.Fatalf("expected error code %q, got %q in %+v", expectedCode, code, msg)
	}
}

// assertMessageType verifies that a message has the expected type.
// Fails the test with detailed message if type does not match.
func assertMessageType(t *testing.T, msg map[string]any, expectedType string) {
	t.Helper()
	if msg["type"] != expectedType {
		t.Fatalf("expected message type %q, got %q in %+v", expectedType, msg["type"], msg)
	}
}

// ─── Chat flow helpers ─────────────────────────────────────────────────────────

// readChatToolCall reads a chat.tool_call message and verifies the tool name matches expected.
// Returns the full tool call message for inspection of parameters.
func readChatToolCall(t *testing.T, conn *websocket.Conn, expectedName string) map[string]any {
	t.Helper()
	msg := readWSMessageOfType(t, conn, "chat.tool_call")
	if name, _ := msg["name"].(string); name != expectedName {
		t.Fatalf("expected tool call name %q, got %q in %+v", expectedName, name, msg)
	}
	return msg
}

// readChatToolResult reads a chat.tool_result message and verifies the tool name matches expected.
// Returns the full tool result message for inspection of result.
func readChatToolResult(t *testing.T, conn *websocket.Conn, expectedName string) map[string]any {
	t.Helper()
	msg := readWSMessageOfType(t, conn, "chat.tool_result")
	if name, _ := msg["name"].(string); name != expectedName {
		t.Fatalf("expected tool result name %q, got %q in %+v", expectedName, name, msg)
	}
	return msg
}

// readChatChunksFinal reads all chat.chunk messages until one with done=true is received.
// Returns all chunks read, including the final one with done=true.
func readChatChunksFinal(t *testing.T, conn *websocket.Conn) []map[string]any {
	t.Helper()
	var chunks []map[string]any
	for {
		chunk := readWSMessageOfType(t, conn, "chat.chunk")
		chunks = append(chunks, chunk)
		done, _ := chunk["done"].(bool)
		if done {
			break
		}
	}
	return chunks
}

// ─── Authentication and connection helpers ──────────────────────────────────

// testAuthConnectionRejectsWithoutToken verifies websocket rejects connections without valid auth token.
// Sets up project and attempts connection with empty token.
func testAuthConnectionRejectsWithoutToken(t *testing.T, tokenValue string) {
	t.Helper()
	projectDir := setupWSProject(t)
	t.Setenv("ENGINE_LOCAL_WS_TOKEN", tokenValue)

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

// ─── HTTP redirect transport for GitHub API mocking ─────────────────────────

// wsRedirectTransport redirects HTTP requests to a test server.
// Used to mock GitHub API responses in websocket handler tests.
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

// setupDiscordBridgeAndProjectForTest consolidates: setupDiscordBridgeScopedTest + setupWSProjectAndConnection.
// Caller provides bridge, and returns projectDir, connection, and cleanup function.
func setupDiscordBridgeAndProjectForTest(t *testing.T, bridge DiscordBridge) (string, *websocket.Conn, func()) {
	t.Helper()
	SetDiscordBridge(bridge)
	projectDir, conn, cleanup := setupWSProjectAndConnection(t)
	return projectDir, conn, func() {
		cleanup()
		SetDiscordBridge(nil)
	}
}

// setupWSProjectWithNullBridgeAndConnection opens a ws test connection with a nil discord bridge.
// This preserves legacy test call sites that verify DISCORD_UNAVAILABLE paths.
func setupWSProjectWithNullBridgeAndConnection(t *testing.T) (string, *websocket.Conn, func()) {
	t.Helper()
	SetDiscordBridge(nil)
	projectDir, conn, cleanup := setupWSProjectAndConnection(t)
	return projectDir, conn, func() {
		cleanup()
		SetDiscordBridge(nil)
	}
}

// setupGitHubHTTPMock creates a test HTTP server with the given response and redirects wsHTTPClient to it.
// Returns the server and cleanup function (which closes server and restores original client).
func setupGitHubHTTPMock(t *testing.T, statusCode int, body string) (*httptest.Server, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(body)) //nolint:errcheck
	}))
	original := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &wsRedirectTransport{target: server.URL, real: http.DefaultTransport},
	}
	return server, func() {
		wsHTTPClient = original
		server.Close()
	}
}

// ─── Hub and server setup helpers ──────────────────────────────────────────────

// setupWSHubAndServer creates a WebSocket hub and test HTTP server serving that hub.
// Returns the hub, server, and cleanup function that closes the server.
// Consolidates: NewHub + httptest.NewServer(hub.ServeWS) + defer close pattern.
func setupWSHubAndServer(t *testing.T, projectDir string) (*Hub, *httptest.Server, func()) {
	t.Helper()
	hub := NewHub(projectDir)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	return hub, server, func() {
		server.Close()
	}
}

// testWSConnectionError tests a connection pattern that should result in an HTTP error response.
// Sets up hub, server, dials without websocket upgrade headers, and verifies connection fails.
// Used for testing upgrade errors and protocol violations.
func testWSConnectionError(t *testing.T, projectDir string, testFunc func(*httptest.Server) error) {
	t.Helper()
	hub := NewHub(projectDir)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer server.Close()
	if err := testFunc(server); err != nil {
		t.Fatalf("connection error test: %v", err)
	}
}