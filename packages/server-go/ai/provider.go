package ai

import (
	"os"
	"strings"
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

// newProvider returns the Provider for the given backend name.
// Supported values: "ollama", "openai", "anthropic".
// Any unrecognised name falls back to "anthropic".
func newProvider(name string) Provider {
	switch name {
	case "openai":
		return &openAIProvider{}
	case "ollama":
		return &ollamaProvider{}
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
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	runOpenAICompatibleLoop(
		ctx, "ollama", model,
		ollamaChatCompletionsURL(baseURL),
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
	apiKey := os.Getenv("OPENAI_API_KEY")
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
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	runAnthropicLoop(ctx, model, apiKey, systemPrompt, history, allToolCalls, finalText)
}
