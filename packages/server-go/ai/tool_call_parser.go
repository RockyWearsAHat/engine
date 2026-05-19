package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// parsedToolCall is one tool call extracted from raw assistant text by a fallback parser.
// Used when a model (e.g. qwen3.x via Ollama) emits tool calls as XML/JSON inside the content
// stream instead of using the OpenAI native `tool_calls` field. The OpenAI-compat loop converts
// these into synthetic tool_calls so execution proceeds normally.
type parsedToolCall struct {
	Name      string
	Arguments string // raw JSON string, exactly as the loop's native path expects
}

// hermesToolCallBlock matches Qwen/Hermes XML wrappers. Qwen3, Hermes-2-Pro, and several
// other open models emit tool calls in this form when their template falls back from the
// native function-calling header:
//
//	<tool_call>
//	{"name": "read_file", "arguments": {"path": "main.go"}}
//	</tool_call>
//
// Variant tags (case-insensitive) we accept: <tool_call>, <tool-call>, <function_call>,
// <function>. The body must be valid JSON containing at minimum a "name" field.
// Go's regexp engine (RE2) has no backreferences, so opening and closing tags are matched
// by independent alternations. A cross-tag pairing (e.g. `<tool_call>...</function>`) is
// permitted by this regex but never appears in real model output; the JSON body validation
// below still gates whether the match is honoured.
var hermesToolCallBlock = regexp.MustCompile(`(?is)<\s*(?:tool_call|tool-call|function_call|function)\s*>(.*?)<\s*/\s*(?:tool_call|tool-call|function_call|function)\s*>`)

// pipeWrappedToolCall matches `<|tool_call|>{...}<|/tool_call|>` (and related sentinel
// variants used by some Mistral/Llama tool templates). Same JSON body requirement as above.
var pipeWrappedToolCall = regexp.MustCompile(`(?is)<\|\s*tool_call\s*\|>(.*?)<\|\s*/?\s*tool_call\s*\|>`)

// fencedToolCallJSON matches a fenced JSON code block whose payload is a tool-call object.
// Only triggered if no XML-wrapped calls were found, so well-formed responses that happen to
// include JSON examples in fences don't get mis-executed.
var fencedToolCallJSON = regexp.MustCompile("(?is)```\\s*(?:json|tool_call|tool|function)?\\s*\\n(.*?)\\n```")

// parseToolCallsFromText returns every tool call it can recognise in raw, in order.
// Returns nil when the text does not look like a tool-call response so callers can keep
// streaming behaviour unchanged. Validation is strict: a candidate must (a) parse as JSON
// and (b) contain a string "name" field — otherwise it is discarded silently.
//
// This is a fallback for models that ignore the OpenAI `tools` schema and emit calls as
// content. We never invoke it when the provider already returned native tool_calls.
func parseToolCallsFromText(raw string) []parsedToolCall {
	if raw == "" {
		return nil
	}

	var out []parsedToolCall

	// 1) Hermes/Qwen-style XML wrappers — strictest format, try first.
	for _, m := range hermesToolCallBlock.FindAllStringSubmatch(raw, -1) {
		if call, ok := decodeToolCallJSON(m[1]); ok {
			out = append(out, call)
		}
	}
	// 2) Pipe-sentinel wrappers used by some Mistral templates.
	for _, m := range pipeWrappedToolCall.FindAllStringSubmatch(raw, -1) {
		if call, ok := decodeToolCallJSON(m[1]); ok {
			out = append(out, call)
		}
	}

	if len(out) > 0 {
		return out
	}

	// 3) Fenced JSON — only used as last resort. We require the JSON to look like a
	// tool-call ({"name": "...", "arguments": ...}) to avoid grabbing arbitrary JSON
	// snippets the model may have produced as examples or output.
	for _, m := range fencedToolCallJSON.FindAllStringSubmatch(raw, -1) {
		body := strings.TrimSpace(m[1])
		if !looksLikeToolCallJSON(body) {
			continue
		}
		if call, ok := decodeToolCallJSON(body); ok {
			out = append(out, call)
		}
	}
	return out
}

// looksLikeToolCallJSON gates the fenced-JSON path: only objects whose top-level keys
// include "name" (and ideally "arguments" or "parameters") qualify. This keeps fenced
// example JSON in the model's prose from being mis-executed.
func looksLikeToolCallJSON(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return false
	}
	_, hasName := probe["name"]
	if !hasName {
		return false
	}
	_, hasArgs := probe["arguments"]
	_, hasParams := probe["parameters"]
	return hasArgs || hasParams
}

// decodeToolCallJSON parses one tool-call JSON body. Accepts both `arguments` and
// `parameters` for the argument map (different model families use different keys).
// Returns ok=false when the body is not parseable or has no name.
func decodeToolCallJSON(body string) (parsedToolCall, bool) {
	body = strings.TrimSpace(body)
	// Some models wrap the JSON in their own code fences; strip a leading/trailing fence.
	body = strings.TrimPrefix(body, "```json")
	body = strings.TrimPrefix(body, "```")
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)

	var obj struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return parsedToolCall{}, false
	}
	if strings.TrimSpace(obj.Name) == "" {
		return parsedToolCall{}, false
	}

	args := obj.Arguments
	if len(args) == 0 {
		args = obj.Parameters
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	// Some models emit arguments as a JSON-encoded string (`"arguments": "{\"x\":1}"`).
	// Unwrap one layer so the loop's downstream json.Unmarshal succeeds.
	if len(args) > 0 && args[0] == '"' {
		var unwrapped string
		if err := json.Unmarshal(args, &unwrapped); err == nil {
			args = json.RawMessage(unwrapped)
		}
	}
	return parsedToolCall{Name: obj.Name, Arguments: string(args)}, true
}

// stripToolCallMarkup removes the XML/sentinel wrappers from raw text so the user-visible
// portion of an assistant message does not contain the tool-call wire format. The loop
// has already executed the calls themselves; this only cleans up the displayed text.
func stripToolCallMarkup(raw string) string {
	if raw == "" {
		return raw
	}
	out := hermesToolCallBlock.ReplaceAllString(raw, "")
	out = pipeWrappedToolCall.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}

// synthesizeToolCallID generates a stable-looking call_<name>_<i> id for parsed calls.
// The OpenAI-compat loop requires a non-empty id when echoing the assistant message back,
// and we need ours to be deterministic across the same iteration so the matching tool
// result message links correctly.
func synthesizeToolCallID(name string, index int) string {
	return fmt.Sprintf("call_parsed_%s_%d", name, index)
}
