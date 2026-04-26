package ai

import "strings"

// RequestClass classifies a user message to determine which execution path to take.
// Evaluated before any role orchestration to route cheap Q&A away from the full
// planning machinery.
type RequestClass int

const (
	// RequestSimpleQA requires a single LLM call — no planning, no tool loop.
	RequestSimpleQA RequestClass = iota

	// RequestToolOperation is a direct tool invocation — no planning needed.
	RequestToolOperation

	// RequestWorkflow requires the full planner → executor → verifier loop.
	RequestWorkflow
)

// workflowKeywords are strong signals that a structural change is intended.
var workflowKeywords = []string{
	"build", "implement", "create", "add", "write", "fix", "refactor", "update",
	"change", "remove", "delete", "move", "rename", "migrate", "setup", "configure",
	"install", "generate", "scaffold", "deploy", "upgrade", "integrate", "extract",
	"replace", "rewrite", "redesign", "restructure", "test",
}

// toolOperationKeywords are direct tool-invocation signals.
var toolOperationKeywords = []string{
	"read file", "read the file", "show me", "open file", "run test", "run the test",
	"run command", "execute", "list files", "list directory",
}

// ClassifyRequest decides the execution class for a user message.
// Heuristic-only: no LLM call, O(n) in message length.
func ClassifyRequest(userMessage string, conversationDepth int) RequestClass {
	lower := strings.ToLower(strings.TrimSpace(userMessage))
	if lower == "" {
		return RequestSimpleQA
	}

	// Direct tool operations — act immediately, no planning.
	for _, kw := range toolOperationKeywords {
		if strings.Contains(lower, kw) {
			return RequestToolOperation
		}
	}

	// Workflow signals — requires plan → executor loop.
	for _, kw := range workflowKeywords {
		if containsWord(lower, kw) {
			return RequestWorkflow
		}
	}

	// Short messages without task keywords are Q&A.
	// Long messages with no workflow signals may still be explanations — keep as Q&A.
	return RequestSimpleQA
}

// containsWord returns true when word appears as a standalone token in s.
// Prevents "test" matching "latest" or "protest".
func containsWord(s, word string) bool {
	if !strings.Contains(s, word) {
		return false
	}
	idx := strings.Index(s, word)
	for idx >= 0 {
		before := idx == 0 || !isAlpha(rune(s[idx-1]))
		after := idx+len(word) >= len(s) || !isAlpha(rune(s[idx+len(word)]))
		if before && after {
			return true
		}
		next := strings.Index(s[idx+1:], word)
		if next < 0 {
			break
		}
		idx = idx + 1 + next
	}
	return false
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// RequestClassString returns a human-readable label for a RequestClass.
func RequestClassString(rc RequestClass) string {
	switch rc {
	case RequestSimpleQA:
		return "simple_qa"
	case RequestToolOperation:
		return "tool_operation"
	case RequestWorkflow:
		return "workflow"
	default:
		return "unknown"
	}
}
