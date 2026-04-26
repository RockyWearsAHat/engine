package ai

import "testing"

// ── ClassifyRequest ───────────────────────────────────────────────────────────

func TestClassifyRequest_EmptyMessage_SimpleQA(t *testing.T) {
	if got := ClassifyRequest("", 0); got != RequestSimpleQA {
		t.Errorf("expected SimpleQA, got %v", got)
	}
}

func TestClassifyRequest_PureQuestion_SimpleQA(t *testing.T) {
	cases := []string{
		"what does this function do?",
		"explain the architecture",
		"how does TF-IDF scoring work here?",
		"why is this parameter optional?",
	}
	for _, msg := range cases {
		if got := ClassifyRequest(msg, 0); got != RequestSimpleQA {
			t.Errorf("expected SimpleQA for %q, got %v", msg, got)
		}
	}
}

func TestClassifyRequest_WorkflowKeywords_Workflow(t *testing.T) {
	cases := []string{
		"implement the classifier",
		"fix the broken test",
		"refactor the auth module",
		"create a new handler",
		"add unit tests for this file",
		"write the completion gate",
	}
	for _, msg := range cases {
		if got := ClassifyRequest(msg, 0); got != RequestWorkflow {
			t.Errorf("expected Workflow for %q, got %v", msg, got)
		}
	}
}

func TestClassifyRequest_ToolOperation_ToolOperation(t *testing.T) {
	cases := []string{
		"read file context.go",
		"show me the current errors",
		"run test for handler",
		"list files in ai/",
	}
	for _, msg := range cases {
		if got := ClassifyRequest(msg, 0); got != RequestToolOperation {
			t.Errorf("expected ToolOperation for %q, got %v", msg, got)
		}
	}
}

func TestClassifyRequest_WorkflowNotFalsePositive(t *testing.T) {
	// "latest" should not trigger "test" word boundary match
	msg := "what is the latest version?"
	if got := ClassifyRequest(msg, 0); got != RequestSimpleQA {
		t.Errorf("expected SimpleQA for %q (false-positive check), got %v", msg, got)
	}
}

// ── containsWord ─────────────────────────────────────────────────────────────

func TestContainsWord_ExactMatch(t *testing.T) {
	if !containsWord("fix the bug", "fix") {
		t.Error("expected match for 'fix' in 'fix the bug'")
	}
}

func TestContainsWord_WordBoundaryLeft(t *testing.T) {
	if containsWord("prefix test", "fix") {
		t.Error("expected no match for 'fix' inside 'prefix'")
	}
}

func TestContainsWord_WordBoundaryRight(t *testing.T) {
	if containsWord("testing is important", "test") {
		t.Error("expected no match for 'test' inside 'testing'")
	}
}

func TestContainsWord_AtStart(t *testing.T) {
	if !containsWord("test the handler", "test") {
		t.Error("expected match for 'test' at start of string")
	}
}

func TestContainsWord_AtEnd(t *testing.T) {
	if !containsWord("run the test", "test") {
		t.Error("expected match for 'test' at end of string")
	}
}

// ── RequestClassString ────────────────────────────────────────────────────────

func TestRequestClassString_AllValues(t *testing.T) {
	cases := map[RequestClass]string{
		RequestSimpleQA:     "simple_qa",
		RequestToolOperation: "tool_operation",
		RequestWorkflow:     "workflow",
	}
	for rc, want := range cases {
		if got := RequestClassString(rc); got != want {
			t.Errorf("RequestClassString(%d) = %q, want %q", rc, got, want)
		}
	}
}

func TestRequestClassString_Unknown(t *testing.T) {
	if got := RequestClassString(RequestClass(99)); got != "unknown" {
		t.Errorf("expected 'unknown' for unknown class, got %q", got)
	}
}

// TestContainsWord_MultipleOccurrences_NoneAtBoundary covers the `return false`
// at the bottom of containsWord when every occurrence fails the boundary check and
// strings.Index finds no further occurrence (next<0 → break → return false).
func TestContainsWord_MultipleOccurrences_NoneAtBoundary(t *testing.T) {
	// "test" appears in "testable" and "retesting" but never as a whole word.
	if containsWord("testable retesting", "test") {
		t.Error("expected false: 'test' never at a word boundary in 'testable retesting'")
	}
}
