package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/db"
	gofs "github.com/engine/server/fs"
	gogit "github.com/engine/server/git"
	gh "github.com/engine/server/github"
	"github.com/engine/server/remote"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	goprocess "github.com/shirou/gopsutil/v3/process"
)

const (
	defaultAnthropicModel = "claude-opus-4-5"
	defaultOpenAIModel    = "gpt-4o"
	defaultOllamaModel    = "llama3.2"
	defaultOllamaBaseURL  = "http://127.0.0.1:11434"
)

var ollamaModelPrefixes = []string{
	"gemma",
	"llama",
	"qwen",
	"mistral",
	"mixtral",
	"deepseek",
	"codellama",
	"phi",
	"command-r",
	"granite",
	"smollm",
	"starcoder",
	"wizard",
	"dolphin",
	"yi",
	"nemotron",
}

var openURLCommand = exec.Command

var saveMessageFn = db.SaveMessage
var trimToTokenBudgetFn = TrimToTokenBudgetAnthropicFormat
var processListFn = goprocess.Processes
var newProcessFn = goprocess.NewProcess
var cloneRepoFn = func(url, dest string) error {
	cmd := exec.Command("git", "clone", url, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}


var (
	machineCredStoreOnce sync.Once
	machineCredStore     *remote.KeychainStore
)

func getMachineCredStore() *remote.KeychainStore {
	machineCredStoreOnce.Do(func() { machineCredStore = remote.NewKeychainStore() })
	return machineCredStore
}

var credStoreGetFn = func(key string) (string, error) { return getMachineCredStore().Get(key) }
var credStoreSetFn = func(key, value string) error    { return getMachineCredStore().Set(key, value) }
var credStoreDelFn = func(key string) error           { return getMachineCredStore().Delete(key) }

// Must be less than OLLAMA_KEEP_ALIVE (default 30m) so the model never expires.
// Exported as a var so tests can override it to a short duration.
var ollamaWarmKeepInterval = 20 * time.Minute

func init() {
	go ollamaWarmKeeper()
}

// ollamaWarmKeeper periodically sends a no-op generate request to Ollama so the
// currently loaded model stays in VRAM between user sessions.
// Uses /api/generate with an empty prompt and keep_alive set — this resets the
// expiry timer without actually running inference.
func ollamaWarmKeeper() {
	for {
		time.Sleep(ollamaWarmKeepInterval)
		ollamaPing()
	}
}

// ollamaPing sends one warm-keep ping to Ollama. Extracted for testability.
func ollamaPing() {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	rootURL := ollamaRootURL(baseURL)

	keepAlive := os.Getenv("OLLAMA_KEEP_ALIVE")
	if keepAlive == "" {
		keepAlive = "30m"
	}

	// Only ping if a model is actually loaded right now.
	model := firstOllamaModel(rootURL+"/api/ps", "models", "name")
	if model == "" {
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"model":      model,
		"prompt":     "",
		"keep_alive": keepAlive,
	})
	resp, err := http.Post(rootURL+"/api/generate", "application/json", bytes.NewReader(payload))
	if err == nil {
		resp.Body.Close()
	}
}

func inferredProviderForModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return "ollama"
	}
	if strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1-") ||
		strings.HasPrefix(lower, "o3-") || strings.HasPrefix(lower, "o4-") {
		return "openai"
	}
	if strings.HasPrefix(lower, "claude") {
		return "anthropic"
	}
	if looksLikeOllamaModel(lower) {
		return "ollama"
	}
	return "anthropic"
}

func looksLikeOllamaModel(lowerModel string) bool {
	if strings.Contains(lowerModel, ":") {
		return true
	}
	for _, prefix := range ollamaModelPrefixes {
		if strings.HasPrefix(lowerModel, prefix) {
			return true
		}
	}
	return false
}

func resolveProvider(explicitProvider string, model string) string {
	switch strings.ToLower(strings.TrimSpace(explicitProvider)) {
	case "anthropic", "openai", "ollama":
		return strings.ToLower(strings.TrimSpace(explicitProvider))
	case "", "auto":
		return inferredProviderForModel(model)
	default:
		return inferredProviderForModel(model)
	}
}

func defaultModelForProvider(provider string) string {
	switch provider {
	case "openai":
		return defaultOpenAIModel
	case "ollama":
		return defaultOllamaModel
	default:
		return defaultAnthropicModel
	}
}

func ollamaChatCompletionsURL(baseURL string) string {
	normalized := ollamaRootURL(baseURL)
	if strings.HasSuffix(normalized, "/chat/completions") {
		return normalized
	}
	return normalized + "/v1/chat/completions"
}

func ollamaRootURL(baseURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if normalized == "" {
		return defaultOllamaBaseURL
	}
	switch {
	case strings.HasSuffix(normalized, "/v1/chat/completions"):
		return strings.TrimSuffix(normalized, "/v1/chat/completions")
	case strings.HasSuffix(normalized, "/v1"):
		return strings.TrimSuffix(normalized, "/v1")
	default:
		return normalized
	}
}

func detectOllamaModel(baseURL string) string {
	rootURL := ollamaRootURL(baseURL)
	if model := firstOllamaModel(rootURL+"/api/ps", "models", "name"); model != "" {
		return model
	}
	if model := firstOllamaModel(rootURL+"/v1/models", "data", "id"); model != "" {
		return model
	}
	return ""
}

func firstOllamaModel(url string, listKey string, nameKey string) string {
	resp, err := http.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	items, _ := payload[listKey].([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if entry == nil {
			continue
		}
		if name, ok := entry[nameKey].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// ChatContext carries callbacks for streaming responses to the WebSocket client.
type ChatContext struct {
	ProjectPath      string
	SessionID        string
	OnChunk          func(content string, done bool)
	OnToolCall       func(name string, input any)
	OnToolResult     func(name string, result any, isError bool)
	OnError          func(err string)
	OnSessionUpdated func(session *db.Session)
	// GetOpenTabs returns the client's currently open editor tabs.
	GetOpenTabs func() []TabInfo
	// SendToClient sends an arbitrary message back to the WS client.
	SendToClient func(msgType string, payload any)
	// RequestApproval asks the client to elevate a risky action and blocks until the user responds.
	RequestApproval func(kind, title, message, command string) (bool, error)
	// Cancel, when closed, signals the agentic loop to stop at the next safe checkpoint.
	// The loop sends a final chat.chunk with done=true before exiting.
	Cancel <-chan struct{}
	// ActiveTools is the live tool set for the current request. Starts as bootstrapTools
	// and grows each time the model calls search_tools to discover new capabilities.
	ActiveTools []anthropicTool
	// Usage accumulates token counts and cost estimates for this chat session.
	Usage *SessionUsage
	// Quarantine tracks tool failure counts and quarantines repeatedly failing tools.
	Quarantine *ToolQuarantine
	// OnBlocked is called when a tool is quarantined after repeated failures,
	// signalling that the agent cannot make progress without human intervention.
	OnBlocked func(reason string)
	// Role determines the lean system prompt and pre-granted tool set.
	// Defaults to RoleInteractive when zero-valued.
	Role AgentRole
	// MarkVital is set by the agentic loop before iterating. Calling MarkVital(n) marks the
	// last n messages in the active history as vital checkpoints so they survive context windowing.
	// Nil outside of an active loop.
	MarkVital func(n int)
		// AutonomousPolicy, when non-nil, controls headless session behaviour:
		// auto-approving commits and/or pushes without prompting the user.
		AutonomousPolicy *AutonomousPolicy
	// ProjectTools holds project-defined tools discovered from <projectRoot>/.engine/tools/.
	// Loaded once per Chat() call; nil when the directory is absent or empty.
	ProjectTools []projectToolDef
	// DiscordDM sends a direct message to the project owner via Discord. Nil when Discord is not configured.
	DiscordDM func(message string) error
}

// isCancelled returns true if the context's cancel channel has been closed.
func (ctx *ChatContext) isCancelled() bool {
	if ctx.Cancel == nil {
		return false
	}
	select {
	case <-ctx.Cancel:
		return true
	default:
		return false
	}
}

// TabInfo represents an open editor tab (pushed by client via editor.tabs.sync).
type TabInfo struct {
	Path     string `json:"path"`
	IsActive bool   `json:"isActive"`
	IsDirty  bool   `json:"isDirty"`
}

// ToolCall is a single tool invocation recorded in the DB.
type ToolCall struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Input   any `json:"input"`
	Result  any `json:"result,omitempty"`
	IsError bool        `json:"isError,omitempty"`
}

// --- Anthropic API types (raw HTTP, no official Go SDK) ---

type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema any `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content any `json:"content"` // string | []contentBlock
	// Vital marks this message as a key checkpoint. Vital messages are always kept during
	// context windowing; only non-vital messages are pruned when context grows too large.
	// Never serialised to the API.
	Vital bool `json:"-"`
}

type contentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     any `json:"input,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   string      `json:"content,omitempty"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools"`
	Stream    bool               `json:"stream"`
}

// strProp is a helper to build simple {"type":"string","description":"..."} JSON.
func strProp(desc string) any {
	return map[string]any{"type": "string", "description": desc}
}

func objSchema(required []string, props map[string]any) any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// toolRegistry is the complete catalog of every tool Engine can execute.
// Only a small bootstrap subset is sent to the model at the start of each request.
// The model discovers additional tools by calling search_tools.
var toolRegistry = []anthropicTool{
	// ── Discovery ────────────────────────────────────────────────────────────
	{
		Name:        "search_tools",
		Description: "Search for available tools by describing what you want to do. Returns matching tool names, descriptions, and full schemas — and makes them available for immediate use. Call this during your planning phase before executing, or whenever you need a capability you don't currently have.",
		InputSchema: objSchema([]string{"query"}, map[string]any{
			"query": strProp("Natural language description of what you want to do, e.g. 'run shell commands', 'commit to git', 'open GitHub issues'"),
		}),
	},
	// ── Core navigation (always available) ──────────────────────────────────
	{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path.",
		InputSchema: objSchema([]string{"path"}, map[string]any{
			"path": strProp("Absolute path to the file"),
		}),
	},
	{
		Name:        "list_directory",
		Description: "List files and directories at a path, up to 4 levels deep.",
		InputSchema: objSchema([]string{"path"}, map[string]any{
			"path": strProp("Absolute directory path"),
		}),
	},
	// ── File operations ──────────────────────────────────────────────────────
	{
		Name:        "write_file",
		Description: "Create or overwrite a file with the given content. Parent directories are created automatically. Use this whenever you need to create a new file or save content to disk.",
		InputSchema: objSchema([]string{"path", "content"}, map[string]any{
			"path":    strProp("Absolute path to write to"),
			"content": strProp("Content to write"),
		}),
	},
	{
		Name:        "open_file",
		Description: "Open an EXISTING file in the editor so the user can view it. This does NOT create files — use write_file to create new files.",
		InputSchema: objSchema([]string{"path"}, map[string]any{
			"path": strProp("Absolute path to the existing file to open"),
		}),
	},
	// ── Shell / execution ────────────────────────────────────────────────────
	{
		Name:        "shell",
		Description: "Execute a shell command and return stdout + stderr. Use for running tests, builds, installs, etc.",
		InputSchema: objSchema([]string{"command"}, map[string]any{
			"command": strProp("Shell command to run"),
			"cwd":     strProp("Working directory (optional, defaults to project root)"),
		}),
	},
	{
		Name:        "test.run",
		Description: "Run a test command in the client terminal and observe output for issue resolution.",
		InputSchema: objSchema([]string{"command"}, map[string]any{
			"command":    strProp("Shell command to run"),
			"terminalId": strProp("Terminal ID to run in (optional)"),
			"issue":      strProp("Issue description to validate against"),
		}),
	},
	// ── Search ───────────────────────────────────────────────────────────────
	{
		Name:        "search_files",
		Description: "Search for a pattern in files using ripgrep. Returns matching lines with file paths and line numbers.",
		InputSchema: objSchema([]string{"pattern"}, map[string]any{
			"pattern":      strProp("Regex pattern to search for"),
			"directory":    strProp("Directory to search in (optional, defaults to project root)"),
			"file_pattern": strProp("Glob pattern to filter files (e.g. \"*.go\")"),
		}),
	},
	{
		Name:        "search_history",
		Description: "Search Engine's stored history for the current project across prior sessions, summaries, learnings, and validation evidence. Results are scoped to this project only.",
		InputSchema: objSchema([]string{"query"}, map[string]any{
			"query": strProp("Keywords or question to search for in prior Engine history"),
			"scope": strProp("Optional scope: current-session (only this session) or project (all sessions for this project, default)"),
			"limit": map[string]any{
				"type":        "number",
				"description": "Optional max results to return (default 5, max 10)",
			},
		}),
	},
	// ── Git ──────────────────────────────────────────────────────────────────
	{
		Name:        "git_status",
		Description: "Get the current git status: branch, staged/unstaged/untracked files.",
		InputSchema: objSchema(nil, map[string]any{}),
	},
	{
		Name:        "git_diff",
		Description: "Get git diff for current changes (staged + unstaged).",
		InputSchema: objSchema(nil, map[string]any{
			"path": strProp("Specific file path to diff (optional)"),
		}),
	},
	{
		Name:        "git_commit",
		Description: "Stage all changes and create a git commit.",
		InputSchema: objSchema([]string{"message"}, map[string]any{
			"message": strProp("Commit message"),
		}),
	},
	// ── Editor UI ────────────────────────────────────────────────────────────
	{
		Name:        "get_system_info",
		Description: "Get current system resource usage: memory (used/total/%), CPU %, and disk usage for the project path.",
		InputSchema: objSchema(nil, map[string]any{}),
	},
	{
		Name:        "list_open_tabs",
		Description: "List the files currently open in the editor. Returns path, whether it is the active tab, and whether it has unsaved changes.",
		InputSchema: objSchema(nil, map[string]any{}),
	},
	{
		Name:        "close_tab",
		Description: "Close a specific file tab in the editor. Will not close tabs with unsaved changes unless force is true.",
		InputSchema: objSchema([]string{"path"}, map[string]any{
			"path":  strProp("Absolute path of the tab to close"),
			"force": map[string]any{"type": "boolean", "description": "Force close even if there are unsaved changes"},
		}),
	},
	{
		Name:        "focus_tab",
		Description: "Bring a specific file tab to the foreground in the editor.",
		InputSchema: objSchema([]string{"path"}, map[string]any{
			"path": strProp("Absolute path of the tab to focus"),
		}),
	},
	// ── GitHub ───────────────────────────────────────────────────────────────
	{
		Name:        "github_list_issues",
		Description: "List GitHub issues for a repository.",
		InputSchema: objSchema([]string{"owner", "repo"}, map[string]any{
			"owner": strProp("Repository owner"),
			"repo":  strProp("Repository name"),
			"state": strProp("Issue state: open, closed, or all (default: open)"),
		}),
	},
	{
		Name:        "github_get_issue",
		Description: "Get details of a specific GitHub issue.",
		InputSchema: objSchema([]string{"owner", "repo", "number"}, map[string]any{
			"owner":  strProp("Repository owner"),
			"repo":   strProp("Repository name"),
			"number": map[string]any{"type": "number", "description": "Issue number"},
		}),
	},
	{
		Name:        "github_close_issue",
		Description: "Close a GitHub issue with an optional comment explaining the resolution.",
		InputSchema: objSchema([]string{"owner", "repo", "number"}, map[string]any{
			"owner":   strProp("Repository owner"),
			"repo":    strProp("Repository name"),
			"number":  map[string]any{"type": "number", "description": "Issue number"},
			"comment": strProp("Closing comment with resolution evidence"),
		}),
	},
	{
		Name:        "github_create_issue",
		Description: "Create a new GitHub issue.",
		InputSchema: objSchema([]string{"owner", "repo", "title"}, map[string]any{
			"owner": strProp("Repository owner"),
			"repo":  strProp("Repository name"),
			"title": strProp("Issue title"),
			"body":  strProp("Issue body/description"),
		}),
	},
	{
		Name:        "github_comment",
		Description: "Add a comment to a GitHub issue.",
		InputSchema: objSchema([]string{"owner", "repo", "number", "body"}, map[string]any{
			"owner":  strProp("Repository owner"),
			"repo":   strProp("Repository name"),
			"number": map[string]any{"type": "number", "description": "Issue number"},
			"body":   strProp("Comment body"),
		}),
	},
	// ── Additional git operations ─────────────────────────────────────────
	{
		Name:        "git_push",
		Description: "Push the current branch to the remote repository. Requires user approval.",
		InputSchema: objSchema(nil, map[string]any{
			"remote": strProp("Remote name (optional, defaults to 'origin')"),
		}),
	},
	{
		Name:        "git_pull",
		Description: "Pull latest changes from the remote repository for the current branch.",
		InputSchema: objSchema(nil, map[string]any{
			"remote": strProp("Remote name (optional, defaults to 'origin')"),
		}),
	},
	{
		Name:        "git_branch",
		Description: "Create a new branch and switch to it, switch to an existing branch, or list all local branches.",
		InputSchema: objSchema(nil, map[string]any{
			"name":   strProp("Branch name to create or switch to (optional — omit to list all branches)"),
			"create": map[string]any{"type": "boolean", "description": "Create the branch if true, otherwise switch to existing"},
		}),
	},
	// ── System control ────────────────────────────────────────────────────
	{
		Name:        "process_list",
		Description: "List running processes with PID, name, CPU%, and memory usage. Optionally filter by name.",
		InputSchema: objSchema(nil, map[string]any{
			"filter": strProp("Substring to filter process names (optional)"),
		}),
	},
	{
		Name:        "process_kill",
		Description: "Kill a running process by PID. Requires explicit approval. Use TERM for graceful shutdown, KILL to force.",
		InputSchema: objSchema([]string{"pid"}, map[string]any{
			"pid":    map[string]any{"type": "number", "description": "Process ID to kill"},
			"signal": strProp("Signal to send: TERM (default, graceful) or KILL (force)"),
		}),
	},
	{
		Name:        "open_url",
		Description: "Open a URL in the system default browser.",
		InputSchema: objSchema([]string{"url"}, map[string]any{
			"url": strProp("URL to open"),
		}),
	},
	{
		Name:        "screenshot",
		Description: "Capture a screenshot of the current screen. Returns the file path of the saved PNG for further inspection.",
		InputSchema: objSchema(nil, map[string]any{
			"path": strProp("Output path for the PNG (optional, defaults to /tmp/engine-screenshot-{timestamp}.png)"),
		}),
	},
	// ── Git clone ─────────────────────────────────────────────────────────────
	{
		Name:        "git_clone",
		Description: "Clone a git repository to a local path. Use when you need to work on a repository that is not already cloned locally.",
		InputSchema: objSchema([]string{"url"}, map[string]any{
			"url":  strProp("Repository URL to clone (https:// or git@ format)"),
			"path": strProp("Local destination path (optional; defaults to ~/engine-workspace/<repo-name>)"),
		}),
	},
	// ── Context management ───────────────────────────────────────────────────
	{
		Name: "mark_vital",
		Description: "Mark the last n messages in the active history as vital checkpoints. " +
			"Vital messages are ALWAYS kept during context windowing — they survive even aggressive compaction. " +
			"Call this at the end of a completed phase or section to checkpoint important findings, " +
			"decisions, or deliverables. Non-vital messages will be pruned once they fall outside the " +
			"recent window. This keeps context lean without losing key milestones.",
		InputSchema: objSchema(nil, map[string]any{
			"n": map[string]any{"type": "number", "description": "Number of recent messages to mark vital (default 1)"},
		}),
	},

	// ── Browser automation ───────────────────────────────────────────────────────
	{
		Name:        "browser_navigate",
		Description: "Navigate the system browser (Chrome on macOS) to a URL. Use to research sites, access web apps, or start a login flow.",
		InputSchema: objSchema([]string{"url"}, map[string]any{
			"url": strProp("URL to navigate to"),
		}),
	},
	{
		Name:        "browser_read_page",
		Description: "Read the visible text content of the currently active browser tab (up to 8000 chars). Use after browser_navigate to extract page content.",
		InputSchema: objSchema(nil, map[string]any{}),
	},
	{
		Name:        "browser_click",
		Description: "Click at screen coordinates in the browser window. Use after browser_navigate and screenshot to interact with UI elements.",
		InputSchema: objSchema([]string{"x", "y"}, map[string]any{
			"x": map[string]any{"type": "number", "description": "Screen X coordinate"},
			"y": map[string]any{"type": "number", "description": "Screen Y coordinate"},
		}),
	},
	{
		Name:        "browser_type",
		Description: "Type text into the focused browser element. Use after browser_click to fill in a form field.",
		InputSchema: objSchema([]string{"text"}, map[string]any{
			"text": strProp("Text to type"),
		}),
	},
	// ── Machine-scoped credential storage ────────────────────────────────────────
	{
		Name:        "credential_set",
		Description: "Store a credential in the machine keychain, scoped to this Engine installation (not per-project). Use to save passwords, tokens, or API keys for reuse across sessions.",
		InputSchema: objSchema([]string{"key", "value"}, map[string]any{
			"key":   strProp("Credential key/name (e.g. 'github_token', 'openai_key')"),
			"value": strProp("Secret value to store"),
		}),
	},
	{
		Name:        "credential_get",
		Description: "Retrieve a previously stored credential from the machine keychain.",
		InputSchema: objSchema([]string{"key"}, map[string]any{
			"key": strProp("Credential key/name to retrieve"),
		}),
	},
	{
		Name:        "credential_delete",
		Description: "Delete a stored credential from the machine keychain.",
		InputSchema: objSchema([]string{"key"}, map[string]any{
			"key": strProp("Credential key/name to delete"),
		}),
	},
	// ── Discord communication ─────────────────────────────────────────────────────
	{
		Name:        "discord_dm",
		Description: "Send a direct message to the project owner via Discord. Use when you need credentials, approval, or human input that cannot be obtained autonomously.",
		InputSchema: objSchema([]string{"message"}, map[string]any{
			"message": strProp("Message to send to the project owner"),
		}),
	},
}

// toolRegistryIndex is a flat name→tool map for O(1) lookup.
var toolRegistryIndex = func() map[string]anthropicTool {
	m := make(map[string]anthropicTool, len(toolRegistry))
	for _, t := range toolRegistry {
		m[t.Name] = t
	}
	return m
}()

// bootstrapTools is the minimal set sent to the model at the start of every request.
// Only navigation + tool discovery. Everything else must be discovered via search_tools.
var bootstrapToolNames = []string{"search_tools", "read_file", "list_directory", "mark_vital"}

func bootstrapTools() []anthropicTool {
	out := make([]anthropicTool, 0, len(bootstrapToolNames))
	for _, name := range bootstrapToolNames {
		if t, ok := toolRegistryIndex[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// executeSearchTools searches the registry by keyword and injects matched tools into
// ctx.ActiveTools so they are available on the very next model iteration.
// Returns a human-readable summary of what was found and added.
func executeSearchTools(query string, ctx *ChatContext) string {
	query = strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(query)

	type scored struct {
		tool  anthropicTool
		score int
	}

	// Score each tool by how many query words appear in its name+description.
	var matches []scored
	for _, t := range toolRegistry {
		haystack := strings.ToLower(t.Name + " " + t.Description)
		score := 0
		for _, w := range words {
			if strings.Contains(haystack, w) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{t, score})
		}
	}
	for _, def := range ctx.ProjectTools {
		t := def.schema
		haystack := strings.ToLower(t.Name + " " + t.Description)
		score := 0
		for _, w := range words {
			if strings.Contains(haystack, w) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{t, score})
		}
	}

	// Sort by score descending.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].score > matches[j-1].score; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}

	if len(matches) == 0 {
		return "No tools matched that query. Available categories: file operations, shell/execution, search, git, editor UI, github."
	}

	// Merge newly found tools into ctx.ActiveTools (dedup by name).
	activeByName := make(map[string]bool, len(ctx.ActiveTools))
	for _, t := range ctx.ActiveTools {
		activeByName[t.Name] = true
	}
	var added []string
	for _, m := range matches {
		if !activeByName[m.tool.Name] {
			ctx.ActiveTools = append(ctx.ActiveTools, m.tool)
			activeByName[m.tool.Name] = true
			added = append(added, m.tool.Name)
		}
	}

	var sb strings.Builder
	sb.WriteString("Tools found and now available:\n")
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("  • %s — %s\n", m.tool.Name, m.tool.Description))
	}
	if len(added) > 0 {
		sb.WriteString(fmt.Sprintf("\nAdded to active tools: %s", strings.Join(added, ", ")))
	} else {
		sb.WriteString("\n(All matched tools were already active)")
	}
	return sb.String()
}

// executeTool runs the named tool and returns (result string, isError bool).
func executeTool(name string, input map[string]any, ctx *ChatContext) (string, bool) {
	return aiExecuteTool(name, input, ctx)
}

func ExecuteToolForTest(name string, input map[string]any, ctx *ChatContext) (string, bool) {
	return aiExecuteTool(name, input, ctx)
}

func aiExecuteTool(name string, input map[string]any, ctx *ChatContext) (string, bool) {
	str := func(key string) string {
		v, _ := input[key].(string)
		return v
	}
	numVal := func(key string) float64 {
		v, _ := input[key].(float64)
		return v
	}
	boolVal := func(key string) bool {
		v, _ := input[key].(bool)
		return v
	}

	switch name {
	case "search_tools":
		return executeSearchTools(str("query"), ctx), false

	case "read_file":
		path, err := resolveWorkspacePath(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		fc, err := gofs.ReadFile(path)
		if err != nil {
			return err.Error(), true
		}
		return fc.Content, false

	case "write_file":
		path, err := resolveWorkspacePath(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		if err := gofs.WriteFile(path, str("content")); err != nil {
			return err.Error(), true
		}
		if ctx.SendToClient != nil {
			ctx.SendToClient("file.saved", map[string]any{"path": path})
		}
		return "File written: " + path, false

	case "list_directory":
		path, err := resolveWorkspaceDirectory(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		tree, err := gofs.GetTree(path, 4)
		if err != nil {
			return err.Error(), true
		}
		return formatTree(tree, 0), false

	case "shell":
		command := str("command")
		if title, message, needsApproval := requiresShellApproval(ctx.ProjectPath, command); needsApproval {
			if ctx.RequestApproval == nil {
				return "This shell command requires explicit approval, but no approval handler is available.", true
			}
			allowed, err := ctx.RequestApproval("shell", title, message, command)
			if err != nil {
				return err.Error(), true
			}
			if !allowed {
				return "The user denied this shell command.", true
			}
		}
		cwd := str("cwd")
		if cwd == "" {
			cwd = ctx.ProjectPath
		}
		cwd, err := resolveWorkspaceDirectory(ctx.ProjectPath, cwd)
		if err != nil {
			return err.Error(), true
		}
		// Use the user's login shell so the AI has the full PATH (Homebrew, nvm,
		// cargo, etc.). The -l flag sources login scripts (.zprofile, .bash_profile).
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd := exec.Command(shell, "-l", "-c", command)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		result := strings.TrimSpace(string(out))
		if result == "" {
			result = "(no output)"
		}
		if len(result) > 4*1024*1024 {
			result = result[:4*1024*1024] + "\n...(truncated)"
		}
		return result, err != nil && len(out) == 0

	case "search_files":
		dir := str("directory")
		if dir == "" {
			dir = ctx.ProjectPath
		}
		dir, err := resolveWorkspaceDirectory(ctx.ProjectPath, dir)
		if err != nil {
			return err.Error(), true
		}
		result, _ := gofs.SearchFiles(str("pattern"), dir, str("file_pattern"))
		return result, false

	case "git_status":
		status, _ := gogit.GetStatus(ctx.ProjectPath)
		b, _ := json.MarshalIndent(status, "", "  ")
		return string(b), false

	case "git_diff":
		diffPath := str("path")
		if diffPath != "" {
			resolvedDiffPath, err := workspaceRelativePath(ctx.ProjectPath, diffPath)
			if err != nil {
				return err.Error(), true
			}
			diffPath = resolvedDiffPath
		}
		diff, _ := gogit.GetDiff(ctx.ProjectPath, diffPath)
		return diff, false

	case "git_commit":
		// Secret scan: block commits that contain secrets.
		if diff, diffErr := gogit.GetDiff(ctx.ProjectPath, ""); diffErr == nil && diff != "" {
			if findings := ScanDiff(diff); len(findings) > 0 {
				report := FormatScanReport(findings)
				return report, true
			}
		}
		// Autonomous sessions with auto_commit bypass user approval.
		if ctx.AutonomousPolicy != nil && ctx.AutonomousPolicy.AutoCommit {
			hash, commitErr := gogit.Commit(ctx.ProjectPath, str("message"))
			if commitErr != nil {
				return commitErr.Error(), true
			}
			return "Committed: " + hash, false
		}
		if ctx.RequestApproval == nil {
			return "Git commits require explicit approval, but no approval handler is available.", true
		}
		allowed, err := ctx.RequestApproval(
			"git_commit",
			"Approve git commit",
			"The assistant wants to stage all current changes and create a git commit. Review the proposed message before allowing it.",
			str("message"),
		)
		if err != nil {
			return err.Error(), true
		}
		if !allowed {
			return "The user denied the git commit.", true
		}
		hash, err := gogit.Commit(ctx.ProjectPath, str("message"))
		if err != nil {
			return err.Error(), true
		}
		return "Committed: " + hash, false

	case "open_file":
		path, err := resolveWorkspacePath(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		if ctx.SendToClient != nil {
			ctx.SendToClient("editor.open", map[string]string{"path": path})
		}
		return "Opening " + path, false

	case "get_system_info":
		return getSystemInfo(ctx.ProjectPath), false

	case "list_open_tabs":
		if ctx.GetOpenTabs == nil {
			return "[]", false
		}
		tabs := ctx.GetOpenTabs()
		b, _ := json.MarshalIndent(tabs, "", "  ")
		return string(b), false

	case "search_history":
		if ctx == nil {
			return "History search is unavailable because the chat context is missing.", true
		}
		query := strings.TrimSpace(str("query"))
		if query == "" {
			return "query is required", true
		}
		scope := str("scope")
		limit := int(numVal("limit"))
		openTabs := []TabInfo{}
		if ctx.GetOpenTabs != nil {
			openTabs = ctx.GetOpenTabs()
		}
		residualProfile := BuildAttentionResidualProfile(ctx.ProjectPath, ctx.SessionID, query, openTabs)
		hits, _ := SearchHistoryWithResiduals(ctx.ProjectPath, ctx.SessionID, query, openTabs, scope, limit, residualProfile)
		return FormatHistorySearchResults(query, hits, ctx.SessionID), false

	case "close_tab":
		path, err := resolveWorkspacePath(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		force := boolVal("force")
		if ctx.GetOpenTabs != nil && !force {
			for _, t := range ctx.GetOpenTabs() {
				if t.Path == path && t.IsDirty {
					return fmt.Sprintf("Tab %s has unsaved changes. Use force=true to close anyway.", path), true
				}
			}
		}
		if ctx.SendToClient != nil {
			ctx.SendToClient("editor.tab.close", map[string]string{"path": path})
		}
		return "Closed tab: " + path, false

	case "focus_tab":
		path, err := resolveWorkspacePath(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		if ctx.SendToClient != nil {
			ctx.SendToClient("editor.tab.focus", map[string]string{"path": path})
		}
		return "Focused tab: " + path, false

	case "test.run":
		// Run a test command and observe terminal output
		// The client will stream output back via WebSocket
		terminalID := str("terminalId")
		command := str("command")
		issue := str("issue")

		if ctx.SendToClient == nil {
			return "No client available to run terminal command", true
		}

		// Notify client to run the command and observe
		ctx.SendToClient("test.run", map[string]any{
			"terminalId": terminalID,
			"command":    command,
			"issue":      issue,
		})

		return fmt.Sprintf("Test command queued: %s\nIssue context: %s\nMonitoring for issue resolution...", command, issue), false

	case "github_list_issues":
		owner := str("owner")
		repo := str("repo")
		state := str("state")
		if state == "" {
			state = "open"
		}
		client, err := gh.NewClient(owner, repo)
		if err != nil {
			return err.Error(), true
		}
		issues, err := client.ListIssues(state, nil)
		if err != nil {
			return err.Error(), true
		}
		var sb strings.Builder
		for _, issue := range issues {
			labels := ""
			for _, l := range issue.Labels {
				labels += " [" + l.Name + "]"
			}
			sb.WriteString(fmt.Sprintf("#%d %s (%s)%s\n", issue.Number, issue.Title, issue.State, labels))
		}
		if sb.Len() == 0 {
			return "No issues found", false
		}
		return sb.String(), false

	case "github_get_issue":
		owner := str("owner")
		repo := str("repo")
		numF, _ := input["number"].(float64)
		num := int(numF)
		if num == 0 {
			return "issue number required", true
		}
		client, err := gh.NewClient(owner, repo)
		if err != nil {
			return err.Error(), true
		}
		issue, err := client.GetIssue(num)
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("#%d: %s\nState: %s\nURL: %s\n\n%s", issue.Number, issue.Title, issue.State, issue.HTMLURL, issue.Body), false

	case "github_close_issue":
		owner := str("owner")
		repo := str("repo")
		numF, _ := input["number"].(float64)
		num := int(numF)
		comment := str("comment")
		if num == 0 {
			return "issue number required", true
		}
		client, err := gh.NewClient(owner, repo)
		if err != nil {
			return err.Error(), true
		}
		if err := client.CloseIssue(num, comment); err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("Issue #%d closed", num), false

	case "github_create_issue":
		owner := str("owner")
		repo := str("repo")
		title := str("title")
		body := str("body")
		if title == "" {
			return "title required", true
		}
		client, err := gh.NewClient(owner, repo)
		if err != nil {
			return err.Error(), true
		}
		issue, err := client.CreateIssue(title, body, nil)
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("Created #%d: %s\n%s", issue.Number, issue.Title, issue.HTMLURL), false

	case "github_comment":
		owner := str("owner")
		repo := str("repo")
		numF, _ := input["number"].(float64)
		num := int(numF)
		body := str("body")
		if num == 0 || body == "" {
			return "issue number and body required", true
		}
		client, err := gh.NewClient(owner, repo)
		if err != nil {
			return err.Error(), true
		}
		comment, err := client.AddComment(num, body)
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("Comment added: %s", comment.HTMLURL), false

	case "git_push":
		remote := str("remote")
		if remote == "" {
			remote = "origin"
		}
		// Autonomous sessions with auto_push bypass user approval.
		if ctx.AutonomousPolicy != nil && ctx.AutonomousPolicy.AutoCommit && ctx.AutonomousPolicy.AutoPush {
			out, pushErr := gogit.Push(ctx.ProjectPath, remote)
			if pushErr != nil {
				return pushErr.Error(), true
			}
			return out, false
		}
		if ctx.RequestApproval == nil {
			return "git_push requires approval, but no approval handler is available.", true
		}
		branch, _ := gogit.GetCurrentBranch(ctx.ProjectPath)
		allowed, err := ctx.RequestApproval(
			"git_push",
			"Approve git push",
			fmt.Sprintf("The assistant wants to push branch '%s' to remote '%s'.", branch, remote),
			fmt.Sprintf("git push %s %s", remote, branch),
		)
		if err != nil {
			return err.Error(), true
		}
		if !allowed {
			return "The user denied the git push.", true
		}
		out, pushErr := gogit.Push(ctx.ProjectPath, remote)
		if pushErr != nil {
			return pushErr.Error(), true
		}
		return out, false

	case "git_pull":
		out, pullErr := gogit.Pull(ctx.ProjectPath, str("remote"))
		if pullErr != nil {
			return pullErr.Error(), true
		}
		return out, false

	case "git_branch":
		name := str("name")
		create := boolVal("create")
		if name == "" {
			branches, branchErr := gogit.ListBranches(ctx.ProjectPath)
			if branchErr != nil {
				return branchErr.Error(), true
			}
			return strings.Join(branches, "\n"), false
		}
		out, branchErr := gogit.CreateBranch(ctx.ProjectPath, name, create)
		if branchErr != nil {
			return branchErr.Error(), true
		}
		return out, false

	case "process_list":
		filter := strings.ToLower(str("filter"))
		procs, procErr := processListFn()
		if procErr != nil {
			return procErr.Error(), true
		}
		var sb strings.Builder
		count := 0
		for _, p := range procs {
			n, _ := p.Name()
			if filter != "" && !strings.Contains(strings.ToLower(n), filter) {
				continue
			}
			cpuPct, _ := p.CPUPercent()
			memInfo, _ := p.MemoryInfo()
			memMB := 0.0
			if memInfo != nil {
				memMB = float64(memInfo.RSS) / 1e6
			}
			sb.WriteString(fmt.Sprintf("PID %-8d %-30s CPU: %5.1f%%  MEM: %6.1f MB\n",
				p.Pid, n, cpuPct, memMB))
			count++
			if count >= 50 {
				sb.WriteString(fmt.Sprintf("... (limited to %d results, use filter to narrow)\n", count))
				break
			}
		}
		if sb.Len() == 0 {
			return "No matching processes found", false
		}
		return sb.String(), false

	case "process_kill":
		if ctx.RequestApproval == nil {
			return "process_kill requires approval, but no approval handler is available.", true
		}
		pidF, _ := input["pid"].(float64)
		pid := int32(pidF)
		if pid == 0 {
			return "pid is required", true
		}
		p, procErr := newProcessFn(pid)
		if procErr != nil {
			return fmt.Sprintf("process %d not found: %v", pid, procErr), true
		}
		procName, _ := p.Name()
		signal := str("signal")
		if signal == "" {
			signal = "TERM"
		}
		allowed, err := ctx.RequestApproval(
			"process_kill",
			"Approve process termination",
			fmt.Sprintf("The assistant wants to send SIG%s to PID %d (%s).", signal, pid, procName),
			fmt.Sprintf("kill -%s %d", signal, pid),
		)
		if err != nil {
			return err.Error(), true
		}
		if !allowed {
			return "The user denied the process kill.", true
		}
		if signal == "KILL" {
			if err := p.Kill(); err != nil {
				return err.Error(), true
			}
		} else {
			if err := p.Terminate(); err != nil {
				return err.Error(), true
			}
		}
		return fmt.Sprintf("Sent SIG%s to PID %d (%s)", signal, pid, procName), false

	case "open_url":
		urlStr := str("url")
		if urlStr == "" {
			return "url is required", true
		}
		urlCmd, osErr := openURLForOS(urlStr)
		if osErr != "" {
			return osErr, true
		}
		if err := urlCmd.Start(); err != nil {
			return err.Error(), true
		}
		return "Opened: " + urlStr, false

	case "screenshot":
		outPath := str("path")
		if outPath == "" {
			outPath = fmt.Sprintf("/tmp/engine-screenshot-%d.png", time.Now().UnixMilli())
		}
		ssCmd, ssOsErr := screenshotCmdForOS(outPath)
		if ssOsErr != "" {
			return ssOsErr, true
		}
		if ssOut, ssErr := ssCmd.CombinedOutput(); ssErr != nil {
			return fmt.Sprintf("screenshot failed: %v\n%s", ssErr, string(ssOut)), true
		}
		return "Screenshot saved: " + outPath, false

	case "git_clone":
		url := str("url")
		if url == "" {
			return "url is required", true
		}
		dest := str("path")
		if dest == "" {
			repoName := strings.TrimSuffix(filepath.Base(url), ".git")
			workspaceDir := os.Getenv("ENGINE_WORKSPACE_DIR")
			if workspaceDir == "" {
				home, _ := os.UserHomeDir()
				workspaceDir = filepath.Join(home, "engine-workspace")
			}
			dest = filepath.Join(workspaceDir, repoName)
		}
		if _, err := os.Stat(dest); err == nil {
			return "Already cloned at: " + dest, false
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err.Error(), true
		}
		if err := cloneRepoFn(url, dest); err != nil {
			return err.Error(), true
		}
		return "Cloned to: " + dest, false

	case "mark_vital":
		if ctx.MarkVital == nil {
			return "mark_vital: not available outside an active loop", true
		}
		n := int(numVal("n"))
		if n <= 0 {
			n = 1
		}
		ctx.MarkVital(n)
		return fmt.Sprintf("Marked last %d message(s) as vital checkpoints.", n), false

	case "browser_navigate":
		url := str("url")
		if url == "" {
			return "browser_navigate: url is required", true
		}
		result, err := browserNavigateFnForOS(url)
		if err != nil {
			return err.Error(), true
		}
		return result, false

	case "browser_read_page":
		result, err := browserReadPageFnForOS()
		if err != nil {
			return err.Error(), true
		}
		return result, false

	case "browser_click":
		x := int(numVal("x"))
		y := int(numVal("y"))
		result, err := browserClickFnForOS(x, y)
		if err != nil {
			return err.Error(), true
		}
		return result, false

	case "browser_type":
		text := str("text")
		if text == "" {
			return "browser_type: text is required", true
		}
		result, err := browserTypeFnForOS(text)
		if err != nil {
			return err.Error(), true
		}
		return result, false

	case "credential_set":
		key := str("key")
		value := str("value")
		if key == "" {
			return "credential_set: key is required", true
		}
		if err := credStoreSetFn(key, value); err != nil {
			return err.Error(), true
		}
		return "Credential stored: " + key, false

	case "credential_get":
		key := str("key")
		if key == "" {
			return "credential_get: key is required", true
		}
		val, err := credStoreGetFn(key)
		if err != nil {
			return err.Error(), true
		}
		return val, false

	case "credential_delete":
		key := str("key")
		if key == "" {
			return "credential_delete: key is required", true
		}
		if err := credStoreDelFn(key); err != nil {
			return err.Error(), true
		}
		return "Credential deleted: " + key, false

	case "discord_dm":
		message := str("message")
		if message == "" {
			return "discord_dm: message is required", true
		}
		if ctx.DiscordDM == nil {
			return "discord_dm: Discord not configured", true
		}
		if err := ctx.DiscordDM(message); err != nil {
			return err.Error(), true
		}
		return "DM sent", false

	default:
		if result, isError, found := executeProjectTool(name, input, ctx); found {
			return result, isError
		}
		return "Unknown tool: " + name, true
	}
}
// getSystemInfo returns a formatted string with current memory, CPU, and disk usage.
func getSystemInfo(projectPath string) string {
	var sb strings.Builder

	if vm, err := mem.VirtualMemory(); err == nil {
		sb.WriteString(fmt.Sprintf("Memory: %.1f GB used / %.1f GB total (%.1f%%)\n",
			float64(vm.Used)/1e9, float64(vm.Total)/1e9, vm.UsedPercent))
	}

	if cpus, err := cpu.Percent(200*time.Millisecond, false); err == nil && len(cpus) > 0 {
		sb.WriteString(fmt.Sprintf("CPU: %.1f%%\n", cpus[0]))
	}

	if du, err := disk.Usage(projectPath); err == nil {
		sb.WriteString(fmt.Sprintf("Disk (%s): %.1f GB used / %.1f GB total (%.1f%%)\n",
			projectPath, float64(du.Used)/1e9, float64(du.Total)/1e9, du.UsedPercent))
	}

	sb.WriteString(fmt.Sprintf("OS: %s/%s\n", runtime.GOOS, runtime.GOARCH))

	return strings.TrimSpace(sb.String())
}

func formatTree(node *gofs.FileNode, depth int) string {
	prefix := strings.Repeat("  ", depth)
	icon := "📄"
	if node.Type == "directory" {
		icon = "📁"
	}
	result := fmt.Sprintf("%s%s %s\n", prefix, icon, node.Name)
	for _, child := range node.Children {
		result += formatTree(child, depth+1)
	}
	return result
}

// Chat runs the full agentic loop for a user message, streaming results via ctx callbacks.
func Chat(ctx *ChatContext, userMessage string) {
	explicitProvider := os.Getenv("ENGINE_MODEL_PROVIDER")
	model := strings.TrimSpace(os.Getenv("ENGINE_MODEL"))
	if resolvedTeam, teamProvider, teamModel, ok := ResolveTeamOrchestratorModel(ctx.ProjectPath, os.Getenv("ENGINE_ACTIVE_TEAM")); ok {
		if model == "" {
			model = teamModel
		}
		if strings.TrimSpace(explicitProvider) == "" || strings.EqualFold(strings.TrimSpace(explicitProvider), "auto") {
			explicitProvider = teamProvider
		}
		if strings.TrimSpace(os.Getenv("ENGINE_ACTIVE_TEAM")) == "" {
			os.Setenv("ENGINE_ACTIVE_TEAM", resolvedTeam) //nolint:errcheck
		}
	}
	provider := resolveProvider(explicitProvider, model)
	ollamaBaseURL := os.Getenv("OLLAMA_BASE_URL")
	if provider == "ollama" && model == "" {
		model = detectOllamaModel(ollamaBaseURL)
	}
	if model == "" {
		model = defaultModelForProvider(provider)
	}

	if provider == "anthropic" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		ctx.OnError("ANTHROPIC_API_KEY not set — configure it in Engine Settings")
		return
	}
	if provider == "openai" && os.Getenv("OPENAI_API_KEY") == "" {
		ctx.OnError("OPENAI_API_KEY not set — configure it in Engine Settings")
		return
	}

	// Persist user message
	userMsgID := newID()
	if err := saveMessageFn(userMsgID, ctx.SessionID, "user", userMessage, nil); err != nil {
		ctx.OnError("Failed to save message: " + err.Error())
		return
	}
	if ctx.OnSessionUpdated != nil {
		if updatedSession, err := db.GetSession(ctx.SessionID); err == nil && updatedSession != nil {
			ctx.OnSessionUpdated(updatedSession)
		}
	}

	history, _ := db.GetMessages(ctx.SessionID)
	session, _ := db.GetSession(ctx.SessionID)
	userMessageCount := 0
	for _, h := range history {
		if strings.EqualFold(strings.TrimSpace(h.Role), "user") {
			userMessageCount++
		}
	}
	isFirstUserMessage := userMessageCount <= 1
	openTabs := []TabInfo{}
	if ctx.GetOpenTabs != nil {
		openTabs = ctx.GetOpenTabs()
	}
	residualProfile := BuildAttentionResidualProfile(ctx.ProjectPath, ctx.SessionID, userMessage, openTabs)

	// Use residual-aware selection so older high-value context can survive beyond a flat recent window.
	windowResult := BuildAttentionConversationWindow(history, userMessage, openTabs, residualProfile)
	messages := windowResult.Messages

	// Initialise the active tool set to the bootstrap minimum.
	// The model discovers additional tools by calling search_tools.
	ctx.ActiveTools = bootstrapTools()
	ctx.ProjectTools = LoadProjectTools(ctx.ProjectPath)

	if ctx.Usage == nil {
		ctx.Usage = &SessionUsage{}
	}
	if ctx.Quarantine == nil {
		ctx.Quarantine = NewToolQuarantine()
	}

	// Build system prompt
	branch, _ := gogit.GetCurrentBranch(ctx.ProjectPath)
	// Build system prompt: lean, role-focused.
	var selectiveContext selectiveContextResult
	extraContext := ""
	projectDirection := resolveProjectDirection(ctx.ProjectPath)
	applyFirstTurnAutonomyContext(ctx, userMessage, projectDirection, isFirstUserMessage)
	if ctx.Role == RoleInteractive {
		selectiveContext = BuildSelectiveContext(ctx.ProjectPath, session, userMessage, openTabs, residualProfile)
		expansion := BuildPreStartExpansion(userMessage, projectDirection)
		extraContext = strings.TrimSpace(selectiveContext.Prompt + "\n\n" + expansion)
	}
	systemPrompt := buildRoleSystemPrompt(ctx.Role, ctx.ProjectPath, branch, extraContext)

	// Seed the active tool set from the role's pre-granted tools.
	// For RoleInteractive (nil tools), bootstrapTools + search_tools discovery is used.
	if preGranted := roleBootstrapTools(ctx.Role); preGranted != nil {
		toolSet := make([]anthropicTool, 0, len(preGranted))
		for _, name := range preGranted {
			if t, ok := toolRegistryIndex[name]; ok {
				toolSet = append(toolSet, t)
			}
		}
		ctx.ActiveTools = toolSet
	}

	var allToolCalls []ToolCall
	var finalText strings.Builder

	// Enforce token budget: trim oldest messages if over budget.
	trimmedMessages, tokensUsed := trimToTokenBudgetFn(messages, DefaultTokenBudget)
	if tokensUsed > DefaultTokenBudget {
		ctx.OnError(fmt.Sprintf("⚠️ Conversation history exceeds token budget (%d > %d). Oldest messages were trimmed to fit.", tokensUsed, DefaultTokenBudget))
	}
	messages = trimmedMessages

	// Dispatch to the correct provider. Adding a new provider = add a case in
	// newProvider() in provider.go — nothing else changes.
	newProvider(provider).RunLoop(ctx, model, systemPrompt, messages, &allToolCalls, &finalText)

	// Persist final assistant message
	var tc any
	if len(allToolCalls) > 0 {
		tc = allToolCalls
	}
	assistantMessageID := newID()
	db.SaveMessage(assistantMessageID, ctx.SessionID, "assistant", finalText.String(), tc) //nolint:errcheck
	db.SaveAttentionResiduals(BuildAttentionResidualRecords(                               //nolint:errcheck
		ctx.SessionID,
		userMsgID,
		userMessage,
		windowResult,
		selectiveContext,
		func() string {
			if session == nil {
				return ""
			}
			return session.Summary
		}(),
	))
	if summary := BuildUpdatedSessionSummary(func() string {
		if session == nil {
			return ""
		}
		return session.Summary
	}(), userMessage, finalText.String(), allToolCalls); summary != "" {
		db.UpdateSessionSummary(ctx.SessionID, summary) //nolint:errcheck
		if ctx.OnSessionUpdated != nil {
			if updatedSession, err := db.GetSession(ctx.SessionID); err == nil && updatedSession != nil {
				ctx.OnSessionUpdated(updatedSession)
			}
		}
	}
	ctx.OnChunk("", true)
}

func resolveProjectDirection(projectPath string) string {
	direction, _ := db.GetProjectDirection(projectPath)
	if strings.TrimSpace(direction) != "" {
		return direction
	}
	return EnsureProjectDirection(projectPath)
}

func applyFirstTurnAutonomyContext(ctx *ChatContext, userMessage, projectDirection string, isFirstUserMessage bool) {
	if !isFirstUserMessage {
		return
	}
	ensureProjectProfileCache(ctx.ProjectPath, userMessage, projectDirection)
	if HasExplicitStyleGuidance(userMessage) {
		return
	}
	notice := BuildStyleAssumptionNotice()
	if ctx.SendToClient != nil {
		ctx.SendToClient("chat.notice", map[string]any{"message": notice})
	}
	if ctx.DiscordDM != nil {
		_ = ctx.DiscordDM(notice)
	}
}

func ensureProjectProfileCache(projectPath, userMessage, projectDirection string) {
	if strings.TrimSpace(projectPath) == "" || strings.TrimSpace(userMessage) == "" {
		return
	}

	profile := BuildHeuristicProjectProfile(projectPath, userMessage, projectDirection)
	if profile.Verification.StartCmd == "" && profile.Verification.CheckURL == "" &&
		len(profile.Verification.CheckCmds) == 0 && !profile.Verification.UsesPlaywright {
		profile.Verification = DeriveVerificationStrategy(profile.Type)
	}

	raw, err := json.Marshal(profile)
	if err == nil {
		db.UpsertProjectProfile(projectPath, string(raw)) //nolint:errcheck
	}
	WriteProjectProfileCache(projectPath, &profile) //nolint:errcheck
}

// runAnthropicLoop executes the Anthropic-native streaming agentic loop.
// It is called by anthropicProvider.RunLoop in provider.go.
func runAnthropicLoop(
	ctx *ChatContext,
	model, apiKey, systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	messages := history

	historyRef := &messages
	ctx.MarkVital = func(n int) {
		msgs := *historyRef
		start := len(msgs) - n
		if start < 0 {
			start = 0
		}
		for i := start; i < len(msgs); i++ {
			msgs[i].Vital = true
		}
		*historyRef = msgs
	}

	for {
		if ctx.isCancelled() {
			return
		}

		windowed := windowByVitality(messages, 20)

		req := anthropicRequest{
			Model:     model,
			MaxTokens: 8192,
			System:    systemPrompt,
			Messages:  windowed,
			Tools:     ctx.ActiveTools, // live set — grows as model calls search_tools
			Stream:    true,
		}

		var responseBlocks []contentBlock
		var stopReason string
		var lastErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				wait := retryBackoff(attempt - 1)
				select {
				case <-ctx.Cancel:
					return
				case <-time.After(wait):
				}
			}
			var usage usageSnapshot
			var apiDurationMs int64
			responseBlocks, stopReason, usage, apiDurationMs, lastErr = streamRequest(apiKey, req, ctx, finalText)
			if lastErr == nil {
				totalTokens := usage.InputTokens + usage.OutputTokens
				costUSD := EstimateCost(model, usage.InputTokens, usage.OutputTokens)
				db.LogUsageEvent( //nolint:errcheck
					newID(),
					ctx.SessionID,
					ctx.ProjectPath,
					"anthropic",
					model,
					usage.InputTokens,
					usage.OutputTokens,
					totalTokens,
					costUSD,
					apiDurationMs,
				)
				break
			}
			// Only retry transient errors - check if error message contains status code
			errStr := lastErr.Error()
			isTransient := strings.Contains(errStr, "429") || strings.Contains(errStr, "500") ||
				strings.Contains(errStr, "502") || strings.Contains(errStr, "503") || strings.Contains(errStr, "504")
			if !isTransient {
				break
			}
		}
		if lastErr != nil {
			ctx.OnError(lastErr.Error())
			return
		}

		messages = append(messages, anthropicMessage{Role: "assistant", Content: responseBlocks})

		if stopReason != "tool_use" {
			break
		}

		var toolResults []contentBlock
		for _, block := range responseBlocks {
			if block.Type != "tool_use" {
				continue
			}
			inputMap, _ := block.Input.(map[string]any)
			if inputMap == nil {
				inputMap = map[string]any{}
			}

			if ctx.Quarantine != nil {
				if allowed, reason := ctx.Quarantine.Check(block.Name); !allowed {
					result := reason
					isError := true
					ctx.OnToolCall(block.Name, inputMap)
					ctx.OnToolResult(block.Name, result, isError)
					*allToolCalls = append(*allToolCalls, ToolCall{
						ID: block.ID, Name: block.Name, Input: inputMap,
						Result: result, IsError: isError,
					})
					toolResults = append(toolResults, contentBlock{
						Type:      "tool_result",
						ToolUseID: block.ID,
						Content:   result,
					})
					continue
				}
			}

			ctx.OnToolCall(block.Name, inputMap)

			start := time.Now()
			result, isError := executeTool(block.Name, inputMap, ctx)
			durationMs := time.Since(start).Milliseconds()

			if ctx.Quarantine != nil {
				ctx.Quarantine.RecordOutcome(block.Name, isError, func(msg string) {
					if ctx.OnError != nil {
						ctx.OnError(msg)
					}
					if ctx.OnBlocked != nil {
						ctx.OnBlocked(msg)
					}
				})
			}

			db.LogToolCall(newID(), ctx.SessionID, block.Name, inputMap, result, isError, durationMs) //nolint:errcheck
			ctx.OnToolResult(block.Name, result, isError)

			*allToolCalls = append(*allToolCalls, ToolCall{
				ID: block.ID, Name: block.Name, Input: inputMap,
				Result: result, IsError: isError,
			})
			toolResults = append(toolResults, contentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result,
			})
		}

		messages = append(messages, anthropicMessage{Role: "user", Content: toolResults})

		if ctx.isCancelled() {
			return
		}
	}
}

// streamRequest sends one Anthropic streaming request and returns all content blocks.
type usageSnapshot struct {
	InputTokens  int
	OutputTokens int
}

func tokenCountFromUsage(usage map[string]any, key string) int {
	v, ok := usage[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func streamRequest(
	apiKey string,
	req anthropicRequest,
	ctx *ChatContext,
	finalText *strings.Builder,
) ([]contentBlock, string, usageSnapshot, int64, error) {
	body, _ := json.Marshal(req)
	requestStart := time.Now()

	httpReq, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, "", usageSnapshot{}, 0, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
		return nil, "", usageSnapshot{}, 0, fmt.Errorf("anthropic error %d: %v", resp.StatusCode, errBody)
	}

	var (
		blocks     []contentBlock
		stopReason string
		curText    strings.Builder
		curTool    *contentBlock
		usage      usageSnapshot
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			break
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event["type"] {
		case "content_block_start":
			cb, _ := event["content_block"].(map[string]any)
			if cb == nil {
				continue
			}
			switch cb["type"] {
			case "text":
				curText.Reset()
			case "tool_use":
				id, _ := cb["id"].(string)
				name, _ := cb["name"].(string)
				curTool = &contentBlock{Type: "tool_use", ID: id, Name: name}
			}

		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			if delta == nil {
				continue
			}
			switch delta["type"] {
			case "text_delta":
				text, _ := delta["text"].(string)
				curText.WriteString(text)
				finalText.WriteString(text)
				ctx.OnChunk(text, false)
			case "input_json_delta":
				if curTool != nil {
					partial, _ := delta["partial_json"].(string)
					existing, _ := curTool.Input.(string)
					curTool.Input = existing + partial
				}
			}

		case "content_block_stop":
			if curTool != nil {
				// Parse accumulated JSON input
				inputStr, _ := curTool.Input.(string)
				if inputStr == "" {
					inputStr = "{}"
				}
				var inputMap map[string]any
				json.Unmarshal([]byte(inputStr), &inputMap) //nolint:errcheck
				curTool.Input = inputMap
				blocks = append(blocks, *curTool)
				curTool = nil
			} else if curText.Len() > 0 {
				blocks = append(blocks, contentBlock{Type: "text", Text: curText.String()})
				curText.Reset()
			}

		case "message_start":
			msg, _ := event["message"].(map[string]any)
			if msg != nil {
				usageMap, _ := msg["usage"].(map[string]any)
				if usageMap != nil {
					usage.InputTokens = tokenCountFromUsage(usageMap, "input_tokens")
					usage.OutputTokens = tokenCountFromUsage(usageMap, "output_tokens")
				}
			}

		case "message_delta":
			md, _ := event["delta"].(map[string]any)
			if md != nil {
				if deltaUsage, ok := md["usage"].(map[string]any); ok {
					if out := tokenCountFromUsage(deltaUsage, "output_tokens"); out > 0 {
						usage.OutputTokens = out
					}
				}
				if sr, ok := md["stop_reason"].(string); ok {
					stopReason = sr
				}
			}
		}
	}

	if ctx.Usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		ctx.Usage.Add(req.Model, usage.InputTokens, usage.OutputTokens)
	}

	return blocks, stopReason, usage, time.Since(requestStart).Milliseconds(), scanner.Err()
}

// newID generates a simple unique ID using time + random.
func newID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond()%1000)
}

// ── OpenAI types and agentic loop ─────────────────────────────────────────────

type openAIFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  any `json:"parameters"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIMessage struct {
	Role       string      `json:"role"`
	Content    any `json:"content,omitempty"` // string or nil
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  any `json:"tool_calls,omitempty"`
}

type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	Tools     []openAITool    `json:"tools,omitempty"`
	Stream    bool            `json:"stream"`
	KeepAlive string          `json:"keep_alive,omitempty"` // Ollama only — extends model TTL
}

// openAIToolsFrom converts the Anthropic tool definitions to OpenAI format.
func openAIToolsFrom(src []anthropicTool) []openAITool {
	out := make([]openAITool, len(src))
	for i, t := range src {
		out[i] = openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return out
}

// runOpenAICompatibleLoop runs the full agentic loop against an OpenAI-compatible chat completions API.
func runOpenAICompatibleLoop(
	ctx *ChatContext,
	providerName, model, endpointURL, apiKey string,
	useAuthorization bool,
	systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	// keepAlive is Ollama-specific — it resets the model unload timer on every
	// request so the model stays in VRAM between chat turns. Ignored by OpenAI.
	keepAlive := ""
	if providerName == "ollama" {
		keepAlive = os.Getenv("OLLAMA_KEEP_ALIVE")
		if keepAlive == "" {
			keepAlive = "30m"
		}
	}
	// Convert history to OpenAI message format
	windowedHistory := windowByVitality(history, 20)
	msgs := []openAIMessage{{Role: "system", Content: systemPrompt}}
	for _, m := range windowedHistory {
		content, _ := m.Content.(string)
		msgs = append(msgs, openAIMessage{Role: m.Role, Content: content})
	}

	oaiTools := openAIToolsFrom(ctx.ActiveTools)

	for {
		if ctx.isCancelled() {
			return
		}

		// Rebuild OAI tools each iteration — search_tools may have added new ones.
		oaiTools = openAIToolsFrom(ctx.ActiveTools)

		windowed := msgs
		if len(windowed) > 41 { // 1 system + 40 conversation
			windowed = append(msgs[:1:1], msgs[len(msgs)-40:]...)
		}

		req := openAIRequest{
			Model:     model,
			Messages:  windowed,
			Tools:     oaiTools,
			Stream:    true,
			KeepAlive: keepAlive,
		}

		body, _ := json.Marshal(req)
		httpReq, err := http.NewRequest("POST", endpointURL, bytes.NewReader(body))
		if err != nil {
			ctx.OnError(providerName + " request build: " + err.Error())
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if useAuthorization {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			ctx.OnError(providerName + " request: " + err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			var errBody map[string]any
			json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
			ctx.OnError(fmt.Sprintf("%s error %d: %v", providerName, resp.StatusCode, errBody))
			return
		}

		requestStart := time.Now()

		// Parse SSE stream
		type toolCallDelta struct {
			index int
			id    string
			name  string
			args  strings.Builder
		}
		toolCallMap := map[int]*toolCallDelta{}
		var textBuf strings.Builder
		finishReason := ""
		promptTokens := 0
		completionTokens := 0

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := line[6:]
			if data == "[DONE]" {
				break
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			if usageMap, ok := event["usage"].(map[string]any); ok {
				if in := tokenCountFromUsage(usageMap, "prompt_tokens"); in > 0 {
					promptTokens = in
				}
				if out := tokenCountFromUsage(usageMap, "completion_tokens"); out > 0 {
					completionTokens = out
				}
			}
			choices, _ := event["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				finishReason = fr
			}
			delta, _ := choice["delta"].(map[string]any)
			if delta == nil {
				continue
			}
			if text, ok := delta["content"].(string); ok && text != "" {
				textBuf.WriteString(text)
				finalText.WriteString(text)
				ctx.OnChunk(text, false)
			}
			if tcs, ok := delta["tool_calls"].([]any); ok {
				for _, tcRaw := range tcs {
					tc, _ := tcRaw.(map[string]any)
					if tc == nil {
						continue
					}
					idx := int(func() float64 { v, _ := tc["index"].(float64); return v }())
					if toolCallMap[idx] == nil {
						toolCallMap[idx] = &toolCallDelta{index: idx}
					}
					tcd := toolCallMap[idx]
					if id, ok := tc["id"].(string); ok && id != "" {
						tcd.id = id
					}
					if fn, ok := tc["function"].(map[string]any); ok {
						if name, ok := fn["name"].(string); ok && name != "" {
							tcd.name = name
						}
						if args, ok := fn["arguments"].(string); ok {
							tcd.args.WriteString(args)
						}
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ctx.OnError(providerName + " stream: " + err.Error())
			return
		}

		if ctx.Usage != nil && (promptTokens > 0 || completionTokens > 0) {
			ctx.Usage.Add(model, promptTokens, completionTokens)
		}

		db.LogUsageEvent( //nolint:errcheck
			newID(),
			ctx.SessionID,
			ctx.ProjectPath,
			providerName,
			model,
			promptTokens,
			completionTokens,
			promptTokens+completionTokens,
			EstimateCost(model, promptTokens, completionTokens),
			time.Since(requestStart).Milliseconds(),
		)

		// Build assistant message with tool_calls if any
		if len(toolCallMap) > 0 {
		// Assign synthetic IDs to any tool calls that Ollama/Gemma emitted without one.
		// An empty id in the assistant message causes Ollama to reject the continuation (400).
		for i, tcd := range toolCallMap {
			if tcd != nil && tcd.id == "" {
				toolCallMap[i].id = fmt.Sprintf("call_%s_%d", tcd.name, i)
			}
		}

		type oaiTC struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		tcsSlice := make([]oaiTC, 0, len(toolCallMap))
		for _, tcd := range toolCallMap {
			tcsSlice = append(tcsSlice, oaiTC{
				ID:   tcd.id,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: tcd.name, Arguments: tcd.args.String()},
			})
		}
		msgs = append(msgs, openAIMessage{Role: "assistant", ToolCalls: tcsSlice})
		} else if textBuf.Len() > 0 {
			msgs = append(msgs, openAIMessage{Role: "assistant", Content: textBuf.String()})
		}

		if finishReason != "tool_calls" || len(toolCallMap) == 0 {
			break
		}

		// Execute tools and add results.
		// Tool call error codes (compact, to avoid clogging context):
		//   E1 = bad JSON arguments   E2 = unknown tool   E3 = execution error
		for i := 0; i < len(toolCallMap); i++ {
			tcd := toolCallMap[i]
			if tcd == nil {
				continue
			}

			var inputMap map[string]any
			argsStr := tcd.args.String()
			if argsStr == "" || argsStr == "null" {
				argsStr = "{}"
			}
			if err := json.Unmarshal([]byte(argsStr), &inputMap); err != nil || inputMap == nil {
				// E1: malformed args — feed compact error back, skip execution
				errMsg := fmt.Sprintf("E1: invalid JSON arguments for %s. Correct the arguments and retry.", tcd.name)
				ctx.OnToolCall(tcd.name, map[string]any{"_raw": argsStr})
				ctx.OnToolResult(tcd.name, errMsg, true)
				msgs = append(msgs, openAIMessage{Role: "tool", ToolCallID: tcd.id, Content: errMsg})
				continue
			}

			ctx.OnToolCall(tcd.name, inputMap)
			start := time.Now()
			result, isError := executeTool(tcd.name, inputMap, ctx)
			durationMs := time.Since(start).Milliseconds()

			db.LogToolCall(newID(), ctx.SessionID, tcd.name, inputMap, result, isError, durationMs) //nolint:errcheck
			ctx.OnToolResult(tcd.name, result, isError)

			*allToolCalls = append(*allToolCalls, ToolCall{
				ID: tcd.id, Name: tcd.name, Input: inputMap,
				Result: result, IsError: isError,
			})
			msgs = append(msgs, openAIMessage{
				Role:       "tool",
				ToolCallID: tcd.id,
				Content:    result,
			})
		}

		if ctx.isCancelled() {
			return
		}
	}
}
