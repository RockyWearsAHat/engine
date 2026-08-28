package ai

import (
	"strings"
	"sync"
	"testing"
)

// Role pin must reach ChatContext on the event path. Test that RequestedRole
// overrides the role and reaches planner phase via ChatFn callback.
func TestEventOrchestrator_RequestedRoleReachesPlannerPhase(t *testing.T) {
	var mu sync.Mutex
	var seen []AgentRole
	_, _ = RunEventOrchestratorAsState(OrchestratorConfig{
		ProjectPath:        t.TempDir(),
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "role",
		TaskMode:           true,
		TaskID:             "role-task",
		RequestedRole:      "design-reviewer",
		MaxOuterIterations: 1,
		OnProgress:         func(string) {},
		OnPhase:            func(string, string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, prompt string) {
			mu.Lock()
			seen = append(seen, c.Role)
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
	for i, r := range seen {
		if r != RoleDesignReviewer {
			t.Fatalf("call %d: Role = %v, want RoleDesignReviewer", i, r)
		}
	}
}
