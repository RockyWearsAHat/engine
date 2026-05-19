package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipExhaustedSteps_AppendsFeedbackVariants(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{
		{Index: 1, Attempts: 2, Done: false, LastFeedback: ""},
		{Index: 2, Attempts: 2, Done: false, LastFeedback: "compile failed"},
		{Index: 3, Attempts: 1, Done: false},
	}}
	skipped := skipExhaustedSteps(state, 2)
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped steps, got %d", len(skipped))
	}
	if !state.Plan[0].Done || !strings.Contains(state.Plan[0].LastFeedback, "skipped after 2 failed attempts") {
		t.Fatalf("unexpected first skipped step feedback: %+v", state.Plan[0])
	}
	if !state.Plan[1].Done || !strings.Contains(state.Plan[1].LastFeedback, "last feedback") {
		t.Fatalf("unexpected second skipped step feedback: %+v", state.Plan[1])
	}
	if state.Plan[2].Done {
		t.Fatal("step below attempt threshold should not be skipped")
	}
}

func TestSkipExhaustedSteps_DisabledWhenMaxAttemptsNonPositive(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{{Index: 1, Attempts: 999, Done: false}}}
	skipped := skipExhaustedSteps(state, 0)
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped steps when maxAttempts<=0, got %v", skipped)
	}
	if state.Plan[0].Done {
		t.Fatal("step should remain unmodified when maxAttempts<=0")
	}
}

func TestLoadOrCreateOrchestrationState_RefreshesBriefOnExistingFile(t *testing.T) {
	projectPath := t.TempDir()
	engineDir := filepath.Join(projectPath, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir engine dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, orchestrationFile), []byte(`{"owner":"o","repo":"r","brief":"old"}`), 0o644); err != nil {
		t.Fatalf("write orchestration file: %v", err)
	}

	state, err := loadOrCreateOrchestrationState(projectPath, "o", "r", "new brief")
	if err != nil {
		t.Fatalf("loadOrCreateOrchestrationState: %v", err)
	}
	if state.Brief != "new brief" {
		t.Fatalf("expected refreshed brief, got %q", state.Brief)
	}
	if strings.TrimSpace(state.UpdatedAt) == "" {
		t.Fatal("expected UpdatedAt to be set")
	}
}

func TestPersistOrchestration_PathError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(projectPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1, Title: "t"}}}
	if err := persistOrchestration(projectPath, state); err == nil {
		t.Fatal("expected persistOrchestration to fail when project path is a file")
	}
}
