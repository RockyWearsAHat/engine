package ai

import (
	"strings"
	"testing"
)

// ── parseRepairPlan ───────────────────────────────────────────────────────────

func TestParseRepairPlan_NumberedStepsParsed(t *testing.T) {
	planText := "1. Fix the import in handler.go\n2. Update the test expectation\n3. Run lint"
	plan := parseRepairPlan("broken test", planText, 1)
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d: %v", len(plan.Steps), plan.Steps)
	}
	if !strings.Contains(plan.Steps[0].Action, "import") {
		t.Errorf("expected step 1 about import, got %q", plan.Steps[0].Action)
	}
}

func TestParseRepairPlan_FallbackStepWhenNoParsed(t *testing.T) {
	plan := parseRepairPlan("issue", "no steps here, just some text", 1)
	if len(plan.Steps) != 1 {
		t.Fatalf("expected fallback step, got %d steps", len(plan.Steps))
	}
}

func TestParseRepairPlan_PreservesIssueAndAttempt(t *testing.T) {
	plan := parseRepairPlan("the issue", "1. do something", 3)
	if plan.Issue != "the issue" {
		t.Errorf("expected issue preserved, got %q", plan.Issue)
	}
	if plan.AttemptNum != 3 {
		t.Errorf("expected attempt 3, got %d", plan.AttemptNum)
	}
}

func TestParseRepairPlan_ParenthesisStyle(t *testing.T) {
	planText := "1) Extract function\n2) Write test"
	plan := parseRepairPlan("issue", planText, 1)
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps with parenthesis style, got %d", len(plan.Steps))
	}
}

// ── summarizeFirstLines ───────────────────────────────────────────────────────

func TestSummarizeFirstLines_TruncatesCorrectly(t *testing.T) {
	text := "line1\nline2\nline3\nline4"
	got := summarizeFirstLines(text, 2)
	if strings.Contains(got, "line3") {
		t.Errorf("expected only first 2 lines, got %q", got)
	}
	if !strings.Contains(got, "line1") {
		t.Errorf("expected first line in result, got %q", got)
	}
}

func TestSummarizeFirstLines_FewerLinesThanN(t *testing.T) {
	got := summarizeFirstLines("only one line", 5)
	if got != "only one line" {
		t.Errorf("expected full text returned, got %q", got)
	}
}

// ── ExecuteRepairLoop ─────────────────────────────────────────────────────────

func TestExecuteRepairLoop_ResolvesOnFirstAttempt(t *testing.T) {
	ctx := &ChatContext{
		SessionID: "s1",
		SendToClient: func(msgType string, payload any) {},
		OnChunk: func(string, bool) {},
	}

	callCount := 0
	runTest := func() BehavioralResult {
		callCount++
		return BehavioralResult{IssueResolved: true, Evidence: "all good"}
	}

	result := ExecuteRepairLoop(ctx, nil, "test issue", "initial failure", runTest)
	if !result.Resolved {
		t.Error("expected loop to resolve on first attempt")
	}
	if result.AttemptsMade != 1 {
		t.Errorf("expected 1 attempt, got %d", result.AttemptsMade)
	}
}

func TestExecuteRepairLoop_StopsAtMaxAttempts(t *testing.T) {
	ctx := &ChatContext{
		SessionID: "s1",
		SendToClient: func(string, any) {},
		OnChunk: func(string, bool) {},
	}

	runTest := func() BehavioralResult {
		return BehavioralResult{IssueResolved: false, Evidence: "still broken"}
	}

	result := ExecuteRepairLoop(ctx, nil, "issue", "failure", runTest)
	if result.Resolved {
		t.Error("expected loop to not resolve")
	}
	if result.AttemptsMade != maxRepairAttempts {
		t.Errorf("expected %d attempts, got %d", maxRepairAttempts, result.AttemptsMade)
	}
}

func TestExecuteRepairLoop_UpdatesWorkingState(t *testing.T) {
	ctx := &ChatContext{
		SessionID: "s1",
		SendToClient: func(string, any) {},
		OnChunk: func(string, bool) {},
	}

	var persisted *WorkingState
	orig := saveWorkingStateFn
	t.Cleanup(func() { saveWorkingStateFn = orig })
	saveWorkingStateFn = func(_, _ string) error { return nil }

	ws := &WorkingState{CurrentTask: "fix issue"}
	runTest := func() BehavioralResult { return BehavioralResult{IssueResolved: true} }

	ExecuteRepairLoop(ctx, ws, "issue", "evidence", runTest)
	_ = persisted // ws was updated in-place
	if ws.AttemptCount != 1 {
		t.Errorf("expected 1 recorded attempt on WorkingState, got %d", ws.AttemptCount)
	}
}

// ── GenerateRepairPlan (nil context) ─────────────────────────────────────────

func TestGenerateRepairPlan_NilContext_ReturnsFallback(t *testing.T) {
	plan := GenerateRepairPlan(nil, "some issue", "evidence", 1)
	if plan.Issue != "some issue" {
		t.Errorf("expected issue preserved, got %q", plan.Issue)
	}
	if plan.AttemptNum != 1 {
		t.Errorf("expected attempt 1, got %d", plan.AttemptNum)
	}
}

// TestGenerateRepairPlan_MockChat_WritesChunk covers the OnChunk callback body
// (planOutput.WriteString) by injecting a mock Chat that calls the closure.
func TestGenerateRepairPlan_MockChat_WritesChunk(t *testing.T) {
	orig := generateRepairPlanChatFn
	t.Cleanup(func() { generateRepairPlanChatFn = orig })

	// Inject a mock that writes plan steps via OnChunk.
	generateRepairPlanChatFn = func(ctx *ChatContext, _ string) {
		ctx.OnChunk("1. Fix nil pointer in handler.go", false)
		ctx.OnChunk("\n2. Add nil guard in auth.go", true)
	}

	ctx := &ChatContext{
		SessionID:    "test-session",
		SendToClient: func(string, any) {},
		OnChunk:      func(string, bool) {},
	}
	plan := GenerateRepairPlan(ctx, "nil crash", "stack trace", 1)
	if len(plan.Steps) == 0 {
		t.Fatal("expected steps from mock chat output")
	}
	if !strings.Contains(plan.Steps[0].Action, "nil pointer") {
		t.Errorf("expected first step about nil pointer, got %q", plan.Steps[0].Action)
	}
}
