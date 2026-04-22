package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/engine/server/db"
	gofs "github.com/engine/server/fs"
	gogit "github.com/engine/server/git"
	gh "github.com/engine/server/github"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
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

// ollamaWarmKeepInterval is how often we ping Ollama to reset the model TTL.
// Must be less than OLLAMA_KEEP_ALIVE (default 30m) so the model never expires.
const ollamaWarmKeepInterval = 20 * time.Minute

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

		baseURL := os.Getenv("OLLAMA_BASE_URL")
		rootURL := ollamaRootURL(baseURL)

		keepAlive := os.Getenv("OLLAMA_KEEP_ALIVE")
		if keepAlive == "" {
			keepAlive = "30m"
		}

		// Only ping if a model is actually loaded right now.
		model := firstOllamaModel(rootURL+"/api/ps", "models", "name")
		if model == "" {
			continue
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
	switch {
	case strings.HasSuffix(normalized, "/chat/completions"):
		return normalized
	case strings.HasSuffix(normalized, "/v1"):
		return normalized + "/chat/completions"
	default:
		return normalized + "/v1/chat/completions"
	}
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
	OnThinking       func(content string) // thinking-model tokens (Ollama delta.thinking)
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
	// Always use an empty slice, never nil — nil marshals to JSON "null" which
	// Ollama's schema validator rejects. An empty array is the correct no-required-fields form.
	if required == nil {
		required = []string{}
	}
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
	// ── Discovery (deferred loader) ───────────────────────────────────────────
	// search_tools is the ONLY way new schemas enter the tools[] array.
	// When the model calls it, matched tools are injected into ctx.ActiveTools
	// and will be in the API tools[] on the very next iteration — no text faking.
	{
		Name:        "search_tools",
		Description: "Inject the schemas for tools you need into your active tool set. Call this before using any tool you don't already have. Returns matched tool names and adds their schemas to tools[] for the next call.",
		InputSchema: objSchema([]string{"query"}, map[string]interface{}{
			"query": strProp("Natural language description of what you want to do, e.g. 'run shell commands', 'commit to git', 'open GitHub issues'"),
		}),
	},
	// ── Core navigation ──────────────────────────────────────────────────────
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
	// ── Persistent memory ────────────────────────────────────────────────────
	// Memory tools are always in bootstrap — model can write/read its own memory
	// at any time without calling search_tools first.
	{
		Name:        "memory_save",
		Description: "Save a persistent memory for this project. Injected into every future system prompt. Save the ACTUAL STATE and WHY it is that way — not action labels or summaries of what you did.",
		InputSchema: objSchema([]string{"content", "category"}, map[string]interface{}{
			"content": strProp("The real constraint, state, or decision AND the reason it exists. " +
				"BAD: 'context bloat fix applied' or 'fixed slow generation'. " +
				"GOOD: 'read_file capped at 12KB — Gemma4:31b runs at ~7 tok/s on this M3 Pro; " +
				"a 50KB file read = ~12k tokens = ~27 min of silence before first output, editor was unusable'. " +
				"The WHY is what makes a memory useful. Without it the next session has no idea if the constraint still applies."),
			"category": strProp("One of: fact, decision, preference, constraint, error"),
		}),
	},
	{
		Name:        "memory_update",
		Description: "Update the content of an existing memory by ID. Always include a reason — it is stored in the version history so future sessions understand why this belief changed.",
		InputSchema: objSchema([]string{"id", "content", "reason"}, map[string]interface{}{
			"id":      strProp("Memory ID from memory_list"),
			"content": strProp("New content for the memory"),
			"reason":  strProp("Why this memory changed — stored in version history for future context"),
		}),
	},
	{
		Name:        "memory_list",
		Description: "List all persistent memories for this project. Returns id, category, content, importance, and how many times each has been updated.",
		InputSchema: objSchema([]string{}, map[string]interface{}{}),
	},
	{
		Name:        "memory_history",
		Description: "Show the full version history of a single memory — every previous value and the reason it was changed. Use when you need to understand how a belief evolved.",
		InputSchema: objSchema([]string{"id"}, map[string]interface{}{
			"id": strProp("Memory ID from memory_list"),
		}),
	},
	{
		Name:        "memory_delete",
		Description: "Delete a memory that is stale or wrong. Pass the ID from memory_list.",
		InputSchema: objSchema([]string{"id"}, map[string]interface{}{
			"id": strProp("Memory ID from memory_list"),
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

// allTools returns the full tool registry — every tool is available from the first request.
// All schemas are sent in the tools[] array so the model generates properly-typed args
// instead of freeform text. search_tools remains available as a filter/info helper but
// is no longer a required unlock gate.
func allTools() []anthropicTool {
	out := make([]anthropicTool, len(toolRegistry))
	copy(out, toolRegistry)
	return out
}

// bootstrapTools returns the minimal tool set sent on the first request.
// Only search_tools + memory tools. Everything else is deferred: when the model
// calls search_tools, matched schemas are injected into ctx.ActiveTools and will be
// present in the tools[] array on the very next API iteration — never as freeform text.
// read_file and list_directory are intentionally NOT here — they would cause the model
// to explore the codebase on every message including greetings and simple questions.
func bootstrapTools() []anthropicTool {
	return []anthropicTool{
		toolRegistryIndex["search_tools"],
		toolRegistryIndex["memory_save"],
		toolRegistryIndex["memory_update"],
		toolRegistryIndex["memory_list"],
		toolRegistryIndex["memory_history"],
		toolRegistryIndex["memory_delete"],
	}
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
		if len(result) > 16*1024 {
			result = result[:16*1024] + "\n...(output truncated — too large for context)"
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
		status, err := gogit.GetStatus(ctx.ProjectPath)
		if err != nil {
			return err.Error(), true
		}
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
		diff, err := gogit.GetDiff(ctx.ProjectPath, diffPath)
		if err != nil {
			return err.Error(), true
		}
		return diff, false

	case "git_commit":
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
		residualProfile, err := BuildAttentionResidualProfile(ctx.ProjectPath, ctx.SessionID, query, openTabs)
		if err != nil {
			residualProfile = map[string]float64{}
		}
		hits, err := SearchHistoryWithResiduals(ctx.ProjectPath, ctx.SessionID, query, openTabs, scope, limit, residualProfile)
		if err != nil {
			return err.Error(), true
		}
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

	case "memory_save":
		content := str("content")
		category := str("category")
		if content == "" {
			return "content is required", true
		}
		if category == "" {
			category = "fact"
		}
		// Strip line numbers — they drift the moment any edit lands and become
		// actively harmful when the model treats them as current ground truth.
		content = stripLineNumbers(content)
		if content == "" {
			return "memory content was empty after stripping line numbers — save concepts, not locations", true
		}
		importance := 0.7 // user-invoked saves are high-importance by default
		if category == "progress" {
			importance = 0.5
		}
		err := db.SaveMemory(db.Memory{
			ProjectPath: ctx.ProjectPath,
			SessionID:   ctx.SessionID,
			Category:    category,
			Content:     content,
			Importance:  importance,
		})
		if err != nil {
			return "failed to save memory: " + err.Error(), true
		}
		return fmt.Sprintf("Memory saved (%s): %s", category, content), false

	case "memory_update":
		id := str("id")
		content := str("content")
		reason := str("reason")
		if id == "" || content == "" {
			return "id, content, and reason are required", true
		}
		content = stripLineNumbers(content)
		if content == "" {
			return "memory content was empty after stripping line numbers", true
		}
		if reason == "" {
			reason = "no reason given"
		}
		if err := db.UpdateMemory(id, content, reason); err != nil {
			return "failed to update memory: " + err.Error(), true
		}
		return fmt.Sprintf("Memory updated (reason: %s)", reason), false

	case "memory_delete":
		id := str("id")
		if id == "" {
			return "id is required", true
		}
		if err := db.DeleteMemory(id); err != nil {
			return "failed to delete memory: " + err.Error(), true
		}
		return "Memory deleted", false

	case "memory_list":
		memories, err := db.GetProjectMemories(ctx.ProjectPath, 30)
		if err != nil {
			return "failed to list memories: " + err.Error(), true
		}
		if len(memories) == 0 {
			return "No memories saved yet for this project.", false
		}
		var b strings.Builder
		for _, m := range memories {
			versions := ""
			if m.VersionCount > 0 {
				versions = fmt.Sprintf(", updated %dx", m.VersionCount)
			}
			fmt.Fprintf(&b, "[%s] (%s%s) %s\n", m.ID[:8], m.Category, versions, m.Content)
		}
		return strings.TrimSpace(b.String()), false

	case "memory_history":
		id := str("id")
		if id == "" {
			return "id is required", true
		}
		versions, err := db.GetMemoryHistory(id)
		if err != nil {
			return "failed to get memory history: " + err.Error(), true
		}
		if len(versions) == 0 {
			return "No version history for this memory — it has never been updated.", false
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Version history for memory %s (%d changes):\n", id[:8], len(versions))
		for i, v := range versions {
			fmt.Fprintf(&b, "\n[v%d — %s]\n  was: %s\n  became: %s\n  reason: %s\n",
				len(versions)-i, v.ChangedAt[:10], v.OldContent, v.NewContent, v.Reason)
		}
		return strings.TrimSpace(b.String()), false

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

const maxTreeOutputBytes = 10 * 1024 // 10 KB max — prevents node_modules-style explosions

func formatTree(node *gofs.FileNode, depth int) string {
	var b strings.Builder
	writeTreeNode(node, depth, &b)
	s := b.String()
	if len(s) >= maxTreeOutputBytes {
		s = s[:maxTreeOutputBytes] + "\n... (directory listing truncated — use search_files or read specific paths)\n"
	}
	return s
}

func writeTreeNode(node *gofs.FileNode, depth int, b *strings.Builder) {
	if b.Len() >= maxTreeOutputBytes {
		return
	}
	prefix := strings.Repeat("  ", depth)
	icon := "📄"
	if node.Type == "directory" {
		icon = "📁"
	}
	fmt.Fprintf(b, "%s%s %s\n", prefix, icon, node.Name)
	for _, child := range node.Children {
		writeTreeNode(child, depth+1, b)
	}
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
	if err := db.SaveMessage(userMsgID, ctx.SessionID, "user", userMessage, nil); err != nil {
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
	residualProfile, err := BuildAttentionResidualProfile(ctx.ProjectPath, ctx.SessionID, userMessage, openTabs)
	if err != nil {
		residualProfile = map[string]float64{}
	}

	// Use residual-aware selection so older high-value context can survive beyond a flat recent window.
	windowResult := BuildAttentionConversationWindow(history, userMessage, openTabs, residualProfile)
	messages := windowResult.Messages

	// Initialise the active tool set. Start with bootstrap, then restore any tools
	// the model already used in this session — so it doesn't waste a round-trip
	// re-running search_tools every request.
	ctx.ActiveTools = bootstrapTools()
	if recentNames, err := db.GetRecentToolNames(ctx.SessionID, 50); err == nil {
		activeByName := map[string]bool{}
		for _, t := range ctx.ActiveTools {
			activeByName[t.Name] = true
		}
		for _, name := range recentNames {
			if t, ok := toolRegistryIndex[name]; ok && !activeByName[name] {
				ctx.ActiveTools = append(ctx.ActiveTools, t)
				activeByName[name] = true
			}
		}
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
	// Inject top residuals as always-on baked-in memory (not query-filtered).
	if memSection := BuildResidualMemorySection(ctx.ProjectPath, ctx.SessionID, 6); memSection != "" {
		systemLines = append(systemLines, "", memSection)
	}
	// Inject project memories as past observations — NOT ground truth.
	// Code changes. Line numbers drift. Architectural decisions get reversed.
	// Treat every memory as a hint from the past: useful for orientation,
	// but always verify against the actual file before acting on it.
	if memories, err := db.GetProjectMemories(ctx.ProjectPath, 20); err == nil && len(memories) > 0 {
		var mb strings.Builder
		mb.WriteString("## Past Observations (hints only — may be stale, verify before acting)\n")
		mb.WriteString("WARNING: Do NOT use line numbers from memories. Code changes. Always re-read the file.\n")
		charBudget := 1400
		now := time.Now().UTC()
		for _, m := range memories {
			age := memoryAge(m.UpdatedAt, now)
			volatility := ""
			if m.VersionCount >= 3 {
				volatility = fmt.Sprintf(", changed %dx — VOLATILE", m.VersionCount)
			} else if m.VersionCount > 0 {
				volatility = fmt.Sprintf(", changed %dx", m.VersionCount)
			}
			line := fmt.Sprintf("- [%s|%s%s] %s\n", m.Category, age, volatility, stripLineNumbers(m.Content))
			if mb.Len()+len(line) > charBudget {
				break
			}
			mb.WriteString(line)
			db.TouchMemory(m.ID)
		}
		systemLines = append(systemLines, "", strings.TrimSpace(mb.String()))
	}
	selectiveContext := BuildSelectiveContext(ctx.ProjectPath, session, userMessage, openTabs, residualProfile)
	if selectiveContext.Prompt != "" {
		systemLines = append(systemLines, selectiveContext.Prompt)
	}
	systemLines = append(systemLines,
		"",
		"## Tools",
		"All tools you've used in this session are already active. Call search_tools only if you need a capability you haven't used before.",
		"IMPORTANT: For greetings, simple questions, or anything not requiring codebase access — respond directly. Do NOT call any tools.",
		"Examples of what search_tools unlocks when needed:",
		"  search_tools(\"read files\")        → read_file, list_directory",
		"  search_tools(\"write files\")       → write_file",
		"  search_tools(\"run shell\")         → shell, test.run",
		"  search_tools(\"git\")               → git_status, git_diff, git_commit",
		"  search_tools(\"github issues\")     → github_* tools",
		"",
		"## Memory",
		"You have memory_save, memory_update, memory_delete, memory_list always available.",
		"Save the ACTUAL STATE and WHY — not action labels or summaries of what you did.",
		"",
		"BAD memory: 'context bloat fix applied' — tells nothing about the real constraint.",
		"GOOD memory: 'read_file capped at 12KB — Gemma4:31b runs at ~7 tok/s on this M3 Pro; " +
			"50KB file × 4 reads = ~50k tokens = 26+ min prefill hang, editor was unusable without this limit'.",
		"",
		"BAD memory: 'memory versioning added'.",
		"GOOD memory: 'memory_versions table is append-only; every memory_update stores old+new content and reason " +
			"so future sessions can see how a belief evolved, not just what it is now'.",
		"",
		"Save concepts not locations. Line numbers drift the moment any edit lands.",
		"Treat your own past memories as hints, not facts. Always re-read the file before editing.",
		"",
		"## Principles",
		"- When the user says to implement or continue, DO IT — don't re-read files already discussed",
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
	// Auto-extract memories from this turn — runs async, never blocks response.
	go autoExtractMemories(ctx.ProjectPath, ctx.SessionID, userMessage, finalText.String(), allToolCalls)
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

		responseBlocks, stopReason, err := streamRequest(apiKey, req, ctx, finalText)
		if err != nil {
			ctx.OnError(err.Error())
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
			ctx.OnToolCall(block.Name, inputMap)

			start := time.Now()
			result, isError := executeTool(block.Name, inputMap, ctx)
			durationMs := time.Since(start).Milliseconds()

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

	httpReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
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

type chatFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type chatTool struct {
	Type     string         `json:"type"`
	Function chatFunction `json:"function"`
}

type chatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content,omitempty"` // string or nil
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  interface{} `json:"tool_calls,omitempty"`
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []chatMessage   `json:"messages"`
	Tools     []chatTool      `json:"tools,omitempty"`
	Stream    bool            `json:"stream"`
	KeepAlive string          `json:"keep_alive,omitempty"` // Ollama only — extends model TTL
	Options   *ollamaOptions  `json:"options,omitempty"`    // Ollama only — context/generation limits
}

// ollamaOptions contains Ollama-specific generation parameters.
// Ignored by OpenAI/Anthropic-compatible endpoints.
type ollamaOptions struct {
	NumCtx int `json:"num_ctx,omitempty"` // token context window size
}

// chatToolsFrom converts the Anthropic tool definitions to OpenAI format.
func chatToolsFrom(src []anthropicTool) []chatTool {
	out := make([]chatTool, len(src))
	for i, t := range src {
		out[i] = chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return out
}

// stripLastToolExchange removes the most recent assistant+tool_calls block and all following
// tool-result messages from msgs. Used for self-healing when the provider rejects a
// continuation — the model will get a clean slate to redo its tool calls.
func stripLastToolExchange(msgs []chatMessage) []chatMessage {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].ToolCalls != nil {
			return msgs[:i]
		}
	}
	return msgs
}

// runChatCompletionsLoop runs the full agentic loop against any /v1/chat/completions endpoint.
func runChatCompletionsLoop(
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
	var ollamaOpts *ollamaOptions
	if providerName == "ollama" {
		keepAlive = os.Getenv("OLLAMA_KEEP_ALIVE")
		if keepAlive == "" {
			keepAlive = "30m"
		}
		// Don't set num_ctx by default — let Ollama use the model's native maximum.
		// Override with OLLAMA_NUM_CTX if you need to cap it (e.g. low VRAM).
		if v := os.Getenv("OLLAMA_NUM_CTX"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				ollamaOpts = &ollamaOptions{NumCtx: n}
			}
		}
	}
	// Convert history to OpenAI message format
	msgs := []chatMessage{{Role: "system", Content: systemPrompt}}
	for _, m := range history {
		content, _ := m.Content.(string)
		msgs = append(msgs, chatMessage{Role: m.Role, Content: content})
	}

	// Build a Go context that is cancelled when the user clicks Stop.
	// Using this on the HTTP request means the TCP connection to Ollama is torn
	// down immediately — the scanner exits and generation stops at the source.
	httpCtx, httpCancel := context.WithCancel(context.Background())
	defer httpCancel()
	if ctx.Cancel != nil {
		go func() {
			select {
			case <-ctx.Cancel:
				httpCancel()
			case <-httpCtx.Done():
			}
		}()
	}

	oaiTools := chatToolsFrom(ctx.ActiveTools)

	healAttempts := 0
	e1Counts := map[string]int{} // consecutive E1 failures per tool name
	for {
		if ctx.isCancelled() {
			return
		}

		// Rebuild OAI tools each iteration — search_tools may have added new ones.
		oaiTools = chatToolsFrom(ctx.ActiveTools)

		windowed := msgs
		if len(windowed) > 51 { // 1 system + 50 conversation
			windowed = append(msgs[:1], msgs[len(msgs)-50:]...)
		}

		req := chatRequest{
			Model:     model,
			Messages:  windowed,
			Tools:     oaiTools,
			Stream:    true,
			KeepAlive: keepAlive,
			Options:   ollamaOpts,
		}

		// Log estimated prompt size so context bloat is visible in server output.
		if estimatedTokens := estimateTokens(windowed); estimatedTokens > 0 {
			log.Printf("[ollama] prompt ~%d tokens (%d msgs)", estimatedTokens, len(windowed))
		}

		body, _ := json.Marshal(req)
		httpReq, err := http.NewRequestWithContext(httpCtx, "POST", endpointURL, bytes.NewReader(body))
		if err != nil {
			if httpCtx.Err() != nil {
				return // user cancelled
			}
			ctx.OnError(providerName + " request build: " + err.Error())
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if useAuthorization {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			if httpCtx.Err() != nil {
				return // user cancelled — not an error
			}
			// Network error — self-heal: strip any pending tool exchange and retry.
			if healAttempts < 2 {
				healAttempts++
				msgs = stripLastToolExchange(msgs)
				continue
			}
			ctx.OnError(providerName + " request: " + err.Error())
			return
		}

		if resp.StatusCode != 200 {
			var errBody map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
			resp.Body.Close()
			// Provider rejected the request — self-heal: strip the last tool exchange
			// so the model retries tool calls fresh without the malformed history.
			if healAttempts < 2 {
				healAttempts++
				msgs = stripLastToolExchange(msgs)
				continue
			}
			ctx.OnError(fmt.Sprintf("%s error %d: %v", providerName, resp.StatusCode, errBody))
			return
		}
		healAttempts = 0 // successful response — reset heal counter
		defer resp.Body.Close() //nolint:gocritic

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
			// Thinking-model tokens: Ollama puts them in delta.thinking (not delta.content).
			if thinking, ok := delta["thinking"].(string); ok && thinking != "" {
				if ctx.OnThinking != nil {
					ctx.OnThinking(thinking)
				}
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
						newName, hasName := fn["name"].(string)
						if hasName && newName != "" && tcd.name != "" && tcd.name != newName {
							// Gemma4/Ollama reuses index 0 for sequential tool calls in the same
							// stream response. A name change on an existing index means a new tool
							// call — allocate a new virtual index to avoid smashing the args together.
							virtualIdx := len(toolCallMap)
							for toolCallMap[virtualIdx] != nil {
								virtualIdx++
							}
							newTcd := &toolCallDelta{index: virtualIdx}
							toolCallMap[virtualIdx] = newTcd
							tcd = newTcd
						}
						if hasName && newName != "" {
							tcd.name = newName
						}
						if args, ok := fn["arguments"].(string); ok {
							tcd.args.WriteString(args)
						}
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			if httpCtx.Err() != nil {
				return // user cancelled — connection torn down intentionally
			}
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

		// DEFERRED LOADING BARRIER: if search_tools is in this batch alongside other
		// tools, the other tools were called before their schemas were in tools[].
		// Drop everything except search_tools so the next API iteration has proper
		// schemas loaded before the model generates args for those tools.
		hasSearchTools := false
		for _, tcd := range toolCallMap {
			if tcd != nil && tcd.name == "search_tools" {
				hasSearchTools = true
				break
			}
		}
		if hasSearchTools && len(toolCallMap) > 1 {
			for i, tcd := range toolCallMap {
				if tcd != nil && tcd.name != "search_tools" {
					toolCallMap[i] = nil
				}
			}
		}

		tcsSlice := make([]oaiTC, 0, len(toolCallMap))
		for i, tcd := range toolCallMap {
			if tcd == nil {
				continue
			}
			// Normalize empty/null args to "{}".
			argsStr := tcd.args.String()
			if argsStr == "" || argsStr == "null" {
				argsStr = "{}"
			}
			// Normalize concatenated JSON objects (Gemma emits {"path":"a"}{"path":"b"})
			// to just the first valid object, both here (so Ollama accepts the continuation)
			// and later at execution time. Without this, Ollama rejects the continuation
			// request with 400 because Arguments must be valid JSON.
			if json.Unmarshal([]byte(argsStr), &(map[string]interface{}{})) != nil {
				var first map[string]interface{}
				dec := json.NewDecoder(strings.NewReader(argsStr))
				if dec.Decode(&first) == nil {
					if nb, err := json.Marshal(first); err == nil {
						argsStr = string(nb)
						tcd.args.Reset()
						tcd.args.WriteString(argsStr)
					}
				}
			}
			tcsSlice = append(tcsSlice, oaiTC{
				ID:   toolCallMap[i].id,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: tcd.name, Arguments: argsStr},
			})
		}
		// Preserve any text the model emitted alongside tool calls (e.g. a plan).
		// OpenAI wire format allows content + tool_calls in the same assistant message.
		assistantMsg := chatMessage{Role: "assistant", ToolCalls: tcsSlice}
		if textBuf.Len() > 0 {
			assistantMsg.Content = textBuf.String()
			finalText.WriteString(textBuf.String())
		}
		msgs = append(msgs, assistantMsg)
		} else if textBuf.Len() > 0 {
			msgs = append(msgs, chatMessage{Role: "assistant", Content: textBuf.String()})
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
				// Gemma sometimes concatenates two JSON objects — extract the first valid one.
				dec := json.NewDecoder(strings.NewReader(argsStr))
				if decErr := dec.Decode(&inputMap); decErr != nil || inputMap == nil {
					e1Counts[tcd.name]++
					var errMsg string
					if e1Counts[tcd.name] >= 3 {
						errMsg = fmt.Sprintf("E1: %s arguments are still malformed after multiple attempts. Skip this tool and use a different approach.", tcd.name)
					} else {
						errMsg = fmt.Sprintf("E1: invalid JSON arguments for %s. Each tool call must have a single valid JSON object. Correct and retry.", tcd.name)
					}
					ctx.OnToolCall(tcd.name, map[string]interface{}{"_raw": argsStr})
					ctx.OnToolResult(tcd.name, errMsg, true)
					msgs = append(msgs, chatMessage{Role: "tool", ToolCallID: tcd.id, Content: errMsg})
					continue
				}
			}
			e1Counts[tcd.name] = 0 // successful parse resets the counter

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
			msgs = append(msgs, chatMessage{
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

// autoExtractMemories scans a completed conversation turn and saves atomic memories for
// any signals found. Runs asynchronously after each assistant response — never blocks.
// This is lightweight pattern-matching, not an LLM call, so it's always fast.
func autoExtractMemories(projectPath, sessionID, userMsg, assistantMsg string, toolCalls []ToolCall) {
lower := strings.ToLower(assistantMsg)
user := strings.ToLower(userMsg)

type candidate struct {
content    string
category   string
importance float64
}
var found []candidate

// Detect explicit decisions in assistant text.
decisionMarkers := []string{
"decided to ", "we'll use ", "using ", "going with ",
"architecture:", "approach:", "design decision:",
}
for _, line := range strings.Split(assistantMsg, "\n") {
ll := strings.ToLower(strings.TrimSpace(line))
for _, marker := range decisionMarkers {
if strings.Contains(ll, marker) && len(line) > 20 && len(line) < 200 {
found = append(found, candidate{strings.TrimSpace(line), "decision", 0.75})
break
}
}
}

// Detect fixed bugs / resolved errors — only save if the line explains WHY it broke,
// not just that it was fixed. "Fixed X" is an action label. "Fixed X because Y" is state.
if strings.Contains(lower, "fixed") || strings.Contains(lower, "resolved") {
	for _, line := range strings.Split(assistantMsg, "\n") {
		ll := strings.ToLower(strings.TrimSpace(line))
		hasResolution := strings.Contains(ll, "fixed") || strings.Contains(ll, "resolved")
		hasReason := strings.Contains(ll, "because") || strings.Contains(ll, "due to") ||
			strings.Contains(ll, "caused by") || strings.Contains(ll, "was ") || strings.Contains(ll, "—")
		if hasResolution && hasReason && len(line) > 20 && len(line) < 220 {
			found = append(found, candidate{strings.TrimSpace(line), "error", 0.6})
			break
		}
	}
}

// Detect user preferences from user message.
prefMarkers := []string{"prefer ", "always ", "never ", "don't ", "please use ", "i want "}
for _, marker := range prefMarkers {
if strings.Contains(user, marker) && len(userMsg) < 200 {
found = append(found, candidate{strings.TrimSpace(userMsg), "preference", 0.8})
break
}
}

// Detect tool-call progress — only git_commit is meaningful; write_file is noise.
for _, tc := range toolCalls {
	switch tc.Name {
	case "git_commit":
		inputMap, _ := tc.Input.(map[string]interface{})
		msg, _ := inputMap["message"].(string)
		if msg != "" && len(msg) < 200 {
			found = append(found, candidate{
				fmt.Sprintf("Last commit: %s", msg),
				"progress", 0.4,
			})
		}
	// write_file deliberately excluded — "Wrote file: X" is action noise, not state
	}
}

// Dedup against existing memories to avoid spamming duplicates.
existing, err := db.GetProjectMemories(projectPath, 50)
if err != nil {
existing = nil
}
existingSet := make(map[string]bool, len(existing))
for _, m := range existing {
existingSet[strings.ToLower(strings.TrimSpace(m.Content))] = true
}

	for _, c := range found {
		// Strip line numbers before saving — they drift the moment any edit lands.
		clean := stripLineNumbers(c.content)
		if clean == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(clean))
		if existingSet[key] {
			continue
		}
		db.SaveMemory(db.Memory{ //nolint:errcheck
			ProjectPath: projectPath,
			SessionID:   sessionID,
			Category:    c.category,
			Content:     clean,
			Importance:  c.importance,
		})
		existingSet[key] = true // prevent dupes within the same batch
	}
}

// estimateTokens approximates token count for a slice of chat messages.
// Uses the chars/4 heuristic — accurate enough for logging and budget checks.
func estimateTokens(msgs []chatMessage) int {
total := 0
for _, m := range msgs {
if str, ok := m.Content.(string); ok {
total += len(str) / 4
}
total += 4 // role overhead per message
}
return total
}

// lineNumberPattern matches things like "line 42", "at ~1153", ":42:", "line ~90".
var lineNumberPattern = regexp.MustCompile(`(?i)(line\s*~?\d+|at\s*~?\d+|:\d{2,}[:\s]|\bat\s+line\s+\d+)`)

// stripLineNumbers removes line number references from memory content.
// Line numbers drift the moment any edit lands — storing them as memories
// causes the model to navigate to wrong locations and make incorrect edits.
func stripLineNumbers(s string) string {
return strings.TrimSpace(lineNumberPattern.ReplaceAllString(s, ""))
}

// memoryAge returns a human-readable age string for a memory given its updatedAt timestamp.
// Shown alongside every memory so the model can judge how stale it might be.
func memoryAge(updatedAt string, now time.Time) string {
t, err := time.Parse(time.RFC3339, updatedAt)
if err != nil {
return "?"
}
d := now.Sub(t)
switch {
case d < 10*time.Minute:
return "just now"
case d < time.Hour:
return fmt.Sprintf("%dm ago", int(d.Minutes()))
case d < 24*time.Hour:
return fmt.Sprintf("%dh ago", int(d.Hours()))
default:
return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
}
