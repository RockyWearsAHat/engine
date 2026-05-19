package ai

import "testing"

func TestParseIntEnv_DefaultForMissingInvalidAndNonPositive(t *testing.T) {
	t.Setenv("ENGINE_PARSE_INT_TEST", "")
	if got := parseIntEnv("ENGINE_PARSE_INT_TEST", 64); got != 64 {
		t.Fatalf("missing env should return default, got %d", got)
	}

	t.Setenv("ENGINE_PARSE_INT_TEST", "nope")
	if got := parseIntEnv("ENGINE_PARSE_INT_TEST", 64); got != 64 {
		t.Fatalf("invalid env should return default, got %d", got)
	}

	t.Setenv("ENGINE_PARSE_INT_TEST", "0")
	if got := parseIntEnv("ENGINE_PARSE_INT_TEST", 64); got != 64 {
		t.Fatalf("non-positive env should return default, got %d", got)
	}

	t.Setenv("ENGINE_PARSE_INT_TEST", "-4")
	if got := parseIntEnv("ENGINE_PARSE_INT_TEST", 64); got != 64 {
		t.Fatalf("negative env should return default, got %d", got)
	}
}

func TestParseIntEnv_ValidValue(t *testing.T) {
	t.Setenv("ENGINE_PARSE_INT_TEST", " 128 ")
	if got := parseIntEnv("ENGINE_PARSE_INT_TEST", 64); got != 128 {
		t.Fatalf("expected parsed value 128, got %d", got)
	}
}

func TestResolveProvider_LlamaCppAliases(t *testing.T) {
	if got := resolveProvider("llama.cpp", "model"); got != "llamacpp" {
		t.Fatalf("expected llama.cpp alias to map to llamacpp, got %q", got)
	}
	if got := resolveProvider("llama-cpp", "model"); got != "llamacpp" {
		t.Fatalf("expected llama-cpp alias to map to llamacpp, got %q", got)
	}
}

func TestDefaultModelForProvider_AllBranches(t *testing.T) {
	if got := defaultModelForProvider("openai"); got != defaultOpenAIModel {
		t.Fatalf("openai default mismatch: %q", got)
	}
	if got := defaultModelForProvider("ollama"); got != defaultOllamaModel {
		t.Fatalf("ollama default mismatch: %q", got)
	}
	if got := defaultModelForProvider("llamacpp"); got != defaultLlamacppModel {
		t.Fatalf("llamacpp default mismatch: %q", got)
	}
	if got := defaultModelForProvider("anthropic"); got != defaultAnthropicModel {
		t.Fatalf("anthropic default mismatch: %q", got)
	}
	if got := defaultModelForProvider("unknown-provider"); got != defaultAnthropicModel {
		t.Fatalf("unknown provider should fall back to anthropic default, got %q", got)
	}
}
