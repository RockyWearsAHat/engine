package ai

import "strings"

// autonomousForcePrefixes are explicit slash-style overrides. When a prompt
// starts with any of these the agent ALWAYS engages autonomous mode, regardless
// of the broader intent classifier. Use these for the "I know what I want, just
// do it" quick command.
var autonomousForcePrefixes = []string{
	"/auto",
	"/autonomous",
	"/build",
	"/ship",
	"!auto",
	"!autonomous",
	"!build",
}

// autonomousForceExact catches very short single-token quick commands.
var autonomousForceExact = map[string]bool{
	"/auto":       true,
	"/autonomous": true,
	"/build":      true,
	"/ship":       true,
	"!auto":       true,
	"!autonomous": true,
	"!build":      true,
}

// DetectAutonomousIntent returns true when a chat prompt should run in
// autonomous build mode (no approval modals, awareness-only).
//
// Two paths:
//  1. Explicit override: prompt starts with /auto, /build, !auto, !build, etc.
//     Always engages autonomous mode regardless of phrasing.
//  2. Intent parse: hand the prompt to the existing ClassifyRequest heuristic,
//     which already maps structural-work verbs (build/implement/fix/refactor/
//     scaffold/create/add/...) to RequestWorkflow. A workflow request IS by
//     definition something the agent should drive autonomously.
//
// Question-form prompts are explicitly excluded so "what does build do?" or
// "explain the build process" stay in interactive mode even though they
// contain a workflow keyword. The slash override always wins.
func DetectAutonomousIntent(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	trimmed := strings.Trim(normalized, " \t\n\r?!.")
	if autonomousForceExact[trimmed] {
		return true
	}
	for _, p := range autonomousForcePrefixes {
		if strings.HasPrefix(normalized, p) {
			return true
		}
	}
	if isQuestionForm(normalized) {
		return false
	}
	if ClassifyRequest(prompt, 0) == RequestWorkflow {
		return true
	}
	return false
}

// isQuestionForm returns true when the (lowercased) prompt reads like a
// question or explanation request rather than a directive. Used to keep
// "what does build do?" or "explain the build process" out of autonomous
// mode even though they contain workflow keywords.
func isQuestionForm(lower string) bool {
	if strings.Contains(lower, "?") {
		return true
	}
	prefixes := []string{
		"what ", "what's ", "whats ",
		"how ", "how's ", "hows ",
		"why ", "why's ", "whys ",
		"who ", "when ", "where ",
		"can you explain", "could you explain", "please explain", "explain ",
		"tell me ", "describe ", "show me how",
		"is it ", "are we ", "should we ", "should i ",
		"do you ", "does ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	// Trailing question fragments — "build failed, what happened".
	contains := []string{
		" what happened", " what went wrong", " any idea", " any ideas",
		" why is", " why does", " why did",
	}
	for _, c := range contains {
		if strings.Contains(lower, c) {
			return true
		}
	}
	return false
}
