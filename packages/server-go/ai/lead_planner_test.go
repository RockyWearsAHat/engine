package ai

import (
	"strings"
	"testing"
)

func TestPlanTeamComposition_BuildTask(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "implement a new search feature",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	if composition.LeadRole != "interactive" {
		t.Errorf("LeadRole: got %s, want interactive", composition.LeadRole)
	}

	if len(composition.Specialists) < 5 {
		t.Errorf("Specialists count: got %d, want at least 5", len(composition.Specialists))
	}

	// Verify expected roles for build task
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if !roles["planner"] || !roles["scaffolder"] || !roles["implementer"] || !roles["tester"] || !roles["reviewer"] {
		t.Errorf("Missing required roles: planner=%v, scaffolder=%v, implementer=%v, tester=%v, reviewer=%v",
			roles["planner"], roles["scaffolder"], roles["implementer"], roles["tester"], roles["reviewer"])
	}
}

func TestPlanTeamComposition_TestTask(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "debug and fix the login test failures",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	if len(composition.Specialists) < 2 {
		t.Errorf("Specialists count: got %d, want at least 2", len(composition.Specialists))
	}

	// Verify tester role is included
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if !roles["tester"] {
		t.Errorf("Missing tester role")
	}
}

func TestPlanTeamComposition_ArchitectTask(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "review and refactor the module architecture",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	if len(composition.Specialists) < 1 {
		t.Errorf("Specialists count: got %d, want at least 1", len(composition.Specialists))
	}

	// Verify architect or reviewer is included
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if !roles["architect"] && !roles["reviewer"] {
		t.Errorf("Missing architect or reviewer role")
	}
}

func TestPlanTeamComposition_NoObjective(t *testing.T) {
	handoff := &AutonomyHandoff{}

	_, err := PlanTeamComposition(handoff, nil)
	if err == nil || !strings.Contains(err.Error(), "objective") {
		t.Errorf("Expected error about objective, got: %v", err)
	}
}

func TestPlanTeamComposition_SpecialistHasContext(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "implement the new auth module",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	// Find tester specialist
	var tester *SpecialistRequest
	for i := range composition.Specialists {
		if composition.Specialists[i].Role == "tester" {
			tester = &composition.Specialists[i]
			break
		}
	}

	if tester == nil {
		t.Fatalf("Tester specialist not found")
	}

	// Verify it has task brief and context
	if tester.TaskBrief == "" {
		t.Errorf("TaskBrief empty")
	}
	if tester.Context == nil || tester.Context["coverage"] != "100%" {
		t.Errorf("Expected coverage context, got: %v", tester.Context)
	}
}

func TestClassifyObjective(t *testing.T) {
	tests := []struct {
		objective string
		wantType  string
	}{
		{"build the dashboard", "build"},
		{"implement the search feature", "build"},
		{"scaffold the database module", "build"},
		{"debug the test failures", "test"},
		{"fix the race condition", "test"},
		{"review the architecture", "design"},
		{"refactor the handler module", "design"},
		{"update the documentation", "document"},
		{"write README for the project", "document"},
		{"something completely random", "generic"},
	}

	for _, tt := range tests {
		t.Run(tt.objective, func(t *testing.T) {
			got := classifyObjective(tt.objective, ExecutionIntent{})
			if got != tt.wantType {
				t.Errorf("classifyObjective(%q) = %q, want %q", tt.objective, got, tt.wantType)
			}
		})
	}
}

func TestPlanTeamComposition_DocumentTask(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "update the documentation and README",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	if len(composition.Specialists) < 1 {
		t.Errorf("Specialists count: got %d, want at least 1", len(composition.Specialists))
	}

	// Verify documenter role is included
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if !roles["documenter"] {
		t.Errorf("Missing documenter role")
	}
}

func TestPlanTeamComposition_NilHandoff(t *testing.T) {
	_, err := PlanTeamComposition(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "handoff") {
		t.Fatalf("expected handoff error, got %v", err)
	}
}

func TestPlanTeamComposition_GenericTask(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "something completely random",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	if composition.LeadRole != "interactive" {
		t.Fatalf("LeadRole = %q, want interactive", composition.LeadRole)
	}
	if composition.CoordinationStyle != "lead-only" {
		t.Fatalf("CoordinationStyle = %q, want lead-only", composition.CoordinationStyle)
	}
	if len(composition.Specialists) != 1 {
		t.Fatalf("expected one planner specialist for generic task, got %d", len(composition.Specialists))
	}
	if composition.Specialists[0].Role != "planner" {
		t.Fatalf("expected planner specialist for generic task, got %q", composition.Specialists[0].Role)
	}
}

func TestPlanTeamComposition_WebAppAddsPlayTester(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "build a new dashboard feature",
		},
	}

	profile := &ProjectProfile{
		Type: ProjectTypeWebApp,
	}

	composition, err := PlanTeamComposition(handoff, profile)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	// Should have build pipeline + play-tester
	if len(composition.Specialists) < 6 {
		t.Errorf("Specialists count: got %d, want at least 6 (build pipeline + play-tester)", len(composition.Specialists))
	}

	// Verify play-tester is included
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if !roles["autonomous-builder"] {
		t.Errorf("Missing autonomous-builder (play-tester) role for WebApp")
	}
}

func TestPlanTeamComposition_ServiceAddsPlayTester(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "implement a new API endpoint",
		},
	}

	profile := &ProjectProfile{
		Type: ProjectTypeService,
	}

	composition, err := PlanTeamComposition(handoff, profile)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	// Should have build pipeline + play-tester for service
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if !roles["autonomous-builder"] {
		t.Errorf("Missing autonomous-builder (play-tester) role for Service")
	}
}

func TestPlanTeamComposition_CliNoPlayTester(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "build the CLI command handler",
		},
	}

	profile := &ProjectProfile{
		Type: ProjectTypeCLI,
	}

	composition, err := PlanTeamComposition(handoff, profile)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	// Should have build pipeline but NO play-tester for CLI
	roles := make(map[string]bool)
	for _, s := range composition.Specialists {
		roles[s.Role] = true
	}
	if roles["autonomous-builder"] {
		t.Errorf("Unexpected autonomous-builder role for CLI project")
	}
}

func TestPlanTeamComposition_SpecialistTaskBriefsSet(t *testing.T) {
	handoff := &AutonomyHandoff{
		Objective: HandoffObjective{
			Statement: "build the search feature",
		},
	}

	composition, err := PlanTeamComposition(handoff, nil)
	if err != nil {
		t.Fatalf("PlanTeamComposition failed: %v", err)
	}

	// All specialists must have non-empty task brief
	for i, spec := range composition.Specialists {
		if spec.TaskBrief == "" {
			t.Errorf("Specialist %d (%s) has empty TaskBrief", i, spec.Role)
		}
		if spec.ID == "" {
			t.Errorf("Specialist %d (%s) has empty ID", i, spec.Role)
		}
	}
}
