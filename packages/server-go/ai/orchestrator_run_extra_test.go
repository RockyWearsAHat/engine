package ai

import (
	"strings"
	"testing"
	"time"
)

func TestRunAutonomousProject_HappyPathCompletes(t *testing.T) {
	dir := setupPhasesDB(t)

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
		ChatFn: func(ctx *ChatContext, userMessage string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				ctx.OnChunk("builder completed step", false)
			case RoleModuleIndexer:
				ctx.OnChunk("| module | purpose |\n| app | demo |", false)
			case RoleReviewer:
				if strings.Contains(userMessage, "Final behavioral validation") {
					ctx.OnChunk("validated behavior\nAPPROVE", false)
					return
				}
				ctx.OnChunk("reviewed\nAPPROVE", false)
			}
		},
	}

	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("RunAutonomousProject: %v", err)
	}
	if state == nil {
		t.Fatal("expected state")
	}
	if state.CompletedAt == "" {
		t.Fatal("expected completion timestamp")
	}
	if len(state.Plan) != 1 || !state.Plan[0].Done {
		t.Fatalf("expected one completed step, got %+v", state.Plan)
	}
}

func TestRunAutonomousProject_ValidationRejectReopensStep(t *testing.T) {
	dir := setupPhasesDB(t)
	validateAttempts := 0

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "acme",
		Repo:               "demo",
		Brief:              "build a tiny app",
		SessionIDPrefix:    "orch-test",
		MaxOuterIterations: 12,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(ctx *ChatContext, userMessage string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				ctx.OnChunk("builder completed step", false)
			case RoleModuleIndexer:
				ctx.OnChunk("| module | purpose |\n| app | demo |", false)
			case RoleReviewer:
				if strings.Contains(userMessage, "Final behavioral validation") {
					validateAttempts++
					if validateAttempts == 1 {
						ctx.OnChunk("behavior failed\nREJECT: endpoint mismatch", false)
						return
					}
					ctx.OnChunk("behavior fixed\nAPPROVE", false)
					return
				}
				ctx.OnChunk("reviewed\nAPPROVE", false)
			}
		},
	}

	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("RunAutonomousProject: %v", err)
	}
	if state == nil {
		t.Fatal("expected state")
	}
	if len(state.Plan) != 1 || !state.Plan[0].Done {
		t.Fatalf("expected one completed step, got %+v", state.Plan)
	}
	if state.Plan[0].Attempts < 2 {
		t.Fatalf("expected reopened step to be attempted again, got %d", state.Plan[0].Attempts)
	}
	if validateAttempts < 2 {
		t.Fatalf("expected at least two validation attempts, got %d", validateAttempts)
	}
}

func TestRunAutonomousProject_SkipsExhaustedStep(t *testing.T) {
	dir := setupPhasesDB(t)
	orig := OrchestratorMaxStepAttempts
	OrchestratorMaxStepAttempts = 1
	defer func() { OrchestratorMaxStepAttempts = orig }()

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
		ChatFn: func(ctx *ChatContext, userMessage string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				// Emit nothing to force "builder produced no output" and trigger step skip.
			case RoleReviewer:
				if strings.Contains(userMessage, "Final behavioral validation") {
					ctx.OnChunk("validated despite skip\nAPPROVE", false)
				}
			}
		},
	}

	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("RunAutonomousProject: %v", err)
	}
	if state == nil {
		t.Fatal("expected state")
	}
	if len(state.Plan) != 1 {
		t.Fatalf("expected one step, got %d", len(state.Plan))
	}
	if !state.Plan[0].Done {
		t.Fatal("expected exhausted step to be marked done")
	}
	if !strings.Contains(state.Plan[0].LastFeedback, "skipped after 1 failed attempts") {
		t.Fatalf("expected skip feedback, got %q", state.Plan[0].LastFeedback)
	}
}

func TestRunAutonomousProject_CancelledBeforeLoop(t *testing.T) {
	dir := setupPhasesDB(t)
	cancel := make(chan struct{})
	close(cancel)

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "acme",
		Repo:               "demo",
		Brief:              "build a tiny app",
		SessionIDPrefix:    "orch-test",
		MaxOuterIterations: 8,
		Cancel:             cancel,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(ctx *ChatContext, _ string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			}
		},
	}

	_, err := RunAutonomousProject(cfg)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancelled error, got %v", err)
	}
}

func TestRunAutonomousProject_HitsSafetyCap(t *testing.T) {
	dir := setupPhasesDB(t)

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "acme",
		Repo:               "demo",
		Brief:              "build a tiny app",
		SessionIDPrefix:    "orch-test",
		MaxOuterIterations: 1,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(ctx *ChatContext, userMessage string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				ctx.OnChunk("builder completed step", false)
			case RoleReviewer:
				if strings.Contains(userMessage, "Final behavioral validation") {
					ctx.OnChunk("validated behavior\nAPPROVE", false)
					return
				}
				ctx.OnChunk("not acceptable\nREJECT: keep retrying", false)
			}
		},
	}

	_, err := RunAutonomousProject(cfg)
	if err == nil || !strings.Contains(err.Error(), "safety cap") {
		t.Fatalf("expected safety cap error, got %v", err)
	}
}

func TestRunAutonomousProject_InconclusiveReviewAdvances(t *testing.T) {
	dir := setupPhasesDB(t)

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
		ChatFn: func(ctx *ChatContext, userMessage string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				ctx.OnChunk("builder completed step", false)
			case RoleModuleIndexer:
				ctx.OnChunk("| module | purpose |\n| app | demo |", false)
			case RoleReviewer:
				if strings.Contains(userMessage, "Final behavioral validation") {
					ctx.OnChunk("validated behavior\nAPPROVE", false)
					return
				}
				ctx.OnChunk("unclear reviewer output", false)
			}
		},
	}

	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("RunAutonomousProject: %v", err)
	}
	if state == nil || len(state.Plan) != 1 {
		t.Fatalf("expected one step state, got %+v", state)
	}
	if !state.Plan[0].Done {
		t.Fatalf("expected step marked done after inconclusive review")
	}
	if !strings.Contains(state.Plan[0].LastFeedback, "Review inconclusive") {
		t.Fatalf("expected inconclusive marker in feedback, got %q", state.Plan[0].LastFeedback)
	}
}

func TestRunAutonomousProject_RedirectInjectedIntoBuilderPrompt(t *testing.T) {
	dir := setupPhasesDB(t)
	const redirectMsg = "please prioritize auth flow"
	sawRedirect := false
	redirected := false

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "acme",
		Repo:               "demo",
		Brief:              "build a tiny app",
		SessionIDPrefix:    "orch-test",
		MaxOuterIterations: 8,
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		OnPhase:    func(string, string) {},
		ChatFn: func(ctx *ChatContext, userMessage string) {
			switch ctx.Role {
			case RoleGriller:
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				if !redirected {
					if h := GetOrchestratorHandle(dir); h != nil {
						h.Redirect(redirectMsg)
						redirected = true
					}
				}
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				if strings.Contains(userMessage, redirectMsg) {
					sawRedirect = true
				}
				ctx.OnChunk("builder completed step", false)
			case RoleModuleIndexer:
				ctx.OnChunk("| module | purpose |\n| app | demo |", false)
			case RoleReviewer:
				ctx.OnChunk("APPROVE", false)
			}
		},
	}

	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("RunAutonomousProject: %v", err)
	}
	if state == nil || state.CompletedAt == "" {
		t.Fatalf("expected completed state, got %+v", state)
	}
	if !sawRedirect {
		t.Fatal("expected builder prompt to include redirected message")
	}
}

func TestRunAutonomousProject_CancelledWhilePaused(t *testing.T) {
	dir := setupPhasesDB(t)
	releaseChat := make(chan struct{})

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
		ChatFn: func(ctx *ChatContext, _ string) {
			switch ctx.Role {
			case RoleGriller:
				<-releaseChat
				ctx.OnChunk("# Design\nA tiny app.", false)
			case RolePRDWriter:
				ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
			case RolePlanner:
				ctx.OnChunk("1. Build app\n   Add app code\n   Acceptance: `echo ok` returns 0\n", false)
			case RoleAutonomousBuilder:
				ctx.OnChunk("builder completed step", false)
			case RoleReviewer:
				ctx.OnChunk("APPROVE", false)
			}
		},
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := RunAutonomousProject(cfg)
		errCh <- err
	}()

	var h *OrchestratorHandle
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h = GetOrchestratorHandle(dir)
		if h != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h == nil {
		t.Fatal("expected orchestrator handle to be registered")
	}

	h.Pause()
	close(releaseChat)
	time.Sleep(120 * time.Millisecond)
	h.Stop()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled error, got %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for orchestrator to return")
	}
}
