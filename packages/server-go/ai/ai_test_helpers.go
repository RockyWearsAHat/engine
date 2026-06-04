package ai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/engine/server/db"
)

// ─────────────────────────────────────────────────────────────────────────────
// ── Consolidated Test Database Helpers
// ─────────────────────────────────────────────────────────────────────────────
// These helpers reduce duplication across test files that need database setup
// with consistent patterns: TempDir, Setenv, db.Init, optionally project structure.

// setupTestDBWithStateDir initializes the database for a project with fresh ENGINE_STATE_DIR.
// Common pattern: t.TempDir() for state, t.Setenv(), db.Init().
// Used by: context_profile_test.go, autonomy_handoff_test.go, and consolidated patterns.
func setupTestDBWithStateDir(t *testing.T, projectPath string) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
}

// setupTempDBProject creates a temp project dir and initializes the DB.
// Returns the project root. Consolidates setupIntakeDB pattern.
// Used by: orchestrator_intake_extra_test.go and similar.
func setupTempDBProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setupTestDBWithStateDir(t, dir)
	return dir
}

// setupPhasesDBProject creates a temp project dir with .engine/config.yaml, then initializes DB.
// Returns the project root. Consolidates setupPhasesDB pattern.
// Used by: orchestrator_phases_extra_test.go, orchestrator_run_extra_test.go.
func setupPhasesDBProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	config := `teams:
  fast:
    orchestrator:
      model: "openai:gpt-4o-mini"
autonomous:
  team: "fast"
`
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	setupTestDBWithStateDir(t, dir)
	return dir
}

// setupHistoryDBProject creates a temp project with PROJECT_GOAL.md,
// .github/references/architecture.md, and initializes the DB.
// Returns the project root. Consolidates setupHistoryTestProject pattern.
// Used by: history_context_test.go.
func setupHistoryDBProject(t *testing.T) string {
	t.Helper()
	projectDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".github", "references"), 0755); err != nil {
		t.Fatalf("mkdir project refs: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "PROJECT_GOAL.md"),
		[]byte("Engine should preserve project direction and retrieve the right history when the AI needs it."),
		0644,
	); err != nil {
		t.Fatalf("write project goal: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, ".github", "references", "architecture.md"),
		[]byte("Persistent context, autonomous validation, and agent orchestration are first-class architecture goals."),
		0644,
	); err != nil {
		t.Fatalf("write architecture doc: %v", err)
	}
	setupTestDBWithStateDir(t, projectDir)
	return projectDir
}

// ─────────────────────────────────────────────────────────────────────────────
// ── Consolidated Test Document Writing Helpers
// ─────────────────────────────────────────────────────────────────────────────
// These helpers reduce duplication of repeated document write sequences in tests.

// writeTestDocTriplet writes the three core orchestration docs (design, vocabulary, prd)
// to the project path, failing the test if any write fails.
// Consolidates the repeated 3-write pattern in orchestrator_gap_extra_test.go.
func writeTestDocTriplet(t *testing.T, dir string, designContent, vocabContent, prdContent string) {
	t.Helper()
	if err := WriteDoc(dir, DocDesign, designContent); err != nil {
		t.Fatalf("WriteDoc design: %v", err)
	}
	if err := WriteDoc(dir, DocVocabulary, vocabContent); err != nil {
		t.Fatalf("WriteDoc vocabulary: %v", err)
	}
	if err := WriteDoc(dir, DocPRD, prdContent); err != nil {
		t.Fatalf("WriteDoc prd: %v", err)
	}
}
