package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/engine/server/db"
)

func TestIngestMemoryEventAndCompileContext(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-memory-os", projectDir, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := ingestMemoryEvent(projectDir, "session-memory-os", "user_message", "test-model", map[string]any{
		"id":   "u-1",
		"text": "Implement deterministic context compiler with blockers and validations",
	}); err != nil {
		t.Fatalf("ingest user_message: %v", err)
	}
	if _, err := ingestMemoryEvent(projectDir, "session-memory-os", "tool_call_result", "test-model", map[string]any{
		"tool":    "run_tests",
		"isError": true,
		"error":   "coverage below threshold",
	}); err != nil {
		t.Fatalf("ingest tool_call_result error: %v", err)
	}
	if _, err := ingestMemoryEvent(projectDir, "session-memory-os", "validation_result", "test-model", map[string]any{
		"passed": true,
		"text":   "typecheck clean",
	}); err != nil {
		t.Fatalf("ingest validation_result: %v", err)
	}

	compiled := buildDeterministicMemoryContext(projectDir, "session-memory-os", "finish memory OS", []TabInfo{{Path: "packages/server-go/ai/context.go", IsActive: true}}, 2400)
	if !strings.Contains(compiled, "Deterministic memory context") {
		t.Fatalf("missing deterministic context header: %s", compiled)
	}
	if !strings.Contains(compiled, "Active Goals") {
		t.Fatalf("missing goals section: %s", compiled)
	}
	if !strings.Contains(compiled, "Unresolved Blockers") {
		t.Fatalf("missing blockers section: %s", compiled)
	}
}

func TestPersistScribeSnapshotWritesSharedAndModelNotes(t *testing.T) {
	projectDir := setupHistoryTestProject(t)
	if err := db.CreateSession("session-scribe", projectDir, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	persistScribeSnapshot(projectDir, "session-scribe", "gpt-5.3-codex", "Deterministic memory context (Memory OS):\n- Active Goals:\n  * keep state", 7)

	sharedPath := filepath.Join(projectDir, ".engine", "memory", "scribe", "shared.md")
	modelPath := filepath.Join(projectDir, ".engine", "memory", "scribe", "models", "gpt-5.3-codex.md")

	sharedContent, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("read shared snapshot: %v", err)
	}
	if !strings.Contains(string(sharedContent), "Shared Scribe Memory") {
		t.Fatalf("unexpected shared snapshot content: %s", string(sharedContent))
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Fatalf("model snapshot missing: %v", err)
	}

	snapshot, err := db.LoadLatestMemoryStateSnapshot(projectDir, "session-scribe", "scribe-shared")
	if err != nil {
		t.Fatalf("LoadLatestMemoryStateSnapshot: %v", err)
	}
	if snapshot == nil || snapshot.EventSequence != 7 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}
