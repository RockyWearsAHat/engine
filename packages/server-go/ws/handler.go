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

	"github.com/gorilla/websocket"
	"github.com/myeditor/server/ai"
	"github.com/myeditor/server/db"
	gofs "github.com/myeditor/server/fs"
	gogit "github.com/myeditor/server/git"
	"github.com/myeditor/server/terminal"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

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

	termMgr  *terminal.Manager
	termIDs  map[string]bool

	// openTabs is the client's current set of open editor tabs (updated via editor.tabs.sync).
	tabsMu   sync.RWMutex
	openTabs []ai.TabInfo

	writeMu sync.Mutex
}

func newConn(ws *websocket.Conn, projectPath string) *conn {
	return &conn{
		ws:          ws,
		projectPath: projectPath,
		termMgr:     terminal.NewManager(),
		termIDs:     make(map[string]bool),
	}
}

// send marshals and writes a message to the WebSocket client (thread-safe).
func (c *conn) send(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("ws marshal error: %v", err)
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("ws write error: %v", err)
	}
}

func (c *conn) sendErr(message, code string) {
	c.send(map[string]string{"type": "error", "message": message, "code": code})
}

func (c *conn) run() {
	defer func() {
		c.termMgr.KillAll()
		c.ws.Close()
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
		if c.sessionID == "" {
			c.send(map[string]interface{}{"type": "chat.error", "sessionId": msg.SessionID, "error": "No active session"})
			return
		}
		sessionID := c.sessionID
		go ai.Chat(&ai.ChatContext{
			ProjectPath: projectPath,
			SessionID:   sessionID,
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
			GetOpenTabs: func() []ai.TabInfo {
				c.tabsMu.RLock()
				defer c.tabsMu.RUnlock()
				return c.openTabs
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

	// ── Files ─────────────────────────────────────────────────────────────────

	case "file.read":
		var msg struct{ Path string `json:"path"` }
		json.Unmarshal(raw, &msg) //nolint:errcheck
		fc, err := gofs.ReadFile(msg.Path)
		if err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "file.content", "path": msg.Path, "content": fc.Content, "language": fc.Language})

	case "file.save":
		var msg struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if err := gofs.WriteFile(msg.Path, msg.Content); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "file.saved", "path": msg.Path})

	case "file.tree":
		var msg struct{ Path string `json:"path"` }
		json.Unmarshal(raw, &msg) //nolint:errcheck
		tree, err := gofs.GetTree(msg.Path, 4)
		if err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "file.tree", "tree": tree})

	// ── Git ───────────────────────────────────────────────────────────────────

	case "git.status":
		status, err := gogit.GetStatus(projectPath)
		if err != nil {
			c.sendErr(err.Error(), "GIT_ERROR")
			return
		}
		c.send(map[string]interface{}{"type": "git.status", "status": status})

	case "git.diff":
		var msg struct{ Path string `json:"path"` }
		json.Unmarshal(raw, &msg) //nolint:errcheck
		diff, _ := gogit.GetDiff(projectPath, msg.Path)
		c.send(map[string]interface{}{"type": "git.diff", "path": msg.Path, "diff": diff})

	case "git.log":
		var msg struct{ Limit int `json:"limit"` }
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if msg.Limit <= 0 {
			msg.Limit = 20
		}
		commits, _ := gogit.GetLog(projectPath, msg.Limit)
		c.send(map[string]interface{}{"type": "git.log", "commits": commits})

	// ── GitHub Issues ─────────────────────────────────────────────────────────

	case "github.issues":
		var msg struct{ ProjectPath string `json:"projectPath"` }
		json.Unmarshal(raw, &msg) //nolint:errcheck
		pp := msg.ProjectPath
		if pp == "" {
			pp = projectPath
		}
		go c.handleGitHubIssues(pp)

	// ── Terminals ─────────────────────────────────────────────────────────────

	case "terminal.create":
		var msg struct{ Cwd string `json:"cwd"` }
		json.Unmarshal(raw, &msg) //nolint:errcheck
		id := newID()
		_, err := c.termMgr.Create(id, msg.Cwd,
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
		c.send(map[string]interface{}{"type": "terminal.created", "terminalId": id, "cwd": msg.Cwd})

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
		var msg struct{ TerminalID string `json:"terminalId"` }
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
	remoteURL, err := gogit.GetRemoteOrigin(projectPath)
	if err != nil || remoteURL == "" {
		c.send(map[string]interface{}{"type": "github.issues", "issues": []interface{}{}, "error": "No git remote"})
		return
	}

	// Extract owner/repo from remote URL (HTTPS or SSH)
	remoteURL = strings.TrimSuffix(remoteURL, ".git")
	var owner, repo string
	if idx := strings.Index(remoteURL, "github.com/"); idx != -1 {
		parts := strings.SplitN(remoteURL[idx+11:], "/", 2)
		if len(parts) == 2 {
			owner, repo = parts[0], parts[1]
		}
	} else if idx := strings.Index(remoteURL, "github.com:"); idx != -1 {
		parts := strings.SplitN(remoteURL[idx+11:], "/", 2)
		if len(parts) == 2 {
			owner, repo = parts[0], parts[1]
		}
	}

	if owner == "" || repo == "" {
		c.send(map[string]interface{}{"type": "github.issues", "issues": []interface{}{}, "error": "Not a GitHub repo"})
		return
	}

	issues, err := fetchGitHubIssues(owner, repo)
	if err != nil {
		c.send(map[string]interface{}{"type": "github.issues", "issues": []interface{}{}, "error": err.Error()})
		return
	}
	c.send(map[string]interface{}{"type": "github.issues", "issues": issues})
}

type githubIssue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	HtmlURL   string `json:"htmlUrl"`
	State     string `json:"state"`
	Author    string `json:"author"`
	Labels    []struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"labels"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func fetchGitHubIssues(owner, repo string) ([]githubIssue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=open&per_page=30", owner, repo)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "MyEditor/0.1")
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

func getenv(key string) string {
	return os.Getenv(key)
}

func osGetenv(key string) string {
	return os.Getenv(key)
}
