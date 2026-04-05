package ai

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// TestObserver watches terminal output and detects issues.
type TestObserver struct {
	output    strings.Builder
	startTime time.Time
	errors    []string
	warnings  []string
}

// NewTestObserver creates a new test observer.
func NewTestObserver() *TestObserver {
	return &TestObserver{
		startTime: time.Now(),
		errors:    []string{},
		warnings:  []string{},
	}
}

// Observe processes a line of terminal output and detects issues.
func (to *TestObserver) Observe(line string) {
	to.output.WriteString(line)
	to.output.WriteString("\n")

	// Detect error patterns
	if IsErrorLine(line) {
		to.errors = append(to.errors, line)
	}

	// Detect warning patterns
	if IsWarningLine(line) {
		to.warnings = append(to.warnings, line)
	}
}

// GetSummary returns a summary of observed issues.
func (to *TestObserver) GetSummary() TestSummary {
	return TestSummary{
		Output:    to.output.String(),
		Errors:    to.errors,
		Warnings:  to.warnings,
		DurationMs: int64(time.Since(to.startTime).Milliseconds()),
		Success:    len(to.errors) == 0,
	}
}

// TestSummary contains the results of a test run.
type TestSummary struct {
	Output     string   `json:"output"`
	Errors     []string `json:"errors"`
	Warnings   []string `json:"warnings"`
	DurationMs int64    `json:"durationMs"`
	Success    bool     `json:"success"`
}

// IsErrorLine detects if a line contains an error.
func IsErrorLine(line string) bool {
	line = strings.ToLower(line)
	errorPatterns := []string{
		"error",
		"failed",
		"panic",
		"exception",
		"fatal",
		"crash",
		"abort",
		"segfault",
	}
	for _, pattern := range errorPatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

// IsWarningLine detects if a line contains a warning.
func IsWarningLine(line string) bool {
	line = strings.ToLower(line)
	warningPatterns := []string{
		"warning",
		"deprecated",
		"todo",
		"fixme",
		"xxx",
	}
	for _, pattern := range warningPatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

// BehavioralValidator checks if a fix actually resolved the issue.
type BehavioralValidator struct {
	originalIssue   string
	issuePredicate  func(string) bool
	maxWaitMs       int64
}

// NewBehavioralValidator creates a validator for an issue.
func NewBehavioralValidator(issue string) *BehavioralValidator {
	return &BehavioralValidator{
		originalIssue: issue,
		issuePredicate: func(output string) bool {
			return strings.Contains(strings.ToLower(output), strings.ToLower(issue))
		},
		maxWaitMs: 30000, // 30 second timeout
	}
}

// Validate checks if the issue appears in the test output.
func (bv *BehavioralValidator) Validate(summary TestSummary) BehavioralResult {
	issueStillPresent := bv.issuePredicate(summary.Output)
	
	return BehavioralResult{
		IssueResolved:  !issueStillPresent,
		TestPassed:     summary.Success,
		ErrorCount:     len(summary.Errors),
		WarningCount:   len(summary.Warnings),
		DurationMs:     summary.DurationMs,
		Evidence:       ExtractRelevantOutput(summary.Output, bv.originalIssue),
	}
}

// BehavioralResult represents the outcome of behavioral validation.
type BehavioralResult struct {
	IssueResolved  bool   `json:"issueResolved"`
	TestPassed     bool   `json:"testPassed"`
	ErrorCount     int    `json:"errorCount"`
	WarningCount   int    `json:"warningCount"`
	DurationMs     int64  `json:"durationMs"`
	Evidence       string `json:"evidence"`
}

// ExtractRelevantOutput pulls the most relevant parts of output for evidence.
func ExtractRelevantOutput(fullOutput string, issue string) string {
	lines := strings.Split(fullOutput, "\n")
	
	// Look for lines containing the issue keyword
	issueKeyword := strings.ToLower(issue)
	relevantLines := []string{}
	
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), issueKeyword) {
			// Include context: 2 lines before, 3 lines after
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 3
			if end > len(lines) {
				end = len(lines)
			}
			for j := start; j < end; j++ {
				if j >= 0 && j < len(lines) {
					relevantLines = append(relevantLines, lines[j])
				}
			}
			break
		}
	}
	
	if len(relevantLines) == 0 {
		// Fallback: return last 10 lines
		start := len(lines) - 10
		if start < 0 {
			start = 0
		}
		relevantLines = lines[start:]
	}
	
	return strings.Join(relevantLines, "\n")
}

// TestOrchestrator orchestrates the full test→observe→validate cycle.
type TestOrchestrator struct {
	observer  *TestObserver
	validator *BehavioralValidator
	issue     string
}

// NewTestOrchestrator creates a test orchestrator for validating a fix.
func NewTestOrchestrator(issue string) *TestOrchestrator {
	return &TestOrchestrator{
		observer:  NewTestObserver(),
		validator: NewBehavioralValidator(issue),
		issue:     issue,
	}
}

// AddOutput adds a line of terminal output.
func (to *TestOrchestrator) AddOutput(line string) {
	to.observer.Observe(line)
}

// GetValidationResult returns the behavioral validation result.
func (to *TestOrchestrator) GetValidationResult() BehavioralResult {
	summary := to.observer.GetSummary()
	return to.validator.Validate(summary)
}

// FormatValidationReport creates a human-readable validation report.
func FormatValidationReport(result BehavioralResult, issue string) string {
	report := strings.Builder{}
	report.WriteString(fmt.Sprintf("═══ BEHAVIORAL VALIDATION REPORT ═══\n"))
	report.WriteString(fmt.Sprintf("Issue: %s\n", issue))
	report.WriteString(fmt.Sprintf("Status: "))
	
	if result.IssueResolved {
		report.WriteString("✓ RESOLVED\n")
	} else {
		report.WriteString("✗ NOT RESOLVED\n")
	}
	
	report.WriteString(fmt.Sprintf("Test Passed: %v\n", result.TestPassed))
	report.WriteString(fmt.Sprintf("Errors: %d | Warnings: %d\n", result.ErrorCount, result.WarningCount))
	report.WriteString(fmt.Sprintf("Duration: %dms\n", result.DurationMs))
	report.WriteString(fmt.Sprintf("\nEvidence:\n%s\n", result.Evidence))
	
	return report.String()
}

// IssueMatcher helps match GitHub issues to test failures.
type IssueMatcher struct {
	issueTitle   string
	issueBody    string
	pattern      *regexp.Regexp
}

// NewIssueMatcher creates a matcher from a GitHub issue.
func NewIssueMatcher(title, body string) *IssueMatcher {
	// Extract common keywords from issue: error types, variable names, function names
	pattern := regexp.MustCompile(`(?i)([a-z_]\w+|\w+(?:\s+\w+)?)`)
	
	return &IssueMatcher{
		issueTitle: title,
		issueBody:  body,
		pattern:    pattern,
	}
}

// Matches checks if the test output matches this issue.
func (im *IssueMatcher) Matches(testOutput string) bool {
	combined := strings.ToLower(im.issueTitle + " " + im.issueBody)
	output := strings.ToLower(testOutput)
	
	// Look for error-specific keywords first
	errorKeywords := []string{"error", "fail", "crash", "panic", "exception", "fatal"}
	hasErrorContext := false
	for _, kw := range errorKeywords {
		if strings.Contains(combined, kw) && strings.Contains(output, kw) {
			hasErrorContext = true
			break
		}
	}
	
	// Extract substantive terms (nouns, variables, function names)
	issueTerms := []string{}
	words := strings.FieldsFunc(combined, func(r rune) bool {
		return r == ' ' || r == '\n' || r == ':' || r == ',' || r == '.'
	})
	
	for _, word := range words {
		// Keep words that look like variables or identifiers
		if len(word) > 2 && !isCommonWord(word) {
			issueTerms = append(issueTerms, word)
		}
	}
	
	// For matches, require:
	// 1. Error context is present in both, OR
	// 2. Multiple significant terms from issue appear in output
	
	matchCount := 0
	for _, term := range issueTerms {
		if strings.Contains(output, term) {
			matchCount++
		}
	}
	
	// Need at least 2 matches or error context match
	return hasErrorContext || matchCount >= 2
}

// isCommonWord returns true for words that appear in many contexts
func isCommonWord(word string) bool {
	common := map[string]bool{
		"and": true, "or": true, "the": true, "a": true, "an": true,
		"is": true, "are": true, "be": true, "to": true, "for": true,
		"of": true, "in": true, "on": true, "at": true, "by": true,
		"with": true, "from": true, "up": true, "fix": true, "should": true,
		"when": true, "where": true, "why": true, "what": true, "how": true,
		"it": true, "this": true, "that": true, "as": true, "using": true,
	}
	return common[word]
}
