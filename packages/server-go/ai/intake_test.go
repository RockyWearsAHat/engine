package ai

import (
	"strings"
	"testing"
)

// ── DeriveVerificationStrategy ────────────────────────────────────────────────

func TestDeriveVerificationStrategy_WebApp(t *testing.T) {
	vs := DeriveVerificationStrategy(ProjectTypeWebApp)
	if !vs.UsesPlaywright {
		t.Error("web-app should use Playwright")
	}
	if vs.Port == 0 {
		t.Error("web-app should have a non-zero port")
	}
	if vs.CheckURL == "" {
		t.Error("web-app should have a check URL")
	}
}

func TestDeriveVerificationStrategy_RestAPI(t *testing.T) {
	vs := DeriveVerificationStrategy(ProjectTypeRestAPI)
	if vs.UsesPlaywright {
		t.Error("rest-api should not use Playwright")
	}
	if vs.CheckURL == "" {
		t.Error("rest-api should have a health check URL")
	}
	if len(vs.CheckCmds) == 0 {
		t.Error("rest-api should have check commands")
	}
}

func TestDeriveVerificationStrategy_CLI(t *testing.T) {
	vs := DeriveVerificationStrategy(ProjectTypeCLI)
	if vs.UsesPlaywright {
		t.Error("cli should not use Playwright")
	}
	if len(vs.CheckCmds) == 0 {
		t.Error("cli should have check commands")
	}
}

func TestDeriveVerificationStrategy_Library(t *testing.T) {
	vs := DeriveVerificationStrategy(ProjectTypeLibrary)
	if vs.UsesPlaywright {
		t.Error("library should not use Playwright")
	}
	if len(vs.CheckCmds) == 0 {
		t.Error("library should have check commands")
	}
}

func TestDeriveVerificationStrategy_Service(t *testing.T) {
	vs := DeriveVerificationStrategy(ProjectTypeService)
	if vs.UsesPlaywright {
		t.Error("service should not use Playwright")
	}
	if vs.StartCmd == "" {
		t.Error("service should have a start command")
	}
}

func TestDeriveVerificationStrategy_Unknown(t *testing.T) {
	vs := DeriveVerificationStrategy(ProjectTypeUnknown)
	if vs.UsesPlaywright {
		t.Error("unknown should not use Playwright")
	}
}

// ── ParseProjectProfileJSON ───────────────────────────────────────────────────

func TestParseProjectProfileJSON_CleanJSON(t *testing.T) {
	raw := `{
		"projectPath": "/project/stripe",
		"type": "web-app",
		"doneDefinition": ["checkout flow works", "payments succeed"],
		"deployTarget": "Vercel",
		"verification": {
			"usesPlaywright": true,
			"startCmd": "pnpm dev",
			"checkURL": "http://localhost:3000",
			"port": 3000,
			"checkCmds": []
		},
		"liveCheckCmd": "curl -sf http://localhost:3000",
		"workingBehaviors": ["User can complete a purchase"]
	}`

	profile, err := ParseProjectProfileJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.Type != ProjectTypeWebApp {
		t.Errorf("type = %q, want web-app", profile.Type)
	}
	if !profile.Verification.UsesPlaywright {
		t.Error("verification.usesPlaywright should be true")
	}
	if len(profile.DoneDefinition) != 2 {
		t.Errorf("doneDefinition len = %d, want 2", len(profile.DoneDefinition))
	}
}

func TestParseProjectProfileJSON_WithSurroundingProse(t *testing.T) {
	raw := `Here is the extracted profile:

{"projectPath":"/p","type":"cli","doneDefinition":[],"deployTarget":"local","verification":{"usesPlaywright":false,"startCmd":"","checkURL":"","port":0,"checkCmds":["./bin/app --version"]},"liveCheckCmd":"","workingBehaviors":[]}

Let me know if you need more details.`

	profile, err := ParseProjectProfileJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.Type != ProjectTypeCLI {
		t.Errorf("type = %q, want cli", profile.Type)
	}
}

func TestParseProjectProfileJSON_AppliesDefaultVerification(t *testing.T) {
	// When LLM returns a profile with empty verification, defaults should be applied.
	raw := `{"projectPath":"/p","type":"rest-api","doneDefinition":[],"deployTarget":"local","verification":{"usesPlaywright":false,"startCmd":"","checkURL":"","port":0,"checkCmds":[]},"liveCheckCmd":"","workingBehaviors":[]}`

	profile, err := ParseProjectProfileJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.Verification.CheckURL == "" {
		t.Error("expected default CheckURL to be applied for rest-api")
	}
}

func TestParseProjectProfileJSON_NoJSON_Error(t *testing.T) {
	_, err := ParseProjectProfileJSON("no json here at all")
	if err == nil {
		t.Error("expected error for no JSON, got nil")
	}
	if !strings.Contains(err.Error(), "no JSON object found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseProjectProfileJSON_UnterminatedJSON_Error(t *testing.T) {
	_, err := ParseProjectProfileJSON(`{"type":"web-app"`)
	if err == nil {
		t.Error("expected error for unterminated JSON, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseProjectProfileJSON_InvalidJSON_Error(t *testing.T) {
	_, err := ParseProjectProfileJSON(`{not valid json}`)
	if err == nil {
		t.Error("expected parse error for invalid JSON, got nil")
	}
}

// ── ProjectType constants ─────────────────────────────────────────────────────

func TestProjectTypeConstants_NonEmpty(t *testing.T) {
	types := []ProjectType{
		ProjectTypeWebApp,
		ProjectTypeRestAPI,
		ProjectTypeCLI,
		ProjectTypeLibrary,
		ProjectTypeService,
		ProjectTypeUnknown,
	}
	for _, pt := range types {
		if pt == "" {
			t.Errorf("ProjectType constant is empty")
		}
	}
}

// ── Prompt expansion and style guidance ──────────────────────────────────────

func TestBuildPreStartExpansion_IncludesObjectiveAndCriteria(t *testing.T) {
	expanded := BuildPreStartExpansion(
		"Build a Stripe checkout flow. Make it live on Vercel. Add webhook verification.",
		"Project goal: ship a production-ready e-commerce app.",
	)
	if !strings.Contains(expanded, "Objective:") {
		t.Fatalf("expected objective in expansion, got: %s", expanded)
	}
	if !strings.Contains(expanded, "Success criteria:") {
		t.Fatalf("expected success criteria in expansion, got: %s", expanded)
	}
	if !strings.Contains(expanded, "Direction context:") {
		t.Fatalf("expected direction context in expansion, got: %s", expanded)
	}
}

func TestHasExplicitStyleGuidance_TrueForStyleKeywords(t *testing.T) {
	if !HasExplicitStyleGuidance("Use a minimalist design style with muted colors") {
		t.Fatal("expected style guidance detection to be true")
	}
}

func TestHasExplicitStyleGuidance_FalseWithoutStyleKeywords(t *testing.T) {
	if HasExplicitStyleGuidance("Build a CLI that imports CSV and prints totals") {
		t.Fatal("expected style guidance detection to be false")
	}
}

func TestBuildStyleAssumptionNotice_NonEmpty(t *testing.T) {
	msg := BuildStyleAssumptionNotice()
	if strings.TrimSpace(msg) == "" {
		t.Fatal("expected non-empty style assumption notice")
	}
}

// ── Heuristic project profile ────────────────────────────────────────────────

func TestBuildHeuristicProjectProfile_RestAPI(t *testing.T) {
	profile := BuildHeuristicProjectProfile(
		"/repo/app",
		"Build a REST API with health endpoint and deploy to Docker",
		"",
	)
	if profile.Type != ProjectTypeRestAPI {
		t.Fatalf("expected rest-api, got %q", profile.Type)
	}
	if profile.DeployTarget != "Docker" {
		t.Fatalf("expected Docker deploy target, got %q", profile.DeployTarget)
	}
	if profile.Verification.CheckURL == "" {
		t.Fatal("expected non-empty CheckURL for rest-api")
	}
}

func TestBuildHeuristicProjectProfile_WebApp(t *testing.T) {
	profile := BuildHeuristicProjectProfile(
		"/repo/web",
		"Build a web dashboard UI and put it on Vercel",
		"",
	)
	if profile.Type != ProjectTypeWebApp {
		t.Fatalf("expected web-app, got %q", profile.Type)
	}
	if !profile.Verification.UsesPlaywright {
		t.Fatal("expected web-app verification to use Playwright")
	}
	if profile.DeployTarget != "Vercel" {
		t.Fatalf("expected Vercel deploy target, got %q", profile.DeployTarget)
	}
}

func TestBuildPreStartExpansion_EmptyMessage_Defaults(t *testing.T) {
	expanded := BuildPreStartExpansion("   ", "")
	if !strings.Contains(expanded, "No explicit objective provided") {
		t.Fatalf("expected default objective text, got: %s", expanded)
	}
	if !strings.Contains(expanded, "Ship a working solution") {
		t.Fatalf("expected default success criteria, got: %s", expanded)
	}
}

func TestBuildPreStartExpansion_WithStyleGuidance_UsesStyleAssumption(t *testing.T) {
	expanded := BuildPreStartExpansion("Use a clean design theme", "dir")
	if !strings.Contains(expanded, "Style guidance is provided") {
		t.Fatalf("expected style-guidance assumption text, got: %s", expanded)
	}
}

func TestBuildPreStartExpansionWithProfile_IncludesProjectShapeAndVerification(t *testing.T) {
	profile := &ProjectProfile{
		Type:         ProjectTypeRestAPI,
		DeployTarget: "Docker",
		LiveCheckCmd: "curl -sf http://localhost:8080/health",
		WorkingBehaviors: []string{
			"User can query orders",
			"User can create checkout sessions",
		},
		Verification: VerificationStrategy{
			UsesPlaywright: false,
			StartCmd:       "go run .",
			CheckURL:       "http://localhost:8080/health",
			CheckCmds:      []string{"curl -sf http://localhost:8080/health"},
		},
	}

	expanded := BuildPreStartExpansionWithProfile(
		"Build checkout API",
		"Direction says build and publish a stable API",
		profile,
	)

	if !strings.Contains(expanded, "Project shape:") {
		t.Fatalf("expected project shape section, got: %s", expanded)
	}
	if !strings.Contains(expanded, "Verification plan:") {
		t.Fatalf("expected verification plan section, got: %s", expanded)
	}
	if !strings.Contains(expanded, "Type=rest-api") {
		t.Fatalf("expected rest-api type in expansion, got: %s", expanded)
	}
}

func TestBuildHeuristicProjectProfile_NoCriteria_DefaultDone(t *testing.T) {
	profile := BuildHeuristicProjectProfile("/repo", "short", "")
	if len(profile.DoneDefinition) == 0 {
		t.Fatal("expected default done definition")
	}
	if len(profile.WorkingBehaviors) == 0 {
		t.Fatal("expected generated working behaviors")
	}
}

func TestBuildHeuristicProjectProfile_CLI_LiveCheckFromCheckCmd(t *testing.T) {
	profile := BuildHeuristicProjectProfile("/repo", "build a command line cli tool", "")
	if profile.Type != ProjectTypeCLI {
		t.Fatalf("expected cli type, got %q", profile.Type)
	}
	if len(profile.Verification.CheckCmds) == 0 {
		t.Fatal("expected cli verification check commands")
	}
	if profile.LiveCheckCmd != profile.Verification.CheckCmds[0] {
		t.Fatalf("expected live check to use first check command, got %q want %q", profile.LiveCheckCmd, profile.Verification.CheckCmds[0])
	}
}

func TestDetectProjectTypeHeuristic_CoversAllBranches(t *testing.T) {
	tests := []struct {
		in   string
		want ProjectType
	}{
		{"build endpoint and rest api", ProjectTypeRestAPI},
		{"build a terminal tool cli", ProjectTypeCLI},
		{"publish an sdk library", ProjectTypeLibrary},
		{"run background daemon worker service", ProjectTypeService},
		{"ship a website frontend ui", ProjectTypeWebApp},
		{"totally ambiguous request", ProjectTypeUnknown},
	}
	for _, tc := range tests {
		got := detectProjectTypeHeuristic(tc.in, "")
		if got != tc.want {
			t.Fatalf("detectProjectTypeHeuristic(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDetectDeployTargetHeuristic_CoversAllBranches(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"deploy to vercel", "Vercel"},
		{"use docker compose", "Docker"},
		{"ship on aws", "AWS"},
		{"host on github pages", "GitHub Pages"},
		{"publish to npm", "npm"},
		{"no deploy target", "local"},
	}
	for _, tc := range tests {
		got := detectDeployTargetHeuristic(tc.in)
		if got != tc.want {
			t.Fatalf("detectDeployTargetHeuristic(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtractSuccessCriteria_TooShortAndCap(t *testing.T) {
	msg := "short. valid criterion one is here. valid criterion two is here. valid criterion three is here. valid criterion four is here. valid criterion five is here. valid criterion six is here. valid criterion seven is here."
	out := extractSuccessCriteria(msg)
	if len(out) != 6 {
		t.Fatalf("expected cap of 6 criteria, got %d", len(out))
	}
	for _, c := range out {
		if len(c) < 10 {
			t.Fatalf("expected short criteria to be skipped, got %q", c)
		}
	}
}

func TestFirstNonEmptyLine_EmptyAndTrimmed(t *testing.T) {
	if got := firstNonEmptyLine("\n\n  a line\nnext"); got != "a line" {
		t.Fatalf("firstNonEmptyLine trimmed result = %q, want a line", got)
	}
	if got := firstNonEmptyLine("\n  \n"); got != "" {
		t.Fatalf("firstNonEmptyLine empty case = %q, want empty", got)
	}
}

func TestTruncateForPrompt_AllBranches(t *testing.T) {
	if got := truncateForPrompt("abcd", 2); got != "ab" {
		t.Fatalf("truncateForPrompt n<=3 = %q, want ab", got)
	}
	if got := truncateForPrompt("abc", 5); got != "abc" {
		t.Fatalf("truncateForPrompt short string = %q, want abc", got)
	}
	if got := truncateForPrompt("abcdef", 5); got != "ab..." {
		t.Fatalf("truncateForPrompt long string = %q, want ab...", got)
	}
}
