package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"path/filepath"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/db"
)

func makeDiscordRESTSession(t *testing.T) *discordgo.Session {
	t.Helper()
	dg, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	return dg
}

func installDiscordChannelAPIShim(t *testing.T, channels map[string]*discordgo.Channel) func() {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/channels/") {
			id := strings.TrimPrefix(r.URL.Path, "/channels/")
			if ch, ok := channels[id]; ok {
				_ = json.NewEncoder(w).Encode(ch)
				return
			}
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	oldEndpointAPI := discordgo.EndpointAPI
	oldEndpointChannels := discordgo.EndpointChannels
	oldEndpointChannel := discordgo.EndpointChannel

	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(channelID string) string {
		return discordgo.EndpointChannels + channelID
	}

	return func() {
		discordgo.EndpointAPI = oldEndpointAPI
		discordgo.EndpointChannels = oldEndpointChannels
		discordgo.EndpointChannel = oldEndpointChannel
		server.Close()
	}
}

// ── DB init helper ────────────────────────────────────────────────────────────

// initDiscordTestDB sets ENGINE_STATE_DIR to a fresh temp dir and initialises
// the SQLite database so discord tests that touch DB functions can run.
func initDiscordTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", dir)
	if err := db.Init(""); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	return dir
}

// newDisabledSvc creates a discord Service with a disabled Config (no Discord
// connection), a tmp state dir, and an initialised DB.
func newDisabledSvc(t *testing.T) (*Service, string) {
	t.Helper()
	stateRoot := initDiscordTestDB(t)
	svc, err := NewService(Config{Enabled: false, CommandPrefix: "!"}, stateRoot)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, stateRoot
}

// addProject injects a project binding into s.state so handler tests can
// resolve a project by channel without requiring a real Discord API call.
func addProject(s *Service, projectPath, channelID, repoName string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		ChannelID:   channelID,
		RepoName:    repoName,
	}
}

// msg builds a minimal *discordgo.MessageCreate for use in handler tests.
func msg(channelID, content string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: channelID,
			Content:   content,
			Author:    &discordgo.User{Username: "testuser", ID: "u-test"},
		},
	}
}

// ── DiscordBridge interface methods ───────────────────────────────────────────

func TestServiceCurrentConfig(t *testing.T) {
	svc, dir := newDisabledSvc(t)
	cfg := svc.CurrentConfig()
	if cfg.Enabled {
		t.Error("expected Enabled=false")
	}
	if cfg.CommandPrefix != "!" {
		t.Errorf("CommandPrefix = %q, want !", cfg.CommandPrefix)
	}
	_ = dir
}

func TestServiceReload_Disabled(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	newCfg := Config{Enabled: false, CommandPrefix: "?"}
	err := svc.Reload(newCfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if svc.cfg.CommandPrefix != "?" {
		t.Errorf("cfg not updated, CommandPrefix = %q", svc.cfg.CommandPrefix)
	}
}

func TestServiceSearchHistory_Empty(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	hits, err := svc.SearchHistory("/nonexistent", "query", "", 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	// Empty DB → no hits expected.
	_ = hits
}

func TestServiceRecentHistory_Empty(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	rows, err := svc.RecentHistory("/nonexistent", "", "", 10)
	if err != nil {
		t.Fatalf("RecentHistory: %v", err)
	}
	_ = rows
}

// ── stateDir ─────────────────────────────────────────────────────────────────

func TestStateDir_EnvOverride(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "/tmp/test-engine-state")
	got := stateDir("/some/project")
	if got != "/tmp/test-engine-state" {
		t.Errorf("stateDir = %q, want /tmp/test-engine-state", got)
	}
}

func TestStateDir_EmptyProjectPath(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "")
	got := stateDir("")
	// Should return a non-empty path (UserConfigDir or UserHomeDir based path).
	if got == "" {
		t.Error("stateDir should return non-empty path when projectPath is empty")
	}
}

// ── handleStatusCommand ───────────────────────────────────────────────────────

func TestHandleStatusCommand_ProjectNotFound(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	// no panic — send is a no-op since dg is nil
	svc.handleStatusCommand(msg("unknown-channel", ""), nil)
}

func TestHandleStatusCommand_ProjectFound(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	// Save state so saveState works.
	svc.project = stateRoot
	addProject(svc, stateRoot, "ch-status", "test-repo")

	// no panic — GetStatus handles non-git dirs gracefully, send is nil-safe
	svc.handleStatusCommand(msg("ch-status", ""), nil)
}

// ── handleSessionsCommand ──────────────────────────────────────────────────────

func TestHandleSessionsCommand_ProjectNotFound(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleSessionsCommand(msg("ch-none", ""), nil)
}

func TestHandleSessionsCommand_NoSessions(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	addProject(svc, stateRoot, "ch-sess", "repo")

	svc.handleSessionsCommand(msg("ch-sess", ""), nil)
}

// ── handleLastCommitCommand ───────────────────────────────────────────────────

func TestHandleLastCommitCommand_ProjectNotFound(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleLastCommitCommand(msg("ch-none", ""), nil)
}

func TestHandleLastCommitCommand_NoCommits(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	addProject(svc, stateRoot, "ch-commit", "repo")
	// Non-git dir → GetLog returns empty → "No commit information" path
	svc.handleLastCommitCommand(msg("ch-commit", ""), nil)
}

// ── handlePauseResume ─────────────────────────────────────────────────────────

func TestHandlePauseResume_ProjectNotFound(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handlePauseResume(msg("ch-none", ""), true, nil)
}

func TestHandlePauseResume_Pause(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	addProject(svc, stateRoot, "ch-pause", "repo")

	// saveState writes to stateDir; make sure project dir exists
	if err := os.MkdirAll(filepath.Join(stateRoot, ".engine"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svc.handlePauseResume(msg("ch-pause", ""), true, nil)
}

func TestHandlePauseResume_Resume(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	addProject(svc, stateRoot, "ch-resume", "repo")
	if err := os.MkdirAll(filepath.Join(stateRoot, ".engine"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svc.handlePauseResume(msg("ch-resume", ""), false, nil)
}

// ── handleSearchCommand ───────────────────────────────────────────────────────

func TestHandleSearchCommand_NoArgs(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleSearchCommand(msg("ch", ""), nil)
}

func TestHandleSearchCommand_NoResults(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleSearchCommand(msg("ch", ""), []string{"query-that-wont-match"})
}

// ── handleHistoryCommand ──────────────────────────────────────────────────────

func TestHandleHistoryCommand_NoMessages(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleHistoryCommand(msg("ch", ""), nil)
}

func TestHandleHistoryCommand_WithHours(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleHistoryCommand(msg("ch", ""), []string{"48"})
}

func TestHandleHistoryCommand_InvalidHours(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.handleHistoryCommand(msg("ch", ""), []string{"notanumber"})
}

// ── helper/function coverage boosters ────────────────────────────────────────

func TestEnsureSession_CreatesAndReuses(t *testing.T) {
	stateRoot := initDiscordTestDB(t)
	projectDir := t.TempDir()

	first, err := ensureSession(projectDir)
	if err != nil {
		t.Fatalf("ensureSession create: %v", err)
	}
	if first == "" {
		t.Fatal("expected non-empty session id")
	}

	second, err := ensureSession(projectDir)
	if err != nil {
		t.Fatalf("ensureSession reuse: %v", err)
	}
	if second == "" {
		t.Fatal("expected reused session id")
	}
	_ = stateRoot
}

func TestResolveProjectByRef_MatchesPathAndRepo(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "channel-1", "MyRepo")

	if _, _, ok := svc.resolveProjectByRef(projectPath); !ok {
		t.Fatal("expected resolve by full path")
	}
	if _, _, ok := svc.resolveProjectByRef("MyRepo"); !ok {
		t.Fatal("expected resolve by repo name")
	}
	if _, _, ok := svc.resolveProjectByRef("myrepo"); !ok {
		t.Fatal("expected resolve by slug/lower name")
	}
}

func TestResolveProjectByChannel_NoDGPaths(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "bound-channel", "repo")

	if _, ok := svc.resolveProjectByChannel(""); ok {
		t.Fatal("expected empty channel to fail")
	}
	if _, ok := svc.resolveProjectByChannel("bound-channel"); !ok {
		t.Fatal("expected direct channel binding match")
	}
	if _, ok := svc.resolveProjectByChannel("unknown"); ok {
		t.Fatal("expected unknown channel to fail without discord session")
	}
}

func TestSendAndSendTagged_NoopCases(t *testing.T) {
	svc, _ := newDisabledSvc(t)

	// dg is nil: both methods should no-op safely.
	svc.send("ch", "message")
	svc.send("", "message")
	svc.send("ch", "")

	svc.sendTagged("ch", "message", "agent", "s1")
	svc.sendTagged("", "message", "agent", "s1")
	svc.sendTagged("ch", "", "agent", "s1")
}

func TestSplitChannelThread_NoDiscordSession(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	channel, thread := svc.splitChannelThread("abc123")
	if channel != "abc123" || thread != "" {
		t.Fatalf("unexpected split result: channel=%q thread=%q", channel, thread)
	}
}

func TestSplitChannelThread_WithThreadChannelFromState(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	restore := installDiscordChannelAPIShim(t, map[string]*discordgo.Channel{
		"thread-1": {
		ID:       "thread-1",
		GuildID:  "guild-test",
		ParentID: "channel-parent",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	channel, thread := svc.splitChannelThread("thread-1")
	if channel != "channel-parent" || thread != "thread-1" {
		t.Fatalf("unexpected split: channel=%q thread=%q", channel, thread)
	}
}

func TestResolveProjectByChannel_WithThreadParentLookup(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "channel-parent", "repo")
	restore := installDiscordChannelAPIShim(t, map[string]*discordgo.Channel{
		"thread-2": {
		ID:       "thread-2",
		GuildID:  "guild-test",
		ParentID: "channel-parent",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	p, ok := svc.resolveProjectByChannel("thread-2")
	if !ok {
		t.Fatal("expected thread channel to resolve through parent binding")
	}
	if p.ProjectPath != projectPath {
		t.Fatalf("expected project path %q, got %+v", projectPath, p)
	}
}

func TestAcquireChatThread_ReusesMappedThreadSession(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	binding := ProjectBinding{ProjectPath: projectPath, RepoName: "repo", ChannelID: "channel-parent"}
	restore := installDiscordChannelAPIShim(t, map[string]*discordgo.Channel{
		"thread-3": {
		ID:       "thread-3",
		GuildID:  "guild-test",
		ParentID: "channel-parent",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)
	if err := db.DiscordBindSessionThread("sess-existing", projectPath, "thread-3", "channel-parent"); err != nil {
		t.Fatalf("bind thread: %v", err)
	}

	threadID, sessionID, err := svc.acquireChatThread(msg("thread-3", "hello"), binding, "hello")
	if err != nil {
		t.Fatalf("acquireChatThread: %v", err)
	}
	if threadID != "thread-3" || sessionID != "sess-existing" {
		t.Fatalf("expected existing mapping, got thread=%q session=%q", threadID, sessionID)
	}
}

func TestAcquireChatThread_ThreadWithoutMappingCreatesSession(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	binding := ProjectBinding{ProjectPath: projectPath, RepoName: "repo", ChannelID: "channel-parent"}
	restore := installDiscordChannelAPIShim(t, map[string]*discordgo.Channel{
		"thread-4": {
		ID:       "thread-4",
		GuildID:  "guild-test",
		ParentID: "channel-parent",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	threadID, sessionID, err := svc.acquireChatThread(msg("thread-4", "start"), binding, "start")
	if err != nil {
		t.Fatalf("acquireChatThread: %v", err)
	}
	if threadID != "thread-4" {
		t.Fatalf("expected existing thread id, got %q", threadID)
	}
	if sessionID == "" {
		t.Fatal("expected newly created session id")
	}

	mapping, err := db.DiscordGetSessionByThread("thread-4")
	if err != nil {
		t.Fatalf("DiscordGetSessionByThread: %v", err)
	}
	if mapping == nil || mapping.SessionID != sessionID {
		t.Fatalf("expected mapping to new session, got %+v", mapping)
	}
}

func TestRecordOutbound_UsesResolvedThreadSession(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	restore := installDiscordChannelAPIShim(t, map[string]*discordgo.Channel{
		"thread-5": {
		ID:       "thread-5",
		GuildID:  "guild-test",
		ParentID: "channel-parent",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)
	if err := db.DiscordBindSessionThread("sess-outbound", projectPath, "thread-5", "channel-parent"); err != nil {
		t.Fatalf("bind thread: %v", err)
	}

	svc.recordOutbound("thread-5", "agent reply", "agent", "")
	rows, err := db.DiscordListRecentMessages(projectPath, "thread-5", "", 5)
	if err != nil {
		t.Fatalf("DiscordListRecentMessages: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected outbound message to be archived")
	}
	if rows[0].SessionID != "sess-outbound" {
		t.Fatalf("expected resolved session id, got %+v", rows[0])
	}
}

func TestParseCommand_BasicCases(t *testing.T) {
	if _, _, ok := parseCommand("", "!"); ok {
		t.Fatal("expected empty command to fail")
	}
	if _, _, ok := parseCommand("hello", "!"); ok {
		t.Fatal("expected missing prefix to fail")
	}
	cmd, args, ok := parseCommand("!status project1", "!")
	if !ok {
		t.Fatal("expected valid command")
	}
	if cmd != "status" {
		t.Fatalf("cmd = %q, want status", cmd)
	}
	if len(args) != 1 || args[0] != "project1" {
		t.Fatalf("args = %#v, want [project1]", args)
	}
}
