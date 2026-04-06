package ai

import "testing"

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
