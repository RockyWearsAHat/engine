package ai

import (
	"strings"
	"testing"
	"time"
)

func TestObserverErrorDetection(t *testing.T) {
	observer := NewTestObserver()
	
	observer.Observe("Starting application...")
	observer.Observe("Error: undefined variable 'x'")
	observer.Observe("Stopping...")
	
	summary := observer.GetSummary()
	
	if len(summary.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(summary.Errors))
	}
	if !strings.Contains(summary.Output, "Starting") {
		t.Errorf("Output not recorded")
	}
	if summary.Success {
		t.Errorf("Expected Success to be false when errors present")
	}
}

func TestObserverWarningDetection(t *testing.T) {
	observer := NewTestObserver()
	
	observer.Observe("Starting...")
	observer.Observe("Warning: deprecated API used")
	observer.Observe("Continuing...")
	
	summary := observer.GetSummary()
	
	if len(summary.Warnings) != 1 {
		t.Errorf("Expected 1 warning, got %d", len(summary.Warnings))
	}
}

func TestIsErrorLine(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		// Basic keywords
		{"Error: file not found", true},
		{"Exception occurred", true},
		{"PANIC: null pointer", true},
		{"Everything is fine", false},
		{"Info: starting", false},
		// Language-specific: Go
		{"main.go:42:5: undefined: fooBar", true},
		{"cannot use x as int in argument", true},
		// Language-specific: TypeScript
		{"TS2322: Type 'string' is not assignable to type 'number'", true},
		{"SyntaxError: Unexpected token }", true},
		{"TypeError: Cannot read properties of undefined", true},
		{"ReferenceError: x is not defined", true},
		// Language-specific: Rust
		{"error[E0308]: mismatched types", true},
		{"error: aborting due to 3 previous errors", true},
		// Language-specific: Python
		{"Traceback (most recent call last)", true},
		{"ValueError: invalid literal", true},
		{"IndentationError: unexpected indent", true},
		// Build tools
		{"FAIL github.com/engine/server/ai", true},
		{"npm ERR! missing script: test", true},
		{"ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL", true},
		// Exit codes
		{"exited with code 1", true},
		{"exit status 2", true},
		// Network
		{"ECONNREFUSED", true},
		{"ECONNRESET", true},
		// False positives that SHOULD NOT match
		{"error handling is important", false},
		{"onError callback registered", false},
		{"if err != nil {", false},
		{"catch (e) {", false},
		{`console.error("debug info")`, false},
		{`"error": "none"`, false},
	}
	
	for _, tt := range tests {
		result := IsErrorLine(tt.line)
		if result != tt.expected {
			t.Errorf("IsErrorLine(%q) = %v, expected %v", tt.line, result, tt.expected)
		}
	}
}

func TestIsWarningLine(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"warning: unused variable", true},
		{"Warning: deprecated API", true},
		{"DeprecationWarning: Buffer() is deprecated", true},
		{"warning[unused_imports]:", true},
		{"  warning: field is never read", true},
		{"warn: low memory", true},
		{"Everything is fine", false},
	}
	
	for _, tt := range tests {
		result := IsWarningLine(tt.line)
		if result != tt.expected {
			t.Errorf("IsWarningLine(%q) = %v, expected %v", tt.line, result, tt.expected)
		}
	}
}

func TestIsStackTraceLine(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		// JS/TS stack traces
		{"    at Object.<anonymous> (/app/index.js:10:5)", true},
		{"    at /app/server.ts:42:10", true},
		// Python stack traces
		{`  File "/app/main.py", line 42`, true},
		// Go stack traces
		{"    main.go:42", true},
		// Rust source annotations
		{"   --> src/main.rs:42:10", true},
		// Not stack traces
		{"Normal output line", false},
		{"Error: something broke", false},
	}
	
	for _, tt := range tests {
		result := IsStackTraceLine(tt.line)
		if result != tt.expected {
			t.Errorf("IsStackTraceLine(%q) = %v, expected %v", tt.line, result, tt.expected)
		}
	}
}

func TestDetectExitCode(t *testing.T) {
	tests := []struct {
		line     string
		expected int
	}{
		{"exited with code 1", 1},
		{"exit status 2", 2},
		{"exit code 127", 127},
		{"exited with code 0", 0},
		{"normal output", -1},
		{"no exit here", -1},
	}
	
	for _, tt := range tests {
		result := DetectExitCode(tt.line)
		if result != tt.expected {
			t.Errorf("DetectExitCode(%q) = %v, expected %v", tt.line, result, tt.expected)
		}
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		line     string
		expected string
	}{
		{"main.go:42:5: undefined: fooBar", "compile"},
		{"TS2322: Type mismatch", "compile"},
		{"error[E0308]: mismatched types", "compile"},
		{"SyntaxError: Unexpected token", "compile"},
		{"cannot find module 'express'", "compile"},
		{"panic: runtime error: index out of range", "runtime"},
		{"segmentation fault", "runtime"},
		{"TypeError: Cannot read properties", "runtime"},
		{"FAIL github.com/engine/server", "test"},
		{"test suite failed", "test"},
		{"assertion failed: expected 5", "test"},
		{"ECONNREFUSED 127.0.0.1:3000", "network"},
		{"ETIMEDOUT", "network"},
		{"EACCES: permission denied", "permission"},
		{"access denied to /root/secret", "permission"},
		{"something unknown happened", "unknown"},
	}
	
	for _, tt := range tests {
		result := ClassifyError(tt.line)
		if result != tt.expected {
			t.Errorf("ClassifyError(%q) = %q, expected %q", tt.line, result, tt.expected)
		}
	}
}

func TestBehavioralValidatorResolved(t *testing.T) {
	validator := NewBehavioralValidator("undefined variable")
	
	summary := TestSummary{
		Output:    "App started successfully\nNo errors found",
		Errors:    []string{},
		Warnings:  []string{},
		Success:   true,
	}
	
	result := validator.Validate(summary)
	
	if !result.IssueResolved {
		t.Errorf("Expected issue to be resolved")
	}
	if !result.TestPassed {
		t.Errorf("Expected test to pass")
	}
}

func TestBehavioralValidatorNotResolved(t *testing.T) {
	validator := NewBehavioralValidator("undefined variable")
	
	summary := TestSummary{
		Output:    "Error: undefined variable 'x' at line 42",
		Errors:    []string{"Error: undefined variable 'x'"},
		Warnings:  []string{},
		Success:   false,
	}
	
	result := validator.Validate(summary)
	
	if result.IssueResolved {
		t.Errorf("Expected issue to NOT be resolved")
	}
	if result.TestPassed {
		t.Errorf("Expected test to fail")
	}
}

func TestExtractRelevantOutput(t *testing.T) {
	fullOutput := "Starting app...\nLoaded config...\nError: undefined variable 'x'\nStack trace line 1\nStack trace line 2\nFailed to continue"
	
	relevant := ExtractRelevantOutput(fullOutput, "undefined variable")
	
	if !strings.Contains(relevant, "undefined variable") {
		t.Errorf("Relevant output missing error line")
	}
	if !strings.Contains(relevant, "Stack trace") {
		t.Errorf("Relevant output missing stack trace")
	}
}

func TestTestOrchestratorCycle(t *testing.T) {
	orchestrator := NewTestOrchestrator("connection timeout")
	
	orchestrator.AddOutput("Connecting to database...")
	orchestrator.AddOutput("Connection established")
	orchestrator.AddOutput("Running tests...")
	orchestrator.AddOutput("All tests passed")
	
	time.Sleep(1 * time.Millisecond)
	
	result := orchestrator.GetValidationResult()
	
	if !result.IssueResolved {
		t.Errorf("Expected issue to be resolved")
	}
	if !result.TestPassed {
		t.Errorf("Expected test to pass")
	}
	if result.DurationMs == 0 {
		t.Errorf("Expected duration to be recorded, got %d", result.DurationMs)
	}
}

func TestFormatValidationReport(t *testing.T) {
	result := BehavioralResult{
		IssueResolved: true,
		TestPassed:    true,
		ErrorCount:    0,
		WarningCount:  1,
		DurationMs:    250,
		Evidence:      "App running smoothly",
	}
	
	report := FormatValidationReport(result, "connection timeout")
	
	if !strings.Contains(report, "RESOLVED") {
		t.Errorf("Report missing resolved status")
	}
	if !strings.Contains(report, "250ms") {
		t.Errorf("Report missing duration")
	}
}

func TestIssueMatcherExtractsPattern(t *testing.T) {
	title := "Fix: undefined variable error"
	body := "Error: x is not defined when loading config\nExpected behavior: should use default value"
	
	matcher := NewIssueMatcher(title, body)
	
	testOutput1 := "Error: undefined variable 'x'"
	testOutput2 := "Successfully loaded with defaults"
	
	if !matcher.Matches(testOutput1) {
		t.Errorf("Matcher should match relevant output")
	}
	if matcher.Matches(testOutput2) {
		t.Errorf("Matcher should not match unrelated output")
	}
}

func TestObserverWithStackTrace(t *testing.T) {
	observer := NewTestObserver()
	
	observer.Observe("TypeError: Cannot read properties of undefined")
	observer.Observe("    at Object.<anonymous> (/app/index.js:10:5)")
	observer.Observe("    at Module._compile (node:internal/modules/cjs/loader:1105:14)")
	
	summary := observer.GetSummary()
	
	if len(summary.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d (stack trace lines should not count as separate errors)", len(summary.Errors))
	}
}

func TestObserverMultipleLanguageErrors(t *testing.T) {
	observer := NewTestObserver()
	
	observer.Observe("main.go:42:5: undefined: fooBar")
	observer.Observe("TS2322: Type 'string' is not assignable to type 'number'")
	observer.Observe("FAIL github.com/engine/server/ai")
	
	summary := observer.GetSummary()
	
	if len(summary.Errors) != 3 {
		t.Errorf("Expected 3 errors, got %d", len(summary.Errors))
	}
	if summary.Success {
		t.Errorf("Expected Success to be false")
	}
}

func TestFalsePositivesNotDetectedAsErrors(t *testing.T) {
	falsePosLines := []string{
		"error handling is important for robust code",
		"onError callback registered successfully",
		"if err != nil { return err }",
		"catch (error) { handleError(error) }",
	}
	
	for _, line := range falsePosLines {
		if IsErrorLine(line) {
			t.Errorf("False positive: %q should not be detected as error", line)
		}
	}
}
