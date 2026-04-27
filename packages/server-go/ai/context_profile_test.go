package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/engine/server/db"
)

func initContextProfileTestDB(t *testing.T, projectPath string) {
	t.Helper()
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
}

func TestResolveProjectDirection_UsesExisting(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)
	if err := db.UpsertProjectDirection(dir, "existing direction"); err != nil {
		t.Fatalf("UpsertProjectDirection: %v", err)
	}

	got := resolveProjectDirection(dir)
	if got != "existing direction" {
		t.Fatalf("resolveProjectDirection = %q, want existing direction", got)
	}
}

func TestApplyFirstTurnAutonomyContext_SendsNoticeAndCachesProfile(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	var notices []string
	discordNotices := 0
	ctx := &ChatContext{
		ProjectPath: dir,
		SendToClient: func(msgType string, payload any) {
			if msgType != "chat.notice" {
				return
			}
			if m, ok := payload.(map[string]any); ok {
				if s, ok := m["message"].(string); ok {
					notices = append(notices, s)
				}
			}
		},
		DiscordDM: func(message string) error {
			discordNotices++
			return nil
		},
	}

	applyFirstTurnAutonomyContext(ctx, "Build a REST API and ship it.", "", true)

	if len(notices) != 1 {
		t.Fatalf("expected 1 chat.notice, got %d", len(notices))
	}
	if discordNotices != 1 {
		t.Fatalf("expected 1 Discord notice, got %d", discordNotices)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cache", "project-profile.json")); err != nil {
		t.Fatalf("expected project-profile cache file, got %v", err)
	}
	stored, err := db.GetProjectProfile(dir)
	if err != nil {
		t.Fatalf("GetProjectProfile: %v", err)
	}
	if stored == "" {
		t.Fatal("expected stored project profile JSON")
	}
}

func TestApplyFirstTurnAutonomyContext_StyleGiven_SkipsNotice(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	noticeCount := 0
	ctx := &ChatContext{
		ProjectPath: dir,
		SendToClient: func(msgType string, payload any) {
			noticeCount++
		},
	}

	applyFirstTurnAutonomyContext(ctx, "Build a web app with minimalist design style.", "", true)
	if noticeCount != 0 {
		t.Fatalf("expected no style assumption notice when style specified, got %d", noticeCount)
	}
}

func TestApplyFirstTurnAutonomyContext_NotFirstMessage_Noops(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	noticeCount := 0
	ctx := &ChatContext{
		ProjectPath: dir,
		SendToClient: func(msgType string, payload any) {
			noticeCount++
		},
	}

	applyFirstTurnAutonomyContext(ctx, "Build anything", "", false)
	if noticeCount != 0 {
		t.Fatalf("expected no notice for non-first message, got %d", noticeCount)
	}
}

func TestEnsureProjectProfileCache_EmptyInputs_NoPanic(t *testing.T) {
	ensureProjectProfileCache("", "", "")
	ensureProjectProfileCache("/tmp/any", "", "")
}

func TestLoadProjectProfile_FromDB(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	raw := `{"projectPath":"` + dir + `","type":"web-app","doneDefinition":["home page loads"],"deployTarget":"local","verification":{"usesPlaywright":true,"startCmd":"pnpm dev","checkURL":"http://localhost:5173","port":5173,"checkCmds":[]},"liveCheckCmd":"curl -sf http://localhost:5173","workingBehaviors":["User can load homepage"]}`
	if err := db.UpsertProjectProfile(dir, raw); err != nil {
		t.Fatalf("UpsertProjectProfile: %v", err)
	}

	profile := loadProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected profile to load from DB")
	}
	if profile.Type != ProjectTypeWebApp {
		t.Fatalf("loaded profile type = %q, want web-app", profile.Type)
	}
}

func TestEnsureProjectProfileCache_ExplicitPublishIntentStored(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	ensureProjectProfileCache(dir, "Deploy this API to Docker and publish release", "")

	profile := loadProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected profile to load")
	}
	if profile.ExecutionIntent.PublishIntent != PublishIntentExplicit {
		t.Fatalf("expected explicit publish intent, got %q", profile.ExecutionIntent.PublishIntent)
	}
	if len(profile.ExecutionIntent.PublishEvidence) == 0 {
		t.Fatal("expected publish evidence for explicit deploy request")
	}
}

func TestApplyFirstTurnAutonomyContext_StyleNoticeDisabledInPolicy(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, "config.yaml"), []byte("autonomous:\n  style_assumption_notice: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	noticeCount := 0
	ctx := &ChatContext{
		ProjectPath: dir,
		SendToClient: func(msgType string, payload any) {
			if msgType == "chat.notice" {
				noticeCount++
			}
		},
	}

	applyFirstTurnAutonomyContext(ctx, "Build anything", "", true)
	if noticeCount != 0 {
		t.Fatalf("expected no style notice when policy disables it, got %d", noticeCount)
	}

	raw, err := db.GetProjectProfile(dir)
	if err != nil {
		t.Fatalf("GetProjectProfile: %v", err)
	}
	var parsed ProjectProfile
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if parsed.ExecutionIntent.PublishIntent == "" {
		t.Fatal("expected execution intent to be set in stored profile")
	}
}

func TestLoadProjectProfile_InvalidJSON_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	if err := db.UpsertProjectProfile(dir, "not-json"); err != nil {
		t.Fatalf("UpsertProjectProfile: %v", err)
	}

	profile := loadProjectProfile(dir)
	if profile != nil {
		t.Fatal("expected nil profile for invalid JSON")
	}
}

func TestLoadProjectProfile_Missing_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	initContextProfileTestDB(t, dir)

	profile := loadProjectProfile(dir)
	if profile != nil {
		t.Fatal("expected nil profile when none is stored")
	}
}
