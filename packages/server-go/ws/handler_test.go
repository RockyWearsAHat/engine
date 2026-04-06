package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
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

	hub := NewHub(projectDir)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
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

func writeWSMessage(t *testing.T, conn *websocket.Conn, payload map[string]interface{}) {
	t.Helper()
	if err := conn.WriteJSON(payload); err != nil {
		t.Fatalf("write websocket json: %v", err)
	}
}

func readWSMessageOfType(t *testing.T, conn *websocket.Conn, expectedType string) map[string]interface{} {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}

		var message map[string]interface{}
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
		ctx.OnToolCall("list_open_tabs", map[string]interface{}{})
		ctx.OnToolResult("list_open_tabs", "[]", false)
		ctx.OnChunk("pong", false)
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]interface{}{
		"type": "config.sync",
		"config": map[string]interface{}{
			"modelProvider": "ollama",
			"ollamaBaseUrl": "http://127.0.0.1:11434",
			"model":         "gemma4:31b",
		},
	})
	writeWSMessage(t, conn, map[string]interface{}{
		"type": "project.open",
		"path": projectDir,
	})

	sessionCreated := readWSMessageOfType(t, conn, "session.created")
	session, _ := sessionCreated["session"].(map[string]interface{})
	sessionID, _ := session["id"].(string)
	if sessionID == "" {
		t.Fatalf("expected session id in session.created message, got %+v", sessionCreated)
	}

	openTabPath := filepath.Join(projectDir, "PROJECT_GOAL.md")
	writeWSMessage(t, conn, map[string]interface{}{
		"type": "editor.tabs.sync",
		"tabs": []map[string]interface{}{
			{"path": openTabPath, "isActive": true, "isDirty": false},
		},
	})
	writeWSMessage(t, conn, map[string]interface{}{
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

	writeWSMessage(t, conn, map[string]interface{}{
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

	writeWSMessage(t, conn, map[string]interface{}{
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
