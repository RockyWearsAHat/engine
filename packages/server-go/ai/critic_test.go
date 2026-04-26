package ai

import (
	"strings"
	"testing"
)

// ── CriticVerdict.String ──────────────────────────────────────────────────────

func TestCriticVerdict_String_Approve(t *testing.T) {
	if CriticApprove.String() != "APPROVE" {
		t.Errorf("expected APPROVE, got %q", CriticApprove.String())
	}
}

func TestCriticVerdict_String_Reject(t *testing.T) {
	if CriticReject.String() != "REJECT" {
		t.Errorf("expected REJECT, got %q", CriticReject.String())
	}
}

// ── CriticResult.IsApproved ───────────────────────────────────────────────────

func TestCriticResult_IsApproved_True(t *testing.T) {
	r := CriticResult{Verdict: CriticApprove}
	if !r.IsApproved() {
		t.Error("expected IsApproved() true for CriticApprove")
	}
}

func TestCriticResult_IsApproved_False(t *testing.T) {
	r := CriticResult{Verdict: CriticReject}
	if r.IsApproved() {
		t.Error("expected IsApproved() false for CriticReject")
	}
}

// ── CriticResult.FindingsText ─────────────────────────────────────────────────

func TestCriticResult_FindingsText_JoinsWithNewlines(t *testing.T) {
	r := CriticResult{Findings: []string{"issue A", "issue B"}}
	got := r.FindingsText()
	if !strings.Contains(got, "issue A") || !strings.Contains(got, "issue B") {
		t.Errorf("expected both findings in text, got %q", got)
	}
}

// ── parseCriticOutput ─────────────────────────────────────────────────────────

func TestParseCriticOutput_ApproveVerdict(t *testing.T) {
	result := parseCriticOutput("APPROVE\nLooks good.", "diff")
	if !result.IsApproved() {
		t.Error("expected APPROVE")
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected no findings on approve, got %v", result.Findings)
	}
}

func TestParseCriticOutput_RejectVerdict(t *testing.T) {
	result := parseCriticOutput("REJECT\n- main.go:12: missing nil check", "diff")
	if result.IsApproved() {
		t.Error("expected REJECT verdict")
	}
	if len(result.Findings) == 0 {
		t.Error("expected at least one finding on reject")
	}
}

func TestParseCriticOutput_DiffPreserved(t *testing.T) {
	result := parseCriticOutput("APPROVE", "my diff content")
	if result.Diff != "my diff content" {
		t.Errorf("expected diff preserved, got %q", result.Diff)
	}
}

func TestParseCriticOutput_CaseInsensitiveReject(t *testing.T) {
	result := parseCriticOutput("reject\n1. bad code", "diff")
	if result.IsApproved() {
		t.Error("expected REJECT for lowercase 'reject'")
	}
}

// ── extractFindings ───────────────────────────────────────────────────────────

func TestExtractFindings_BulletLines(t *testing.T) {
	output := "REJECT\n- handler.go:10: nil pointer\n- auth.go:22: missing check"
	findings := extractFindings(output)
	if len(findings) < 2 {
		t.Fatalf("expected ≥2 findings, got %d: %v", len(findings), findings)
	}
}

func TestExtractFindings_NumberedLines(t *testing.T) {
	output := "REJECT\n1. Missing error return\n2. Unused variable"
	findings := extractFindings(output)
	if len(findings) < 2 {
		t.Fatalf("expected ≥2 findings, got %d: %v", len(findings), findings)
	}
}

func TestExtractFindings_FallbackWhenEmpty(t *testing.T) {
	findings := extractFindings("REJECT")
	if len(findings) != 1 {
		t.Fatalf("expected 1 fallback finding, got %d", len(findings))
	}
	if !strings.Contains(findings[0], "without specific findings") {
		t.Errorf("expected fallback message, got %q", findings[0])
	}
}

// ── RunCriticGate (empty diff) ────────────────────────────────────────────────

func TestRunCriticGate_EmptyDiff_AutoApprove(t *testing.T) {
	ctx := &ChatContext{}
	result := RunCriticGate(ctx, "")
	if !result.IsApproved() {
		t.Error("expected auto-approve for empty diff")
	}
}

// ── InjectCriticFindings ──────────────────────────────────────────────────────

func TestInjectCriticFindings_ApprovedReturnsEmpty(t *testing.T) {
	r := CriticResult{Verdict: CriticApprove}
	if got := InjectCriticFindings(r); got != "" {
		t.Errorf("expected empty string for approved result, got %q", got)
	}
}

func TestInjectCriticFindings_RejectedReturnsBlock(t *testing.T) {
	r := CriticResult{Verdict: CriticReject, Findings: []string{"nil pointer at line 10"}}
	got := InjectCriticFindings(r)
	if !strings.Contains(got, "REJECTED") {
		t.Errorf("expected REJECTED in injection block, got %q", got)
	}
	if !strings.Contains(got, "nil pointer") {
		t.Errorf("expected finding in injection block, got %q", got)
	}
	if !strings.HasPrefix(got, "<critic_findings") {
		t.Errorf("expected XML tag prefix, got %q", got)
	}
}

func TestInjectCriticFindings_MultipleFindings(t *testing.T) {
	r := CriticResult{
		Verdict:  CriticReject,
		Findings: []string{"issue A", "issue B", "issue C"},
	}
	got := InjectCriticFindings(r)
	if !strings.Contains(got, "issue A") || !strings.Contains(got, "issue C") {
		t.Errorf("expected all findings in block, got %q", got)
	}
}

// ── RunCriticGate (non-empty diff via mock) ───────────────────────────────────

func TestRunCriticGate_NonEmptyDiff_MockApprove(t *testing.T) {
	orig := runCriticChatFn
	t.Cleanup(func() { runCriticChatFn = orig })

	runCriticChatFn = func(ctx *ChatContext, input string) {
		// Simulate reviewer returning APPROVE.
		ctx.OnChunk("APPROVE\nLooks correct.", true)
	}

	ctx := &ChatContext{
		SessionID: "test-session",
		Cancel:    make(chan struct{}),
	}
	result := RunCriticGate(ctx, "+ func foo() {}")
	if !result.IsApproved() {
		t.Errorf("expected APPROVE from mock reviewer, got %v", result.Verdict)
	}
}

func TestRunCriticGate_NonEmptyDiff_MockReject(t *testing.T) {
	orig := runCriticChatFn
	t.Cleanup(func() { runCriticChatFn = orig })

	runCriticChatFn = func(ctx *ChatContext, input string) {
		ctx.OnChunk("REJECT\n- main.go:5: nil deref\n- handler.go:12: no error check", true)
	}

	ctx := &ChatContext{
		SessionID: "test-session",
		Cancel:    make(chan struct{}),
	}
	result := RunCriticGate(ctx, "+ func bar() { _ = (*int)(nil) }")
	if result.IsApproved() {
		t.Error("expected REJECT verdict from mock reviewer")
	}
	if len(result.Findings) == 0 {
		t.Error("expected at least one finding in rejected result")
	}
}

func TestRunCriticGate_NilCancelAndError_DoNotPanic(t *testing.T) {
	orig := runCriticChatFn
	t.Cleanup(func() { runCriticChatFn = orig })

	runCriticChatFn = func(ctx *ChatContext, _ string) {
		// If OnError is nil and runCriticChatFn calls it, it must not panic.
		if ctx.Cancel == nil {
			t.Error("expected non-nil Cancel channel")
		}
		if ctx.OnError == nil {
			t.Error("expected non-nil OnError")
		}
		ctx.OnChunk("APPROVE", true)
	}

	// Deliberately provide no Cancel or OnError — RunCriticGate must guard these.
	ctx := &ChatContext{SessionID: "s"}
	result := RunCriticGate(ctx, "diff content")
	if !result.IsApproved() {
		t.Error("expected APPROVE")
	}
}

// ── extractFindings: colon-containing lines ───────────────────────────────────

func TestExtractFindings_ColonLines(t *testing.T) {
	output := "REJECT\nhandler.go:42: missing error return"
	findings := extractFindings(output)
	if len(findings) == 0 {
		t.Fatal("expected colon-line finding to be extracted")
	}
	if !strings.Contains(findings[0], "handler.go:42") {
		t.Errorf("expected file:line in finding, got %q", findings[0])
	}
}

func TestExtractFindings_RejectColonLine_Excluded(t *testing.T) {
	// A line starting with "REJECT:" should not be double-counted as a finding.
	output := "REJECT: code issues found\n- actual issue"
	findings := extractFindings(output)
	for _, f := range findings {
		if strings.HasPrefix(strings.ToUpper(f), "REJECT") {
			t.Errorf("REJECT line should not appear in findings, got %q", f)
		}
	}
}

// TestExtractFindings_EmptyLines covers the `if line == "" { continue }` branch
// by passing output that contains blank lines between findings.
func TestExtractFindings_EmptyLines(t *testing.T) {
	output := "REJECT\n\n- nil pointer\n\n- missing check"
	findings := extractFindings(output)
	if len(findings) < 2 {
		t.Fatalf("expected ≥2 findings even with blank lines, got %d: %v", len(findings), findings)
	}
}
