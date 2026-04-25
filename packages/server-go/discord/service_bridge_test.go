package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/db"
)

func installDiscordGuildAPIShim(t *testing.T, guildID string, channels map[string]*discordgo.Channel) (func(), *[]string) {
	t.Helper()
	sent := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if r.Method == http.MethodGet && strings.HasPrefix(path, "/channels/") {
			id := strings.TrimPrefix(path, "/channels/")
			if ch, ok := channels[id]; ok {
				_ = json.NewEncoder(w).Encode(ch)
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		guildChannelsPath := fmt.Sprintf("/guilds/%s/channels", guildID)
		if path == guildChannelsPath {
			switch r.Method {
			case http.MethodGet:
				list := make([]*discordgo.Channel, 0, len(channels))
				for _, ch := range channels {
					list = append(list, ch)
				}
				_ = json.NewEncoder(w).Encode(list)
				return
			case http.MethodPost:
				var req struct {
					Name string `json:"name"`
					Type int    `json:"type"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				id := "created-" + slug(req.Name)
				ch := &discordgo.Channel{ID: id, Name: req.Name, Type: discordgo.ChannelType(req.Type)}
				channels[id] = ch
				_ = json.NewEncoder(w).Encode(ch)
				return
			}
		}

		if r.Method == http.MethodPost && strings.HasPrefix(path, "/channels/") && strings.HasSuffix(path, "/messages") {
			var req struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			sent = append(sent, req.Content)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1", "content": req.Content})
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))

	oldEndpointAPI := discordgo.EndpointAPI
	oldEndpointDiscord := discordgo.EndpointDiscord
	oldEndpointGuilds := discordgo.EndpointGuilds
	oldEndpointChannels := discordgo.EndpointChannels
	oldEndpointGuild := discordgo.EndpointGuild
	oldEndpointGuildChannels := discordgo.EndpointGuildChannels
	oldEndpointChannel := discordgo.EndpointChannel
	oldEndpointChannelMessages := discordgo.EndpointChannelMessages

	discordgo.EndpointDiscord = server.URL + "/"
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointGuilds = discordgo.EndpointAPI + "guilds/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointGuild = func(gID string) string {
		return discordgo.EndpointGuilds + gID
	}
	discordgo.EndpointGuildChannels = func(gID string) string {
		return discordgo.EndpointGuild(gID) + "/channels"
	}
	discordgo.EndpointChannel = func(channelID string) string {
		return discordgo.EndpointChannels + channelID
	}
	discordgo.EndpointChannelMessages = func(channelID string) string {
		return discordgo.EndpointChannels + channelID + "/messages"
	}

	cleanup := func() {
		discordgo.EndpointDiscord = oldEndpointDiscord
		discordgo.EndpointAPI = oldEndpointAPI
		discordgo.EndpointGuilds = oldEndpointGuilds
		discordgo.EndpointChannels = oldEndpointChannels
		discordgo.EndpointGuild = oldEndpointGuild
		discordgo.EndpointGuildChannels = oldEndpointGuildChannels
		discordgo.EndpointChannel = oldEndpointChannel
		discordgo.EndpointChannelMessages = oldEndpointChannelMessages
		server.Close()
	}

	return cleanup, &sent
}

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

func TestAddProject_ValidationErrors(t *testing.T) {
	svc, _ := newDisabledSvc(t)

	if err := svc.addProject("reply", "   "); err == nil {
		t.Fatal("expected error for empty path")
	}

	if err := svc.addProject("reply", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestEnsureProjectChannel_FindOrCreate(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.cfg.GuildID = "guild-1"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{
		"ch-existing": {ID: "ch-existing", Name: "proj-repo", Type: discordgo.ChannelTypeGuildText},
	}
	cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	ch, err := svc.ensureProjectChannel("proj-repo")
	if err != nil {
		t.Fatalf("ensureProjectChannel existing: %v", err)
	}
	if ch.ID != "ch-existing" {
		t.Fatalf("existing channel id = %q, want ch-existing", ch.ID)
	}

	created, err := svc.ensureProjectChannel("proj-new")
	if err != nil {
		t.Fatalf("ensureProjectChannel create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected created channel id")
	}
}

func TestEnsureControlChannel_CreateAndSend(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-1"
	svc.cfg.ControlChannelName = "engine-control"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{}
	cleanup, sent := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	id, err := svc.ensureControlChannel()
	if err != nil {
		t.Fatalf("ensureControlChannel: %v", err)
	}
	if strings.TrimSpace(id) == "" {
		t.Fatal("expected control channel id")
	}
	if len(*sent) == 0 {
		t.Fatal("expected welcome message to be sent")
	}
}

func TestEnsureControlChannel_ReusesExisting(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.cfg.GuildID = "guild-1"
	svc.cfg.ControlChannelName = "engine-control"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{
		"ctrl-1": {ID: "ctrl-1", Name: "engine-control", Type: discordgo.ChannelTypeGuildText},
	}
	cleanup, sent := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	svc.stateMu.Lock()
	svc.state.ControlChannelID = "ctrl-1"
	svc.stateMu.Unlock()

	id, err := svc.ensureControlChannel()
	if err != nil {
		t.Fatalf("ensureControlChannel reuse: %v", err)
	}
	if id != "ctrl-1" {
		t.Fatalf("control channel id = %q, want ctrl-1", id)
	}
	if len(*sent) != 0 {
		t.Fatalf("expected no welcome send on reuse, got %d sends", len(*sent))
	}
}

func TestAddProject_Success(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-1"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{}
	cleanup, sent := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	projectDir := t.TempDir()
	err := svc.addProject("reply-channel", projectDir)
	if err != nil {
		t.Fatalf("addProject: %v", err)
	}

	svc.stateMu.RLock()
	binding, ok := svc.state.Projects[projectDir]
	svc.stateMu.RUnlock()
	if !ok {
		t.Fatal("expected project binding to be saved")
	}
	if strings.TrimSpace(binding.ChannelID) == "" {
		t.Fatal("expected bound project channel id")
	}
	if len(*sent) < 2 {
		t.Fatalf("expected at least two outbound messages, got %d", len(*sent))
	}
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

// ── Start ─────────────────────────────────────────────────────────────────────

func TestStart_Disabled(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start() on disabled service should return nil, got: %v", err)
	}
}

// ── send / sendTagged empty-msg path (requires non-nil dg) ────────────────────

func TestSend_EmptyMsg_WithNonNilDG(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.dg = makeDiscordRESTSession(t)
	svc.send("ch-id", "")
	svc.send("ch-id", "   ")
}

func TestSendTagged_EmptyMsg_WithNonNilDG(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.dg = makeDiscordRESTSession(t)
	svc.sendTagged("ch-id", "", "agent", "s1")
	svc.sendTagged("ch-id", "   ", "agent", "s1")
}

// ── send / sendTagged success path via shim ───────────────────────────────────

func TestSend_SuccessPath_WithShim(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	cleanup, sent := installDiscordGuildAPIShim(t, "guild-send", map[string]*discordgo.Channel{})
	defer cleanup()
	svc.dg = makeDiscordRESTSession(t)

	svc.send("channel-123", "hello world")

	if len(*sent) == 0 {
		t.Fatal("expected message to be sent via shim")
	}
	if (*sent)[0] != "hello world" {
		t.Fatalf("expected 'hello world', got %q", (*sent)[0])
	}
}

func TestSendTagged_SuccessPath_WithShim(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	cleanup, sent := installDiscordGuildAPIShim(t, "guild-tagged", map[string]*discordgo.Channel{})
	defer cleanup()
	svc.dg = makeDiscordRESTSession(t)

	svc.sendTagged("channel-xyz", "tagged message", "agent", "session-1")

	if len(*sent) == 0 {
		t.Fatal("expected tagged message to be sent via shim")
	}
	if (*sent)[0] != "tagged message" {
		t.Fatalf("expected 'tagged message', got %q", (*sent)[0])
	}
	_ = projectPath
}

// ── send error path via error-returning shim ──────────────────────────────────

func installErrorMessageShim(t *testing.T) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	oldAPI := discordgo.EndpointAPI
	oldDiscord := discordgo.EndpointDiscord
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel
	oldChannelMessages := discordgo.EndpointChannelMessages
	discordgo.EndpointDiscord = srv.URL + "/"
	discordgo.EndpointAPI = srv.URL + "/"
	discordgo.EndpointChannels = srv.URL + "/channels/"
	discordgo.EndpointChannel = func(id string) string { return srv.URL + "/channels/" + id }
	discordgo.EndpointChannelMessages = func(id string) string { return srv.URL + "/channels/" + id + "/messages" }
	return func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointDiscord = oldDiscord
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
		discordgo.EndpointChannelMessages = oldChannelMessages
		srv.Close()
	}
}

func TestSend_SendError(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	cleanup := installErrorMessageShim(t)
	defer cleanup()
	svc.dg = makeDiscordRESTSession(t)
	svc.send("ch-err", "this should fail")
}

func TestSendTagged_SendError(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	cleanup := installErrorMessageShim(t)
	defer cleanup()
	svc.dg = makeDiscordRESTSession(t)
	svc.sendTagged("ch-err", "tagged fail", "agent", "sess")
}

// ── onReady via shim ──────────────────────────────────────────────────────────

func TestOnReady_WithShim(t *testing.T) {
	guildID := "guild-onready"
	channels := map[string]*discordgo.Channel{}
	cleanup, _ := installDiscordGuildAPIShim(t, guildID, channels)
	defer cleanup()

	dir := initDiscordTestDB(t)
	svc, err := NewService(Config{
		Enabled:            true,
		GuildID:            guildID,
		ControlChannelName: "engine-control",
		CommandPrefix:      "!",
		StoragePath:        dir,
	}, dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.dg = makeDiscordRESTSession(t)

	svc.onReady(nil, nil)

	svc.stateMu.RLock()
	chanID := svc.state.ControlChannelID
	svc.stateMu.RUnlock()
	if chanID == "" {
		t.Fatal("expected control channel to be created by onReady")
	}
}

// ── ensureControlChannel error path ──────────────────────────────────────────

func TestEnsureControlChannel_GuildChannelsError(t *testing.T) {
	shimGuildID := "guild-shim"
	cleanup, _ := installDiscordGuildAPIShim(t, shimGuildID, map[string]*discordgo.Channel{})
	defer cleanup()

	dir := initDiscordTestDB(t)
	svc, err := NewService(Config{
		Enabled:            true,
		GuildID:            "different-guild",
		ControlChannelName: "engine-control",
		CommandPrefix:      "!",
		StoragePath:        dir,
	}, dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.dg = makeDiscordRESTSession(t)

	_, cerr := svc.ensureControlChannel()
	if cerr == nil {
		t.Fatal("expected error when GuildChannels returns 404")
	}
}

// ── handleSearchCommand results path ─────────────────────────────────────────

func TestHandleSearchCommand_WithResults(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "ch-search", "repo")

	_ = db.DiscordRecordMessage(db.DiscordMessage{
		ID:          "dm-results-1",
		ProjectPath: projectPath,
		ChannelID:   "ch-search",
		AuthorName:  "testuser",
		Direction:   "in",
		Kind:        "message",
		Content:     "hello unique-xyz-term",
	})

	svc.handleSearchCommand(msg("ch-search", ""), []string{"unique-xyz-term"})
}

// ── Reload with enabled config + loadState error ──────────────────────────────

func TestServiceReload_Enabled_LoadStateError(t *testing.T) {
	svc, dir := newDisabledSvc(t)

	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(dir, defaultStateFileName)
	if err := os.WriteFile(corrupt, []byte("{invalid json}"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Enabled:       true,
		BotToken:      "fake-token",
		GuildID:       "guild-1",
		AllowedUsers:  map[string]bool{"user1": true},
		StoragePath:   dir,
		CommandPrefix: "!",
	}
	if err := svc.Reload(cfg); err == nil {
		t.Fatal("expected error from corrupt state file in Reload")
	}
}

// ── handleSessionsCommand with sessions present ───────────────────────────────

func TestHandleSessionsCommand_WithSessions(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "ch-with-sess", "repo")

	if err := db.CreateSession("sess-1", projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	svc.handleSessionsCommand(msg("ch-with-sess", ""), nil)
}

func TestHandleSessionsCommand_ManySessions(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "ch-many-sess", "repo")

	for i := 0; i < 7; i++ {
		if err := db.CreateSession(fmt.Sprintf("sess-%d", i), projectPath, "main"); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	svc.handleSessionsCommand(msg("ch-many-sess", ""), nil)
}

// ── handleHistoryCommand with messages present ────────────────────────────────

func TestHandleHistoryCommand_WithMessages(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "ch-hist", "repo")

	_ = db.DiscordRecordMessage(db.DiscordMessage{
		ID:          "hist-msg-1",
		ProjectPath: projectPath,
		ChannelID:   "ch-hist",
		AuthorName:  "user1",
		Direction:   "in",
		Kind:        "message",
		Content:     "some chat message for history test",
	})

	svc.handleHistoryCommand(msg("ch-hist", ""), nil)
}

// ── splitForDiscord long text ─────────────────────────────────────────────────

func TestSplitForDiscord_LongText(t *testing.T) {
	long := strings.Repeat("a", 3000)
	parts := splitForDiscord(long, maxDiscordMessageChars)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts for long text, got %d", len(parts))
	}
	for _, p := range parts {
		if len(p) > maxDiscordMessageChars {
			t.Errorf("part exceeds maxDiscordMessageChars: len=%d", len(p))
		}
	}
}

// ── WriteConfig covered path ──────────────────────────────────────────────────

func TestWriteConfig_Success(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Enabled:       false,
		CommandPrefix: "!",
	}
	if err := WriteConfig(dir, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
}

func TestWriteConfig_MkdirError(t *testing.T) {
	notADir := t.TempDir() + "/file.txt"
	_ = os.WriteFile(notADir, []byte("x"), 0600)
	err := WriteConfig(filepath.Join(notADir, "subdir"), Config{})
	if err == nil {
		t.Fatal("expected error when path is under a file")
	}
}

// ── Close with non-nil dg ─────────────────────────────────────────────────────

func TestClose_WithNonNilDG(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.dg = makeDiscordRESTSession(t)
	// Close on a never-opened session returns an error or nil — both are OK.
	// The key is no panic.
	_ = svc.Close()
}

// ── Start enabled path (dg.Open() fails) ─────────────────────────────────────

func TestStart_Enabled_OpenFails(t *testing.T) {
	// Point gateway endpoint to a server that returns an error so dg.Open() fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401: Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	oldGWB := discordgo.EndpointGatewayBot
	defer func() { discordgo.EndpointGatewayBot = oldGWB }()
	discordgo.EndpointGatewayBot = srv.URL + "/gateway/bot"

	dir := initDiscordTestDB(t)
	svc, err := NewService(Config{
		Enabled:            true,
		GuildID:            "guild-start-err",
		BotToken:           "fake-token",
		ControlChannelName: "engine-control",
		CommandPrefix:      "!",
		StoragePath:        dir,
	}, dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if err := svc.Start(); err == nil {
		t.Error("expected error from Start when gateway unreachable")
		_ = svc.Close()
	}
}

// ── onReady error path ────────────────────────────────────────────────────────

func TestOnReady_EnsureChannelError(t *testing.T) {
	// No endpoint shim — dg.GuildChannels will fail with HTTP error, covering
	// the log.Printf error branch in onReady.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	oldAPI := discordgo.EndpointAPI
	oldDiscord := discordgo.EndpointDiscord
	oldGuilds := discordgo.EndpointGuilds
	oldGuild := discordgo.EndpointGuild
	oldGuildChannels := discordgo.EndpointGuildChannels
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel
	defer func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointDiscord = oldDiscord
		discordgo.EndpointGuilds = oldGuilds
		discordgo.EndpointGuild = oldGuild
		discordgo.EndpointGuildChannels = oldGuildChannels
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
	}()
	discordgo.EndpointDiscord = srv.URL + "/"
	discordgo.EndpointAPI = srv.URL + "/"
	discordgo.EndpointGuilds = srv.URL + "/guilds/"
	discordgo.EndpointGuild = func(gID string) string { return srv.URL + "/guilds/" + gID }
	discordgo.EndpointGuildChannels = func(gID string) string { return srv.URL + "/guilds/" + gID + "/channels" }
	discordgo.EndpointChannels = srv.URL + "/channels/"
	discordgo.EndpointChannel = func(id string) string { return srv.URL + "/channels/" + id }

	dir := initDiscordTestDB(t)
	svc, err := NewService(Config{
		Enabled:            true,
		GuildID:            "guild-err-ready",
		ControlChannelName: "engine-control",
		CommandPrefix:      "!",
		StoragePath:        dir,
	}, dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.dg = makeDiscordRESTSession(t)
	svc.onReady(nil, nil)
}
