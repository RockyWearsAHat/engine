package ai

import (
	"fmt"
	"time"

	"github.com/engine/server/db"
	gh "github.com/engine/server/github"
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
func SendTestCommand(ctx *ChatContext, terminalID string, command string, issue string, timeoutMs int64) {
	if ctx == nil || ctx.SendToClient == nil {
		return
	}
	
	ctx.SendToClient("test.run", map[string]interface{}{
		"terminalId": terminalID,
		"command":    command,
		"timeoutMs":  timeoutMs,
		"issue":      issue,
	})
}

// ReceiveTestOutput should be called by the WebSocket handler when terminal output arrives.
func ReceiveTestOutput(tlc *TestLoopController, output string) {
	if tlc == nil || tlc.orchestrator == nil {
		return
	}
	
	tlc.orchestrator.AddOutput(output)
}

// CompleteTestRun should be called when the test terminal closes or timeout occurs.
// Returns the behavioral validation result and persists it to the database.
func CompleteTestRun(tlc *TestLoopController) BehavioralResult {
	if tlc == nil || tlc.orchestrator == nil {
		return BehavioralResult{}
	}
	
	result := tlc.orchestrator.GetValidationResult()

	// Persist validation result to DB
	sessionID := ""
	if tlc.ctx != nil {
		sessionID = tlc.ctx.SessionID
	}
	resultID := fmt.Sprintf("vr-%d", time.Now().UnixNano())
	_ = db.SaveValidationResult(
		resultID,
		sessionID,
		tlc.orchestrator.issue,
		result.IssueResolved,
		result.TestPassed,
		result.ErrorCount,
		result.WarningCount,
		result.DurationMs,
		result.Evidence,
		"",
	)

	// Record a learning event from the outcome
	category := "test-strategy"
	if result.ErrorCount > 0 {
		category = "error-pattern"
	}
	outcome := "failure"
	if result.IssueResolved {
		outcome = "success"
	}
	confidence := 0.5
	if result.IssueResolved && result.TestPassed {
		confidence = 0.9
	}
	learnID := fmt.Sprintf("le-%d", time.Now().UnixNano())
	_ = db.SaveLearningEvent(
		learnID,
		sessionID,
		fmt.Sprintf("issue:%s → %s", tlc.orchestrator.issue, outcome),
		outcome,
		confidence,
		category,
		result.Evidence,
	)

	return result
}

// ReportTestResult sends the validation result back to the AI via the ChatContext.
func ReportTestResult(tlc *TestLoopController, issue string) {
	if tlc == nil {
		return
	}
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
type OnTestComplete func(result BehavioralResult, issue string)

// MakeTestCompleteHandler returns a callback that integrates test results into the chat.
func MakeTestCompleteHandler(tlc *TestLoopController, issue string) OnTestComplete {
	return func(result BehavioralResult, issue string) {
		report := FormatValidationReport(result, issue)
		
		if tlc.ctx != nil && tlc.ctx.SendToClient != nil {
			tlc.ctx.SendToClient("test.result", map[string]interface{}{
				"issueResolved": result.IssueResolved,
				"testPassed":    result.TestPassed,
				"errorCount":    result.ErrorCount,
				"warningCount":  result.WarningCount,
				"report":        report,
			})
		}
		
		if tlc.ctx != nil {
			summary := fmt.Sprintf(
				"Test result: %s. Issue resolved: %v. Errors: %d, Warnings: %d",
				issue,
				result.IssueResolved,
				result.ErrorCount,
				result.WarningCount,
			)
			
			if tlc.ctx.SessionID != "" && tlc.ctx.SendToClient != nil {
				tlc.ctx.SendToClient("chat.message", map[string]interface{}{
					"role":    "system",
					"content": summary,
				})
			}
		}
	}
}

// InjectLearnings queries prior learnings from the DB and returns context for the system prompt.
func InjectLearnings(issueText string) string {
	learnings, err := db.GetRelevantLearnings(issueText, 5)
	if err != nil || len(learnings) == 0 {
		return ""
	}

	var result string
	result = "Prior learnings relevant to this issue:\n"
	for _, l := range learnings {
		result += fmt.Sprintf("- [%s] %s (confidence: %.0f%%, %s)\n",
			l.Category, l.Pattern, l.Confidence*100, l.Outcome)
	}
	return result
}

// IssueToTestPredicate converts a GitHub issue to a test predicate.
func IssueToTestPredicate(issueTitle string, issueBody string) func(ctx *ChatContext) {
	return func(ctx *ChatContext) {
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

// AutoCloseIssueIfResolved checks the latest validation result for an issue
// and closes it on GitHub if resolved, with evidence as the closing comment.
func AutoCloseIssueIfResolved(sessionID string, issueNumber int, owner, repo, issueText string) (bool, error) {
	latest, err := db.GetLatestValidation(sessionID, issueText)
	if err != nil {
		return false, fmt.Errorf("check validation: %w", err)
	}
	if latest == nil || !latest.IssueResolved {
		return false, nil
	}

	ghClient, err := newGitHubClient(owner, repo)
	if err != nil {
		return false, err
	}

	comment := fmt.Sprintf(
		"✅ **Automatically resolved by Engine**\n\n"+
			"Validation passed at %s\n\n"+
			"**Evidence:**\n%s\n\n"+
			"**Duration:** %dms | **Errors:** %d | **Warnings:** %d",
		time.Now().Format(time.RFC3339),
		latest.Evidence,
		latest.DurationMs,
		latest.ErrorCount,
		latest.WarningCount,
	)

	return true, ghClient.CloseIssue(issueNumber, comment)
}

// newGitHubClient is a seam for testing — defaults to real client.
var newGitHubClient = newRealGitHubClient

type githubCloser interface {
	CloseIssue(number int, comment string) error
}

func newRealGitHubClient(owner, repo string) (githubCloser, error) {
	return gh.NewClient(owner, repo)
}
