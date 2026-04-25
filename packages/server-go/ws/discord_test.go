package ws

import (
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

// ─── handleDiscordConfigGet (nil bridge) ─────────────────────────────────────

func TestHandleDiscordConfigGet_NilBridge_ReturnsOnDiskConfig(t *testing.T) {
	projectDir := setupWSProject(t)

	// Ensure no discord bridge is registered.
	SetDiscordBridge(nil)
	t.Cleanup(func() { SetDiscordBridge(nil) })

	conn, cleanup := openWSTestConnection(t, projectDir)
	defer cleanup()

	// Open the project so the connection has a projectPath.
	writeWSMessage(t, conn, map[string]interface{}{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	// Request discord config with no bridge registered.
	writeWSMessage(t, conn, map[string]interface{}{
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

	writeWSMessage(t, conn, map[string]interface{}{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]interface{}{
		"type": "discord.config.set",
		"config": map[string]interface{}{
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

	writeWSMessage(t, conn, map[string]interface{}{
		"type": "project.open",
		"path": projectDir,
	})
	readWSMessageOfType(t, conn, "session.created")

	writeWSMessage(t, conn, map[string]interface{}{
		"type": "discord.validate",
		"config": map[string]interface{}{
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
