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
	"time"

	"github.com/engine/server/db"
	gofs "github.com/engine/server/fs"
	gogit "github.com/engine/server/git"
	gh "github.com/engine/server/github"
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

	payload, _ := json.Marshal(map[string]interface{}{
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

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	items, _ := payload[listKey].([]interface{})
	for _, item := range items {
		entry, _ := item.(map[string]interface{})
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
	OnToolCall       func(name string, input interface{})
	OnToolResult     func(name string, result interface{}, isError bool)
	OnError          func(err string)
	OnSessionUpdated func(session *db.Session)
	// GetOpenTabs returns the client's currently open editor tabs.
	GetOpenTabs func() []TabInfo
	// SendToClient sends an arbitrary message back to the WS client.
	SendToClient func(msgType string, payload interface{})
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
	Input   interface{} `json:"input"`
	Result  interface{} `json:"result,omitempty"`
	IsError bool        `json:"isError,omitempty"`
}

// --- Anthropic API types (raw HTTP, no official Go SDK) ---

type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string | []contentBlock
}

type contentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
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
func strProp(desc string) interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

func objSchema(required []string, props map[string]interface{}) interface{} {
	return map[string]interface{}{
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
		InputSchema: objSchema([]string{"query"}, map[string]interface{}{
			"query": strProp("Natural language description of what you want to do, e.g. 'run shell commands', 'commit to git', 'open GitHub issues'"),
		}),
	},
	// ── Core navigation (always available) ──────────────────────────────────
	{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute path to the file"),
		}),
	},
	{
		Name:        "list_directory",
		Description: "List files and directories at a path, up to 4 levels deep.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute directory path"),
		}),
	},
	// ── File operations ──────────────────────────────────────────────────────
	{
		Name:        "write_file",
		Description: "Create or overwrite a file with the given content. Parent directories are created automatically. Use this whenever you need to create a new file or save content to disk.",
		InputSchema: objSchema([]string{"path", "content"}, map[string]interface{}{
			"path":    strProp("Absolute path to write to"),
			"content": strProp("Content to write"),
		}),
	},
	{
		Name:        "open_file",
		Description: "Open an EXISTING file in the editor so the user can view it. This does NOT create files — use write_file to create new files.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute path to the existing file to open"),
		}),
	},
	// ── Shell / execution ────────────────────────────────────────────────────
	{
		Name:        "shell",
		Description: "Execute a shell command and return stdout + stderr. Use for running tests, builds, installs, etc.",
		InputSchema: objSchema([]string{"command"}, map[string]interface{}{
			"command": strProp("Shell command to run"),
			"cwd":     strProp("Working directory (optional, defaults to project root)"),
		}),
	},
	{
		Name:        "test.run",
		Description: "Run a test command in the client terminal and observe output for issue resolution.",
		InputSchema: objSchema([]string{"command"}, map[string]interface{}{
			"command":    strProp("Shell command to run"),
			"terminalId": strProp("Terminal ID to run in (optional)"),
			"issue":      strProp("Issue description to validate against"),
		}),
	},
	// ── Search ───────────────────────────────────────────────────────────────
	{
		Name:        "search_files",
		Description: "Search for a pattern in files using ripgrep. Returns matching lines with file paths and line numbers.",
		InputSchema: objSchema([]string{"pattern"}, map[string]interface{}{
			"pattern":      strProp("Regex pattern to search for"),
			"directory":    strProp("Directory to search in (optional, defaults to project root)"),
			"file_pattern": strProp("Glob pattern to filter files (e.g. \"*.go\")"),
		}),
	},
	{
		Name:        "search_history",
		Description: "Search Engine's stored workspace history across prior sessions, summaries, learnings, and validation evidence.",
		InputSchema: objSchema([]string{"query"}, map[string]interface{}{
			"query": strProp("Keywords or question to search for in prior Engine history"),
			"scope": strProp("Optional scope: project or current-session"),
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Optional max results to return (default 5, max 10)",
			},
		}),
	},
	// ── Git ──────────────────────────────────────────────────────────────────
	{
		Name:        "git_status",
		Description: "Get the current git status: branch, staged/unstaged/untracked files.",
		InputSchema: objSchema(nil, map[string]interface{}{}),
	},
	{
		Name:        "git_diff",
		Description: "Get git diff for current changes (staged + unstaged).",
		InputSchema: objSchema(nil, map[string]interface{}{
			"path": strProp("Specific file path to diff (optional)"),
		}),
	},
	{
		Name:        "git_commit",
		Description: "Stage all changes and create a git commit.",
		InputSchema: objSchema([]string{"message"}, map[string]interface{}{
			"message": strProp("Commit message"),
		}),
	},
	// ── Editor UI ────────────────────────────────────────────────────────────
	{
		Name:        "get_system_info",
		Description: "Get current system resource usage: memory (used/total/%), CPU %, and disk usage for the project path.",
		InputSchema: objSchema(nil, map[string]interface{}{}),
	},
	{
		Name:        "list_open_tabs",
		Description: "List the files currently open in the editor. Returns path, whether it is the active tab, and whether it has unsaved changes.",
		InputSchema: objSchema(nil, map[string]interface{}{}),
	},
	{
		Name:        "close_tab",
		Description: "Close a specific file tab in the editor. Will not close tabs with unsaved changes unless force is true.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path":  strProp("Absolute path of the tab to close"),
			"force": map[string]interface{}{"type": "boolean", "description": "Force close even if there are unsaved changes"},
		}),
	},
	{
		Name:        "focus_tab",
		Description: "Bring a specific file tab to the foreground in the editor.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute path of the tab to focus"),
		}),
	},
	// ── GitHub ───────────────────────────────────────────────────────────────
	{
		Name:        "github_list_issues",
		Description: "List GitHub issues for a repository.",
		InputSchema: objSchema([]string{"owner", "repo"}, map[string]interface{}{
			"owner": strProp("Repository owner"),
			"repo":  strProp("Repository name"),
			"state": strProp("Issue state: open, closed, or all (default: open)"),
		}),
	},
	{
		Name:        "github_get_issue",
		Description: "Get details of a specific GitHub issue.",
		InputSchema: objSchema([]string{"owner", "repo", "number"}, map[string]interface{}{
			"owner":  strProp("Repository owner"),
			"repo":   strProp("Repository name"),
			"number": map[string]interface{}{"type": "number", "description": "Issue number"},
		}),
	},
	{
		Name:        "github_close_issue",
		Description: "Close a GitHub issue with an optional comment explaining the resolution.",
		InputSchema: objSchema([]string{"owner", "repo", "number"}, map[string]interface{}{
			"owner":   strProp("Repository owner"),
			"repo":    strProp("Repository name"),
			"number":  map[string]interface{}{"type": "number", "description": "Issue number"},
			"comment": strProp("Closing comment with resolution evidence"),
		}),
	},
	{
		Name:        "github_create_issue",
		Description: "Create a new GitHub issue.",
		InputSchema: objSchema([]string{"owner", "repo", "title"}, map[string]interface{}{
			"owner": strProp("Repository owner"),
			"repo":  strProp("Repository name"),
			"title": strProp("Issue title"),
			"body":  strProp("Issue body/description"),
		}),
	},
	{
		Name:        "github_comment",
		Description: "Add a comment to a GitHub issue.",
		InputSchema: objSchema([]string{"owner", "repo", "number", "body"}, map[string]interface{}{
			"owner":  strProp("Repository owner"),
			"repo":   strProp("Repository name"),
			"number": map[string]interface{}{"type": "number", "description": "Issue number"},
			"body":   strProp("Comment body"),
		}),
	},
	// ── Additional git operations ─────────────────────────────────────────
	{
		Name:        "git_push",
		Description: "Push the current branch to the remote repository. Requires user approval.",
		InputSchema: objSchema(nil, map[string]interface{}{
			"remote": strProp("Remote name (optional, defaults to 'origin')"),
		}),
	},
	{
		Name:        "git_pull",
		Description: "Pull latest changes from the remote repository for the current branch.",
		InputSchema: objSchema(nil, map[string]interface{}{
			"remote": strProp("Remote name (optional, defaults to 'origin')"),
		}),
	},
	{
		Name:        "git_branch",
		Description: "Create a new branch and switch to it, switch to an existing branch, or list all local branches.",
		InputSchema: objSchema(nil, map[string]interface{}{
			"name":   strProp("Branch name to create or switch to (optional — omit to list all branches)"),
			"create": map[string]interface{}{"type": "boolean", "description": "Create the branch if true, otherwise switch to existing"},
		}),
	},
	// ── System control ────────────────────────────────────────────────────
	{
		Name:        "process_list",
		Description: "List running processes with PID, name, CPU%, and memory usage. Optionally filter by name.",
		InputSchema: objSchema(nil, map[string]interface{}{
			"filter": strProp("Substring to filter process names (optional)"),
		}),
	},
	{
		Name:        "process_kill",
		Description: "Kill a running process by PID. Requires explicit approval. Use TERM for graceful shutdown, KILL to force.",
		InputSchema: objSchema([]string{"pid"}, map[string]interface{}{
			"pid":    map[string]interface{}{"type": "number", "description": "Process ID to kill"},
			"signal": strProp("Signal to send: TERM (default, graceful) or KILL (force)"),
		}),
	},
	{
		Name:        "open_url",
		Description: "Open a URL in the system default browser.",
		InputSchema: objSchema([]string{"url"}, map[string]interface{}{
			"url": strProp("URL to open"),
		}),
	},
	{
		Name:        "screenshot",
		Description: "Capture a screenshot of the current screen. Returns the file path of the saved PNG for further inspection.",
		InputSchema: objSchema(nil, map[string]interface{}{
			"path": strProp("Output path for the PNG (optional, defaults to /tmp/engine-screenshot-{timestamp}.png)"),
		}),
	},
	// ── Git clone ─────────────────────────────────────────────────────────────
	{
		Name:        "git_clone",
		Description: "Clone a git repository to a local path. Use when you need to work on a repository that is not already cloned locally.",
		InputSchema: objSchema([]string{"url"}, map[string]interface{}{
			"url":  strProp("Repository URL to clone (https:// or git@ format)"),
			"path": strProp("Local destination path (optional; defaults to ~/engine-workspace/<repo-name>)"),
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
var bootstrapToolNames = []string{"search_tools", "read_file", "list_directory"}

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
func executeTool(name string, input map[string]interface{}, ctx *ChatContext) (string, bool) {
	return aiExecuteTool(name, input, ctx)
}

func ExecuteToolForTest(name string, input map[string]interface{}, ctx *ChatContext) (string, bool) {
	return aiExecuteTool(name, input, ctx)
}

func aiExecuteTool(name string, input map[string]interface{}, ctx *ChatContext) (string, bool) {
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
			ctx.SendToClient("file.saved", map[string]interface{}{"path": path})
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
		ctx.SendToClient("test.run", map[string]interface{}{
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
		if ctx.RequestApproval == nil {
			return "git_push requires approval, but no approval handler is available.", true
		}
		branch, _ := gogit.GetCurrentBranch(ctx.ProjectPath)
		remote := str("remote")
		if remote == "" {
			remote = "origin"
		}
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

	default:
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

	if ctx.Usage == nil {
		ctx.Usage = &SessionUsage{}
	}
	if ctx.Quarantine == nil {
		ctx.Quarantine = NewToolQuarantine()
	}

	// Build system prompt
	branch, _ := gogit.GetCurrentBranch(ctx.ProjectPath)
	systemLines := []string{
		"You are the AI assistant for Engine — an AI-native code editor.",
		"You ARE the editor. You have full control over the project.",
		"",
		fmt.Sprintf("Project: %s", ctx.ProjectPath),
		fmt.Sprintf("Branch: %s", branch),
	}
	selectiveContext := BuildSelectiveContext(ctx.ProjectPath, session, userMessage, openTabs, residualProfile)
	if selectiveContext.Prompt != "" {
		systemLines = append(systemLines, selectiveContext.Prompt)
	}
	systemLines = append(systemLines,
		"",
		"## Tool discovery",
		"You start each request with only three tools: search_tools, read_file, list_directory.",
		"Before executing any task, call search_tools with a description of what you need to do.",
		"search_tools injects the matching schemas into your active tool set immediately — no extra round-trip needed.",
		"Examples:",
		"  search_tools(\"write files to disk\")      → unlocks write_file",
		"  search_tools(\"run shell commands build\")  → unlocks shell, test.run",
		"  search_tools(\"git commit push status\")    → unlocks git_status, git_diff, git_commit",
		"  search_tools(\"github issues\")             → unlocks github_* tools",
		"Plan first, discover the tools you need, then execute. Do not call tools you haven't discovered.",
		"",
		"## Principles",
		"- Always validate changes by running the code, not just checking syntax",
		"- File operations are confined to the current project root unless the user explicitly elevates them",
		"- Risky shell commands and git commits require explicit user approval",
		"- When you open a file, the user sees it in Engine immediately",
		"- Be decisive: fix problems completely, not just symptoms",
		"- Use search_history when prior workspace decisions or debugging context may matter",
	)

	systemPrompt := strings.Join(systemLines, "\n")

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
	var tc interface{}
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
	for {
		if ctx.isCancelled() {
			return
		}

		windowed := messages
		if len(windowed) > 50 {
			windowed = windowed[len(windowed)-50:]
		}

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
			responseBlocks, stopReason, lastErr = streamRequest(apiKey, req, ctx, finalText)
			if lastErr == nil {
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
			inputMap, _ := block.Input.(map[string]interface{})
			if inputMap == nil {
				inputMap = map[string]interface{}{}
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
func streamRequest(
	apiKey string,
	req anthropicRequest,
	ctx *ChatContext,
	finalText *strings.Builder,
) ([]contentBlock, string, error) {
	body, _ := json.Marshal(req)

	httpReq, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var errBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
		return nil, "", fmt.Errorf("anthropic error %d: %v", resp.StatusCode, errBody)
	}

	var (
		blocks     []contentBlock
		stopReason string
		curText    strings.Builder
		curTool    *contentBlock
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

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event["type"] {
		case "content_block_start":
			cb, _ := event["content_block"].(map[string]interface{})
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
			delta, _ := event["delta"].(map[string]interface{})
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
				var inputMap map[string]interface{}
				json.Unmarshal([]byte(inputStr), &inputMap) //nolint:errcheck
				curTool.Input = inputMap
				blocks = append(blocks, *curTool)
				curTool = nil
			} else if curText.Len() > 0 {
				blocks = append(blocks, contentBlock{Type: "text", Text: curText.String()})
				curText.Reset()
			}

		case "message_start":
			msg, _ := event["message"].(map[string]interface{})
			if msg != nil {
				usage, _ := msg["usage"].(map[string]interface{})
				if usage != nil && ctx.Usage != nil {
					in := int(func() float64 { v, _ := usage["input_tokens"].(float64); return v }())
					out := int(func() float64 { v, _ := usage["output_tokens"].(float64); return v }())
					ctx.Usage.Add(req.Model, in, out)
				}
			}

		case "message_delta":
			md, _ := event["delta"].(map[string]interface{})
			if md != nil {
				if sr, ok := md["stop_reason"].(string); ok {
					stopReason = sr
				}
			}
		}
	}

	return blocks, stopReason, scanner.Err()
}

// newID generates a simple unique ID using time + random.
func newID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond()%1000)
}

// ── OpenAI types and agentic loop ─────────────────────────────────────────────

type openAIFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content,omitempty"` // string or nil
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  interface{} `json:"tool_calls,omitempty"`
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
	msgs := []openAIMessage{{Role: "system", Content: systemPrompt}}
	for _, m := range history {
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
		if len(windowed) > 51 { // 1 system + 50 conversation
			windowed = append(msgs[:1], msgs[len(msgs)-50:]...)
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
			var errBody map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
			ctx.OnError(fmt.Sprintf("%s error %d: %v", providerName, resp.StatusCode, errBody))
			return
		}

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
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			choices, _ := event["choices"].([]interface{})
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]interface{})
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				finishReason = fr
			}
			delta, _ := choice["delta"].(map[string]interface{})
			if delta == nil {
				continue
			}
			if text, ok := delta["content"].(string); ok && text != "" {
				textBuf.WriteString(text)
				finalText.WriteString(text)
				ctx.OnChunk(text, false)
			}
			if tcs, ok := delta["tool_calls"].([]interface{}); ok {
				for _, tcRaw := range tcs {
					tc, _ := tcRaw.(map[string]interface{})
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
					if fn, ok := tc["function"].(map[string]interface{}); ok {
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

			var inputMap map[string]interface{}
			argsStr := tcd.args.String()
			if argsStr == "" || argsStr == "null" {
				argsStr = "{}"
			}
			if err := json.Unmarshal([]byte(argsStr), &inputMap); err != nil || inputMap == nil {
				// E1: malformed args — feed compact error back, skip execution
				errMsg := fmt.Sprintf("E1: invalid JSON arguments for %s. Correct the arguments and retry.", tcd.name)
				ctx.OnToolCall(tcd.name, map[string]interface{}{"_raw": argsStr})
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
