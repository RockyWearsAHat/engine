package ai

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// Haiku pin must reach ChatFn on the event path. Serial path set it in
// stageChatContextCreation; event path (newPhaseChat) dropped it, so a
// TaskMode dispatch that asked for haiku ran every phase at env default.
// Two call sites share newPhaseChat: planner phases and TeamWorker.runStep.
// Both checked here.
func TestEventOrchestrator_RequestedModelReachesPlannerPhase(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	_, _ = RunEventOrchestratorAsState(OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "pin",
		TaskMode:           true,
		TaskID:             "pin-task",
		RequestedModel:     "claude-haiku-4-5",
		MaxOuterIterations: 1,
		OnProgress:         func(string) {},
		OnPhase:            func(string, string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, prompt string) {
			mu.Lock()
			seen = append(seen, c.ModelOverride)
			mu.Unlock()
			if strings.Contains(prompt, plannerPromptMarker) {
				c.OnChunk(onePlanStep, false)
				return
			}
			c.OnChunk("VALIDATION_PASSED", false)
		},
	})
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("ChatFn never called")
	}
	for i, m := range seen {
		if m != "claude-haiku-4-5" {
			t.Fatalf("call %d: ModelOverride = %q, want haiku pin", i, m)
		}
	}
}

func TestTeamWorker_RunStepCarriesRequestedModel(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	if err := brain.UpdatePlan([]PlanStep{{Index: 1, Title: "Pin", Body: "x", Acceptance: "y"}}); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	if err := brain.AddTeam("team-pin-0", "general", []int{0}, nil); err != nil {
		t.Fatalf("add team: %v", err)
	}
	var mu sync.Mutex
	var seen []string
	worker := &TeamWorker{
		teamID: "team-pin-0",
		role:   "general",
		steps:  []int{0},
		brain:  brain,
		bus:    NewEventBus(),
		comms:  NewAgentCommsHub(),
		cfg: OrchestratorConfig{
			ProjectPath:     projectDir,
			SessionIDPrefix: "pin",
			TaskMode:        true,
			RequestedModel:  "claude-haiku-4-5",
			ChatFn: func(c *ChatContext, _ string) {
				mu.Lock()
				seen = append(seen, c.ModelOverride)
				mu.Unlock()
				c.OnChunk("still working", false)
			},
		},
		ctx:      context.Background(),
		maxTurns: 1,
	}
	worker.run()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("ChatFn never called from runStep")
	}
	for i, m := range seen {
		if m != "claude-haiku-4-5" {
			t.Fatalf("runStep call %d: ModelOverride = %q, want haiku pin", i, m)
		}
	}
}
