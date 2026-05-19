package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWriteContextDoc_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	if got := readContextDoc(dir); got != "" {
		t.Fatalf("expected empty before write, got %q", got)
	}
	body := "## Vocabulary\n- Foo: a thing"
	if err := writeContextDoc(dir, body); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readContextDoc(dir)
	if got != body {
		t.Errorf("roundtrip mismatch: got %q, want %q", got, body)
	}
	if _, err := os.Stat(filepath.Join(dir, ".engine", "context.md")); err != nil {
		t.Errorf("context.md not created: %v", err)
	}
}

func TestBuildPlannerPromptWithContext_IncludesVocabulary(t *testing.T) {
	prompt := buildPlannerPromptWithContext("build a todo app", "## Vocabulary\nTodo: a task")
	if !strings.Contains(prompt, "UBIQUITOUS LANGUAGE") {
		t.Error("expected vocabulary header in prompt")
	}
	if !strings.Contains(prompt, "Todo: a task") {
		t.Error("expected vocabulary content in prompt")
	}
	if !strings.Contains(prompt, "build a todo app") {
		t.Error("expected brief content in prompt")
	}
	if !strings.Contains(prompt, "TDD") {
		t.Error("expected TDD discipline mention in prompt")
	}
}

func TestBuildPlannerPromptWithContext_OmitsVocabularyWhenEmpty(t *testing.T) {
	prompt := buildPlannerPromptWithContext("brief only", "")
	if strings.Contains(prompt, "UBIQUITOUS LANGUAGE") {
		t.Error("vocabulary header should be absent when context empty")
	}
	if !strings.Contains(prompt, "brief only") {
		t.Error("brief still required")
	}
}

func TestBuildStepPromptWithContext_IncludesTDDInstructions(t *testing.T) {
	state := &OrchestrationState{Owner: "rocky", Repo: "demo", Plan: []PlanStep{{Index: 1, Title: "step"}}}
	step := &state.Plan[0]
	prompt := buildStepPromptWithContext(state, step, "", "Vocab here")

	for _, want := range []string{"RED:", "GREEN:", "REFACTOR:", "Vocab here", "deep modules"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("step prompt missing %q\nfull:\n%s", want, prompt)
		}
	}
}

func TestBuildStepPromptWithContext_IncludesRedirect(t *testing.T) {
	state := &OrchestrationState{Owner: "x", Repo: "y", Plan: []PlanStep{{Index: 1, Title: "t"}}}
	prompt := buildStepPromptWithContext(state, &state.Plan[0], "switch to npm", "")
	if !strings.Contains(prompt, "URGENT INSTRUCTION") || !strings.Contains(prompt, "switch to npm") {
		t.Errorf("redirect not surfaced: %s", prompt)
	}
}

func TestBuildReviewerPromptWithContext_IncludesRubric(t *testing.T) {
	state := &OrchestrationState{Owner: "x", Repo: "y", Plan: []PlanStep{{Index: 1, Title: "t", Acceptance: "tests pass"}}}
	prompt := buildReviewerPromptWithContext(state, &state.Plan[0], "VOCAB DOC")

	for _, want := range []string{"ACCEPTANCE", "TDD DISCIPLINE", "UBIQUITOUS LANGUAGE", "DEEP MODULES", "APPROVE", "REJECT", "VOCAB DOC"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("reviewer prompt missing %q", want)
		}
	}
}

func TestOrchestratorIntakePhase_UsesExistingContext(t *testing.T) {
	dir := t.TempDir()
	preexisting := "## Vocabulary\n- Existing: do not regenerate"
	if err := writeContextDoc(dir, preexisting); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath: dir,
		Owner:       "x",
		Repo:        "y",
		ChatFn: func(ctx *ChatContext, _ string) {
			t.Fatal("ChatFn should not be invoked when context.md already exists")
		},
	}
	state := &OrchestrationState{Brief: "brief"}

	got, err := orchestratorIntakePhase(cfg, state, nil)
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if got != preexisting {
		t.Errorf("returned doc mismatch: %q", got)
	}
}
