package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTeamDispatcher_DispatchesWorkerWithAgentComms(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	if err := brain.UpdatePlan([]PlanStep{{Index: 1, Title: "Add API", Body: "Implement endpoint", Acceptance: "`go test ./...` passes"}}); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	if err := brain.AddTeam("team-api-0", "api", []int{0}, nil); err != nil {
		t.Fatalf("add team: %v", err)
	}

	bus := NewEventBus()
	teamDone := bus.Subscribe(EventTeamDone, 1)
	comms := NewAgentCommsHub()
	seenPrompt := make(chan string, 1)
	seenTurns := make(chan int, 1)
	cfg := OrchestratorConfig{
		ProjectPath:     projectDir,
		SessionIDPrefix: "test",
		ChatFn: func(ctx *ChatContext, prompt string) {
			if ctx.AgentName != "team-api-0" {
				seenPrompt <- "wrong agent: " + ctx.AgentName
				return
			}
			if ctx.AgentComms != comms {
				seenPrompt <- "missing comms"
				return
			}
			seenTurns <- ctx.MaxTurns
			seenPrompt <- prompt
			ctx.OnChunk("signal_done", false)
		},
	}

	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	if err := dispatcher.DispatchTeam("team-api-0"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	select {
	case prompt := <-seenPrompt:
		if !strings.Contains(prompt, "TDD discipline") || !strings.Contains(prompt, "TEAM IDENTITY:") {
			t.Fatalf("prompt missing shared builder contract: %s", prompt)
		}
		if !strings.Contains(prompt, "team-api-0 (api)") {
			t.Fatalf("prompt missing team identity: %s", prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("chat function was not called")
	}

	select {
	case turns := <-seenTurns:
		if turns == 1 {
			t.Fatal("team worker should not clamp the builder to one turn")
		}
	case <-time.After(time.Second):
		t.Fatal("did not capture max turn budget")
	}

	select {
	case <-teamDone:
	case <-time.After(time.Second):
		t.Fatal("team did not complete")
	}

	team, err := brain.GetTeam("team-api-0")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Status != "done" {
		t.Fatalf("expected done team, got %q", team.Status)
	}
	agents := comms.List()
	if len(agents) != 1 || agents[0].ID != "team-api-0" || agents[0].Status != "done" {
		t.Fatalf("team not registered as done in comms: %+v", agents)
	}

	if err := dispatcher.DispatchTeam("team-api-0"); err == nil {
		t.Fatal("expected duplicate dispatch to fail")
	}
	dispatcher.Wait()
	dispatcher.Stop()
	if dispatcher.ActiveTeams() != 0 {
		t.Fatalf("expected no active teams after stop, got %d", dispatcher.ActiveTeams())
	}
}

func TestTeamDispatcher_DispatchErrors(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	if err := brain.AddTeam("blocked", "api", []int{0}, []string{"missing"}); err != nil {
		t.Fatalf("add blocked team: %v", err)
	}
	dispatcher := NewTeamDispatcher(brain, NewEventBus(), OrchestratorConfig{ProjectPath: projectDir}, 4, NewAgentCommsHub())
	if err := dispatcher.DispatchTeam("missing"); err == nil {
		t.Fatal("expected missing team error")
	}
	if err := dispatcher.DispatchTeam("blocked"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked team error, got %v", err)
	}
}

func TestTeamDispatcher_ActiveTeamsCountsOnlyUncanceledWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := NewTeamDispatcher(nil, NewEventBus(), OrchestratorConfig{}, 4, nil)
	dispatcher.workers["manual"] = &TeamWorker{ctx: ctx}
	if got := dispatcher.ActiveTeams(); got != 1 {
		t.Fatalf("expected one active worker, got %d", got)
	}
	cancel()
	if got := dispatcher.ActiveTeams(); got != 0 {
		t.Fatalf("expected canceled worker to be inactive, got %d", got)
	}
}

func TestTeamWorker_RunHandlesOutOfRangeAndFailure(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	if err := brain.AddTeam("team-general-0", "general", []int{99}, nil); err != nil {
		t.Fatalf("add team: %v", err)
	}
	worker := &TeamWorker{
		teamID:   "team-general-0",
		role:     "general",
		steps:    []int{99},
		brain:    brain,
		bus:      NewEventBus(),
		comms:    NewAgentCommsHub(),
		cfg:      OrchestratorConfig{ProjectPath: projectDir},
		ctx:      context.Background(),
		maxTurns: 1,
	}
	worker.run()
	team, err := brain.GetTeam("team-general-0")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Status != "done" {
		t.Fatalf("out-of-range-only worker should finish done, got %q", team.Status)
	}

	if err := brain.UpdatePlan([]PlanStep{{Index: 1, Title: "Stuck", Body: "No signal", Acceptance: "`go test ./...` passes"}}); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	if err := brain.AddTeam("team-stuck-0", "general", []int{0}, nil); err != nil {
		t.Fatalf("add stuck team: %v", err)
	}
	worker = &TeamWorker{
		teamID:   "team-stuck-0",
		role:     "general",
		steps:    []int{0},
		brain:    brain,
		bus:      NewEventBus(),
		comms:    NewAgentCommsHub(),
		cfg:      OrchestratorConfig{ProjectPath: projectDir, SessionIDPrefix: "stuck", ChatFn: func(ctx *ChatContext, _ string) { ctx.OnChunk("still working", false) }},
		ctx:      context.Background(),
		maxTurns: 2,
	}
	worker.run()
	team, err = brain.GetTeam("team-stuck-0")
	if err != nil {
		t.Fatalf("get stuck team: %v", err)
	}
	if team.Status != "failed" || !strings.Contains(team.Feedback, "timed out") {
		t.Fatalf("expected failed stuck team with timeout feedback, got %+v", team)
	}
}

func TestTeamWorker_RunStepCancellation(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := &TeamWorker{
		teamID:   "team-canceled-0",
		role:     "general",
		brain:    brain,
		bus:      NewEventBus(),
		cfg:      OrchestratorConfig{ProjectPath: projectDir, SessionIDPrefix: "cancel"},
		ctx:      ctx,
		maxTurns: 1,
	}
	err = worker.runStep(&PlanStep{Index: 1, Title: "Canceled"}, 0)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled runStep, got %v", err)
	}
}

func TestTeamWorker_ReportsProgressToLead(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}

	// Plan with 2 steps to verify progress reporting
	if err := brain.UpdatePlan([]PlanStep{
		{Index: 0, Title: "Step One", Body: "First step", Acceptance: "pass"},
		{Index: 1, Title: "Step Two", Body: "Second step", Acceptance: "pass"},
	}); err != nil {
		t.Fatalf("update plan: %v", err)
	}

	if err := brain.AddTeam("team-multi-0", "general", []int{0, 1}, nil); err != nil {
		t.Fatalf("add team: %v", err)
	}

	bus := NewEventBus()
	comms := NewAgentCommsHub()
	comms.Register("lead", "orchestrator", "running")

	cfg := OrchestratorConfig{
		ProjectPath:     projectDir,
		SessionIDPrefix: "test",
		ChatFn: func(ctx *ChatContext, _ string) {
			// Signal done immediately to complete steps
			ctx.OnChunk("signal_done", false)
		},
	}

	dispatcher := NewTeamDispatcher(brain, bus, cfg, 4, comms)
	if err := dispatcher.DispatchTeam("team-multi-0"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Wait for team to complete
	dispatcher.Wait()

	// Check that lead received progress messages
	leadMessages := comms.Inbox("lead", false)
	if len(leadMessages) < 2 {
		t.Fatalf("expected at least 2 progress messages to lead, got %d", len(leadMessages))
	}

	// Verify message content
	if !strings.Contains(leadMessages[0].Body, "Step 1/2") {
		t.Errorf("first message should contain 'Step 1/2', got: %s", leadMessages[0].Body)
	}
	if !strings.Contains(leadMessages[0].Body, "Step One") {
		t.Errorf("first message should contain step title, got: %s", leadMessages[0].Body)
	}

	if !strings.Contains(leadMessages[1].Body, "Step 2/2") {
		t.Errorf("second message should contain 'Step 2/2', got: %s", leadMessages[1].Body)
	}
	if !strings.Contains(leadMessages[1].Body, "Step Two") {
		t.Errorf("second message should contain step title, got: %s", leadMessages[1].Body)
	}

	dispatcher.Stop()
}
