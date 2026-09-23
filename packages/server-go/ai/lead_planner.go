package ai

import (
	"fmt"
	"strings"
)

// SpecialistRequest describes a needed specialist agent for the current task.
// The lead agent uses this to bootstrap the team via EngineTeamSet.
type SpecialistRequest struct {
	ID       string `json:"id"`        // Unique identifier within the team (e.g., "tester", "architect")
	Role     string `json:"role"`      // System role from the role catalog
	TaskBrief string `json:"taskBrief"` // Concise task definition for the specialist
	Context  map[string]string `json:"context,omitempty"` // Contextual hints (e.g., {"coverage_threshold": "100"})
}

// TeamComposition describes the full set of specialists needed for a project objective.
type TeamComposition struct {
	LeadRole      string                `json:"leadRole"`
	Specialists   []SpecialistRequest   `json:"specialists"`
	CoordinationStyle string             `json:"coordinationStyle"` // "lead-only", "peer-aware", etc.
}

// PlanTeamComposition analyzes an autonomous handoff and determines what specialists
// the team needs. Returns a TeamComposition ready to bootstrap.
//
// Task classification:
//   - "build/implement/scaffold" → Planner, Scaffolder, Implementer, Tester, Reviewer
//   - "test/debug/fix" → Tester, Debugger, possibly Implementer
//   - "document/design/review" → Reviewer, Documenter, Architect
//   - "play-test/explore" → AutonomousBuilder with play-tester flavor
//
// The lead agent uses this result to invoke EngineTeamSet and bootstrap the team.
func PlanTeamComposition(handoff *AutonomyHandoff, profile *ProjectProfile) (TeamComposition, error) {
	if handoff == nil {
		return TeamComposition{}, fmt.Errorf("handoff required")
	}

	objective := strings.TrimSpace(handoff.Objective.Statement)
	if objective == "" {
		return TeamComposition{}, fmt.Errorf("objective statement required")
	}

	reqType := classifyObjective(objective, handoff.ExecutionIntent)
	composition := TeamComposition{
		LeadRole:            "interactive",
		CoordinationStyle:   "lead-only",
		Specialists:         []SpecialistRequest{},
	}

	// Task-driven specialist selection.
	// Each specialist is created with specific responsibility.
	switch reqType {
	case "build", "implement", "scaffold":
		// Full build pipeline: plan → scaffold → implement → test → review
		composition.Specialists = append(composition.Specialists,
			SpecialistRequest{
				ID:   "planner",
				Role: "planner",
				TaskBrief: fmt.Sprintf("Create a numbered implementation plan for: %s", objective),
				Context: map[string]string{"output": "numbered plan only"},
			},
			SpecialistRequest{
				ID:   "scaffolder",
				Role: "scaffolder",
				TaskBrief: "Create compile-valid file stubs for the first plan step",
				Context: map[string]string{"targets": "minimum required files"},
			},
			SpecialistRequest{
				ID:   "implementer",
				Role: "implementer",
				TaskBrief: "Implement production code for the assigned module",
				Context: map[string]string{"principle": "CS3500 discipline"},
			},
			SpecialistRequest{
				ID:   "tester",
				Role: "tester",
				TaskBrief: "Write tests and iterate until suite passes",
				Context: map[string]string{"coverage": "100%"},
			},
			SpecialistRequest{
				ID:   "reviewer",
				Role: "reviewer",
				TaskBrief: "Review for correctness, design, and CS3500 principles",
				Context: map[string]string{"gate": "APPROVE/REJECT"},
			},
		)

	case "test", "debug", "fix":
		// Debugging & testing pipeline
		composition.Specialists = append(composition.Specialists,
			SpecialistRequest{
				ID:   "tester",
				Role: "tester",
				TaskBrief: fmt.Sprintf("Debug and fix: %s", objective),
				Context: map[string]string{"mode": "debug+fix"},
			},
			SpecialistRequest{
				ID:   "reviewer",
				Role: "reviewer",
				TaskBrief: "Verify fix is correct and no regressions introduced",
				Context: map[string]string{"gate": "APPROVE/REJECT"},
			},
		)

	case "design", "architect", "review":
		// Architecture & review pipeline
		composition.Specialists = append(composition.Specialists,
			SpecialistRequest{
				ID:   "architect",
				Role: "architect",
				TaskBrief: fmt.Sprintf("Architectural review: %s", objective),
				Context: map[string]string{"principle": "deep modules, clear interfaces"},
			},
			SpecialistRequest{
				ID:   "reviewer",
				Role: "reviewer",
				TaskBrief: "Final review of architectural decisions",
				Context: map[string]string{"gate": "APPROVE/REJECT"},
			},
		)

	case "document":
		// Documentation pipeline
		composition.Specialists = append(composition.Specialists,
			SpecialistRequest{
				ID:   "documenter",
				Role: "documenter",
				TaskBrief: fmt.Sprintf("Update WORKING_BEHAVIORS and docs: %s", objective),
				Context: map[string]string{"style": "existing"},
			},
		)

	default:
		// Generic: just planner and reviewer
		composition.Specialists = append(composition.Specialists,
			SpecialistRequest{
				ID:   "planner",
				Role: "planner",
				TaskBrief: fmt.Sprintf("Plan: %s", objective),
				Context: map[string]string{"output": "numbered plan only"},
			},
		)
	}

	// Optionally add play-tester if the profile suggests interactive/web app project
	if profile != nil && (profile.Type == ProjectTypeWebApp || profile.Type == ProjectTypeService) {
		composition.Specialists = append(composition.Specialists,
			SpecialistRequest{
				ID:   "play-tester",
				Role: "autonomous-builder", // Uses AutonomousBuilder role as foundation
				TaskBrief: "User-experience discovery: run the app, exercise features, report findings",
				Context: map[string]string{"mode": "play-test", "discovery": "true"},
			},
		)
	}

	return composition, nil
}

// classifyObjective maps an objective statement to a task type.
// Uses similar keywords as the request classifier but optimized for autonomous
// multi-agent decision-making.
func classifyObjective(objective string, execIntent ExecutionIntent) string {
	lower := strings.ToLower(objective)

	// Build/implement keywords
	buildKeywords := []string{
		"build", "implement", "scaffold", "create", "add", "feature",
		"new module", "new component", "initialize", "setup",
	}
	for _, kw := range buildKeywords {
		if strings.Contains(lower, kw) {
			return "build"
		}
	}

	// Test/debug keywords
	testKeywords := []string{
		"test", "debug", "fix", "bug", "error", "fail", "crash",
		"regression", "patch", "hotfix",
	}
	for _, kw := range testKeywords {
		if strings.Contains(lower, kw) {
			return "test"
		}
	}

	// Design/architecture keywords
	designKeywords := []string{
		"design", "architect", "review", "refactor", "pattern",
		"structure", "module", "interface", "abstract",
	}
	for _, kw := range designKeywords {
		if strings.Contains(lower, kw) {
			return "design"
		}
	}

	// Documentation keywords
	docKeywords := []string{
		"document", "doc", "readme", "behavior", "spec",
		"guide", "comment", "explain",
	}
	for _, kw := range docKeywords {
		if strings.Contains(lower, kw) {
			return "document"
		}
	}

	return "generic"
}
