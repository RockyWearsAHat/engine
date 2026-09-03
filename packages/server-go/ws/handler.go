package ws

// quality:allow-long-file quality:allow-long-function

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
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
	"github.com/engine/server/mesh"
	"github.com/engine/server/quality"
	"github.com/engine/server/remote"
	"github.com/engine/server/runtimecfg"
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
var runAutonomousProject = ai.RunAutonomousProject

// Overridable DB/AI calls for testing error paths.
var (
	dbListSessions             = db.ListSessions
	dbCreateSession            = db.CreateSession
	aiBuildInitialSummary      = ai.BuildInitialSessionSummary
	aiEnsureSessionWorktree    = ai.EnsureSessionWorktree
	aiCleanupSessionWorktreeDB = ai.CleanupSessionWorktree
	// checkpointFn commits+pushes via the git-checkpoint CLI (git.Checkpoint).
	// Injection seam for tests — production never performs a real push.
	checkpointFn = gogit.Checkpoint
	qualityScanFn              = quality.ScanProject
	qualityScanWithProgressFn  = func(projectPath string, maxIssues int, onProgress quality.ProgressCallback) (quality.Report, error) {
		if onProgress == nil {
			return qualityScanFn(projectPath, maxIssues)
		}
		return quality.ScanProjectWithProgress(projectPath, maxIssues, onProgress)
	}
	qualityRefreshFn = quality.RefreshProjectIndex
)

// Overridable repo registry calls for testing.
var (
	repoRegistryLoadFn   = workspace.LoadRegistry
	repoRegistryAddFn    = workspace.AddToRegistry
	repoRegistryRemoveFn = workspace.RemoveFromRegistry
	meshLoadConfigFn     = mesh.LoadConfig
	meshSaveConfigFn     = mesh.SaveConfig
	meshDefaultPathFn    = mesh.DefaultConfigPath
	meshClientHealthFn   = func(selfName string, peer *mesh.Peer, timeout time.Duration) (*mesh.HealthResponse, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return mesh.NewClient(selfName).Health(ctx, peer)
	}
)

var meshActivityMu sync.Mutex

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
	githubWebhookRandReadFn = rand.Read
)

var (
	githubAuthSuccessHookMu sync.RWMutex
	githubAuthSuccessHook   func(token, webhookSecret string)
)

// SetGitHubAuthSuccessHook registers a callback invoked after successful GitHub login.
// Passing nil clears the callback.
func SetGitHubAuthSuccessHook(hook func(token, webhookSecret string)) {
	githubAuthSuccessHookMu.Lock()
	defer githubAuthSuccessHookMu.Unlock()
	githubAuthSuccessHook = hook
}

func notifyGitHubAuthSuccess(token, webhookSecret string) {
	githubAuthSuccessHookMu.RLock()
	hook := githubAuthSuccessHook
	githubAuthSuccessHookMu.RUnlock()
	if hook != nil {
		hook(token, webhookSecret)
	}
}

// TriggerGitHubAuthSuccessHook invokes the currently registered GitHub auth
// success hook, if any. Exported for use in integration tests.
func TriggerGitHubAuthSuccessHook(token, webhookSecret string) {
	notifyGitHubAuthSuccess(token, webhookSecret)
}

func ensureGitHubWebhookSecret() (string, error) {
	if secret := strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET")); secret != "" {
		return secret, nil
	}

	raw := make([]byte, 32)
	if _, err := githubWebhookRandReadFn(raw); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}

	secret := base64.RawURLEncoding.EncodeToString(raw)
	os.Setenv("GITHUB_WEBHOOK_SECRET", secret) //nolint:errcheck
	return secret, nil
}

// DiscordBridge is the subset of the Discord service the WS handler uses.
// Kept narrow so tests can stub it.
type DiscordBridge interface {
	SendDMToOwner(message string) error
	NotifyProjectProgress(projectPath, message string)
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

// GetDiscordBridge returns the currently registered DiscordBridge (may be nil).
func GetDiscordBridge() DiscordBridge {
	return discordBridge
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

// ServeWS upgrades an HTTP request to a WebSocket connection and spawns a message handler goroutine.
// Side effects: authenticates against localAuthToken (if set), spawns a persistent goroutine that runs until
// the connection closes or an error occurs. That goroutine dispatches incoming messages, which may trigger
// file I/O, database writes, git operations, and AI chat orchestration. Errors during upgrade are logged;
// the connection is closed on failures. This function does not return errors — callers must assume the
// connection state is managed by the spawned goroutine.
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
	chatCancelFns map[string]chan struct{} // keyed by sessionID

	writeMu sync.Mutex

	// testObserverMu guards testObservers, which accumulate per-session output.
	testObserverMu sync.Mutex
	testObservers  map[string]*ai.TestObserver // keyed by sessionID
}

type runtimeConfig struct {
	GitHubToken           *string `json:"githubToken"`
	GitHubOwner           *string `json:"githubOwner"`
	GitHubRepo            *string `json:"githubRepo"`
	AnthropicKey          *string `json:"anthropicKey"`
	OpenAIKey             *string `json:"openaiKey"`
	ModelProvider         *string `json:"modelProvider"`
	ActiveTeam            *string `json:"activeTeam"`
	OllamaBaseURL         *string `json:"ollamaBaseUrl"`
	OllamaNumCtx          *string `json:"ollamaNumCtx"`
	LlamacppBaseURL       *string `json:"llamacppBaseUrl"`
	Model                 *string `json:"model"`
	ClonesDir             *string `json:"clonesDir"`
	ContextMaxTokens      *string `json:"contextMaxTokens"`
	ContextRecentWindow   *string `json:"contextRecentWindow"`
	ListDirectoryMaxChars *string `json:"listDirectoryMaxChars"`
	LocalFirst            *string `json:"localFirst"`
	LlamacppModel         *string `json:"llamacppModel"`
	OllamaModel           *string `json:"ollamaModel"`
	PlannerProvider       *string `json:"plannerProvider"`
	PlannerModel          *string `json:"plannerModel"`
	ReviewerProvider      *string `json:"reviewerProvider"`
	ReviewerModel         *string `json:"reviewerModel"`
}

// resolveChatSession resolves the session ID to an active database session, updating connection state.
// If the requested session ID is empty, falls back to the connection's current session ID.
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

// latestScaffoldStatusLine retrieves the most recent scaffold session status for a project.
func latestScaffoldStatusLine(projectPath string) string {
	sessions, err := dbListSessions(projectPath)
	if err != nil {
		return ""
	}
	for _, sess := range sessions {
		if !strings.HasPrefix(strings.ToLower(sess.ID), "scaffold-") {
			continue
		}
		updated := strings.TrimSpace(sess.UpdatedAt)
		if updated == "" {
			updated = strings.TrimSpace(sess.CreatedAt)
		}
		summary := strings.TrimSpace(sess.Summary)
		if summary == "" {
			summary = "no summary yet"
		}
		return fmt.Sprintf("latest scaffold: %s | updated: %s | messages: %d | %s", shortID(sess.ID), updated, sess.MessageCount, truncate(summary, 160))
	}
	return ""
}

// shortID truncates an ID to its first 8 characters if longer than 8 chars.
func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// truncate trims a string to at most n runes, appending "..." if truncated.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// promptWithProjectStatusContext decorates a user prompt with project and session context from Discord.
func promptWithProjectStatusContext(projectPath, prompt string) string {
	direction := strings.TrimSpace(ai.EnsureProjectDirection(projectPath))
	status := latestScaffoldStatusLine(projectPath)
	if direction == "" && status == "" {
		return prompt
	}
	repoName := strings.TrimSpace(filepath.Base(projectPath))
	if repoName == "" || repoName == "." {
		repoName = "project"
	}
	lines := []string{
		"Discord project context:",
		fmt.Sprintf("- Project: %s", repoName),
	}
	if direction != "" {
		lines = append(lines, "- Persistent project direction: "+direction)
	}
	if status != "" {
		lines = append(lines, "- "+status)
	}
	lines = append(lines, "- This message came from the project channel, so answer with awareness of existing project/session history and the persistent project direction. Do not claim setup is just starting if scaffold sessions already exist.", "", "User message:", prompt)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// newConn initializes a new per-connection state for a WebSocket client.
func newConn(ws *websocket.Conn, projectPath string) *conn {
	return &conn{
		ws:              ws,
		projectPath:     projectPath,
		done:            make(chan struct{}),
		termMgr:         terminal.NewManager(),
		termIDs:         make(map[string]bool),
		approvalWaiters: make(map[string]chan bool),
		chatCancelFns:   make(map[string]chan struct{}),
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

// sendErr marshals and sends an error message to the client.
func (c *conn) sendErr(message, code string) {
	c.send(map[string]string{"type": "error", "message": message, "code": code})
}

// requestApproval sends an approval request to the client and waits for a response (with timeout).
// Returns (allow, nil) if approved/denied before timeout, or (false, error) if timeout occurs.
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

// resolveApproval resolves a pending approval request by ID.
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

// resolveAllApprovals resolves all pending approval requests with the same response.
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

// refreshProjectQualityAndReply refreshes the quality index for the project and sends a file operation response.
// This encapsulates the side-effect chain of: async quality refresh → response send.
func (c *conn) refreshProjectQualityAndReply(msgType, path string) {
	go qualityRefreshFn(c.projectPath)
	c.send(map[string]any{"type": msgType, "path": path})
}

// sendCommitResultAndRefresh sends the commit result and follows up with status and log updates.
// This encapsulates the side-effect chain of: commit result → status fetch → log fetch.
func (c *conn) sendCommitResultAndRefresh(hash, message, projectPath string) {
	c.send(map[string]any{"type": "git.commit.result", "ok": true, "hash": hash, "message": message})
	if status, err := gogit.GetStatus(projectPath); err == nil {
		c.send(map[string]any{"type": "git.status", "status": status})
	}
	if commits, err := gogit.GetLog(projectPath, 8); err == nil {
		c.send(map[string]any{"type": "git.log", "commits": commits})
	}
}

// initializeSessionData builds the initial summary and ensures the session worktree, updating paths as needed.
// Returns the resolved project path (which may differ from the input if worktree was created).
func (c *conn) initializeSessionData(sessionID, projectPath string) string {
	if summary := aiBuildInitialSummary(projectPath); summary != "" {
		db.UpdateSessionSummary(sessionID, summary) //nolint:errcheck
	}
	if wtPath, wtErr := aiEnsureSessionWorktree(sessionID, projectPath); wtErr == nil && wtPath != projectPath {
		projectPath = wtPath
		c.projectPath = wtPath
		db.UpdateSessionProjectPath(sessionID, wtPath) //nolint:errcheck
	}
	return projectPath
}

// run manages the per-connection message loop. It reads incoming WebSocket messages,
// parses the message type, and dispatches to handler functions. Handles panics to prevent
// crashes. Closes the connection and signals all send goroutines when the loop exits.
// This function runs in its own goroutine spawned by ServeWS and blocks indefinitely
// until the connection is closed or a read error occurs.
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

// dispatch routes incoming WebSocket messages to handler functions based on message type.
// Side effects: this function and its callees may perform file I/O, database writes, git operations,
// spawn AI chat goroutines, manage terminals, and send back multiple response messages via c.send().
// All errors are communicated back to the client via sendErr() or explicit error response payloads.
// The function does not return errors — the connection remains open after handling each message.
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
			msg.Path = c.initializeSessionData(id, msg.Path)
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
		projectPath = c.initializeSessionData(id, projectPath)
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
		chatPrompt := promptWithProjectStatusContext(projectPath, msg.Content)

		// One ingress, one brain: if an orchestrator is already active for this
		// project, treat new chat input as a continuation directive.
		if handle := ai.GetOrchestratorHandle(projectPath); handle != nil {
			handle.Redirect(chatPrompt)
			c.send(map[string]any{"type": "chat.started", "sessionId": sessionID})
			c.send(map[string]any{
				"type":      "chat.notice",
				"sessionId": sessionID,
				"notice":    "Directive queued into active orchestrator.",
			})
			c.send(map[string]any{"type": "chat.chunk", "sessionId": sessionID, "content": "", "done": true})
			return
		}

		// Create a cancel channel for this request so the client can stop it.
		cancelCh := make(chan struct{})
		c.chatCancelMu.Lock()
		if old, ok := c.chatCancelFns[sessionID]; ok {
			close(old) // cancel any previous in-flight request for this session
		}
		c.chatCancelFns[sessionID] = cancelCh
		c.chatCancelMu.Unlock()

		c.send(map[string]any{"type": "chat.started", "sessionId": sessionID})
		go func() {
			defer func() {
				// Remove cancel fn when goroutine exits.
				c.chatCancelMu.Lock()
				if fn, ok := c.chatCancelFns[sessionID]; ok {
					// Only delete if it's still the same channel (not replaced by a newer request).
					if fn == cancelCh {
					delete(c.chatCancelFns, sessionID)
					}
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

			state, orchestratorErr := runAutonomousProject(ai.OrchestratorConfig{
				ProjectPath:     projectPath,
				Brief:           chatPrompt,
				SessionIDPrefix: fmt.Sprintf("chat-%s", shortID(sessionID)),
				Cancel:          cancelCh,
				ChatFn:          runAIChat,
				// InteractiveChat is the conversational executor. When the
				// orchestrator triages this brief as a chat (not a build), it
				// hands back here so the reply streams to the client with the
				// full interactive surface — tokens, tool calls, approvals,
				// session updates — exactly like a direct interactive turn.
				InteractiveChat: func(brief string, cancel <-chan struct{}) {
					runAIChat(&ai.ChatContext{
						ProjectPath: projectPath,
						SessionID:   sessionID,
						Cancel:      cancel,
						Role:        ai.RoleInteractive,
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
						DiscordProgress: func(message string) error {
							if discordBridge == nil {
								return fmt.Errorf("Discord not configured")
							}
							discordBridge.NotifyProjectProgress(projectPath, message)
							return nil
						},
					}, brief)
				},
				OnPhase: func(phase, detail string) {
					c.send(map[string]any{
						"type":      "chat.notice",
						"sessionId": sessionID,
						"notice":    fmt.Sprintf("Orchestrator %s: %s", phase, detail),
					})
				},
				OnError: func(errMsg string) {
					if strings.TrimSpace(errMsg) == "" {
						return
					}
					c.send(map[string]any{"type": "chat.error", "sessionId": sessionID, "error": errMsg})
				},
			})
			if orchestratorErr != nil {
				if strings.Contains(strings.ToLower(orchestratorErr.Error()), "cancel") {
					c.send(map[string]any{"type": "chat.notice", "sessionId": sessionID, "notice": "Orchestrator cancelled."})
					c.send(map[string]any{"type": "chat.chunk", "sessionId": sessionID, "content": "", "done": true})
					return
				}
				c.send(map[string]any{"type": "chat.error", "sessionId": sessionID, "error": orchestratorErr.Error()})
				c.send(map[string]any{"type": "chat.chunk", "sessionId": sessionID, "content": "", "done": true})
				return
			}
			// A conversational route already streamed its reply and its own
			// terminal done chunk through InteractiveChat — emitting a build
			// summary here would be wrong. Only the build pipeline reports a
			// completion summary.
			if state != nil && state.Conversational {
				return
			}
			summary := "Orchestrator completed."
			if state != nil {
				summary = fmt.Sprintf("Orchestrator completed (%d steps, %d iterations).", len(state.Plan), state.OuterIterations)
			}

			// Session lifecycle concludes here: the build pipeline reached a
			// real success (not conversational, not cancelled, not errored).
			// Fold the session's isolated worktree branch back into base and
			// push it — the same "always work back to main" default the
			// worklist path (task_api.go) gets on a successful item, so an
			// interactive build session doesn't strand commits the way the
			// dead worktree-cleanup code used to.
			//
			// merge=true always targets CleanupSessionWorktree's own notion
			// of the baseline (git.GetBaseBranch) — there is no per-session
			// branch-override surface in the ws protocol to respect instead.
			//
			// NOTE: repoPath contract. CleanupSessionWorktree(sessionID,
			// repoPath, merge) requires repoPath to be the ORIGIN repo (it
			// recomputes the worktree location from it internally) — not a
			// worktree path. When EnsureSessionWorktree substituted a real
			// worktree for this session, projectPath here IS that worktree
			// path, which this call cannot distinguish from an origin path.
			// That mirrors the pre-existing "session.cleanup" WS handler
			// above, which has the exact same call shape and the same gap:
			// neither has anywhere to recover the true origin path, because
			// db.Session only stores the (possibly-substituted) ProjectPath.
			// Verified failure mode in that case: CleanupSessionWorktree's
			// internal `git checkout <base>` runs from inside the worktree
			// and fails with "'<base>' is already used by worktree at
			// '<origin>'", so the merge — and this checkpoint — never runs
			// for a session that actually got a real worktree. Sessions that
			// fell back to the origin path (no worktree created) are
			// unaffected. Flagged, not silently patched here — fixing it
			// needs either a stored origin-path field on db.Session or a
			// `git rev-parse --git-common-dir`-based resolver shared by both
			// call sites.
			if cleanupErr := aiCleanupSessionWorktreeDB(sessionID, projectPath, true); cleanupErr != nil {
				log.Printf("[engine] ws: session %s worktree merge-back failed: %v", sessionID, cleanupErr)
				c.send(map[string]any{"type": "chat.notice", "sessionId": sessionID, "notice": fmt.Sprintf("Session merge-back failed: %v", cleanupErr)})
			} else if ckErr := checkpointFn(projectPath, fmt.Sprintf("session %s: %s", sessionID, summary), true); ckErr != nil {
				log.Printf("[engine] ws: session %s checkpoint/push failed: %v", sessionID, ckErr)
				c.send(map[string]any{"type": "chat.notice", "sessionId": sessionID, "notice": fmt.Sprintf("Checkpoint/push failed: %v", ckErr)})
			}

			c.send(map[string]any{"type": "chat.chunk", "sessionId": sessionID, "content": summary, "done": false})
			c.send(map[string]any{"type": "chat.chunk", "sessionId": sessionID, "content": "", "done": true})
		}()

	case "chat.stop":
		var msg struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil || msg.SessionID == "" {
			return
		}
		c.chatCancelMu.Lock()
		if cancelCh, ok := c.chatCancelFns[msg.SessionID]; ok {
			close(cancelCh)
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
		c.refreshProjectQualityAndReply("file.saved", msg.Path)

	case "file.create":
		var msg struct {
			Path string `json:"path"`
		}
		json.Unmarshal(raw, &msg) //nolint:errcheck
		if err := gofs.WriteFile(msg.Path, ""); err != nil {
			c.sendErr(err.Error(), "FILE_ERROR")
			return
		}
		c.refreshProjectQualityAndReply("file.created", msg.Path)

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
		c.sendCommitResultAndRefresh(hash, message, projectPath)

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
		if err := applyRuntimeConfig(projectPath, msg.Config); err != nil {
			c.sendErr("Failed to apply runtime config: "+err.Error(), "CONFIG_SYNC_ERROR")
			return
		}

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

	// ── Project network mesh control ───────────────────────────────────────

	case "mesh.config.get":
		c.handleMeshConfigGet()

	case "mesh.config.set":
		var msg struct {
			SelfName      string `json:"selfName"`
			ListenAddr    string `json:"listenAddr"`
			SelfOllamaURL string `json:"selfOllamaURL"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		c.handleMeshConfigSet(msg.SelfName, msg.ListenAddr, msg.SelfOllamaURL)

	case "mesh.peer.upsert":
		var msg struct {
			Peer struct {
				Name      string   `json:"name"`
				Address   string   `json:"address"`
				Secret    string   `json:"secret"`
				Roles     []string `json:"roles"`
				OllamaURL string   `json:"ollamaURL"`
			} `json:"peer"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		c.handleMeshPeerUpsert(msg.Peer.Name, msg.Peer.Address, msg.Peer.Secret, msg.Peer.Roles, msg.Peer.OllamaURL)

	case "mesh.peer.remove":
		var msg struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		c.handleMeshPeerRemove(msg.Name)

	case "mesh.health.scan":
		var msg struct {
			TimeoutMs int `json:"timeoutMs"`
		}
		_ = json.Unmarshal(raw, &msg)
		c.handleMeshHealthScan(msg.TimeoutMs)

	case "mesh.activity.get":
		var msg struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(raw, &msg)
		c.handleMeshActivityGet(msg.Limit)

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

	case "quality.report.get":
		var msg struct {
			ProjectPath string `json:"projectPath"`
			MaxIssues   int    `json:"maxIssues"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.send(map[string]any{"type": "quality.report", "error": "Bad payload"})
			return
		}
		targetProjectPath := strings.TrimSpace(msg.ProjectPath)
		if targetProjectPath == "" {
			targetProjectPath = projectPath
		}
		report, err := qualityScanWithProgressFn(targetProjectPath, msg.MaxIssues, func(progress quality.ScanProgress) {
			c.send(map[string]any{"type": "quality.scan.progress", "progress": progress})
		})
		if err != nil {
			c.send(map[string]any{"type": "quality.report", "error": err.Error()})
			return
		}
		c.send(map[string]any{"type": "quality.report", "report": report})

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
		teamName := strings.TrimSpace(msg.Team)
		if teamName != "" {
			_, resolvedProvider, resolvedModel, ok := ai.ResolveTeamOrchestratorModel(projectPath, teamName)
			if !ok {
				c.sendErr("Unknown team or missing orchestrator model in .engine/config.yaml", "TEAM_CONFIG_ERROR")
				return
			}
			// Server is authoritative: selecting a team must apply that team's
			// orchestrator runtime, not a caller-provided provider/model pair.
			msg.Provider = resolvedProvider
			msg.Model = resolvedModel
		}
		if teamName == "" {
			hasProvider := strings.TrimSpace(msg.Provider) != ""
			hasModel := strings.TrimSpace(msg.Model) != ""
			if hasProvider != hasModel {
				c.sendErr("provider and model must be set together when team is omitted", "BAD_PAYLOAD")
				return
			}
		}
		if strings.TrimSpace(msg.Provider) != "" {
			msg.Provider = strings.TrimSpace(msg.Provider)
		}
		if strings.TrimSpace(msg.Model) != "" {
			msg.Model = strings.TrimSpace(msg.Model)
		}
		if strings.TrimSpace(msg.Team) != "" {
			msg.Team = strings.TrimSpace(msg.Team)
		}
		if _, err := runtimecfg.Apply(projectPath, runtimecfg.Patch{
			ModelProvider: &msg.Provider,
			Model:         &msg.Model,
			ActiveTeam:    &msg.Team,
		}); err != nil {
			c.sendErr("Failed to persist team runtime config: "+err.Error(), "TEAM_CONFIG_ERROR")
			return
		}
		c.send(map[string]any{
			"type": "engine.team.updated",
			"team": msg.Team,
		})

	case "llama.fleet.scan":
		var msg struct {
			WriteFile bool `json:"writeFile"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendErr("Bad payload", "BAD_PAYLOAD")
			return
		}
		result, err := runLlamaFleetScan(projectPath, msg.WriteFile)
		if err != nil {
			c.send(map[string]any{
				"type":  "llama.fleet.scan.result",
				"error": err.Error(),
			})
			return
		}
		c.send(map[string]any{
			"type":   "llama.fleet.scan.result",
			"result": result,
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

// handleRepoList retrieves and sends the list of registered repositories.
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

// handleRepoAdd registers a new repository by URL or path.
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

// handleRepoRemove unregisters a repository from the registry.
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
// handleRemotePairCodeGenerate generates a one-time pairing code for remote access.
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

func normalizeMeshRoles(roles []string) []string {
	if len(roles) == 0 {
		return []string{}
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		clean := strings.TrimSpace(role)
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, clean)
	}
	return out
}

func meshPeerPayload(peer mesh.Peer) map[string]any {
	return map[string]any{
		"name":      strings.TrimSpace(peer.Name),
		"address":   strings.TrimSpace(peer.Address),
		"roles":     normalizeMeshRoles(peer.Roles),
		"ollamaURL": strings.TrimSpace(peer.OllamaURL),
		"hasSecret": strings.TrimSpace(peer.Secret) != "",
	}
}

func meshConfigPayload(cfg *mesh.Config) map[string]any {
	if cfg == nil {
		cfg = &mesh.Config{}
	}
	peers := make([]map[string]any, 0, len(cfg.Peers))
	for _, peer := range cfg.Peers {
		peers = append(peers, meshPeerPayload(peer))
	}
	return map[string]any{
		"selfName":      strings.TrimSpace(cfg.SelfName),
		"listenAddr":    strings.TrimSpace(cfg.ListenAddr),
		"selfOllamaURL": strings.TrimSpace(cfg.SelfOllamaURL),
		"peers":         peers,
		"configPath":    meshDefaultPathFn(),
	}
}

func meshActivityPath(projectPath string) string {
	root := strings.TrimSpace(projectPath)
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".engine", "mesh-activity.json")
}

func loadMeshActivity(projectPath string) ([]map[string]any, error) {
	path := meshActivityPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return []map[string]any{}, nil
	}
	var records []map[string]any
	if err := json.Unmarshal(data, &records); err != nil {
		return []map[string]any{}, err
	}
	if records == nil {
		return []map[string]any{}, nil
	}
	return records, nil
}

func appendMeshActivity(projectPath string, entry map[string]any) error {
	path := meshActivityPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	meshActivityMu.Lock()
	defer meshActivityMu.Unlock()
	records, err := loadMeshActivity(projectPath)
	if err != nil {
		records = []map[string]any{}
	}
	records = append(records, entry)
	if len(records) > 500 {
		records = records[len(records)-500:]
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (c *conn) logMeshActivity(action, target, status, message, errText string, resolved bool) {
	entry := map[string]any{
		"id":      newID(),
		"at":      time.Now().UTC().Format(time.RFC3339),
		"action":  strings.TrimSpace(action),
		"target":  strings.TrimSpace(target),
		"status":  strings.TrimSpace(status),
		"message": strings.TrimSpace(message),
	}
	if strings.TrimSpace(errText) != "" {
		entry["error"] = strings.TrimSpace(errText)
	}
	if resolved {
		entry["resolved"] = true
	}
	if err := appendMeshActivity(c.projectPath, entry); err != nil {
		log.Printf("[engine] ws: mesh activity log append error: %v", err)
	}
}

func (c *conn) handleMeshActivityGet(limit int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	meshActivityMu.Lock()
	records, err := loadMeshActivity(c.projectPath)
	meshActivityMu.Unlock()
	if err != nil {
		c.sendErr("Failed to load mesh activity: "+err.Error(), "MESH_ACTIVITY_ERROR")
		return
	}
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	c.send(map[string]any{"type": "mesh.activity", "records": records})
}

func (c *conn) handleMeshConfigGet() {
	c.logMeshActivity("mesh.config.get", "config", "started", "Loading project network config", "", false)
	cfg, err := meshLoadConfigFn("")
	if err != nil {
		c.logMeshActivity("mesh.config.get", "config", "error", "Failed to load project network config", err.Error(), false)
		c.sendErr("Failed to load mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	c.logMeshActivity("mesh.config.get", "config", "ok", "Project network config loaded", "", false)
	c.send(map[string]any{"type": "mesh.config", "config": meshConfigPayload(cfg)})
}

func (c *conn) handleMeshConfigSet(selfName, listenAddr, selfOllamaURL string) {
	c.logMeshActivity("mesh.config.set", "config", "started", "Saving project network config", "", false)
	cfg, err := meshLoadConfigFn("")
	if err != nil {
		c.logMeshActivity("mesh.config.set", "config", "error", "Failed to load config before save", err.Error(), false)
		c.sendErr("Failed to load mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	if cfg == nil {
		cfg = &mesh.Config{}
	}
	if strings.TrimSpace(selfName) != "" {
		cfg.SelfName = strings.TrimSpace(selfName)
	}
	if strings.TrimSpace(listenAddr) != "" {
		cfg.ListenAddr = strings.TrimSpace(listenAddr)
	}
	if strings.TrimSpace(selfOllamaURL) != "" {
		cfg.SelfOllamaURL = strings.TrimSpace(selfOllamaURL)
	}
	if strings.TrimSpace(cfg.SelfName) == "" {
		c.logMeshActivity("mesh.config.set", "config", "error", "Config save rejected: selfName missing", "selfName is required", false)
		c.sendErr("selfName is required", "BAD_PAYLOAD")
		return
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = ":24445"
	}
	if err := meshSaveConfigFn("", cfg); err != nil {
		c.logMeshActivity("mesh.config.set", "config", "error", "Failed to persist project network config", err.Error(), false)
		c.sendErr("Failed to save mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	c.logMeshActivity("mesh.config.set", "config", "ok", "Project network config saved", "", true)
	c.send(map[string]any{"type": "mesh.config.saved", "config": meshConfigPayload(cfg)})
}

func (c *conn) handleMeshPeerUpsert(name, address, secret string, roles []string, ollamaURL string) {
	cleanName := strings.TrimSpace(name)
	cleanAddr := strings.TrimSpace(address)
	c.logMeshActivity("mesh.peer.upsert", cleanName, "started", "Saving network agent node", "", false)
	if cleanName == "" || cleanAddr == "" {
		c.logMeshActivity("mesh.peer.upsert", cleanName, "error", "Node save rejected: name/address required", "peer.name and peer.address are required", false)
		c.sendErr("peer.name and peer.address are required", "BAD_PAYLOAD")
		return
	}

	cfg, err := meshLoadConfigFn("")
	if err != nil {
		c.logMeshActivity("mesh.peer.upsert", cleanName, "error", "Failed to load config before node save", err.Error(), false)
		c.sendErr("Failed to load mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	if cfg == nil {
		cfg = &mesh.Config{}
	}
	if strings.TrimSpace(cfg.SelfName) == "" {
		c.logMeshActivity("mesh.peer.upsert", cleanName, "error", "Node save blocked: selfName not configured", "Set mesh selfName before adding peers", false)
		c.sendErr("Set mesh selfName before adding peers", "MESH_CONFIG_ERROR")
		return
	}

	cleanRoles := normalizeMeshRoles(roles)
	cleanSecret := strings.TrimSpace(secret)
	cleanOllamaURL := strings.TrimSpace(ollamaURL)

	updated := false
	for i := range cfg.Peers {
		if !strings.EqualFold(strings.TrimSpace(cfg.Peers[i].Name), cleanName) {
			continue
		}
		cfg.Peers[i].Name = cleanName
		cfg.Peers[i].Address = cleanAddr
		cfg.Peers[i].Roles = cleanRoles
		cfg.Peers[i].OllamaURL = cleanOllamaURL
		if cleanSecret != "" {
			cfg.Peers[i].Secret = cleanSecret
		}
		updated = true
		break
	}

	if !updated {
		if cleanSecret == "" {
			c.logMeshActivity("mesh.peer.upsert", cleanName, "error", "New node rejected: missing shared secret", "peer.secret is required when adding a new peer", false)
			c.sendErr("peer.secret is required when adding a new peer", "BAD_PAYLOAD")
			return
		}
		cfg.Peers = append(cfg.Peers, mesh.Peer{
			Name:      cleanName,
			Address:   cleanAddr,
			Secret:    cleanSecret,
			Roles:     cleanRoles,
			OllamaURL: cleanOllamaURL,
		})
	}

	if err := meshSaveConfigFn("", cfg); err != nil {
		c.logMeshActivity("mesh.peer.upsert", cleanName, "error", "Failed to persist node", err.Error(), false)
		c.sendErr("Failed to save mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	c.logMeshActivity("mesh.peer.upsert", cleanName, "ok", "Network agent node saved", "", true)
	c.send(map[string]any{"type": "mesh.config.saved", "config": meshConfigPayload(cfg)})
}

func (c *conn) handleMeshPeerRemove(name string) {
	cleanName := strings.TrimSpace(name)
	c.logMeshActivity("mesh.peer.remove", cleanName, "started", "Removing network agent node", "", false)
	if cleanName == "" {
		c.logMeshActivity("mesh.peer.remove", cleanName, "error", "Node removal rejected: name required", "name is required", false)
		c.sendErr("name is required", "BAD_PAYLOAD")
		return
	}
	cfg, err := meshLoadConfigFn("")
	if err != nil {
		c.logMeshActivity("mesh.peer.remove", cleanName, "error", "Failed to load config before node removal", err.Error(), false)
		c.sendErr("Failed to load mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	if cfg == nil || len(cfg.Peers) == 0 {
		c.logMeshActivity("mesh.peer.remove", cleanName, "error", "Node removal failed: node not found", "Peer not found", false)
		c.sendErr("Peer not found", "NOT_FOUND")
		return
	}

	nextPeers := make([]mesh.Peer, 0, len(cfg.Peers))
	removed := false
	for _, peer := range cfg.Peers {
		if strings.EqualFold(strings.TrimSpace(peer.Name), cleanName) {
			removed = true
			continue
		}
		nextPeers = append(nextPeers, peer)
	}
	if !removed {
		c.logMeshActivity("mesh.peer.remove", cleanName, "error", "Node removal failed: node not found", "Peer not found", false)
		c.sendErr("Peer not found", "NOT_FOUND")
		return
	}
	cfg.Peers = nextPeers
	if err := meshSaveConfigFn("", cfg); err != nil {
		c.logMeshActivity("mesh.peer.remove", cleanName, "error", "Failed to persist node removal", err.Error(), false)
		c.sendErr("Failed to save mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	c.logMeshActivity("mesh.peer.remove", cleanName, "ok", "Network agent node removed", "", true)
	c.send(map[string]any{"type": "mesh.config.saved", "config": meshConfigPayload(cfg)})
}

func (c *conn) handleMeshHealthScan(timeoutMs int) {
	c.logMeshActivity("mesh.health.scan", "all", "started", "Scanning network agent nodes", "", false)
	cfg, err := meshLoadConfigFn("")
	if err != nil {
		c.logMeshActivity("mesh.health.scan", "all", "error", "Failed to load config before health scan", err.Error(), false)
		c.sendErr("Failed to load mesh config: "+err.Error(), "MESH_CONFIG_ERROR")
		return
	}
	if cfg == nil || len(cfg.Peers) == 0 {
		c.logMeshActivity("mesh.health.scan", "all", "ok", "No network nodes configured", "", true)
		c.send(map[string]any{"type": "mesh.health.results", "results": []map[string]any{}})
		return
	}

	if timeoutMs <= 0 {
		timeoutMs = 4000
	}
	if timeoutMs < 500 {
		timeoutMs = 500
	}
	if timeoutMs > 30000 {
		timeoutMs = 30000
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	results := make([]map[string]any, len(cfg.Peers))
	var wg sync.WaitGroup
	for i := range cfg.Peers {
		peer := cfg.Peers[i]
		wg.Add(1)
		go func(index int, p mesh.Peer) {
			defer wg.Done()
			health, healthErr := meshClientHealthFn(strings.TrimSpace(cfg.SelfName), &p, timeout)
			if healthErr != nil {
				c.logMeshActivity("mesh.health.scan", strings.TrimSpace(p.Name), "error", "Agent node offline", healthErr.Error(), false)
				results[index] = map[string]any{
					"peer":  meshPeerPayload(p),
					"ok":    false,
					"error": healthErr.Error(),
				}
				return
			}
			c.logMeshActivity("mesh.health.scan", strings.TrimSpace(p.Name), "ok", "Agent node healthy", "", true)
			results[index] = map[string]any{
				"peer": meshPeerPayload(p),
				"ok":   true,
				"health": map[string]any{
					"name":      strings.TrimSpace(health.Name),
					"os":        strings.TrimSpace(health.OS),
					"arch":      strings.TrimSpace(health.Arch),
					"cpus":      health.CPUs,
					"roles":     normalizeMeshRoles(health.Roles),
					"ollamaURL": strings.TrimSpace(health.OllamaURL),
					"upAt":      strings.TrimSpace(health.UpAt),
				},
			}
		}(i, peer)
	}
	wg.Wait()
	c.logMeshActivity("mesh.health.scan", "all", "ok", "Network scan complete", "", true)
	c.send(map[string]any{"type": "mesh.health.results", "results": results})
}

// handleGitHubIssues fetches GitHub issues for a project and sends them to the client.
func (c *conn) handleGitHubIssues(projectPath string) {
	owner, repo, overrideConfigured := githubRepoOverride(projectPath)
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

	issues, err := fetchGitHubIssues(projectPath, owner, repo)
	if err != nil {
		c.send(map[string]any{"type": "github.issues", "issues": []any{}, "error": err.Error()})
		return
	}
	c.send(map[string]any{"type": "github.issues", "issues": issues})
}

// handleGitHubUser retrieves the authenticated GitHub user and sends info to the client.
func (c *conn) handleGitHubUser() {
	user, err := fetchGitHubUser(c.projectPath)
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
// handleGitHubAuthStart initiates a GitHub OAuth device flow and polls for the access token.
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
	webhookSecret, err := ensureGitHubWebhookSecret()
	if err != nil {
		c.send(map[string]any{"type": "github.auth.error", "error": err.Error()})
		return
	}

	// Activate the token immediately so the rest of the server uses it.
	if _, applyErr := runtimecfg.Apply(c.projectPath, runtimecfg.Patch{GitHubToken: &tok.AccessToken}); applyErr != nil {
		c.send(map[string]any{"type": "github.auth.error", "error": applyErr.Error()})
		return
	}
	notifyGitHubAuthSuccess(tok.AccessToken, webhookSecret)

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

func applyRuntimeConfig(projectPath string, cfg runtimeConfig) error {
	_, err := runtimecfg.Apply(projectPath, runtimecfg.Patch{
		GitHubToken:           cfg.GitHubToken,
		GitHubOwner:           cfg.GitHubOwner,
		GitHubRepo:            cfg.GitHubRepo,
		AnthropicKey:          cfg.AnthropicKey,
		OpenAIKey:             cfg.OpenAIKey,
		ModelProvider:         cfg.ModelProvider,
		ActiveTeam:            cfg.ActiveTeam,
		OllamaBaseURL:         cfg.OllamaBaseURL,
		OllamaNumCtx:          cfg.OllamaNumCtx,
		LlamacppBaseURL:       cfg.LlamacppBaseURL,
		Model:                 cfg.Model,
		ClonesDir:             cfg.ClonesDir,
		ContextMaxTokens:      cfg.ContextMaxTokens,
		ContextRecentWindow:   cfg.ContextRecentWindow,
		ListDirectoryMaxChars: cfg.ListDirectoryMaxChars,
		LocalFirst:            cfg.LocalFirst,
		LlamacppModel:         cfg.LlamacppModel,
		OllamaModel:           cfg.OllamaModel,
		PlannerProvider:       cfg.PlannerProvider,
		PlannerModel:          cfg.PlannerModel,
		ReviewerProvider:      cfg.ReviewerProvider,
		ReviewerModel:         cfg.ReviewerModel,
	})
	return err
}

func fetchGitHubUser(projectPath string) (*githubUser, error) {
	token := githubToken(projectPath)
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

func fetchGitHubIssues(projectPath, owner, repo string) ([]githubIssue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=open&per_page=30", owner, repo)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Engine/0.1")
	if token := githubToken(projectPath); token != "" {
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
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
		PullRequest any    `json:"pull_request"`
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

func githubToken(projectPath string) string {
	if cfg, err := runtimecfg.Load(projectPath); err == nil && strings.TrimSpace(cfg.GitHubToken) != "" {
		return strings.TrimSpace(cfg.GitHubToken)
	}
	return os.Getenv("GITHUB_TOKEN")
}

func githubRepoOverride(projectPath string) (string, string, bool) {
	owner := ""
	repo := ""
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		owner = strings.TrimSpace(cfg.GitHubOwner)
		repo = strings.TrimSpace(cfg.GitHubRepo)
	}
	if owner == "" {
		owner = strings.TrimSpace(os.Getenv("ENGINE_GITHUB_OWNER"))
	}
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("ENGINE_GITHUB_REPO"))
	}
	return owner, repo, owner != "" || repo != ""
}
