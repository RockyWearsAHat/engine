package ai

// quality:allow-long-file quality:allow-long-function quality:allow-large-block

import (
	"bufio"
	"bytes"
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/db"
	gofs "github.com/engine/server/fs"
	gogit "github.com/engine/server/git"
	gh "github.com/engine/server/github"
	"github.com/engine/server/quality"
	"github.com/engine/server/remote"
	"github.com/engine/server/runtimecfg"
	"github.com/engine/server/workspace"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	goprocess "github.com/shirou/gopsutil/v3/process"
)

const (
	defaultAnthropicModel = "claude-opus-4-8"
	defaultOpenAIModel    = "gpt-4o"
	defaultOllamaModel    = "llama3.2"
	defaultLlamacppModel  = "default"
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
var qualityScanFn = quality.ScanProject

var (
	machineCredStoreOnce sync.Once
	machineCredStore     *remote.KeychainStore
)

func getMachineCredStore() *remote.KeychainStore {
	machineCredStoreOnce.Do(func() { machineCredStore = remote.NewKeychainStore() })
	return machineCredStore
}

// credStoreGetFn retrieves a credential from the machine keychain.
var credStoreGetFn = func(key string) (string, error) { return getMachineCredStore().Get(key) }

// credStoreSetFn stores a credential in the machine keychain.
var credStoreSetFn = func(key, value string) error { return getMachineCredStore().Set(key, value) }

// credStoreDelFn deletes a credential from the machine keychain.
var credStoreDelFn = func(key string) error { return getMachineCredStore().Delete(key) }

// ollamaWarmKeepInterval is the interval between warm-keep pings to Ollama.
// Must be less than OLLAMA_KEEP_ALIVE (default 30m) so the model never expires.
// Exported as a var so tests can override it to a short duration.
var ollamaWarmKeepInterval = 20 * time.Minute

// interactiveShellTimeout is the max time for interactive shell operations.
// Exported as a var so tests can force deterministic timeout coverage.
var interactiveShellTimeout = 2 * time.Minute

// TestHelpers: functions below are exported helpers for test suites to reduce duplication.
// They stage side-effect chains (env setup, server creation, callbacks) in named functions.

// stageTestCallbacks creates standard no-op callbacks for test ChatContexts.
func stageTestCallbacks() (func(_ *db.Session), func(string, bool), func(string, any), func(string, any, bool), func(string)) {
	return func(_ *db.Session) {}, func(string, bool) {}, func(string, any) {}, func(string, any, bool) {}, func(string) {}
}

// newChatContextForOllamaTest creates a ChatContext pre-configured for Ollama integration tests
// with standard no-op callbacks. Caller provides SessionID, ProjectPath, and Role.
// Use this to avoid repeating the callback setup 5+ times per test file.
func newChatContextForOllamaTest(sessionID, projectDir string, role AgentRole) *ChatContext {
	onSessionUpdated, onChunk, onToolCall, onToolResult, onError := stageTestCallbacks()
	return &ChatContext{
		ProjectPath:      projectDir,
		SessionID:        sessionID,
		Role:             role,
		OnSessionUpdated: onSessionUpdated,
		OnChunk:          onChunk,
		OnToolCall:       onToolCall,
		OnToolResult:     onToolResult,
		OnError:          onError,
	}
}

// stageTestOrchestratorCallbacks creates standard no-op callbacks for test OrchestratorConfigs.
func stageTestOrchestratorCallbacks() (func(string, string), func(string), func(string)) {
	return func(string, string) {}, func(string) {}, func(string) {}
}

// newOrchestratorConfigForTest creates a standard OrchestratorConfig for orchestrator tests.
// All callbacks are set to no-ops. Caller provides ProjectPath and optional Owner/Repo for GitHub context.
func newOrchestratorConfigForTest(projectPath, owner, repo, sessionIDPrefix string) OrchestratorConfig {
	onPhase, onProgress, onError := stageTestOrchestratorCallbacks()
	return OrchestratorConfig{
		ProjectPath:     projectPath,
		Owner:           owner,
		Repo:            repo,
		OnPhase:         onPhase,
		OnProgress:      onProgress,
		OnError:         onError,
		SessionIDPrefix: sessionIDPrefix,
	}
}

// autonomousShellTimeout is the max time for autonomous shell operations.
// Exported as a var so tests can force deterministic timeout coverage.
var autonomousShellTimeout = 5 * time.Minute

// autonomousBuilderMaxTurns bounds headless builder loops so a local model that
// keeps narrating instead of calling signal_done reports a real failure instead
// of silently stopping or running forever.
var autonomousBuilderMaxTurns = 32

// scored represents a tool matched by search, with a keyword match score.
type scored struct {
	tool  anthropicTool
	score int
}

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

// parseIntEnv reads an int from env var `key`, falling back to `def` on missing
// or non-numeric values. Used for runtime tuning knobs like ENGINE_OLLAMA_NUM_CTX.
// Returns def if the env var is absent, empty, non-numeric, or ≤ 0.
func parseIntRaw(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseIntEnv(key string, def int) int {
	return parseIntRaw(os.Getenv(key), def)
}

func runtimeValue(primary, fallback string) string {
	value := strings.TrimSpace(primary)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

// runtimeContextMaxTokens returns the max token budget for context from env or default.
// Validates minimum of 16000 tokens; returns DefaultTokenBudget if lower.
func runtimeContextMaxTokens(projectPath string) int {
	b := 0
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		b = parseIntRaw(cfg.ContextMaxTokens, 0)
	}
	if b <= 0 {
		b = parseIntEnv("ENGINE_CONTEXT_MAX_TOKENS", DefaultTokenBudget)
	}
	if b < 16000 {
		return DefaultTokenBudget
	}
	return b
}

// runtimeContextRecentWindow returns the max number of recent messages to keep.
// Defaults to 60, clamped to [12, 1000].
func runtimeContextRecentWindow(projectPath string) int {
	w := 0
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		w = parseIntRaw(cfg.ContextRecentWindow, 0)
	}
	if w <= 0 {
		w = parseIntEnv("ENGINE_CONTEXT_RECENT_WINDOW", 60)
	}
	if w < 12 {
		return 12
	}
	if w > 1000 {
		return 1000
	}
	return w
}

// inferredProviderForModel infers the provider name from a model string.
// Returns "openai" for gpt-*/o1-/o3-/o4- prefixes, "anthropic" for claude,
// "ollama" for ollama-style names, and "llamacpp" as fallback.
func inferredProviderForModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return "llamacpp"
	}
	if strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1-") ||
		strings.HasPrefix(lower, "o3-") || strings.HasPrefix(lower, "o4-") {
		return "openai"
	}
	// Must precede the generic "claude" prefix check: the claude-code sentinel
	// routes to the CLI provider, not the raw Anthropic API.
	if lower == "claude-code" || lower == "claudecode" || lower == "claude_code" {
		return "claudecode"
	}
	if strings.HasPrefix(lower, "claude") {
		return "anthropic"
	}
	if looksLikeOllamaModel(lower) {
		return "ollama"
	}
	return "llamacpp"
}

// looksLikeOllamaModel checks if the model name matches Ollama conventions.
// Returns true if the name contains ':' or starts with a known Ollama prefix.
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

// resolveProvider returns the normalized provider name, using explicit override or inference.
// Accepts "anthropic", "openai", "ollama", "llamacpp", "llama.cpp", "llama-cpp",
// "auto", or "" (empty defaults to inference). Unrecognized values fall back to inference.
func resolveProvider(explicitProvider string, model string) string {
	switch strings.ToLower(strings.TrimSpace(explicitProvider)) {
	case "anthropic", "openai", "ollama", "llamacpp", "claudecode":
		return strings.ToLower(strings.TrimSpace(explicitProvider))
	case "llama.cpp", "llama-cpp":
		return "llamacpp"
	case "claude-code", "claude_code":
		return "claudecode"
	case "", "auto":
		return inferredProviderForModel(model)
	default:
		return inferredProviderForModel(model)
	}
}

// defaultModelForProvider returns the default model name for a given provider.
// Returns the canonical model string for the provider (e.g. "claude-opus-4-8" for anthropic).
func defaultModelForProvider(provider string) string {
	switch provider {
	case "openai":
		return defaultOpenAIModel
	case "ollama":
		return defaultOllamaModel
	case "llamacpp":
		return defaultLlamacppModel
	case "claudecode":
		// The CLI uses whatever model `claude` is configured for (opus-4-8 by
		// default); we still hand it a concrete alias so --model is explicit.
		return defaultAnthropicModel
	default:
		return defaultAnthropicModel
	}
}

// ollamaChatCompletionsURL returns the normalized OpenAI-compatible chat completions URL for Ollama.
func ollamaChatCompletionsURL(baseURL string) string {
	normalized := ollamaRootURL(baseURL)
	if strings.HasSuffix(normalized, "/chat/completions") {
		return normalized
	}
	return normalized + "/v1/chat/completions"
}

// ollamaRootURL normalizes an Ollama base URL by removing trailing slashes and path suffixes.
// Returns the default Ollama URL if baseURL is empty.
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

// detectOllamaModel queries the Ollama server to find the first loaded model.
// Returns empty string if no model is loaded or the server is unreachable.
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

// firstOllamaModel extracts the first model name from an Ollama API response.
// Queries url and extracts a model name from listKey (array) / nameKey (field).
// Returns empty string on error or if no model is found.
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

// ChatContext carries all state and callbacks for a single chat session.
// It holds project paths, session IDs, streaming callbacks, tool state, and agent metadata.
// Zero-valued ChatContext.Cancel is treated as non-cancellable.
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
	// ProviderOverride overrides the provider for this Chat (e.g., "anthropic", "openai").
	// Empty string uses auto-detection from model name.
	ProviderOverride string
	// ModelOverride overrides the model name for this Chat (e.g., "claude-opus-4-5").
	// Empty string uses the env-configured default.
	ModelOverride string
	// MarkVital is set by the agentic loop before iterating. Calling MarkVital(n) marks the
	// last n messages in the active history as vital checkpoints so they survive context windowing.
	// Nil outside of an active loop.
	MarkVital func(n int)
	// ReloadContext requests replay from an earlier user checkpoint during retry flows.
	ReloadContext bool
	// RetryFromMessageID anchors replay to the user message that precedes this message ID.
	RetryFromMessageID string
	// AutonomousPolicy, when non-nil, controls headless session behaviour:
	// auto-approving commits and/or pushes without prompting the user.
	AutonomousPolicy *AutonomousPolicy
	// ProjectTools holds project-defined tools discovered from <projectRoot>/.engine/tools/.
	// Loaded once per Chat() call; nil when the directory is absent or empty.
	ProjectTools []projectToolDef
	// DiscordDM sends a direct message to the project owner via Discord. Nil when Discord is not configured.
	DiscordDM func(message string) error
	// DiscordProgress posts a progress/milestone/completion update to the project's Discord channel.
	// Nil when Discord is not configured or the project has no channel.
	DiscordProgress func(message string) error
	// MaxTurns, when > 0, limits the number of provider loop iterations for this
	// Chat invocation so the caller can orchestrate work step-by-step from Go.
	MaxTurns int
	// AgentName identifies this ChatContext inside the project agent communication pool.
	AgentName string
	// AgentComms is the project-scoped peer message hub used by lead/worker agents.
	AgentComms *AgentCommsHub
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

// OutputCollector holds a thread-safe string builder for accumulating chat output.
type OutputCollector struct {
	mu  sync.Mutex
	buf strings.Builder
}

// Write appends content to the output buffer in a thread-safe manner.
func (oc *OutputCollector) Write(content string) {
	if content == "" {
		return
	}
	oc.mu.Lock()
	oc.buf.WriteString(content)
	oc.mu.Unlock()
}

// String returns the accumulated output.
func (oc *OutputCollector) String() string {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	return oc.buf.String()
}

// stageCollectorCreation creates a new OutputCollector for accumulating output.
func stageCollectorCreation() *OutputCollector {
	return &OutputCollector{}
}

// stageCallbacksInit prepares output-collecting callbacks from an OutputCollector.
// onError forwards to cfg.OnError (role-prefixed) instead of swallowing errors
// silently — without this, a real provider/config failure inside a role-based
// phase call (planner, reviewer, ...) never reaches the client; only a generic
// downstream "output empty or unparsable" message did.
func stageCallbacksInit(oc *OutputCollector, cfg OrchestratorConfig, role AgentRole) (func(string, bool), func(string), func(string, any), func(string, any, bool)) {
	onChunk := func(content string, _ bool) {
		oc.Write(content)
	}
	onError := func(msg string) {
		if cfg.OnError != nil && strings.TrimSpace(msg) != "" {
			cfg.OnError(fmt.Sprintf("%s: %s", agentRoleLabel(role), msg))
		}
	}
	onToolCall := func(string, any) {}
	onToolResult := func(string, any, bool) {}
	return onChunk, onError, onToolCall, onToolResult
}

// stageOutputCollectorWithCallbacks encapsulates the side-effect chain of creating
// an OutputCollector and initializing output-collecting callbacks. Both stages are
// explicitly combined to clarify they are a dependent pair.
func stageOutputCollectorWithCallbacks(cfg OrchestratorConfig, role AgentRole) (*OutputCollector, func(string, bool), func(string), func(string, any), func(string, any, bool)) {
	oc := stageCollectorCreation()
	onChunk, onError, onToolCall, onToolResult := stageCallbacksInit(oc, cfg, role)
	return oc, onChunk, onError, onToolCall, onToolResult
}

// stageChatContextCreation constructs a ChatContext from config, sessionID, role, cancel, and callbacks.
func stageChatContextCreation(cfg OrchestratorConfig, sessionID string, role AgentRole, cancel <-chan struct{}, onChunk func(string, bool), onError func(string), onToolCall func(string, any), onToolResult func(string, any, bool)) *ChatContext {
	return &ChatContext{
		ProjectPath:  cfg.ProjectPath,
		SessionID:    sessionID,
		Role:         role,
		Cancel:       cancel,
		OnChunk:      onChunk,
		OnError:      onError,
		OnToolCall:   onToolCall,
		OnToolResult: onToolResult,
	}
}

// newChatContextForRole creates a ChatContext with output collection pre-configured.
// This consolidates the repeated pattern of building a context with sync.Mutex + strings.Builder
// for collecting streamed output from a role-based chat session.
func newChatContextForRole(cfg OrchestratorConfig, sessionID string, role AgentRole, cancel <-chan struct{}) (*ChatContext, *OutputCollector) {
	oc, onChunk, onError, onToolCall, onToolResult := stageOutputCollectorWithCallbacks(cfg, role)
	ctx := stageChatContextCreation(cfg, sessionID, role, cancel, onChunk, onError, onToolCall, onToolResult)
	return ctx, oc
}

// shouldExitTurnLoop checks whether the agentic loop should exit based on turn limits.
// Returns true if max turns exceeded (with error logging for autonomous builder role),
// false if the loop should continue.
func (ctx *ChatContext) shouldExitTurnLoop(turns int, isAutonomousBuilder bool) bool {
	turnLimit := ctx.MaxTurns
	if turnLimit <= 0 && isAutonomousBuilder {
		turnLimit = autonomousBuilderMaxTurns
	}
	if turnLimit <= 0 || turns <= turnLimit {
		return false
	}
	if isAutonomousBuilder {
		ctx.OnError(fmt.Sprintf("autonomous builder stopped after %d turns without signal_done", turnLimit))
	}
	return true
}

// TabInfo represents an open editor tab pushed by the client.
type TabInfo struct {
	// Path is the absolute file path of the tab.
	Path string `json:"path"`
	// IsActive indicates whether this tab is the currently focused tab.
	IsActive bool `json:"isActive"`
	// IsDirty indicates whether the tab has unsaved changes.
	IsDirty bool `json:"isDirty"`
}

// ToolCall records a single tool invocation, stored in the message history.
type ToolCall struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Input   any    `json:"input"`
	Result  any    `json:"result,omitempty"`
	IsError bool   `json:"isError,omitempty"`
}

// --- Anthropic API types (raw HTTP, no official Go SDK) ---

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
	// CacheControl marks a prompt-cache breakpoint ending at this tool.
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// cacheControl is an Anthropic prompt-cache breakpoint. A breakpoint caches the
// entire rendered prefix up to and including the block it is attached to.
// Render order is tools -> system -> messages, so a breakpoint on the last system
// block covers the tool definitions as well.
type cacheControl struct {
	// Type is always "ephemeral".
	Type string `json:"type"`
	// TTL is the cache lifetime; empty means the default 5 minutes.
	TTL string `json:"ttl,omitempty"`
}

// ephemeralCache returns a default (5-minute) cache breakpoint.
func ephemeralCache() *cacheControl { return &cacheControl{Type: "ephemeral"} }

// systemBlock is one block of the system prompt. The Anthropic API accepts the
// system prompt either as a bare string or as an array of text blocks; only the
// array form can carry a cache_control breakpoint, so Engine always sends the array.
type systemBlock struct {
	// Type is always "text".
	Type string `json:"type"`
	// Text is the system prompt content.
	Text string `json:"text"`
	// CacheControl marks a prompt-cache breakpoint ending at this block.
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// anthropicMessage represents a message in the Anthropic API format.
type anthropicMessage struct {
	// Role is "user" or "assistant".
	Role string `json:"role"`
	// Content is the message body: either a string or []contentBlock.
	Content any `json:"content"`
	// Vital marks this message as a key checkpoint. Vital messages are always kept during
	// context windowing; only non-vital messages are pruned when context grows too large.
	// Never serialised to the API.
	Vital bool `json:"-"`
}

// contentBlock represents a single content unit in an Anthropic message.
type contentBlock struct {
	// Type is "text", "tool_use", or "tool_result".
	Type string `json:"type"`
	// Text is the text content (for type="text").
	Text string `json:"text,omitempty"`
	// ID is the content block ID (for type="tool_result").
	ID string `json:"id,omitempty"`
	// Name is the tool name (for type="tool_use").
	Name string `json:"name,omitempty"`
	// Input is the tool arguments (for type="tool_use").
	Input any `json:"input,omitempty"`
	// ToolUseID references the original tool_use block ID (for type="tool_result").
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Content is the tool result text (for type="tool_result").
	Content string `json:"content,omitempty"`
	// CacheControl marks a prompt-cache breakpoint ending at this block.
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// anthropicRequest is the HTTP request body for the Anthropic API.
// anthropicRequest is the HTTP request body for the Anthropic API.
type anthropicRequest struct {
	// Model is the model identifier (e.g., "claude-opus-4-5").
	Model string `json:"model"`
	// MaxTokens is the max tokens in the response.
	MaxTokens int `json:"max_tokens"`
	// System is the system prompt, sent as text blocks so it can carry a
	// prompt-cache breakpoint.
	System []systemBlock `json:"system,omitempty"`
	// Messages is the conversation history.
	Messages []anthropicMessage `json:"messages"`
	// Tools is the tool registry available for this request.
	Tools []anthropicTool `json:"tools"`
	// Stream indicates streaming mode.
	Stream bool `json:"stream"`
}

// strProp builds a {"type":"string","description":"..."} schema fragment.
func strProp(desc string) any {
	return map[string]any{"type": "string", "description": desc}
}

// numProp builds a {"type":"number","description":"..."} schema fragment.
func numProp(desc string) any {
	return map[string]any{"type": "number", "description": desc}
}

// arrProp builds an array-of-strings schema fragment for tool inputs.
func arrProp(desc string) any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": desc,
	}
}

// objSchema builds a JSON Schema object with required fields and properties.
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
			"path":             strProp("Absolute directory path"),
			"processId":        numProp("Optional process ID to scan and mark generated files in cache before listing"),
			"includeGenerated": map[string]any{"type": "boolean", "description": "When true, expand generated files instead of collapsing them"},
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
		Description: "Execute a shell command and return stdout + stderr. Defaults to project root when cwd is omitted.",
		InputSchema: objSchema([]string{"command"}, map[string]any{
			"command": strProp("Shell command to run"),
			"cwd":     strProp("Working directory (optional). When omitted, command runs from project root."),
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
	{
		Name:        "quality_report",
		Description: "Run a deterministic codebase quality index scan (no AI reasoning) to find dead-code candidates, duplicate logic chunks, large uncommented blocks, documentation drift, and CS 2420/3500 contention signals.",
		InputSchema: objSchema(nil, map[string]any{
			"projectPath": strProp("Optional project-relative directory to scan. Defaults to current project root."),
			"maxIssues":   numProp("Optional maximum findings to return (default 120)."),
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
	// ── Autonomous session control ───────────────────────────────────────────
	{
		Name:        "signal_done",
		Description: "Signal that the autonomous build session is complete. Call this ONLY after committing all code with git_commit. Passing a summary is optional.",
		InputSchema: objSchema(nil, map[string]any{
			"summary": strProp("Brief summary of what was built and committed"),
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
	// ── Agent team communication ───────────────────────────────────────────────
	{
		Name:        "agent_list",
		Description: "List the live agents in this project's communication pool. Use before delegating or asking a peer for focused context.",
		InputSchema: objSchema(nil, map[string]any{}),
	},
	{
		Name:        "agent_send",
		Description: "Send a focused message or delegation prompt to another project agent. Returns a message id that can be awaited later.",
		InputSchema: objSchema([]string{"to", "body"}, map[string]any{
			"to":       strProp("Recipient agent id from agent_list"),
			"subject":  strProp("Short topic or requested decision"),
			"body":     strProp("Focused message, prompt, question, or handoff packet"),
			"reply_to": strProp("Optional message id this responds to"),
		}),
	},
	{
		Name:        "agent_inbox",
		Description: "Read messages addressed to this agent. Set consume=true after incorporating the messages into the current work.",
		InputSchema: objSchema(nil, map[string]any{
			"consume": map[string]any{"type": "boolean", "description": "Clear returned messages from the inbox"},
		}),
	},
	{
		Name:        "agent_receive",
		Description: "Receive one matching peer message when convenient. Non-blocking by default, with optional inline response and optional completion report when no message exists.",
		InputSchema: objSchema(nil, map[string]any{
			"message_id":        strProp("Optional exact message id to match"),
			"reply_to":          strProp("Optional original message id whose reply should be matched"),
			"timeout_ms":        numProp("Milliseconds to wait; 0 checks once without blocking"),
			"consume":           map[string]any{"type": "boolean", "description": "Whether to remove the matched message from inbox (default true)"},
			"response":          strProp("Optional inline response body to send when a message is received"),
			"response_to":       strProp("Optional recipient for inline response (defaults to message sender)"),
			"response_subject":  strProp("Optional subject for inline response"),
			"response_reply_to": strProp("Optional reply_to id for inline response (defaults to matched message id)"),
			"complete":          map[string]any{"type": "boolean", "description": "When true and no message matched, send completion report"},
			"complete_to":       strProp("Completion-report recipient when complete=true"),
			"complete_subject":  strProp("Optional completion-report subject"),
			"complete_body":     strProp("Optional completion-report body"),
			"complete_reply_to": strProp("Optional original message id to mark completion as a reply to"),
		}),
	},
	{
		Name:        "agent_await",
		Description: "Wait for a matching peer message addressed to this agent. Use reply_to to await a response to a sent message; timeout_ms=0 checks once without blocking.",
		InputSchema: objSchema(nil, map[string]any{
			"message_id": strProp("Optional exact message id to wait for"),
			"reply_to":   strProp("Optional original message id whose reply should be returned"),
			"timeout_ms": numProp("Milliseconds to wait; 0 checks once without blocking"),
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
	{
		Name:        "discord_post_progress",
		Description: "Post a progress update, milestone completion, or work summary to the project's Discord channel. Use for: task completion, milestone reached, autonomous session summary, or any update the user should see asynchronously. Keep messages concise — one to three sentences.",
		InputSchema: objSchema([]string{"message"}, map[string]any{
			"message": strProp("Progress update message to post to the project Discord channel"),
		}),
	},
	// ── LAN compute mesh ──────────────────────────────────────────────────────────
	{
		Name:        "mesh_exec",
		Description: "Run a shell command on a paired mesh peer (Mac↔PC) to dispatch heavy work like test suites or local-model inference. Returns the captured stdout/stderr/exitCode. The peer must be configured in ~/.engine/mesh.json.",
		InputSchema: objSchema([]string{"command"}, map[string]any{
			"peer":      strProp("Peer name (case-insensitive) or role (e.g. 'inference', 'tests'). Empty = first peer with matching role."),
			"role":      strProp("Optional role to dispatch by when peer name is empty."),
			"cwd":       strProp("Working directory on the peer."),
			"command":   strProp("Executable to run on the peer."),
			"args":      arrProp("Command arguments (array of strings)."),
			"env":       arrProp("Extra environment variables (each entry of the form KEY=VALUE)."),
			"timeoutMs": numProp("Optional timeout in milliseconds. 0 = peer-default."),
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

// summarizeToolsAndMergeIntoActive merges newly found tools into ctx.ActiveTools (dedup by name)
// and returns a human-readable summary of what was found and added.
func summarizeToolsAndMergeIntoActive(matches []scored, ctx *ChatContext) string {
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

	return summarizeToolsAndMergeIntoActive(matches, ctx)
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
	case "agent_list":
		return executeAgentListTool(ctx)
	case "agent_send":
		return executeAgentSendTool(input, ctx)
	case "agent_inbox":
		return executeAgentInboxTool(input, ctx)
	case "agent_receive":
		return executeAgentReceiveTool(input, ctx)
	case "agent_await":
		return executeAgentAwaitTool(input, ctx)

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
		processID := int(numVal("processId"))
		includeGenerated := boolVal("includeGenerated")
		result, listErr := listDirectoryWithCompaction(ctx.ProjectPath, path, processID, includeGenerated)
		if listErr != nil {
			return listErr.Error(), true
		}
		return result, false

	case "shell":
		command := str("command")
		if intentErr := ValidatePublishIntentForAction(ctx.ProjectPath, command); intentErr != nil {
			return intentErr.Error(), true
		}
		autonomousAwareness := ""
		if ctx.AutonomousPolicy != nil {
			autonomousAwareness = autonomousShellAwareness(ctx.ProjectPath, command)
		} else if title, message, needsApproval := requiresShellApproval(ctx.ProjectPath, command); needsApproval {
			if ctx.RequestApproval == nil {
				// No interactive approval surface — proceed autonomously and log the action.
				fmt.Printf("[engine-autonomous] proceeding without approval gate: %s | %s\n", title, command)
			} else {
				allowed, err := ctx.RequestApproval("shell", title, message, command)
				if err != nil {
					return err.Error(), true
				}
				if !allowed {
					return "The user denied this shell command.", true
				}
			}
		}
		cwd := str("cwd")
		if cwd == "" {
			cwd = ctx.ProjectPath
		}
		var err error
		if ctx.AutonomousPolicy != nil {
			var cwdAwareness string
			cwd, cwdAwareness, _ = resolveAutonomousShellDirectory(ctx.ProjectPath, cwd)
			if cwdAwareness != "" {
				if autonomousAwareness != "" {
					autonomousAwareness += "\n" + cwdAwareness
				} else {
					autonomousAwareness = cwdAwareness
				}
			}
		} else {
			cwd, err = resolveWorkspaceDirectory(ctx.ProjectPath, cwd)
			if err != nil {
				return err.Error(), true
			}
		}
		// Use the user's login shell so the AI has the full PATH (Homebrew, nvm,
		// cargo, etc.). The -l flag sources login scripts (.zprofile, .bash_profile).
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		shellTimeout := interactiveShellTimeout
		if ctx.AutonomousPolicy != nil {
			shellTimeout = autonomousShellTimeout
		}
		shellCtx, shellCancel := stdctx.WithTimeout(stdctx.Background(), shellTimeout)
		defer shellCancel()
		cmd := exec.CommandContext(shellCtx, shell, "-l", "-c", command)
		cmd.Dir = cwd
		// Autonomous sessions always work inside an isolated project directory.
		// Setting GOWORK=off prevents Go from walking up to a parent go.work file
		// and rejecting module commands for unlisted projects. We apply this
		// unconditionally for all autonomous sessions regardless of path, because
		// any parent-workspace leakage is always wrong in a sandboxed build.
		if ctx.AutonomousPolicy != nil ||
			strings.Contains(cwd, ".engine/projects/") ||
			strings.Contains(ctx.ProjectPath, ".engine/projects/") ||
			strings.Contains(cwd, "/engine/projects/") ||
			strings.Contains(ctx.ProjectPath, "/engine/projects/") {
			env := os.Environ()
			env = append(env, "GOWORK=off")
			cmd.Env = env
		}
		out, err := cmd.CombinedOutput()
		if shellCtx.Err() == stdctx.DeadlineExceeded {
			err = fmt.Errorf("command timed out after %v", shellTimeout)
		}
		result := strings.TrimSpace(string(out))
		if result == "" {
			result = "(no output)"
		}
		if len(result) > 4*1024*1024 {
			result = result[:4*1024*1024] + "\n...(truncated)"
		}
		if autonomousAwareness != "" {
			result = autonomousAwareness + "\n\n" + result
		}
		return result, err != nil

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

	case "quality_report":
		target := ctx.ProjectPath
		if p := strings.TrimSpace(str("projectPath")); p != "" {
			resolved, err := resolveWorkspaceDirectory(ctx.ProjectPath, p)
			if err != nil {
				return err.Error(), true
			}
			target = resolved
		}
		report, err := qualityScanFn(target, int(numVal("maxIssues")))
		if err != nil {
			return err.Error(), true
		}
		b, err := json.Marshal(report)
		if err != nil {
			return err.Error(), true
		}
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
			// No interactive surface — commit automatically.
			fmt.Printf("[engine-autonomous] auto-committing without approval gate: %s\n", str("message"))
			hash, commitErr := gogit.Commit(ctx.ProjectPath, str("message"))
			if commitErr != nil {
				return commitErr.Error(), true
			}
			return "Committed: " + hash, false
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

	case "signal_done":
		summary := strings.TrimSpace(str("summary"))
		if summary == "" {
			summary = "done"
		}
		return summary, false

	case "mesh_exec":
		return executeMeshExec(input, ctx)

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
			// No interactive surface — push automatically.
			fmt.Printf("[engine-autonomous] auto-pushing to %s without approval gate\n", remote)
			out, pushErr := gogit.Push(ctx.ProjectPath, remote)
			if pushErr != nil {
				return pushErr.Error(), true
			}
			return out, false
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
		if ctx.RequestApproval == nil {
			// No interactive surface — kill autonomously.
			fmt.Printf("[engine-autonomous] auto-killing PID %d (%s) signal=%s\n", pid, procName, signal)
		} else {
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

	case "discord_post_progress":
		message := str("message")
		if message == "" {
			return "discord_post_progress: message is required", true
		}
		if ctx.DiscordProgress == nil {
			return "discord_post_progress: Discord not configured", true
		}
		if err := ctx.DiscordProgress(message); err != nil {
			return err.Error(), true
		}
		return "Progress posted", false

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

type generatedCache struct {
	UpdatedAt string   `json:"updatedAt"`
	ProcessID int      `json:"processId,omitempty"`
	Paths     []string `json:"paths"`
}

type generatedIndex struct {
	paths map[string]bool
}

type dirRenderState struct {
	maxChars         int
	truncated        bool
	includeGenerated bool
	hints            map[string]bool
	generated        *generatedIndex
	projectRoot      string
}

func listDirectoryWithCompaction(projectRoot, dir string, processID int, includeGenerated bool) (string, error) {
	idx := loadGeneratedIndex(projectRoot)
	warning := ""
	if processID > 0 {
		if err := updateGeneratedIndexFromPID(projectRoot, processID, idx); err != nil {
			warning = fmt.Sprintf("Warning: process scan failed for PID %d (%v)", processID, err)
		}
	}

	hints := generatedHintsFromTasks(projectRoot)
	if !includeGenerated && isGeneratedPath(dir, hints, idx) {
		// If the caller drills into a generated directory, expand it automatically.
		includeGenerated = true
	}

	maxChars := parseIntEnv("ENGINE_LIST_DIRECTORY_MAX_CHARS", 16000)
	ctxMax := parseIntEnv("ENGINE_CONTEXT_MAX_TOKENS", DefaultTokenBudget)
	autoCap := ctxMax / 4
	if autoCap > 0 && autoCap < maxChars {
		maxChars = autoCap
	}
	if maxChars < 2000 {
		maxChars = 2000
	}

	var sb strings.Builder
	if warning != "" {
		sb.WriteString(warning)
		sb.WriteString("\n\n")
	}

	name := filepath.Base(dir)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = dir
	}
	sb.WriteString("📁 ")
	sb.WriteString(name)
	sb.WriteString("\n")

	state := &dirRenderState{maxChars: maxChars, includeGenerated: includeGenerated, hints: hints, generated: idx, projectRoot: filepath.Clean(projectRoot)}
	if err := appendDirectoryLines(&sb, dir, 1, 4, state); err != nil {
		return "", err
	}
	if state.truncated {
		sb.WriteString("\n... (truncated to protect context budget; narrow path, set includeGenerated=true, or use read_file/search_files for deeper inspection)")
	}

	return strings.TrimSpace(sb.String()), nil
}

func appendDirectoryLines(sb *strings.Builder, dir string, depth, maxDepth int, state *dirRenderState) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	prefix := strings.Repeat("  ", depth)
	collapsedGenerated := 0
	for _, entry := range entries {
		if state.truncated {
			break
		}
		if entry.Name() == ".engine" && shouldHideEngineDirectory(dir, state) {
			continue
		}
		childPath := filepath.Join(dir, entry.Name())
		generated := !state.includeGenerated && isGeneratedPath(childPath, state.hints, state.generated)
		if generated {
			collapsedGenerated++
			continue
		}

		icon := "📄"
		if entry.IsDir() {
			icon = "📁"
		}
		line := fmt.Sprintf("%s%s %s\n", prefix, icon, entry.Name())
		if sb.Len()+len(line) > state.maxChars {
			state.truncated = true
			break
		}
		sb.WriteString(line)

		if entry.IsDir() && depth < maxDepth {
			if err := appendDirectoryLines(sb, childPath, depth+1, maxDepth, state); err != nil {
				return err
			}
		}
	}

	if collapsedGenerated > 0 && !state.truncated {
		line := fmt.Sprintf("%s📦 (collapsed %d generated entries; set includeGenerated=true to expand)\n", prefix, collapsedGenerated)
		if sb.Len()+len(line) > state.maxChars {
			state.truncated = true
		} else {
			sb.WriteString(line)
		}
	}

	return nil
}

func shouldHideEngineDirectory(dir string, state *dirRenderState) bool {
	if state == nil || state.projectRoot == "" {
		return false
	}
	return filepath.Clean(dir) == state.projectRoot
}

func generatedHintsFromTasks(projectRoot string) map[string]bool {
	hints := map[string]bool{
		"node_modules":     true,
		"dist":             true,
		"build":            true,
		"target":           true,
		"coverage":         true,
		".cache":           true,
		"out":              true,
		"tmp":              true,
		"bin":              true,
		"vendor":           true,
		"storybook-static": true,
	}

	detected := workspace.DetectTasks(projectRoot)
	for _, task := range detected.Tasks {
		lower := strings.ToLower(strings.TrimSpace(task.Command + " " + task.ID + " " + task.Label + " " + task.Description))
		for key := range inferGeneratedHintsFromTask(lower) {
			hints[key] = true
		}
	}
	return hints
}

func inferGeneratedHintsFromTask(lower string) map[string]bool {
	hints := map[string]bool{}
	if strings.Contains(lower, "cover") {
		hints["coverage"] = true
		hints[".cache"] = true
		hints["llvm-cov-target"] = true
	}
	if strings.Contains(lower, "build") || strings.Contains(lower, "bundle") || strings.Contains(lower, "compile") {
		hints["dist"] = true
		hints["build"] = true
		hints["target"] = true
		hints["bin"] = true
	}
	if strings.Contains(lower, "test") {
		hints["coverage"] = true
		hints[".cache"] = true
	}
	if strings.Contains(lower, "storybook") {
		hints["storybook-static"] = true
	}
	if strings.Contains(lower, "temp") || strings.Contains(lower, "tmp") {
		hints["tmp"] = true
	}
	return hints
}

func isGeneratedPath(path string, hints map[string]bool, idx *generatedIndex) bool {
	clean := filepath.Clean(path)
	if idx != nil && idx.paths[clean] {
		return true
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for _, part := range parts {
		if hints[part] {
			return true
		}
	}
	lower := strings.ToLower(clean)
	return strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".profraw") || strings.HasSuffix(lower, ".profdata")
}

func generatedCachePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".engine", "generated-files-cache.json")
}

func loadGeneratedIndex(projectRoot string) *generatedIndex {
	idx := &generatedIndex{paths: map[string]bool{}}
	data, err := os.ReadFile(generatedCachePath(projectRoot))
	if err != nil {
		return idx
	}
	var cache generatedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return idx
	}
	for _, p := range cache.Paths {
		if p == "" {
			continue
		}
		idx.paths[filepath.Clean(p)] = true
	}
	return idx
}

func saveGeneratedIndex(projectRoot string, processID int, idx *generatedIndex) error {
	if idx == nil {
		return nil
	}
	paths := make([]string, 0, len(idx.paths))
	for p := range idx.paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cache := generatedCache{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		ProcessID: processID,
		Paths:     paths,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".engine"), 0755); err != nil {
		return err
	}
	return os.WriteFile(generatedCachePath(projectRoot), data, 0644)
}

func updateGeneratedIndexFromPID(projectRoot string, processID int, idx *generatedIndex) error {
	if idx == nil {
		return nil
	}
	if processID <= 0 {
		return fmt.Errorf("invalid processId")
	}
	proc, err := newProcessFn(int32(processID))
	if err != nil {
		return fmt.Errorf("resolve process %d: %w", processID, err)
	}
	files, err := proc.OpenFiles()
	if err != nil {
		return fmt.Errorf("read open files for pid %d: %w", processID, err)
	}
	updateGeneratedIndexFromFiles(projectRoot, files, idx)
	return saveGeneratedIndex(projectRoot, processID, idx)
}

// updateGeneratedIndexFromFiles filters and records paths from open files into the index.
// Only absolute paths within projectRoot (or its subdirectories) are recorded.
func updateGeneratedIndexFromFiles(projectRoot string, files []goprocess.OpenFilesStat, idx *generatedIndex) {
	root := filepath.Clean(projectRoot)
	rootPrefix := root + string(filepath.Separator)
	for _, f := range files {
		p := filepath.Clean(strings.TrimSpace(f.Path))
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			continue
		}
		if p != root && !strings.HasPrefix(p, rootPrefix) {
			continue
		}
		idx.paths[p] = true
		for parent := filepath.Dir(p); parent != root && parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			idx.paths[parent] = true
		}
	}
}

func resolveRetryUserAnchor(history []db.Message, messageID string) (int, db.Message, bool) {
	if len(history) == 0 {
		return -1, db.Message{}, false
	}
	anchor := -1
	if strings.TrimSpace(messageID) != "" {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].ID == messageID {
				anchor = i
				break
			}
		}
	}
	if anchor < 0 {
		anchor = len(history) - 1
	}
	for i := anchor; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(history[i].Role), "user") {
			return i, history[i], true
		}
	}
	return -1, db.Message{}, false
}

func historyFromRetryAnchor(history []db.Message, messageID string) []db.Message {
	idx, _, ok := resolveRetryUserAnchor(history, messageID)
	if !ok {
		return history
	}
	out := make([]db.Message, idx+1)
	copy(out, history[:idx+1])
	return out
}

// persistChatFinalState handles the post-conversation side effects:
// saving residuals, updating session summary, writing handoff cache, and snapshotting memory.
// Extracted for clarity and reduced side-effect depth.
func persistChatFinalState(ctx *ChatContext, assistantMessageID, userMsgID, userMessage string,
	finalText *strings.Builder, allToolCalls []ToolCall, session *db.Session,
	windowResult conversationWindowResult, selectiveContext selectiveContextResult,
	profile *ProjectProfile, openTabs []TabInfo, model string, lastLedgerEvent *db.MemoryLedgerEvent) {

	// Save attention residuals for context recovery on next turn.
	if err := db.SaveAttentionResiduals(BuildAttentionResidualRecords(
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
	)); err != nil {
		ctx.OnError(fmt.Sprintf("save attention residuals failed: %v", err))
	}

	// Update session summary and handoff cache if a new summary was generated.
	if summary := BuildUpdatedSessionSummary(func() string {
		if session == nil {
			return ""
		}
		return session.Summary
	}(), userMessage, finalText.String(), allToolCalls); summary != "" {

		if err := db.UpdateSessionSummary(ctx.SessionID, summary); err != nil {
			ctx.OnError(fmt.Sprintf("update session summary failed: %v", err))
		}
		if ctx.OnSessionUpdated != nil {
			if updatedSession, err := db.GetSession(ctx.SessionID); err == nil && updatedSession != nil {
				ctx.OnSessionUpdated(updatedSession)
			}
		}

		if err := WriteAutonomyHandoffCache(ctx.ProjectPath, func() *AutonomyHandoff {
			handoff := BuildAutonomyHandoff(assistantMessageID, ctx.SessionID, ctx.ProjectPath, userMessage, summary, profile)
			return &handoff
		}()); err != nil {
			ctx.OnError(fmt.Sprintf("write autonomy handoff cache failed: %v", err))
		}
	}

	// Snapshot deterministic memory context for scribe notes.
	if compiled := buildDeterministicMemoryContext(ctx.ProjectPath, ctx.SessionID, userMessage, openTabs, 2200); compiled != "" {
		seq := int64(0)
		if lastLedgerEvent != nil {
			seq = lastLedgerEvent.Sequence
		}
		persistScribeSnapshot(ctx.ProjectPath, ctx.SessionID, model, compiled, seq)
	}
}

// Chat runs the full agentic loop for a user message, streaming results via ctx callbacks.
func Chat(ctx *ChatContext, userMessage string) {
	registerChatAgent(ctx)

	runtimeSettings, _ := runtimecfg.Load(ctx.ProjectPath)
	explicitProvider := runtimeValue(runtimeSettings.ModelProvider, os.Getenv("ENGINE_MODEL_PROVIDER"))
	model := runtimeValue(runtimeSettings.Model, os.Getenv("ENGINE_MODEL"))
	activeTeam := runtimeValue(runtimeSettings.ActiveTeam, os.Getenv("ENGINE_ACTIVE_TEAM"))
	if _, teamProvider, teamModel, ok := ResolveTeamOrchestratorModel(ctx.ProjectPath, activeTeam); ok {
		if model == "" {
			model = teamModel
		}
		if explicitProvider == "" || strings.EqualFold(explicitProvider, "auto") {
			explicitProvider = strings.TrimSpace(teamProvider)
		}
	}

	if roleProvider, roleModel := roleModelOverrideFromEnv(ctx.ProjectPath, ctx.Role); roleModel != "" {
		model = roleModel
		if roleProvider != "" {
			explicitProvider = roleProvider
		}
	}
	// Hybrid cost-optimal router: when ENGINE_HYBRID=1, lead/hard roles run on
	// the subscription Opus (claudecode) and worker roles run cheap+parallel on
	// the Haiku API. Engages only when nothing more specific was pinned.
	if strings.TrimSpace(model) == "" && strings.TrimSpace(explicitProvider) == "" {
		if hp, hm := ResolveHybridRouting(ctx.ProjectPath, ctx.Role); hm != "" {
			model = hm
			explicitProvider = hp
		}
	}
	// Local-first cost router: when ENGINE_LOCAL_FIRST=1 and the role is on
	// the cheap-roles allowlist, downshift to local Ollama unless the caller
	// (env or ctx) has already pinned a specific model.
	if strings.TrimSpace(model) == "" && strings.TrimSpace(explicitProvider) == "" {
		if lp, lm := ResolveLocalFirstRouting(ctx.ProjectPath, ctx.Role); lm != "" {
			model = lm
			explicitProvider = lp
		}
	}
	if ctx != nil {
		if strings.TrimSpace(ctx.ModelOverride) != "" {
			model = strings.TrimSpace(ctx.ModelOverride)
		}
		if strings.TrimSpace(ctx.ProviderOverride) != "" {
			explicitProvider = strings.TrimSpace(ctx.ProviderOverride)
		}
	}

	provider := resolveProvider(explicitProvider, model)
	if model == "" {
		ctx.OnError("No model selected — choose a model/team in Preferences or .engine/config.yaml")
		return
	}

	// Autonomous safety: if a cloud provider was selected but the API key is missing,
	// automatically downshift to local llama.cpp/Ollama instead of erroring.
	// This ensures Engine never gets stuck waiting for credentials.
	hasAnthropicKey := runtimeValue(runtimeSettings.AnthropicKey, os.Getenv("ANTHROPIC_API_KEY")) != ""
	hasOpenAIKey := runtimeValue(runtimeSettings.OpenAIKey, os.Getenv("OPENAI_API_KEY")) != ""
	if provider == "anthropic" && !hasAnthropicKey {
		// Downshift to llama.cpp first, then Ollama
		llamacppModel := runtimeValue(runtimeSettings.LlamacppModel, os.Getenv("ENGINE_LLAMACPP_MODEL"))
		if llamacppModel != "" {
			provider = "llamacpp"
			model = llamacppModel
		} else {
			ollamaModel := runtimeValue(runtimeSettings.OllamaModel, os.Getenv("ENGINE_OLLAMA_MODEL"))
			if ollamaModel != "" {
				provider = "ollama"
				model = ollamaModel
			} else {
				provider = "llamacpp"
				model = "default"
			}
		}
	}
	if provider == "openai" && !hasOpenAIKey {
		// Downshift to llama.cpp first, then Ollama
		llamacppModel := runtimeValue(runtimeSettings.LlamacppModel, os.Getenv("ENGINE_LLAMACPP_MODEL"))
		if llamacppModel != "" {
			provider = "llamacpp"
			model = llamacppModel
		} else {
			ollamaModel := runtimeValue(runtimeSettings.OllamaModel, os.Getenv("ENGINE_OLLAMA_MODEL"))
			if ollamaModel != "" {
				provider = "ollama"
				model = ollamaModel
			} else {
				provider = "llamacpp"
				model = "default"
			}
		}
	}

	history, _ := db.GetMessages(ctx.SessionID)
	effectiveUserMessage := userMessage
	userMsgID := newID()
	persistUserMessage := true
	if ctx.ReloadContext {
		if _, anchor, ok := resolveRetryUserAnchor(history, ctx.RetryFromMessageID); ok {
			effectiveUserMessage = anchor.Content
			userMsgID = anchor.ID
			persistUserMessage = false
			history = historyFromRetryAnchor(history, ctx.RetryFromMessageID)
		}
	}

	if persistUserMessage {
		if err := saveMessageFn(userMsgID, ctx.SessionID, "user", effectiveUserMessage, nil); err != nil {
			ctx.OnError("Failed to save message: " + err.Error())
			return
		}
		_, _ = ingestMemoryEvent(ctx.ProjectPath, ctx.SessionID, "user_message", model, map[string]any{
			"id":   userMsgID,
			"text": effectiveUserMessage,
		})
		if ctx.OnSessionUpdated != nil {
			if updatedSession, err := db.GetSession(ctx.SessionID); err == nil && updatedSession != nil {
				ctx.OnSessionUpdated(updatedSession)
			}
		}
		history, _ = db.GetMessages(ctx.SessionID)
	}
	userMessage = effectiveUserMessage

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
	profile := loadProjectProfile(ctx.ProjectPath)
	if ctx.Role == RoleInteractive {
		extraContext = buildInteractiveExtraContext(session, openTabs)
	}

	// For workflow requests run a planner pre-pass so the interactive agent
	// starts with a concrete numbered plan instead of reasoning from scratch.
	// ClassifyRequest is heuristic-only (no LLM call) so this is cheap.
	if ClassifyRequest(userMessage, len(history)) == RequestWorkflow && ctx.Role == RoleInteractive {
		planOutput := runPlannerPrePass(ctx, provider, model, userMessage, branch)
		if strings.TrimSpace(planOutput) != "" {
			if ctx.SendToClient != nil {
				ctx.SendToClient("chat.plan", map[string]any{"plan": planOutput})
			}
			extraContext = strings.TrimSpace(extraContext + "\n\nIMPLEMENTATION PLAN:\n" + planOutput)
		}
	}

	identityContext := buildRuntimeIdentityContext(ctx, provider, model, branch)
	if identityContext != "" {
		extraContext = strings.TrimSpace(strings.TrimSpace(extraContext) + "\n\n" + identityContext)
	}

	if err := WriteAutonomyHandoffCache(ctx.ProjectPath, func() *AutonomyHandoff {
		handoff := BuildAutonomyHandoff(userMsgID, ctx.SessionID, ctx.ProjectPath, userMessage, func() string {
			if session == nil {
				return ""
			}
			return session.Summary
		}(), profile)
		return &handoff
	}()); err != nil {
		ctx.OnError(fmt.Sprintf("write autonomy handoff cache failed: %v", err))
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

	// Enforce token budget with compaction fallback for older context.
	budget := runtimeContextMaxTokens(ctx.ProjectPath)
	trimmedMessages, tokensUsed := trimToTokenBudgetFn(messages, budget)
	if tokensUsed > budget {
		ctx.OnError(fmt.Sprintf("⚠️ Conversation history exceeds token budget (%d > %d). Context compaction was applied.", tokensUsed, budget))
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
	if err := db.SaveMessage(assistantMessageID, ctx.SessionID, "assistant", finalText.String(), tc); err != nil {
		ctx.OnError(fmt.Sprintf("save assistant message failed: %v", err))
	}
	lastLedgerEvent, _ := ingestMemoryEvent(ctx.ProjectPath, ctx.SessionID, "assistant_message", model, map[string]any{
		"id":   assistantMessageID,
		"text": finalText.String(),
	})
	persistChatFinalState(ctx, assistantMessageID, userMsgID, userMessage, &finalText, allToolCalls,
		session, windowResult, selectiveContext, profile, openTabs, model, lastLedgerEvent)
	ctx.OnChunk("", true)
}

func resolveProjectDirection(projectPath string) string {
	direction, _ := db.GetProjectDirection(projectPath)
	if strings.TrimSpace(direction) != "" {
		return direction
	}
	return EnsureProjectDirection(projectPath)
}

// runPlannerPrePass fires a lean RolePlanner call and returns the plan text.
// It calls the provider RunLoop directly — no DB save, no user-message history
// side effects — so it is transparent to the main Chat loop.
func runPlannerPrePass(ctx *ChatContext, provider, model, userMessage, branch string) string {
	cancelChan := ctx.Cancel
	if cancelChan == nil {
		cancelChan = make(chan struct{})
	}

	resolvedProvider := strings.TrimSpace(provider)
	resolvedModel := strings.TrimSpace(model)
	plannerProvider, plannerModel := roleModelOverrideFromEnv(ctx.ProjectPath, RolePlanner)
	if plannerModel != "" {
		resolvedModel = plannerModel
	}
	if plannerProvider != "" {
		resolvedProvider = plannerProvider
	}
	resolvedProvider = resolveProvider(resolvedProvider, resolvedModel)
	if resolvedModel == "" {
		return ""
	}

	plannerCtx := &ChatContext{
		ProjectPath:      ctx.ProjectPath,
		SessionID:        ctx.SessionID,
		Role:             RolePlanner,
		ProviderOverride: resolvedProvider,
		ModelOverride:    resolvedModel,
		Usage:            ctx.Usage,
		Quarantine:       ctx.Quarantine,
		Cancel:           cancelChan,
		GetOpenTabs:      ctx.GetOpenTabs,
		OnError:          func(string) {},       // suppress planner errors from reaching the user
		OnChunk:          func(string, bool) {}, // no-op: output captured via finalText
		OnToolCall:       func(string, any) {},
		OnToolResult:     func(string, any, bool) {},
	}
	// Seed only the tools pre-granted to the planner role.
	if preGranted := roleBootstrapTools(RolePlanner); preGranted != nil {
		toolSet := make([]anthropicTool, 0, len(preGranted))
		for _, name := range preGranted {
			if t, ok := toolRegistryIndex[name]; ok {
				toolSet = append(toolSet, t)
			}
		}
		plannerCtx.ActiveTools = toolSet
	}
	systemPrompt := buildRoleSystemPrompt(RolePlanner, ctx.ProjectPath, branch, "")
	history := []anthropicMessage{{Role: "user", Content: userMessage}}
	var allToolCalls []ToolCall
	var finalText strings.Builder
	newProvider(resolvedProvider).RunLoop(plannerCtx, resolvedModel, systemPrompt, history, &allToolCalls, &finalText)
	return strings.TrimSpace(finalText.String())
}

func roleModelOverrideFromEnv(projectPath string, role AgentRole) (provider string, model string) {
	if cfg, err := runtimecfg.Load(projectPath); err == nil {
		switch role {
		case RolePlanner:
			provider = strings.TrimSpace(cfg.PlannerProvider)
			model = strings.TrimSpace(cfg.PlannerModel)
		case RoleReviewer:
			provider = strings.TrimSpace(cfg.ReviewerProvider)
			model = strings.TrimSpace(cfg.ReviewerModel)
		}
		if model != "" {
			return provider, model
		}
	}

	switch role {
	case RolePlanner:
		return strings.TrimSpace(os.Getenv("ENGINE_PLANNER_PROVIDER")), strings.TrimSpace(os.Getenv("ENGINE_PLANNER_MODEL"))
	case RoleReviewer:
		return strings.TrimSpace(os.Getenv("ENGINE_REVIEWER_PROVIDER")), strings.TrimSpace(os.Getenv("ENGINE_REVIEWER_MODEL"))
	default:
		return "", ""
	}
}

func applyFirstTurnAutonomyContext(ctx *ChatContext, userMessage, projectDirection string, isFirstUserMessage bool) {
	if !isFirstUserMessage {
		return
	}
	ensureProjectProfileCache(ctx.ProjectPath, userMessage, projectDirection)
	if HasExplicitStyleGuidance(userMessage) {
		return
	}
	if policy := ResolveAutonomousPolicy(ctx.ProjectPath); !policy.StyleAssumptionNotice {
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

func buildRuntimeIdentityContext(ctx *ChatContext, provider, model, branch string) string {
	if ctx == nil {
		return ""
	}
	resolvedProvider := strings.TrimSpace(provider)
	if resolvedProvider == "" {
		resolvedProvider = "unknown"
	}
	resolvedModel := strings.TrimSpace(model)
	if resolvedModel == "" {
		resolvedModel = "unknown"
	}
	role := agentRoleLabel(ctx.Role)
	if strings.TrimSpace(role) == "" {
		role = "unknown"
	}

	return strings.TrimSpace(strings.Join([]string{
		"Runtime identity (authoritative for this turn):",
		"- Agent role: " + role,
		"- Model provider: " + resolvedProvider,
		"- Model: " + resolvedModel,
		"When asked who/what you are, answer: Engine assistant for this workspace.",
		"When asked which model/provider is running, answer only from this runtime identity block.",
		"Never claim provider or model capabilities unless they are shown by workspace/tool evidence.",
	}, "\n"))
}

func buildInteractiveExtraContext(session *db.Session, openTabs []TabInfo) string {
	sections := make([]string, 0, 2)
	if focus := truncateSummary(strings.TrimSpace(buildCurrentFocusContext(openTabs)), 240); focus != "" {
		sections = append(sections, "Current focus:\n"+focus)
	}
	if session != nil {
		if summary := truncateSummary(strings.TrimSpace(session.Summary), 280); summary != "" {
			sections = append(sections, "Recent session memory:\n"+summary)
		}
	}
	return strings.Join(sections, "\n\n")
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

func loadProjectProfile(projectPath string) *ProjectProfile {
	raw, err := db.GetProjectProfile(projectPath)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	profile, parseErr := ParseProjectProfileJSON(raw)
	if parseErr != nil {
		return nil
	}
	return profile
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
	turns := 0

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
		turns++
		isAutonomous := ctx.Role == RoleAutonomousBuilder
		if ctx.shouldExitTurnLoop(turns, isAutonomous) {
			if isAutonomous {
				return
			}
			break
		}

		windowed := windowByVitality(messages, runtimeContextRecentWindow(ctx.ProjectPath))

		req := anthropicRequest{
			Model:     model,
			MaxTokens: 8192,
			System:    buildSystemBlocks(systemPrompt),
			Messages:  windowed,
			Tools:     ctx.ActiveTools, // live set — grows as model calls search_tools
			Stream:    true,
		}
		applyMessageCacheBreakpoint(&req)

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
				// With prompt caching on, input_tokens counts only the
				// uncached remainder, so cached tokens must be folded back in
				// or the usage log under-reports the real context size.
				totalTokens := usage.InputTokens + usage.CacheCreationInputTokens +
					usage.CacheReadInputTokens + usage.OutputTokens
				// Cache writes bill at 1.25x an input token and reads at 0.1x.
				// Costing them at face value would make caching look cheaper
				// than it is; costing them at zero would hide the write premium.
				billableInput := usage.InputTokens +
					(usage.CacheCreationInputTokens*5)/4 +
					usage.CacheReadInputTokens/10
				costUSD := EstimateCost(model, billableInput, usage.OutputTokens)
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
			_, _ = ingestMemoryEvent(ctx.ProjectPath, ctx.SessionID, "tool_call_started", req.Model, map[string]any{
				"tool": block.Name,
			})

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
			_, _ = ingestMemoryEvent(ctx.ProjectPath, ctx.SessionID, "tool_call_result", req.Model, map[string]any{
				"tool":       block.Name,
				"isError":    isError,
				"durationMs": durationMs,
				"error":      result,
			})
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
	// CacheCreationInputTokens is the count written to the prompt cache this
	// request (billed at a premium). CacheReadInputTokens is the count served
	// from cache (billed at a large discount). Reads exceeding creations after
	// the first turn is the signal that caching is working.
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// buildSystemBlocks renders the system prompt as a single cacheable text block.
//
// The breakpoint here is the high-value one: render order is tools -> system ->
// messages, so it caches the tool definitions and the whole system prompt in one
// entry. Both are stable for the life of a session, so every turn after the first
// reads this prefix instead of re-paying for it.
//
// Caveat worth knowing: ctx.ActiveTools grows when the model calls search_tools,
// and tools render at position 0. Each such growth invalidates this entry once.
// Tool discovery settles early in a session, so the steady state still hits.
func buildSystemBlocks(prompt string) []systemBlock {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	return []systemBlock{{
		Type:         "text",
		Text:         prompt,
		CacheControl: ephemeralCache(),
	}}
}

// applyMessageCacheBreakpoint marks the final content block of the last message,
// so the conversation prefix accumulated so far is cached for the next turn.
// Together with the system breakpoint this uses two of the four allowed per request.
func applyMessageCacheBreakpoint(req *anthropicRequest) {
	if req == nil || len(req.Messages) == 0 {
		return
	}
	// Copy before marking. req.Messages normally shares a backing array with the
	// caller's live history; writing the breakpoint in place would persist it into
	// stored history and accumulate a new breakpoint every turn, eventually
	// exceeding the four-per-request limit.
	msgs := make([]anthropicMessage, len(req.Messages))
	copy(msgs, req.Messages)
	last := &msgs[len(msgs)-1]

	switch c := last.Content.(type) {
	case string:
		if strings.TrimSpace(c) == "" {
			return
		}
		last.Content = []contentBlock{{Type: "text", Text: c, CacheControl: ephemeralCache()}}
	case []contentBlock:
		if len(c) == 0 {
			return
		}
		blocks := make([]contentBlock, len(c))
		copy(blocks, c)
		blocks[len(blocks)-1].CacheControl = ephemeralCache()
		last.Content = blocks
	default:
		return
	}
	req.Messages = msgs
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
	reqCtx, cancelReq := providerRequestContext(ctx)
	defer cancelReq()

	httpReq, _ := http.NewRequestWithContext(reqCtx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
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
					usage.CacheCreationInputTokens = tokenCountFromUsage(usageMap, "cache_creation_input_tokens")
					usage.CacheReadInputTokens = tokenCountFromUsage(usageMap, "cache_read_input_tokens")
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

func providerRequestContext(ctx *ChatContext) (stdctx.Context, stdctx.CancelFunc) {
	// 20-minute hard cap per API call so a slow/frozen local model can't stall the orchestrator forever.
	reqCtx, cancel := stdctx.WithTimeout(stdctx.Background(), 20*time.Minute)
	if ctx == nil || ctx.Cancel == nil {
		return reqCtx, cancel
	}
	go func() {
		select {
		case <-ctx.Cancel:
			cancel()
		case <-reqCtx.Done():
		}
	}()
	return reqCtx, cancel
}

func autonomousBuilderContinuePrompt(lastText, finishReason string) string {
	parts := []string{
		"Continue autonomously. You stopped without calling a tool or signal_done.",
		"Use tools now: inspect files, write required changes, run verification from the project root, commit completed work, and call signal_done only when the project is actually complete.",
		"Do not answer with narration or readiness text.",
	}
	if trimmed := strings.TrimSpace(lastText); trimmed != "" {
		parts = append(parts, "Last text response without tool use: "+trimmed)
	}
	if trimmed := strings.TrimSpace(finishReason); trimmed != "" {
		parts = append(parts, "Provider finish reason: "+trimmed)
	}
	return strings.Join(parts, "\n")
}

// ── OpenAI types and agentic loop ─────────────────────────────────────────────

type openAIFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content,omitempty"` // string or nil
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
}

type openAIRequest struct {
	Model      string          `json:"model"`
	Messages   []openAIMessage `json:"messages"`
	Tools      []openAITool    `json:"tools,omitempty"`
	ToolChoice string          `json:"tool_choice,omitempty"` // "auto" | "required" | "none"
	Stream     bool            `json:"stream"`
	KeepAlive  string          `json:"keep_alive,omitempty"` // Ollama only — extends model TTL
	// Options carries Ollama-specific knobs (num_ctx, temperature, etc.). Ollama's
	// /v1/chat/completions accepts an `options` extension; OpenAI ignores unknown
	// fields. Critical for local tool-calling: by default Ollama uses num_ctx=2048
	// which is far too small to fit our system prompt + tool definitions, so the
	// model never sees its tools and falls back to narrating instead of calling.
	Options map[string]any `json:"options,omitempty"`
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
	// extraOptions carries Ollama's `options` extension. The most important one is
	// num_ctx: Ollama's default of 2048 truncates everything past the first ~1500
	// tokens, which is well short of our system prompt + tool definitions. Without
	// raising it, the model literally cannot see the tools — that's the root cause
	// of "qwen3 just emits XML instead of calling tools". 32k matches what most
	// modern local models can handle comfortably; users override via env.
	var extraOptions map[string]any
	if providerName == "ollama" {
		numCtx := parseIntEnv("ENGINE_OLLAMA_NUM_CTX", 32768)
		if numCtx > 0 {
			extraOptions = map[string]any{"num_ctx": numCtx}
		}
	}
	// Convert history to OpenAI message format
	windowedHistory := windowByVitality(history, runtimeContextRecentWindow(ctx.ProjectPath))
	msgs := []openAIMessage{{Role: "system", Content: systemPrompt}}
	for _, m := range windowedHistory {
		content, _ := m.Content.(string)
		msgs = append(msgs, openAIMessage{Role: m.Role, Content: content})
	}

	oaiTools := openAIToolsFrom(ctx.ActiveTools)
	turns := 0

	for {
		if ctx.isCancelled() {
			return
		}
		turns++
		isAutonomous := ctx.Role == RoleAutonomousBuilder
		if ctx.shouldExitTurnLoop(turns, isAutonomous) {
			if isAutonomous {
				return
			}
			break
		}

		// Rebuild OAI tools each iteration — search_tools may have added new ones.
		oaiTools = openAIToolsFrom(ctx.ActiveTools)

		windowed := msgs
		if len(windowed) > 41 { // 1 system + 40 conversation
			windowed = append(msgs[:1:1], msgs[len(msgs)-40:]...)
		}

		// Autonomous builder turns must act through tools. Bounded test/review
		// loops also require a tool call so a single turn cannot be spent on
		// narration instead of action.
		toolChoice := ""
		if len(oaiTools) > 0 {
			toolChoice = "auto"
			if ctx.MaxTurns > 0 || ctx.Role == RoleAutonomousBuilder {
				toolChoice = "required"
			}
		}
		req := openAIRequest{
			Model:      model,
			Messages:   windowed,
			Tools:      oaiTools,
			ToolChoice: toolChoice,
			Stream:     true,
			KeepAlive:  keepAlive,
			Options:    extraOptions,
		}

		body, _ := json.Marshal(req)
		reqCtx, cancelReq := providerRequestContext(ctx)
		httpReq, err := http.NewRequestWithContext(reqCtx, "POST", endpointURL, bytes.NewReader(body))
		if err != nil {
			cancelReq()
			ctx.OnError(providerName + " request build: " + err.Error())
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if useAuthorization {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			cancelReq()
			ctx.OnError(providerName + " request: " + err.Error())
			return
		}

		if resp.StatusCode != 200 {
			var errBody map[string]any
			json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
			resp.Body.Close()
			cancelReq()
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
		scanErr := scanner.Err()
		resp.Body.Close()
		cancelReq()
		if scanErr != nil && !errors.Is(scanErr, io.EOF) && !errors.Is(scanErr, io.ErrUnexpectedEOF) {
			ctx.OnError(providerName + " stream: " + scanErr.Error())
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

		// Fallback: some open models (qwen3.x, hermes, several mistral templates) ignore the
		// OpenAI `tools` schema and emit tool calls as XML inside the content stream. If we
		// got no native tool_calls but the text contains parseable tool-call markup, extract
		// it, synthesise toolCallMap entries, and clean the user-facing text. This keeps the
		// agent productive on models that can call tools by convention but not by protocol.
		if len(toolCallMap) == 0 && textBuf.Len() > 0 {
			parsed := parseToolCallsFromText(textBuf.String())
			if len(parsed) > 0 {
				cleanedText := stripToolCallMarkup(textBuf.String())
				textBuf.Reset()
				textBuf.WriteString(cleanedText)
				if before := finalText.String(); before != "" {
					cleanedFinal := stripToolCallMarkup(before)
					finalText.Reset()
					finalText.WriteString(cleanedFinal)
				}
				for i, p := range parsed {
					tcd := &toolCallDelta{
						index: i,
						id:    synthesizeToolCallID(p.Name, i),
						name:  p.Name,
					}
					tcd.args.WriteString(p.Arguments)
					toolCallMap[i] = tcd
				}
				finishReason = "tool_calls"
			}
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
			if ctx.Role == RoleAutonomousBuilder {
				msgs = append(msgs, openAIMessage{Role: "user", Content: autonomousBuilderContinuePrompt(textBuf.String(), finishReason)})
				continue
			}
			break
		}

		for _, tcd := range toolCallMap {
			if tcd != nil && tcd.name == "signal_done" {
				summary := strings.TrimSpace(tcd.args.String())
				if ctx.OnToolCall != nil {
					ctx.OnToolCall(tcd.name, map[string]any{"summary": summary})
				}
				if ctx.OnToolResult != nil {
					ctx.OnToolResult(tcd.name, "done", false)
				}
				*allToolCalls = append(*allToolCalls, ToolCall{
					ID:     tcd.id,
					Name:   tcd.name,
					Input:  map[string]any{"summary": summary},
					Result: "done",
				})
				return
			}
		}

		// Execute tools and add results.
		// If the model called a tool that exists in the registry but was not yet
		// in ActiveTools (deferred discovery), promote it silently so it shows
		// up in the tools list for the next iteration and can be called again.
		// Tool call error codes (compact, to avoid clogging context):
		//   E1 = bad JSON arguments   E3 = execution error
		activeByName := make(map[string]bool, len(ctx.ActiveTools))
		for _, t := range ctx.ActiveTools {
			activeByName[t.Name] = true
		}
		for i := 0; i < len(toolCallMap); i++ {
			tcd := toolCallMap[i]
			if tcd == nil {
				continue
			}
			if !activeByName[tcd.name] {
				if t, ok := toolRegistryIndex[tcd.name]; ok {
					ctx.ActiveTools = append(ctx.ActiveTools, t)
					activeByName[tcd.name] = true
				}
			}

			var inputMap map[string]any
			argsStr := tcd.args.String()
			if argsStr == "" || argsStr == "null" {
				argsStr = "{}"
			}
			if err := json.Unmarshal([]byte(argsStr), &inputMap); err != nil || inputMap == nil {
				// E1: malformed args — feed compact error back, skip execution
				errMsg := fmt.Sprintf("E1: invalid JSON arguments for %s. Correct the arguments and retry.", tcd.name)
				if ctx.OnToolCall != nil {
					ctx.OnToolCall(tcd.name, map[string]any{"_raw": argsStr})
				}
				if ctx.OnToolResult != nil {
					ctx.OnToolResult(tcd.name, errMsg, true)
				}
				msgs = append(msgs, openAIMessage{Role: "tool", ToolCallID: tcd.id, Content: errMsg})
				continue
			}

			if ctx.OnToolCall != nil {
				ctx.OnToolCall(tcd.name, inputMap)
			}
			start := time.Now()
			result, isError := executeTool(tcd.name, inputMap, ctx)
			durationMs := time.Since(start).Milliseconds()

			db.LogToolCall(newID(), ctx.SessionID, tcd.name, inputMap, result, isError, durationMs) //nolint:errcheck
			if ctx.OnToolResult != nil {
				ctx.OnToolResult(tcd.name, result, isError)
			}

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
