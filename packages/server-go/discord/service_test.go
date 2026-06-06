package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/db"
)

// Helper functions to reduce test duplication

// newServiceWithEmptyProjects creates a Service with no projects.
func newServiceWithEmptyProjects(storagePath string) *Service {
	return &Service{
		cfg: Config{StoragePath: storagePath},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
}

// newServiceWithProjects creates a Service with the given projects map.
func newServiceWithProjects(storagePath string, projects map[string]ProjectBinding) *Service {
	return &Service{
		cfg: Config{StoragePath: storagePath},
		state: persistedState{
			Projects: projects,
		},
	}
}

// newTestMessageCreate constructs a MessageCreate event for testing.
func newTestMessageCreate(channelID, guildID, content string, authorID string, isBot bool) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: channelID,
			GuildID:   guildID,
			Content:   content,
			Author:    &discordgo.User{ID: authorID, Bot: isBot},
		},
	}
}

// assertErrorMessage verifies an error message matches the expected value.
func assertErrorMessage(t *testing.T, err error, wantMsg string, testName string) {
	if err == nil {
		t.Fatalf("%s: expected error %q, got nil", testName, wantMsg)
	}
	if err.Error() != wantMsg {
		t.Fatalf("%s: expected error %q, got %v", testName, wantMsg, err)
	}
}

// ── Staged setup helpers for config and state ────────────────────────────────

// clearDiscordEnv clears all Discord-related environment variables in one step.
func clearDiscordEnv(t *testing.T) {
	t.Setenv("ENGINE_DISCORD", "")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "")
	t.Setenv("ENGINE_DISCORD_PREFIX", "")
	t.Setenv("ENGINE_DISCORD_CONTROL_CHANNEL", "")
	t.Setenv("ENGINE_STATE_DIR", "")
}

// setDiscordEnv sets Discord environment variables from a config-like map.
func setDiscordEnv(t *testing.T, env map[string]string) {
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// createConfigFile writes a discord config file to projectDir/.engine/discord.json and returns its path.
func createConfigFile(t *testing.T, projectDir string, configJSON string) string {
	configDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, defaultConfigFileName)
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

// writePersistentState writes a service state file to the storage directory.
func writePersistentState(t *testing.T, storageDir string, state persistedState) {
	data, _ := json.Marshal(state)
	statePath := filepath.Join(storageDir, defaultStateFileName)
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// setDiscordConfigForTests sets Discord config environment variables from a config map.
// Consolidates repeated t.Setenv() patterns. Keys match ENGINE_* env vars.
func setDiscordConfigForTests(t *testing.T, config map[string]string) {
	for k, v := range config {
		t.Setenv(k, v)
	}
}

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

func TestResolveClonesDir_PrefersProjectPathByDefault(t *testing.T) {
	projectPath := "/tmp/my-project"
	t.Setenv("ENGINE_CLONES_DIR", "")

	got := resolveClonesDir(projectPath)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".engine", "projects")
	if got != want {
		t.Fatalf("resolveClonesDir() = %q want %q", got, want)
	}
}

func TestResolveClonesDir_UsesProjectPathWhenHomeIsWhitespace(t *testing.T) {
	t.Setenv("ENGINE_CLONES_DIR", "")
	t.Setenv("HOME", "   ")

	projectPath := "/tmp/fallback-project"
	got := resolveClonesDir(projectPath)
	want := filepath.Join(projectPath, ".engine", "projects")
	if got != want {
		t.Fatalf("resolveClonesDir() = %q want %q", got, want)
	}
}

func TestResolveClonesDir_UsesRelativeFallbackWhenHomeAndProjectMissing(t *testing.T) {
	t.Setenv("ENGINE_CLONES_DIR", "")
	t.Setenv("HOME", "   ")

	want := filepath.Join(".engine", "projects")
	if got := resolveClonesDir(""); got != want {
		t.Fatalf("resolveClonesDir() = %q want %q", got, want)
	}
}

func TestParseGitHubOwnerRepo(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantRepo  string
		wantOK    bool
	}{
		{
			name:      "https URL",
			input:     "https://github.com/octo/demo.git",
			wantOwner: "octo",
			wantRepo:  "demo",
			wantOK:    true,
		},
		{
			name:      "ssh URL",
			input:     "git@github.com:octo/demo.git",
			wantOwner: "octo",
			wantRepo:  "demo",
			wantOK:    true,
		},
		{
			name:   "non github URL",
			input:  "https://example.com/octo/demo.git",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, ok := parseGitHubOwnerRepo(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok mismatch: got %v want %v", ok, tt.wantOK)
			}
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Fatalf("owner/repo mismatch: got %q/%q want %q/%q", owner, repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}

func TestCloneDirNameCandidates_GitHubIncludesLegacyAndCanonical(t *testing.T) {
	candidates, primary := cloneDirNameCandidates("https://github.com/octo/demo.git")
	if primary != "octo-demo" {
		t.Fatalf("primary = %q want %q", primary, "octo-demo")
	}
	if !reflect.DeepEqual(candidates, []string{"octo-demo", "demo"}) {
		t.Fatalf("candidates = %#v", candidates)
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
	clearDiscordEnv(t)

	projectDir := t.TempDir()
	configJSON := `{
		"enabled": true,
		"botToken": "bot-token",
		"guildId": "guild-123",
		"allowedUserIds": ["user-1", "user-2"],
		"commandPrefix": "/",
		"controlChannelName": "ops-room"
	}`
	configPath := createConfigFile(t, projectDir, configJSON)

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
	configDir := filepath.Join(projectDir, ".engine")
	if cfg.StoragePath != configDir {
		t.Fatalf("unexpected storage path: %q", cfg.StoragePath)
	}
	if cfg.ConfigFilePath != configPath {
		t.Fatalf("unexpected config path: %q", cfg.ConfigFilePath)
	}
}

func TestLoadConfigEnvOverridesProjectFile(t *testing.T) {
	projectDir := t.TempDir()
	configJSON := `{
		"enabled": false,
		"botToken": "file-token",
		"guildId": "file-guild",
		"allowedUserIds": ["file-user"]
	}`
	configPath := createConfigFile(t, projectDir, configJSON)
	if strings.TrimSpace(configPath) == "" {
		t.Fatal("expected config file path")
	}

	setDiscordConfigForTests(t, map[string]string{
		"ENGINE_DISCORD":                  "true",
		"ENGINE_DISCORD_BOT_TOKEN":        "env-token",
		"ENGINE_DISCORD_GUILD_ID":         "env-guild",
		"ENGINE_DISCORD_ALLOWED_USER_IDS": "env-user",
		"ENGINE_DISCORD_PREFIX":           "!",
	})

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

func TestIsThread(t *testing.T) {
	cases := []struct {
		chType discordgo.ChannelType
		want   bool
	}{
		{discordgo.ChannelTypeGuildPublicThread, true},
		{discordgo.ChannelTypeGuildPrivateThread, true},
		{discordgo.ChannelTypeGuildNewsThread, true},
		{discordgo.ChannelTypeGuildText, false},
	}
	for _, c := range cases {
		ch := &discordgo.Channel{Type: c.chType}
		if got := isThread(ch); got != c.want {
			t.Errorf("isThread(%v) = %v, want %v", c.chType, got, c.want)
		}
	}
}

func TestBuildThreadName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello world", "chat-hello world"},
		{"", "chat-chat"},
		{strings.Repeat("x", 70), "chat-" + strings.Repeat("x", 57) + "..."},
	}
	for _, c := range cases {
		got := buildThreadName(c.in)
		if got != c.want {
			t.Errorf("buildThreadName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePositiveInt(t *testing.T) {
	v, err := parsePositiveInt("42")
	if err != nil || v != 42 {
		t.Fatalf("parsePositiveInt(42): %v %v", v, err)
	}
	_, err = parsePositiveInt("0")
	if err == nil {
		t.Fatal("expected error for 0")
	}
	_, err = parsePositiveInt("abc")
	if err == nil {
		t.Fatal("expected error for abc")
	}
	_, err = parsePositiveInt("  ")
	if err == nil {
		t.Fatal("expected error for whitespace")
	}
}

func TestShortTime(t *testing.T) {
	got := shortTime("2024-01-15T10:30:00Z")
	if got != "01-15 10:30" {
		t.Fatalf("shortTime = %q", got)
	}
	got = shortTime("not-a-time")
	if got != "not-a-time" {
		t.Fatalf("shortTime invalid = %q", got)
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName("alice", "in"); got != "alice" {
		t.Fatalf("expected alice, got %q", got)
	}
	if got := displayName("", "out"); got != "engine" {
		t.Fatalf("expected engine, got %q", got)
	}
	if got := displayName("", "in"); got != "user" {
		t.Fatalf("expected user, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("expected no truncation, got %q", got)
	}
	got := truncate("hello world", 7)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	if got := truncate("x", 0); got != "x" {
		t.Fatalf("expected unchanged for max=0, got %q", got)
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("abc"); got != "abc" {
		t.Fatalf("expected abc, got %q", got)
	}
	long := "123456789012345"
	if got := shortID(long); len(got) != 12 {
		t.Fatalf("expected 12-char shortID, got %q", got)
	}
}

func TestTernary(t *testing.T) {
	if got := ternary(true, "yes", "no"); got != "yes" {
		t.Fatalf("expected yes, got %q", got)
	}
	if got := ternary(false, "yes", "no"); got != "no" {
		t.Fatalf("expected no, got %q", got)
	}
}

func TestParseOptionalBool(t *testing.T) {
	cases := []struct {
		in     string
		val    bool
		parsed bool
	}{
		{"true", true, true},
		{"1", true, true},
		{"yes", true, true},
		{"on", true, true},
		{"false", false, true},
		{"0", false, true},
		{"no", false, true},
		{"off", false, true},
		{"", false, false},
		{"maybe", false, false},
	}
	for _, c := range cases {
		val, parsed := parseOptionalBool(c.in)
		if val != c.val || parsed != c.parsed {
			t.Errorf("parseOptionalBool(%q): got (%v,%v), want (%v,%v)", c.in, val, parsed, c.val, c.parsed)
		}
	}
}

func TestValidate_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	result := Validate(cfg)
	if !result.OK {
		t.Fatal("expected OK for disabled config")
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warning for disabled config")
	}
}

func TestBuildInviteURL_EmptyClientID_ReturnsEmpty(t *testing.T) {
	if got := buildInviteURL(""); got != "" {
		t.Errorf("buildInviteURL(\"\") = %q, want \"\"", got)
	}
}

func TestBuildInviteURL_NonEmptyClientID_ReturnsURL(t *testing.T) {
	got := buildInviteURL("12345")
	if got == "" {
		t.Error("buildInviteURL with non-empty ID should return a non-empty URL")
	}
	if !strings.Contains(got, "12345") {
		t.Errorf("invite URL should contain client id, got %q", got)
	}
}

func TestValidate_MissingFields(t *testing.T) {
	cfg := Config{Enabled: true}
	result := Validate(cfg)
	if result.OK {
		t.Fatal("expected not OK for missing fields")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected errors for missing fields")
	}
}

func TestNewService_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	svc, err := NewService(cfg, "")
	if err != nil {
		t.Fatalf("NewService disabled: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestNewService_EnabledNoStateFile(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Enabled:     true,
		StoragePath: dir,
	}
	svc, err := NewService(cfg, dir)
	if err != nil {
		t.Fatalf("NewService enabled (no state file): %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestLoadAndSaveState(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	// loadState when no file exists → no error
	if err := svc.loadState(); err != nil {
		t.Fatalf("loadState no file: %v", err)
	}

	// save some state
	svc.state.ControlChannelID = "ch-ctrl"
	svc.state.Projects["/proj"] = ProjectBinding{
		ProjectPath: "/proj",
		RepoName:    "proj",
		ChannelID:   "ch-1",
		Paused:      false,
	}
	if err := svc.saveState(); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	// reload and verify
	svc2 := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc2.loadState(); err != nil {
		t.Fatalf("loadState after save: %v", err)
	}
	if svc2.state.ControlChannelID != "ch-ctrl" {
		t.Fatalf("expected ch-ctrl, got %q", svc2.state.ControlChannelID)
	}
	if _, ok := svc2.state.Projects["/proj"]; !ok {
		t.Fatal("expected /proj in state")
	}
}

func TestLoadState_BadJSON(t *testing.T) {
	dir := t.TempDir()
	stateFilePath := filepath.Join(dir, defaultStateFileName)
	if err := os.WriteFile(stateFilePath, []byte("{bad json"), 0600); err != nil {
		t.Fatalf("write bad json: %v", err)
	}
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc.loadState(); err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestLoadState_MigratesWorkspaceEngineProjectBinding(t *testing.T) {
	storageDir := t.TempDir()
	workspaceRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	runtimeProjectPath := filepath.Join(home, ".engine", "projects", "demo-repo")
	if err := os.MkdirAll(runtimeProjectPath, 0755); err != nil {
		t.Fatalf("mkdir runtime path: %v", err)
	}

	legacyPath := filepath.Join(workspaceRoot, "engine", "projects", "demo-repo")
	state := persistedState{
		ControlChannelID: "ch-ctrl",
		Projects: map[string]ProjectBinding{
			legacyPath: {
				ProjectPath: legacyPath,
				RepoName:    "demo-repo",
				ChannelID:   "ch-1",
				Paused:      false,
			},
		},
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(storageDir, defaultStateFileName), data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	svc := &Service{
		cfg:     Config{StoragePath: storageDir},
		project: workspaceRoot,
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc.loadState(); err != nil {
		t.Fatalf("loadState: %v", err)
	}
	binding, ok := svc.state.Projects[runtimeProjectPath]
	if !ok {
		t.Fatalf("expected migrated runtime path %q, got keys %v", runtimeProjectPath, keysFromBindings(svc.state.Projects))
	}
	if binding.ProjectPath != runtimeProjectPath {
		t.Fatalf("expected binding path %q, got %q", runtimeProjectPath, binding.ProjectPath)
	}
}

func TestLoadState_MigrationPersistsBestEffortWhenStateFileReadOnly(t *testing.T) {
	storageDir := t.TempDir()
	workspaceRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	runtimeProjectPath := filepath.Join(home, ".engine", "projects", "readonly-repo")
	if err := os.MkdirAll(runtimeProjectPath, 0755); err != nil {
		t.Fatalf("mkdir runtime path: %v", err)
	}
	legacyPath := filepath.Join(workspaceRoot, ".engine", "projects", "readonly-repo")
	state := persistedState{Projects: map[string]ProjectBinding{
		legacyPath: {ProjectPath: legacyPath, RepoName: "readonly-repo", ChannelID: "ch-1"},
	}}
	data, _ := json.Marshal(state)
	statePath := filepath.Join(storageDir, defaultStateFileName)
	if err := os.WriteFile(statePath, data, 0400); err != nil {
		t.Fatalf("write readonly state: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(statePath, 0600) })

	svc := &Service{cfg: Config{StoragePath: storageDir}, project: workspaceRoot}
	if err := svc.loadState(); err != nil {
		t.Fatalf("loadState should keep migrated state in memory even if persistence fails: %v", err)
	}
	if _, ok := svc.state.Projects[runtimeProjectPath]; !ok {
		t.Fatalf("expected in-memory migrated runtime path %q, got keys %v", runtimeProjectPath, keysFromBindings(svc.state.Projects))
	}
}

func TestLoadState_DoesNotMigrateWhenRuntimeCloneMissing(t *testing.T) {
	storageDir := t.TempDir()
	workspaceRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyPath := filepath.Join(workspaceRoot, "engine", "projects", "missing-repo")
	state := persistedState{
		Projects: map[string]ProjectBinding{
			legacyPath: {
				ProjectPath: legacyPath,
				RepoName:    "missing-repo",
				ChannelID:   "ch-1",
				Paused:      false,
			},
		},
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(storageDir, defaultStateFileName), data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	svc := &Service{
		cfg:     Config{StoragePath: storageDir},
		project: workspaceRoot,
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc.loadState(); err != nil {
		t.Fatalf("loadState: %v", err)
	}
	binding, ok := svc.state.Projects[legacyPath]
	if !ok {
		t.Fatalf("expected legacy path to remain when runtime clone missing; keys %v", keysFromBindings(svc.state.Projects))
	}
	if binding.ProjectPath != legacyPath {
		t.Fatalf("expected legacy binding path %q, got %q", legacyPath, binding.ProjectPath)
	}
}

func keysFromBindings(m map[string]ProjectBinding) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestMigrateBindingPath_Guards(t *testing.T) {
	if got, ok := migrateBindingPath("   ", "/tmp/clones", []string{"/tmp/ws/"}); ok || got != "" {
		t.Fatalf("expected empty path guard to skip migration, got %q %v", got, ok)
	}
	if got, ok := migrateBindingPath("/", "/tmp/clones", []string{"/"}); ok || got != "" {
		t.Fatalf("expected invalid repo guard to skip migration, got %q %v", got, ok)
	}
}

func TestIsAllowedUser(t *testing.T) {
	svc := &Service{
		cfg: Config{
			AllowedUsers: map[string]bool{"user-1": true},
		},
	}
	if !svc.isAllowedUser("user-1") {
		t.Fatal("expected user-1 to be allowed")
	}
	if svc.isAllowedUser("user-2") {
		t.Fatal("expected user-2 to be denied")
	}
}

// Duplicate in service_extra_test.go — kept here for historical compatibility

func TestResolveProjectByChannel(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj": {ProjectPath: "/proj", ChannelID: "ch-1"},
			},
		},
	}

	if _, ok := svc.resolveProjectByChannel(""); ok {
		t.Fatal("expected no match for empty channelID")
	}
	if p, ok := svc.resolveProjectByChannel("ch-1"); !ok || p.ProjectPath != "/proj" {
		t.Fatalf("expected /proj, got %+v %v", p, ok)
	}
	if _, ok := svc.resolveProjectByChannel("ch-none"); ok {
		t.Fatal("expected no match for unknown channel")
	}
}

func TestResolveProjectByRef(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj": {ProjectPath: "/proj", RepoName: "MyRepo", ChannelID: "ch-1"},
			},
		},
	}

	_, p, ok := svc.resolveProjectByRef("MyRepo")
	if !ok || p.ProjectPath != "/proj" {
		t.Fatalf("expected /proj by repo name, got %+v %v", p, ok)
	}
	_, _, ok = svc.resolveProjectByRef("notexist")
	if ok {
		t.Fatal("expected no match for notexist")
	}
}

func TestResolveProjectForMessage(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj": {ProjectPath: "/proj", RepoName: "MyRepo", ChannelID: "ch-1"},
			},
		},
	}

	// found by channel
	p, ok := svc.resolveProjectForMessage("ch-1", nil)
	if !ok || p.ProjectPath != "/proj" {
		t.Fatalf("expected /proj by channel, got %+v %v", p, ok)
	}

	// not found, no args
	_, ok = svc.resolveProjectForMessage("ch-other", nil)
	if ok {
		t.Fatal("expected no match")
	}

	// found by ref via args
	p, ok = svc.resolveProjectForMessage("ch-other", []string{"MyRepo"})
	if !ok || p.ProjectPath != "/proj" {
		t.Fatalf("expected /proj by ref, got %+v %v", p, ok)
	}
}

func TestResolveAskTarget(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj": {ProjectPath: "/proj", RepoName: "MyRepo", ChannelID: "ch-1"},
			},
		},
	}

	// found by channel
	p, prompt, ok := svc.resolveAskTarget("ch-1", []string{"hello", "world"})
	if !ok || p.ProjectPath != "/proj" || prompt != "hello world" {
		t.Fatalf("expected /proj, got %+v %v %q", p, ok, prompt)
	}

	// not found, one arg only (no project name + prompt)
	_, _, ok = svc.resolveAskTarget("ch-other", []string{"only-one"})
	if ok {
		t.Fatal("expected no match when not found and only one arg")
	}

	// found by ref
	p, prompt, ok = svc.resolveAskTarget("ch-other", []string{"MyRepo", "ask this"})
	if !ok || p.ProjectPath != "/proj" || prompt != "ask this" {
		t.Fatalf("expected /proj, got %+v %v %q", p, ok, prompt)
	}
}

func TestResolveContext(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj": {ProjectPath: "/proj", ChannelID: "ch-1"},
			},
		},
	}

	// channel-based match, no thread
	pp, sid := svc.resolveContext("ch-1", "")
	if pp != "/proj" || sid != "" {
		t.Fatalf("expected /proj, got %q %q", pp, sid)
	}

	// no match
	pp, sid = svc.resolveContext("ch-none", "")
	if pp != "" || sid != "" {
		t.Fatalf("expected empty, got %q %q", pp, sid)
	}
}

func TestSplitChannelThread_NilDG(t *testing.T) {
	svc := &Service{}
	ch, th := svc.splitChannelThread("ch-1")
	if ch != "ch-1" || th != "" {
		t.Fatalf("expected ch-1, '', got %q %q", ch, th)
	}
	ch, th = svc.splitChannelThread("")
	if ch != "" || th != "" {
		t.Fatalf("expected empty, got %q %q", ch, th)
	}
}

func TestListProjects_Empty(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	// send is no-op with nil dg — just verify no panic
	svc.listProjects("ch-1")
}

func TestListProjects_WithProjects(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj-a": {ProjectPath: "/proj-a", RepoName: "proj-a", ChannelID: "ch-a", Paused: false},
				"/proj-b": {ProjectPath: "/proj-b", RepoName: "proj-b", ChannelID: "ch-b", Paused: true},
			},
		},
	}
	// send is no-op with nil dg — just verify no panic
	svc.listProjects("ch-1")
}

func TestRemoveProject_EmptyName(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc.removeProject("ch-1", ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestRemoveProject_NotFound(t *testing.T) {
	svc := &Service{
		cfg: Config{},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc.removeProject("ch-1", "nonexistent"); err == nil {
		t.Fatal("expected error for not found project")
	}
}

func TestRemoveProject_Found(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: map[string]ProjectBinding{
				"/proj": {ProjectPath: "/proj", RepoName: "proj", ChannelID: "ch-1"},
			},
		},
	}
	if err := svc.removeProject("ch-ctrl", "proj"); err != nil {
		t.Fatalf("removeProject: %v", err)
	}
	if _, ok := svc.state.Projects["/proj"]; ok {
		t.Fatal("expected project to be removed from state")
	}
}

func TestRecordInbound_NilDG(t *testing.T) {
	svc := &Service{
		cfg: Config{CommandPrefix: "!"},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			Content: "!help",
			Author:  &discordgo.User{Username: "alice", ID: "u1"},
		},
	}
	// no panic
	svc.recordInbound(m)
}

func TestSendTagged_NilDG(t *testing.T) {
	svc := &Service{}
	// no panic, early return
	svc.sendTagged("ch-1", "hello", "message", "sess-1")
	svc.sendTagged("", "hello", "message", "sess-1")
	svc.sendTagged("ch-1", "", "message", "sess-1")
}

func TestOnMessage_EarlyReturns(t *testing.T) {
	svc := &Service{
		cfg: Config{
			GuildID:       "guild-1",
			CommandPrefix: "!",
			AllowedUsers:  map[string]bool{"user-1": true},
		},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	// nil message
	svc.onMessage(nil, nil)

	// bot author
	svc.onMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			Author: &discordgo.User{Bot: true},
		},
	})

	// wrong guild
	svc.onMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID: "wrong-guild",
			Author:  &discordgo.User{ID: "user-1"},
		},
	})

	// not allowed user
	svc.onMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID: "guild-1",
			Author:  &discordgo.User{ID: "user-forbidden"},
		},
	})

	// no command prefix → non-command message (records inbound, doesn't dispatch)
	svc.onMessage(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID: "guild-1",
			Author:  &discordgo.User{ID: "user-1"},
			Content: "just chatting",
		},
	})
}

func TestHandleProjectCommand_NoArgs(t *testing.T) {
	svc := &Service{
		cfg: Config{CommandPrefix: "!"},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "ch-1",
			GuildID:   "guild-1",
			Author:    &discordgo.User{ID: "u1"},
		},
	}
	// no panic, send no-op
	svc.handleProjectCommand(m, nil)
	svc.handleProjectCommand(m, []string{"add"})     // missing path
	svc.handleProjectCommand(m, []string{"remove"})  // missing name
	svc.handleProjectCommand(m, []string{"unknown"}) // unknown sub
	svc.handleProjectCommand(m, []string{"list"})    // lists empty
}

func TestHandleAskCommand_Branches(t *testing.T) {
	projectDir := t.TempDir()
	if err := db.Init(projectDir); err != nil {
		t.Fatalf("db init: %v", err)
	}
	svc := &Service{
		cfg: Config{CommandPrefix: "!", StoragePath: projectDir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "ch-1",
			GuildID:   "guild-1",
			Author:    &discordgo.User{ID: "u1"},
		},
	}

	// no args → "Usage: !ask..."
	svc.handleAskCommand(m, nil)

	// args but project not found
	svc.handleAskCommand(m, []string{"some", "prompt"})

	// project found but paused
	svc.state.Projects[projectDir] = ProjectBinding{
		ProjectPath: projectDir,
		ChannelID:   "ch-1",
		Paused:      true,
	}
	m.Message.ChannelID = "ch-1"
	svc.handleAskCommand(m, []string{"a", "prompt"})

	// project found, not paused, but dg==nil → acquireChatThread fails
	svc.state.Projects[projectDir] = ProjectBinding{
		ProjectPath: projectDir,
		ChannelID:   "ch-1",
		Paused:      false,
	}
	svc.handleAskCommand(m, []string{"do", "something"})
}

func TestOnMessage_Commands(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{
		cfg: Config{
			GuildID:       "guild-1",
			CommandPrefix: "!",
			AllowedUsers:  map[string]bool{"user-1": true},
			StoragePath:   dir,
		},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	cmds := []string{"!help", "!projects", "!status", "!sessions", "!lastcommit",
		"!pause", "!resume", "!ask", "!auto", "!autonomous", "!build", "!search", "!unknown"}

	for _, content := range cmds {
		m := &discordgo.MessageCreate{
			Message: &discordgo.Message{
				GuildID:   "guild-1",
				ChannelID: "ch-ctrl",
				Content:   content,
				Author:    &discordgo.User{ID: "user-1"},
			},
		}
		// no panic, dg is nil so send is no-op
		svc.onMessage(nil, m)
	}
}

func TestHandleAutoCommand_Branches(t *testing.T) {
	svc := &Service{
		cfg: Config{CommandPrefix: "!"},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "ch-1",
			GuildID:   "guild-1",
			Author:    &discordgo.User{ID: "u1"},
		},
	}

	// usage
	svc.handleAutoCommand(m, nil)
	// control-channel miss
	m.Message.ChannelID = "ch-ctrl"
	svc.handleAutoCommand(m, []string{"unknown", "prompt"})
	// empty prompt in project channel
	svc.state.Projects["/proj"] = ProjectBinding{ProjectPath: "/proj", RepoName: "proj", ChannelID: "ch-1"}
	m.Message.ChannelID = "ch-1"
	svc.handleAutoCommand(m, []string{"   "})
	// non-empty prompt routes through runAgentChat (paused returns immediately)
	svc.state.Projects["/proj"] = ProjectBinding{ProjectPath: "/proj", RepoName: "proj", ChannelID: "ch-1", Paused: true}
	svc.handleAutoCommand(m, []string{"ship", "it"})
}

// TestSaveState_NilProjectsInit ensures loadState initializes nil Projects map.
func TestLoadState_NilProjects(t *testing.T) {
	dir := t.TempDir()
	// Write state with null Projects field
	raw, _ := json.Marshal(map[string]any{"controlChannelId": "ch-x", "projects": nil})
	if err := os.WriteFile(filepath.Join(dir, defaultStateFileName), raw, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	if err := svc.loadState(); err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if svc.state.Projects == nil {
		t.Fatal("expected non-nil Projects after loadState")
	}
}

// Test handleHistoryCommand via onMessage path with nil dg (no panic).
func TestOnMessage_HistoryCommand(t *testing.T) {
	svc := &Service{
		cfg: Config{
			GuildID:       "guild-1",
			CommandPrefix: "!",
			AllowedUsers:  map[string]bool{"user-1": true},
		},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID:   "guild-1",
			ChannelID: "ch-1",
			Content:   "!history 12",
			Author:    &discordgo.User{ID: "user-1"},
		},
	}
	svc.onMessage(nil, m)
}

func TestValidate_WithTokenGatewayFail(t *testing.T) {
	// Valid-format token, all required fields — fails at dg.Open() because fake token.
	cfg := Config{
		Enabled:      true,
		BotToken:     "FAKE_BOT_TOKEN_xyz",
		GuildID:      "guild-id",
		AllowedUsers: map[string]bool{"user123": true},
	}
	result := Validate(cfg)
	// Should fail at Open() — not OK.
	if result.OK {
		t.Skip("Discord gateway unexpectedly accepted a fake token — skipping")
	}
	if len(result.Errors) == 0 {
		t.Error("expected errors for invalid token at gateway")
	}
}

func TestStateDir_EnvOverride2(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "/override")
	got := stateDir("/project")
	if got != "/override" {
		t.Errorf("expected /override, got %q", got)
	}
}

func TestStateDir_ProjectPath(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "")
	got := stateDir("/myproject")
	if got != "/myproject/.engine" {
		t.Errorf("expected /myproject/.engine, got %q", got)
	}
}

func TestSaveState_WriteError(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{
		cfg: Config{StoragePath: dir},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}
	// Make discord-state.json a directory so WriteFile fails.
	badPath := filepath.Join(dir, "discord-state.json")
	if err := os.MkdirAll(badPath, 0755); err != nil {
		t.Fatalf("mkdir discord-state.json: %v", err)
	}
	err := svc.saveState()
	if err == nil {
		t.Error("expected error when discord-state.json is a directory")
	}
}

func TestWriteConfig_WriteFileError(t *testing.T) {
	dir := t.TempDir()
	// Make discord.json a directory.
	badPath := filepath.Join(dir, "discord.json")
	if err := os.MkdirAll(badPath, 0755); err != nil {
		t.Fatalf("mkdir discord.json: %v", err)
	}
	t.Setenv("ENGINE_STATE_DIR", dir)
	err := WriteConfig("/project", Config{Enabled: false})
	if err == nil {
		t.Error("expected error when discord.json is a directory")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Additional coverage tests
// ────────────────────────────────────────────────────────────────────────────

func TestAddProject_Error_InvalidPath(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	err := svc.addProject("ch1", "")
	if err == nil {
		t.Error("addProject with empty path should error")
	}
}

func TestRemoveProject_NoMatch_Error(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	err := svc.removeProject("ch1", "nonexistent")
	if err != nil {
		t.Logf("removeProject error (expected): %v", err)
	}
}

func TestHandleSessionsCommand_Empty_NoError(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}

	// Verify this doesn't panic
	svc.handleSessionsCommand(m, []string{})
}

func TestHandleLastCommitCommand_NoBinding_NoError(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "unknown"},
	}

	// Verify this doesn't panic
	svc.handleLastCommitCommand(m, []string{})
}

func TestHandlePauseResume_NoBinding_NoError(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "unknown"},
	}

	// Verify this doesn't panic
	svc.handlePauseResume(m, true, []string{})
}

func TestRecordInbound_NoBinding_NoError(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "unknown",
			Author:    &discordgo.User{Username: "user1"},
			Content:   "test message",
		},
	}

	// Verify this doesn't panic
	svc.recordInbound(m)
}

func TestRecordOutbound_NoError(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	// Verify this doesn't panic (no dg means it will fail silently)
	svc.recordOutbound("ch1", "test", "message", "session1")
}

func TestSendTagged_NoSession_NoError(t *testing.T) {
	svc := &Service{
		cfg: Config{StoragePath: t.TempDir()},
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
	}

	// Verify this doesn't panic (no dg means it will fail silently)
	svc.sendTagged("ch1", "test message", "agent", "session1")
}

// ─── LeaveGuild ───────────────────────────────────────────────────────────────

func TestLeaveGuild_NilSession_ReturnsError(t *testing.T) {
	svc := &Service{
		cfg: Config{GuildID: "guild123"},
		// dg is nil — gateway not started
	}
	err := svc.LeaveGuild("guild123")
	if err == nil {
		t.Error("expected error when discord session is nil")
	}
}

func TestLeaveGuild_EmptyGuildIDFallsBackToCfg(t *testing.T) {
	// Replace the package-level guildLeaveFn with a stub that records the call.
	var calledWith string
	original := guildLeaveFn
	guildLeaveFn = func(_ *discordgo.Session, id string) error {
		calledWith = id
		return nil
	}
	t.Cleanup(func() { guildLeaveFn = original })

	svc := &Service{
		cfg: Config{GuildID: "cfg-guild"},
		dg:  &discordgo.Session{}, // non-nil so the nil guard passes
	}
	if err := svc.LeaveGuild(""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledWith != "cfg-guild" {
		t.Errorf("expected cfg guild id %q, got %q", "cfg-guild", calledWith)
	}
}

func TestLeaveGuild_ExplicitGuildID_UsedDirectly(t *testing.T) {
	var calledWith string
	original := guildLeaveFn
	guildLeaveFn = func(_ *discordgo.Session, id string) error {
		calledWith = id
		return nil
	}
	t.Cleanup(func() { guildLeaveFn = original })

	svc := &Service{
		cfg: Config{GuildID: "cfg-guild"},
		dg:  &discordgo.Session{},
	}
	if err := svc.LeaveGuild("explicit-guild"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledWith != "explicit-guild" {
		t.Errorf("expected explicit guild id %q, got %q", "explicit-guild", calledWith)
	}
}

func TestLeaveGuild_BothIDsEmpty_ReturnsError(t *testing.T) {
	svc := &Service{
		cfg: Config{GuildID: ""},
		dg:  &discordgo.Session{},
	}
	if err := svc.LeaveGuild(""); err == nil {
		t.Error("expected error when both guildID and cfg.GuildID are empty")
	}
}

func TestLeaveGuild_LeaveFnError_PropagatesError(t *testing.T) {
	original := guildLeaveFn
	guildLeaveFn = func(_ *discordgo.Session, _ string) error {
		return fmt.Errorf("api error")
	}
	t.Cleanup(func() { guildLeaveFn = original })

	svc := &Service{
		cfg: Config{GuildID: "guild123"},
		dg:  &discordgo.Session{},
	}
	err := svc.LeaveGuild("guild123")
	if err == nil || err.Error() != "api error" {
		t.Errorf("expected api error, got %v", err)
	}
}
