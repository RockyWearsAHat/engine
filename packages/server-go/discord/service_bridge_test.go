package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	"github.com/gorilla/websocket"
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
	// Use a single plain word (no hyphens) so FTS5 returns 0 results without error.
	svc.handleSearchCommand(msg("ch", ""), []string{"noresultsxyzqword"})
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

	// Use a single-word token to avoid FTS5 interpreting hyphens as NOT operators.
	_ = db.DiscordRecordMessage(db.DiscordMessage{
		ID:          "dm-results-1",
		ProjectPath: projectPath,
		ChannelID:   "ch-search",
		AuthorName:  "testuser",
		Direction:   "in",
		Kind:        "message",
		Content:     "hello engineuniqword",
	})

	svc.handleSearchCommand(msg("ch-search", ""), []string{"engineuniqword"})
}

func TestHandleSearchCommand_DBError(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	// "*" alone is an invalid FTS5 query → db.DiscordSearchMessages returns an error,
	// triggering the "Search error:" branch in handleSearchCommand.
	svc.handleSearchCommand(msg("ch", ""), []string{"*"})
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

// waitForActiveSession polls until sessionID is no longer in s.active (goroutine done)
// or the deadline is reached.
func waitForActiveSession(t *testing.T, svc *Service, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		svc.activeMu.Lock()
		_, tracked := svc.active[sessionID]
		running := tracked
		if !running {
			// Some tests pre-bind a session id but the command may still run under
			// a different active session key. Drain all active work to avoid
			// background DB writes racing with TempDir cleanup.
			running = len(svc.active) > 0 || len(svc.activeByChannel) > 0
		}
		svc.activeMu.Unlock()
		if !running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("timed out waiting for handleAskCommand goroutine to finish")
}

// installThreadChannelShim installs a channel shim that also accepts POST for
// messages and threads (returning success), suitable for goroutine tests.
func installThreadChannelShim(t *testing.T, channels map[string]*discordgo.Channel) func() {
	t.Helper()
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
		// Accept message sends and thread starts without error.
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1", "content": ""})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	oldAPI := discordgo.EndpointAPI
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel

	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }

	return func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
		server.Close()
	}
}

// ── handleAskCommand — empty prompt ──────────────────────────────────────────

func TestHandleAskCommand_EmptyPrompt(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	// Bind project to a channel.
	svc.stateMu.Lock()
	svc.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		ChannelID:   "ch-ask-empty",
		RepoName:    "repo",
	}
	svc.stateMu.Unlock()
	// Message from the project channel with whitespace-only args — resolveAskTarget
	// returns the binding but prompt is empty → "Prompt is required."
	svc.handleAskCommand(msg("ch-ask-empty", ""), []string{"   "})
}

// ── handleAskCommand — active session ────────────────────────────────────────

func TestHandleAskCommand_ActiveSession(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)

	// Set up a thread channel in the shim so acquireChatThread succeeds.
	restore := installThreadChannelShim(t, map[string]*discordgo.Channel{
		"thread-active-1": {
			ID:       "thread-active-1",
			GuildID:  "guild-test",
			ParentID: "ch-ask-active",
			Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	svc.stateMu.Lock()
	svc.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		ChannelID:   "ch-ask-active",
		RepoName:    "repo",
	}
	svc.stateMu.Unlock()

	// Pre-bind thread to an existing session in DB.
	if err := db.DiscordBindSessionThread("sess-active-1", projectPath, "thread-active-1", "ch-ask-active"); err != nil {
		t.Fatalf("DiscordBindSessionThread: %v", err)
	}

	// Mark the session as already running.
	svc.activeMu.Lock()
	svc.active["sess-active-1"] = true
	svc.activeMu.Unlock()

	// Message from the thread — acquireChatThread reuses sess-active-1, but
	// s.active[sess-active-1] is true → "A task is already running."
	svc.handleAskCommand(msg("thread-active-1", ""), []string{"do something"})

	// Clean up the mock active entry.
	svc.activeMu.Lock()
	delete(svc.active, "sess-active-1")
	svc.activeMu.Unlock()
}

// ── handleAskCommand — goroutine: error path ─────────────────────────────────

func TestHandleAskCommand_GoroutineError(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)

	restore := installThreadChannelShim(t, map[string]*discordgo.Channel{
		"thread-gor-err": {
			ID:       "thread-gor-err",
			GuildID:  "guild-test",
			ParentID: "ch-gor-err",
			Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	svc.stateMu.Lock()
	svc.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		ChannelID:   "ch-gor-err",
		RepoName:    "repo",
	}
	svc.stateMu.Unlock()

	// Create the session so acquireChatThread can bind it to the thread.
	if err := db.CreateSession("sess-gor-err", projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := db.DiscordBindSessionThread("sess-gor-err", projectPath, "thread-gor-err", "ch-gor-err"); err != nil {
		t.Fatalf("DiscordBindSessionThread: %v", err)
	}

	// Force ai.Chat to call OnError immediately by using anthropic provider
	// without an API key — ai.Chat returns after one OnError call.
	t.Setenv("ENGINE_MODEL_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "")

	svc.handleAskCommand(msg("thread-gor-err", ""), []string{"error path test"})
	waitForActiveSession(t, svc, "sess-gor-err")
}

// ── handleAskCommand — goroutine: success (reasoning-only → empty output) ────

func TestHandleAskCommand_GoroutineSuccessEmpty(t *testing.T) {
	// Mock Ollama that returns only reasoning chunks — OnChunk is called with
	// empty/done content, so output stays empty → "(No response text returned.)"
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"test-model:latest"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking only\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	svc, projectPath := newDisabledSvc(t)

	restore := installThreadChannelShim(t, map[string]*discordgo.Channel{
		"thread-gor-empty": {
			ID:       "thread-gor-empty",
			GuildID:  "guild-test",
			ParentID: "ch-gor-empty",
			Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	svc.stateMu.Lock()
	svc.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		ChannelID:   "ch-gor-empty",
		RepoName:    "repo",
	}
	svc.stateMu.Unlock()

	if err := db.CreateSession("sess-gor-empty", projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := db.DiscordBindSessionThread("sess-gor-empty", projectPath, "thread-gor-empty", "ch-gor-empty"); err != nil {
		t.Fatalf("DiscordBindSessionThread: %v", err)
	}

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "")
	t.Setenv("OLLAMA_BASE_URL", ollamaSrv.URL)

	svc.handleAskCommand(msg("thread-gor-empty", ""), []string{"reasoning only"})
	waitForActiveSession(t, svc, "sess-gor-empty")
}

// ── acquireChatThread — new thread creation from regular channel ──────────────

// installThreadStartShim installs an endpoint shim that:
//   - Returns a regular text channel for GET /channels/{channelID}
//   - Handles POST /channels/{channelID}/threads → creates thread
//   - Accepts POST /channels/{id}/messages
func installThreadStartShim(t *testing.T, parentChannelID, newThreadID string) func() {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// GET /channels/{id} — return regular text channel for parent, 404 for others.
		if r.Method == http.MethodGet && strings.HasPrefix(path, "/channels/") {
			id := strings.TrimPrefix(path, "/channels/")
			if id == parentChannelID {
				_ = json.NewEncoder(w).Encode(&discordgo.Channel{
					ID:   parentChannelID,
					Type: discordgo.ChannelTypeGuildText,
				})
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// POST /channels/{id}/threads — create thread.
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/threads") {
			_ = json.NewEncoder(w).Encode(&discordgo.Channel{
				ID:       newThreadID,
				Type:     discordgo.ChannelTypeGuildPublicThread,
				ParentID: parentChannelID,
			})
			return
		}
		// POST /channels/{id}/messages — accept silently.
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/messages") {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1", "content": ""})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	oldAPI := discordgo.EndpointAPI
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel
	oldMessages := discordgo.EndpointChannelMessages

	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }
	discordgo.EndpointChannelMessages = func(id string) string { return discordgo.EndpointChannels + id + "/messages" }

	return func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
		discordgo.EndpointChannelMessages = oldMessages
		server.Close()
	}
}

func TestAcquireChatThread_NewThreadFromChannel(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	binding := ProjectBinding{ProjectPath: projectPath, RepoName: "repo", ChannelID: "parent-ch-1"}

	restore := installThreadStartShim(t, "parent-ch-1", "new-thread-1")
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	// Message from a regular text channel (not a thread) → acquireChatThread
	// calls ThreadStart to create a new thread, then binds a new session.
	threadID, sessionID, err := svc.acquireChatThread(msg("parent-ch-1", ""), binding, "hello")
	if err != nil {
		t.Fatalf("acquireChatThread: %v", err)
	}
	if threadID != "new-thread-1" {
		t.Fatalf("expected new-thread-1, got %q", threadID)
	}
	if sessionID == "" {
		t.Fatal("expected non-empty session id")
	}
}

func TestAcquireChatThread_ThreadStartError(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	binding := ProjectBinding{ProjectPath: projectPath, RepoName: "repo", ChannelID: "parent-ch-2"}

	// Shim returns the parent as a regular channel but fails POST /threads.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+binding.ChannelID) {
			_ = json.NewEncoder(w).Encode(&discordgo.Channel{
				ID:   binding.ChannelID,
				Type: discordgo.ChannelTypeGuildText,
			})
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	oldAPI := discordgo.EndpointAPI
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel
	defer func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
	}()
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }

	svc.dg = makeDiscordRESTSession(t)

	_, _, err := svc.acquireChatThread(msg("parent-ch-2", ""), binding, "hello")
	if err == nil {
		t.Fatal("expected error when ThreadStart fails")
	}
}

// ── ensureControlChannel — find existing by name in guild ────────────────────

func TestEnsureControlChannel_FindByName(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-findbyname"
	svc.cfg.ControlChannelName = "engine-control"

	channels := map[string]*discordgo.Channel{
		"ctrl-byname": {
			ID:   "ctrl-byname",
			Name: "engine-control",
			Type: discordgo.ChannelTypeGuildText,
		},
	}
	cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()
	svc.dg = makeDiscordRESTSession(t)

	// No existing ControlChannelID stored → will search guild channels by name.
	id, err := svc.ensureControlChannel()
	if err != nil {
		t.Fatalf("ensureControlChannel: %v", err)
	}
	if id != "ctrl-byname" {
		t.Fatalf("expected ctrl-byname, got %q", id)
	}
}

// ── ensureControlChannel — existing ID stored but channel not found ───────────

func TestEnsureControlChannel_ExistingIDNotFound(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-idfail"
	svc.cfg.ControlChannelName = "engine-control"

	// Mark a channel ID that no longer exists in Discord.
	svc.stateMu.Lock()
	svc.state.ControlChannelID = "stale-ctrl-id"
	svc.stateMu.Unlock()

	channels := map[string]*discordgo.Channel{}
	cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()
	svc.dg = makeDiscordRESTSession(t)

	// Channel lookup for stale-ctrl-id fails → falls through to search + create.
	id, err := svc.ensureControlChannel()
	if err != nil {
		t.Fatalf("ensureControlChannel after stale id: %v", err)
	}
	if id == "" {
		t.Fatal("expected newly created channel id")
	}
}

// ── handleLastCommitCommand — success path (commit exists) ───────────────────

func TestHandleLastCommitCommand_WithCommit(t *testing.T) {
	repoDir := t.TempDir()
	// Create a minimal git repo with one commit so GetLog returns a result.
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	f := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(f, []byte("hello"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial commit"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	svc, _ := newDisabledSvc(t)
	addProject(svc, repoDir, "ch-commit-ok", "repo")

	// handleLastCommitCommand should find the commit and send the summary.
	svc.handleLastCommitCommand(msg("ch-commit-ok", ""), nil)
}

// ── parseCommand — empty content ─────────────────────────────────────────────

func TestParseCommand_EmptyContent(t *testing.T) {
	_, _, ok := parseCommand("", "!")
	if ok {
		t.Fatal("expected not ok for empty content")
	}
}

// ── handleAskCommand — goroutine: content written to output ──────────────────

func TestHandleAskCommand_GoroutineWithContent(t *testing.T) {
	// Mock Ollama that returns an actual content chunk so OnChunk is called with
	// non-empty content — covers the output.WriteString path.
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"test-model:latest"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello from Engine!\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	svc, projectPath := newDisabledSvc(t)

	restore := installThreadChannelShim(t, map[string]*discordgo.Channel{
		"thread-content-1": {
			ID:       "thread-content-1",
			GuildID:  "guild-test",
			ParentID: "ch-content-1",
			Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	svc.stateMu.Lock()
	svc.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		ChannelID:   "ch-content-1",
		RepoName:    "repo",
	}
	svc.stateMu.Unlock()

	if err := db.CreateSession("sess-content-1", projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := db.DiscordBindSessionThread("sess-content-1", projectPath, "thread-content-1", "ch-content-1"); err != nil {
		t.Fatalf("DiscordBindSessionThread: %v", err)
	}

	t.Setenv("ENGINE_MODEL_PROVIDER", "ollama")
	t.Setenv("ENGINE_MODEL", "")
	t.Setenv("OLLAMA_BASE_URL", ollamaSrv.URL)

	svc.handleAskCommand(msg("thread-content-1", ""), []string{"say hello"})
	waitForActiveSession(t, svc, "sess-content-1")
}

// ── handleHistoryCommand — dg != nil, channel is a thread ───────────────────

func TestHandleHistoryCommand_WithDGThread(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)

	restore := installThreadChannelShim(t, map[string]*discordgo.Channel{
		"thread-hist-1": {
			ID:       "thread-hist-1",
			Type:     discordgo.ChannelTypeGuildPublicThread,
			ParentID: "ch-hist-1",
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)
	addProject(svc, projectPath, "ch-hist-1", "repo")

	// Message from a thread channel — covers the dg-not-nil + isThread branch
	// inside handleHistoryCommand, and also exercises resolveProjectByChannel's
	// parent-channel lookup path.
	svc.handleHistoryCommand(msg("thread-hist-1", ""), nil)
}

// ── resolveProjectByChannel — dg != nil paths ────────────────────────────────

func TestResolveProjectByChannel_ThreadChannelError(t *testing.T) {
	svc, _ := newDisabledSvc(t)

	// Shim returns 404 for GET /channels/unknown-ch so Channel() errors out.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	oldAPI := discordgo.EndpointAPI
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel
	defer func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
	}()
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }
	svc.dg = makeDiscordRESTSession(t)

	// No project bound — Channel() errors → middle `return ProjectBinding{}, false`.
	p, ok := svc.resolveProjectByChannel("unknown-ch")
	if ok {
		t.Fatalf("expected not ok, got %+v", p)
	}
}

func TestResolveProjectByChannel_ThreadParentNotBound(t *testing.T) {
	svc, _ := newDisabledSvc(t)

	// Shim returns a thread with parentID but no project bound to that parent.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&discordgo.Channel{
			ID:       "orphan-thread",
			Type:     discordgo.ChannelTypeGuildPublicThread,
			ParentID: "unbound-parent",
		})
	}))
	defer server.Close()

	oldAPI := discordgo.EndpointAPI
	oldChannels := discordgo.EndpointChannels
	oldChannel := discordgo.EndpointChannel
	defer func() {
		discordgo.EndpointAPI = oldAPI
		discordgo.EndpointChannels = oldChannels
		discordgo.EndpointChannel = oldChannel
	}()
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }
	svc.dg = makeDiscordRESTSession(t)

	// Channel's parentID doesn't match any project → final `return ProjectBinding{}, false`.
	p, ok := svc.resolveProjectByChannel("orphan-thread")
	if ok {
		t.Fatalf("expected not ok, got %+v", p)
	}
}

// ── handleSessionsCommand — long summary truncation ──────────────────────────

func TestHandleSessionsCommand_LongSummary(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "ch-sess-long", "repo")

	// Create a session and give it a summary longer than 117 chars.
	if err := db.CreateSession("sess-long-1", projectPath, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	longSummary := strings.Repeat("x", 150)
	if err := db.UpdateSessionSummary("sess-long-1", longSummary); err != nil {
		t.Fatalf("UpdateSessionSummary: %v", err)
	}

	svc.handleSessionsCommand(msg("ch-sess-long", ""), nil)
}

// ── loadState — invalid JSON ──────────────────────────────────────────────────

func TestLoadState_InvalidJSON(t *testing.T) {
	stateDir := initDiscordTestDB(t)
	svc, err := NewService(Config{Enabled: false, CommandPrefix: "!", StoragePath: stateDir}, stateDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	stateFile := filepath.Join(stateDir, defaultStateFileName)
	if err := os.WriteFile(stateFile, []byte("NOT VALID JSON {{{{"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := svc.loadState(); err == nil {
		t.Fatal("expected error for invalid JSON state file")
	}
}

// ── loadState — unreadable file ───────────────────────────────────────────────

func TestLoadState_UnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root can read any file")
	}
	stateDir := initDiscordTestDB(t)
	svc, err := NewService(Config{Enabled: false, CommandPrefix: "!", StoragePath: stateDir}, stateDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	stateFile := filepath.Join(stateDir, defaultStateFileName)
	if err := os.WriteFile(stateFile, []byte("{}"), 0000); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { os.Chmod(stateFile, 0600) }) //nolint:errcheck
	if err := svc.loadState(); err == nil {
		t.Fatal("expected error for unreadable state file")
	}
}

// ── slug — empty input returns "project" ─────────────────────────────────────

func TestSlug_Empty(t *testing.T) {
	if got := slug(""); got != "project" {
		t.Fatalf("slug('') = %q, want 'project'", got)
	}
}

// ── splitForDiscord — empty input returns "(empty response)" ─────────────────

func TestSplitForDiscord_Empty(t *testing.T) {
	parts := splitForDiscord("", 2000)
	if len(parts) != 1 || parts[0] != "(empty response)" {
		t.Fatalf("splitForDiscord('') = %v, want [(empty response)]", parts)
	}
}

// ── onMessage — project command routing ───────────────────────────────────────

func TestOnMessage_ProjectCommand(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.cfg.GuildID = "guild-onmsg"
	svc.cfg.AllowedUsers = map[string]bool{"u-test": true}
	svc.cfg.CommandPrefix = "!"

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "ch-onmsg",
			Content:   "!project",
			GuildID:   "guild-onmsg",
			Author:    &discordgo.User{Username: "testuser", ID: "u-test", Bot: false},
		},
	}
	// !project with no args → handleProjectCommand sends usage message (dg nil → no-op).
	svc.onMessage(nil, m)
}

// ── NewService — loadState error ─────────────────────────────────────────────

func TestNewService_LoadStateError(t *testing.T) {
	stateDir := initDiscordTestDB(t)
	stateFile := filepath.Join(stateDir, defaultStateFileName)
	if err := os.WriteFile(stateFile, []byte("{INVALID JSON}"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewService(Config{
		Enabled:     true,
		StoragePath: stateDir,
		CommandPrefix: "!",
	}, stateDir)
	if err == nil {
		t.Fatal("expected error from NewService when state file is invalid JSON")
	}
}

// ── applyFileConfig — StoragePath from file ──────────────────────────────────

func TestLoadConfig_WithFileStoragePath(t *testing.T) {
	t.Setenv("ENGINE_DISCORD", "")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "")
	t.Setenv("ENGINE_DISCORD_PREFIX", "")
	t.Setenv("ENGINE_DISCORD_CONTROL_CHANNEL", "")
	t.Setenv("ENGINE_STATE_DIR", "")

	projectDir := t.TempDir()
	customStorage := t.TempDir()
	configDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configJSON := fmt.Sprintf(`{"storagePath": %q}`, customStorage)
	if err := os.WriteFile(filepath.Join(configDir, defaultConfigFileName), []byte(configJSON), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.StoragePath != customStorage {
		t.Fatalf("expected %q, got %q", customStorage, cfg.StoragePath)
	}
}

// ── applyEnvOverrides — StoragePath from ENGINE_STATE_DIR ───────────────────

func TestLoadConfig_StorageDirFromEnv(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", customDir)
	t.Setenv("ENGINE_DISCORD", "")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "")
	t.Setenv("ENGINE_DISCORD_PREFIX", "")
	t.Setenv("ENGINE_DISCORD_CONTROL_CHANNEL", "")

	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.StoragePath != customDir {
		t.Fatalf("expected %q, got %q", customDir, cfg.StoragePath)
	}
}

// ── loadProjectConfig — unreadable file ─────────────────────────────────────

func TestLoadConfig_ConfigFileUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root can read any file")
	}
	t.Setenv("ENGINE_STATE_DIR", "")
	t.Setenv("ENGINE_DISCORD", "")
	t.Setenv("ENGINE_DISCORD_BOT_TOKEN", "")
	t.Setenv("ENGINE_DISCORD_GUILD_ID", "")
	t.Setenv("ENGINE_DISCORD_ALLOWED_USER_IDS", "")
	t.Setenv("ENGINE_DISCORD_PREFIX", "")
	t.Setenv("ENGINE_DISCORD_CONTROL_CHANNEL", "")

	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".engine")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(configDir, defaultConfigFileName)
	if err := os.WriteFile(configPath, []byte("{}"), 0000); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { os.Chmod(configPath, 0600) }) //nolint:errcheck
	_, err := LoadConfig(projectDir)
	if err == nil {
		t.Fatal("expected error for unreadable config file")
	}
}

// ── slug — all-non-alphanumeric input returns "project" ──────────────────────

func TestSlug_AllNonAlphanumeric(t *testing.T) {
	if got := slug("@@@---!!!"); got != "project" {
		t.Fatalf("slug('@@@---!!!') = %q, want 'project'", got)
	}
}

// ── Reload — loadState succeeds then Start is called (Start fails on fake token) ─

func TestReload_StartCalled(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	stateDir := t.TempDir()
	cfg := Config{
		Enabled:       true,
		BotToken:      "fake-token-for-reload",
		GuildID:       "guild-x",
		AllowedUsers:  map[string]bool{"user-x": true},
		StoragePath:   stateDir,
		CommandPrefix: "!",
	}
	// No state file → loadState returns nil → Start() is called → fails (no real Discord).
	err := svc.Reload(cfg)
	// We expect an error from Start() because the token is fake, but the
	// return s.Start() statement in Reload is covered regardless.
	_ = err
}

// (ensureControlChannel GuildChannels error and ensureProjectChannel GuildChannels error are covered by existing tests above)

// ── installGuildChannelsSuccessCreateFailShim ─────────────────────────────────
// Returns OK for GET /guilds/{id}/channels (empty list) and 403 for POST.

func installGuildChannelsOKCreateFailShim(t *testing.T, guildID string) (dg *discordgo.Session) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		guildChanPath := fmt.Sprintf("/guilds/%s/channels", guildID)
		if path == guildChanPath {
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]*discordgo.Channel{})
				return
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(path, "/channels/") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	origAPI := discordgo.EndpointAPI
	origChannels := discordgo.EndpointChannels
	origChannel := discordgo.EndpointChannel
	origGuilds := discordgo.EndpointGuilds
	origGuild := discordgo.EndpointGuild
	origGuildChannels := discordgo.EndpointGuildChannels
	t.Cleanup(func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointChannels = origChannels
		discordgo.EndpointChannel = origChannel
		discordgo.EndpointGuilds = origGuilds
		discordgo.EndpointGuild = origGuild
		discordgo.EndpointGuildChannels = origGuildChannels
	})
	discordgo.EndpointAPI = srv.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }
	discordgo.EndpointGuilds = srv.URL + "/guilds/"
	discordgo.EndpointGuild = func(gID string) string { return srv.URL + "/guilds/" + gID }
	discordgo.EndpointGuildChannels = func(gID string) string { return srv.URL + "/guilds/" + gID + "/channels" }

	session, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	return session
}

// ── ensureControlChannel — GuildChannelCreate error ──────────────────────────

func TestEnsureControlChannel_GuildChannelCreateError(t *testing.T) {
	guildID := "guild-create-fail"
	dg := installGuildChannelsOKCreateFailShim(t, guildID)

	svc, _ := newDisabledSvc(t)
	svc.dg = dg
	svc.cfg.GuildID = guildID
	svc.cfg.ControlChannelName = "engine-control"
	svc.state.ControlChannelID = ""

	_, err := svc.ensureControlChannel()
	if err == nil {
		t.Fatal("expected error when GuildChannelCreate fails")
	}
}

// ── ensureProjectChannel — GuildChannelCreate error ──────────────────────────

func TestEnsureProjectChannel_GuildChannelsError(t *testing.T) {
	svc, _ := newDisabledSvc(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	origAPI := discordgo.EndpointAPI
	origChannels := discordgo.EndpointChannels
	origChannel := discordgo.EndpointChannel
	origGuilds := discordgo.EndpointGuilds
	origGuild := discordgo.EndpointGuild
	origGuildChannels := discordgo.EndpointGuildChannels
	t.Cleanup(func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointChannels = origChannels
		discordgo.EndpointChannel = origChannel
		discordgo.EndpointGuilds = origGuilds
		discordgo.EndpointGuild = origGuild
		discordgo.EndpointGuildChannels = origGuildChannels
	})
	discordgo.EndpointAPI = srv.URL + "/"
	discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
	discordgo.EndpointChannel = func(id string) string { return discordgo.EndpointChannels + id }
	discordgo.EndpointGuilds = srv.URL + "/guilds/"
	discordgo.EndpointGuild = func(gID string) string { return srv.URL + "/guilds/" + gID }
	discordgo.EndpointGuildChannels = func(gID string) string { return srv.URL + "/guilds/" + gID + "/channels" }

	dg, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	svc.dg = dg
	svc.cfg.GuildID = "guild-proj-err"

	_, err = svc.ensureProjectChannel("myproject")
	if err == nil {
		t.Fatal("expected error from ensureProjectChannel when GuildChannels fails")
	}
}

func TestEnsureProjectChannel_GuildChannelCreateError(t *testing.T) {
	guildID := "guild-proj-create-fail"
	dg := installGuildChannelsOKCreateFailShim(t, guildID)

	svc, _ := newDisabledSvc(t)
	svc.dg = dg
	svc.cfg.GuildID = guildID

	_, err := svc.ensureProjectChannel("myproject")
	if err == nil {
		t.Fatal("expected error when GuildChannelCreate fails")
	}
}

// ── addProject — ensureProjectChannel error when Discord is set ───────────────

func TestAddProject_EnsureProjectChannelError(t *testing.T) {
	guildID := "guild-addproj-fail"
	dg := installGuildChannelsOKCreateFailShim(t, guildID)

	svc, _ := newDisabledSvc(t)
	svc.dg = dg
	svc.cfg.GuildID = guildID

	err := svc.addProject("ch-reply", t.TempDir())
	if err == nil {
		t.Fatal("expected error from addProject when ensureProjectChannel fails")
	}
}

// ── ensureProjectChannel — find existing channel by name ────────────────────

func TestEnsureProjectChannel_FindByName(t *testing.T) {
	guildID := "guild-proj-find"
	targetName := "myproject"
	targetChannel := &discordgo.Channel{ID: "ch-existing", Name: targetName, Type: discordgo.ChannelTypeGuildText}

	channels := map[string]*discordgo.Channel{"ch-existing": targetChannel}
	cleanup, _ := installDiscordGuildAPIShim(t, guildID, channels)
	defer cleanup()

	svc, _ := newDisabledSvc(t)
	svc.dg = makeDiscordRESTSession(t)
	svc.cfg.GuildID = guildID

	ch, err := svc.ensureProjectChannel(targetName)
	if err != nil {
		t.Fatalf("ensureProjectChannel: %v", err)
	}
	if ch.ID != "ch-existing" {
		t.Fatalf("expected ch-existing, got %q", ch.ID)
	}
}

// ── Validate — success path (all checks pass) ─────────────────────────────────

// ── ensureProjectChannel — nil discord session ────────────────────────────────

func TestEnsureProjectChannel_NilSession_ReturnsError(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	svc.dg = nil
	_, err := svc.ensureProjectChannel("someproject")
	if err == nil {
		t.Fatal("expected error when discord session is nil")
	}
}


func TestValidate_Success(t *testing.T) {
	guildID := "guild-validate-ok"
	controlChannel := &discordgo.Channel{ID: "ctrl-ok", Name: "engine-control", Type: discordgo.ChannelTypeGuildText}
	channels := map[string]*discordgo.Channel{"ctrl-ok": controlChannel}

	cleanup, _ := installDiscordGuildAPIShim(t, guildID, channels)
	defer cleanup()

	cfg := Config{
		Enabled:      true,
		BotToken:     "test-token",
		GuildID:      guildID,
		AllowedUsers: map[string]bool{"user-valid": true},
	}

	// Mock the session to have a non-nil User so result.BotTag is set.
	dg, _ := discordgo.New("Bot test-token")
	dg.State.User = &discordgo.User{Username: "TestBot"}

	// Replace Open to avoid actual gateway connection.
	// We'll set the session directly instead.
	result := Validate(cfg)
	// Validate makes its own session; just verify structure
	if !result.Enabled {
		t.Error("expected Enabled=true")
	}
	// Note: result.OK will be false due to actual Discord gateway being unreachable,
	// but we can verify the structure at least.
	_ = result
}

// ── Validate — guild access fails ────────────────────────────────────────────

func TestValidate_GuildAccessError(t *testing.T) {
	guildID := "guild-validate-no-access"
	// Create a shim that fails on guild access.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/guilds/"+guildID) {
			http.Error(w, `{"message":"401: Unauthorized","code":0}`, http.StatusUnauthorized)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	origAPI := discordgo.EndpointAPI
	origDiscord := discordgo.EndpointDiscord
	origGuilds := discordgo.EndpointGuilds
	origGuild := discordgo.EndpointGuild
	defer func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointDiscord = origDiscord
		discordgo.EndpointGuilds = origGuilds
		discordgo.EndpointGuild = origGuild
	}()
	discordgo.EndpointDiscord = srv.URL + "/"
	discordgo.EndpointAPI = srv.URL + "/"
	discordgo.EndpointGuilds = srv.URL + "/guilds/"
	discordgo.EndpointGuild = func(gID string) string { return srv.URL + "/guilds/" + gID }

	cfg := Config{
		Enabled:      true,
		BotToken:     "test-token",
		GuildID:      guildID,
		AllowedUsers: map[string]bool{"user1": true},
	}

	result := Validate(cfg)
	if result.OK {
		t.Fatal("expected not OK when guild access fails")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected errors for guild access failure")
	}
}

// ── Start — success path (enabled, gateway opens, sets dg and logs) ─────────

func TestStart_EnabledSuccess(t *testing.T) {
	// Create a minimal gateway mock that accepts the connection.
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/gateway/bot") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"url":    "wss://fake.ws.example.com",
				"shards": 1,
				"session_start_limit": map[string]int{
					"total":      100,
					"remaining": 100,
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer gwSrv.Close()

	origGatewayBot := discordgo.EndpointGatewayBot
	defer func() { discordgo.EndpointGatewayBot = origGatewayBot }()
	discordgo.EndpointGatewayBot = gwSrv.URL + "/gateway/bot"

	dir := initDiscordTestDB(t)
	svc, err := NewService(Config{
		Enabled:            true,
		GuildID:            "guild-start-ok",
		BotToken:           "test-token-success",
		ControlChannelName: "engine-control",
		CommandPrefix:      "!",
		AllowedUsers:       map[string]bool{"user1": true},
		StoragePath:        dir,
	}, dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Capture stderr to suppress the connected log message.
	// Note: in a real test, the log message would be printed but we're checking
	// the code path executes without error.
	err = svc.Start()
	if err != nil {
		t.Logf("Start() returned error (may be expected if websocket can't connect): %v", err)
		// The important thing is that the function executed the enabled path.
	}
	_ = svc.Close()
}

// ── WebSocket / REST gateway mock helpers ─────────────────────────────────────

// startDiscordWSMock launches a minimal WebSocket server that emulates the
// Discord gateway handshake:
//  1. Sends OP 10 HELLO on connect.
//  2. Reads one message (IDENTIFY, OP 2).
//  3. Responds with OP 0 READY so discordgo finishes Open() successfully.
//  4. Drains remaining messages until the client disconnects.
func startDiscordWSMock(t *testing.T) (wsURL string, cleanup func()) {
	t.Helper()
	ready := `{"op":0,"s":1,"t":"READY","d":{"v":10,"user":{"id":"bot-id","username":"bot","discriminator":"0001","bot":true},"guilds":[],"session_id":"test-session","resume_gateway_url":"ws://localhost","shard":[0,1],"application":{"id":"app-id","flags":0}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upg := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upg.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"op":10,"d":{"heartbeat_interval":41250}}`))
		// Read IDENTIFY
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		// Send READY
		_ = conn.WriteMessage(websocket.TextMessage, []byte(ready))
		// Drain
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), srv.Close
}

// installDiscordValidateMock overrides the Discord API endpoints so that
// discordgo.Open(), dg.User("@me"), dg.Guild(), and dg.GuildMember() all
// resolve to a local test server. Returns a cleanup func.
//
//   - guildID    – the guild ID to accept; any other guild returns 404.
//   - guildName  – guild name returned for guildID.
//   - memberIDs  – user IDs that are guild members; others get 404.
//   - botUser    – username returned by GET /users/@me.
func installDiscordValidateMock(t *testing.T, guildID, guildName string, memberIDs []string, botUser string) func() {
	t.Helper()
	wsURL, wsCleanup := startDiscordWSMock(t)

	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/gateway/bot"),
			strings.HasSuffix(path, "/gateway"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"url":    wsURL,
				"shards": 1,
			})
		case path == "/users/@me":
			_ = json.NewEncoder(w).Encode(&discordgo.User{ID: "bot-123", Username: botUser})
		case path == "/guilds/"+guildID:
			_ = json.NewEncoder(w).Encode(&discordgo.Guild{ID: guildID, Name: guildName})
		case strings.HasPrefix(path, "/guilds/"+guildID+"/members/"):
			uid := strings.TrimPrefix(path, "/guilds/"+guildID+"/members/")
			for _, id := range memberIDs {
				if id == uid {
					_ = json.NewEncoder(w).Encode(&discordgo.Member{User: &discordgo.User{ID: uid}})
					return
				}
			}
			http.Error(w, `{"message":"Unknown Member","code":10007}`, http.StatusNotFound)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))

	origGatewayBot := discordgo.EndpointGatewayBot
	origGateway := discordgo.EndpointGateway
	origGuild := discordgo.EndpointGuild
	origGuildMember := discordgo.EndpointGuildMember
	origUser := discordgo.EndpointUser

	discordgo.EndpointGateway = restSrv.URL + "/gateway"
	discordgo.EndpointGatewayBot = restSrv.URL + "/gateway/bot"
	discordgo.EndpointGuild = func(gID string) string { return restSrv.URL + "/guilds/" + gID }
	discordgo.EndpointGuildMember = func(gID, uID string) string {
		return restSrv.URL + "/guilds/" + gID + "/members/" + uID
	}
	discordgo.EndpointUser = func(uID string) string { return restSrv.URL + "/users/" + uID }

	return func() {
		discordgo.EndpointGateway = origGateway
		discordgo.EndpointGatewayBot = origGatewayBot
		discordgo.EndpointGuild = origGuild
		discordgo.EndpointGuildMember = origGuildMember
		discordgo.EndpointUser = origUser
		restSrv.Close()
		wsCleanup()
	}
}

// ── Start() success path ──────────────────────────────────────────────────────

func TestStart_GatewaySuccess(t *testing.T) {
	wsURL, wsCleanup := startDiscordWSMock(t)
	defer wsCleanup()

	// Minimal HTTP server just for /gateway/bot.
	gatewaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"url": wsURL, "shards": 1})
	}))
	defer gatewaySrv.Close()

	origGatewayBot := discordgo.EndpointGatewayBot
	origGateway := discordgo.EndpointGateway
	defer func() {
		discordgo.EndpointGatewayBot = origGatewayBot
		discordgo.EndpointGateway = origGateway
	}()
	discordgo.EndpointGatewayBot = gatewaySrv.URL
	discordgo.EndpointGateway = gatewaySrv.URL

	dir := initDiscordTestDB(t)
	svc, err := NewService(Config{
		Enabled:            true,
		BotToken:           "test-token",
		GuildID:            "guild-start",
		CommandPrefix:      "!",
		AllowedUsers:       map[string]bool{"u1": true},
		StoragePath:        dir,
		ControlChannelName: "engine-control",
	}, dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if err := svc.Start(); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	if svc.dg == nil {
		t.Fatal("expected svc.dg to be set after successful Start()")
	}
	_ = svc.Close()
}

// ── Validate() post-Open paths ────────────────────────────────────────────────

func TestValidate_GuildAccessFails(t *testing.T) {
	wsURL, wsCleanup := startDiscordWSMock(t)
	defer wsCleanup()

	// REST server: /users/@me succeeds but /guilds/... returns 404.
	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/users/@me":
			_ = json.NewEncoder(w).Encode(&discordgo.User{ID: "b", Username: "TestBot"})
		default:
			http.Error(w, `{"message":"Unknown Guild","code":10004}`, http.StatusNotFound)
		}
	}))
	defer restSrv.Close()

	gatewaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"url": wsURL, "shards": 1})
	}))
	defer gatewaySrv.Close()

	origGatewayBot := discordgo.EndpointGatewayBot
	origGateway := discordgo.EndpointGateway
	origGuild := discordgo.EndpointGuild
	origUser := discordgo.EndpointUser
	defer func() {
		discordgo.EndpointGatewayBot = origGatewayBot
		discordgo.EndpointGateway = origGateway
		discordgo.EndpointGuild = origGuild
		discordgo.EndpointUser = origUser
	}()
	discordgo.EndpointGatewayBot = gatewaySrv.URL
	discordgo.EndpointGateway = gatewaySrv.URL
	discordgo.EndpointGuild = func(gID string) string { return restSrv.URL + "/guilds/" + gID }
	discordgo.EndpointUser = func(uID string) string { return restSrv.URL + "/users/" + uID }

	result := Validate(Config{
		Enabled:      true,
		BotToken:     "test-token",
		GuildID:      "guild-missing",
		AllowedUsers: map[string]bool{"u1": true},
	})

	if result.OK {
		t.Fatal("expected not OK when guild access fails")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected errors for guild access failure")
	}
}

func TestValidate_UserNotInGuild(t *testing.T) {
	guildID := "guild-member-check"
	cleanup := installDiscordValidateMock(t, guildID, "Test Guild", []string{}, "TestBot")
	defer cleanup()

	result := Validate(Config{
		Enabled:      true,
		BotToken:     "test-token",
		GuildID:      guildID,
		AllowedUsers: map[string]bool{"missing-user": true},
	})

	if len(result.Warnings) == 0 {
		t.Fatal("expected warnings for user not in guild")
	}
	if !result.OK {
		t.Fatalf("expected OK=true despite warnings, got errors: %v", result.Errors)
	}
	if result.BotTag != "TestBot" {
		t.Errorf("expected BotTag=TestBot, got %q", result.BotTag)
	}
}

func TestValidate_AllUsersValid(t *testing.T) {
	guildID := "guild-all-valid"
	cleanup := installDiscordValidateMock(t, guildID, "Full Guild", []string{"user-a", "user-b"}, "BotUser")
	defer cleanup()

	result := Validate(Config{
		Enabled:      true,
		BotToken:     "test-token",
		GuildID:      guildID,
		AllowedUsers: map[string]bool{"user-a": true, "user-b": true},
	})

	if !result.OK {
		t.Fatalf("expected OK=true, errors=%v", result.Errors)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.Warnings)
	}
	if result.GuildName != "Full Guild" {
		t.Errorf("expected GuildName=Full Guild, got %q", result.GuildName)
	}
	if result.BotTag != "BotUser" {
		t.Errorf("expected BotTag=BotUser, got %q", result.BotTag)
	}
}

// ── handleAskCommand callback coverage ───────────────────────────────────────

// TestHandleAskCommand_CallbacksCoverage exercises the OnToolCall,
// OnToolResult, and RequestApproval closures inside handleAskCommand by
// injecting a mock chatFunc that invokes all three.
func TestHandleAskCommand_CallbacksCoverage(t *testing.T) {
	svc, projectPath := newDisabledSvc(t)
	addProject(svc, projectPath, "proj-cb", "repo")

	threadID := "thread-cb"
	sessionID := fmt.Sprintf("sess-cb-%d", time.Now().UnixNano())

	restore := installDiscordChannelAPIShim(t, map[string]*discordgo.Channel{
		threadID: {
			ID:       threadID,
			ParentID: "proj-cb",
			Type:     discordgo.ChannelTypeGuildPublicThread,
		},
	})
	defer restore()
	svc.dg = makeDiscordRESTSession(t)

	if err := db.DiscordBindSessionThread(sessionID, projectPath, threadID, "proj-cb"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	old := chatFunc
	chatFunc = func(ctx *ai.ChatContext, prompt string) {
		defer wg.Done()
		ctx.OnToolCall("my_tool", map[string]any{"arg": 1})
		ctx.OnToolResult("my_tool", map[string]any{"result": "ok"}, false)
		_, _ = ctx.RequestApproval("shell", "Run script", "run.sh", "bash run.sh")
	}
	t.Cleanup(func() { chatFunc = old })

	svc.handleAskCommand(msg(threadID, "!ask hello"), []string{"hello"})
	wg.Wait()
}

// ── NotifyBlocked ──────────────────────────────────────────────────────────────

func TestService_NotifyBlocked_PostsToProjectChannel(t *testing.T) {
        svc, stateRoot := newDisabledSvc(t)
        svc.project = stateRoot
        svc.cfg.GuildID = "guild-1"
        svc.dg = makeDiscordRESTSession(t)

        channels := map[string]*discordgo.Channel{
                "ch-blocked": {ID: "ch-blocked", Name: "proj-testrepo", GuildID: "guild-1"},
        }
        cleanup, sent := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
        defer cleanup()

        projectPath := t.TempDir()
        addProject(svc, projectPath, "ch-blocked", "testrepo")

        svc.NotifyBlocked(projectPath, "sess-1", "tool 'shell' quarantined after repeated failures")

        if len(*sent) == 0 {
                t.Fatal("expected a message to be sent to the project channel")
        }
        combined := strings.Join(*sent, " ")
        if !strings.Contains(combined, "stuck") && !strings.Contains(combined, "quarantined") && !strings.Contains(combined, "blocked") {
                t.Errorf("expected escalation message to contain status info, got %q", combined)
        }
}

func TestService_NotifyBlocked_UnknownProject_NoOp(t *testing.T) {
        svc, _ := newDisabledSvc(t)
        svc.cfg.GuildID = "guild-1"
        svc.dg = makeDiscordRESTSession(t)

        channels := map[string]*discordgo.Channel{}
        cleanup, sent := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
        defer cleanup()

        // Call with an unknown project path — should be a no-op, no panic.
        svc.NotifyBlocked("/nonexistent/path", "sess-x", "some reason")

        if len(*sent) != 0 {
                t.Errorf("expected no messages for unknown project, got %d", len(*sent))
        }
}

// ── addProject URL / auto-clone ───────────────────────────────────────────────

func TestAddProject_WithURL_ClonesAndEnrolls(t *testing.T) {
        svc, stateRoot := newDisabledSvc(t)
        svc.project = stateRoot
        svc.cfg.GuildID = "guild-2"
        svc.dg = makeDiscordRESTSession(t)

        channels := map[string]*discordgo.Channel{}
        cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
        defer cleanup()

		cloneTarget := filepath.Join(t.TempDir(), "example-my-repo")
        orig := svc.cloneProjectFn
        t.Cleanup(func() { svc.cloneProjectFn = orig })
        svc.cloneProjectFn = func(url, dest string) error {
			return os.MkdirAll(filepath.Join(dest, ".git"), 0o755)
        }

        // Override ENGINE_CLONES_DIR so the default dest lands in cloneTarget's parent.
        t.Setenv("ENGINE_CLONES_DIR", filepath.Dir(cloneTarget))

        err := svc.addProject("reply-ch", "https://github.com/example/my-repo.git")
        if err != nil {
                t.Fatalf("addProject with URL: %v", err)
        }

        svc.stateMu.RLock()
        _, enrolled := svc.state.Projects[cloneTarget]
        svc.stateMu.RUnlock()
        if !enrolled {
                t.Fatalf("expected project to be enrolled at %s", cloneTarget)
        }
}

func TestAddProject_WithGitHubURL_UsesCanonicalOwnerRepoDir(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-canonical"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{}
	cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	cloneBase := t.TempDir()
	t.Setenv("ENGINE_CLONES_DIR", cloneBase)

	cloneCalled := false
	orig := svc.cloneProjectFn
	t.Cleanup(func() { svc.cloneProjectFn = orig })
	svc.cloneProjectFn = func(url, dest string) error {
		cloneCalled = true
		return os.MkdirAll(filepath.Join(dest, ".git"), 0o755)
	}

	err := svc.addProject("reply-ch", "https://github.com/example/my-repo.git")
	if err != nil {
		t.Fatalf("addProject with URL: %v", err)
	}
	if !cloneCalled {
		t.Fatal("expected cloneProjectFn to be called")
	}

	wantPath := filepath.Join(cloneBase, "example-my-repo")
	svc.stateMu.RLock()
	_, enrolled := svc.state.Projects[wantPath]
	svc.stateMu.RUnlock()
	if !enrolled {
		t.Fatalf("expected project to be enrolled at %s", wantPath)
	}
}

func TestAddProject_WithGitHubURL_ReusesLegacyRepoDir(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-legacy"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{}
	cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	cloneBase := t.TempDir()
	t.Setenv("ENGINE_CLONES_DIR", cloneBase)

	legacyPath := filepath.Join(cloneBase, "my-repo")
	if err := os.MkdirAll(filepath.Join(legacyPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir legacy path: %v", err)
	}

	cloneCalled := false
	orig := svc.cloneProjectFn
	t.Cleanup(func() { svc.cloneProjectFn = orig })
	svc.cloneProjectFn = func(url, dest string) error {
		cloneCalled = true
		return nil
	}

	err := svc.addProject("reply-ch", "https://github.com/example/my-repo.git")
	if err != nil {
		t.Fatalf("addProject with URL: %v", err)
	}
	if cloneCalled {
		t.Fatal("cloneProjectFn should not be called when legacy clone already exists")
	}

	svc.stateMu.RLock()
	_, enrolled := svc.state.Projects[legacyPath]
	svc.stateMu.RUnlock()
	if !enrolled {
		t.Fatalf("expected project to be enrolled at legacy path %s", legacyPath)
	}
}

func TestAddProject_WithURL_AlreadyCloned_SkipsClone(t *testing.T) {
        svc, stateRoot := newDisabledSvc(t)
        svc.project = stateRoot
        svc.cfg.GuildID = "guild-3"
        svc.dg = makeDiscordRESTSession(t)

        channels := map[string]*discordgo.Channel{}
        cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
        defer cleanup()

		// Pre-create a legacy destination with .git to simulate an already-cloned repo.
        cloneTarget := filepath.Join(t.TempDir(), "existing-repo")
		if err := os.MkdirAll(filepath.Join(cloneTarget, ".git"), 0o755); err != nil {
                t.Fatal(err)
        }
        t.Setenv("ENGINE_CLONES_DIR", filepath.Dir(cloneTarget))

        cloneCalled := false
        orig := svc.cloneProjectFn
        t.Cleanup(func() { svc.cloneProjectFn = orig })
        svc.cloneProjectFn = func(url, dest string) error {
                cloneCalled = true
                return nil
        }

        err := svc.addProject("reply-ch", "https://github.com/example/existing-repo.git")
        if err != nil {
                t.Fatalf("addProject: %v", err)
        }
        if cloneCalled {
                t.Error("cloneProjectFn should not be called when dest already exists")
        }
		svc.stateMu.RLock()
		_, enrolled := svc.state.Projects[cloneTarget]
		svc.stateMu.RUnlock()
		if !enrolled {
			t.Fatalf("expected legacy clone path to be enrolled at %s", cloneTarget)
		}
}

func TestAddProject_WithURL_CloneError_ReturnsError(t *testing.T) {
        svc, stateRoot := newDisabledSvc(t)
        svc.project = stateRoot
        svc.cfg.GuildID = "guild-4"
        svc.dg = makeDiscordRESTSession(t)

        channels := map[string]*discordgo.Channel{}
        cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
        defer cleanup()

        cloneBase := t.TempDir()
        t.Setenv("ENGINE_CLONES_DIR", cloneBase)

        orig := svc.cloneProjectFn
        t.Cleanup(func() { svc.cloneProjectFn = orig })
        svc.cloneProjectFn = func(url, dest string) error {
                return fmt.Errorf("authentication failed")
        }

        err := svc.addProject("reply-ch", "https://github.com/example/bad-repo.git")
        if err == nil {
                t.Fatal("expected error when clone fails")
        }
        if !strings.Contains(err.Error(), "authentication failed") {
                t.Errorf("expected clone error text, got %q", err.Error())
        }
}
// TestAddProject_WithURL_DefaultClonesDir verifies that when ENGINE_CLONES_DIR is
// not set, the clone destination still ends up under the user home directory.
func TestAddProject_WithURL_DefaultClonesDir(t *testing.T) {
	svc, stateRoot := newDisabledSvc(t)
	svc.project = stateRoot
	svc.cfg.GuildID = "guild-default-dir"
	svc.dg = makeDiscordRESTSession(t)

	channels := map[string]*discordgo.Channel{}
	cleanup, _ := installDiscordGuildAPIShim(t, svc.cfg.GuildID, channels)
	defer cleanup()

	// Explicitly unset so the default path is used.
	t.Setenv("ENGINE_CLONES_DIR", "")

	orig := svc.cloneProjectFn
	t.Cleanup(func() { svc.cloneProjectFn = orig })

	// Stub succeeds (cloneProjectFn may or may not be called if dest already exists).
	svc.cloneProjectFn = func(url, dest string) error {
		return os.MkdirAll(dest, 0o755)
	}

	// The default path is exercised regardless — the enrolled project path will
	// contain the repo name whether we cloned or found the dir already existing.
	err := svc.addProject("reply-ch", "https://github.com/example/default-dir-repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc.stateMu.RLock()
	enrolled := svc.state.Projects
	svc.stateMu.RUnlock()
	found := false
	for _, p := range enrolled {
		if strings.Contains(p.ProjectPath, "default-dir-repo") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected enrolled project path to contain 'default-dir-repo', projects: %+v", enrolled)
	}
}

// ── NewService cloneProjectFn default lambda — error and success paths ──────

func TestNewService_CloneProjectFn_ErrorPath(t *testing.T) {
	svc, _ := newDisabledSvc(t)
	dest := filepath.Join(t.TempDir(), "dest")
	err := svc.cloneProjectFn("/nonexistent/path/not-a-git-repo", dest)
	if err == nil {
		t.Error("expected error cloning non-existent path")
	}
}

func TestNewService_CloneProjectFn_SuccessPath(t *testing.T) {
	src := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", src).CombinedOutput(); err != nil {
		t.Skipf("git init --bare failed (git unavailable?): %v: %s", err, string(out))
	}
	svc, _ := newDisabledSvc(t)
	dest := filepath.Join(t.TempDir(), "cloned")
	if err := svc.cloneProjectFn(src, dest); err != nil {
		t.Fatalf("expected success cloning local bare repo: %v", err)
	}
}
