package discord

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		prefix    string
		ok        bool
		wantCmd   string
		wantParts []string
	}{
		{
			name:      "valid command",
			content:   "!status project-a",
			prefix:    "!",
			ok:        true,
			wantCmd:   "status",
			wantParts: []string{"project-a"},
		},
		{
			name:      "case normalized",
			content:   "!AsK hello world",
			prefix:    "!",
			ok:        true,
			wantCmd:   "ask",
			wantParts: []string{"hello", "world"},
		},
		{
			name:    "missing prefix",
			content: "status",
			prefix:  "!",
			ok:      false,
		},
		{
			name:    "prefix only",
			content: "!",
			prefix:  "!",
			ok:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, parts, ok := parseCommand(tt.content, tt.prefix)
			if ok != tt.ok {
				t.Fatalf("ok mismatch: got %v want %v", ok, tt.ok)
			}
			if cmd != tt.wantCmd {
				t.Fatalf("cmd mismatch: got %q want %q", cmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(parts, tt.wantParts) {
				t.Fatalf("parts mismatch: got %#v want %#v", parts, tt.wantParts)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "My Editor", want: "my-editor"},
		{in: "Repo___Name", want: "repo-name"},
		{in: "  ", want: "project"},
	}

	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Fatalf("slug(%q) = %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitForDiscord(t *testing.T) {
	parts := splitForDiscord("line1\nline2\nline3", 7)
	if len(parts) < 2 {
		t.Fatalf("expected split output, got %#v", parts)
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("unexpected empty split result: %#v", parts)
	}
}

func TestLoadConfigFromProjectFile(t *testing.T) {
	t.Setenv("ENGINE_DISCORD", "")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "")
	t.Setenv("ENGINE_DISCORD_PREFIX", "")
	t.Setenv("ENGINE_DISCORD_CONTROL_CHANNEL", "")
	t.Setenv("ENGINE_STATE_DIR", "")

	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, defaultConfigFileName)
	configJSON := `{
		"enabled": true,
		"botToken": "bot-token",
		"guildId": "guild-123",
		"allowedUserIds": ["user-1", "user-2"],
		"commandPrefix": "/",
		"controlChannelName": "ops-room"
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("expected enabled config")
	}
	if cfg.BotToken != "bot-token" {
		t.Fatalf("unexpected token: %q", cfg.BotToken)
	}
	if cfg.GuildID != "guild-123" {
		t.Fatalf("unexpected guild: %q", cfg.GuildID)
	}
	if !cfg.AllowedUsers["user-1"] || !cfg.AllowedUsers["user-2"] {
		t.Fatalf("expected allowed users to load: %#v", cfg.AllowedUsers)
	}
	if cfg.CommandPrefix != "/" {
		t.Fatalf("unexpected prefix: %q", cfg.CommandPrefix)
	}
	if cfg.ControlChannelName != "ops-room" {
		t.Fatalf("unexpected control channel: %q", cfg.ControlChannelName)
	}
	if cfg.StoragePath != configDir {
		t.Fatalf("unexpected storage path: %q", cfg.StoragePath)
	}
	if cfg.ConfigFilePath != configPath {
		t.Fatalf("unexpected config path: %q", cfg.ConfigFilePath)
	}
}

func TestLoadConfigEnvOverridesProjectFile(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, defaultConfigFileName)
	configJSON := `{
		"enabled": false,
		"botToken": "file-token",
		"guildId": "file-guild",
		"allowedUserIds": ["file-user"]
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ENGINE_DISCORD", "true")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "env-token")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "env-guild")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "env-user")
	t.Setenv("ENGINE_DISCORD_PREFIX", "!")

	cfg, err := LoadConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("expected env to enable config")
	}
	if cfg.BotToken != "env-token" {
		t.Fatalf("expected env token override, got %q", cfg.BotToken)
	}
	if cfg.GuildID != "env-guild" {
		t.Fatalf("expected env guild override, got %q", cfg.GuildID)
	}
	if !cfg.AllowedUsers["env-user"] || len(cfg.AllowedUsers) != 1 {
		t.Fatalf("expected env allowed users override, got %#v", cfg.AllowedUsers)
	}
}
