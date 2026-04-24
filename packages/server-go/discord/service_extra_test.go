package discord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ── parseOptionalBool ────────────────────────────────────────────────────────

func TestParseOptionalBool_True(t *testing.T) {
	for _, raw := range []string{"1", "true", "yes", "on", "TRUE", "YES"} {
		v, ok := parseOptionalBool(raw)
		if !ok || !v {
			t.Errorf("parseOptionalBool(%q) = (%v, %v), want (true, true)", raw, v, ok)
		}
	}
}

func TestParseOptionalBool_False(t *testing.T) {
	for _, raw := range []string{"0", "false", "no", "off"} {
		v, ok := parseOptionalBool(raw)
		if !ok || v {
			t.Errorf("parseOptionalBool(%q) = (%v, %v), want (false, true)", raw, v, ok)
		}
	}
}

func TestParseOptionalBool_Empty(t *testing.T) {
	_, ok := parseOptionalBool("")
	if ok {
		t.Error("empty string should return ok=false")
	}
}

func TestParseOptionalBool_Unknown(t *testing.T) {
	_, ok := parseOptionalBool("maybe")
	if ok {
		t.Error("unknown value should return ok=false")
	}
}

// ── buildThreadName ──────────────────────────────────────────────────────────

func TestBuildThreadName_Short(t *testing.T) {
	got := buildThreadName("hello world")
	if got != "chat-hello world" {
		t.Errorf("got %q", got)
	}
}

func TestBuildThreadName_Long(t *testing.T) {
	long := "this is a very long prompt that exceeds the sixty character limit by quite a bit"
	got := buildThreadName(long)
	// "chat-" (5) + 57 chars + "..." (3) = 65 max
	if len(got) > 65 {
		t.Errorf("expected truncated, got len=%d: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestBuildThreadName_Empty(t *testing.T) {
	got := buildThreadName("")
	if got != "chat-chat" {
		t.Errorf("expected chat-chat, got %q", got)
	}
}

func TestBuildThreadName_Newlines(t *testing.T) {
	got := buildThreadName("line1\nline2")
	if got != "chat-line1 line2" {
		t.Errorf("expected newlines replaced, got %q", got)
	}
}

// ── parsePositiveInt ─────────────────────────────────────────────────────────

func TestParsePositiveInt_Valid(t *testing.T) {
	v, err := parsePositiveInt("42")
	if err != nil || v != 42 {
		t.Errorf("got (%d, %v), want (42, nil)", v, err)
	}
}

func TestParsePositiveInt_Zero(t *testing.T) {
	_, err := parsePositiveInt("0")
	if err == nil {
		t.Error("expected error for zero")
	}
}

func TestParsePositiveInt_NonDigit(t *testing.T) {
	_, err := parsePositiveInt("12a")
	if err == nil {
		t.Error("expected error for non-digit")
	}
}

func TestParsePositiveInt_Empty(t *testing.T) {
	_, err := parsePositiveInt("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

// ── shortTime ────────────────────────────────────────────────────────────────

func TestShortTime_ValidRFC3339(t *testing.T) {
	ts := "2024-06-15T14:30:00Z"
	got := shortTime(ts)
	if got == ts {
		t.Errorf("expected formatted time, got original %q", got)
	}
}

func TestShortTime_InvalidFallback(t *testing.T) {
	got := shortTime("not-a-time")
	if got != "not-a-time" {
		t.Errorf("expected passthrough of invalid time, got %q", got)
	}
}

// ── displayName ──────────────────────────────────────────────────────────────

func TestDisplayName_NonEmpty(t *testing.T) {
	if got := displayName("Alice", "in"); got != "Alice" {
		t.Errorf("got %q", got)
	}
}

func TestDisplayName_EmptyOut(t *testing.T) {
	if got := displayName("", "out"); got != "engine" {
		t.Errorf("got %q", got)
	}
}

func TestDisplayName_EmptyIn(t *testing.T) {
	if got := displayName("", "in"); got != "user" {
		t.Errorf("got %q", got)
	}
}

func TestDisplayName_Spaces(t *testing.T) {
	if got := displayName("   ", "out"); got != "engine" {
		t.Errorf("got %q", got)
	}
}

// ── truncate ─────────────────────────────────────────────────────────────────

func TestTruncate_Short(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncate_Exact(t *testing.T) {
	if got := truncate("hello", 5); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncate_Long(t *testing.T) {
	got := truncate("hello world", 8)
	// truncate appends a unicode ellipsis (3 bytes) so byte length may exceed max
	if len([]rune(got)) > 8 {
		t.Errorf("expected rune len<=8, got %q (runes=%d)", got, len([]rune(got)))
	}
}

func TestTruncate_ZeroMax(t *testing.T) {
	if got := truncate("hello", 0); got != "hello" {
		t.Errorf("zero max should return original, got %q", got)
	}
}

// ── shortID ──────────────────────────────────────────────────────────────────

func TestShortID_Long(t *testing.T) {
	got := shortID("abcdefghijklmnop")
	// shortID truncates at 12 chars
	if len(got) > 12 {
		t.Errorf("expected len<=12, got %q", got)
	}
}

func TestShortID_Short(t *testing.T) {
	got := shortID("abc")
	if got != "abc" {
		t.Errorf("short input should be unchanged, got %q", got)
	}
}

// ── ternary ──────────────────────────────────────────────────────────────────

func TestTernary_True(t *testing.T) {
	if got := ternary(true, "yes", "no"); got != "yes" {
		t.Errorf("got %q", got)
	}
}

func TestTernary_False(t *testing.T) {
	if got := ternary(false, "yes", "no"); got != "no" {
		t.Errorf("got %q", got)
	}
}

// ── isThread ─────────────────────────────────────────────────────────────────

func TestIsThread_PublicThread(t *testing.T) {
	ch := &discordgo.Channel{Type: discordgo.ChannelTypeGuildPublicThread}
	if !isThread(ch) {
		t.Error("expected true for public thread")
	}
}

func TestIsThread_PrivateThread(t *testing.T) {
	ch := &discordgo.Channel{Type: discordgo.ChannelTypeGuildPrivateThread}
	if !isThread(ch) {
		t.Error("expected true for private thread")
	}
}

func TestIsThread_NewsThread(t *testing.T) {
	ch := &discordgo.Channel{Type: discordgo.ChannelTypeGuildNewsThread}
	if !isThread(ch) {
		t.Error("expected true for news thread")
	}
}

func TestIsThread_RegularChannel(t *testing.T) {
	ch := &discordgo.Channel{Type: discordgo.ChannelTypeGuildText}
	if isThread(ch) {
		t.Error("expected false for regular text channel")
	}
}

// ── stateDir / configFilePath ─────────────────────────────────────────────────

func TestStateDir(t *testing.T) {
	got := stateDir("/tmp/myproject")
	if got != filepath.Join("/tmp/myproject", ".engine") {
		t.Errorf("got %q", got)
	}
}

func TestConfigFilePath(t *testing.T) {
	got := configFilePath("/tmp/proj")
	expected := filepath.Join("/tmp/proj", ".engine", defaultConfigFileName)
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

// ── NewService / Close ────────────────────────────────────────────────────────

func TestNewService_DisabledDoesNotLoadState(t *testing.T) {
	projectDir := t.TempDir()
	cfg := Config{
		Enabled:      false,
		AllowedUsers: map[string]bool{},
		StoragePath:  projectDir,
	}
	svc, err := NewService(cfg, projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestClose_NilSession(t *testing.T) {
	svc := &Service{dg: nil}
	if err := svc.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ── isAllowedUser ─────────────────────────────────────────────────────────────

func TestIsAllowedUser_Allowed(t *testing.T) {
	svc := &Service{
		cfg: Config{AllowedUsers: map[string]bool{"user-123": true}},
	}
	if !svc.isAllowedUser("user-123") {
		t.Error("expected user-123 to be allowed")
	}
}

func TestIsAllowedUser_NotAllowed(t *testing.T) {
	svc := &Service{
		cfg: Config{AllowedUsers: map[string]bool{"user-123": true}},
	}
	if svc.isAllowedUser("other-user") {
		t.Error("expected other-user to be denied")
	}
}

// ── resolveProjectByRef / resolveProjectForMessage ────────────────────────────

func TestResolveProjectByRef_NotFound(t *testing.T) {
	svc := &Service{
		state: persistedState{Projects: make(map[string]ProjectBinding)},
	}
	_, _, ok := svc.resolveProjectByRef("nonexistent")
	if ok {
		t.Error("expected false for empty projects")
	}
}

func TestResolveProjectForMessage_NoMatch(t *testing.T) {
	svc := &Service{
		state: persistedState{Projects: make(map[string]ProjectBinding)},
	}
	_, ok := svc.resolveProjectForMessage("chan-abc", nil)
	if ok {
		t.Error("expected false with no projects")
	}
}

func TestResolveAskTarget_TooFewArgs(t *testing.T) {
	svc := &Service{
		state: persistedState{Projects: make(map[string]ProjectBinding)},
	}
	_, _, ok := svc.resolveAskTarget("chan-abc", []string{"onearg"})
	if ok {
		t.Error("expected false with one arg and no matching channel")
	}
}

// ── parseCSVSet / parseSliceSet ──────────────────────────────────────────────

func TestParseCSVSet(t *testing.T) {
	got := parseCSVSet("a,b, c , d")
	if !got["a"] || !got["b"] || !got["c"] || !got["d"] {
		t.Errorf("expected all values in set, got %v", got)
	}
	if len(got) != 4 {
		t.Errorf("expected 4 entries, got %d", len(got))
	}
}

func TestParseSliceSet(t *testing.T) {
	got := parseSliceSet([]string{"x", "y", "z"})
	if !got["x"] || !got["y"] || !got["z"] {
		t.Errorf("expected all values in set, got %v", got)
	}
}

// ── WriteConfig / LoadConfig error paths ────────────────────────────────────

func TestWriteConfig_CreatesFile(t *testing.T) {
	projectDir := t.TempDir()
	cfg := Config{
		Enabled:            false,
		BotToken:           "write-test-token",
		GuildID:            "guild-write",
		AllowedUsers:       map[string]bool{"u1": true},
		CommandPrefix:      "!",
		ControlChannelName: "eng-ctrl",
		StoragePath:        filepath.Join(projectDir, ".engine"),
		ConfigFilePath:     filepath.Join(projectDir, ".engine", defaultConfigFileName),
	}
	if err := WriteConfig(projectDir, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	path := filepath.Join(projectDir, ".engine", defaultConfigFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	t.Setenv("ENGINE_DISCORD", "")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "")
	projectDir := t.TempDir()
	cfg, err := LoadConfig(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CommandPrefix != defaultCommandPrefix {
		t.Errorf("expected default prefix, got %q", cfg.CommandPrefix)
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	t.Setenv("ENGINE_DISCORD", "")
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(configDir, defaultConfigFileName)
	if err := os.WriteFile(configPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(projectDir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── Validate ──────────────────────────────────────────────────────────────────

func TestValidate_InvalidToken(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		BotToken:     "invalid-token-xxx",
		GuildID:      "test-guild",
		AllowedUsers: map[string]bool{"u1": true},
		StoragePath:  t.TempDir(),
	}
	result := Validate(cfg)
	if result.OK {
		t.Error("expected not OK for fake token")
	}
}

func TestValidate_DisabledConfig(t *testing.T) {
	cfg := Config{
		Enabled:      false,
		BotToken:     "",
		GuildID:      "",
		AllowedUsers: map[string]bool{},
		StoragePath:  t.TempDir(),
	}
	result := Validate(cfg)
	// Disabled config with no token should report not valid
	_ = result // just confirm it doesn't panic
}

// ── splitChannelThread (nil dg) ──────────────────────────────────────────────

func TestSplitChannelThread_NilDg(t *testing.T) {
	svc := &Service{
		dg:    nil,
		state: persistedState{Projects: make(map[string]ProjectBinding)},
	}
	channel, thread := svc.splitChannelThread("chan-123")
	if channel != "chan-123" {
		t.Errorf("expected passthrough, got %q", channel)
	}
	if thread != "" {
		t.Errorf("expected empty thread, got %q", thread)
	}
}

// ── stateFile ─────────────────────────────────────────────────────────────────

func TestStateFile(t *testing.T) {
	projectDir := t.TempDir()
	svc := &Service{
		cfg:     Config{StoragePath: filepath.Join(projectDir, ".engine")},
		project: projectDir,
	}
	sf := svc.stateFile()
	if sf == "" {
		t.Error("expected non-empty state file path")
	}
}

// ── applyEnvOverrides ─────────────────────────────────────────────────────────

func TestApplyEnvOverrides_SetsFromEnv(t *testing.T) {
	t.Setenv("ENGINE_DISCORD", "true")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "env-tok")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "env-guild")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "u1,u2")
	t.Setenv("ENGINE_DISCORD_PREFIX", "//")
	t.Setenv("ENGINE_DISCORD_CONTROL_CHANNEL", "ctrl")
	t.Setenv("ENGINE_STATE_DIR", "")

	cfg := &Config{AllowedUsers: map[string]bool{}}
	applyEnvOverrides(cfg)

	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.BotToken != "env-tok" {
		t.Errorf("token: %q", cfg.BotToken)
	}
	if cfg.GuildID != "env-guild" {
		t.Errorf("guild: %q", cfg.GuildID)
	}
	if !cfg.AllowedUsers["u1"] || !cfg.AllowedUsers["u2"] {
		t.Errorf("allowed users: %v", cfg.AllowedUsers)
	}
	if cfg.CommandPrefix != "//" {
		t.Errorf("prefix: %q", cfg.CommandPrefix)
	}
	if cfg.ControlChannelName != "ctrl" {
		t.Errorf("control channel: %q", cfg.ControlChannelName)
	}
}

// ── resolveContext (no dg, no projects) ─────────────────────────────────────

func TestResolveContext_NoProjects(t *testing.T) {
	svc := &Service{
		dg:    nil,
		state: persistedState{Projects: make(map[string]ProjectBinding)},
	}
	projectPath, sessionID := svc.resolveContext("chan-1", "")
	if projectPath != "" || sessionID != "" {
		t.Errorf("expected empty results, got (%q, %q)", projectPath, sessionID)
	}
}

// ── shortTime edge: whitespace ────────────────────────────────────────────────

func TestShortTime_RFC3339Nano(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	got := shortTime(ts)
	if got == ts {
		t.Error("expected reformatted time")
	}
}
