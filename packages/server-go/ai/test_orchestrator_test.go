package ai

import (
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
	if !contains(summary.Output, "Starting") {
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
		{"Error: file not found", true},
		{"Exception occurred", true},
		{"PANIC: null pointer", true},
		{"Everything is fine", false},
		{"Info: starting", false},
	}
	
	for _, tt := range tests {
		result := IsErrorLine(tt.line)
		if result != tt.expected {
			t.Errorf("IsErrorLine(%q) = %v, expected %v", tt.line, result, tt.expected)
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
	fullOutput := `Starting app...
	Loaded config...
	Error: undefined variable 'x'
	Stack trace line 1
	Stack trace line 2
	Failed to continue`
	
	relevant := ExtractRelevantOutput(fullOutput, "undefined variable")
	
	if !contains(relevant, "Error: undefined variable") {
		t.Errorf("Relevant output missing error line")
	}
	if !contains(relevant, "Stack trace") {
		t.Errorf("Relevant output missing stack trace")
	}
}

func TestTestOrchestratorCycle(t *testing.T) {
	orchestrator := NewTestOrchestrator("connection timeout")
	
	// Simulate successful test run
	orchestrator.AddOutput("Connecting to database...")
	orchestrator.AddOutput("Connection established")
	orchestrator.AddOutput("Running tests...")
	orchestrator.AddOutput("All tests passed")
	
	// Small delay to ensure duration > 0
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
	
	if !contains(report, "RESOLVED") {
		t.Errorf("Report missing resolved status")
	}
	if !contains(report, "250ms") {
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

func contains(s, substr string) bool {
	for i := 0; i < len(s); i++ {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
