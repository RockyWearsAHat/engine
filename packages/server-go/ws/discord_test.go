package ws

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/engine/server/discord"
)

// ─── maskToken ────────────────────────────────────────────────────────────────

func TestMaskToken_EmptyString_ReturnsEmpty(t *testing.T) {
	if got := maskToken(""); got != "" {
		t.Errorf("maskToken(\"\") = %q, want \"\"", got)
	}
}

func TestMaskToken_ShortToken_ReturnsPlaceholder(t *testing.T) {
	// <= 8 chars → masked with bullets
	got := maskToken("abc")
	if got != "•••" {
		t.Errorf("maskToken(short) = %q, want \"•••\"", got)
	}
}

func TestMaskToken_LongToken_ShowsFirstAndLast4(t *testing.T) {
	// "abcdefghij" → "abcd…ghij"
	got := maskToken("abcdefghij")
	if got != "abcd…ghij" {
		t.Errorf("maskToken(long) = %q, want \"abcd…ghij\"", got)
	}
}

func TestMaskToken_TrimWhitespace(t *testing.T) {
	// Leading/trailing whitespace should not affect the result.
	got := maskToken("  abcdefghij  ")
	if got != "abcd…ghij" {
		t.Errorf("maskToken(padded) = %q, want \"abcd…ghij\"", got)
	}
}

// ─── toPayload ────────────────────────────────────────────────────────────────

func TestToPayload_BotTokenIsMasked(t *testing.T) {
	cfg := discord.Config{
		BotToken: "ABCDEFGHIJKLMN",
		GuildID:  "guild1",
		Enabled:  true,
	}
	p := toPayload(cfg)
	// Raw token must not be exposed.
	if p.BotToken != "" {
		t.Errorf("toPayload should not expose raw BotToken, got %q", p.BotToken)
	}
	// Masked version must be present.
	if p.BotTokenMasked == "" {
		t.Error("toPayload should set BotTokenMasked for a non-empty token")
	}
	if p.HasToken != true {
		t.Error("toPayload should set HasToken=true for a non-empty token")
	}
}

func TestToPayload_EmptyToken_HasTokenFalse(t *testing.T) {
	cfg := discord.Config{}
	p := toPayload(cfg)
	if p.HasToken {
		t.Error("toPayload should set HasToken=false when BotToken is empty")
	}
}

func TestToPayload_AllowedUsersAreFlattened(t *testing.T) {
	cfg := discord.Config{
		AllowedUsers: map[string]bool{
			"user1": true,
			"user2": true,
		},
	}
	p := toPayload(cfg)
	if len(p.AllowedUserIDs) != 2 {
		t.Errorf("expected 2 allowed user ids, got %d", len(p.AllowedUserIDs))
	}
}

// ─── fromPayload ──────────────────────────────────────────────────────────────

func TestFromPayload_UpdatesTokenOnlyWhenNonEmpty(t *testing.T) {
	existing := discord.Config{BotToken: "original-token"}

	// Empty token in payload → keep existing.
	withEmpty := fromPayload(discordConfigPayload{BotToken: ""}, existing)
	if withEmpty.BotToken != "original-token" {
		t.Errorf("fromPayload should preserve existing token when payload token is empty, got %q", withEmpty.BotToken)
	}

	// Non-empty token in payload → overwrite.
	withNew := fromPayload(discordConfigPayload{BotToken: "new-token"}, existing)
	if withNew.BotToken != "new-token" {
		t.Errorf("fromPayload should update token when payload token is non-empty, got %q", withNew.BotToken)
	}
}

func TestFromPayload_AllowedUserIDsOverwriteExisting(t *testing.T) {
	existing := discord.Config{
		AllowedUsers: map[string]bool{"old-user": true},
	}
	p := discordConfigPayload{
		AllowedUserIDs: []string{"new-user"},
	}
	out := fromPayload(p, existing)
	if _, ok := out.AllowedUsers["old-user"]; ok {
		t.Error("fromPayload should replace AllowedUsers, not merge")
	}
	if _, ok := out.AllowedUsers["new-user"]; !ok {
		t.Error("fromPayload should include new-user in AllowedUsers")
	}
}

func TestFromPayload_TrimsWhitespaceFromStrings(t *testing.T) {
	existing := discord.Config{}
	p := discordConfigPayload{
		BotToken:           "  tok  ",
		GuildID:            " guild ",
		CommandPrefix:      " ! ",
		ControlChannelName: " general ",
	}
	out := fromPayload(p, existing)
	if out.BotToken != "tok" {
		t.Errorf("expected trimmed BotToken %q, got %q", "tok", out.BotToken)
	}
	if out.GuildID != "guild" {
		t.Errorf("expected trimmed GuildID %q, got %q", "guild", out.GuildID)
	}
	if out.CommandPrefix != "!" {
		t.Errorf("expected trimmed CommandPrefix %q, got %q", "!", out.CommandPrefix)
	}
	if out.ControlChannelName != "general" {
		t.Errorf("expected trimmed ControlChannelName %q, got %q", "general", out.ControlChannelName)
	}
}

func TestFromPayload_SkipsBlankAllowedUserIDs(t *testing.T) {
	existing := discord.Config{}
	p := discordConfigPayload{
		AllowedUserIDs: []string{"  ", "valid-user", ""},
	}
	out := fromPayload(p, existing)
	if _, ok := out.AllowedUsers[""]; ok {
		t.Error("fromPayload should skip blank user IDs")
	}
	if _, ok := out.AllowedUsers["valid-user"]; !ok {
		t.Error("fromPayload should include valid-user")
	}
	if len(out.AllowedUsers) != 1 {
		t.Errorf("expected 1 allowed user, got %d", len(out.AllowedUsers))
	}
}

func TestSameStringSet(t *testing.T) {
	if !sameStringSet(map[string]bool{"a": true}, map[string]bool{"a": true}) {
		t.Error("expected equal maps to match")
	}
	if sameStringSet(map[string]bool{"a": true}, map[string]bool{"a": true, "b": true}) {
		t.Error("expected mismatched lengths to fail")
	}
	if sameStringSet(map[string]bool{"a": true}, map[string]bool{"b": true}) {
		t.Error("expected missing key to fail")
	}
}

func TestSameDiscordRuntimeConfig(t *testing.T) {
	base := discord.Config{
		Enabled:            true,
		BotToken:           "token",
		GuildID:            "guild",
		CommandPrefix:      "!",
		ControlChannelName: "engine-control",
		AllowedUsers:       map[string]bool{"u1": true},
	}

	if !sameDiscordRuntimeConfig(base, base) {
		t.Error("expected identical config to match")
	}
	if sameDiscordRuntimeConfig(base, discord.Config{Enabled: false, BotToken: "token", GuildID: "guild", CommandPrefix: "!", ControlChannelName: "engine-control", AllowedUsers: map[string]bool{"u1": true}}) {
		t.Error("expected enabled mismatch to fail")
	}
	if sameDiscordRuntimeConfig(base, discord.Config{Enabled: true, BotToken: "different", GuildID: "guild", CommandPrefix: "!", ControlChannelName: "engine-control", AllowedUsers: map[string]bool{"u1": true}}) {
		t.Error("expected token mismatch to fail")
	}
	if sameDiscordRuntimeConfig(base, discord.Config{Enabled: true, BotToken: "token", GuildID: "other", CommandPrefix: "!", ControlChannelName: "engine-control", AllowedUsers: map[string]bool{"u1": true}}) {
		t.Error("expected guild mismatch to fail")
	}
	if sameDiscordRuntimeConfig(base, discord.Config{Enabled: true, BotToken: "token", GuildID: "guild", CommandPrefix: "#", ControlChannelName: "engine-control", AllowedUsers: map[string]bool{"u1": true}}) {
		t.Error("expected prefix mismatch to fail")
	}
	if sameDiscordRuntimeConfig(base, discord.Config{Enabled: true, BotToken: "token", GuildID: "guild", CommandPrefix: "!", ControlChannelName: "different", AllowedUsers: map[string]bool{"u1": true}}) {
		t.Error("expected channel mismatch to fail")
	}
	if sameDiscordRuntimeConfig(base, discord.Config{Enabled: true, BotToken: "token", GuildID: "guild", CommandPrefix: "!", ControlChannelName: "engine-control", AllowedUsers: map[string]bool{"u2": true}}) {
		t.Error("expected allowed-user mismatch to fail")
	}

	trimA := base
	trimB := base
	trimA.BotToken = " token "
	trimB.BotToken = "token"
	if !sameDiscordRuntimeConfig(trimA, trimB) {
		t.Error("expected trimmed tokens to be treated as equal")
	}
}

// ─── handleDiscordConfigGet (nil bridge) ─────────────────────────────────────

func TestHandleDiscordConfigGet_NilBridge_ReturnsOnDiskConfig(t *testing.T) {
	projectDir := setupWSProject(t)

	// Ensure no discord bridge is registered.
	SetDiscordBridge(nil)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Open the project so the connection has a projectPath.
	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	// Request discord config with no bridge registered.
	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.get",
	})

	response := readWSMessageOfType(t, conn, "discord.config")
	if response["type"] != "discord.config" {
		t.Fatalf("expected discord.config response, got %+v", response)
	}
	// active should be false when the bridge is nil.
	if active, _ := response["active"].(bool); active {
		t.Error("expected active=false when discord bridge is nil")
	}
	// config should be present.
	if _, ok := response["config"]; !ok {
		t.Error("expected config field in discord.config response")
	}
}

func TestHandleDiscordConfigSet_WriteError(t *testing.T) {
	SetDiscordBridge(nil)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)
	// ENGINE_STATE_DIR is set by setupWSProject — block discord.json there.
	stateDir := os.Getenv("ENGINE_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(projectDir, ".engine")
	}
	// Put a directory named "discord.json" so WriteFile fails.
	badPath := filepath.Join(stateDir, "discord.json")
	if err := os.MkdirAll(badPath, 0755); err != nil {
		t.Fatalf("mkdir discord.json block: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"token":   "token",
			"guildId": "guild",
			"enabled": false,
		},
	})

	response := readWSMessageOfType(t, conn, "error")
	if response["code"] != "DISCORD_WRITE" {
		t.Errorf("expected DISCORD_WRITE error, got %+v", response)
	}
}

func TestHandleDiscordValidate_WithOverride(t *testing.T) {
	SetDiscordBridge(nil)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.validate",
		"config": map[string]any{
			"token":   "tkn",
			"guildId": "gid",
			"enabled": false,
		},
	})

	response := readWSMessageOfType(t, conn, "discord.validate.result")
	if _, ok := response["result"]; !ok {
		t.Errorf("expected result field, got %+v", response)
	}
}

// ─── handleDiscordUnlink ──────────────────────────────────────────────────────

func TestHandleDiscordUnlink_NilBridge_ClearsConfigOnDisk(t *testing.T) {
	SetDiscordBridge(nil)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.unlink",
	})

	// Expect discord.config.saved with active=false.
	response := readWSMessageOfType(t, conn, "discord.config.saved")
	if active, _ := response["active"].(bool); active {
		t.Error("expected active=false after unlink")
	}
	cfgMap, _ := response["config"].(map[string]any)
	if cfgMap == nil {
		t.Fatal("expected config field in response")
	}
	if enabled, _ := cfgMap["enabled"].(bool); enabled {
		t.Error("expected enabled=false after unlink")
	}
}

func TestHandleDiscordUnlink_WriteError_ReturnsErrorCode(t *testing.T) {
	SetDiscordBridge(nil)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)

	// Block the config file so WriteConfig fails.
	stateDir := os.Getenv("ENGINE_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(projectDir, ".engine")
	}
	badPath := filepath.Join(stateDir, "discord.json")
	if err := os.MkdirAll(badPath, 0755); err != nil {
		t.Fatalf("mkdir block: %v", err)
	}

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{
		"type": "discord.unlink",
	})

	response := readWSMessageOfType(t, conn, "error")
	if response["code"] != "DISCORD_WRITE" {
		t.Errorf("expected DISCORD_WRITE error, got %+v", response)
	}
}

// stubLeavingBridge wraps stubDiscordBridge and also satisfies the
// LeaveGuild interface so that handleDiscordUnlink exercises the leave path.
type stubLeavingBridge struct {
	stubDiscordBridge
	leaveErr error
	leftID   string
}

func (s *stubLeavingBridge) LeaveGuild(guildID string) error {
	s.leftID = guildID
	return s.leaveErr
}

func TestHandleDiscordUnlink_WithBridge_LeaveSuccess(t *testing.T) {
	stub := &stubLeavingBridge{
		stubDiscordBridge: stubDiscordBridge{
			cfg: discord.Config{
				Enabled:      true,
				BotToken:     "tok",
				GuildID:      "guild-abc",
				AllowedUsers: map[string]bool{"u1": true},
			},
		},
	}
	SetDiscordBridge(stub)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{"type": "discord.unlink"})

	response := readWSMessageOfType(t, conn, "discord.config.saved")
	if w, ok := response["warning"].(string); ok && w != "" {
		t.Errorf("unexpected warning: %s", w)
	}
	if stub.leftID != "guild-abc" {
		t.Errorf("LeaveGuild called with %q, want %q", stub.leftID, "guild-abc")
	}
}

func TestHandleDiscordUnlink_WithBridge_LeaveError_WarningReturned(t *testing.T) {
	stub := &stubLeavingBridge{
		stubDiscordBridge: stubDiscordBridge{
			cfg: discord.Config{
				Enabled:      true,
				BotToken:     "tok",
				GuildID:      "guild-xyz",
				AllowedUsers: map[string]bool{"u1": true},
			},
		},
		leaveErr: fmt.Errorf("api error"),
	}
	SetDiscordBridge(stub)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{"type": "discord.unlink"})

	response := readWSMessageOfType(t, conn, "discord.config.saved")
	warning, _ := response["warning"].(string)
	if warning == "" {
		t.Error("expected warning when LeaveGuild returns an error")
	}
}

func TestHandleDiscordUnlink_WithBridge_ReloadError_WarningReturned(t *testing.T) {
	// Leave succeeds, but Reload fails → warning should mention reload failure.
	stub := &stubLeavingBridge{
		stubDiscordBridge: stubDiscordBridge{
			cfg: discord.Config{
				Enabled:      true,
				BotToken:     "tok",
				GuildID:      "guild-reload-err",
				AllowedUsers: map[string]bool{"u1": true},
			},
			reloadErr: fmt.Errorf("reload failed"),
		},
	}
	SetDiscordBridge(stub)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]any{"type": "discord.unlink"})

	response := readWSMessageOfType(t, conn, "discord.config.saved")
	warning, _ := response["warning"].(string)
	if warning == "" {
		t.Error("expected warning when Reload returns an error after unlink")
	}
}

func TestHandleDiscordConfigSet_WithBridge_ReloadSuccess(t *testing.T) {
	// hadBridge=true, config differs (sameDiscordRuntimeConfig=false), Reload succeeds.
	// This covers the `active = discordBridge.CurrentConfig().Enabled` line.
	stub := &stubDiscordBridge{
		cfg: discord.Config{
			BotToken: "tok",
			GuildID:  "guild-1",
			Enabled:  false,
		},
	}
	SetDiscordBridge(stub)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	projectDir := setupWSProject(t)
	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	writeWSMessage(t, conn, map[string]any{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	// Send a config with Enabled=true — differs from stub (Enabled=false), so
	// sameDiscordRuntimeConfig returns false and Reload is called.
	writeWSMessage(t, conn, map[string]any{
		"type": "discord.config.set",
		"config": map[string]any{
			"token":   "tok",
			"guildId": "guild-1",
			"enabled": true,
		},
	})

	response := readWSMessageOfType(t, conn, "discord.config.saved")
	if _, ok := response["config"]; !ok {
		t.Errorf("expected config field in discord.config.saved response, got %+v", response)
	}
}
