package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/myeditor/server/db"
	gofs "github.com/myeditor/server/fs"
	gogit "github.com/myeditor/server/git"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

const defaultModel = "claude-opus-4-5"

// ChatContext carries callbacks for streaming responses to the WebSocket client.
type ChatContext struct {
	ProjectPath string
	SessionID   string
	OnChunk     func(content string, done bool)
	OnToolCall  func(name string, input interface{})
	OnToolResult func(name string, result interface{}, isError bool)
	OnError     func(err string)
	// GetOpenTabs returns the client's currently open editor tabs.
	GetOpenTabs func() []TabInfo
	// SendToClient sends an arbitrary message back to the WS client.
	SendToClient func(msgType string, payload interface{})
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
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Input      interface{} `json:"input,omitempty"`
	ToolUseID  string      `json:"tool_use_id,omitempty"`
	Content    string      `json:"content,omitempty"`
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

var tools = []anthropicTool{
	{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute path to the file"),
		}),
	},
	{
		Name:        "write_file",
		Description: "Write content to a file (creates file and parent dirs if needed).",
		InputSchema: objSchema([]string{"path", "content"}, map[string]interface{}{
			"path":    strProp("Absolute path to write to"),
			"content": strProp("Content to write"),
		}),
	},
	{
		Name:        "list_directory",
		Description: "List files and directories at a path, up to 4 levels deep.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute directory path"),
		}),
	},
	{
		Name:        "shell",
		Description: "Execute a shell command and return stdout + stderr. Use for running tests, builds, installs, etc.",
		InputSchema: objSchema([]string{"command"}, map[string]interface{}{
			"command": strProp("Shell command to run"),
			"cwd":     strProp("Working directory (optional, defaults to project root)"),
		}),
	},
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
	{
		Name:        "open_file",
		Description: "Open a file in the editor UI so the user can see it.",
		InputSchema: objSchema([]string{"path"}, map[string]interface{}{
			"path": strProp("Absolute path to the file to open"),
		}),
	},
	{
		Name:        "get_system_info",
		Description: "Get current system resource usage: memory (used/total/%), CPU %, and disk usage for the project path. Use this to check if the system is under memory or CPU pressure before starting intensive operations.",
		InputSchema: objSchema(nil, map[string]interface{}{}),
	},
	{
		Name:        "list_open_tabs",
		Description: "List the files currently open in the editor. Returns path, whether it is the active tab, and whether it has unsaved changes.",
		InputSchema: objSchema(nil, map[string]interface{}{}),
	},
	{
		Name:        "close_tab",
		Description: "Close a specific file tab in the editor. Use to reduce memory usage or clean up irrelevant files. Will not close tabs with unsaved changes unless force is true.",
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
}

// executeTool runs the named tool and returns (result string, isError bool).
func executeTool(name string, input map[string]interface{}, ctx *ChatContext) (string, bool) {
	str := func(key string) string {
		v, _ := input[key].(string)
		return v
	}
	boolVal := func(key string) bool {
		v, _ := input[key].(bool)
		return v
	}

	switch name {
	case "read_file":
		fc, err := gofs.ReadFile(str("path"))
		if err != nil {
			return err.Error(), true
		}
		return fc.Content, false

	case "write_file":
		if err := gofs.WriteFile(str("path"), str("content")); err != nil {
			return err.Error(), true
		}
		return "File written: " + str("path"), false

	case "list_directory":
		tree, err := gofs.GetTree(str("path"), 4)
		if err != nil {
			return err.Error(), true
		}
		return formatTree(tree, 0), false

	case "shell":
		cwd := str("cwd")
		if cwd == "" {
			cwd = ctx.ProjectPath
		}
		cmd := exec.Command("bash", "-c", str("command"))
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
		diff, err := gogit.GetDiff(ctx.ProjectPath, str("path"))
		if err != nil {
			return err.Error(), true
		}
		return diff, false

	case "git_commit":
		hash, err := gogit.Commit(ctx.ProjectPath, str("message"))
		if err != nil {
			return err.Error(), true
		}
		return "Committed: " + hash, false

	case "open_file":
		if ctx.SendToClient != nil {
			ctx.SendToClient("editor.open", map[string]string{"path": str("path")})
		}
		return "Opening " + str("path"), false

	case "get_system_info":
		return getSystemInfo(ctx.ProjectPath), false

	case "list_open_tabs":
		if ctx.GetOpenTabs == nil {
			return "[]", false
		}
		tabs := ctx.GetOpenTabs()
		b, _ := json.MarshalIndent(tabs, "", "  ")
		return string(b), false

	case "close_tab":
		path := str("path")
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
		if ctx.SendToClient != nil {
			ctx.SendToClient("editor.tab.focus", map[string]string{"path": str("path")})
		}
		return "Focused tab: " + str("path"), false

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
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		ctx.OnError("ANTHROPIC_API_KEY not set")
		return
	}
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = defaultModel
	}

	// Persist user message
	userMsgID := newID()
	if err := db.SaveMessage(userMsgID, ctx.SessionID, "user", userMessage, nil); err != nil {
		ctx.OnError("Failed to save message: " + err.Error())
		return
	}

	history, _ := db.GetMessages(ctx.SessionID)
	session, _ := db.GetSession(ctx.SessionID)

	// Build Anthropic messages from DB history (exclude the just-saved user message)
	messages := make([]anthropicMessage, 0, len(history))
	for _, m := range history[:len(history)-1] {
		messages = append(messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, anthropicMessage{Role: "user", Content: userMessage})

	// Build system prompt
	branch, _ := gogit.GetCurrentBranch(ctx.ProjectPath)
	systemLines := []string{
		"You are the AI assistant for MyEditor — an AI-native code editor.",
		"You ARE the editor. You have full control: read files, write files, run commands, search code, commit changes.",
		"",
		fmt.Sprintf("Project: %s", ctx.ProjectPath),
		fmt.Sprintf("Branch: %s", branch),
	}
	if session != nil && session.Summary != "" {
		systemLines = append(systemLines, "Project context: "+session.Summary)
	}
	systemLines = append(systemLines,
		"",
		"Key principles:",
		"- Always validate changes by running the code, not just checking syntax",
		"- Use shell to run tests, builds, and observe real output",
		"- Use get_system_info to check resource pressure before intensive operations",
		"- Use list_open_tabs to understand what the user is focused on",
		"- When you open a file, the user sees it in Monaco immediately",
		"- Be decisive: fix problems completely, not just symptoms",
	)
	systemPrompt := strings.Join(systemLines, "\n")

	var allToolCalls []ToolCall
	var finalText strings.Builder

	// Agentic loop
	for {
		// Window to last 50 messages
		windowed := messages
		if len(windowed) > 50 {
			windowed = windowed[len(windowed)-50:]
		}

		req := anthropicRequest{
			Model:     model,
			MaxTokens: 8192,
			System:    systemPrompt,
			Messages:  windowed,
			Tools:     tools,
			Stream:    true,
		}

		responseBlocks, stopReason, err := streamRequest(apiKey, req, ctx, &finalText)
		if err != nil {
			ctx.OnError(err.Error())
			return
		}

		messages = append(messages, anthropicMessage{Role: "assistant", Content: responseBlocks})

		if stopReason != "tool_use" {
			break
		}

		// Execute all tool calls from this turn
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

			allToolCalls = append(allToolCalls, ToolCall{
				ID: block.ID, Name: block.Name, Input: inputMap,
				Result: result, IsError: isError,
			})
			toolResults = append(toolResults, contentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result,
			})
		}

		// Feed results back for next iteration
		messages = append(messages, anthropicMessage{Role: "user", Content: toolResults})
	}

	// Persist final assistant message
	var tc interface{}
	if len(allToolCalls) > 0 {
		tc = allToolCalls
	}
	db.SaveMessage(newID(), ctx.SessionID, "assistant", finalText.String(), tc) //nolint:errcheck
	ctx.OnChunk("", true)
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
