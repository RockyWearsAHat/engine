package ai

import "testing"

// A plan without runnable Acceptance commands fails the structural contract.
// A chatbot help-menu cannot satisfy this contract because it has no commands.
func TestValidatePlanQuality_RejectsPlanWithoutAcceptanceCommands(t *testing.T) {
	// Real-world example: Qwen 3.6 produced this as a "plan" — no Acceptance lines.
	garbage := []PlanStep{
		{Index: 1, Title: "**Run/Build the Project**: `npm run dev` / `npm run build`"},
		{Index: 2, Title: "**Optimize node_modules**", Body: "Check for bloated dependencies"},
		{Index: 3, Title: "**Debug a Build/Test Error**", Body: "Share the error log."},
	}
	if err := validatePlanQuality(garbage); err == nil {
		t.Fatal("expected plan without Acceptance commands to be rejected, but it passed")
	}
}

func TestValidatePlanQuality_AcceptsPlanWithRunnableAcceptance(t *testing.T) {
	real := []PlanStep{
		{
			Index:      1,
			Title:      "Scaffold: package.json, tsconfig, canvas entry",
			Body:       "Create Vite + TypeScript project structure.",
			Acceptance: "`npm run build` exits 0",
		},
		{
			Index:      2,
			Title:      "Core game loop and input system",
			Body:       "Game class with delta-time loop. InputManager.",
			Acceptance: "`npm test` shows InputManager spec passing",
		},
	}
	if err := validatePlanQuality(real); err != nil {
		t.Fatalf("expected real plan to pass, got: %v", err)
	}
}

func TestValidatePlanQuality_RejectsEmptyPlan(t *testing.T) {
	if err := validatePlanQuality(nil); err == nil {
		t.Fatal("expected empty plan to be rejected")
	}
}

func TestHasRunnableAcceptance(t *testing.T) {
	cases := map[string]bool{
		"`go test ./...` exits 0":                       true,
		"npm run build exits cleanly":                   true,
		"curl /health returns 200":                      true,
		"./bin/cli --help prints expected usage":        true,
		"python3 -m pytest tests/":                      true,
		"":                                              false,
		"Share the error log and I'll pinpoint it":      false,
		"I can map out src/main.ts for you":             false,
		"💡 Just tell me what you're trying to accomplish": false,
	}
	for input, want := range cases {
		if got := hasRunnableAcceptance(input); got != want {
			t.Errorf("hasRunnableAcceptance(%q) = %v, want %v", input, got, want)
		}
	}
}
