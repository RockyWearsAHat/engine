package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/engine/server/db"
)

const (
	projectGuideMaxChars   = 900
	architectureMaxChars   = 700
	sessionSummaryMaxChars = 1400
)

var whitespacePattern = regexp.MustCompile(`\s+`)

func containsAnyKeyword(text string, keywords []string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func hasToolWithName(toolCalls []ToolCall, names ...string) bool {
	if len(toolCalls) == 0 {
		return false
	}
	lookup := map[string]bool{}
	for _, name := range names {
		lookup[strings.TrimSpace(name)] = true
	}
	for _, tc := range toolCalls {
		if lookup[strings.TrimSpace(tc.Name)] {
			return true
		}
	}
	return false
}

func hasToolErrors(toolCalls []ToolCall) bool {
	for _, tc := range toolCalls {
		if tc.IsError {
			return true
		}
		if containsAnyKeyword(normalizeSummaryText(fmt.Sprint(tc.Result)), []string{"error", "failed", "panic", "exception"}) {
			return true
		}
	}
	return false
}

func validationStatus(assistantText string, toolCalls []ToolCall) string {
	if hasToolErrors(toolCalls) || containsAnyKeyword(assistantText, []string{"failed", "error", "panic", "unable", "blocked"}) {
		return "failing checks detected; revision required"
	}
	if containsAnyKeyword(assistantText, []string{"tests passed", "pass", "verified", "green", "success"}) {
		return "latest checks passing"
	}
	if hasToolWithName(toolCalls, "shell", "test.run") {
		return "checks executed; awaiting full verification"
	}
	return "pending verification"
}

func weakPointsSummary(userMessage, assistantText string, toolCalls []ToolCall) string {
	weakPoints := make([]string, 0, 4)
	add := func(point string) {
		weakPoints = append(weakPoints, point)
	}

	if containsAnyKeyword(assistantText, []string{"approval", "permission", "not allowed"}) {
		add("approval-gated action blocked autonomous progress")
	}
	if containsAnyKeyword(assistantText, []string{"blocked", "cannot proceed", "need more info", "missing requirement"}) {
		add("missing information or constraint is blocking completion")
	}
	if hasToolErrors(toolCalls) {
		add("recent tool or test failures require targeted replan")
	}
	if len(toolCalls) > 0 && !hasToolWithName(toolCalls, "shell", "test.run") {
		add("validation command not observed in recent tool activity")
	}
	if containsAnyKeyword(userMessage, []string{"maybe", "not sure", "either", "whatever"}) {
		add("ambiguous user direction; apply safest default and confirm only if blocked")
	}

	if len(weakPoints) == 0 {
		return "none currently detected"
	}
	return strings.Join(weakPoints, "; ")
}

func nextAutonomousStep(validation, weakPoints string) string {
	if strings.Contains(validation, "failing checks") {
		return "diagnose first failing check, patch root cause, and rerun focused validation"
	}
	if strings.Contains(weakPoints, "approval-gated") {
		return "request required approval context, then resume from blocked step"
	}
	if strings.Contains(weakPoints, "missing information") {
		return "derive best safe assumption from context and continue; ask user only for missing required facts"
	}
	if strings.Contains(validation, "pending") {
		return "run the most relevant build/test command for the current change before moving on"
	}
	return "continue implementation against remaining plan items and keep validate-revise loop active"
}

func readWorkspaceDocSnippet(projectPath string, parts ...string) string {
	pathParts := append([]string{projectPath}, parts...)
	path := filepath.Join(pathParts...)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return normalizeSummaryText(string(data))
}

func normalizeSummaryText(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	compact := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#*- ")
		if line == "" || strings.HasPrefix(line, "---") {
			continue
		}
		compact = append(compact, line)
	}
	return whitespacePattern.ReplaceAllString(strings.Join(compact, " "), " ")
}

func truncateSummary(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}

func uniqToolNames(toolCalls []ToolCall) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		name := strings.TrimSpace(tc.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 6 {
			break
		}
	}
	return names
}

func BuildInitialProjectDirection(projectPath string) string {
	projectGoal := truncateSummary(readWorkspaceDocSnippet(projectPath, "PROJECT_GOAL.md"), projectGuideMaxChars)
	architecture := truncateSummary(readWorkspaceDocSnippet(projectPath, ".github", "references", "architecture.md"), architectureMaxChars)

	sections := make([]string, 0, 2)
	if projectGoal != "" {
		sections = append(sections, "Project goal: "+projectGoal)
	}
	if architecture != "" {
		sections = append(sections, "Architecture direction: "+architecture)
	}
	return strings.Join(sections, "\n")
}

func EnsureProjectDirection(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	if existing, err := db.GetProjectDirection(projectPath); err == nil && strings.TrimSpace(existing) != "" {
		return existing
	}
	direction := BuildInitialProjectDirection(projectPath)
	if strings.TrimSpace(direction) != "" {
		db.UpsertProjectDirection(projectPath, direction) //nolint:errcheck
	}
	return direction
}

func BuildInitialSessionSummary(projectPath string) string {
	direction := truncateSummary(BuildWorkspacePromptContext(projectPath), 300)
	if direction == "" {
		return ""
	}
	return truncateSummary("Project context: "+direction, sessionSummaryMaxChars)
}

func BuildWorkspacePromptContext(projectPath string) string {
	return EnsureProjectDirection(projectPath)
}

func BuildUpdatedSessionSummary(previousSummary, userMessage, assistantText string, toolCalls []ToolCall) string {
	if strings.TrimSpace(previousSummary) == "" && strings.TrimSpace(userMessage) == "" && strings.TrimSpace(assistantText) == "" && len(toolCalls) == 0 {
		return ""
	}

	sections := make([]string, 0, 5)

	// What the user asked this turn.
	focus := truncateSummary(normalizeSummaryText(userMessage), 280)
	if focus != "" {
		sections = append(sections, "Focus: "+focus)
	} else if carry := truncateSummary(normalizeSummaryText(previousSummary), 280); carry != "" {
		sections = append(sections, "Focus: "+carry)
	}

	// Factual outcome of what the assistant did.
	if outcome := truncateSummary(normalizeSummaryText(assistantText), 420); outcome != "" {
		sections = append(sections, "Outcome: "+outcome)
	}

	// Which tools were exercised (backend tracking — not shown to the agent).
	if toolNames := uniqToolNames(toolCalls); len(toolNames) > 0 {
		sections = append(sections, "Tools used: "+strings.Join(toolNames, ", "))
	}

	// Orchestrator state helpers stored in DB for routing decisions.
	if vs := validationStatus(assistantText, toolCalls); vs != "" {
		sections = append(sections, "Validation: "+vs)
	}
	if wp := weakPointsSummary(userMessage, assistantText, toolCalls); wp != "" {
		sections = append(sections, "Blockers: "+wp)
	}

	return truncateSummary(strings.Join(sections, "\n"), sessionSummaryMaxChars)
}
