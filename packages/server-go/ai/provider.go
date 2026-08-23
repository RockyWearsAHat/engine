package ai

import (
	"os"
	"strings"

	"github.com/engine/server/runtimecfg"
)

// Provider executes the full agentic chat loop for a specific AI backend.
//
// To add a new provider:
//  1. Define a struct implementing Provider.
//  2. Add a case in newProvider().
//
// All provider structs read their credentials and config from env at RunLoop
// call time (via os.Getenv) so config.sync can update them at runtime.
type Provider interface {
	RunLoop(
		ctx *ChatContext,
		model string,
		systemPrompt string,
		history []anthropicMessage,
		allToolCalls *[]ToolCall,
		finalText *strings.Builder,
	)
}

var newProvider = newProviderForName

// newProviderForName returns the Provider for the given backend name.
// Supported values: "ollama", "llamacpp", "openai", "anthropic", "claudecode".
// Any unrecognised name falls back to "anthropic".
//
// "llamacpp" hits the llama.cpp server's /v1/chat/completions endpoint directly,
// which gives lower-latency inference than Ollama's wrapper and supports grammar-
// constrained tool calling. Use it when you control the runtime; use "ollama" when
// you want Ollama's model management on top.
//
// "claudecode" shells out to the Claude Code CLI (`claude -p`), which runs its
// own agentic loop against the project directory and authenticates via the
// user's Claude subscription (Max/Pro) instead of an ANTHROPIC_API_KEY. See
// claudecode.go for why it behaves differently from the API providers.
func newProviderForName(name string) Provider {
	switch name {
	case "openai":
		return &openAIProvider{}
	case "ollama":
		return &ollamaProvider{}
	case "llamacpp", "llama.cpp", "llama-cpp":
		return &llamacppProvider{}
	case "claudecode", "claude-code", "claude_code":
		return &claudecodeProvider{}
	default: // "anthropic"
		return &anthropicProvider{}
	}
}

// ── Ollama ───────────────────────────────────────────────────────────────────
// Uses the OpenAI-compatible /v1/chat/completions endpoint that Ollama exposes.
// No API key required.

type ollamaProvider struct{}

func (p *ollamaProvider) RunLoop(
	ctx *ChatContext,
	model, systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	baseURL := ""
	if cfg, err := runtimecfg.Load(ctx.ProjectPath); err == nil {
		baseURL = strings.TrimSpace(cfg.OllamaBaseURL)
	}
	if baseURL == "" {
		baseURL = os.Getenv("OLLAMA_BASE_URL")
	}
	runOpenAICompatibleLoop(
		ctx, "ollama", model,
		ollamaChatCompletionsURL(baseURL),
		"", false,
		systemPrompt, history, allToolCalls, finalText,
	)
}

// ── llama.cpp ────────────────────────────────────────────────────────────────
// Uses llama.cpp's `llama-server` /v1/chat/completions endpoint. No API key.
// Default base URL is http://127.0.0.1:8080 (llama-server's default).
//
// Why this exists alongside the ollama provider:
//   - llama.cpp exposes native function calling on models that support it (Qwen,
//     Functionary, etc.) and supports a `grammar` parameter for constrained output.
//   - Ollama wraps llama.cpp but historically lags behind on chat-template fixes
//     and adds an extra serialisation hop that hurts latency.
//   - Running llama-server directly avoids the Ollama daemon entirely for users
//     who already have a model loaded by hand.
type llamacppProvider struct{}

func (p *llamacppProvider) RunLoop(
	ctx *ChatContext,
	model, systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	baseURL := ""
	if cfg, err := runtimecfg.Load(ctx.ProjectPath); err == nil {
		baseURL = strings.TrimSpace(cfg.LlamacppBaseURL)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("LLAMACPP_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	runOpenAICompatibleLoop(
		ctx, "llamacpp", model,
		endpoint,
		"", false,
		systemPrompt, history, allToolCalls, finalText,
	)
}

// ── OpenAI ───────────────────────────────────────────────────────────────────
// Uses the official OpenAI /v1/chat/completions endpoint with Bearer auth.

type openAIProvider struct{}

func (p *openAIProvider) RunLoop(
	ctx *ChatContext,
	model, systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	apiKey := ""
	if cfg, err := runtimecfg.Load(ctx.ProjectPath); err == nil {
		apiKey = strings.TrimSpace(cfg.OpenAIKey)
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	runOpenAICompatibleLoop(
		ctx, "openai", model,
		"https://api.openai.com/v1/chat/completions",
		apiKey, true,
		systemPrompt, history, allToolCalls, finalText,
	)
}

// ── Anthropic ────────────────────────────────────────────────────────────────
// Uses the native Anthropic Messages API with SSE streaming.

type anthropicProvider struct{}

func (p *anthropicProvider) RunLoop(
	ctx *ChatContext,
	model, systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	apiKey := ""
	if cfg, err := runtimecfg.Load(ctx.ProjectPath); err == nil {
		apiKey = strings.TrimSpace(cfg.AnthropicKey)
	}
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	runAnthropicLoop(ctx, model, apiKey, systemPrompt, history, allToolCalls, finalText)
}
