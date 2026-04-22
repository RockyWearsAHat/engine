package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

func carryForwardProjectMemory(previousSummary string) string {
	lines := strings.Split(strings.ReplaceAll(previousSummary, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Project goal:"),
			strings.HasPrefix(line, "Architecture direction:"),
			strings.HasPrefix(line, "Project memory:"):
			kept = append(kept, strings.TrimPrefix(line, "Project memory: "))
		}
	}
	if len(kept) == 0 {
		return truncateSummary(normalizeSummaryText(previousSummary), 620)
	}
	return truncateSummary(normalizeSummaryText(strings.Join(kept, " ")), 620)
}

func BuildInitialSessionSummary(projectPath string) string {
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

func BuildWorkspacePromptContext(projectPath string) string {
	return BuildInitialSessionSummary(projectPath)
}

func BuildUpdatedSessionSummary(previousSummary, userMessage, assistantText string, toolCalls []ToolCall) string {
	sections := make([]string, 0, 4)
	if previous := carryForwardProjectMemory(previousSummary); previous != "" {
		sections = append(sections, "Project memory: "+previous)
	}
	if focus := truncateSummary(normalizeSummaryText(userMessage), 280); focus != "" {
		sections = append(sections, "Current focus: "+focus)
	}
	if outcome := truncateSummary(normalizeSummaryText(assistantText), 420); outcome != "" {
		sections = append(sections, "Latest outcome: "+outcome)
	}
	if toolNames := uniqToolNames(toolCalls); len(toolNames) > 0 {
		sections = append(sections, "Recent tools: "+strings.Join(toolNames, ", "))
	}
	return truncateSummary(strings.Join(sections, "\n"), sessionSummaryMaxChars)
}

// CompactMessagesWithLLM calls the LLM to compress a list of dropped conversation
// messages into a dense factual summary block. Falls back to rule-based summary
// if the LLM call fails. This is used when BuildAttentionConversationWindow drops
// messages that previously carried important context.
func CompactMessagesWithLLM(dropped []map[string]string, currentSummary string) string {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" || len(dropped) == 0 {
		return currentSummary
	}

	// Build the conversation snippet to compress.
	var snippet strings.Builder
	for _, m := range dropped {
		role := m["role"]
		content := truncateSummary(m["content"], 800)
		snippet.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, content))
	}

	prompt := fmt.Sprintf(`The following conversation messages are being dropped from the active context window due to length limits. Compress them into a dense factual summary that preserves:
- Decisions made
- Files created or modified (with paths)
- Errors encountered and how they were resolved
- Current state of work in progress
- Key technical facts established

Be specific and terse. Use bullet points. Max 400 words.

Messages to compress:
%s

Existing session summary (build on this, don't repeat it):
%s`, snippet.String(), truncateSummary(currentSummary, 600))

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      "claude-haiku-4-5",
		"max_tokens": 512,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	httpReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return currentSummary
	}
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return currentSummary
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return currentSummary
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Content) == 0 {
		return currentSummary
	}

	compacted := strings.TrimSpace(result.Content[0].Text)
	if compacted == "" {
		return currentSummary
	}

	// Carry forward project memory + inject compacted block.
	projectMem := carryForwardProjectMemory(currentSummary)
	sections := make([]string, 0, 2)
	if projectMem != "" {
		sections = append(sections, "Project memory: "+projectMem)
	}
	sections = append(sections, "Compacted history:\n"+compacted)
	return truncateSummary(strings.Join(sections, "\n"), sessionSummaryMaxChars*2)
}
