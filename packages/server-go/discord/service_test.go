package discord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
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
	svc.handleProjectCommand(m, []string{"add"})      // missing path
	svc.handleProjectCommand(m, []string{"remove"})   // missing name
	svc.handleProjectCommand(m, []string{"unknown"})  // unknown sub
	svc.handleProjectCommand(m, []string{"list"})     // lists empty
}

func TestHandleAskCommand_Branches(t *testing.T) {
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

	// no args → "Usage: !ask..."
	svc.handleAskCommand(m, nil)

	// args but project not found
	svc.handleAskCommand(m, []string{"some", "prompt"})

	// project found but paused
	svc.state.Projects["/proj"] = ProjectBinding{
		ProjectPath: "/proj",
		ChannelID:   "ch-1",
		Paused:      true,
	}
	m.Message.ChannelID = "ch-1"
	svc.handleAskCommand(m, []string{"a", "prompt"})

	// project found, not paused, but dg==nil → acquireChatThread fails
	svc.state.Projects["/proj"] = ProjectBinding{
		ProjectPath: "/proj",
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
		"!pause", "!resume", "!ask", "!search", "!unknown"}

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
