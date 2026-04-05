package ai

import (
	"fmt"
)

// TestLoopController manages the AI test→observe→validate cycle.
// Used by Chat() when the AI decides to test a fix.
type TestLoopController struct {
	ctx          *ChatContext
	orchestrator *TestOrchestrator
	terminalID   string
}

// NewTestLoopController creates a controller for testing a fix.
func NewTestLoopController(ctx *ChatContext, issue string, terminalID string) *TestLoopController {
	return &TestLoopController{
		ctx:          ctx,
		orchestrator: NewTestOrchestrator(issue),
		terminalID:   terminalID,
	}
}

// SendTestCommand sends a command to the terminal and sets up observation.
// The AI should use this tool to run tests.
//
// Example tool call:
//   Tool: "test.run"
//   Input: {
//     "terminalId": "term-123",
//     "command": "npm test -- --testNamePattern=UserLogin",
//     "timeoutMs": 30000,
//     "issue": "login endpoint returns 500"
//   }
func SendTestCommand(ctx *ChatContext, terminalID string, command string, issue string, timeoutMs int64) {
	if ctx == nil || ctx.SendToClient == nil {
		return
	}
	
	// Instruct the client to run the test command and stream output back
	ctx.SendToClient("test.run", map[string]interface{}{
		"terminalId": terminalID,
		"command":    command,
		"timeoutMs":  timeoutMs,
		"issue":      issue,
	})
}

// ReceiveTestOutput should be called by the WebSocket handler when terminal output arrives
// during a test run. It accumulates output and will call ValidateTestResult when complete.
func ReceiveTestOutput(tlc *TestLoopController, output string) {
	if tlc == nil || tlc.orchestrator == nil {
		return
	}
	
	tlc.orchestrator.AddOutput(output)
}

// CompleteTestRun should be called when the test terminal closes or timeout occurs.
// Returns the behavioral validation result.
func CompleteTestRun(tlc *TestLoopController) BehavioralResult {
	if tlc == nil || tlc.orchestrator == nil {
		return BehavioralResult{}
	}
	
	return tlc.orchestrator.GetValidationResult()
}

// ReportTestResult sends the validation result back to the AI via the ChatContext.
// The AI can then decide: if issue resolved, commit; if not, iterate.
func ReportTestResult(tlc *TestLoopController, issue string) {
	result := CompleteTestRun(tlc)
	report := FormatValidationReport(result, issue)
	
	if tlc.ctx != nil && tlc.ctx.SendToClient != nil {
		tlc.ctx.SendToClient("test.result", map[string]interface{}{
			"issue":           issue,
			"issueResolved":   result.IssueResolved,
			"testPassed":      result.TestPassed,
			"errorCount":      result.ErrorCount,
			"warningCount":    result.WarningCount,
			"durationMs":      result.DurationMs,
			"evidence":        result.Evidence,
			"report":          report,
		})
	}
}

// OnTestComplete is a callback hook for when a test run completes.
// Integrates with ChatContext to feed validation results back to the AI.
type OnTestComplete func(result BehavioralResult, issue string)

// MakeTestCompleteHandler returns a callback that integrates test results into the chat.
func MakeTestCompleteHandler(tlc *TestLoopController, issue string) OnTestComplete {
	return func(result BehavioralResult, issue string) {
		report := FormatValidationReport(result, issue)
		
		// Send result to client for UI display
		if tlc.ctx != nil && tlc.ctx.SendToClient != nil {
			tlc.ctx.SendToClient("test.result", map[string]interface{}{
				"issueResolved": result.IssueResolved,
				"testPassed":    result.TestPassed,
				"errorCount":    result.ErrorCount,
				"warningCount":  result.WarningCount,
				"report":        report,
			})
		}
		
		// Append validation result to chat context so AI can reason about it
		if tlc.ctx != nil {
			summary := fmt.Sprintf(
				"Test result: %s. Issue resolved: %v. Errors: %d, Warnings: %d",
				issue,
				result.IssueResolved,
				result.ErrorCount,
				result.WarningCount,
			)
			
			// Store in session for context
			if tlc.ctx.SessionID != "" {
				// Note: ideally this would save to DB, but for now we just
				// notify the AI via the callback
				if tlc.ctx.SendToClient != nil {
					tlc.ctx.SendToClient("chat.message", map[string]interface{}{
						"role":    "system",
						"content": summary,
					})
				}
			}
		}
	}
}

// IssueToTestPredicate converts a GitHub issue to a test predicate.
// Returns a function that runs the appropriate test for that issue.
func IssueToTestPredicate(issueTitle string, issueBody string) func(ctx *ChatContext) {
	return func(ctx *ChatContext) {
		// The AI should extract what to test from the issue:
		// Examples:
		// - "Login endpoint returns 500" → run integration test for login
		// - "File upload fails with large files" → run test with 500MB file
		// - "Memory leak in WebSocket handler" → run stress test
		
		msg := fmt.Sprintf(
			"Issue detected: %s\n%s\n\nPlease run appropriate tests to validate the fix.",
			issueTitle,
			issueBody,
		)
		
		if ctx != nil && ctx.SendToClient != nil {
			ctx.SendToClient("test.recommend", map[string]interface{}{
				"issue":  issueTitle,
				"prompt": msg,
			})
		}
	}
}
