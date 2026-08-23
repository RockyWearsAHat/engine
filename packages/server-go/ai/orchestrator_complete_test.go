package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedOrchestration writes a pre-built orchestration.json into the project's
// .engine directory so RunAutonomousProject resumes against it instead of
// planning from scratch.
func seedOrchestration(t *testing.T, dir string, state OrchestrationState) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed state: %v", err)
	}
	path := filepath.Join(dir, ".engine", orchestrationFile)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write seed orchestration.json: %v", err)
	}
}

// validateApproveChatFn returns a ChatFn that drives the early phases and makes
// the behavioral validator APPROVE. Used by the skip-block tests, which seed a
// finished plan so the loop goes straight to validation.
func validateApproveChatFn() func(*ChatContext, string) {
	return func(ctx *ChatContext, userMessage string) {
		switch ctx.Role {
		case RoleGriller:
			ctx.OnChunk("# Design\nA tiny app.", false)
		case RolePRDWriter:
			ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
		case RolePlanner:
			ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
		case RoleModuleIndexer:
			ctx.OnChunk("| module | purpose |\n| app | demo |", false)
		case RoleReviewer:
			if strings.Contains(userMessage, "Final behavioral validation") {
				ctx.OnChunk("validated behavior\nAPPROVE", false)
				return
			}
			ctx.OnChunk("reviewed\nAPPROVE", false)
		}
	}
}

// FIX A — negative control: validation passes but a step was skipped → the
// orchestrator must NOT report done.
func TestRunAutonomousProject_SkippedStepBlocksDone(t *testing.T) {
	dir := setupPhasesDB(t)
	seedOrchestration(t, dir, OrchestrationState{
		Owner: "acme",
		Repo:  "demo",
		Brief: "build a tiny app",
		Plan: []PlanStep{
			{Index: 1, Title: "Build app", Acceptance: "`echo ok` returns 0", Done: true},
			{Index: 2, Title: "Stuck step", Acceptance: "`echo ok` returns 0", Done: true, LastFeedback: "skipped after 5 failed attempts — no acceptance match"},
		},
	})

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "acme",
		Repo:               "demo",
		Brief:              "build a tiny app",
		SessionIDPrefix:    "orch-test",
		MaxOuterIterations: 8,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn:             validateApproveChatFn(),
	}

	state, err := RunAutonomousProject(cfg)
	if err == nil {
		t.Fatal("expected error when a skipped step remains, got nil")
	}
	if !strings.Contains(err.Error(), "skipped without satisfying acceptance") {
		t.Fatalf("expected skip-block error, got %v", err)
	}
	if state == nil {
		t.Fatal("expected state")
	}
	if state.CompletedAt != "" {
		t.Fatalf("expected no completion timestamp, got %q", state.CompletedAt)
	}
}

// FIX A — positive control: validation passes with no skipped steps → done.
func TestRunAutonomousProject_NoSkippedStepCompletes(t *testing.T) {
	dir := setupPhasesDB(t)
	seedOrchestration(t, dir, OrchestrationState{
		Owner: "acme",
		Repo:  "demo",
		Brief: "build a tiny app",
		Plan: []PlanStep{
			{Index: 1, Title: "Build app", Acceptance: "`echo ok` returns 0", Done: true},
		},
	})

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "acme",
		Repo:               "demo",
		Brief:              "build a tiny app",
		SessionIDPrefix:    "orch-test",
		MaxOuterIterations: 8,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn:             validateApproveChatFn(),
	}

	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("RunAutonomousProject: %v", err)
	}
	if state == nil || state.CompletedAt == "" {
		t.Fatalf("expected completed state, got %+v", state)
	}
}

func TestSkippedStepIndices(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{
		{Index: 1, Done: true},
		{Index: 2, Done: true, LastFeedback: "skipped after 5 failed attempts"},
		{Index: 3, Done: false},
		{Index: 4, Done: true, LastFeedback: "skipped after 2 failed attempts; last feedback: nope"},
	}}
	idxs := skippedStepIndices(state)
	if len(idxs) != 2 || idxs[0] != 2 || idxs[1] != 4 {
		t.Fatalf("expected [2 4], got %v", idxs)
	}
	if len(skippedStepIndices(&OrchestrationState{Plan: []PlanStep{{Index: 1, Done: true}}})) != 0 {
		t.Fatal("expected no skipped indices for a clean plan")
	}
}

// FIX B — INCOMPLETE critique triggers exactly one regenerate; the improved
// plan is the one stored.
func TestOrchestratorPlanPhase_CritiqueRegeneratesOnIncomplete(t *testing.T) {
	dir := setupPhasesDB(t)
	plannerCalls := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, userMessage string) {
			plannerCalls++
			switch {
			case strings.Contains(userMessage, "reviewing a build plan against its brief"):
				c.OnChunk("The plan misses authentication and has no tests.\nINCOMPLETE: missing auth, missing tests", false)
			case strings.Contains(userMessage, "ADDRESS THESE GAPS"):
				c.OnChunk("1. Build app with auth\n   Add auth module.\n   Acceptance: `go test ./auth/...` passes\n2. Add tests\n   Cover the app.\n   Acceptance: `go test ./...` passes\n", false)
			default:
				c.OnChunk("1. Build app\n   Add app code.\n   Acceptance: `echo ok` returns 0\n", false)
			}
		},
	}
	state := &OrchestrationState{Brief: "build a tiny app with auth and tests", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("orchestratorPlanPhase: %v", err)
	}
	if plannerCalls != 3 {
		t.Fatalf("expected 3 chat calls (plan + critique + regenerate), got %d", plannerCalls)
	}
	if len(state.Plan) != 2 {
		t.Fatalf("expected regenerated 2-step plan, got %d steps: %+v", len(state.Plan), state.Plan)
	}
	if !strings.Contains(state.Plan[0].Title, "auth") {
		t.Fatalf("expected improved plan to be stored, got %q", state.Plan[0].Title)
	}
}

// FIX B — COMPLETE critique keeps the original plan and runs no regenerate.
func TestOrchestratorPlanPhase_CritiqueCompleteKeepsOriginal(t *testing.T) {
	dir := setupPhasesDB(t)
	plannerCalls := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, userMessage string) {
			plannerCalls++
			if strings.Contains(userMessage, "reviewing a build plan against its brief") {
				c.OnChunk("Plan covers the brief fully.\nCOMPLETE", false)
				return
			}
			c.OnChunk("1. Build app\n   Add app code.\n   Acceptance: `echo ok` returns 0\n", false)
		},
	}
	state := &OrchestrationState{Brief: "build a tiny app", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("orchestratorPlanPhase: %v", err)
	}
	if plannerCalls != 2 {
		t.Fatalf("expected 2 chat calls (plan + critique), got %d", plannerCalls)
	}
	if len(state.Plan) != 1 || state.Plan[0].Title != "Build app" {
		t.Fatalf("expected original plan kept, got %+v", state.Plan)
	}
}

func TestParsePlanCritiqueVerdict(t *testing.T) {
	cases := []struct {
		in       string
		wantV    string
		wantGaps string
	}{
		{"reasoning\nCOMPLETE", "COMPLETE", ""},
		{"reasoning\n`COMPLETE`", "COMPLETE", ""},
		{"stuff\nINCOMPLETE: missing auth, missing tests", "INCOMPLETE", "missing auth, missing tests"},
		{"some prose with no verdict line", "COMPLETE", ""},
		{"", "COMPLETE", ""},
	}
	for _, c := range cases {
		v, g := parsePlanCritiqueVerdict(c.in)
		if v != c.wantV || g != c.wantGaps {
			t.Errorf("parsePlanCritiqueVerdict(%q) = (%q,%q), want (%q,%q)", c.in, v, g, c.wantV, c.wantGaps)
		}
	}
}

func TestBuildPlanCritiquePrompt(t *testing.T) {
	out := buildPlanCritiquePrompt("my brief", "1. step")
	if !strings.Contains(out, "my brief") || !strings.Contains(out, "1. step") {
		t.Fatalf("expected brief and plan in prompt, got %q", out)
	}
	if !strings.Contains(out, "COMPLETE") || !strings.Contains(out, "INCOMPLETE") {
		t.Fatalf("expected verdict instruction in prompt, got %q", out)
	}
}
