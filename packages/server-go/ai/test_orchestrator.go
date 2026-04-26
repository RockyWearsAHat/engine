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

// IsErrorLine detects if a line contains an error using language-aware patterns.
func IsErrorLine(line string) bool {
	lower := strings.ToLower(line)

	// Skip known false positives
	for _, fp := range falsePositivePatterns {
		if fp.MatchString(lower) {
			return false
		}
	}

	// Check compiled regex patterns first (more precise)
	for _, p := range errorRegexPatterns {
		if p.MatchString(line) {
			return true
		}
	}

	// Fall back to keyword matching
	for _, keyword := range errorKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// IsWarningLine detects if a line contains a warning using language-aware patterns.
func IsWarningLine(line string) bool {
	lower := strings.ToLower(line)
	for _, keyword := range warningKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// IsStackTraceLine detects if a line is part of a stack trace.
func IsStackTraceLine(line string) bool {
	for _, p := range stackTracePatterns {
		if p.MatchString(line) {
			return true
		}
	}
	return false
}

// DetectExitCode extracts an exit code from a line if present.
// Returns -1 if no exit code found.
func DetectExitCode(line string) int {
	matches := exitCodePattern.FindStringSubmatch(line)
	if len(matches) >= 2 {
		code := 0
		for _, c := range matches[1] {
			code = code*10 + int(c-'0')
		}
		return code
	}
	return -1
}

// ClassifyError attempts to categorize an error line.
// Returns: "compile", "runtime", "test", "network", "permission", or "unknown".
func ClassifyError(line string) string {
	lower := strings.ToLower(line)
	for _, p := range compileErrorPatterns {
		if p.MatchString(lower) {
			return "compile"
		}
	}
	for _, p := range runtimeErrorPatterns {
		if p.MatchString(lower) {
			return "runtime"
		}
	}
	for _, p := range testErrorPatterns {
		if p.MatchString(lower) {
			return "test"
		}
	}
	for _, p := range networkErrorPatterns {
		if p.MatchString(lower) {
			return "network"
		}
	}
	for _, p := range permissionErrorPatterns {
		if p.MatchString(lower) {
			return "permission"
		}
	}
	return "unknown"
}

// --- Pattern definitions ---

var errorKeywords = []string{
	"error", "failed", "panic", "exception", "fatal",
	"crash", "abort", "segfault", "segmentation fault",
	"unhandled", "uncaught", "enoent", "eacces", "eperm",
	"econnrefused", "econnreset", "etimedout",
}

var warningKeywords = []string{
	"warning", "deprecated", "warn:",
}

// Compiled regex patterns for precise error detection
var errorRegexPatterns = []*regexp.Regexp{
	// Go errors
	regexp.MustCompile(`(?i)^.*\.go:\d+:\d+:.*`),                           // file.go:10:5: error
	regexp.MustCompile(`(?i)cannot use .* as .* in`),                         // type mismatch
	regexp.MustCompile(`(?i)undefined: \w+`),                                 // undefined symbol
	// TypeScript/JavaScript errors
	regexp.MustCompile(`(?i)TS\d{4,5}:`),                                     // TS2322: ...
	regexp.MustCompile(`(?i)SyntaxError:`),                                   // SyntaxError: Unexpected token
	regexp.MustCompile(`(?i)TypeError:`),                                     // TypeError: Cannot read
	regexp.MustCompile(`(?i)ReferenceError:`),                                // ReferenceError: x is not defined
	// Rust errors
	regexp.MustCompile(`(?i)^error\[E\d{4}\]:`),                             // error[E0308]:
	regexp.MustCompile(`(?i)^error: aborting due to`),                        // error: aborting due to N errors
	// Python errors
	regexp.MustCompile(`(?i)Traceback \(most recent call last\)`),            // Python traceback
	regexp.MustCompile(`(?i)^\w+Error:`),                                     // ValueError: ...
	regexp.MustCompile(`(?i)^IndentationError:`),                             // IndentationError
	// Build tool errors
	regexp.MustCompile(`(?i)FAIL\s+\S+`),                                    // FAIL github.com/...
	regexp.MustCompile(`(?i)npm ERR!`),                                       // npm ERR!
	regexp.MustCompile(`(?i)ERR_PNPM_`),                                     // ERR_PNPM_...
	// Exit codes
	regexp.MustCompile(`(?i)exit(?:ed with)?\s+(?:code|status)\s+[1-9]\d*`), // exit code 1, exited with code 2
}

var stackTracePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s+at\s+\S+\s+\(`),                         // JS/TS: at Function (file:line)
	regexp.MustCompile(`^\s+at\s+\S+:\d+:\d+`),                      // JS/TS: at file:line:col
	regexp.MustCompile(`^\s+File ".*", line \d+`),                    // Python: File "x.py", line 10
	regexp.MustCompile(`^\s+\S+\.go:\d+`),                            // Go: goroutine stack
	regexp.MustCompile(`^\s+\d+:\s+0x[0-9a-f]+`),                    // Go: frame address
	regexp.MustCompile(`(?i)^\s+\d+ \|`),                             // Rust source annotation
	regexp.MustCompile(`^\s+-->.*:\d+:\d+`),                          // Rust: --> file:line:col
}

var falsePositivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)error.+handling`),       // "error handling" in comments
	regexp.MustCompile(`(?i)\bon.?error\b`),           // "onError" callback names
	regexp.MustCompile(`(?i)error.?boundary`),       // React ErrorBoundary
	regexp.MustCompile(`(?i)if\s+err\s*!=\s*nil`),   // Go error check pattern
	regexp.MustCompile(`(?i)catch\s*\(`),             // catch block definition
	regexp.MustCompile(`(?i)\.error\s*\(`),           // console.error() call
	regexp.MustCompile(`(?i)"error"\s*:`),            // JSON key "error":
}

var exitCodePattern = regexp.MustCompile(`(?i)exit(?:ed with)?\s+(?:code|status)\s+(\d+)`)

var compileErrorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\.go:\d+:\d+:`),
	regexp.MustCompile(`(?i)TS\d{4,5}:`),
	regexp.MustCompile(`(?i)error\[E\d{4}\]:`),
	regexp.MustCompile(`(?i)SyntaxError:`),
	regexp.MustCompile(`(?i)IndentationError:`),
	regexp.MustCompile(`(?i)cannot find module`),
	regexp.MustCompile(`(?i)module not found`),
}

var runtimeErrorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)panic:`),
	regexp.MustCompile(`(?i)segmentation fault`),
	regexp.MustCompile(`(?i)null pointer`),
	regexp.MustCompile(`(?i)stack overflow`),
	regexp.MustCompile(`(?i)out of memory`),
	regexp.MustCompile(`(?i)TypeError:`),
	regexp.MustCompile(`(?i)ReferenceError:`),
}

var testErrorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)FAIL\s+\S+`),
	regexp.MustCompile(`(?i)test.*failed`),
	regexp.MustCompile(`(?i)assertion.*failed`),
	regexp.MustCompile(`(?i)expect\(.*\)\.to`),
}

var networkErrorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ECONNREFUSED`),
	regexp.MustCompile(`(?i)ECONNRESET`),
	regexp.MustCompile(`(?i)ETIMEDOUT`),
	regexp.MustCompile(`(?i)connection refused`),
	regexp.MustCompile(`(?i)dns.*resolution.*failed`),
}

var permissionErrorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)EACCES`),
	regexp.MustCompile(`(?i)EPERM`),
	regexp.MustCompile(`(?i)permission denied`),
	regexp.MustCompile(`(?i)access denied`),
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
	report.WriteString("═══ BEHAVIORAL VALIDATION REPORT ═══\n")
	fmt.Fprintf(&report, "Issue: %s\n", issue)
	report.WriteString("Status: ")
	
	if result.IssueResolved {
		report.WriteString("✓ RESOLVED\n")
	} else {
		report.WriteString("✗ NOT RESOLVED\n")
	}
	
	fmt.Fprintf(&report, "Test Passed: %v\n", result.TestPassed)
	fmt.Fprintf(&report, "Errors: %d | Warnings: %d\n", result.ErrorCount, result.WarningCount)
	fmt.Fprintf(&report, "Duration: %dms\n", result.DurationMs)
	fmt.Fprintf(&report, "\nEvidence:\n%s\n", result.Evidence)
	
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
