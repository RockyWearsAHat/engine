package ai

import (
	"strings"
	"testing"
)

// ── FormatFinalResponse ───────────────────────────────────────────────────────

func TestFormatFinalResponse_EmptyInput(t *testing.T) {
	if got := FormatFinalResponse("", RequestSimpleQA); got != "" {
		t.Errorf("expected empty output for empty input, got %q", got)
	}
}

func TestFormatFinalResponse_CleanResponse_Unchanged(t *testing.T) {
	input := "Here is the answer to your question."
	got := FormatFinalResponse(input, RequestSimpleQA)
	if got != input {
		t.Errorf("expected clean response unchanged, got %q", got)
	}
}

func TestFormatFinalResponse_StripsPanicLines(t *testing.T) {
	input := "panic: runtime error: index out of range\n\nSome helpful text."
	got := FormatFinalResponse(input, RequestWorkflow)
	if strings.Contains(got, "panic: runtime error") {
		t.Errorf("expected panic line stripped, got %q", got)
	}
	if !strings.Contains(got, "helpful text") {
		t.Errorf("expected normal content preserved, got %q", got)
	}
}

func TestFormatFinalResponse_StripsGoroutineLines(t *testing.T) {
	input := "goroutine 1 [running]:\nmain.main()\n\nActual response here."
	got := FormatFinalResponse(input, RequestWorkflow)
	if strings.Contains(got, "goroutine 1") {
		t.Errorf("expected goroutine line stripped, got %q", got)
	}
}

func TestFormatFinalResponse_PreservesCodeBlock_WithPanicInside(t *testing.T) {
	input := "Here is an example:\n```\npanic: this is intentional\n```\nDone."
	got := FormatFinalResponse(input, RequestWorkflow)
	if !strings.Contains(got, "panic: this is intentional") {
		t.Errorf("expected panic inside code block preserved, got %q", got)
	}
}

func TestFormatFinalResponse_SimpleQA_LongResponseGetsNote(t *testing.T) {
	// Generate a response > 1000 words.
	words := strings.Repeat("word ", 1001)
	got := FormatFinalResponse(words, RequestSimpleQA)
	if !strings.Contains(got, "response length unusual") {
		t.Errorf("expected length notice for long Q&A response, got snippet: %q", got[:min(len(got), 200)])
	}
}

func TestFormatFinalResponse_Workflow_LongResponseNoNote(t *testing.T) {
	words := strings.Repeat("word ", 1001)
	got := FormatFinalResponse(words, RequestWorkflow)
	if strings.Contains(got, "response length unusual") {
		t.Errorf("expected no length notice for workflow response, got %q", got[len(got)-100:])
	}
}

// ── isPanicLine ───────────────────────────────────────────────────────────────

func TestIsPanicLine_PanicPrefix(t *testing.T) {
	if !isPanicLine("panic: nil pointer dereference") {
		t.Error("expected isPanicLine true for panic prefix")
	}
}

func TestIsPanicLine_StackFrame(t *testing.T) {
	if !isPanicLine("\t/Users/dev/main.go:42 +0x1c4") {
		t.Error("expected isPanicLine true for stack frame line")
	}
}

func TestIsPanicLine_NormalLine(t *testing.T) {
	if isPanicLine("This is normal text.") {
		t.Error("expected isPanicLine false for normal line")
	}
}

// ── isGoroutineLine ───────────────────────────────────────────────────────────

func TestIsGoroutineLine_GoroutineHeader(t *testing.T) {
	if !isGoroutineLine("goroutine 1 [running]:") {
		t.Error("expected isGoroutineLine true for goroutine header")
	}
}

func TestIsGoroutineLine_NormalLine(t *testing.T) {
	if isGoroutineLine("this is not a goroutine line") {
		t.Error("expected isGoroutineLine false for normal line")
	}
}

// ── wordCount ─────────────────────────────────────────────────────────────────

func TestWordCount_AccurateCount(t *testing.T) {
	if got := wordCount("one two three"); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestWordCount_EmptyString(t *testing.T) {
	if got := wordCount(""); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
