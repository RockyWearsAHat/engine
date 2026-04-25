package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// ── BuildInitialSessionSummary ────────────────────────────────────────────────

func TestBuildInitialSessionSummary_ReturnsEmpty(t *testing.T) {
	result := BuildInitialSessionSummary("/any/path")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
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
	if !strings.Contains(result, "Current focus") {
		t.Errorf("expected 'Current focus' section, got %q", result)
	}
}

func TestBuildUpdatedSessionSummary_UsePreviousWhenUserMessageEmpty(t *testing.T) {
	result := BuildUpdatedSessionSummary("carrying context", "", "assistant response", nil)
	if !strings.Contains(result, "carrying context") {
		t.Errorf("expected previous summary carried over, got %q", result)
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
