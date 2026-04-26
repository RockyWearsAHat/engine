package ai

import (
	"strings"
)

// FormatFinalResponse cleans up an LLM response before delivery to the client.
// It strips leaked error noise and enforces length expectations based on request class.
func FormatFinalResponse(raw string, rc RequestClass) string {
	if raw == "" {
		return raw
	}

	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	inCodeBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track code fence state — never strip content inside fences.
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			cleaned = append(cleaned, line)
			continue
		}
		if inCodeBlock {
			cleaned = append(cleaned, line)
			continue
		}

		// Strip raw panic stack traces that leaked outside a code block.
		if isPanicLine(trimmed) {
			continue
		}
		// Strip raw "goroutine N [running]:" lines.
		if isGoroutineLine(trimmed) {
			continue
		}

		cleaned = append(cleaned, line)
	}

	result := strings.TrimSpace(strings.Join(cleaned, "\n"))

	// For simple Q&A, a response exceeding 1000 words is almost certainly
	// an implementer-style dump — append a notice so the user is aware.
	if rc == RequestSimpleQA && wordCount(result) > 1000 {
		result += "\n\n<!-- note: response length unusual for a simple Q&A -->"
	}

	return result
}

// isPanicLine returns true for typical Go panic / stack-trace lines.
func isPanicLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "panic:") {
		return true
	}
	// Stack frame lines: "\t/path/file.go:123 +0x..."
	if strings.HasPrefix(line, "\t") && strings.Contains(line, ".go:") {
		return true
	}
	return false
}

// isGoroutineLine returns true for "goroutine N [running]:" headers.
func isGoroutineLine(line string) bool {
	return strings.HasPrefix(strings.ToLower(line), "goroutine ") &&
		strings.Contains(line, "[")
}

// wordCount returns an approximate word count for a string.
func wordCount(s string) int {
	return len(strings.Fields(s))
}
