package ws

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/engine/server/discord"
	"github.com/engine/server/github"
	"github.com/engine/server/remote"
	"github.com/engine/server/workspace"
	"github.com/gorilla/websocket"
)

// ─── stub Discord bridge ──────────────────────────────────────────────────────

type stubDiscordBridge struct {
	cfg           discord.Config
	reloadErr     error
	searchHits    []db.DiscordSearchHit
	searchErr     error
	recentRows    []db.DiscordMessage
	recentErr     error
}

func (s *stubDiscordBridge) CurrentConfig() discord.Config { return s.cfg }
func (s *stubDiscordBridge) Reload(_ discord.Config) error { return s.reloadErr }
func (s *stubDiscordBridge) SearchHistory(_, _, _ string, _ int) ([]db.DiscordSearchHit, error) {
	return s.searchHits, s.searchErr
}
func (s *stubDiscordBridge) RecentHistory(_, _, _ string, _ int) ([]db.DiscordMessage, error) {
	return s.recentRows, s.recentErr
}
func (s *stubDiscordBridge) SendDMToOwner(_ string) error { return nil }

// ─── stub HTTP transport ──────────────────────────────────────────────────────

type fixedResponseTransport struct {
	statusCode int
	body       string
}

func (t *fixedResponseTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.statusCode,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     make(http.Header),
	}, nil
}

// ─── Discord config get ───────────────────────────────────────────────────────

func TestHandler_DiscordConfigGet_NilBridge(t *testing.T) {
	SetDiscordBridge(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "discord.config.get"})
	msg := readWSMessageOfType(t, conn, "discord.config")
	if msg["type"] != "discord.config" {
		t.Fatalf("expected discord.config, got %+v", msg)
	}
}

func TestHandler_DiscordConfigGet_WithBridge(t *testing.T) {
	stub := &stubDiscordBridge{cfg: discord.Config{Enabled: true, BotToken: "tok"}}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "discord.config.get"})
	msg := readWSMessageOfType(t, conn, "discord.config")
	if msg["active"] != true {
		t.Fatalf("expected active:true, got %+v", msg)
	}
}

// ─── Discord history search ───────────────────────────────────────────────────

func TestHandler_DiscordHistorySearch_NilBridge(t *testing.T) {
	SetDiscordBridge(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "discord.history.search",
		"projectPath": projectDir,
		"query":       "hello",
		"limit":       10,
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DISCORD_UNAVAILABLE" {
		t.Fatalf("expected DISCORD_UNAVAILABLE, got %+v", msg)
	}
}

func TestHandler_DiscordHistorySearch_WithBridge_Success(t *testing.T) {
	stub := &stubDiscordBridge{
		searchHits: []db.DiscordSearchHit{{DiscordMessage: db.DiscordMessage{ID: "m1", Content: "cave AI"}}},

	}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "discord.history.search",
		"projectPath": projectDir,
		"query":       "cave",
		"limit":       5,
	})
	msg := readWSMessageOfType(t, conn, "discord.history.results")
	hits, _ := msg["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %+v", msg)
	}
}

func TestHandler_DiscordHistorySearch_WithBridge_Error(t *testing.T) {
	stub := &stubDiscordBridge{searchErr: fmt.Errorf("db failure")}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":  "discord.history.search",
		"query": "x",
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DISCORD_SEARCH" {
		t.Fatalf("expected DISCORD_SEARCH error, got %+v", msg)
	}
}

// ─── Discord history recent ───────────────────────────────────────────────────

func TestHandler_DiscordHistoryRecent_NilBridge(t *testing.T) {
	SetDiscordBridge(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "discord.history.recent",
		"projectPath": projectDir,
		"threadId":    "tid",
		"limit":       10,
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DISCORD_UNAVAILABLE" {
		t.Fatalf("expected DISCORD_UNAVAILABLE, got %+v", msg)
	}
}

func TestHandler_DiscordHistoryRecent_WithBridge_Success(t *testing.T) {
	stub := &stubDiscordBridge{
		recentRows: []db.DiscordMessage{{ID: "r1", Content: "hello"}},
	}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "discord.history.recent",
		"projectPath": projectDir,
		"threadId":    "tid",
		"limit":       5,
	})
	msg := readWSMessageOfType(t, conn, "discord.history.recent")
	rows, _ := msg["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", msg)
	}
}

func TestHandler_DiscordHistoryRecent_WithBridge_Error(t *testing.T) {
	stub := &stubDiscordBridge{recentErr: fmt.Errorf("db err")}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":     "discord.history.recent",
		"threadId": "tid",
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "DISCORD_RECENT" {
		t.Fatalf("expected DISCORD_RECENT error, got %+v", msg)
	}
}

// ─── Discord config set ───────────────────────────────────────────────────────

func TestHandler_DiscordConfigSet_NilBridgeDisabledConfig(t *testing.T) {
	SetDiscordBridge(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"enabled": false,
			"token":   "",
		},
	})
	msg := readWSMessageOfType(t, conn, "discord.config.saved")
	if msg["type"] != "discord.config.saved" {
		t.Fatalf("expected discord.config.saved, got %+v", msg)
	}
}

func TestHandler_DiscordConfigSet_WithBridge_ReloadError(t *testing.T) {
	stub := &stubDiscordBridge{reloadErr: fmt.Errorf("reload fail")}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"enabled": true,
			"token":   "tok",
		},
	})
	msg := readWSMessageOfType(t, conn, "discord.config.saved")
	if msg["warning"] == nil {
		t.Fatalf("expected warning on reload error, got %+v", msg)
	}
}

func TestHandler_DiscordConfigSet_WithBridge_ReloadOK(t *testing.T) {
	stub := &stubDiscordBridge{cfg: discord.Config{Enabled: true}}
	SetDiscordBridge(stub)
	defer SetDiscordBridge(nil)

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"enabled": true,
			"token":   "tok",
		},
	})
	msg := readWSMessageOfType(t, conn, "discord.config.saved")
	if msg["warning"] != nil {
		t.Fatalf("unexpected warning: %+v", msg)
	}
}

// ─── GitHub user ──────────────────────────────────────────────────────────────

func TestHandler_GitHubUser_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ENGINE_GITHUB_TOKEN", "")
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.user"})
	msg := readWSMessageOfType(t, conn, "github.user")
	if msg["error"] == nil {
		t.Fatalf("expected error in github.user response, got %+v", msg)
	}
}

func TestHandler_GitHubUser_MockHTTP_Success(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	origClient := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &fixedResponseTransport{
			statusCode: 200,
			body:       `{"login":"caveman","name":"Cave Man","avatar_url":"http://example.com/av"}`,
		},
	}
	defer func() { wsHTTPClient = origClient }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.user"})
	msg := readWSMessageOfType(t, conn, "github.user")
	user, _ := msg["user"].(map[string]any)
	if user["login"] != "caveman" {
		t.Fatalf("expected login caveman, got %+v", msg)
	}
}

func TestHandler_GitHubUser_MockHTTP_APIError(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	origClient := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &fixedResponseTransport{statusCode: 401, body: `{"message":"Bad credentials"}`},
	}
	defer func() { wsHTTPClient = origClient }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.user"})
	msg := readWSMessageOfType(t, conn, "github.user")
	if msg["error"] == nil {
		t.Fatalf("expected error on 401, got %+v", msg)
	}
}

// ─── GitHub issues ────────────────────────────────────────────────────────────

func TestHandler_GitHubIssues_EnvOverridePartial(t *testing.T) {
	// Simulate config where override is active but owner/repo are blank.
	// githubRepoOverride checks ENGINE_GITHUB_OWNER / ENGINE_GITHUB_REPO.
	t.Setenv("ENGINE_GITHUB_OWNER", "")
	t.Setenv("ENGINE_GITHUB_REPO", "")

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "github.issues",
		"projectPath": "/nonexistent/path",
	})
	// This path hits "no GitHub remote or configured repository" since the dir
	// isn't a git repo and no env override is fully configured.
	msg := readWSMessageOfType(t, conn, "github.issues")
	if msg["error"] == nil {
		t.Fatalf("expected error in github.issues response, got %+v", msg)
	}
}

func TestHandler_GitHubIssues_MockHTTP_Success(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_OWNER", "cave")
	t.Setenv("ENGINE_GITHUB_REPO", "engine")
	t.Setenv("GITHUB_TOKEN", "tok")

	origClient := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &fixedResponseTransport{
			statusCode: 200,
			body: `[{"number":1,"title":"bug","body":"desc","html_url":"http://gh.com/1","state":"open","user":{"login":"dev"},"labels":[],"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","pull_request":null}]`,
		},
	}
	defer func() { wsHTTPClient = origClient }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "github.issues",
		"projectPath": projectDir,
	})
	msg := readWSMessageOfType(t, conn, "github.issues")
	issues, _ := msg["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %+v", msg)
	}
}

func TestHandler_GitHubIssues_MockHTTP_APIError(t *testing.T) {
	t.Setenv("ENGINE_GITHUB_OWNER", "cave")
	t.Setenv("ENGINE_GITHUB_REPO", "engine")

	origClient := wsHTTPClient
	wsHTTPClient = &http.Client{
		Transport: &fixedResponseTransport{statusCode: 403, body: `{"message":"Forbidden"}`},
	}
	defer func() { wsHTTPClient = origClient }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.issues"})
	msg := readWSMessageOfType(t, conn, "github.issues")
	if msg["error"] == nil {
		t.Fatalf("expected error on 403, got %+v", msg)
	}
}

// ─── approval.respond dispatch ────────────────────────────────────────────────

func TestHandler_ApprovalRespond_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Send valid JSON but with id as a non-string to trigger inner unmarshal error.
	if err := conn.WriteMessage(1, []byte(`{"type":"approval.respond","id":42,"allow":true}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_ApprovalRespond_MissingID(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":  "approval.respond",
		"id":    "",
		"allow": true,
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_ApprovalRespond_UnknownID_NoOp(t *testing.T) {
	// resolveApproval with unknown ID is a no-op — no response sent, no crash.
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Send approval.respond with an unknown ID — then send another message
	// to confirm the connection is still alive.
	writeWSMessage(t, conn, map[string]any{
		"type":  "approval.respond",
		"id":    "nonexistent-id",
		"allow": true,
	})
	// Send discord.config.get (quick no-bridge response) to verify connection alive.
	SetDiscordBridge(nil)
	writeWSMessage(t, conn, map[string]any{"type": "discord.config.get"})
	msg := readWSMessageOfType(t, conn, "discord.config")
	if msg["type"] != "discord.config" {
		t.Fatalf("connection dead after unknown approval.respond, got %+v", msg)
	}
}

// ─── requestApproval: timeout path ────────────────────────────────────────────

func TestHandler_RequestApproval_Timeout(t *testing.T) {
	// Set a very short approval timeout so we don't wait 5 minutes.
	origTimeout := approvalTimeout
	approvalTimeout = 20 * time.Millisecond
	defer func() { approvalTimeout = origTimeout }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()

	runAIChat = func(ctx *ai.ChatContext, _ string) {
		allowed, err := ctx.RequestApproval("shell", "Run command", "execute ls", "ls -la")
		if err == nil || allowed {
			ctx.OnError("expected timeout error, got: allowed=" + fmt.Sprintf("%v", allowed))
			return
		}
		ctx.OnChunk("timed-out", false)
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")
	// Don't send session ID — let it resolve to the current session.
	writeWSMessage(t, conn, map[string]any{
		"type":    "chat",
		"content": "run command",
	})
	readWSMessageOfType(t, conn, "chat.started")
	// The approval request is sent, we don't respond → timeout fires.
	// After timeout the mock sends chunk "timed-out".
	msg := readWSMessageOfType(t, conn, "chat.chunk")
	if msg["content"] != "timed-out" {
		t.Fatalf("expected timed-out chunk after approval timeout, got %+v", msg)
	}
}

// ─── requestApproval: allow path ─────────────────────────────────────────────

func TestHandler_RequestApproval_Allow(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()

	runAIChat = func(ctx *ai.ChatContext, _ string) {
		allowed, err := ctx.RequestApproval("shell", "Run command", "exec ls", "ls")
		if err != nil {
			ctx.OnError("approval error: " + err.Error())
			return
		}
		if allowed {
			ctx.OnChunk("approved", false)
		} else {
			ctx.OnChunk("denied", false)
		}
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type":    "chat",
		"content": "approve me",
	})
	readWSMessageOfType(t, conn, "chat.started")

	// Read approval.request message.
	approvalMsg := readWSMessageOfType(t, conn, "approval.request")
	req, _ := approvalMsg["request"].(map[string]any)
	approvalID, _ := req["id"].(string)
	if approvalID == "" {
		t.Fatalf("expected approval id in request, got %+v", approvalMsg)
	}

	// Respond with allow=true.
	writeWSMessage(t, conn, map[string]any{
		"type":  "approval.respond",
		"id":    approvalID,
		"allow": true,
	})

	chunk := readWSMessageOfType(t, conn, "chat.chunk")
	if chunk["content"] != "approved" {
		t.Fatalf("expected approved chunk, got %+v", chunk)
	}
}

func TestHandler_RequestApproval_Deny(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()

	runAIChat = func(ctx *ai.ChatContext, _ string) {
		allowed, err := ctx.RequestApproval("shell", "Run", "exec", "rm")
		if err != nil {
			ctx.OnError(err.Error())
			return
		}
		if allowed {
			ctx.OnChunk("approved", false)
		} else {
			ctx.OnChunk("denied", false)
		}
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type":    "chat",
		"content": "deny me",
	})
	readWSMessageOfType(t, conn, "chat.started")

	approvalMsg := readWSMessageOfType(t, conn, "approval.request")
	req, _ := approvalMsg["request"].(map[string]any)
	approvalID, _ := req["id"].(string)

	writeWSMessage(t, conn, map[string]any{
		"type":  "approval.respond",
		"id":    approvalID,
		"allow": false,
	})

	chunk := readWSMessageOfType(t, conn, "chat.chunk")
	if chunk["content"] != "denied" {
		t.Fatalf("expected denied chunk, got %+v", chunk)
	}
}

// ─── session.list / session.create / session.load ─────────────────────────────

func TestHandler_SessionList(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{"type": "session.list"})
	msg := readWSMessageOfType(t, conn, "session.list")
	sessions, _ := msg["sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatalf("expected at least one session in list, got %+v", msg)
	}
}

func TestHandler_SessionCreate(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "session.create",
		"projectPath": projectDir,
	})
	msg := readWSMessageOfType(t, conn, "session.created")
	sess, _ := msg["session"].(map[string]any)
	if sess["id"] == nil {
		t.Fatalf("expected session id in session.created, got %+v", msg)
	}
}

func TestHandler_SessionLoad(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// First open a project to create a session.
	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	created := readWSMessageOfType(t, conn, "session.created")
	sess, _ := created["session"].(map[string]any)
	sessionID, _ := sess["id"].(string)
	if sessionID == "" {
		t.Fatalf("no session id from project.open: %+v", created)
	}

	// Now request session.load with the same ID.
	writeWSMessage(t, conn, map[string]any{
		"type":      "session.load",
		"sessionId": sessionID,
	})
	msg := readWSMessageOfType(t, conn, "session.loaded")
	loadedSess, _ := msg["session"].(map[string]any)
	if loadedSess["id"] != sessionID {
		t.Fatalf("expected session %q, got %+v", sessionID, msg)
	}
}

// ─── file.read ─────────────────────────────────────────────────────────────────

func TestHandler_FileRead_Success(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	filePath := projectDir + "/PROJECT_GOAL.md"
	writeWSMessage(t, conn, map[string]any{
		"type": "file.read",
		"path": filePath,
	})
	msg := readWSMessageOfType(t, conn, "file.content")
	if msg["path"] != filePath {
		t.Fatalf("expected path %q, got %+v", filePath, msg)
	}
}

func TestHandler_FileRead_NotFound(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "file.read",
		"path": "/nonexistent/cave.txt",
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "FILE_ERROR" {
		t.Fatalf("expected FILE_ERROR, got %+v", msg)
	}
}

// ─── file.save / file.create / folder.create ─────────────────────────────────

func TestHandler_FileSave(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	filePath := projectDir + "/save-test.txt"
	writeWSMessage(t, conn, map[string]any{
		"type":    "file.save",
		"path":    filePath,
		"content": "cave content",
	})
	msg := readWSMessageOfType(t, conn, "file.saved")
	if msg["path"] != filePath {
		t.Fatalf("expected file.saved with path, got %+v", msg)
	}
}

func TestHandler_FileCreate(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	filePath := projectDir + "/create-test.txt"
	writeWSMessage(t, conn, map[string]any{
		"type": "file.create",
		"path": filePath,
	})
	msg := readWSMessageOfType(t, conn, "file.created")
	if msg["path"] != filePath {
		t.Fatalf("expected file.created with path, got %+v", msg)
	}
}

func TestHandler_FolderCreate(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	folderPath := projectDir + "/new-folder"
	writeWSMessage(t, conn, map[string]any{
		"type": "folder.create",
		"path": folderPath,
	})
	msg := readWSMessageOfType(t, conn, "folder.created")
	if msg["path"] != folderPath {
		t.Fatalf("expected folder.created with path, got %+v", msg)
	}
}

// ─── file.tree ─────────────────────────────────────────────────────────────────

func TestHandler_FileTree(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "file.tree",
		"path": projectDir,
	})
	msg := readWSMessageOfType(t, conn, "file.tree")
	if msg["tree"] == nil {
		t.Fatalf("expected file.tree response with tree, got %+v", msg)
	}
}

// ─── engine.config.get ────────────────────────────────────────────────────────

func TestHandler_EngineConfigGet_NoFile(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "engine.config.get",
	})
	msg := readWSMessageOfType(t, conn, "engine.config")
	if msg["error"] == nil {
		t.Fatalf("expected error when no config file, got %+v", msg)
	}
}

// ─── test.observe / test.summary.get ─────────────────────────────────────────

func TestHandler_TestObserve_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Send valid JSON but with sessionId as a non-string to trigger inner unmarshal error.
	if err := conn.WriteMessage(1, []byte(`{"type":"test.observe","sessionId":42,"line":"x"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_TestObserve_MissingSessionID(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "test.observe",
		"sessionId": "",
		"line":      "go test ok",
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD for empty sessionId, got %+v", msg)
	}
}

func TestHandler_TestObserve_Valid(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Observe 20 lines to force a summary to be sent.
	for i := range 21 {
		writeWSMessage(t, conn, map[string]any{
			"type":      "test.observe",
			"sessionId": "sess-obs",
			"line":      fmt.Sprintf("line %d", i),
		})
	}
	msg := readWSMessageOfType(t, conn, "test.summary")
	if msg["sessionId"] != "sess-obs" {
		t.Fatalf("expected test.summary for sess-obs, got %+v", msg)
	}
}

func TestHandler_TestSummaryGet_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Send valid JSON but with sessionId as a non-string.
	if err := conn.WriteMessage(1, []byte(`{"type":"test.summary.get","sessionId":42}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

func TestHandler_TestSummaryGet_Valid(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// First observe some lines.
	writeWSMessage(t, conn, map[string]any{
		"type":      "test.observe",
		"sessionId": "sess-sum",
		"line":      "some output",
	})
	// Then explicitly request summary.
	writeWSMessage(t, conn, map[string]any{
		"type":      "test.summary.get",
		"sessionId": "sess-sum",
	})
	msg := readWSMessageOfType(t, conn, "test.summary")
	if msg["sessionId"] != "sess-sum" {
		t.Fatalf("expected test.summary for sess-sum, got %+v", msg)
	}
}

func TestHandler_TestSummaryGet_MissingSessionID(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "test.summary.get",
		"sessionId": "",
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "BAD_PAYLOAD" {
		t.Fatalf("expected BAD_PAYLOAD, got %+v", msg)
	}
}

// ─── resolveAllApprovals ──────────────────────────────────────────────────────

func TestHandler_ResolveAllApprovals_OnConnectionClose(t *testing.T) {
	origTimeout := approvalTimeout
	approvalTimeout = 30 * time.Second
	defer func() { approvalTimeout = origTimeout }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()

	resolved := make(chan bool, 1)
	runAIChat = func(ctx *ai.ChatContext, _ string) {
		allowed, _ := ctx.RequestApproval("shell", "T", "m", "c")
		resolved <- allowed
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	created := readWSMessageOfType(t, conn, "session.created")
	sess, _ := created["session"].(map[string]any)
	sessionID, _ := sess["id"].(string)

	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": sessionID,
		"content":   "stop me",
	})
	readWSMessageOfType(t, conn, "chat.started")
	readWSMessageOfType(t, conn, "approval.request")

	// Close the connection — this triggers resolveAllApprovals(false) in run().
	cleanup()

	select {
	case allow := <-resolved:
		if allow {
			t.Fatal("expected allow=false when connection closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval to be resolved on connection close")
	}
}

// readAnyWSMessage reads the next raw message without filtering by type.
func readAnyWSMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("decode websocket message: %v", err)
	}
	return msg
}

// ─── discord.validate ─────────────────────────────────────────────────────────

func TestHandler_DiscordValidate_NilBridgeNoOverride(t *testing.T) {
	SetDiscordBridge(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "discord.validate"})
	msg := readWSMessageOfType(t, conn, "discord.validate.result")
	if msg["result"] == nil {
		t.Fatalf("expected result in discord.validate.result, got %+v", msg)
	}
}

func TestHandler_DiscordValidate_WithOverride(t *testing.T) {
	SetDiscordBridge(nil)
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.validate",
		"config": map[string]any{
			"enabled": true,
			"token":   "test-token",
			"guildId": "gid",
		},
	})
	msg := readWSMessageOfType(t, conn, "discord.validate.result")
	if msg["result"] == nil {
		t.Fatalf("expected result, got %+v", msg)
	}
}

// ─── git.status / git.diff / git.log ─────────────────────────────────────────

func TestHandler_GitStatus(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "git.status"})
	readWSMessageOfType(t, conn, "git.status")
}

func TestHandler_GitDiff(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "git.diff", "path": ""})
	readWSMessageOfType(t, conn, "git.diff")
}

func TestHandler_GitLog(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "git.log"})
	readWSMessageOfType(t, conn, "git.log")
}

// ─── git.commit ───────────────────────────────────────────────────────────────

func TestHandler_GitCommit_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "git.commit", "message": 42})
	msg := readWSMessageOfType(t, conn, "git.commit.result")
	if msg["ok"] != false {
		t.Errorf("expected ok=false, got %v", msg["ok"])
	}
}

func TestHandler_GitCommit_EmptyMessage(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "git.commit", "message": ""})
	msg := readWSMessageOfType(t, conn, "git.commit.result")
	if msg["ok"] != false {
		t.Errorf("expected ok=false for empty message, got %v", msg["ok"])
	}
}

// ─── workspace.tasks ─────────────────────────────────────────────────────────

func TestHandler_WorkspaceTasks(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "workspace.tasks"})
	readWSMessageOfType(t, conn, "workspace.tasks")
}

// ─── config.sync ─────────────────────────────────────────────────────────────

func TestHandler_ConfigSync_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	// JSON with wrong type for config field triggers unmarshal error.
	writeWSMessage(t, conn, map[string]any{"type": "config.sync", "config": "not-an-object"})
	readWSMessageOfType(t, conn, "error")
}

func TestHandler_ConfigSync_Success(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	// config.sync has no response on success; just verify it doesn't panic.
	writeWSMessage(t, conn, map[string]any{
		"type":   "config.sync",
		"config": map[string]any{},
	})
	// Send a known-response message to flush the connection.
	writeWSMessage(t, conn, map[string]any{"type": "session.list"})
	readWSMessageOfType(t, conn, "session.list")
}

// ─── session.cleanup ─────────────────────────────────────────────────────────

func TestHandler_SessionCleanup_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "session.cleanup", "sessionId": 42})
	readWSMessageOfType(t, conn, "error")
}

func TestHandler_SessionCleanup_MissingSessionId(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "session.cleanup", "sessionId": ""})
	readWSMessageOfType(t, conn, "error")
}

func TestHandler_SessionCleanup_NotFound(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "session.cleanup", "sessionId": "nonexistent-session-id"})
	readWSMessageOfType(t, conn, "error")
}

// ─── file.search ─────────────────────────────────────────────────────────────

func TestHandler_FileSearch_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "file.search", "query": 42})
	readWSMessageOfType(t, conn, "search.results")
}

func TestHandler_FileSearch_EmptyQuery(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "file.search", "query": ""})
	readWSMessageOfType(t, conn, "search.results")
}

func TestHandler_FileSearch_WithQuery(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{
		"type":  "file.search",
		"query": "Engine",
		"root":  projectDir,
	})
	readWSMessageOfType(t, conn, "search.results")
}

// ─── engine.team.set ─────────────────────────────────────────────────────────

func TestHandler_EngineTeamSet_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "engine.team.set", "team": 42})
	readWSMessageOfType(t, conn, "error")
}

func TestHandler_EngineTeamSet_Success(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{
		"type":     "engine.team.set",
		"team":     "fast",
		"provider": "ollama",
		"model":    "llama3",
	})
	readWSMessageOfType(t, conn, "engine.team.updated")
}

func TestHandler_EngineTeamSet_ResolveFromConfigWhenProviderAndModelMissing(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	t.Setenv("ENGINE_MODEL_PROVIDER", "")
	t.Setenv("ENGINE_MODEL", "")

	engineDir := projectDir + "/.engine"
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configYAML := "teams:\n  fast:\n    orchestrator:\n      model: \"openai:gpt-4o-mini\"\n"
	if err := os.WriteFile(engineDir+"/config.yaml", []byte(configYAML), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type": "engine.team.set",
		"team": "fast",
	})
	readWSMessageOfType(t, conn, "engine.team.updated")

	if got := os.Getenv("ENGINE_MODEL_PROVIDER"); got != "openai" {
		t.Fatalf("expected ENGINE_MODEL_PROVIDER openai, got %q", got)
	}
	if got := os.Getenv("ENGINE_MODEL"); got != "gpt-4o-mini" {
		t.Fatalf("expected ENGINE_MODEL gpt-4o-mini, got %q", got)
	}
}

// ─── editor.tabs.sync ────────────────────────────────────────────────────────

func TestHandler_EditorTabsSync(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{
		"type": "editor.tabs.sync",
		"tabs": []map[string]any{{"path": "src/main.go", "active": true}},
	})
	// No response expected; flush with session.list.
	writeWSMessage(t, conn, map[string]any{"type": "session.list"})
	readWSMessageOfType(t, conn, "session.list")
}

// ─── engine.config.get with file ─────────────────────────────────────────────

func TestHandler_EngineConfigGet_WithFile(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Write a config file.
	engineDir := projectDir + "/.engine"
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(engineDir+"/config.yaml", []byte("model: gpt-4\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Open project so c.projectPath is set.
	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{"type": "engine.config.get"})
	msg := readWSMessageOfType(t, conn, "engine.config")
	if msg["yaml"] == "" || msg["yaml"] == nil {
		t.Errorf("expected yaml content, got %+v", msg)
	}
}

// ─── file.save bad payload ────────────────────────────────────────────────────

func TestHandler_FileSave_BadPayload(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	// "content" must be a string; send integer to trigger unmarshal error.
	writeWSMessage(t, conn, map[string]any{"type": "file.save", "path": "file.txt", "content": 42})
	readWSMessageOfType(t, conn, "error")
}

// ─── default / unknown message type ──────────────────────────────────────────

func TestHandler_UnknownMessageType(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()
	writeWSMessage(t, conn, map[string]any{"type": "totally.unknown.message.type.xyz"})
	readWSMessageOfType(t, conn, "error")
}

// ─── project.open loading existing session ────────────────────────────────────

func TestHandler_ProjectOpen_LoadsExistingSession(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// First open — creates session.
	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	readWSMessageOfType(t, conn, "session.created")

	// Open a new WS connection to the same project — should load existing session.
	conn2, cleanup2 := openWSTestConnection(t, projectDir)
	defer cleanup2()
	writeWSMessage(t, conn2, map[string]any{"type": "project.open", "path": projectDir})
	readWSMessageOfType(t, conn2, "session.loaded")
}

func TestHandler_Chat_NoActiveSession_ReturnsError(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":    "chat",
		"content": "hello without session",
	})
	msg := readWSMessageOfType(t, conn, "chat.error")
	errText, _ := msg["error"].(string)
	if !strings.Contains(errText, "No active session") {
		t.Fatalf("expected no-active-session error, got %+v", msg)
	}
}

func TestHandler_ChatStop_CancelsRunningChat(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()
	runAIChat = func(ctx *ai.ChatContext, _ string) {
		<-ctx.Cancel
		ctx.OnChunk("cancelled", false)
		ctx.OnChunk("", true)
	}

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	created := readWSMessageOfType(t, conn, "session.created")
	sess, _ := created["session"].(map[string]any)
	sessionID, _ := sess["id"].(string)

	writeWSMessage(t, conn, map[string]any{
		"type":      "chat",
		"sessionId": sessionID,
		"content":   "long-running command",
	})
	readWSMessageOfType(t, conn, "chat.started")

	writeWSMessage(t, conn, map[string]any{
		"type":      "chat.stop",
		"sessionId": sessionID,
	})
	chunk := readWSMessageOfType(t, conn, "chat.chunk")
	if chunk["content"] != "cancelled" {
		t.Fatalf("expected cancelled chunk, got %+v", chunk)
	}
}

func TestHandler_SessionLoad_NotFound(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "session.load",
		"sessionId": "missing-session",
	})
	msg := readWSMessageOfType(t, conn, "error")
	if msg["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %+v", msg)
	}
}

func TestHandler_SessionCleanup_Success(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	created := readWSMessageOfType(t, conn, "session.created")
	sess, _ := created["session"].(map[string]any)
	sessionID, _ := sess["id"].(string)

	writeWSMessage(t, conn, map[string]any{
		"type":      "session.cleanup",
		"sessionId": sessionID,
		"merge":     false,
	})
	msg := readWSMessageOfType(t, conn, "session.cleanup.started")
	if msg["sessionId"] != sessionID {
		t.Fatalf("expected cleanup started for %q, got %+v", sessionID, msg)
	}
}

func TestHandler_SessionCreate_WorktreeFallbackProjectPath(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":        "session.create",
		"projectPath": projectDir,
	})
	msg := readWSMessageOfType(t, conn, "session.created")
	sess, _ := msg["session"].(map[string]any)
	pp, _ := sess["projectPath"].(string)
	if pp == "" {
		t.Fatalf("expected non-empty session projectPath, got %+v", msg)
	}
	if pp != projectDir && !strings.Contains(pp, "/.engine/worktrees/") {
		t.Fatalf("expected repo path or worktree path, got %q", pp)
	}
}

// ─── terminal dispatch ───────────────────────────────────────────────────────

func TestHandler_TerminalCreate_BadCwd(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "terminal.create",
		"cwd":  filepath.Join(projectDir, "does-not-exist"),
	})
	readWSMessageOfType(t, conn, "error")
}

func TestHandler_TerminalLifecycle(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "terminal.create",
		"cwd":  projectDir,
	})
	created := readWSMessageOfType(t, conn, "terminal.created")
	id, _ := created["terminalId"].(string)
	if id == "" {
		t.Fatalf("expected terminalId, got %+v", created)
	}

	writeWSMessage(t, conn, map[string]any{
		"type":       "terminal.input",
		"terminalId": id,
		"data":       "echo hi\n",
	})
	writeWSMessage(t, conn, map[string]any{
		"type":       "terminal.resize",
		"terminalId": id,
		"cols":       80,
		"rows":       24,
	})
	writeWSMessage(t, conn, map[string]any{
		"type":       "terminal.close",
		"terminalId": id,
	})
	readWSMessageOfType(t, conn, "terminal.closed")
}

// ─── remote pairing dispatch ─────────────────────────────────────────────────

func TestHandler_RemotePairCodeGenerate_Disabled(t *testing.T) {
	orig := localPairingManager
	localPairingManager = nil
	defer func() { localPairingManager = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "remote.pair.code.generate"})
	readWSMessageOfType(t, conn, "error")
}

func TestHandler_RemotePairCodeGenerate_Success(t *testing.T) {
	orig := localPairingManager
	pm := remote.NewPairingManager()
	localPairingManager = pm
	defer func() { localPairingManager = orig }()

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "remote.pair.code.generate"})
	msg := readWSMessageOfType(t, conn, "remote.pair.code")
	code, _ := msg["code"].(string)
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got %+v", msg)
	}
}

// Suppress unused import lint.
var _ = json.Marshal
var _ *websocket.Conn
var _ = httptest.NewServer
var _ = discordgo.EndpointGatewayBot

// TestHandler_DiscordConfigSet_NilBridge_EnabledStartFails covers the
// discordBridge==nil && cfg.Enabled==true && service.Start() fails path.
func TestHandler_DiscordConfigSet_NilBridge_EnabledStartFails(t *testing.T) {
	SetDiscordBridge(nil)
	defer SetDiscordBridge(nil)

	// Intercept Discord gateway endpoint to return 401 so dg.Open() fails.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401: Unauthorized","code":0}`, http.StatusUnauthorized)
	}))
	defer errSrv.Close()

	origGWB := discordgo.EndpointGatewayBot
	defer func() { discordgo.EndpointGatewayBot = origGWB }()
	discordgo.EndpointGatewayBot = errSrv.URL + "/gateway/bot"

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"enabled": true,
			"token":   "fake-bot-token",
			"guildId": "guild-ws-test",
		},
	})
	msg := drainWSUntilType(t, conn, "discord.config.saved")
	if msg["warning"] == nil {
		t.Fatalf("expected start-failed warning, got %+v", msg)
	}
}

// ── Repository Registry WS handler tests ─────────────────────────────────────

func TestHandler_RepoList_Empty(t *testing.T) {
	origLoad := repoRegistryLoadFn
	defer func() { repoRegistryLoadFn = origLoad }()
	repoRegistryLoadFn = func(_ string) ([]workspace.RegistryEntry, error) {
		return []workspace.RegistryEntry{}, nil
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "repo.list"})
	msg := drainWSUntilType(t, conn, "repo.list")
	entries, ok := msg["entries"].([]any)
	if !ok {
		t.Fatalf("expected entries array, got %T", msg["entries"])
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestHandler_RepoList_Error(t *testing.T) {
	origLoad := repoRegistryLoadFn
	defer func() { repoRegistryLoadFn = origLoad }()
	repoRegistryLoadFn = func(_ string) ([]workspace.RegistryEntry, error) {
		return nil, fmt.Errorf("disk error")
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "repo.list"})
	msg := drainWSUntilType(t, conn, "error")
	if code, _ := msg["code"].(string); code != "REPO_LIST_ERROR" {
		t.Errorf("code = %q, want REPO_LIST_ERROR", code)
	}
}

func TestHandler_RepoAdd_Success(t *testing.T) {
	origAdd := repoRegistryAddFn
	defer func() { repoRegistryAddFn = origAdd }()
	repoRegistryAddFn = func(_ string, p string) (*workspace.RegistryEntry, error) {
		return &workspace.RegistryEntry{Name: "testrepo", LocalPath: p, URL: ""}, nil
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "repo.add",
		"urlOrPath": "/some/path",
	})
	msg := drainWSUntilType(t, conn, "repo.added")
	entry, ok := msg["entry"].(map[string]any)
	if !ok {
		t.Fatalf("expected entry map, got %T", msg["entry"])
	}
	if entry["name"] != "testrepo" {
		t.Errorf("name = %v, want testrepo", entry["name"])
	}
}

func TestHandler_RepoAdd_Error(t *testing.T) {
	origAdd := repoRegistryAddFn
	defer func() { repoRegistryAddFn = origAdd }()
	repoRegistryAddFn = func(_ string, _ string) (*workspace.RegistryEntry, error) {
		return nil, fmt.Errorf("clone failed")
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type":      "repo.add",
		"urlOrPath": "https://github.com/x/y.git",
	})
	msg := drainWSUntilType(t, conn, "error")
	if code, _ := msg["code"].(string); code != "REPO_ADD_ERROR" {
		t.Errorf("code = %q, want REPO_ADD_ERROR", code)
	}
}

func TestHandler_RepoRemove_Success(t *testing.T) {
	origRemove := repoRegistryRemoveFn
	defer func() { repoRegistryRemoveFn = origRemove }()
	repoRegistryRemoveFn = func(_ string, _ string) error { return nil }

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "repo.remove",
		"name": "testrepo",
	})
	msg := drainWSUntilType(t, conn, "repo.removed")
	if msg["name"] != "testrepo" {
		t.Errorf("name = %v, want testrepo", msg["name"])
	}
}

func TestHandler_RepoRemove_Error(t *testing.T) {
	origRemove := repoRegistryRemoveFn
	defer func() { repoRegistryRemoveFn = origRemove }()
	repoRegistryRemoveFn = func(_ string, _ string) error {
		return fmt.Errorf("not found")
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "repo.remove",
		"name": "ghost",
	})
	msg := drainWSUntilType(t, conn, "error")
	if code, _ := msg["code"].(string); code != "REPO_REMOVE_ERROR" {
		t.Errorf("code = %q, want REPO_REMOVE_ERROR", code)
	}
}

// ── DiscordDM closure coverage ────────────────────────────────────────────────

func testSendChatAndWaitForRunAIChat(t *testing.T, conn *websocket.Conn, projectDir string) {
	t.Helper()
	writeWSMessage(t, conn, map[string]any{"type": "project.open", "path": projectDir})
	readWSMessageOfType(t, conn, "session.created")
	writeWSMessage(t, conn, map[string]any{"type": "chat", "content": "ping"})
}

func TestHandler_DiscordDM_NilBridge_ReturnsError(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	SetDiscordBridge(nil)

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()
	var dmErr error
	done := make(chan struct{})
	runAIChat = func(ctx *ai.ChatContext, _ string) {
		dmErr = ctx.DiscordDM("hello owner")
		close(done)
		ctx.OnChunk("ok", true)
	}

	testSendChatAndWaitForRunAIChat(t, conn, projectDir)
	<-done
	if dmErr == nil || dmErr.Error() != "Discord not configured" {
		t.Fatalf("expected 'Discord not configured', got %v", dmErr)
	}
}

func TestHandler_DiscordDM_WithBridge_CallsSendDMToOwner(t *testing.T) {
	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	var capturedMsg string
	SetDiscordBridge(&stubDiscordBridgeWithDM{captureMsg: &capturedMsg})

	origRunAIChat := runAIChat
	defer func() { runAIChat = origRunAIChat }()
	done := make(chan struct{})
	runAIChat = func(ctx *ai.ChatContext, _ string) {
		_ = ctx.DiscordDM("hello from agent")
		close(done)
		ctx.OnChunk("ok", true)
	}

	testSendChatAndWaitForRunAIChat(t, conn, projectDir)
	<-done
	if capturedMsg != "hello from agent" {
		t.Fatalf("SendDMToOwner not called with correct msg, got %q", capturedMsg)
	}
}

type stubDiscordBridgeWithDM struct {
	stubDiscordBridge
	captureMsg *string
}

func (s *stubDiscordBridgeWithDM) SendDMToOwner(message string) error {
	*s.captureMsg = message
	return nil
}

// ─── GitHub Auth device flow ──────────────────────────────────────────────────

func withAuthFnsReset(t *testing.T) {
	t.Helper()
	origClientID := githubClientIDFn
	origStart := githubStartDeviceFlowFn
	origPoll := githubPollForTokenFn
	origRandRead := githubWebhookRandReadFn
	SetGitHubAuthSuccessHook(nil)
	t.Cleanup(func() {
		githubClientIDFn = origClientID
		githubStartDeviceFlowFn = origStart
		githubPollForTokenFn = origPoll
		githubWebhookRandReadFn = origRandRead
		SetGitHubAuthSuccessHook(nil)
	})
}

func TestHandler_GitHubAuthStart_NoClientID(t *testing.T) {
	withAuthFnsReset(t)
	githubClientIDFn = func() string { return "" }

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})
	msg := readWSMessageOfType(t, conn, "github.auth.error")
	if msg["error"] == nil {
		t.Fatalf("expected error when GITHUB_CLIENT_ID is empty, got %+v", msg)
	}
}

func TestHandler_GitHubAuthStart_DeviceFlowError(t *testing.T) {
	withAuthFnsReset(t)
	githubClientIDFn = func() string { return "test-client-id" }
	githubStartDeviceFlowFn = func(_, _ string) (*github.DeviceCodeResponse, error) {
		return nil, fmt.Errorf("network error")
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})
	msg := readWSMessageOfType(t, conn, "github.auth.error")
	if msg["error"] == nil {
		t.Fatalf("expected error when device flow fails, got %+v", msg)
	}
}

func TestHandler_GitHubAuthStart_PollError(t *testing.T) {
	withAuthFnsReset(t)
	githubClientIDFn = func() string { return "test-client-id" }
	githubStartDeviceFlowFn = func(_, _ string) (*github.DeviceCodeResponse, error) {
		return &github.DeviceCodeResponse{
			DeviceCode:      "dev123",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		}, nil
	}
	githubPollForTokenFn = func(_ string, _ *github.DeviceCodeResponse, _ func(string)) (*github.TokenResponse, error) {
		return nil, fmt.Errorf("polling timeout")
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})
	// Should receive github.auth.code first, then github.auth.error.
	readWSMessageOfType(t, conn, "github.auth.code")
	errMsg := readWSMessageOfType(t, conn, "github.auth.error")
	if errMsg["error"] == nil {
		t.Fatalf("expected error after poll failure, got %+v", errMsg)
	}
}

func TestHandler_GitHubAuthStart_Success(t *testing.T) {
	withAuthFnsReset(t)
	t.Setenv("GITHUB_TOKEN", "") // start clean
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	githubClientIDFn = func() string { return "test-client-id" }
	githubStartDeviceFlowFn = func(_, _ string) (*github.DeviceCodeResponse, error) {
		return &github.DeviceCodeResponse{
			DeviceCode:      "dev456",
			UserCode:        "WXYZ-5678",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		}, nil
	}
	githubPollForTokenFn = func(_ string, _ *github.DeviceCodeResponse, onStatus func(string)) (*github.TokenResponse, error) {
		onStatus("waiting")
		return &github.TokenResponse{AccessToken: "ghp_testtoken"}, nil
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})

	codeMsg := readWSMessageOfType(t, conn, "github.auth.code")
	if codeMsg["userCode"] != "WXYZ-5678" {
		t.Fatalf("unexpected userCode: %+v", codeMsg)
	}
	if codeMsg["verificationUri"] != "https://github.com/login/device" {
		t.Fatalf("unexpected verificationUri: %+v", codeMsg)
	}

	// Status update.
	readWSMessageOfType(t, conn, "github.auth.status")

	doneMsg := readWSMessageOfType(t, conn, "github.auth.done")
	if doneMsg["token"] != "ghp_testtoken" {
		t.Fatalf("unexpected token in github.auth.done: %+v", doneMsg)
	}
	if strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET")) == "" {
		t.Fatalf("expected GITHUB_WEBHOOK_SECRET to be auto-provisioned")
	}
}

func TestHandler_GitHubAuthStart_Success_NotifiesHookWithSecret(t *testing.T) {
	withAuthFnsReset(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "preset-webhook-secret")

	githubClientIDFn = func() string { return "test-client-id" }
	githubStartDeviceFlowFn = func(_, _ string) (*github.DeviceCodeResponse, error) {
		return &github.DeviceCodeResponse{
			DeviceCode:      "dev456",
			UserCode:        "WXYZ-5678",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		}, nil
	}
	githubPollForTokenFn = func(_ string, _ *github.DeviceCodeResponse, _ func(string)) (*github.TokenResponse, error) {
		return &github.TokenResponse{AccessToken: "ghp_testtoken"}, nil
	}

	var hookToken string
	var hookSecret string
	SetGitHubAuthSuccessHook(func(token, webhookSecret string) {
		hookToken = token
		hookSecret = webhookSecret
	})

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})
	readWSMessageOfType(t, conn, "github.auth.code")
	readWSMessageOfType(t, conn, "github.auth.done")

	if hookToken != "ghp_testtoken" {
		t.Fatalf("expected hook token ghp_testtoken, got %q", hookToken)
	}
	if hookSecret != "preset-webhook-secret" {
		t.Fatalf("expected hook secret preset-webhook-secret, got %q", hookSecret)
	}
}

func TestHandler_GitHubAuthStart_EmptyAccessToken(t *testing.T) {
	withAuthFnsReset(t)
	githubClientIDFn = func() string { return "test-client-id" }
	githubStartDeviceFlowFn = func(_, _ string) (*github.DeviceCodeResponse, error) {
		return &github.DeviceCodeResponse{
			DeviceCode:      "dev789",
			UserCode:        "EMPTY-001",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		}, nil
	}
	githubPollForTokenFn = func(_ string, _ *github.DeviceCodeResponse, _ func(string)) (*github.TokenResponse, error) {
		return &github.TokenResponse{AccessToken: ""}, nil // empty token
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})
	readWSMessageOfType(t, conn, "github.auth.code")
	errMsg := readWSMessageOfType(t, conn, "github.auth.error")
	if errMsg["error"] == nil {
		t.Fatalf("expected error for empty access token, got %+v", errMsg)
	}
}

func TestHandler_GitHubAuthStart_WebhookSecretGenerationError(t *testing.T) {
	withAuthFnsReset(t)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	githubClientIDFn = func() string { return "test-client-id" }
	githubStartDeviceFlowFn = func(_, _ string) (*github.DeviceCodeResponse, error) {
		return &github.DeviceCodeResponse{
			DeviceCode:      "dev123",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		}, nil
	}
	githubPollForTokenFn = func(_ string, _ *github.DeviceCodeResponse, _ func(string)) (*github.TokenResponse, error) {
		return &github.TokenResponse{AccessToken: "ghp_testtoken"}, nil
	}
	githubWebhookRandReadFn = func(_ []byte) (int, error) {
		return 0, fmt.Errorf("rng failed")
	}

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{"type": "github.auth.start"})
	readWSMessageOfType(t, conn, "github.auth.code")
	errMsg := readWSMessageOfType(t, conn, "github.auth.error")

	if !strings.Contains(fmt.Sprint(errMsg["error"]), "generate webhook secret") {
		t.Fatalf("expected webhook secret generation error, got %+v", errMsg)
	}
}

func TestHandler_ConfigSync_ClonesDir_SetsEnv(t *testing.T) {
	t.Setenv("ENGINE_CLONES_DIR", "")

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	clones := t.TempDir()
	writeWSMessage(t, conn, map[string]any{
		"type": "config.sync",
		"config": map[string]any{
			"clonesDir": clones,
		},
	})
	// Flush with a known-response message.
	writeWSMessage(t, conn, map[string]any{"type": "session.list"})
	readWSMessageOfType(t, conn, "session.list")

	if got := os.Getenv("ENGINE_CLONES_DIR"); got != clones {
		t.Fatalf("expected ENGINE_CLONES_DIR %q, got %q", clones, got)
	}
}
func TestTriggerGitHubAuthSuccessHook_CallsRegisteredHook(t *testing.T) {
	var called bool
	var gotToken, gotSecret string
	SetGitHubAuthSuccessHook(func(token, webhookSecret string) {
		called = true
		gotToken = token
		gotSecret = webhookSecret
	})
	t.Cleanup(func() { SetGitHubAuthSuccessHook(nil) })

	TriggerGitHubAuthSuccessHook("mytoken", "mysecret")

	if !called {
		t.Fatal("expected hook to be called")
	}
	if gotToken != "mytoken" {
		t.Errorf("expected token 'mytoken', got %q", gotToken)
	}
	if gotSecret != "mysecret" {
		t.Errorf("expected secret 'mysecret', got %q", gotSecret)
	}
}

// ── default injectable fn bodies ──────────────────────────────────────────────

// roundTripFuncAdapter adapts a function to http.RoundTripper, used for mocking OAuthHTTPClient.
type roundTripFuncAdapter func(*http.Request) (*http.Response, error)

func (f roundTripFuncAdapter) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGithubStartDeviceFlowFn_DefaultBody(t *testing.T) {
	origFn := githubStartDeviceFlowFn
	t.Cleanup(func() { githubStartDeviceFlowFn = origFn })

	// Mock OAuthHTTPClient so no real network call is made.
	origClient := github.OAuthHTTPClient
	github.OAuthHTTPClient = &http.Client{
		Transport: roundTripFuncAdapter(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("device_code=DC&user_code=UC&verification_uri=https://example.com&expires_in=900&interval=5")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	t.Cleanup(func() { github.OAuthHTTPClient = origClient })

	dcr, err := origFn("test-client", "repo")
	if err != nil {
		t.Fatalf("default githubStartDeviceFlowFn: %v", err)
	}
	if dcr.DeviceCode != "DC" {
		t.Errorf("unexpected DeviceCode %q", dcr.DeviceCode)
	}
}

func TestGithubPollForTokenFn_DefaultBody(t *testing.T) {
	origFn := githubPollForTokenFn
	t.Cleanup(func() { githubPollForTokenFn = origFn })

	// ExpiresIn=0 so PollForToken loop never runs and returns immediately.
	_, err := origFn("test-client", &github.DeviceCodeResponse{ExpiresIn: 0, Interval: 0}, nil)
	if err == nil {
		t.Fatal("expected polling timeout error with zero ExpiresIn")
	}
}