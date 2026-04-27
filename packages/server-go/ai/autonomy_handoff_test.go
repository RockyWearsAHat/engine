package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/engine/server/db"
)

func initAutonomyHandoffTestDB(t *testing.T, projectPath string) {
	t.Helper()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
}

func TestBuildAutonomyHandoff_MinimumFields(t *testing.T) {
	h := BuildAutonomyHandoff("req-1", "sess-1", "/repo", "Build REST API", "", nil)
	if h.Version == "" || h.RequestID == "" || h.SessionID == "" || h.ProjectPath == "" {
		t.Fatalf("expected identity fields to be set, got %+v", h)
	}
	if h.ExecutionIntent.PublishIntent != PublishIntentNone {
		t.Fatalf("expected publish intent none by default, got %q", h.ExecutionIntent.PublishIntent)
	}
	if len(h.Scope.InScope) == 0 || len(h.Scope.OutScope) == 0 {
		t.Fatalf("expected scope to be populated, got %+v", h.Scope)
	}
}

func TestBuildAutonomyHandoff_ExplicitStyleAndIntent(t *testing.T) {
	profile := &ProjectProfile{
		ExecutionIntent: ExecutionIntent{
			PublishIntent: PublishIntentExplicit,
			PublishEvidence: []PublishIntentEvidence{{
				Source:     "request",
				Excerpt:    "publish this package",
				CapturedAt: "2026-01-01T00:00:00Z",
			}},
		},
	}
	h := BuildAutonomyHandoff("req-3", "sess-3", "/repo", "Use minimalist design style and publish to npm", "summary", profile)
	if !h.Style.ExplicitStyleProvided {
		t.Fatal("expected explicit style to be detected")
	}
	if h.Style.AssumptionNoticeSent.Chat {
		t.Fatal("style assumption notice should not be sent when explicit style is provided")
	}
	if h.ExecutionIntent.PublishIntent != PublishIntentExplicit {
		t.Fatalf("expected explicit publish intent, got %q", h.ExecutionIntent.PublishIntent)
	}
	if len(h.Continuity.KnownRisks) != 0 {
		t.Fatalf("expected no publish-risk warning when explicit intent exists, got %+v", h.Continuity.KnownRisks)
	}
}

func TestBuildAutonomyHandoff_ProfileWithEmptyIntentDefaultsToNone(t *testing.T) {
	profile := &ProjectProfile{ExecutionIntent: ExecutionIntent{}}
	h := BuildAutonomyHandoff("req-5", "sess-5", "/repo", "", "", profile)
	if h.ExecutionIntent.PublishIntent != PublishIntentNone {
		t.Fatalf("expected empty profile intent to default to none, got %q", h.ExecutionIntent.PublishIntent)
	}
	if h.Objective.Statement != "" {
		t.Fatalf("expected empty objective statement for empty request, got %q", h.Objective.Statement)
	}
}

func TestWriteAutonomyHandoffCache_WritesFile(t *testing.T) {
	dir := t.TempDir()
	h := BuildAutonomyHandoff("req-2", "sess-2", dir, "Build web app", "summary", nil)
	if err := WriteAutonomyHandoffCache(dir, &h); err != nil {
		t.Fatalf("WriteAutonomyHandoffCache: %v", err)
	}
	path := filepath.Join(dir, ".cache", "autonomy-handoff.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read handoff cache: %v", err)
	}
	var parsed AutonomyHandoff
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal handoff cache: %v", err)
	}
	if parsed.RequestID != "req-2" {
		t.Fatalf("requestId=%q, want req-2", parsed.RequestID)
	}
}

func TestWriteAutonomyHandoffCache_NilOrEmptyPath_NoError(t *testing.T) {
	if err := WriteAutonomyHandoffCache("", nil); err != nil {
		t.Fatalf("expected nil error for nil handoff and empty path, got %v", err)
	}
}

func TestWriteAutonomyHandoffCache_NilHandoff_NoError(t *testing.T) {
	if err := WriteAutonomyHandoffCache(t.TempDir(), nil); err != nil {
		t.Fatalf("expected nil error for nil handoff, got %v", err)
	}
}

func TestWriteAutonomyHandoffCache_EmptyPath_NoError(t *testing.T) {
	h := BuildAutonomyHandoff("req-empty", "sess-empty", "", "", "", nil)
	if err := WriteAutonomyHandoffCache("", &h); err != nil {
		t.Fatalf("expected nil error for empty path, got %v", err)
	}
}

func TestWriteAutonomyHandoffCache_MkdirError(t *testing.T) {
	projectFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(projectFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	h := BuildAutonomyHandoff("req-4", "sess-4", projectFile, "Build", "", nil)
	err := WriteAutonomyHandoffCache(projectFile, &h)
	if err == nil {
		t.Fatal("expected mkdir error when project path points to a file")
	}
}

func TestWriteAutonomyHandoffCache_WriteError(t *testing.T) {
	projectDir := t.TempDir()
	cacheDir := filepath.Join(projectDir, ".cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cacheDir, "autonomy-handoff.json"), 0o755); err != nil {
		t.Fatalf("mkdir conflicting path: %v", err)
	}
	h := BuildAutonomyHandoff("req-6", "sess-6", projectDir, "Build", "", nil)
	err := WriteAutonomyHandoffCache(projectDir, &h)
	if err == nil {
		t.Fatal("expected write error when target path is a directory")
	}
}

func TestValidatePublishIntentForAction_BlockedWithoutEvidence(t *testing.T) {
	dir := t.TempDir()
	initAutonomyHandoffTestDB(t, dir)

	if err := db.UpsertProjectProfile(dir, `{"projectPath":"`+dir+`","type":"web-app","doneDefinition":[],"deployTarget":"local","verification":{"usesPlaywright":true,"startCmd":"pnpm dev","checkURL":"http://localhost:3000","port":3000,"checkCmds":[]},"liveCheckCmd":"curl -sf http://localhost:3000","executionIntent":{"publishIntent":"none","publishEvidence":[]},"workingBehaviors":[]}`); err != nil {
		t.Fatalf("UpsertProjectProfile: %v", err)
	}

	err := ValidatePublishIntentForAction(dir, "npm publish")
	if err == nil {
		t.Fatal("expected publish action to be blocked without explicit evidence")
	}
}

func TestValidatePublishIntentForAction_AllowsWithEvidence(t *testing.T) {
	dir := t.TempDir()
	initAutonomyHandoffTestDB(t, dir)

	if err := db.UpsertProjectProfile(dir, `{"projectPath":"`+dir+`","type":"library","doneDefinition":[],"deployTarget":"npm","verification":{"usesPlaywright":false,"startCmd":"","checkURL":"","port":0,"checkCmds":["pnpm test"]},"liveCheckCmd":"pnpm test","executionIntent":{"publishIntent":"explicit","publishEvidence":[{"source":"request","excerpt":"publish to npm","capturedAt":"2026-01-01T00:00:00Z"}]},"workingBehaviors":[]}`); err != nil {
		t.Fatalf("UpsertProjectProfile: %v", err)
	}

	if err := ValidatePublishIntentForAction(dir, "npm publish"); err != nil {
		t.Fatalf("expected publish action to be allowed, got %v", err)
	}
}

func TestValidatePublishIntentForAction_NonPublishAction_Allows(t *testing.T) {
	if err := ValidatePublishIntentForAction(t.TempDir(), "go test ./..."); err != nil {
		t.Fatalf("expected non-publish action to pass, got %v", err)
	}
}

func TestValidatePublishIntentForAction_PolicyDisabled_Allows(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte("autonomous:\n  require_explicit_publish_intent: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := ValidatePublishIntentForAction(dir, "npm publish"); err != nil {
		t.Fatalf("expected publish action allowed when policy disabled, got %v", err)
	}
}
