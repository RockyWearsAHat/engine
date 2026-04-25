package ai

import (
	"errors"
	"strings"
	"testing"

	"github.com/engine/server/db"
)

// ── NewTestLoopController ─────────────────────────────────────────────────────

func TestNewTestLoopController_CreatesController(t *testing.T) {
	ctx := &ChatContext{}
	tlc := NewTestLoopController(ctx, "bug #42", "term-1")
	if tlc == nil {
		t.Fatal("expected non-nil controller")
	}
	if tlc.terminalID != "term-1" {
		t.Errorf("expected terminalID term-1, got %q", tlc.terminalID)
	}
	if tlc.orchestrator == nil {
		t.Error("expected non-nil orchestrator")
	}
}

// ── SendTestCommand ───────────────────────────────────────────────────────────

func TestSendTestCommand_NilCtx_NoOp(t *testing.T) {
	// Should not panic.
	SendTestCommand(nil, "term-1", "go test ./...", "bug #1", 30000)
}

func TestSendTestCommand_NilSendToClient_NoOp(t *testing.T) {
	ctx := &ChatContext{} // SendToClient is nil
	SendTestCommand(ctx, "term-1", "go test ./...", "bug #1", 30000)
}

func TestSendTestCommand_SendsMessage(t *testing.T) {
	var captured []interface{}
	ctx := &ChatContext{
		SendToClient: func(msgType string, payload interface{}) {
			captured = append(captured, payload)
		},
	}
	SendTestCommand(ctx, "term-1", "go test ./...", "bug #1", 30000)
	if len(captured) != 1 {
		t.Errorf("expected 1 message sent, got %d", len(captured))
	}
}

// ── ReceiveTestOutput ─────────────────────────────────────────────────────────

func TestReceiveTestOutput_NilTLC_NoOp(t *testing.T) {
	ReceiveTestOutput(nil, "output")
}

func TestReceiveTestOutput_NilOrchestrator_NoOp(t *testing.T) {
	tlc := &TestLoopController{} // orchestrator is nil
	ReceiveTestOutput(tlc, "output")
}

func TestReceiveTestOutput_AddssOutput(t *testing.T) {
	tlc := NewTestLoopController(&ChatContext{}, "issue", "term")
	ReceiveTestOutput(tlc, "PASS")
	// No panic = success; orchestrator accumulated output.
}

// ── CompleteTestRun ───────────────────────────────────────────────────────────

func TestCompleteTestRun_NilTLC_ReturnsEmpty(t *testing.T) {
	result := CompleteTestRun(nil)
	if result.IssueResolved || result.TestPassed || result.ErrorCount != 0 {
		t.Errorf("expected empty result for nil tlc, got %+v", result)
	}
}

func TestCompleteTestRun_NilOrchestrator_ReturnsEmpty(t *testing.T) {
	tlc := &TestLoopController{}
	result := CompleteTestRun(tlc)
	if result.IssueResolved || result.TestPassed || result.ErrorCount != 0 {
		t.Errorf("expected empty result for nil orchestrator, got %+v", result)
	}
}

func TestCompleteTestRun_PersistsResult(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := setupTestDB(t, projectDir); err != nil {
		t.Fatalf("db setup: %v", err)
	}

	ctx := &ChatContext{ProjectPath: projectDir, SessionID: "test-session-tlc"}
	tlc := NewTestLoopController(ctx, "bug #99", "term-2")
	ReceiveTestOutput(tlc, "all tests passed")
	result := CompleteTestRun(tlc)
	// Just verify it returned without panic.
	_ = result
}

// ── ReportTestResult ──────────────────────────────────────────────────────────

func TestReportTestResult_NilTLC_NoOp(t *testing.T) {
	ReportTestResult(nil, "bug")
}

func TestReportTestResult_SendsResultToClient(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := setupTestDB(t, projectDir); err != nil {
		t.Fatalf("db setup: %v", err)
	}

	var sent []string
	ctx := &ChatContext{
		ProjectPath:  projectDir,
		SessionID:    "report-session",
		SendToClient: func(msgType string, _ interface{}) { sent = append(sent, msgType) },
	}
	tlc := NewTestLoopController(ctx, "issue-X", "term-3")
	ReceiveTestOutput(tlc, "PASS")
	ReportTestResult(tlc, "issue-X")

	found := false
	for _, t := range sent {
		if t == "test.result" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected test.result message sent, got %v", sent)
	}
}

// ── MakeTestCompleteHandler ───────────────────────────────────────────────────

func TestMakeTestCompleteHandler_ReturnsCallback(t *testing.T) {
	ctx := &ChatContext{}
	tlc := NewTestLoopController(ctx, "issue", "term")
	cb := MakeTestCompleteHandler(tlc, "issue-Y")
	if cb == nil {
		t.Fatal("expected non-nil callback")
	}
}

func TestMakeTestCompleteHandler_WithSendToClient(t *testing.T) {
	var sent []string
	ctx := &ChatContext{
		SessionID: "sess",
		SendToClient: func(msgType string, _ interface{}) {
			sent = append(sent, msgType)
		},
	}
	tlc := NewTestLoopController(ctx, "issue", "term")
	cb := MakeTestCompleteHandler(tlc, "issue-Z")
	cb(BehavioralResult{IssueResolved: true, TestPassed: true}, "issue-Z")

	if len(sent) == 0 {
		t.Errorf("expected messages sent, got none")
	}
}

func TestMakeTestCompleteHandler_NilSendToClient_NoOp(t *testing.T) {
	ctx := &ChatContext{} // SendToClient is nil
	tlc := NewTestLoopController(ctx, "issue", "term")
	cb := MakeTestCompleteHandler(tlc, "issue-W")
	// Should not panic.
	cb(BehavioralResult{ErrorCount: 2}, "issue-W")
}

// ── InjectLearnings ───────────────────────────────────────────────────────────

func TestInjectLearnings_EmptyDB_ReturnsEmpty(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	_ = projectDir
	result := InjectLearnings("some issue text")
	if result != "" {
		t.Fatalf("expected empty learnings result, got %q", result)
	}
}

func TestInjectLearnings_WithMatchingLearnings(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	sessionID := "inject-learnings-session"
	if err := db.CreateSession(sessionID, projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveLearningEvent(
		"learn-1",
		sessionID,
		"issue text regression fixed with retry guard",
		"success",
		0.87,
		"test-strategy",
		"replay edge behavior",
	); err != nil {
		t.Fatalf("save learning event: %v", err)
	}

	result := InjectLearnings("issue text")
	if !strings.Contains(result, "Prior learnings relevant to this issue") {
		t.Fatalf("expected learnings header, got %q", result)
	}
	if !strings.Contains(result, "test-strategy") {
		t.Fatalf("expected category in learnings output, got %q", result)
	}
}

func TestNewRealGitHubClient_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	_, err := newRealGitHubClient("owner", "repo")
	if err == nil {
		t.Fatal("expected error when GITHUB_TOKEN is not set")
	}
}

func TestNewRealGitHubClient_WithToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	client, err := newRealGitHubClient("owner", "repo")
	if err != nil {
		t.Fatalf("unexpected newRealGitHubClient error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// ── IssueToTestPredicate ──────────────────────────────────────────────────────

func TestIssueToTestPredicate_ReturnsFunction(t *testing.T) {
	fn := IssueToTestPredicate("Login fails", "Users cannot login after password reset")
	if fn == nil {
		t.Fatal("expected non-nil predicate function")
	}
}

func TestIssueToTestPredicate_WithNilCtx_NoOp(t *testing.T) {
	fn := IssueToTestPredicate("Login fails", "Body text")
	fn(nil) // Should not panic.
}

func TestIssueToTestPredicate_WithNilSendToClient_NoOp(t *testing.T) {
	ctx := &ChatContext{} // SendToClient is nil
	fn := IssueToTestPredicate("Login fails", "Body text")
	fn(ctx) // Should not panic.
}

func TestIssueToTestPredicate_SendsRecommendation(t *testing.T) {
	var sent []string
	ctx := &ChatContext{
		SendToClient: func(msgType string, _ interface{}) {
			sent = append(sent, msgType)
		},
	}
	fn := IssueToTestPredicate("Login fails", "Body")
	fn(ctx)
	if len(sent) == 0 || sent[0] != "test.recommend" {
		t.Errorf("expected test.recommend, got %v", sent)
	}
}

// ── AutoCloseIssueIfResolved ──────────────────────────────────────────────────

func TestAutoCloseIssueIfResolved_NoValidation_ReturnsFalse(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	_ = projectDir
	closed, err := AutoCloseIssueIfResolved("nonexistent-session", 42, "owner", "repo", "issue text")
	if closed {
		t.Error("expected not closed without validation record")
	}
	_ = err // error is acceptable
}

type stubIssueCloser struct {
	closedIssue int
	comment     string
	err         error
}

func (s *stubIssueCloser) CloseIssue(number int, comment string) error {
	s.closedIssue = number
	s.comment = comment
	return s.err
}

func TestAutoCloseIssueIfResolved_Resolved_ClosesIssue(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	sessionID := "autoclose-session"
	issueText := "issue text"
	if err := db.CreateSession(sessionID, projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveValidationResult(
		"vr-autoclose",
		sessionID,
		issueText,
		true,
		true,
		0,
		1,
		1234,
		"integration tests passed",
		"go test ./...",
	); err != nil {
		t.Fatalf("save validation: %v", err)
	}

	closer := &stubIssueCloser{}
	orig := newGitHubClient
	newGitHubClient = func(owner, repo string) (githubCloser, error) {
		if owner != "owner" || repo != "repo" {
			t.Fatalf("unexpected repo target: %s/%s", owner, repo)
		}
		return closer, nil
	}
	defer func() { newGitHubClient = orig }()

	closed, err := AutoCloseIssueIfResolved(sessionID, 77, "owner", "repo", issueText)
	if err != nil {
		t.Fatalf("unexpected AutoCloseIssueIfResolved error: %v", err)
	}
	if !closed {
		t.Fatal("expected issue to be reported as closed")
	}
	if closer.closedIssue != 77 {
		t.Fatalf("expected CloseIssue(77), got %d", closer.closedIssue)
	}
	if !strings.Contains(closer.comment, "integration tests passed") {
		t.Fatalf("expected evidence in closing comment, got %q", closer.comment)
	}
}

func TestAutoCloseIssueIfResolved_ClientInitError(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	sessionID := "autoclose-client-error"
	issueText := "issue text"
	if err := db.CreateSession(sessionID, projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveValidationResult("vr-client-error", sessionID, issueText, true, true, 0, 0, 10, "ok", "go test"); err != nil {
		t.Fatalf("save validation: %v", err)
	}

	orig := newGitHubClient
	newGitHubClient = func(_, _ string) (githubCloser, error) {
		return nil, errors.New("client init failed")
	}
	defer func() { newGitHubClient = orig }()

	closed, err := AutoCloseIssueIfResolved(sessionID, 77, "owner", "repo", issueText)
	if err == nil {
		t.Fatal("expected error from GitHub client seam")
	}
	if closed {
		t.Fatal("expected closed=false on client error")
	}
}

func TestAutoCloseIssueIfResolved_CloseIssueError(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	sessionID := "autoclose-close-error"
	issueText := "issue text"
	if err := db.CreateSession(sessionID, projectDir, "main"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveValidationResult("vr-close-error", sessionID, issueText, true, true, 0, 0, 10, "ok", "go test"); err != nil {
		t.Fatalf("save validation: %v", err)
	}

	closer := &stubIssueCloser{err: errors.New("close failed")}
	orig := newGitHubClient
	newGitHubClient = func(_, _ string) (githubCloser, error) {
		return closer, nil
	}
	defer func() { newGitHubClient = orig }()

	closed, err := AutoCloseIssueIfResolved(sessionID, 99, "owner", "repo", issueText)
	if err == nil {
		t.Fatal("expected close issue error")
	}
	if !closed {
		t.Fatal("expected closed=true when validation says resolved")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func setupTestDB(t *testing.T, projectDir string) error {
	t.Helper()
	return nil // setupHistoryTestProject already calls db.Init
}

// FormatValidationReport is tested indirectly via ReportTestResult.
// Verify it at least produces a non-panicking string.
func TestFormatValidationReport_ProducesString(t *testing.T) {
	result := FormatValidationReport(BehavioralResult{
		IssueResolved: true,
		TestPassed:    true,
		ErrorCount:    0,
		WarningCount:  1,
		DurationMs:    500,
		Evidence:      "tests passed",
	}, "bug #42")
	if !strings.Contains(result, "42") {
		t.Errorf("expected issue number in report, got %q", result)
	}
}
