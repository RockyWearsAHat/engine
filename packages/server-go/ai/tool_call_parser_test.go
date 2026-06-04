package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/engine/server/db"
)

func TestParseToolCallsFromText_HermesXML(t *testing.T) {
	raw := "I will read main.go.\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"main.go\"}}\n</tool_call>"
	calls := parseToolCallsFromText(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d (%+v)", len(calls), calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("expected name=read_file, got %q", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v (%q)", err, calls[0].Arguments)
	}
	if args["path"] != "main.go" {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestParseToolCallsFromText_MultipleHermesXML(t *testing.T) {
	raw := `<tool_call>{"name": "list_directory", "arguments": {"path": "."}}</tool_call>` +
		"\n" +
		`<tool_call>{"name": "read_file", "arguments": {"path": "main.go"}}</tool_call>`
	calls := parseToolCallsFromText(raw)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "list_directory" || calls[1].Name != "read_file" {
		t.Fatalf("wrong order/names: %+v", calls)
	}
}

func TestParseToolCallsFromText_PipeSentinel(t *testing.T) {
	raw := "<|tool_call|>{\"name\":\"read_file\",\"arguments\":{\"path\":\"x\"}}<|/tool_call|>"
	calls := parseToolCallsFromText(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("unexpected name: %q", calls[0].Name)
	}
}

func TestParseToolCallsFromText_FunctionTag(t *testing.T) {
	raw := "<function>{\"name\":\"signal_done\",\"arguments\":{\"summary\":\"ok\"}}</function>"
	calls := parseToolCallsFromText(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "signal_done" {
		t.Fatalf("unexpected name: %q", calls[0].Name)
	}
}

func TestParseToolCallsFromText_ArgumentsAsString(t *testing.T) {
	raw := `<tool_call>{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}</tool_call>`
	calls := parseToolCallsFromText(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Arguments), &args); err != nil {
		t.Fatalf("arguments not JSON after unwrap: %v (%q)", err, calls[0].Arguments)
	}
	if args["path"] != "main.go" {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestParseToolCallsFromText_ParametersAlias(t *testing.T) {
	raw := `<tool_call>{"name":"read_file","parameters":{"path":"a"}}</tool_call>`
	calls := parseToolCallsFromText(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if !strings.Contains(calls[0].Arguments, `"path":"a"`) {
		t.Fatalf("expected path arg, got %q", calls[0].Arguments)
	}
}

func TestParseToolCallsFromText_PlainProseIsIgnored(t *testing.T) {
	raw := "Sure, I'll read main.go and then write the result. Here's a JSON example: {\"foo\":\"bar\"}."
	if calls := parseToolCallsFromText(raw); len(calls) != 0 {
		t.Fatalf("expected no calls in plain prose, got %+v", calls)
	}
}

func TestParseToolCallsFromText_FencedJSONFallback(t *testing.T) {
	raw := "Here is the call:\n```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"a\"}}\n```"
	calls := parseToolCallsFromText(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("unexpected name: %q", calls[0].Name)
	}
}

func TestParseToolCallsFromText_FencedJSONWithoutNameIgnored(t *testing.T) {
	raw := "Example output:\n```json\n{\"result\":\"hello\"}\n```"
	if calls := parseToolCallsFromText(raw); len(calls) != 0 {
		t.Fatalf("fenced non-tool JSON should be ignored, got %+v", calls)
	}
}

func TestParseToolCallsFromText_EmptyInput(t *testing.T) {
	if calls := parseToolCallsFromText(""); calls != nil {
		t.Fatalf("expected nil for empty input, got %+v", calls)
	}
}

func TestLooksLikeToolCallJSON_InvalidShapes(t *testing.T) {
	if looksLikeToolCallJSON("[]") {
		t.Fatal("expected false for non-object JSON")
	}
	if looksLikeToolCallJSON("{bad") {
		t.Fatal("expected false for invalid JSON")
	}
	if looksLikeToolCallJSON("{bad}") {
		t.Fatal("expected false for malformed object JSON")
	}
	if looksLikeToolCallJSON(`{"name":"read_file"}`) {
		t.Fatal("expected false when name is present but arguments/parameters are missing")
	}
}

func TestDecodeToolCallJSON_ErrorBranches(t *testing.T) {
	if _, ok := decodeToolCallJSON("{not-json"); ok {
		t.Fatal("expected decode failure for invalid json")
	}
	if _, ok := decodeToolCallJSON(`{"name":"   ","arguments":{}}`); ok {
		t.Fatal("expected decode failure for blank name")
	}
}

func TestDecodeToolCallJSON_DefaultsEmptyArgumentsObject(t *testing.T) {
	call, ok := decodeToolCallJSON(`{"name":"noop"}`)
	if !ok {
		t.Fatal("expected decode success")
	}
	if call.Arguments != "{}" {
		t.Fatalf("expected default empty object args, got %q", call.Arguments)
	}
}

func TestStripToolCallMarkup_EmptyInput(t *testing.T) {
	if got := stripToolCallMarkup(""); got != "" {
		t.Fatalf("expected empty output for empty input, got %q", got)
	}
}

func TestStripToolCallMarkup_RemovesXMLAndKeepsProse(t *testing.T) {
	raw := "I will read the file.\n<tool_call>{\"name\":\"read_file\",\"arguments\":{\"path\":\"x\"}}</tool_call>\nThen continue."
	cleaned := stripToolCallMarkup(raw)
	if strings.Contains(cleaned, "<tool_call>") || strings.Contains(cleaned, "</tool_call>") {
		t.Fatalf("xml not stripped: %q", cleaned)
	}
	if !strings.Contains(cleaned, "I will read the file.") || !strings.Contains(cleaned, "Then continue.") {
		t.Fatalf("prose lost: %q", cleaned)
	}
}

// TestOpenAILoop_FallsBackToXMLToolCalls drives the OpenAI-compat loop through a fake
// Ollama endpoint that emits a tool call as Hermes-style XML in the content stream
// (no native `tool_calls`). The loop should still execute the tool, send the result
// back, and converge — exactly the broken qwen3.x behaviour we are repairing.
func TestOpenAILoop_FallsBackToXMLToolCalls(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-xml-fallback", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Write a known file so read_file has something to return.
	if err := writeFileForTest(projectDir, "main.go", "package main\n"); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:35b"}]}`))
		case "/v1/chat/completions":
			n := atomic.AddInt32(&callCount, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 1 {
				// Model ignores OpenAI tool_calls and emits Hermes XML in content.
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"<tool_call>{\\\"name\\\":\\\"read_file\\\",\\\"arguments\\\":{\\\"path\\\":\\\"main.go\\\"}}</tool_call>\"},\"finish_reason\":null}]}\n\n"))
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			} else {
				// After the tool result is returned, model wraps up.
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"file looks fine\"},\"finish_reason\":null}]}\n\n"))
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "qwen3:35b")
	t.Setenv("OLLAMA_BASE_URL", server.URL)

	var toolNames []string
	var assistant strings.Builder
	ctx := &ChatContext{
		ProjectPath:      projectDir,
		SessionID:        "session-xml-fallback",
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk: func(content string, done bool) {
			if !done {
				assistant.WriteString(content)
			}
		},
		OnToolCall: func(name string, _ any) {
			toolNames = append(toolNames, name)
		},
		OnToolResult: func(string, any, bool) {},
		OnError: func(err string) {
			t.Fatalf("unexpected chat error: %s", err)
		},
	}

	Chat(ctx, "look at main.go")

	if got := atomic.LoadInt32(&callCount); got < 2 {
		t.Fatalf("expected at least 2 provider calls (initial XML call + follow-up after tool result), got %d", got)
	}
	if len(toolNames) == 0 || toolNames[0] != "read_file" {
		t.Fatalf("expected read_file to be executed from XML fallback, got %+v", toolNames)
	}
	if !strings.Contains(assistant.String(), "file looks fine") {
		t.Fatalf("expected follow-up assistant text streamed to client, got %q", assistant.String())
	}

	saved, err := db.GetMessages("session-xml-fallback")
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	var assistantMsg string
	for _, m := range saved {
		if m.Role == "assistant" {
			assistantMsg = m.Content
		}
	}
	if strings.Contains(assistantMsg, "<tool_call>") {
		t.Fatalf("persisted assistant message still contains raw tool_call markup: %q", assistantMsg)
	}
}

// TestChat_LlamacppProvider_UsesEndpoint asserts the llamacpp provider routes traffic
// to LLAMACPP_BASE_URL/v1/chat/completions and that the loop converges on a normal text
// response (the same shape the OpenAI-compat path produces).
func TestChat_LlamacppProvider_UsesEndpoint(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-llamacpp", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	var hitPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPaths = append(hitPaths, r.URL.Path)
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello from llama.cpp\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "llamacpp")
	t.Setenv("ENGINE_MODEL", "qwen3-coder")
	t.Setenv("LLAMACPP_BASE_URL", server.URL)

	var got strings.Builder
	ctx := &ChatContext{
		ProjectPath:      projectDir,
		SessionID:        "session-llamacpp",
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk: func(content string, done bool) {
			if !done {
				got.WriteString(content)
			}
		},
		OnToolCall:   func(string, any) {},
		OnToolResult: func(string, any, bool) {},
		OnError:      func(err string) { t.Fatalf("unexpected chat error: %s", err) },
	}

	Chat(ctx, "ping")

	if !strings.Contains(got.String(), "hello from llama.cpp") {
		t.Fatalf("expected llama.cpp response, got %q", got.String())
	}
	if len(hitPaths) == 0 || hitPaths[0] != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions hit, got %+v", hitPaths)
	}
}

// TestOllamaLoop_SendsNumCtxOption asserts that Ollama-bound requests carry the
// `options.num_ctx` field so the model actually sees its full system prompt and
// tool definitions. Without this, tool calling fails silently — the root cause
// behind the qwen3.x XML-fallback path we added in tool_call_parser.go.
func TestOllamaLoop_SendsNumCtxOption(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-numctx", projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	type capturedReq struct {
		Options map[string]any `json:"options"`
	}
	var captured capturedReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:35b"}]}`))
		case "/v1/chat/completions":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode captured chat request: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "qwen3:35b")
	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("ENGINE_OLLAMA_NUM_CTX", "16384")

	ctx := &ChatContext{
		ProjectPath:      projectDir,
		SessionID:        "session-numctx",
		OnSessionUpdated: func(_ *db.Session) {},
		OnChunk:          func(string, bool) {},
		OnToolCall:       func(string, any) {},
		OnToolResult:     func(string, any, bool) {},
		OnError:          func(err string) { t.Fatalf("unexpected chat error: %s", err) },
	}
	Chat(ctx, "ping")

	if captured.Options == nil {
		t.Fatalf("expected options to be sent to Ollama, got none")
	}
	v, ok := captured.Options["num_ctx"]
	if !ok {
		t.Fatalf("expected num_ctx in options, got %+v", captured.Options)
	}
	if n, _ := v.(float64); int(n) != 16384 {
		t.Fatalf("expected num_ctx=16384, got %v", v)
	}
}

func writeFileForTest(projectDir, name, content string) error {
	return os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0644)
}
