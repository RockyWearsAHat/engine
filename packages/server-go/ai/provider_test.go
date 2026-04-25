package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveProviderPrefersExplicitSetting(t *testing.T) {
	if got := resolveProvider("ollama", "claude-sonnet-4-6"); got != "ollama" {
		t.Fatalf("expected explicit provider to win, got %q", got)
	}
	if got := resolveProvider("openai", "llama3.2"); got != "openai" {
		t.Fatalf("expected explicit provider to win, got %q", got)
	}
}

func TestResolveProviderFallsBackToModelInference(t *testing.T) {
	if got := resolveProvider("", ""); got != "ollama" {
		t.Fatalf("expected empty model to default to ollama, got %q", got)
	}
	if got := resolveProvider("", "gpt-4o"); got != "openai" {
		t.Fatalf("expected gpt model to infer openai, got %q", got)
	}
	if got := resolveProvider("auto", "claude-sonnet-4-6"); got != "anthropic" {
		t.Fatalf("expected claude model to infer anthropic, got %q", got)
	}
	if got := resolveProvider("", "gemma4:31b"); got != "ollama" {
		t.Fatalf("expected gemma tag model to infer ollama, got %q", got)
	}
	if got := resolveProvider("auto", "llama3.2"); got != "ollama" {
		t.Fatalf("expected llama family model to infer ollama, got %q", got)
	}
}

func TestDefaultModelForProvider(t *testing.T) {
	tests := map[string]string{
		"anthropic": defaultAnthropicModel,
		"openai":    defaultOpenAIModel,
		"ollama":    defaultOllamaModel,
	}
	for provider, expected := range tests {
		if got := defaultModelForProvider(provider); got != expected {
			t.Fatalf("expected %s default model %q, got %q", provider, expected, got)
		}
	}
}

func TestOllamaChatCompletionsURL(t *testing.T) {
	tests := map[string]string{
		"":                          "http://127.0.0.1:11434/v1/chat/completions",
		"http://127.0.0.1:11434":    "http://127.0.0.1:11434/v1/chat/completions",
		"http://127.0.0.1:11434/v1": "http://127.0.0.1:11434/v1/chat/completions",
		"http://127.0.0.1:11434/v1/chat/completions": "http://127.0.0.1:11434/v1/chat/completions",
	}
	for input, expected := range tests {
		if got := ollamaChatCompletionsURL(input); got != expected {
			t.Fatalf("expected Ollama URL %q for %q, got %q", expected, input, got)
		}
	}
}

func TestOllamaRootURL(t *testing.T) {
	tests := map[string]string{
		"":                          "http://127.0.0.1:11434",
		"http://127.0.0.1:11434":    "http://127.0.0.1:11434",
		"http://127.0.0.1:11434/v1": "http://127.0.0.1:11434",
		"http://127.0.0.1:11434/v1/chat/completions": "http://127.0.0.1:11434",
	}
	for input, expected := range tests {
		if got := ollamaRootURL(input); got != expected {
			t.Fatalf("expected Ollama root %q for %q, got %q", expected, input, got)
		}
	}
}

// ── newProvider ───────────────────────────────────────────────────────────────

func TestNewProvider_Anthropic(t *testing.T) {
	p := newProvider("anthropic")
	if _, ok := p.(*anthropicProvider); !ok {
		t.Errorf("expected anthropicProvider, got %T", p)
	}
}

func TestNewProvider_OpenAI(t *testing.T) {
	p := newProvider("openai")
	if _, ok := p.(*openAIProvider); !ok {
		t.Errorf("expected openAIProvider, got %T", p)
	}
}

func TestNewProvider_Ollama(t *testing.T) {
	p := newProvider("ollama")
	if _, ok := p.(*ollamaProvider); !ok {
		t.Errorf("expected ollamaProvider, got %T", p)
	}
}

func TestNewProvider_UnknownFallsBackToAnthropic(t *testing.T) {
	p := newProvider("unknown")
	if _, ok := p.(*anthropicProvider); !ok {
		t.Errorf("expected anthropicProvider for unknown, got %T", p)
	}
}

// ── inferredProviderForModel ──────────────────────────────────────────────────

func TestInferredProviderForModel_GPT(t *testing.T) {
	tests := []string{"gpt-4o", "gpt-4o-mini", "o1-preview", "o3-mini", "o4-ultra"}
	for _, model := range tests {
		if got := inferredProviderForModel(model); got != "openai" {
			t.Errorf("expected openai for %s, got %q", model, got)
		}
	}
}

func TestInferredProviderForModel_Claude(t *testing.T) {
	tests := []string{"claude-opus-4-5", "claude-sonnet-4-6", "claude-haiku-4.5"}
	for _, model := range tests {
		if got := inferredProviderForModel(model); got != "anthropic" {
			t.Errorf("expected anthropic for %s, got %q", model, got)
		}
	}
}

func TestInferredProviderForModel_OllamaFamily(t *testing.T) {
	tests := []string{"llama3.2", "gemma:7b", "mistral", "mixtral", "deepseek-coder", "qwen"}
	for _, model := range tests {
		if got := inferredProviderForModel(model); got != "ollama" {
			t.Errorf("expected ollama for %s, got %q", model, got)
		}
	}
}

func TestInferredProviderForModel_EmptyFallsToOllama(t *testing.T) {
	if got := inferredProviderForModel(""); got != "ollama" {
		t.Errorf("expected ollama for empty model, got %q", got)
	}
}

func TestInferredProviderForModel_CaseInsensitive(t *testing.T) {
	if got := inferredProviderForModel("GPT-4O"); got != "openai" {
		t.Errorf("expected case-insensitive openai for GPT-4O, got %q", got)
	}
	if got := inferredProviderForModel("CLAUDE-SONNET-4-6"); got != "anthropic" {
		t.Errorf("expected case-insensitive anthropic, got %q", got)
	}
}

// ── looksLikeOllamaModel ──────────────────────────────────────────────────────

func TestLooksLikeOllamaModel_WithTag(t *testing.T) {
	tests := []string{"llama3.2", "gemma:7b", "mistral:7b"}
	for _, model := range tests {
		if !looksLikeOllamaModel(model) {
			t.Errorf("expected %s to look like ollama model", model)
		}
	}
}

func TestLooksLikeOllamaModel_WithPrefix(t *testing.T) {
	tests := []string{"llama2", "gemma4", "mistral-small", "qwen-large", "deepseek"}
	for _, model := range tests {
		if !looksLikeOllamaModel(model) {
			t.Errorf("expected %s to look like ollama model", model)
		}
	}
}

func TestLooksLikeOllamaModel_NotOllama(t *testing.T) {
	tests := []string{"gpt-4o", "claude-sonnet", "my-custom-model"}
	for _, model := range tests {
		if looksLikeOllamaModel(model) {
			t.Errorf("expected %s to not look like ollama model", model)
		}
	}
}

// ── detectOllamaModel ─────────────────────────────────────────────────────────

func TestDetectOllamaModel_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/ps") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"models":[{"name":"llama3.2"},{"name":"gemma:7b"}]}`)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	if got := detectOllamaModel(server.URL); got != "llama3.2" {
		t.Errorf("expected llama3.2, got %q", got)
	}
}

func TestDetectOllamaModel_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/ps") {
			w.WriteHeader(http.StatusNotFound)
		} else if strings.Contains(r.URL.Path, "/v1/models") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":[{"id":"mistral"}]}`)
		}
	}))
	defer server.Close()

	if got := detectOllamaModel(server.URL); got != "mistral" {
		t.Errorf("expected mistral from fallback, got %q", got)
	}
}

func TestDetectOllamaModel_NoServer(t *testing.T) {
	// Invalid URL that will fail to connect.
	if got := detectOllamaModel("http://localhost:65535"); got != "" {
		t.Errorf("expected empty model for failed connection, got %q", got)
	}
}

func TestDetectOllamaModel_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"models":[]}`)
	}))
	defer server.Close()

	if got := detectOllamaModel(server.URL); got != "" {
		t.Errorf("expected empty model for empty list, got %q", got)
	}
}

func TestNewProvider_Unknown_DefaultsToAnthropic(t *testing.T) {
	p := newProvider("xyzunknown")
	if _, ok := p.(*anthropicProvider); !ok {
		t.Errorf("expected anthropicProvider for unknown name, got %T", p)
	}
}

// ── ollamaProvider.RunLoop ────────────────────────────────────────────────────

func TestOllamaProvider_RunLoop_Cancelled(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	ctx := &ChatContext{
		Cancel:      ch,
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
	}
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:1")
	var calls []ToolCall
	var text strings.Builder
	p := newProvider("ollama")
	p.RunLoop(ctx, "llama3", "system", []anthropicMessage{}, &calls, &text)
}

func TestOllamaProvider_RunLoop_ServerResponse(t *testing.T) {
	sseResponse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseResponse)
	}))
	defer srv.Close()

	var chunks []string
	ctx := &ChatContext{
		OnChunk:     func(content string, _ bool) { chunks = append(chunks, content) },
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
	}
	t.Setenv("OLLAMA_BASE_URL", srv.URL)
	var calls []ToolCall
	var text strings.Builder
	p := newProvider("ollama")
	p.RunLoop(ctx, "llama3", "system", []anthropicMessage{}, &calls, &text)
	if !strings.Contains(text.String(), "hi") {
		t.Errorf("expected 'hi' in response, got %q", text.String())
	}
}

// ── openAIProvider.RunLoop ────────────────────────────────────────────────────

func TestOpenAIProvider_RunLoop_Cancelled(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	ctx := &ChatContext{
		Cancel:      ch,
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
	}
	t.Setenv("OPENAI_API_KEY", "fake-key")
	var calls []ToolCall
	var text strings.Builder
	p := newProvider("openai")
	p.RunLoop(ctx, "gpt-4o", "system", []anthropicMessage{}, &calls, &text)
}

// ── anthropicProvider.RunLoop ─────────────────────────────────────────────────

func TestAnthropicProvider_RunLoop_Cancelled(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	ctx := &ChatContext{
		Cancel:      ch,
		OnChunk:     func(string, bool) {},
		OnToolCall:  func(string, interface{}) {},
		OnToolResult: func(string, interface{}, bool) {},
		OnError:     func(string) {},
		ActiveTools: bootstrapTools(),
		Usage:       &SessionUsage{},
		Quarantine:  NewToolQuarantine(),
	}
	t.Setenv("ANTHROPIC_API_KEY", "fake-key")
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: failTransport{t}}
	var calls []ToolCall
	var text strings.Builder
	p := newProvider("anthropic")
	p.RunLoop(ctx, "claude-3-5-sonnet-20241022", "system", []anthropicMessage{}, &calls, &text)
}
