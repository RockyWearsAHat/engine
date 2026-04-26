package ai

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTeamOrchestratorModel_SelectedTeam(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}

	content := `teams:
  fast:
    orchestrator:
      model: "ollama:gemma4:31b"
  premium:
    orchestrator:
      model: "anthropic:claude-opus-4.6"
dev_loop:
  default_team: "fast"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	team, provider, model, ok := ResolveTeamOrchestratorModel(projectDir, "premium")
	if !ok {
		t.Fatal("expected team resolution to succeed")
	}
	if team != "premium" {
		t.Fatalf("expected team premium, got %q", team)
	}
	if provider != "anthropic" {
		t.Fatalf("expected provider anthropic, got %q", provider)
	}
	if model != "claude-opus-4.6" {
		t.Fatalf("expected model claude-opus-4.6, got %q", model)
	}
}

func TestResolveTeamOrchestratorModel_DefaultTeamFallback(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}

	content := `teams:
  fast:
    orchestrator:
      model: "openai:gpt-4o-mini"
dev_loop:
  default_team: "fast"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	team, provider, model, ok := ResolveTeamOrchestratorModel(projectDir, "")
	if !ok {
		t.Fatal("expected default team resolution to succeed")
	}
	if team != "fast" {
		t.Fatalf("expected team fast, got %q", team)
	}
	if provider != "openai" {
		t.Fatalf("expected provider openai, got %q", provider)
	}
	if model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %q", model)
	}
}

func TestResolveTeamOrchestratorModel_MissingConfig(t *testing.T) {
	projectDir := t.TempDir()
	_, _, _, ok := ResolveTeamOrchestratorModel(projectDir, "fast")
	if ok {
		t.Fatal("expected missing config to return ok=false")
	}
}

func TestResolveTeamOrchestratorModel_EmptyProjectPath(t *testing.T) {
	_, _, _, ok := ResolveTeamOrchestratorModel("", "fast")
	if ok {
		t.Fatal("expected empty project path to return ok=false")
	}
}

func TestResolveTeamOrchestratorModel_UnknownTeam(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}

	content := `teams:
  fast:
    orchestrator:
      model: "openai:gpt-4o-mini"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, _, ok := ResolveTeamOrchestratorModel(projectDir, "premium")
	if ok {
		t.Fatal("expected unknown team to return ok=false")
	}
}

func TestResolveTeamOrchestratorModel_InfersProviderFromModelName(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}

	content := `teams:
  premium:
    orchestrator:
      model: "claude-sonnet-4-6"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	team, provider, model, ok := ResolveTeamOrchestratorModel(projectDir, "premium")
	if !ok {
		t.Fatal("expected resolution success")
	}
	if team != "premium" {
		t.Fatalf("expected team premium, got %q", team)
	}
	if provider != "anthropic" {
		t.Fatalf("expected inferred provider anthropic, got %q", provider)
	}
	if model != "claude-sonnet-4-6" {
		t.Fatalf("expected model claude-sonnet-4-6, got %q", model)
	}
}

func TestParseYAMLValue_QuotedAndUnquoted(t *testing.T) {
	if got := parseYAMLValue(`model: "openai:gpt-4o"`, "model:"); got != "openai:gpt-4o" {
		t.Fatalf("expected double-quoted value unwrapped, got %q", got)
	}
	if got := parseYAMLValue("model: 'ollama:gemma4:31b'", "model:"); got != "ollama:gemma4:31b" {
		t.Fatalf("expected single-quoted value unwrapped, got %q", got)
	}
	if got := parseYAMLValue("default_team: fast", "default_team:"); got != "fast" {
		t.Fatalf("expected unquoted value, got %q", got)
	}
}

func TestSplitProviderModel_EdgeCases(t *testing.T) {
	if provider, model := splitProviderModel(""); provider != "" || model != "" {
		t.Fatalf("expected empty split for empty model, got provider=%q model=%q", provider, model)
	}
	if provider, model := splitProviderModel("gpt-4o"); provider != "" || model != "gpt-4o" {
		t.Fatalf("expected no provider split, got provider=%q model=%q", provider, model)
	}
	if provider, model := splitProviderModel("openai:gpt-4o"); provider != "openai" || model != "gpt-4o" {
		t.Fatalf("expected provider/model split, got provider=%q model=%q", provider, model)
	}
}

func TestResolveTeamOrchestratorModel_NoSelectedOrDefaultTeam(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}

	content := `teams:
  fast:
    orchestrator:
      model: "openai:gpt-4o-mini"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, _, ok := ResolveTeamOrchestratorModel(projectDir, "")
	if ok {
		t.Fatal("expected unresolved team when both selected and default are empty")
	}
}

func TestResolveTeamOrchestratorModel_TeamWithoutOrchestratorModel(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}

	content := `teams:
  fast:
    orchestrator:
      model_display: "No model value"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, _, ok := ResolveTeamOrchestratorModel(projectDir, "fast")
	if ok {
		t.Fatal("expected unresolved team when orchestrator model is missing")
	}
}

func TestParseTeamConfigYAML_CommentsAndUnknownSectionsIgnored(t *testing.T) {
	yaml := `# comment before sections
providers:
  ignored: true
teams:
  fast:
    description: "Fast local team"
    orchestrator:
      model: 'ollama:gemma4:31b'
      model_display: "Gemma"
dev_loop:
  default_team: fast
`

	models, defaultTeam := parseTeamConfigYAML(yaml)
	if got := models["fast"]; got != "ollama:gemma4:31b" {
		t.Fatalf("expected parsed orchestrator model, got %q", got)
	}
	if defaultTeam != "fast" {
		t.Fatalf("expected default team fast, got %q", defaultTeam)
	}
}

func TestParseTeamConfigYAML_EmptyInput(t *testing.T) {
	models, defaultTeam := parseTeamConfigYAML("")
	if len(models) != 0 {
		t.Fatalf("expected no models for empty yaml, got %#v", models)
	}
	if defaultTeam != "" {
		t.Fatalf("expected empty default team, got %q", defaultTeam)
	}
}

func TestParseTeamConfigYAML_DevLoopExtraKeys(t *testing.T) {
	// dev_loop section with a non-default_team key — exercises the false
	// branch of the inDevLoop && default_team check.
	yaml := `teams:
  fast:
    orchestrator:
      model: "openai:gpt-4o-mini"
dev_loop:
  max_iterations: 10
  default_team: fast
`
	models, defaultTeam := parseTeamConfigYAML(yaml)
	if got := models["fast"]; got != "openai:gpt-4o-mini" {
		t.Fatalf("expected model openai:gpt-4o-mini, got %q", got)
	}
	if defaultTeam != "fast" {
		t.Fatalf("expected default team fast, got %q", defaultTeam)
	}
}

func TestParseTeamConfigYAML_OrphanedIndentBeforeTeam(t *testing.T) {
	// Line with indent > 2 appearing before any team name is encountered —
	// exercises the `if currentTeam == "" { continue }` guard.
	yaml := `teams:
      orphan_key: value
  fast:
    orchestrator:
      model: "openai:gpt-4o-mini"
`
	models, _ := parseTeamConfigYAML(yaml)
	if got := models["fast"]; got != "openai:gpt-4o-mini" {
		t.Fatalf("expected model openai:gpt-4o-mini despite orphaned line, got %q", got)
	}
}

func TestParseAutonomousPolicy_Defaults(t *testing.T) {
	p := parseAutonomousPolicy("teams:\n  fast:\n    orchestrator:\n      model: \"openai:gpt-4o-mini\"\n")
	if p.AutoCommit || p.AutoPush || p.Branch != "" {
		t.Fatalf("expected zero policy for config without autonomous section, got %+v", p)
	}
}

func TestParseAutonomousPolicy_AutoCommitTrue(t *testing.T) {
	yaml := "autonomous:\n  auto_commit: true\n"
	p := parseAutonomousPolicy(yaml)
	if !p.AutoCommit {
		t.Fatal("expected AutoCommit=true")
	}
	if p.AutoPush {
		t.Fatal("expected AutoPush=false")
	}
}

func TestParseAutonomousPolicy_AutoPushTrue(t *testing.T) {
	yaml := "autonomous:\n  auto_commit: true\n  auto_push: true\n"
	p := parseAutonomousPolicy(yaml)
	if !p.AutoCommit {
		t.Fatal("expected AutoCommit=true")
	}
	if !p.AutoPush {
		t.Fatal("expected AutoPush=true")
	}
}

func TestParseAutonomousPolicy_Branch(t *testing.T) {
	yaml := "autonomous:\n  auto_commit: true\n  branch: \"engine/work\"\n"
	p := parseAutonomousPolicy(yaml)
	if p.Branch != "engine/work" {
		t.Fatalf("expected branch engine/work, got %q", p.Branch)
	}
}

func TestParseAutonomousPolicy_CommentLines(t *testing.T) {
	yaml := "autonomous:\n  # auto_push is deliberately off\n  auto_commit: true\n"
	p := parseAutonomousPolicy(yaml)
	if !p.AutoCommit {
		t.Fatal("expected AutoCommit=true despite comment line")
	}
	if p.AutoPush {
		t.Fatal("expected AutoPush=false")
	}
}

func TestParseAutonomousPolicy_AssumptionTolerance(t *testing.T) {
	yaml := "autonomous:\n  auto_commit: true\n  assumption_tolerance: aggressive\n"
	p := parseAutonomousPolicy(yaml)
	if p.AssumptionTolerance != "aggressive" {
		t.Fatalf("expected AssumptionTolerance=aggressive, got %q", p.AssumptionTolerance)
	}

	defaultYaml := "autonomous:\n  auto_commit: true\n"
	d := parseAutonomousPolicy(defaultYaml)
	if d.AssumptionTolerance != "" {
		t.Fatalf("expected empty AssumptionTolerance when not set, got %q", d.AssumptionTolerance)
	}
}

func TestResolveAutonomousPolicy_MissingConfig(t *testing.T) {
	p := ResolveAutonomousPolicy(t.TempDir())
	if p.AutoCommit || p.AutoPush || p.Branch != "" {
		t.Fatalf("expected zero policy for missing config, got %+v", p)
	}
}

func TestResolveAutonomousPolicy_FromFile(t *testing.T) {
	projectDir := t.TempDir()
	engineDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	content := "autonomous:\n  auto_commit: true\n  auto_push: true\n  branch: \"engine/ci\"\n"
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	p := ResolveAutonomousPolicy(projectDir)
	if !p.AutoCommit {
		t.Fatal("expected AutoCommit=true")
	}
	if !p.AutoPush {
		t.Fatal("expected AutoPush=true")
	}
	if p.Branch != "engine/ci" {
		t.Fatalf("expected branch engine/ci, got %q", p.Branch)
	}
}