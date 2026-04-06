package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	gofs "github.com/engine/server/fs"
	gogit "github.com/engine/server/git"
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

// Hub manages the WebSocket server and default project path.
type Hub struct {
	projectPath string
}

// NewHub creates a new Hub.
func NewHub(projectPath string) *Hub {
	return &Hub{projectPath: projectPath}
}

// ServeWS upgrades an HTTP request to a WebSocket connection and handles it.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
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
	}
}

// send marshals and writes a message to the WebSocket client (thread-safe).
// Returns silently if the connection has already been closed.
func (c *conn) send(v interface{}) {
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

	c.send(map[string]interface{}{
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
	case <-time.After(5 * time.Minute):
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
		if existing, err := db.ListSessions(msg.Path); err == nil && len(existing) > 0 {
			sessionID = existing[0].ID
		} else {
			sessionCreated = true
			id := newID()
			branch, _ := gogit.GetCurrentBranch(msg.Path)
			if err := db.CreateSession(id, msg.Path, branch); err != nil {
				c.sendErr(err.Error(), "DB_ERROR")
				return
			}
			if summary := ai.BuildInitialSessionSummary(msg.Path); summary != "" {
				db.UpdateSessionSummary(id, summary) //nolint:errcheck
			}
			sessionID = id
		}

		c.sessionID = sessionID
		session, _ := db.GetSession(sessionID)
		if sessionCreated {
			c.send(map[string]interface{}{"type": "session.created", "session": session})
		} else {
			messages, _ := db.GetMessages(sessionID)
			c.send(map[string]interface{}{"type": "session.loaded", "session": session, "messages": messages})
		}

		tree, err := gofs.GetTree(msg.Path, 1)
		if err == nil {
			c.send(map[string]interface{}{"type": "file.tree", "tree": tree})
		}
		status, err := gogit.GetStatus(msg.Path)
		if err == nil {
			c.send(map[string]interface{}{"type": "git.status", "status": status})
		}
		// Push full session list so the sidebar shows prior sessions immediately.
		if allSessions, err := db.ListSessions(msg.Path); err == nil {
			c.send(map[string]interface{}{"type": "session.list", "sessions": allSessions})
		}

	// ── Sessions ──────────────────────────────────────────────────────────────

	case "session.list":
		sessions, err := db.ListSessions(projectPath)
		if err != nil {
			c.sendErr(err.Error(), "DB_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "session.list", "sessions": sessions})

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
		if err := db.CreateSession(id, projectPath, branch); err != nil {
			c.sendErr(err.Error(), "DB_ERROR")
			return
		}
		if summary := ai.BuildInitialSessionSummary(projectPath); summary != "" {
			db.UpdateSessionSummary(id, summary) //nolint:errcheck
		}
		c.sessionID = id
		session, _ := db.GetSession(id)
		c.send(map[string]interface{}{"type": "session.created", "session": session})

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
		c.send(map[string]interface{}{"type": "session.loaded", "session": session, "messages": messages})

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
			c.send(map[string]interface{}{"type": "chat.error", "sessionId": strings.TrimSpace(msg.SessionID), "error": err.Error()})
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

		c.send(map[string]interface{}{"type": "chat.started", "sessionId": sessionID})
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
					c.send(map[string]interface{}{
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
					c.send(map[string]interface{}{"type": "chat.chunk", "sessionId": sessionID, "content": content, "done": done})
				},
				OnToolCall: func(name string, input interface{}) {
					c.send(map[string]interface{}{"type": "chat.tool_call", "sessionId": sessionID, "name": name, "input": input})
				},
				OnToolResult: func(name string, result interface{}, isError bool) {
					c.send(map[string]interface{}{"type": "chat.tool_result", "sessionId": sessionID, "name": name, "result": result, "isError": isError})
				},
				OnError: func(errMsg string) {
					c.send(map[string]interface{}{"type": "chat.error", "sessionId": sessionID, "error": errMsg})
				},
				OnSessionUpdated: func(session *db.Session) {
					c.send(map[string]interface{}{"type": "session.updated", "session": session})
				},
				GetOpenTabs: func() []ai.TabInfo {
					c.tabsMu.RLock()
					defer c.tabsMu.RUnlock()
					return c.openTabs
				},
				RequestApproval: func(kind, title, message, command string) (bool, error) {
					return c.requestApproval(sessionID, kind, title, message, command)
				},
				SendToClient: func(msgType string, payload interface{}) {
					m := map[string]interface{}{"type": msgType}
					if p, ok := payload.(map[string]interface{}); ok {
						for k, v := range p {
							m[k] = v
						}
					}
					c.send(m)
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
		c.send(map[string]interface{}{"type": "file.content", "path": msg.Path, "content": fc.Content, "language": fc.Language, "size": fc.Size})

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
		c.send(map[string]interface{}{"type": "file.saved", "path": msg.Path})

	case "file.create":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if err := gofs.WriteFile(msg.Path, ""); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "file.created", "path": msg.Path})

	case "folder.create":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if err := os.MkdirAll(msg.Path, 0755); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "folder.created", "path": msg.Path})

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
		c.send(map[string]interface{}{"type": "file.tree", "tree": tree})

	case "file.search":
		var msg struct {
			Query    string `json:"query"`
			Root     string `json:"root"`
			FileGlob string `json:"fileGlob"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.send(map[string]interface{}{"type": "search.results", "query": "", "results": []gofs.SearchResult{}, "error": "Bad payload"})
			return
		}
		query := strings.TrimSpace(msg.Query)
		if query == "" {
			c.send(map[string]interface{}{"type": "search.results", "query": msg.Query, "results": []gofs.SearchResult{}, "error": "Query required"})
			return
		}
		root := msg.Root
		if root == "" {
			root = projectPath
		}
		results, err := gofs.SearchMatches(query, root, msg.FileGlob)
		if err != nil {
			c.send(map[string]interface{}{"type": "search.results", "query": query, "results": []gofs.SearchResult{}, "error": err.Error()})
			return
		}
		c.send(map[string]interface{}{"type": "search.results", "query": query, "results": results})

	// ── Git ───────────────────────────────────────────────────────────────────

	case "git.status":
		go func() {
			status, err := gogit.GetStatus(projectPath)
			if err != nil {
				c.sendErr(err.Error(), "GIT_ERROR")
				return
			}
			c.send(map[string]interface{}{"type": "git.status", "status": status})
		}()

	case "git.diff":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		diff, _ := gogit.GetDiff(projectPath, msg.Path)
		c.send(map[string]interface{}{"type": "git.diff", "path": msg.Path, "diff": diff})

	case "git.log":
		var msg struct {
			Limit int `json:"limit"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if msg.Limit <= 0 {
			msg.Limit = 20
		}
		commits, _ := gogit.GetLog(projectPath, msg.Limit)
		c.send(map[string]interface{}{"type": "git.log", "commits": commits})

	case "git.commit":
		var msg struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.send(map[string]interface{}{"type": "git.commit.result", "ok": false, "message": "Bad payload"})
			return
		}
		message := strings.TrimSpace(msg.Message)
		if message == "" {
			c.send(map[string]interface{}{"type": "git.commit.result", "ok": false, "message": "Commit message required"})
			return
		}
		hash, err := gogit.Commit(projectPath, message)
		if err != nil {
			c.send(map[string]interface{}{"type": "git.commit.result", "ok": false, "message": err.Error()})
			return
		}
		c.send(map[string]interface{}{"type": "git.commit.result", "ok": true, "hash": hash, "message": message})
		if status, err := gogit.GetStatus(projectPath); err == nil {
			c.send(map[string]interface{}{"type": "git.status", "status": status})
		}
		if commits, err := gogit.GetLog(projectPath, 8); err == nil {
			c.send(map[string]interface{}{"type": "git.log", "commits": commits})
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
		c.send(map[string]interface{}{
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

	// ── GitHub Issues ─────────────────────────────────────────────────────────

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
				c.send(map[string]interface{}{"type": "terminal.output", "terminalId": id, "data": data})
			},
			func() {
				delete(c.termIDs, id)
				c.send(map[string]interface{}{"type": "terminal.closed", "terminalId": id})
			},
		)
		if err != nil {
			c.sendErr(err.Error(), "TERMINAL_ERROR")
			return
		}
		c.termIDs[id] = true
		c.send(map[string]interface{}{"type": "terminal.created", "terminalId": id, "cwd": cwd})

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

	default:
		c.sendErr(fmt.Sprintf("Unknown message type: %s", msgType), "UNKNOWN_TYPE")
	}
}

func (c *conn) handleGitHubIssues(projectPath string) {
	owner, repo, overrideConfigured := githubRepoOverride()
	switch {
	case overrideConfigured && (owner == "" || repo == ""):
		c.send(map[string]interface{}{"type": "github.issues", "issues": []interface{}{}, "error": "GitHub owner and repository must both be set in Settings."})
		return
	case !overrideConfigured:
		resolvedOwner, resolvedRepo, err := gogit.ResolveGitHubRepo(projectPath)
		if err != nil {
			c.send(map[string]interface{}{
				"type":   "github.issues",
				"issues": []interface{}{},
				"error":  "No GitHub remote or configured repository. Add a GitHub remote or set GitHub owner/repository in Settings.",
			})
			return
		}
		owner, repo = resolvedOwner, resolvedRepo
	}

	issues, err := fetchGitHubIssues(owner, repo)
	if err != nil {
		c.send(map[string]interface{}{"type": "github.issues", "issues": []interface{}{}, "error": err.Error()})
		return
	}
	c.send(map[string]interface{}{"type": "github.issues", "issues": issues})
}

func (c *conn) handleGitHubUser() {
	user, err := fetchGitHubUser()
	if err != nil {
		c.send(map[string]interface{}{"type": "github.user", "user": nil, "error": err.Error()})
		return
	}
	c.send(map[string]interface{}{"type": "github.user", "user": user})
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

	resp, err := http.DefaultClient.Do(req)
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

	resp, err := http.DefaultClient.Do(req)
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
		PullRequest interface{} `json:"pull_request"`
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
