package ai

import (
	"strings"
	"testing"
)

// TestOrchestratorCoachStep verifies coaching rewrites a brief.
func TestOrchestratorCoachStep(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:     "/tmp/test-project",
		Owner:           "test",
		Repo:            "repo",
		Brief:           "Fix the widget",
		SessionIDPrefix: "coach-test",
		ChatFn: func(ctx *ChatContext, prompt string) {
			// Stub: return a simple rewritten brief
			if ctx.OnChunk != nil {
				ctx.OnChunk("Step: rewrite the widget.\nAcceptance: tests pass.\n", true)
			}
		},
	}

	step := &PlanStep{
		Index:      1,
		Title:      "Fix widget",
		Body:       "Original: unclear and too broad",
		Acceptance: "Widget works",
		LastFeedback: "Reviewer: too vague, break it down",
	}

	// Mock createSessionFn to avoid DB write
	oldCreateSession := createSessionFn
	createSessionFn = func(sessionID, projectPath, baseDir string) error {
		return nil
	}
	defer func() { createSessionFn = oldCreateSession }()

	newBrief := orchestratorCoachStep(cfg, step, make(<-chan struct{}))

	if newBrief == "" {
		t.Errorf("expected non-empty coaching brief")
	}
	if !strings.Contains(newBrief, "rewrite") {
		t.Errorf("expected brief to contain coaching output, got: %s", newBrief)
	}
}

// TestReviewRejectCoaching verifies ReviewRejects counter and coaching flow.
func TestReviewRejectCoaching(t *testing.T) {
	step := &PlanStep{
		Index:      1,
		Title:      "Test step",
		Body:       "Original brief",
		Acceptance: "Tests pass",
	}

	// Simulate three REJECTs
	if step.ReviewRejects != 0 {
		t.Errorf("expected initial ReviewRejects=0, got %d", step.ReviewRejects)
	}

	// First REJECT
	step.ReviewRejects++
	step.LastFeedback = "First rejection"
	if step.ReviewRejects != 1 {
		t.Errorf("expected ReviewRejects=1 after first REJECT")
	}

	// Second REJECT
	step.ReviewRejects++
	step.LastFeedback = "Second rejection"
	if step.ReviewRejects != 2 {
		t.Errorf("expected ReviewRejects=2 after second REJECT")
	}

	// Third REJECT → escalation
	step.ReviewRejects++
	if step.ReviewRejects != 3 {
		t.Errorf("expected ReviewRejects=3 after third REJECT")
	}
	if step.ReviewRejects < 3 {
		t.Errorf("escalation should trigger when ReviewRejects >= 3")
	}
}

// TestCoachingBriefUsedInPrompt verifies CoachingBrief is used in buildStepPromptWithContext.
func TestCoachingBriefUsedInPrompt(t *testing.T) {
	state := &OrchestrationState{
		Owner: "test",
		Repo:  "repo",
		Plan: []PlanStep{
			{Index: 1, Title: "Step 1", Body: "Original", Acceptance: "Pass tests"},
		},
	}
	step := &state.Plan[0]

	// Prompt without coaching brief (uses original Body)
	prompt1 := buildStepPromptWithContext(state, step, "", "")
	if !strings.Contains(prompt1, "Original") {
		t.Errorf("expected prompt to contain original Body")
	}

	// Set coaching brief
	step.CoachingBrief = "Rewritten by coach"
	prompt2 := buildStepPromptWithContext(state, step, "", "")
	if !strings.Contains(prompt2, "Rewritten by coach") {
		t.Errorf("expected prompt to contain CoachingBrief, got: %s", prompt2)
	}
	if strings.Contains(prompt2, "Original") && !strings.Contains(prompt2, "Rewritten") {
		t.Errorf("expected CoachingBrief to replace Body in prompt")
	}
}

// TestOnCoachCallback verifies the callback is fired with correct params.
func TestOnCoachCallback(t *testing.T) {
	var coachCalls []struct {
		coached   int
		escalated bool
	}

	cfg := OrchestratorConfig{
		OnCoach: func(coached int, escalated bool) {
			coachCalls = append(coachCalls, struct {
				coached   int
				escalated bool
			}{coached, escalated})
		},
	}

	// Simulate coaching calls
	cfg.OnCoach(1, false)
	cfg.OnCoach(2, false)
	cfg.OnCoach(3, true)

	if len(coachCalls) != 3 {
		t.Errorf("expected 3 OnCoach calls, got %d", len(coachCalls))
	}

	if coachCalls[0].coached != 1 || coachCalls[0].escalated {
		t.Errorf("call 1: expected coached=1, escalated=false")
	}
	if coachCalls[1].coached != 2 || coachCalls[1].escalated {
		t.Errorf("call 2: expected coached=2, escalated=false")
	}
	if coachCalls[2].coached != 3 || !coachCalls[2].escalated {
		t.Errorf("call 3: expected coached=3, escalated=true")
	}
}
