package ws

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/engine/server/discord"
	gofs "github.com/engine/server/fs"
	gogit "github.com/engine/server/git"
	"github.com/engine/server/github"
	"github.com/engine/server/remote"
	"github.com/engine/server/terminal"
	"github.com/engine/server/workspace"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
}

var runAIChat = ai.Chat

// Overridable DB/AI calls for testing error paths.
var (
	dbListSessions             = db.ListSessions
	dbCreateSession            = db.CreateSession
	aiBuildInitialSummary      = ai.BuildInitialSessionSummary
	aiEnsureSessionWorktree    = ai.EnsureSessionWorktree
	aiCleanupSessionWorktreeDB = ai.CleanupSessionWorktree
)

// Overridable repo registry calls for testing.
var (
	repoRegistryLoadFn   = workspace.LoadRegistry
	repoRegistryAddFn    = workspace.AddToRegistry
	repoRegistryRemoveFn = workspace.RemoveFromRegistry
)

// approvalTimeout is the duration to wait for user approval; exposed for testing.
var approvalTimeout = 5 * time.Minute

// wsHTTPClient is used for GitHub API calls; exposed for testing.
var wsHTTPClient = http.DefaultClient

// GitHub OAuth injectable fns — exposed so tests can stub them.
var (
	githubClientIDFn        = func() string { return os.Getenv("GITHUB_CLIENT_ID") }
	githubStartDeviceFlowFn = func(clientID, scopes string) (*github.DeviceCodeResponse, error) {
		return github.StartDeviceFlow(clientID, scopes)
	}
	githubPollForTokenFn = func(clientID string, dcr *github.DeviceCodeResponse, onStatus func(string)) (*github.TokenResponse, error) {
		return github.PollForToken(clientID, dcr, onStatus)
	}
)

// DiscordBridge is the subset of the Discord service the WS handler uses.
// Kept narrow so tests can stub it.
type DiscordBridge interface {
	SendDMToOwner(message string) error
	CurrentConfig() discord.Config
	Reload(cfg discord.Config) error
	SearchHistory(projectPath, query, since string, limit int) ([]db.DiscordSearchHit, error)
	RecentHistory(projectPath, threadID, since string, limit int) ([]db.DiscordMessage, error)
}

// discordBridge is a package-level handle wired by main.go.
var discordBridge DiscordBridge

// SetDiscordBridge registers the Discord service with the WS layer.
// Passing nil disables the discord.* endpoints.
func SetDiscordBridge(d DiscordBridge) {
	discordBridge = d
}

// pairingCodeGenerator abstracts code generation so tests can inject error stubs.
type pairingCodeGenerator interface {
	GenerateCode() (string, error)
}

// localPairingManager is the PairingManager wired by main.go in remote mode.
// When nil the remote.pair.code.generate endpoint returns an error.
var localPairingManager pairingCodeGenerator

// SetPairingManager registers the PairingManager so WS clients can request
// one-time pairing codes without going through the TLS remote server directly.
func SetPairingManager(pm *remote.PairingManager) {
	if pm == nil {
		localPairingManager = nil
		return
	}
	localPairingManager = pm
}

// Hub manages the WebSocket server and default project path.
type Hub struct {
	projectPath    string
	localAuthToken string
}

// NewHub creates a new Hub.
func NewHub(projectPath string) *Hub {
	return &Hub{projectPath: projectPath, localAuthToken: strings.TrimSpace(os.Getenv("ENGINE_LOCAL_WS_TOKEN"))}
}

// SetDiscord attaches a Discord bridge so discord.* messages can be handled.
// Passing nil disables the discord endpoints. Equivalent to SetDiscordBridge.
func (h *Hub) SetDiscord(d DiscordBridge) {
	SetDiscordBridge(d)
}

// ServeWS upgrades an HTTP request to a WebSocket connection and handles it.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	if h.localAuthToken != "" {
		token := remote.ExtractToken(r)
		if subtle.ConstantTimeCompare([]byte(token), []byte(h.localAuthToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	c := newConn(conn, h.projectPath)
	go c.run()
}

// conn is per-connection state.
type conn struct {
	ws          *websocket.Conn
	projectPath string
	sessionID   string

	// done is closed when the connection's read loop exits. All goroutines that
	// call send() (AI chat, terminal output, etc.) should respect this signal so
	// they don't write to a closed WebSocket connection.
	done chan struct{}

	termMgr         *terminal.Manager
	termIDs         map[string]bool
	approvalMu      sync.Mutex
	approvalWaiters map[string]chan bool

	// openTabs is the client's current set of open editor tabs (updated via editor.tabs.sync).
	tabsMu   sync.RWMutex
	openTabs []ai.TabInfo

	// chatCancelMu guards the chatCancelFns map.
	chatCancelMu  sync.Mutex
	chatCancelFns map[string]func() // keyed by sessionID

	writeMu sync.Mutex

	// testObserverMu guards testObservers, which accumulate per-session output.
	testObserverMu sync.Mutex
	testObservers  map[string]*ai.TestObserver // keyed by sessionID
}

type runtimeConfig struct {
	GitHubToken   *string `json:"githubToken"`
	GitHubOwner   *string `json:"githubOwner"`
	GitHubRepo    *string `json:"githubRepo"`
	AnthropicKey  *string `json:"anthropicKey"`
	OpenAIKey     *string `json:"openaiKey"`
	ModelProvider *string `json:"modelProvider"`
	OllamaBaseURL *string `json:"ollamaBaseUrl"`
	Model         *string `json:"model"`
}

func (c *conn) resolveChatSession(requestedSessionID string) (*db.Session, error) {
	sessionID := strings.TrimSpace(requestedSessionID)
	if sessionID == "" {
		sessionID = c.sessionID
	}
	if sessionID == "" {
		return nil, fmt.Errorf("No active session")
	}

	session, err := db.GetSession(sessionID)
	if err != nil || session == nil {
		return nil, fmt.Errorf("Session not found")
	}

	c.sessionID = sessionID
	if strings.TrimSpace(session.ProjectPath) != "" {
		c.projectPath = session.ProjectPath
	}
	return session, nil
}

func newConn(ws *websocket.Conn, projectPath string) *conn {
	return &conn{
		ws:              ws,
		projectPath:     projectPath,
		done:            make(chan struct{}),
		termMgr:         terminal.NewManager(),
		termIDs:         make(map[string]bool),
		approvalWaiters: make(map[string]chan bool),
		chatCancelFns:   make(map[string]func()),
		testObservers:   make(map[string]*ai.TestObserver),
	}
}


// send marshals and writes a message to the WebSocket client (thread-safe).
// Returns silently if the connection has already been closed.
func (c *conn) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("ws marshal error: %v", err)
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// Check done INSIDE the lock — this is atomic with ws.Close() in run()'s
	// defer (which also holds writeMu before closing). Without this, a goroutine
	// can pass the done check, then run() closes the socket, then the write
	// hits a closed connection.
	select {
	case <-c.done:
		return
	default:
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			log.Printf("ws write error: %v", err)
		}
	}
}

func (c *conn) sendErr(message, code string) {
	c.send(map[string]string{"type": "error", "message": message, "code": code})
}

func (c *conn) requestApproval(sessionID, kind, title, message, command string) (bool, error) {
	id := newID()
	waiter := make(chan bool, 1)

	c.approvalMu.Lock()
	c.approvalWaiters[id] = waiter
	c.approvalMu.Unlock()

	c.send(map[string]any{
		"type": "approval.request",
		"request": map[string]string{
			"id":        id,
			"sessionId": sessionID,
			"kind":      kind,
			"title":     title,
			"message":   message,
			"command":   command,
		},
	})

	select {
	case allow := <-waiter:
		return allow, nil
	case <-time.After(approvalTimeout):
		c.approvalMu.Lock()
		delete(c.approvalWaiters, id)
		c.approvalMu.Unlock()
		return false, fmt.Errorf("approval timed out")
	}
}

func (c *conn) resolveApproval(id string, allow bool) {
	c.approvalMu.Lock()
	waiter, ok := c.approvalWaiters[id]
	if ok {
		delete(c.approvalWaiters, id)
	}
	c.approvalMu.Unlock()
	if !ok {
		return
	}
	waiter <- allow
	close(waiter)
}

func (c *conn) resolveAllApprovals(allow bool) {
	c.approvalMu.Lock()
	waiters := c.approvalWaiters
	c.approvalWaiters = make(map[string]chan bool)
	c.approvalMu.Unlock()

	for _, waiter := range waiters {
		waiter <- allow
		close(waiter)
	}
}

func (c *conn) run() {
	defer func() {
		// Recover any panic so a single bad connection can't crash the server.
		if r := recover(); r != nil {
			log.Printf("[engine] ws: panic in connection handler: %v", r)
		}
		// Close done first so all goroutines (AI chat, terminal callbacks, etc.)
		// stop trying to write. Then acquire writeMu so we wait for any in-flight
		// write to drain before closing the underlying socket — prevents
		// write-to-closed-connection errors even with the fixed atomic check.
		close(c.done)
		c.termMgr.KillAll()
		c.resolveAllApprovals(false)
		c.writeMu.Lock()
		c.ws.Close()
		c.writeMu.Unlock()
	}()

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			c.sendErr("Invalid JSON", "INVALID_JSON")
			continue
		}
		c.dispatch(base.Type, raw)
	}
}

func (c *conn) dispatch(msgType string, raw []byte) {
	projectPath := c.projectPath

	switch msgType {

	// ── Project ───────────────────────────────────────────────────────────────

	case "project.open":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if msg.Path == "" {
			c.sendErr("Path required", "BAD_PAYLOAD")
			return
		}
		c.projectPath = msg.Path
		projectPath = msg.Path

		// Resume the most recent session for this project if one exists with messages.
		// Only create a new session when none exists yet.
		var sessionID string
		var sessionCreated bool
		ai.EnsureProjectDirection(msg.Path)
		if existing, err := dbListSessions(msg.Path); err == nil && len(existing) > 0 {
			sessionID = existing[0].ID
		} else {
			sessionCreated = true
			id := newID()
			branch, _ := gogit.GetCurrentBranch(msg.Path)
			if err := dbCreateSession(id, msg.Path, branch); err != nil {
				c.sendErr(err.Error(), "DB_ERROR")
				return
			}
			if summary := aiBuildInitialSummary(msg.Path); summary != "" {
				db.UpdateSessionSummary(id, summary) //nolint:errcheck
			}
			sessionID = id
		}

		c.sessionID = sessionID
		session, _ := db.GetSession(sessionID)
		if sessionCreated {
			c.send(map[string]any{"type": "session.created", "session": session})
		} else {
			messages, _ := db.GetMessages(sessionID)
			c.send(map[string]any{"type": "session.loaded", "session": session, "messages": messages})
		}

		tree, err := gofs.GetTree(msg.Path, 1)
		if err == nil {
			c.send(map[string]any{"type": "file.tree", "tree": tree})
		}
		status, err := gogit.GetStatus(msg.Path)
		if err == nil {
			c.send(map[string]any{"type": "git.status", "status": status})
		}
		// Push full session list so the sidebar shows prior sessions immediately.
		if allSessions, err := dbListSessions(msg.Path); err == nil {
			c.send(map[string]any{"type": "session.list", "sessions": allSessions})
		}

	// ── Sessions ──────────────────────────────────────────────────────────────

	case "session.list":
		sessions, err := dbListSessions(projectPath)
		if err != nil {
			c.sendErr(err.Error(), "DB_ERROR")
			return
		}
		c.send(map[string]any{"type": "session.list", "sessions": sessions})

	case "session.create":
		var msg struct {
			ProjectPath string `json:"projectPath"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		if msg.ProjectPath != "" {
			c.projectPath = msg.ProjectPath
			projectPath = msg.ProjectPath
		}
		id := newID()
		branch, _ := gogit.GetCurrentBranch(projectPath)
		if err := dbCreateSession(id, projectPath, branch); err != nil {
			c.sendErr(err.Error(), "DB_ERROR")
			return
		}
		ai.EnsureProjectDirection(projectPath)
		if summary := aiBuildInitialSummary(projectPath); summary != "" {
			db.UpdateSessionSummary(id, summary) //nolint:errcheck
		}
		if wtPath, wtErr := aiEnsureSessionWorktree(id, projectPath); wtErr == nil && wtPath != projectPath {
			projectPath = wtPath
			c.projectPath = wtPath
			db.UpdateSessionProjectPath(id, wtPath) //nolint:errcheck
		}
		c.sessionID = id
		session, _ := db.GetSession(id)
		c.send(map[string]any{"type": "session.created", "session": session})

	case "session.cleanup":
		var msg struct {
			SessionID string `json:"sessionId"`
			Merge     bool   `json:"merge"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		if strings.TrimSpace(msg.SessionID) == "" {
			c.sendErr("sessionId required", "BAD_PAYLOAD")
			return
		}
		session, err := db.GetSession(msg.SessionID)
		if err != nil || session == nil {
			c.sendErr("Session not found", "NOT_FOUND")
			return
		}
		go func() {
			if cleanupErr := aiCleanupSessionWorktreeDB(msg.SessionID, session.ProjectPath, msg.Merge); cleanupErr != nil {
				log.Printf("[engine] ws: session.cleanup error: %v", cleanupErr)
			}
		}()
		c.send(map[string]any{"type": "session.cleanup.started", "sessionId": msg.SessionID})

	case "session.load":
		var msg struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		session, err := db.GetSession(msg.SessionID)
		if err != nil || session == nil {
			c.sendErr("Session not found", "NOT_FOUND")
			return
		}
		c.sessionID = msg.SessionID
		c.projectPath = session.ProjectPath
		messages, _ := db.GetMessages(msg.SessionID)
		c.send(map[string]any{"type": "session.loaded", "session": session, "messages": messages})

	// ── Chat ──────────────────────────────────────────────────────────────────

	case "chat":
		var msg struct {
			SessionID string `json:"sessionId"`
			Content   string `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		session, err := c.resolveChatSession(msg.SessionID)
		if err != nil {
			c.send(map[string]any{"type": "chat.error", "sessionId": strings.TrimSpace(msg.SessionID), "error": err.Error()})
			return
		}
		sessionID := session.ID
		projectPath = session.ProjectPath

		// Create a cancel channel for this request so the client can stop it.
		cancelCh := make(chan struct{})
		c.chatCancelMu.Lock()
		if old, ok := c.chatCancelFns[sessionID]; ok {
			old() // cancel any previous in-flight request for this session
		}
		c.chatCancelFns[sessionID] = func() { close(cancelCh) }
		c.chatCancelMu.Unlock()

		c.send(map[string]any{"type": "chat.started", "sessionId": sessionID})
		go func() {
			defer func() {
				// Remove cancel fn when goroutine exits.
				c.chatCancelMu.Lock()
				if fn, ok := c.chatCancelFns[sessionID]; ok {
					// Only delete if it's still the same channel (not replaced by a newer request).
					_ = fn
					delete(c.chatCancelFns, sessionID)
				}
				c.chatCancelMu.Unlock()

				if r := recover(); r != nil {
					log.Printf("[engine] ws: panic in AI chat goroutine: %v", r)
					c.send(map[string]any{
						"type":      "chat.error",
						"sessionId": sessionID,
						"error":     "Internal error — please try again",
					})
				}
			}()
			runAIChat(&ai.ChatContext{
				ProjectPath: projectPath,
				SessionID:   sessionID,
				Cancel:      cancelCh,
				OnChunk: func(content string, done bool) {
					c.send(map[string]any{"type": "chat.chunk", "sessionId": sessionID, "content": content, "done": done})
				},
				OnToolCall: func(name string, input any) {
					c.send(map[string]any{"type": "chat.tool_call", "sessionId": sessionID, "name": name, "input": input})
				},
				OnToolResult: func(name string, result any, isError bool) {
					c.send(map[string]any{"type": "chat.tool_result", "sessionId": sessionID, "name": name, "result": result, "isError": isError})
				},
				OnError: func(errMsg string) {
					c.send(map[string]any{"type": "chat.error", "sessionId": sessionID, "error": errMsg})
				},
				OnSessionUpdated: func(session *db.Session) {
					c.send(map[string]any{"type": "session.updated", "session": session})
				},
				GetOpenTabs: func() []ai.TabInfo {
					c.tabsMu.RLock()
					defer c.tabsMu.RUnlock()
					return c.openTabs
				},
				RequestApproval: func(kind, title, message, command string) (bool, error) {
					return c.requestApproval(sessionID, kind, title, message, command)
				},
				SendToClient: func(msgType string, payload any) {
					m := map[string]any{"type": msgType}
					switch p := payload.(type) {
					case map[string]any:
						maps.Copy(m, p)
					case map[string]string:
						for k, v := range p {
							m[k] = v
						}
					}
					c.send(m)
				},
				DiscordDM: func(message string) error {
					if discordBridge == nil {
						return fmt.Errorf("Discord not configured")
					}
					return discordBridge.SendDMToOwner(message)
				},
			}, msg.Content)
		}()

	case "chat.stop":
		var msg struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil || msg.SessionID == "" {
			return
		}
		c.chatCancelMu.Lock()
		if fn, ok := c.chatCancelFns[msg.SessionID]; ok {
			fn()
			delete(c.chatCancelFns, msg.SessionID)
		}
		c.chatCancelMu.Unlock()

	case "approval.respond":
		var msg struct {
			ID    string `json:"id"`
			Allow bool   `json:"allow"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		if msg.ID == "" {
			c.sendErr("Approval id required", "BAD_PAYLOAD")
			return
		}
		c.resolveApproval(msg.ID, msg.Allow)

	// ── Files ─────────────────────────────────────────────────────────────────

	case "file.read":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		fc, err := gofs.ReadFile(msg.Path)
		if err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]any{"type": "file.content", "path": msg.Path, "content": fc.Content, "language": fc.Language, "size": fc.Size})

	case "file.save":
		var msg struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Invalid file.save payload", "INVALID_PAYLOAD")
			return
		}
		if err := gofs.WriteFile(msg.Path, msg.Content); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]any{"type": "file.saved", "path": msg.Path})

	case "file.create":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if err := gofs.WriteFile(msg.Path, ""); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]any{"type": "file.created", "path": msg.Path})

	case "folder.create":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if err := os.MkdirAll(msg.Path, 0755); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]any{"type": "folder.created", "path": msg.Path})

	case "file.tree":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		tree, err := gofs.GetTree(msg.Path, 1)
		if err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]any{"type": "file.tree", "tree": tree})

	case "file.search":
		var msg struct {
			Query    string `json:"query"`
			Root     string `json:"root"`
			FileGlob string `json:"fileGlob"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.send(map[string]any{"type": "search.results", "query": "", "results": []gofs.SearchResult{}, "error": "Bad payload"})
			return
		}
		query := strings.TrimSpace(msg.Query)
		if query == "" {
			c.send(map[string]any{"type": "search.results", "query": msg.Query, "results": []gofs.SearchResult{}, "error": "Query required"})
			return
		}
		root := msg.Root
		if root == "" {
			root = projectPath
		}
		results, err := gofs.SearchMatches(query, root, msg.FileGlob)
		if err != nil {
			c.send(map[string]any{"type": "search.results", "query": query, "results": []gofs.SearchResult{}, "error": err.Error()})
			return
		}
		c.send(map[string]any{"type": "search.results", "query": query, "results": results})

	// ── Git ───────────────────────────────────────────────────────────────────

	case "git.status":
		go func() {
			status, _ := gogit.GetStatus(projectPath)
			c.send(map[string]any{"type": "git.status", "status": status})
		}()

	case "git.diff":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		diff, _ := gogit.GetDiff(projectPath, msg.Path)
		c.send(map[string]any{"type": "git.diff", "path": msg.Path, "diff": diff})

	case "git.log":
		var msg struct {
			Limit int `json:"limit"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if msg.Limit <= 0 {
			msg.Limit = 20
		}
		commits, _ := gogit.GetLog(projectPath, msg.Limit)
		c.send(map[string]any{"type": "git.log", "commits": commits})

	case "git.commit":
		var msg struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.send(map[string]any{"type": "git.commit.result", "ok": false, "message": "Bad payload"})
			return
		}
		message := strings.TrimSpace(msg.Message)
		if message == "" {
			c.send(map[string]any{"type": "git.commit.result", "ok": false, "message": "Commit message required"})
			return
		}
		hash, err := gogit.Commit(projectPath, message)
		if err != nil {
			c.send(map[string]any{"type": "git.commit.result", "ok": false, "message": err.Error()})
			return
		}
		c.send(map[string]any{"type": "git.commit.result", "ok": true, "hash": hash, "message": message})
		if status, err := gogit.GetStatus(projectPath); err == nil {
			c.send(map[string]any{"type": "git.status", "status": status})
		}
		if commits, err := gogit.GetLog(projectPath, 8); err == nil {
			c.send(map[string]any{"type": "git.log", "commits": commits})
		}

	case "workspace.tasks":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		root := projectPath
		if strings.TrimSpace(msg.Path) != "" {
			root = msg.Path
		}
		detected := workspace.DetectTasks(root)
		c.send(map[string]any{
			"type":               "workspace.tasks",
			"tasks":              detected.Tasks,
			"defaultBuildTaskId": detected.DefaultBuildTask,
			"defaultRunTaskId":   detected.DefaultRunTask,
		})

	case "config.sync":
		var msg struct {
			Config runtimeConfig `json:"config"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		applyRuntimeConfig(msg.Config)

	// ── Discord control plane ─────────────────────────────────────────────

	case "discord.config.get":
		c.handleDiscordConfigGet()

	case "discord.config.set":
		var msg struct {
			Config discordConfigPayload `json:"config"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		c.handleDiscordConfigSet(msg.Config)

	case "discord.validate":
		var msg struct {
			Config *discordConfigPayload `json:"config,omitempty"`
		}
		_ = json.Unmarshal(raw, &msg)
		c.handleDiscordValidate(msg.Config)

	case "discord.unlink":
		c.handleDiscordUnlink(true)

	case "discord.history.search":
		var msg struct {
			ProjectPath string `json:"projectPath"`
			Query       string `json:"query"`
			Since       string `json:"since"`
			Limit       int    `json:"limit"`
		}
		_ = json.Unmarshal(raw, &msg)
		c.handleDiscordHistorySearch(msg.ProjectPath, msg.Query, msg.Since, msg.Limit)

	case "discord.history.recent":
		var msg struct {
			ProjectPath string `json:"projectPath"`
			ThreadID    string `json:"threadId"`
			Since       string `json:"since"`
			Limit       int    `json:"limit"`
		}
		_ = json.Unmarshal(raw, &msg)
		c.handleDiscordHistoryRecent(msg.ProjectPath, msg.ThreadID, msg.Since, msg.Limit)


	// ── Remote pairing ─────────────────────────────────────────────────────────────────────────────

	case "remote.pair.code.generate":
		c.handleRemotePairCodeGenerate()

	// ── GitHub Auth / Issues ──────────────────────────────────────────────────

	case "github.auth.start":
		go c.handleGitHubAuthStart()

	case "github.user":
		c.handleGitHubUser()

	case "github.issues":
		var msg struct {
			ProjectPath string `json:"projectPath"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		pp := msg.ProjectPath
		if pp == "" {
			pp = projectPath
		}
		go c.handleGitHubIssues(pp)

	// ── Usage Dashboard ───────────────────────────────────────────────────────

	case "usage.dashboard.get":
		var msg struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			Model       string `json:"model"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.send(map[string]any{"type": "usage.dashboard", "error": "Bad payload"})
			return
		}

		scope := strings.TrimSpace(msg.Scope)
		if scope == "" {
			scope = "project"
		}
		targetProjectPath := strings.TrimSpace(msg.ProjectPath)
		if targetProjectPath == "" {
			targetProjectPath = projectPath
		}

		dashboard, err := db.GetUsageDashboard(scope, targetProjectPath, strings.TrimSpace(msg.Model))
		if err != nil {
			c.send(map[string]any{"type": "usage.dashboard", "error": err.Error()})
			return
		}
		c.send(map[string]any{"type": "usage.dashboard", "dashboard": dashboard})

	// ── Terminals ─────────────────────────────────────────────────────────────

	case "terminal.create":
		var msg struct {
			Cwd string `json:"cwd"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		id := newID()
		cwd := msg.Cwd
		if cwd == "" {
			cwd = projectPath
		}
		_, err := c.termMgr.Create(id, cwd,
			func(data string) {
				c.send(map[string]any{"type": "terminal.output", "terminalId": id, "data": data})
			},
			func() {
				delete(c.termIDs, id)
				c.send(map[string]any{"type": "terminal.closed", "terminalId": id})
			},
		)
		if err != nil {
			c.sendErr(err.Error(), "TERMINAL_ERROR")
			return
		}
		c.termIDs[id] = true
		c.send(map[string]any{"type": "terminal.created", "terminalId": id, "cwd": cwd})

	case "terminal.input":
		var msg struct {
			TerminalID string `json:"terminalId"`
			Data       string `json:"data"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		c.termMgr.Write(msg.TerminalID, msg.Data)

	case "terminal.resize":
		var msg struct {
			TerminalID string `json:"terminalId"`
			Cols       uint16 `json:"cols"`
			Rows       uint16 `json:"rows"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		c.termMgr.Resize(msg.TerminalID, msg.Cols, msg.Rows)

	case "terminal.close":
		var msg struct {
			TerminalID string `json:"terminalId"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		c.termMgr.Kill(msg.TerminalID)
		delete(c.termIDs, msg.TerminalID)

	// ── Editor Tab Sync ───────────────────────────────────────────────────────

	case "editor.tabs.sync":
		var msg struct {
			Tabs []ai.TabInfo `json:"tabs"`
		}
		if err := json.Unmarshal(raw, &msg); err == nil {
			c.tabsMu.Lock()
			c.openTabs = msg.Tabs
			c.tabsMu.Unlock()
		}

	// ── Engine Team Orchestration ────────────────────────────────────────────

	case "engine.config.get":
		configPath := filepath.Join(projectPath, ".engine", "config.yaml")
		content, err := os.ReadFile(configPath)
		if err != nil {
			c.send(map[string]any{
				"type":  "engine.config",
				"yaml":  "",
				"error": "No .engine/config.yaml found",
			})
			return
		}
		c.send(map[string]any{
			"type": "engine.config",
			"yaml": string(content),
		})

	case "engine.team.set":
		var msg struct {
			Team     string `json:"team"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		if strings.TrimSpace(msg.Team) != "" && (strings.TrimSpace(msg.Provider) == "" || strings.TrimSpace(msg.Model) == "") {
			_, resolvedProvider, resolvedModel, ok := ai.ResolveTeamOrchestratorModel(projectPath, msg.Team)
			if ok {
				if strings.TrimSpace(msg.Provider) == "" {
					msg.Provider = resolvedProvider
				}
				if strings.TrimSpace(msg.Model) == "" {
					msg.Model = resolvedModel
				}
			}
		}
		if strings.TrimSpace(msg.Provider) != "" {
			os.Setenv("ENGINE_MODEL_PROVIDER", strings.TrimSpace(msg.Provider)) //nolint:errcheck
		}
		if strings.TrimSpace(msg.Model) != "" {
			os.Setenv("ENGINE_MODEL", strings.TrimSpace(msg.Model)) //nolint:errcheck
		}
		if strings.TrimSpace(msg.Team) != "" {
			os.Setenv("ENGINE_ACTIVE_TEAM", strings.TrimSpace(msg.Team)) //nolint:errcheck
		}
		c.send(map[string]any{
			"type": "engine.team.updated",
			"team": msg.Team,
		})


	// ── Test Observer ─────────────────────────────────────────────────────────

	case "test.observe":
		var msg struct {
			SessionID string `json:"sessionId"`
			Line      string `json:"line"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		if strings.TrimSpace(msg.SessionID) == "" {
			c.sendErr("sessionId required", "BAD_PAYLOAD")
			return
		}
		c.testObserverMu.Lock()
		obs, ok := c.testObservers[msg.SessionID]
		if !ok {
			obs = ai.NewTestObserver()
			c.testObservers[msg.SessionID] = obs
		}
		prevErrCount := len(obs.GetSummary().Errors)
		obs.Observe(msg.Line)
		summary := obs.GetSummary()
		newErrCount := len(summary.Errors)
		lineCount := strings.Count(summary.Output, "\n")
		c.testObserverMu.Unlock()
		if lineCount%20 == 0 || newErrCount > prevErrCount {
			c.send(map[string]any{
				"type":      "test.summary",
				"sessionId": msg.SessionID,
				"summary":   summary,
			})
		}

	case "test.summary.get":
		var msg struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		if strings.TrimSpace(msg.SessionID) == "" {
			c.sendErr("sessionId required", "BAD_PAYLOAD")
			return
		}
		c.testObserverMu.Lock()
		obs, ok := c.testObservers[msg.SessionID]
		var summary ai.TestSummary
		if ok {
			summary = obs.GetSummary()
		} else {
			summary = ai.TestSummary{Errors: []string{}, Warnings: []string{}, Success: true}
		}
		c.testObserverMu.Unlock()
		c.send(map[string]any{
			"type":      "test.summary",
			"sessionId": msg.SessionID,
			"summary":   summary,
		})

	// ── Repository Registry ───────────────────────────────────────────────────

	case "repo.list":
		c.handleRepoList()

	case "repo.add":
		var msg struct {
			URLOrPath string `json:"urlOrPath"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		c.handleRepoAdd(msg.URLOrPath)

	case "repo.remove":
		var msg struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		c.handleRepoRemove(msg.Name)

	default:
		c.sendErr(fmt.Sprintf("Unknown message type: %s", msgType), "UNKNOWN_TYPE")
	}
}

// ── Repository Registry ────────────────────────────────────────────────────────

func (c *conn) handleRepoList() {
	entries, err := repoRegistryLoadFn(c.projectPath)
	if err != nil {
		c.sendErr("Failed to load repository registry: "+err.Error(), "REPO_LIST_ERROR")
		return
	}
	c.send(map[string]any{
		"type":    "repo.list",
		"entries": entries,
	})
}

func (c *conn) handleRepoAdd(urlOrPath string) {
	entry, err := repoRegistryAddFn(c.projectPath, urlOrPath)
	if err != nil {
		c.sendErr("Failed to add repository: "+err.Error(), "REPO_ADD_ERROR")
		return
	}
	c.send(map[string]any{
		"type":  "repo.added",
		"entry": entry,
	})
}

func (c *conn) handleRepoRemove(name string) {
	if err := repoRegistryRemoveFn(c.projectPath, name); err != nil {
		c.sendErr("Failed to remove repository: "+err.Error(), "REPO_REMOVE_ERROR")
		return
	}
	c.send(map[string]any{
		"type": "repo.removed",
		"name": name,
	})
}

// handleRemotePairCodeGenerate generates a one-time pairing code and sends it back.
func (c *conn) handleRemotePairCodeGenerate() {
	pm := localPairingManager
	if pm == nil {
		c.sendErr("Remote pairing is not enabled on this server", "PAIRING_DISABLED")
		return
	}
	code, err := pm.GenerateCode()
	if err != nil {
		c.sendErr("Failed to generate pairing code", "PAIRING_ERROR")
		return
	}
	c.send(map[string]any{
		"type":      "remote.pair.code",
		"code":      code,
		"expiresIn": 300,
	})
}

func (c *conn) handleGitHubIssues(projectPath string) {
	owner, repo, overrideConfigured := githubRepoOverride()
	switch {
	case overrideConfigured && (owner == "" || repo == ""):
		c.send(map[string]any{"type": "github.issues", "issues": []any{}, "error": "GitHub owner and repository must both be set in Settings."})
		return
	case !overrideConfigured:
		resolvedOwner, resolvedRepo, err := gogit.ResolveGitHubRepo(projectPath)
		if err != nil {
			c.send(map[string]any{
				"type":   "github.issues",
				"issues": []any{},
				"error":  "No GitHub remote or configured repository. Add a GitHub remote or set GitHub owner/repository in Settings.",
			})
			return
		}
		owner, repo = resolvedOwner, resolvedRepo
	}

	issues, err := fetchGitHubIssues(owner, repo)
	if err != nil {
		c.send(map[string]any{"type": "github.issues", "issues": []any{}, "error": err.Error()})
		return
	}
	c.send(map[string]any{"type": "github.issues", "issues": issues})
}

func (c *conn) handleGitHubUser() {
	user, err := fetchGitHubUser()
	if err != nil {
		c.send(map[string]any{"type": "github.user", "user": nil, "error": err.Error()})
		return
	}
	c.send(map[string]any{"type": "github.user", "user": user})
}

// handleGitHubAuthStart runs the GitHub Device Authorization Flow so the user
// can log in without ever entering a token manually.
// Flow:
//  1. Start device flow → send github.auth.code (userCode + verificationUri)
//  2. Poll GitHub in background → send github.auth.status updates
//  3. On success: set GITHUB_TOKEN env var, send github.auth.done
//  4. On failure: send github.auth.error
func (c *conn) handleGitHubAuthStart() {
	clientID := githubClientIDFn()
	if clientID == "" {
		c.send(map[string]any{
			"type":  "github.auth.error",
			"error": "GITHUB_CLIENT_ID not configured — set it in the server environment",
		})
		return
	}

	dcr, err := githubStartDeviceFlowFn(clientID, "")
	if err != nil {
		c.send(map[string]any{"type": "github.auth.error", "error": err.Error()})
		return
	}

	c.send(map[string]any{
		"type":            "github.auth.code",
		"userCode":        dcr.UserCode,
		"verificationUri": dcr.VerificationURI,
		"expiresIn":       dcr.ExpiresIn,
	})

	tok, err := githubPollForTokenFn(clientID, dcr, func(status string) {
		c.send(map[string]any{"type": "github.auth.status", "status": status})
	})
	if err != nil {
		c.send(map[string]any{"type": "github.auth.error", "error": err.Error()})
		return
	}
	if tok.AccessToken == "" {
		c.send(map[string]any{"type": "github.auth.error", "error": "no access token in response"})
		return
	}

	// Activate the token immediately so the rest of the server uses it.
	os.Setenv("GITHUB_TOKEN", tok.AccessToken) //nolint:errcheck

	c.send(map[string]any{"type": "github.auth.done", "token": tok.AccessToken})
}

type githubIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HtmlURL string `json:"htmlUrl"`
	State   string `json:"state"`
	Author  string `json:"author"`
	Labels  []struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"labels"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type githubUser struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
}

func applyRuntimeConfig(cfg runtimeConfig) {
	setRuntimeEnv("GITHUB_TOKEN", cfg.GitHubToken)
	setRuntimeEnv("ENGINE_GITHUB_OWNER", cfg.GitHubOwner)
	setRuntimeEnv("ENGINE_GITHUB_REPO", cfg.GitHubRepo)
	setRuntimeEnv("ANTHROPIC_API_KEY", cfg.AnthropicKey)
	setRuntimeEnv("OPENAI_API_KEY", cfg.OpenAIKey)
	setRuntimeEnv("ENGINE_MODEL_PROVIDER", cfg.ModelProvider)
	setRuntimeEnv("OLLAMA_BASE_URL", cfg.OllamaBaseURL)
	setRuntimeEnv("ENGINE_MODEL", cfg.Model)
}

func setRuntimeEnv(key string, value *string) {
	if value == nil || strings.TrimSpace(*value) == "" {
		os.Unsetenv(key) //nolint:errcheck
		return
	}
	os.Setenv(key, strings.TrimSpace(*value)) //nolint:errcheck
}

func fetchGitHubUser() (*githubUser, error) {
	token := githubToken()
	if token == "" {
		return nil, fmt.Errorf("GitHub token not configured")
	}

	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Engine/0.1")
	req.Header.Set("Authorization", "token "+token)

	resp, err := wsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	var raw struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	return &githubUser{
		Login:     raw.Login,
		Name:      raw.Name,
		AvatarURL: raw.AvatarURL,
	}, nil
}

func fetchGitHubIssues(owner, repo string) ([]githubIssue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=open&per_page=30", owner, repo)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Engine/0.1")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := wsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	var raw []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HtmlURL string `json:"html_url"`
		State   string `json:"state"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"labels"`
		CreatedAt   string      `json:"created_at"`
		UpdatedAt   string      `json:"updated_at"`
		PullRequest any `json:"pull_request"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	var issues []githubIssue
	for _, i := range raw {
		if i.PullRequest != nil {
			continue
		}
		issues = append(issues, githubIssue{
			Number:    i.Number,
			Title:     i.Title,
			Body:      i.Body,
			HtmlURL:   i.HtmlURL,
			State:     i.State,
			Author:    i.User.Login,
			Labels:    i.Labels,
			CreatedAt: i.CreatedAt,
			UpdatedAt: i.UpdatedAt,
		})
	}
	if issues == nil {
		issues = []githubIssue{}
	}
	return issues, nil
}

func newID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func githubToken() string {
	return os.Getenv("GITHUB_TOKEN")
}

func githubRepoOverride() (string, string, bool) {
	owner := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_OWNER"))
	repo := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_REPO"))
	return owner, repo, owner != "" || repo != ""
}
