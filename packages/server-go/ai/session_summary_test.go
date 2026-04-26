package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/engine/server/db"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func initSessionSummaryTestDB(t *testing.T, projectPath string) {
	t.Helper()
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
}

// ── BuildInitialSessionSummary ────────────────────────────────────────────────

func TestBuildInitialSessionSummary_WithGoalFile_ContainsProjectContext(t *testing.T) {
	dir := t.TempDir()
	initSessionSummaryTestDB(t, dir)
	writeTestFile(t, dir, "PROJECT_GOAL.md", "Build an AI-native editor.")
	result := BuildInitialSessionSummary(dir)
	if result == "" {
		t.Fatal("expected non-empty summary when PROJECT_GOAL.md exists")
	}
	if !strings.Contains(result, "Project context") {
		t.Errorf("expected 'Project context' prefix, got %q", result)
	}
}

func TestBuildInitialSessionSummary_NoGoalFile_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	initSessionSummaryTestDB(t, dir)
	result := BuildInitialSessionSummary(dir)
	if result != "" {
		t.Errorf("expected empty when no project files, got %q", result)
	}
}

// ── normalizeSummaryText ──────────────────────────────────────────────────────

func TestNormalizeSummaryText_StripsMarkdownAndBlankLines(t *testing.T) {
	input := "# Heading\n\n* bullet\n- dash\n\nNormal text"
	result := normalizeSummaryText(input)
	if strings.Contains(result, "#") {
		t.Errorf("expected # stripped, got %q", result)
	}
	if strings.Contains(result, "*") {
		t.Errorf("expected * stripped, got %q", result)
	}
	if strings.Contains(result, "Normal text") == false {
		t.Errorf("expected plain text preserved, got %q", result)
	}
}

func TestNormalizeSummaryText_CollapsesWhitespace(t *testing.T) {
	input := "hello    world\n\t\textra"
	result := normalizeSummaryText(input)
	if strings.Contains(result, "  ") {
		t.Errorf("expected whitespace collapsed, got %q", result)
	}
}

func TestNormalizeSummaryText_NormalizesWindowsLineEndings(t *testing.T) {
	input := "line one\r\nline two"
	result := normalizeSummaryText(input)
	if strings.Contains(result, "\r") {
		t.Errorf("expected \\r removed, got %q", result)
	}
}

func TestNormalizeSummaryText_SkipsDashSeparators(t *testing.T) {
	input := "---\nActual content"
	result := normalizeSummaryText(input)
	if strings.Contains(result, "---") {
		t.Errorf("expected --- line dropped, got %q", result)
	}
	if !strings.Contains(result, "Actual content") {
		t.Errorf("expected content preserved, got %q", result)
	}
}

// ── truncateSummary ───────────────────────────────────────────────────────────

func TestTruncateSummary_BelowMax_ReturnsAsIs(t *testing.T) {
	result := truncateSummary("hello", 100)
	if result != "hello" {
		t.Errorf("expected unmodified, got %q", result)
	}
}

func TestTruncateSummary_ExceedsMax_Truncates(t *testing.T) {
	result := truncateSummary("hello world", 8)
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected ellipsis, got %q", result)
	}
	if len(result) > 8 {
		t.Errorf("expected truncated length <= 8, got %d", len(result))
	}
}

func TestTruncateSummary_MaxZero_ReturnsAsIs(t *testing.T) {
	result := truncateSummary("hello", 0)
	if result != "hello" {
		t.Errorf("expected no truncation for max=0, got %q", result)
	}
}

func TestTruncateSummary_MaxThree_NoEllipsis(t *testing.T) {
	result := truncateSummary("hello world", 3)
	if len(result) > 3 {
		t.Errorf("expected at most 3 chars, got %d: %q", len(result), result)
	}
}

// ── uniqToolNames ─────────────────────────────────────────────────────────────

func TestUniqToolNames_EmptySlice(t *testing.T) {
	result := uniqToolNames(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestUniqToolNames_Deduplicates(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file"},
		{Name: "write_file"},
		{Name: "read_file"},
	}
	result := uniqToolNames(calls)
	if len(result) != 2 {
		t.Errorf("expected 2 unique names, got %v", result)
	}
}

func TestUniqToolNames_SkipsEmpty(t *testing.T) {
	calls := []ToolCall{{Name: ""}, {Name: "read_file"}}
	result := uniqToolNames(calls)
	if len(result) != 1 || result[0] != "read_file" {
		t.Errorf("expected only read_file, got %v", result)
	}
}

func TestUniqToolNames_LimitsSixTools(t *testing.T) {
	calls := make([]ToolCall, 10)
	for i := range calls {
		calls[i] = ToolCall{Name: string(rune('a' + i))}
	}
	result := uniqToolNames(calls)
	if len(result) != 6 {
		t.Errorf("expected cap at 6, got %d", len(result))
	}
}

// ── BuildUpdatedSessionSummary ────────────────────────────────────────────────

func TestBuildUpdatedSessionSummary_BuildsFromParts(t *testing.T) {
	result := BuildUpdatedSessionSummary(
		"previous summary",
		"user asked about testing",
		"assistant said run go test ./...",
		[]ToolCall{{Name: "shell"}},
	)
	if result == "" {
		t.Error("expected non-empty summary")
	}
	// New format: factual sections without prescriptive loop instructions.
	if !strings.Contains(result, "Focus") {
		t.Errorf("expected 'Focus' section, got %q", result)
	}
	if !strings.Contains(result, "Outcome") {
		t.Errorf("expected 'Outcome' section, got %q", result)
	}
	if !strings.Contains(result, "Tools used") {
		t.Errorf("expected 'Tools used' section, got %q", result)
	}
	if !strings.Contains(result, "Validation") {
		t.Errorf("expected 'Validation' section, got %q", result)
	}
}

func TestBuildUpdatedSessionSummary_UsePreviousWhenUserMessageEmpty(t *testing.T) {
	result := BuildUpdatedSessionSummary("carrying context", "", "assistant response", nil)
	if !strings.Contains(result, "carrying context") {
		t.Errorf("expected previous summary carried in Focus, got %q", result)
	}
}

func TestBuildUpdatedSessionSummary_DetectsFailingValidation(t *testing.T) {
	result := BuildUpdatedSessionSummary(
		"previous",
		"",
		"tests failed with runtime error",
		[]ToolCall{{Name: "shell", IsError: true, Result: "go test failed"}},
	)
	if !strings.Contains(result, "failing checks detected") {
		t.Errorf("expected failing validation marker, got %q", result)
	}
	if !strings.Contains(result, "revision required") {
		t.Errorf("expected revision-required guidance, got %q", result)
	}
}

func TestValidationStatus_PassingAndPending(t *testing.T) {
	passing := validationStatus("all tests passed and verified", []ToolCall{{Name: "shell", Result: "ok"}})
	if !strings.Contains(passing, "passing") {
		t.Errorf("expected passing validation status, got %q", passing)
	}

	pending := validationStatus("implemented change", nil)
	if !strings.Contains(pending, "pending") {
		t.Errorf("expected pending validation status, got %q", pending)
	}

	executed := validationStatus("ran checks", []ToolCall{{Name: "test.run", Result: "done"}})
	if !strings.Contains(executed, "awaiting") {
		t.Errorf("expected awaiting validation status, got %q", executed)
	}
}

func TestContainsAnyKeyword_EmptyInput(t *testing.T) {
	if containsAnyKeyword("", []string{"fail"}) {
		t.Error("expected false for empty text")
	}
}

func TestHasToolErrors_KeywordResult(t *testing.T) {
	if !hasToolErrors([]ToolCall{{Name: "shell", Result: "panic: something broke"}}) {
		t.Error("expected keyword-based tool error detection")
	}
	if hasToolErrors([]ToolCall{{Name: "shell", Result: "all good"}}) {
		t.Error("expected no tool errors for clean result")
	}
}

func TestWeakPointsSummary_DefaultAndAmbiguous(t *testing.T) {
	defaultWeak := weakPointsSummary("clear request", "completed successfully", nil)
	if !strings.Contains(defaultWeak, "none currently detected") {
		t.Errorf("expected no weak points summary, got %q", defaultWeak)
	}

	ambiguous := weakPointsSummary("maybe do either option", "", []ToolCall{{Name: "read_file"}})
	if !strings.Contains(ambiguous, "ambiguous user direction") {
		t.Errorf("expected ambiguous-direction weak point, got %q", ambiguous)
	}
	if !strings.Contains(ambiguous, "validation command not observed") {
		t.Errorf("expected validation-gap weak point, got %q", ambiguous)
	}
}

func TestWeakPointsSummary_ApprovalAndBlockedKeywords(t *testing.T) {
	approval := weakPointsSummary("do it", "waiting for approval from the team", nil)
	if !strings.Contains(approval, "approval-gated") {
		t.Errorf("expected approval-gated weak point, got %q", approval)
	}

	blocked := weakPointsSummary("do it", "cannot proceed without more info", nil)
	if !strings.Contains(blocked, "missing information") {
		t.Errorf("expected missing-information weak point, got %q", blocked)
	}
}

func TestWeakPointsSummary_ToolErrors(t *testing.T) {
	erring := weakPointsSummary("do it", "all good", []ToolCall{{Name: "shell", IsError: true}})
	if !strings.Contains(erring, "recent tool or test failures") {
		t.Errorf("expected tool-failure weak point, got %q", erring)
	}
}

func TestNextAutonomousStep_Branches(t *testing.T) {
	if step := nextAutonomousStep("failing checks detected; revision required", "none"); !strings.Contains(step, "diagnose") {
		t.Errorf("expected failure branch step, got %q", step)
	}
	if step := nextAutonomousStep("pending verification", "approval-gated action blocked autonomous progress"); !strings.Contains(step, "approval") {
		t.Errorf("expected approval branch step, got %q", step)
	}
	if step := nextAutonomousStep("pending verification", "missing information or constraint is blocking completion"); !strings.Contains(step, "best safe assumption") {
		t.Errorf("expected missing-info branch step, got %q", step)
	}
	if step := nextAutonomousStep("pending verification", "none currently detected"); !strings.Contains(step, "build/test") {
		t.Errorf("expected pending-validation branch step, got %q", step)
	}
	if step := nextAutonomousStep("latest checks passing", "none currently detected"); !strings.Contains(step, "continue implementation") {
		t.Errorf("expected default branch step, got %q", step)
	}
}

func TestBuildUpdatedSessionSummary_AllEmpty_ReturnsEmpty(t *testing.T) {
	result := BuildUpdatedSessionSummary("", "", "", nil)
	if result != "" {
		t.Errorf("expected empty result for all-empty inputs, got %q", result)
	}
}

// ── EnsureProjectDirection ────────────────────────────────────────────────────

func TestEnsureProjectDirection_EmptyPath_ReturnsEmpty(t *testing.T) {
	result := EnsureProjectDirection("")
	if result != "" {
		t.Errorf("expected empty for empty path, got %q", result)
	}
}

func TestBuildInitialProjectDirection_WithProjectGoal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "PROJECT_GOAL.md", "Build an AI-native code editor with full agent control.")
	result := BuildInitialProjectDirection(dir)
	if !strings.Contains(result, "AI-native") {
		t.Errorf("expected project goal in direction, got %q", result)
	}
}

func TestBuildInitialProjectDirection_MissingFiles_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	result := BuildInitialProjectDirection(dir)
	if result != "" {
		t.Errorf("expected empty for missing files, got %q", result)
	}
}
