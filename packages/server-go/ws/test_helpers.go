package ws

// Test helpers for websocket handler testing.
// Consolidates shared setup, connection, and message patterns.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/db"
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
