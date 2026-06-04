package github

import (
	"strings"
	"testing"
)

// TestPhaseEmoji_AllPaths verifies emoji mapping for all phase states.
func TestPhaseEmoji_AllPaths(t *testing.T) {
	cases := map[string]string{
		"plan":     "📋",
		"execute":  "🛠️",
		"review":   "🔍",
		"validate": "🧪",
		"done":     "✅",
		"failure":  "❌",
		"error":    "❌",
		"other":    "🔄",
	}
	for in, want := range cases {
		if got := phaseEmoji(in); got != want {
			t.Fatalf("phaseEmoji(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPhaseToProjectStatus_AllPaths verifies project status mapping for all phase states.
func TestPhaseToProjectStatus_AllPaths(t *testing.T) {
	cases := map[string]string{
		"done":    "Done",
		"success": "Done",
		"failure": "Blocked",
		"error":   "Blocked",
		"blocked": "Blocked",
		"plan":    "In Progress",
	}
	for in, want := range cases {
		if got := phaseToProjectStatus(in); got != want {
			t.Fatalf("phaseToProjectStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProgressAndBlockedBodies_TrimmingPaths verifies comment body text trimming and ellipsis formatting.
func TestProgressAndBlockedBodies_TrimmingPaths(t *testing.T) {
	detail := strings.Repeat("x", 260)
	body := progressCommentBody("execute", detail, 0, 0)
	if strings.Contains(body, "Progress:") {
		t.Fatal("did not expect progress line when total == 0")
	}
	if !strings.Contains(body, "…") {
		t.Fatal("expected ellipsis for long detail")
	}

	reason := strings.Repeat("r", 500)
	blocked := blockedCommentBody(reason)
	if !strings.Contains(blocked, "…") {
		t.Fatal("expected blocked reason to be truncated")
	}
}

func TestCompletionBody_OptionalLines(t *testing.T) {
	without := completionCommentBody(0, "")
	if strings.Contains(without, "Steps completed") || strings.Contains(without, "Pull request") {
		t.Fatalf("unexpected optional lines in completion body: %q", without)
	}

	withAll := completionCommentBody(3, "https://example/pr/1")
	if !strings.Contains(withAll, "Steps completed") || !strings.Contains(withAll, "Pull request") {
		t.Fatalf("expected optional lines in completion body: %q", withAll)
	}
}
