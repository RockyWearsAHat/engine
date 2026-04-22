package ai

import (
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
	_ = projectPath
	return ""
}

func BuildWorkspacePromptContext(projectPath string) string {
	return EnsureProjectDirection(projectPath)
}

func BuildUpdatedSessionSummary(previousSummary, userMessage, assistantText string, toolCalls []ToolCall) string {
	sections := make([]string, 0, 4)
	if focus := truncateSummary(normalizeSummaryText(userMessage), 280); focus != "" {
		sections = append(sections, "Current focus: "+focus)
	} else if carry := truncateSummary(normalizeSummaryText(previousSummary), 280); carry != "" {
		sections = append(sections, "Current focus: "+carry)
	}
	if outcome := truncateSummary(normalizeSummaryText(assistantText), 420); outcome != "" {
		sections = append(sections, "Latest outcome: "+outcome)
	}
	if toolNames := uniqToolNames(toolCalls); len(toolNames) > 0 {
		sections = append(sections, "Recent tools: "+strings.Join(toolNames, ", "))
	}
	return truncateSummary(strings.Join(sections, "\n"), sessionSummaryMaxChars)
}
