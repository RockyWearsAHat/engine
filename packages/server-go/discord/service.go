package discord

// quality:allow-long-file

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	gogit "github.com/engine/server/git"
)

const (
	defaultCommandPrefix   = "!"
	defaultControlChanName = "engine-control"
	defaultConfigFileName  = "discord.json"
	defaultStateFileName   = "discord-state.json"
	maxDiscordMessageChars = 1800
	// Use Administrator to avoid setup failures caused by missing granular perms
	// or role hierarchy mismatches during initial bootstrap.
	requiredInvitePerms = discordgo.PermissionAdministrator
)

// discordApprovalTimeout is how long Engine waits for an !approve/!deny reply.
var discordApprovalTimeout = 10 * time.Minute

// Config controls Discord bot behavior.
type Config struct {
	Enabled            bool
	BotToken           string
	GuildID            string
	AllowedUsers       map[string]bool
	CommandPrefix      string
	ControlChannelName string
	StoragePath        string
	ConfigFilePath     string
}

type fileConfig struct {
	Enabled            *bool    `json:"enabled"`
	BotToken           string   `json:"botToken"`
	GuildID            string   `json:"guildId"`
	AllowedUserIDs     []string `json:"allowedUserIds"`
	CommandPrefix      string   `json:"commandPrefix"`
	ControlChannelName string   `json:"controlChannelName"`
	StoragePath        string   `json:"storagePath"`
}

// ProjectBinding keeps channel/project mapping and runtime controls.
type ProjectBinding struct {
	ProjectPath string `json:"projectPath"`
	RepoName    string `json:"repoName"`
	ChannelID   string `json:"channelId"`
	Paused      bool   `json:"paused"`
}

type persistedState struct {
	ControlChannelID string                    `json:"controlChannelId"`
	Projects         map[string]ProjectBinding `json:"projects"`
}

// chatFunc is the AI chat provider invoked by handleAskCommand.
// Tests replace it with a stub to exercise callback coverage without a live LLM.
var chatFunc = ai.Chat

// runAutonomousProject is the orchestrator entry point used by runAgentChat.
// Tests replace it to drive the build/conversational branches without a live LLM.
var runAutonomousProject = ai.RunAutonomousProject

// listSessionsFn is injectable for status tests without mutating db internals.
var listSessionsFn = db.ListSessions

// guildLeaveFn is injectable for tests covering LeaveGuild without making
// outbound Discord API calls.
var guildLeaveFn = func(dg *discordgo.Session, guildID string) error {
	return dg.GuildLeave(guildID)
}

// Service hosts the Discord control plane.
type Service struct {
	cfg             Config
	project         string
	lifecycleMu     sync.Mutex
	dg              *discordgo.Session
	stateMu         sync.RWMutex
	state           persistedState
	activeMu        sync.Mutex
	active          map[string]bool
	activeByChannel map[string]bool
	cloneProjectFn  func(url, dest string) error
	// pendingApprovals holds response channels for outstanding !approve / !deny requests.
	pendingApprovalMu sync.Mutex
	pendingApprovals  map[string]chan bool
	// testSendHook, when non-nil, intercepts send() calls instead of hitting
	// the live discordgo session. Used by control_test.go to inspect the
	// messages a handler would have posted.
	testSendHook func(channelID, msg string)
}

// LoadConfig reads Discord configuration from a project file first, then env overrides.
func LoadConfig(projectPath string) (Config, error) {
	cfg := Config{
		AllowedUsers:       map[string]bool{},
		CommandPrefix:      defaultCommandPrefix,
		ControlChannelName: defaultControlChanName,
		StoragePath:        stateDir(projectPath),
		ConfigFilePath:     configFilePath(projectPath),
	}

	projectCfg, exists, err := loadProjectConfig(projectPath)
	if err != nil {
		return cfg, err
	}
	if exists {
		applyFileConfig(&cfg, projectCfg)
	}
	applyEnvOverrides(&cfg)

	if !cfg.Enabled {
		return cfg, nil
	}

	if err := validateConfig(&cfg); err != nil {
		return cfg, err
	}

	if err := ensureStoragePath(cfg.StoragePath); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// validateConfig checks that all required Discord config fields are present.
// Returns error with clear message naming the config source if any field is missing.
func validateConfig(cfg *Config) error {
	if cfg.BotToken == "" {
		return fmt.Errorf("discord bot token is required in %s or ENGINE_DISCORD_BOT_TOKEN", cfg.ConfigFilePath)
	}
	if cfg.GuildID == "" {
		return fmt.Errorf("discord guild id is required in %s or ENGINE_DISCORD_GUILD_ID", cfg.ConfigFilePath)
	}
	if len(cfg.AllowedUsers) == 0 {
		return fmt.Errorf("at least one allowed Discord user id is required in %s or ENGINE_DISCORD_ALLOWED_USER_IDS", cfg.ConfigFilePath)
	}
	return nil
}

// ensureStoragePath creates the storage directory if it does not exist.
// Side effect: mkdir. Returns error if creation fails.
func ensureStoragePath(storagePath string) error {
	return os.MkdirAll(storagePath, 0700)
}

func loadProjectConfig(projectPath string) (fileConfig, bool, error) {
	path := configFilePath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileConfig{}, false, nil
		}
		return fileConfig{}, false, fmt.Errorf("read discord config: %w", err)
	}

	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fileConfig{}, false, fmt.Errorf("parse discord config %s: %w", path, err)
	}
	return cfg, true, nil
}

func applyFileConfig(cfg *Config, input fileConfig) {
	if input.Enabled != nil {
		cfg.Enabled = *input.Enabled
	}
	if value := strings.TrimSpace(input.BotToken); value != "" {
		cfg.BotToken = value
	}
	if value := strings.TrimSpace(input.GuildID); value != "" {
		cfg.GuildID = value
	}
	if len(input.AllowedUserIDs) > 0 {
		cfg.AllowedUsers = parseSliceSet(input.AllowedUserIDs)
	}
	if value := strings.TrimSpace(input.CommandPrefix); value != "" {
		cfg.CommandPrefix = value
	}
	if value := strings.TrimSpace(input.ControlChannelName); value != "" {
		cfg.ControlChannelName = value
	}
	if value := strings.TrimSpace(input.StoragePath); value != "" {
		cfg.StoragePath = value
	}
}

func applyEnvOverrides(cfg *Config) {
	if value, ok := parseOptionalBool(os.Getenv("ENGINE_DISCORD")); ok {
		cfg.Enabled = value
	}
	if value := strings.TrimSpace(os.Getenv("ENGINE_DISCORD_BOT_TOKEN")); value != "" {
		cfg.BotToken = value
	}
	if value := strings.TrimSpace(os.Getenv("ENGINE_DISCORD_GUILD_ID")); value != "" {
		cfg.GuildID = value
	}
	if value := strings.TrimSpace(os.Getenv("ENGINE_DISCORD_ALLOWED_USER_IDS")); value != "" {
		cfg.AllowedUsers = parseCSVSet(value)
	}
	if value := strings.TrimSpace(os.Getenv("ENGINE_DISCORD_PREFIX")); value != "" {
		cfg.CommandPrefix = value
	}
	if value := strings.TrimSpace(os.Getenv("ENGINE_DISCORD_CONTROL_CHANNEL")); value != "" {
		cfg.ControlChannelName = value
	}
	if value := strings.TrimSpace(os.Getenv("ENGINE_STATE_DIR")); value != "" {
		cfg.StoragePath = value
	}
}

func parseOptionalBool(raw string) (bool, bool) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return false, false
	}
	switch value {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func configFilePath(projectPath string) string {
	return filepath.Join(stateDir(projectPath), defaultConfigFileName)
}

// NewService creates a Discord service.
func NewService(cfg Config, projectPath string) (*Service, error) {
	s := &Service{
		cfg:     cfg,
		project: projectPath,
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
		active:          make(map[string]bool),
		activeByChannel: make(map[string]bool),
		pendingApprovals: make(map[string]chan bool),
		cloneProjectFn: func(url, dest string) error {
			cmd := exec.Command("git", "clone", url, dest)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}
	if cfg.Enabled {
		if err := s.loadState(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Start opens the Discord gateway and registers handlers.
func (s *Service) Start() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if !s.cfg.Enabled {
		return nil
	}
	if s.dg != nil {
		// Already connected for this process; do not reopen the gateway.
		return nil
	}

	// Validate token format before attempting connection. Discord bot tokens have
	// a minimum length. Attempting to connect with invalid tokens causes aggressive
	// reconnection loops that can trigger API rate limiting.
	if len(s.cfg.BotToken) < 50 {
		return fmt.Errorf("discord bot token is too short (expected ~70 chars, got %d)", len(s.cfg.BotToken))
	}

	dg, _ := discordgo.New("Bot " + s.cfg.BotToken)

	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	// Disable automatic reconnection to prevent aggressive retry loops on invalid tokens.
	// We handle reconnection manually via Reload() if needed.
	dg.ShouldReconnectOnError = false
	dg.AddHandler(s.onReady)
	dg.AddHandler(s.onMessage)

	// Set s.dg before Open so that onReady (fired in a goroutine by discordgo) can
	// safely access it. If Open fails, clear it back to nil.
	s.dg = dg
	if err := dg.Open(); err != nil {
		s.dg = nil
		return fmt.Errorf("discord open: %w", err)
	}
	log.Printf("[engine-discord] connected (guild=%s, prefix=%s)", s.cfg.GuildID, s.cfg.CommandPrefix)
	return nil
}

// Close shuts down the Discord session.
func (s *Service) Close() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.dg == nil {
		return nil
	}
	err := s.dg.Close()
	s.dg = nil
	return err
}

// LeaveGuild forces the bot to leave a guild. If guildID is empty it falls
// back to the configured guild.
func (s *Service) LeaveGuild(guildID string) error {
	target := strings.TrimSpace(guildID)
	if target == "" {
		target = strings.TrimSpace(s.cfg.GuildID)
	}
	if target == "" {
		return fmt.Errorf("guild id is required")
	}
	if s.dg == nil {
		return fmt.Errorf("discord session not active")
	}
	return guildLeaveFn(s.dg, target)
}

func (s *Service) onReady(_ *discordgo.Session, _ *discordgo.Ready) {
	if _, err := s.ensureControlChannel(); err != nil {
		log.Printf("[engine-discord] control channel setup error: %v", err)
	}
}

func (s *Service) onMessage(_ *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return
	}
	if strings.TrimSpace(m.GuildID) != s.cfg.GuildID {
		return
	}
	if !s.isAllowedUser(m.Author.ID) {
		return
	}

	// Archive every allowed inbound message, regardless of whether it parses as
	// a command. This gives agents a comprehensive searchable history of what
	// has been said in the server — without ever feeding it back wholesale.
	s.recordInbound(m)

	cmd, args, ok := parseCommand(m.Content, s.cfg.CommandPrefix)
	if !ok {
		if binding, found := s.resolveProjectByChannel(m.ChannelID); found {
			s.handleDirectChatMessage(m, binding)
		}
		return
	}

	switch cmd {
	case "help":
		s.sendHelp(m.ChannelID)
	case "project":
		s.handleProjectCommand(m, args)
	case "projects":
		s.listProjects(m.ChannelID)
	case "status":
		s.handleStatusCommand(m, args)
	case "sessions":
		s.handleSessionsCommand(m, args)
	case "lastcommit":
		s.handleLastCommitCommand(m, args)
	case "pause":
		s.handlePauseResume(m, true, args)
	case "resume":
		s.handlePauseResume(m, false, args)
	case "stop":
		s.handleStopCommand(m, args)
	case "redirect":
		s.handleRedirectCommand(m, args)
	case "plan":
		s.handlePlanCommand(m, args)
	case "orchestrators", "orch":
		s.handleOrchestratorsCommand(m, args)
	case "ask":
		s.handleAskCommand(m, args)
	case "auto", "autonomous", "build":
		s.handleAutoCommand(m, args)
	case "search":
		s.handleSearchCommand(m, args)
	case "history":
		s.handleHistoryCommand(m, args)
	case "issues", "assigned":
		s.handleIssuesCommand(m, args)
	case "identity":
		s.handleIdentityCommand(m)
	case "approve":
		s.handleApproveCommand(m, args, true)
	case "deny":
		s.handleApproveCommand(m, args, false)
	default:
		s.send(m.ChannelID, "Unknown command. Use !help.")
	}
}

func (s *Service) handleProjectCommand(m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 {
		s.send(m.ChannelID, "Usage: !project add <path> | !project list | !project remove <name>")
		return
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "add":
		if len(args) < 2 {
			s.send(m.ChannelID, "Usage: !project add <path>")
			return
		}
		if err := s.addProject(m.ChannelID, strings.Join(args[1:], " ")); err != nil {
			s.send(m.ChannelID, "Project add failed: "+err.Error())
		}
	case "list":
		s.listProjects(m.ChannelID)
	case "remove":
		if len(args) < 2 {
			s.send(m.ChannelID, "Usage: !project remove <name>")
			return
		}
		if err := s.removeProject(m.ChannelID, args[1]); err != nil {
			s.send(m.ChannelID, "Project remove failed: "+err.Error())
		}
	default:
		s.send(m.ChannelID, "Unknown project command. Use: add, list, remove")
	}
}

func (s *Service) addProject(replyChannelID string, inputPath string) error {
	clean := strings.TrimSpace(inputPath)
	if clean == "" {
		return fmt.Errorf("path is required")
	}

	isURL := strings.HasPrefix(clean, "https://") || strings.HasPrefix(clean, "http://") || strings.HasPrefix(clean, "git@")
	if isURL {
		var err error
		clean, err = s.handleGitURLProject(clean)
		if err != nil {
			return err
		}
	}

	if err := validateProjectPath(clean); err != nil {
		return err
	}

	abs, _ := filepath.Abs(clean)

repo := filepath.Base(abs)
	channelName := "proj-" + slug(repo)
	ch, err := s.ensureProjectChannel(channelName)
	if err != nil {
		return err
	}

	s.updateProjectStateAfterAdd(abs, repo, ch.ID)

	branch, _ := gogit.GetCurrentBranch(abs)
	s.send(replyChannelID, fmt.Sprintf("Project enrolled: %s\\nChannel: <#%s>\\nBranch: %s", abs, ch.ID, branch))
	s.send(ch.ID, "Engine linked to this project channel. Just chat normally to run the agent. Commands still available: !status !sessions !lastcommit !pause !resume !search !history")
	return nil
}

// handleGitURLProject clones a git URL to the local clones directory and
// returns the local path. Side effects: mkdir for clones dir, git clone call,
// directory stat checks. Returns error on any step.
func (s *Service) handleGitURLProject(urlString string) (string, error) {
	clonesDir := resolveClonesDir(s.project)
	if err := os.MkdirAll(clonesDir, 0o755); err != nil {
		return "", fmt.Errorf("create clones directory: %w", err)
	}

	candidates, primary := cloneDirNameCandidates(urlString)
	if existingPath, ok := findExistingClonePath(clonesDir, candidates); ok {
		return existingPath, nil
	}

	dest := filepath.Join(clonesDir, primary)
	if destInfo, err := os.Stat(dest); err == nil && destInfo.IsDir() {
		if _, gitErr := os.Stat(filepath.Join(dest, ".git")); gitErr != nil {
			return "", fmt.Errorf("clone destination exists but is not a git repository: %s", dest)
		}
	} else {
		if err := s.cloneProjectFn(urlString, dest); err != nil {
			return "", fmt.Errorf("clone %s: %w", urlString, err)
		}
	}

	return dest, nil
}

// validateProjectPath checks that the path is an existing directory.
func validateProjectPath(pathStr string) error {
	abs, _ := filepath.Abs(pathStr)
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("path must be an existing directory")
	}
	return nil
}

// updateProjectStateAfterAdd mutates the service state to add the project,
// persists state to disk, and returns without sending notifications.
// Side effects: stateMu lock, state.Projects write, saveState().
func (s *Service) updateProjectStateAfterAdd(projectPath, repoName, channelID string) {
	s.stateMu.Lock()
	s.state.Projects[projectPath] = ProjectBinding{
		ProjectPath: projectPath,
		RepoName:    repoName,
		ChannelID:   channelID,
		Paused:      false,
	}
	s.stateMu.Unlock()
	if err := s.saveState(); err != nil {
		log.Printf("discord: save state after addProject failed: %v", err)
	}
}

func resolveClonesDir(projectPath string) string {
	if clonesDir := strings.TrimSpace(os.Getenv("ENGINE_CLONES_DIR")); clonesDir != "" {
		return clonesDir
	}
	home, _ := os.UserHomeDir()
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".engine", "projects")
	}
	if strings.TrimSpace(projectPath) != "" {
		return filepath.Join(projectPath, ".engine", "projects")
	}
	return filepath.Join(".engine", "projects")
}

func cloneDirNameCandidates(rawURL string) ([]string, string) {
	owner, repo, ok := parseGitHubOwnerRepo(rawURL)
	baseName := strings.TrimSuffix(filepath.Base(rawURL), ".git")
	if !ok || repo == "" {
		return []string{baseName}, baseName
	}
	ownerRepo := owner + "-" + repo
	return []string{ownerRepo, repo}, ownerRepo
}

func parseGitHubOwnerRepo(rawURL string) (string, string, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", "", false
	}

	if pathPart, ok := strings.CutPrefix(trimmed, "git@github.com:"); ok {
		pathPart = strings.TrimSuffix(pathPart, ".git")
		parts := strings.SplitN(pathPart, "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return "", "", false
		}
		return parts[0], parts[1], true
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", false
	}
	if !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", "", false
	}
	pathPart := strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/")
	parts := strings.SplitN(pathPart, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func findExistingClonePath(clonesDir string, candidates []string) (string, bool) {
	seen := make(map[string]bool)
	for _, name := range candidates {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		candidatePath := filepath.Join(clonesDir, trimmed)
		if _, err := os.Stat(filepath.Join(candidatePath, ".git")); err == nil {
			return candidatePath, true
		}
	}
	return "", false
}

// AutoEnrollProject enrolls an already-cloned repo into Discord and announces
// it in the control channel. Used by the scaffold pipeline.
func (s *Service) AutoEnrollProject(projectPath, owner, repo string) error {
	absPath, _ := filepath.Abs(strings.TrimSpace(projectPath))

	s.stateMu.RLock()
	_, alreadyEnrolled := s.state.Projects[absPath]
	s.stateMu.RUnlock()

	if !alreadyEnrolled {
		if err := s.addProject("", absPath); err != nil {
			return err
		}
	}

	if alreadyEnrolled {
		return nil
	}

	controlID, err := s.ensureControlChannel()
	if err != nil {
		return err
	}
	s.send(controlID, fmt.Sprintf("🤖 Picked up **%s/%s** — working autonomously.", owner, repo))
	return nil
}

// NotifyProjectProgress posts a status update to the enrolled project channel.
func (s *Service) NotifyProjectProgress(projectPath, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.stateMu.RLock()
	binding, ok := s.state.Projects[projectPath]
	s.stateMu.RUnlock()
	if !ok {
		return
	}
	s.sendTagged(binding.ChannelID, message, "status", "")
}

func (s *Service) removeProject(replyChannelID, name string) error {
	ref := strings.TrimSpace(name)
	if ref == "" {
		return fmt.Errorf("project name is required")
	}

	path, binding, ok := s.resolveProjectByRef(ref)
	if !ok {
		return fmt.Errorf("project not found: %s", ref)
	}

	s.stateMu.Lock()
	delete(s.state.Projects, path)
	s.stateMu.Unlock()
	if err := s.saveState(); err != nil {
		log.Printf("discord: save state after removeProject failed: %v", err)
	}

	s.send(replyChannelID, fmt.Sprintf("Removed project %s (channel kept: <#%s>).", binding.RepoName, binding.ChannelID))
	return nil
}

// NotifyBlocked posts a help-request message to the project's Discord channel when
// the AI agent is stuck and cannot make progress without human intervention.
func (s *Service) NotifyBlocked(projectPath, sessionID, reason string) {
	s.stateMu.RLock()
	binding, ok := s.state.Projects[projectPath]
	s.stateMu.RUnlock()
	if !ok {
		return
	}
	msg := fmt.Sprintf("🚧 Engine is stuck on **%s** (session: %s):\n%s", binding.RepoName, sessionID, reason)
	s.send(binding.ChannelID, msg)
}

// SendDM sends a direct Discord message to a specific user by ID.
func (s *Service) SendDM(userID, message string) error {
	if s.dg == nil {
		return fmt.Errorf("discord bot not connected")
	}
	ch, err := s.dg.UserChannelCreate(userID)
	if err != nil {
		return fmt.Errorf("create DM channel: %w", err)
	}
	_, err = s.dg.ChannelMessageSend(ch.ID, message)
	return err
}

// SendDMToOwner sends a DM to the first allowed user in the bot config.
func (s *Service) SendDMToOwner(message string) error {
	s.stateMu.RLock()
	var userID string
	for uid := range s.cfg.AllowedUsers {
		userID = uid
		break
	}
	s.stateMu.RUnlock()
	if userID == "" {
		return fmt.Errorf("no allowed Discord users configured")
	}
	return s.SendDM(userID, message)
}

// approvalShortID generates a compact hex ID for an approval request.
func approvalShortID() string {
	return fmt.Sprintf("%06x", time.Now().UnixNano()&0xffffff)
}

// requestApprovalViaDM posts an approval request to the project channel,
// DMs the owner, and blocks until the user responds with !approve <id> or
// !deny <id>. Returns immediately when no users are configured.
func (s *Service) requestApprovalViaDM(projectPath, kind, title, message, command string) (bool, error) {
	s.stateMu.RLock()
	hasUsers := len(s.cfg.AllowedUsers) > 0
	s.stateMu.RUnlock()
	if !hasUsers {
		return false, fmt.Errorf("approval required but no Discord users configured")
	}

	id := approvalShortID()
	preview := command
	if len(preview) > 200 {
		preview = preview[:197] + "\u2026"
	}
	channelMsg := fmt.Sprintf(
		"\u23f3 **Approval needed** (ID: `%s`)\n%s: `%s`\nReply `!approve %s` to allow or `!deny %s` to block.",
		id, title, preview, id, id,
	)
	dmMsg := fmt.Sprintf(
		"\U0001f510 **Engine needs your approval**\n%s\nCommand: `%s`\n\nIn the project channel, reply:\n\u2022 `!approve %s` \u2014 allow\n\u2022 `!deny %s` \u2014 block",
		message, preview, id, id,
	)

	ch := make(chan bool, 1)
	s.pendingApprovalMu.Lock()
	if s.pendingApprovals == nil {
		s.pendingApprovals = make(map[string]chan bool)
	}
	s.pendingApprovals[id] = ch
	s.pendingApprovalMu.Unlock()

	s.stateMu.RLock()
	binding, ok := s.state.Projects[projectPath]
	s.stateMu.RUnlock()
	if ok {
		s.send(binding.ChannelID, channelMsg)
	}
	_ = s.SendDMToOwner(dmMsg)

	select {
	case allow := <-ch:
		return allow, nil
	case <-time.After(discordApprovalTimeout):
		s.pendingApprovalMu.Lock()
		delete(s.pendingApprovals, id)
		s.pendingApprovalMu.Unlock()
		return false, fmt.Errorf("approval timed out after %v", discordApprovalTimeout)
	}
}

// handleApproveCommand resolves a pending approval. allow=true for !approve, allow=false for !deny.
func (s *Service) handleApproveCommand(m *discordgo.MessageCreate, args []string, allow bool) {
	if len(args) == 0 {
		verb := "approve"
		if !allow {
			verb = "deny"
		}
		s.send(m.ChannelID, fmt.Sprintf("Usage: !%s <id>", verb))
		return
	}
	id := strings.TrimSpace(args[0])
	s.pendingApprovalMu.Lock()
	if s.pendingApprovals == nil {
		s.pendingApprovals = make(map[string]chan bool)
	}
	ch, ok := s.pendingApprovals[id]
	if ok {
		delete(s.pendingApprovals, id)
	}
	s.pendingApprovalMu.Unlock()
	if !ok {
		s.send(m.ChannelID, fmt.Sprintf("No pending approval with ID `%s`.", id))
		return
	}
	ch <- allow
	close(ch)
	if allow {
		s.send(m.ChannelID, fmt.Sprintf("\u2705 Approved (`%s`).", id))
	} else {
		s.send(m.ChannelID, fmt.Sprintf("\U0001f6ab Denied (`%s`).", id))
	}
}

func (s *Service) listProjects(channelID string) {
	s.stateMu.RLock()
	projects := make([]ProjectBinding, 0, len(s.state.Projects))
	for _, p := range s.state.Projects {
		projects = append(projects, p)
	}
	s.stateMu.RUnlock()

	if len(projects) == 0 {
		s.send(channelID, "No projects enrolled. Use !project add <path>.")
		return
	}

	sort.Slice(projects, func(i, j int) bool { return projects[i].RepoName < projects[j].RepoName })
	var b strings.Builder
	b.WriteString("Enrolled projects:\n")
	for _, p := range projects {
		state := "active"
		if p.Paused {
			state = "paused"
		}
		fmt.Fprintf(&b, "- %s (%s) in <#%s>\n", p.RepoName, state, p.ChannelID)
	}
	s.send(channelID, b.String())
}

func (s *Service) handleStatusCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use this command in a project channel or pass a project name.")
		return
	}

	status, _ := gogit.GetStatus(binding.ProjectPath)
	msg := fmt.Sprintf(
		"%s status\\nbranch: %s (ahead %d / behind %d)\\nstaged: %d, unstaged: %d, untracked: %d\\nmode: %s",
		binding.RepoName,
		status.Branch,
		status.Ahead,
		status.Behind,
		len(status.Staged),
		len(status.Unstaged),
		len(status.Untracked),
		ternary(binding.Paused, "paused", "active"),
	)
	if scaffold := latestScaffoldStatusLine(binding.ProjectPath); scaffold != "" {
		msg += "\\n" + scaffold
	}
	s.send(m.ChannelID, msg)
}

func latestScaffoldStatusLine(projectPath string) string {
	sessions, err := listSessionsFn(projectPath)
	if err != nil {
		return ""
	}
	for _, sess := range sessions {
		if !strings.HasPrefix(strings.ToLower(sess.ID), "scaffold-") {
			continue
		}
		updated := strings.TrimSpace(sess.UpdatedAt)
		if updated == "" {
			updated = strings.TrimSpace(sess.CreatedAt)
		}
		summary := strings.TrimSpace(sess.Summary)
		if summary == "" {
			summary = "no summary yet"
		}
		return fmt.Sprintf("latest scaffold: %s | updated: %s | messages: %d | %s", shortID(sess.ID), updated, sess.MessageCount, truncate(summary, 160))
	}
	return ""
}

func (s *Service) handleSessionsCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found.")
		return
	}

	sessions, _ := db.ListSessions(binding.ProjectPath)
	if len(sessions) == 0 {
		s.send(m.ChannelID, "No sessions yet for this project.")
		return
	}

	limit := min(len(sessions), 5)
	var b strings.Builder
	b.WriteString("Recent sessions:\n")
	for i := range limit {
		sess := sessions[i]
		summary := strings.TrimSpace(sess.Summary)
		if summary == "" {
			summary = "(no summary yet)"
		}
		if len(summary) > 120 {
			summary = summary[:117] + "..."
		}
		fmt.Fprintf(&b, "- %s | msgs=%d | %s\\n", shortID(sess.ID), sess.MessageCount, summary)
	}
	s.send(m.ChannelID, b.String())
}

func (s *Service) handleLastCommitCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found.")
		return
	}

	commits, err := gogit.GetLog(binding.ProjectPath, 1)
	if err != nil || len(commits) == 0 {
		s.send(m.ChannelID, "No commit information available.")
		return
	}
	commit := commits[0]
	s.send(m.ChannelID, fmt.Sprintf("last commit %s by %s\\n%s", commit.Hash, commit.Author, commit.Message))
}

func (s *Service) handlePauseResume(m *discordgo.MessageCreate, pause bool, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found.")
		return
	}

	s.stateMu.Lock()
	updated := s.state.Projects[binding.ProjectPath]
	updated.Paused = pause
	s.state.Projects[binding.ProjectPath] = updated
	s.stateMu.Unlock()
	if err := s.saveState(); err != nil {
		log.Printf("discord: save state after pause toggle failed: %v", err)
	}

	s.applyPauseToOrchestrator(binding.ProjectPath, pause)

	if pause {
		s.send(m.ChannelID, fmt.Sprintf("Paused project %s.", binding.RepoName))
	} else {
		s.send(m.ChannelID, fmt.Sprintf("Resumed project %s.", binding.RepoName))
	}
}

func (s *Service) handleAskCommand(m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 {
		s.send(m.ChannelID, "Usage: !ask <prompt> or !ask <project> <prompt> from control channel")
		return
	}

	binding, prompt, ok := s.resolveAskTarget(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use a project channel or include project name.")
		return
	}
	if strings.TrimSpace(prompt) == "" {
		s.send(m.ChannelID, "Prompt is required.")
		return
	}
	s.runAgentChat(m, binding, prompt)
}

// handleAutoCommand is the explicit autonomous-mode quick command. Phrasing
// is `!auto <prompt>`, `!autonomous <prompt>`, or `!build <prompt>`. The
// prompt is forwarded to the agent prefixed with `/auto` so the autonomous
// intent detector engages unconditionally — no matter how the user phrased
// the actual request.
func (s *Service) handleAutoCommand(m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 {
		s.send(m.ChannelID, "Usage: !auto <prompt> (or !autonomous / !build). Engages autonomous mode for that turn.")
		return
	}
	binding, prompt, ok := s.resolveAskTarget(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use a project channel or include project name.")
		return
	}
	if strings.TrimSpace(prompt) == "" {
		s.send(m.ChannelID, "Prompt is required.")
		return
	}
	s.runAgentChat(m, binding, "/auto "+prompt)
}

func (s *Service) handleDirectChatMessage(m *discordgo.MessageCreate, binding ProjectBinding) {
	prompt := strings.TrimSpace(m.Content)
	if prompt == "" {
		return
	}
	if isStatusLikeDirectMessage(prompt) {
		s.handleStatusCommand(m, nil)
		return
	}
	s.runAgentChat(m, binding, prompt)
}

func isStatusLikeDirectMessage(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.Trim(normalized, " \t\n\r?!.")
	switch normalized {
	case "status", "update", "progress", "build status", "scaffold status", "what is the status", "whats the status", "what's the status", "how is it going", "where are we", "any update":
		return true
	default:
		return false
	}
}

func (s *Service) runAgentChat(m *discordgo.MessageCreate, binding ProjectBinding, prompt string) {
	if binding.Paused {
		s.send(m.ChannelID, "Project is paused. Run !resume first.")
		return
	}

	replyChannelID, sessionID, err := s.acquireProjectChatSession(m, binding, prompt)
	if err != nil {
		s.send(m.ChannelID, "Could not start project chat: "+err.Error())
		return
	}

	if !s.markTaskActive(sessionID, binding.ChannelID, replyChannelID) {
		return
	}

	s.send(replyChannelID, "Running agent request...")
	channelID := binding.ChannelID
	go s.runAgentChatAsync(binding, replyChannelID, sessionID, channelID, prompt)
}

// markTaskActive atomically checks and marks a session/channel as active.
// Side effects: activeMu lock, active/activeByChannel map write.
// Returns false and sends error message if task already running.
func (s *Service) markTaskActive(sessionID, channelID, replyChannelID string) bool {
	s.activeMu.Lock()
	if s.active == nil {
		s.active = make(map[string]bool)
	}
	if s.activeByChannel == nil {
		s.activeByChannel = make(map[string]bool)
	}
	if s.activeByChannel[channelID] {
		s.activeMu.Unlock()
		s.send(replyChannelID, "A task is already running for this project channel. Wait for it to finish.")
		return false
	}
	if s.active[sessionID] {
		s.activeMu.Unlock()
		s.send(replyChannelID, "A task is already running for this session. Wait for it to finish.")
		return false
	}
	s.active[sessionID] = true
	s.activeByChannel[channelID] = true
	s.activeMu.Unlock()
	return true
}

// markTaskInactive clears the active flags for a session/channel.
// Side effects: activeMu lock, active/activeByChannel map delete.
func (s *Service) markTaskInactive(sessionID, channelID string) {
	s.activeMu.Lock()
	delete(s.active, sessionID)
	delete(s.activeByChannel, channelID)
	s.activeMu.Unlock()
}

// runAgentChatAsync runs the autonomous project orchestrator and handles results.
// Runs in a goroutine. Side effects: ai.RunAutonomousProject (external), db writes,
// send() calls, markTaskInactive cleanup. Handles errors by sending to replyChannelID.
func (s *Service) runAgentChatAsync(binding ProjectBinding, replyChannelID, sessionID, channelID, prompt string) {
	defer s.markTaskInactive(sessionID, channelID)

	var outMu sync.Mutex
	var output strings.Builder
	var lastErr string

	cancel := make(chan struct{})
	defer close(cancel)

	chatPrompt := promptWithProjectStatusContext(binding, prompt)

	state, runErr := runAutonomousProject(s.buildChatContext(binding, sessionID, replyChannelID, &output, &outMu, &lastErr, cancel, chatPrompt))

	if strings.TrimSpace(lastErr) != "" {
		s.send(replyChannelID, "Agent error: "+lastErr)
		return
	}
	if runErr != nil {
		s.send(replyChannelID, "Agent error: "+runErr.Error())
		return
	}

	if state != nil && !state.Conversational {
		s.send(replyChannelID, fmt.Sprintf("Orchestrator completed (%d steps, %d iterations).", len(state.Plan), state.OuterIterations))
		return
	}

	outMu.Lock()
	answer := strings.TrimSpace(output.String())
	outMu.Unlock()
	if answer == "" {
		answer = "(No response text returned.)"
	}

	for _, part := range splitForDiscord(answer, maxDiscordMessageChars) {
		s.sendTagged(replyChannelID, part, "agent", sessionID)
	}
}

// buildChatContext constructs the orchestrator config with callbacks for a chat session.
// Side effects: none (pure construction). The returned config will have side effects
// when executed by the orchestrator.
func (s *Service) buildChatContext(binding ProjectBinding, sessionID, replyChannelID string, output *strings.Builder, outMu *sync.Mutex, lastErr *string, cancel <-chan struct{}, chatPrompt string) ai.OrchestratorConfig {
	return ai.OrchestratorConfig{
		ProjectPath:     binding.ProjectPath,
		Brief:           chatPrompt,
		SessionIDPrefix: fmt.Sprintf("discord-%s", sessionID),
		Cancel:          cancel,
		ChatFn:          chatFunc,
		InteractiveChat: func(brief string, cxl <-chan struct{}) {
			chatFunc(&ai.ChatContext{
				ProjectPath: binding.ProjectPath,
				SessionID:   sessionID,
				Cancel:      cxl,
				Role:        ai.RoleInteractive,
				OnChunk: func(content string, done bool) {
					if done || strings.TrimSpace(content) == "" {
						return
					}
					outMu.Lock()
					output.WriteString(content)
					outMu.Unlock()
				},
				OnToolCall:       func(_ string, _ any) {},
				OnToolResult:     func(_ string, _ any, _ bool) {},
				OnError:          func(err string) { *lastErr = strings.TrimSpace(err) },
				OnSessionUpdated: func(_ *db.Session) {},
				RequestApproval: func(kind, title, message, command string) (bool, error) {
					return s.requestApprovalViaDM(binding.ProjectPath, kind, title, message, command)
				},
			}, brief)
		},
		OnPhase: func(phase, detail string) {
			s.send(replyChannelID, fmt.Sprintf("Orchestrator %s: %s", phase, detail))
		},
		OnProgress: func(message string) {
			if strings.TrimSpace(message) != "" {
				s.send(replyChannelID, message)
			}
		},
		OnError: func(err string) {
			if strings.TrimSpace(err) != "" {
				*lastErr = strings.TrimSpace(err)
			}
		},
	}
}

func promptWithProjectStatusContext(binding ProjectBinding, prompt string) string {
	status := latestScaffoldStatusLine(binding.ProjectPath)
	if status == "" {
		return prompt
	}
	return strings.TrimSpace(fmt.Sprintf("Discord project context:\n- Project: %s\n- %s\n- This message came from the project channel, so answer with awareness of existing project/session history. Do not claim setup is just starting if scaffold sessions already exist.\n\nUser message:\n%s", binding.RepoName, status, prompt))
}

func channelSessionKey(channelID string) string {
	return "channel:" + strings.TrimSpace(channelID)
}

// acquireProjectChatSession returns the project channel ID and chat session ID
// for a !ask request. The session is bound to the project channel so all
// conversation stays in one place.
func (s *Service) acquireProjectChatSession(m *discordgo.MessageCreate, binding ProjectBinding, prompt string) (string, string, error) {
	if strings.TrimSpace(binding.ChannelID) == "" {
		return "", "", fmt.Errorf("project channel is not configured")
	}

	key := channelSessionKey(binding.ChannelID)
	if existing, _ := db.DiscordGetSessionByThread(key); existing != nil && strings.TrimSpace(existing.SessionID) != "" {
		return binding.ChannelID, existing.SessionID, nil
	}

	sessionID := s.newSessionForThread(binding.ProjectPath, key, binding.ChannelID)
	author := "user"
	if m != nil && m.Author != nil && strings.TrimSpace(m.Author.Username) != "" {
		author = m.Author.Username
	}
	s.sendTagged(binding.ChannelID, "Chat started by "+author+":\n> "+truncate(prompt, 600), "message", sessionID)
	return binding.ChannelID, sessionID, nil
}

// acquireChatThread returns the thread ID the chat should post to and the
// session ID tied to it. If the caller is already inside a thread, reuse it;
// otherwise create a new thread under the project channel and bind it to a
// fresh session.
func (s *Service) acquireChatThread(m *discordgo.MessageCreate, binding ProjectBinding, prompt string) (string, string, error) {
	if s.dg == nil {
		return "", "", fmt.Errorf("discord session not open")
	}

	// If the invoking message is already inside a thread, reuse its session.
	ch, err := s.dg.Channel(m.ChannelID)
	if err == nil && ch != nil && isThread(ch) {
		threadID := ch.ID
		existing, _ := db.DiscordGetSessionByThread(threadID) //nolint:errcheck
		if existing != nil && strings.TrimSpace(existing.SessionID) != "" {
			return threadID, existing.SessionID, nil
		}
		sessionID := s.newSessionForThread(binding.ProjectPath, threadID, binding.ChannelID)
		return threadID, sessionID, nil
	}

	// Otherwise, create a new thread under the project channel.
	threadName := buildThreadName(prompt)
	thread, err := s.dg.ThreadStart(binding.ChannelID, threadName, discordgo.ChannelTypeGuildPublicThread, 60*24)
	if err != nil {
		return "", "", fmt.Errorf("create thread: %w", err)
	}
	sessionID := s.newSessionForThread(binding.ProjectPath, thread.ID, binding.ChannelID)
	// Echo the invoking prompt into the new thread so the transcript is
	// self-contained.
	s.sendTagged(thread.ID, "Chat started by "+m.Author.Username+":\n> "+truncate(prompt, 600), "message", sessionID)
	return thread.ID, sessionID, nil
}

func (s *Service) newSessionForThread(projectPath, threadID, channelID string) string {
	id := fmt.Sprintf("discord-%d", time.Now().UnixNano())
	branch, _ := gogit.GetCurrentBranch(projectPath)
	if err := db.CreateSession(id, projectPath, branch); err != nil {
		log.Printf("discord: create session %s failed: %v", id, err)
	}
	if err := db.DiscordBindSessionThread(id, projectPath, threadID, channelID); err != nil {
		log.Printf("discord: bind session thread %s failed: %v", id, err)
	}
	return id
}

func (s *Service) handleSearchCommand(m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 {
		s.send(m.ChannelID, "Usage: !search <query>")
		return
	}
	binding, ok := s.resolveProjectForMessage(m.ChannelID, nil)
	projectPath := ""
	if ok {
		projectPath = binding.ProjectPath
	}
	query := strings.Join(args, " ")
	hits, _ := db.DiscordSearchMessages(projectPath, query, "", 10)
	if len(hits) == 0 {
		s.send(m.ChannelID, "No matches.")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Top %d matches for `%s`:\n", len(hits), truncate(query, 60))
	for _, h := range hits {
		fmt.Fprintf(&b, "- [%s] %s (%s): %s\n",
			shortTime(h.CreatedAt),
			displayName(h.AuthorName, h.Direction),
			h.Kind,
			truncate(h.Snippet, 200),
		)
	}
	s.send(m.ChannelID, truncate(b.String(), maxDiscordMessageChars))
}

func (s *Service) handleHistoryCommand(m *discordgo.MessageCreate, args []string) {
	hours := 24
	if len(args) >= 1 {
		if parsed, err := parsePositiveInt(args[0]); err == nil {
			hours = parsed
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).UTC().Format(time.RFC3339)
	threadID := ""
	projectPath := ""
	if s.dg != nil {
		if ch, err := s.dg.Channel(m.ChannelID); err == nil && ch != nil && isThread(ch) {
			threadID = ch.ID
		}
	}
	if binding, ok := s.resolveProjectForMessage(m.ChannelID, nil); ok {
		projectPath = binding.ProjectPath
	}
	rows, _ := db.DiscordListRecentMessages(projectPath, threadID, since, 20)
	if len(rows) == 0 {
		s.send(m.ChannelID, fmt.Sprintf("No messages in the last %dh.", hours))
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Last %d messages (since %dh ago):\n", len(rows), hours)
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		fmt.Fprintf(&b, "- [%s] %s: %s\n",
			shortTime(r.CreatedAt),
			displayName(r.AuthorName, r.Direction),
			truncate(r.Content, 160),
		)
	}
	s.send(m.ChannelID, truncate(b.String(), maxDiscordMessageChars))
}

// ── Archival helpers ──────────────────────────────────────────────────────

func (s *Service) recordInbound(m *discordgo.MessageCreate) {
	channelID, threadID := s.splitChannelThread(m.ChannelID)
	projectPath, sessionID := s.resolveContext(channelID, threadID)
	authorName := ""
	authorID := ""
	if m.Author != nil {
		authorName = m.Author.Username
		authorID = m.Author.ID
	}
	kind := "message"
	if strings.HasPrefix(strings.TrimSpace(m.Content), s.cfg.CommandPrefix) {
		kind = "command"
	}
	if err := db.DiscordRecordMessage(db.DiscordMessage{
		ID:          "dm-in-" + m.ID,
		ProjectPath: projectPath,
		ChannelID:   channelID,
		ThreadID:    threadID,
		SessionID:   sessionID,
		AuthorID:    authorID,
		AuthorName:  authorName,
		Direction:   "in",
		Kind:        kind,
		Content:     m.Content,
	}); err != nil {
		log.Printf("discord: record inbound message failed: %v", err)
	}
}

func (s *Service) recordOutbound(channelID, content, kind, sessionIDHint string) {
	srcChannel, threadID := s.splitChannelThread(channelID)
	projectPath := ""
	sessionID := strings.TrimSpace(sessionIDHint)
	pp, resolvedSession := s.resolveContext(srcChannel, threadID)
	projectPath = pp
	if sessionID == "" {
		sessionID = resolvedSession
	}
	if err := db.DiscordRecordMessage(db.DiscordMessage{
		ProjectPath: projectPath,
		ChannelID:   srcChannel,
		ThreadID:    threadID,
		SessionID:   sessionID,
		AuthorName:  "engine",
		Direction:   "out",
		Kind:        kind,
		Content:     content,
	}); err != nil {
		log.Printf("discord: record outbound message failed: %v", err)
	}
}

// sendTagged is like send but records the outbound with an explicit kind and
// session hint. Used for agent-authored answers so archive queries can
// distinguish them.
func (s *Service) sendTagged(channelID, msg, kind, sessionID string) {
	if s.dg == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	if strings.TrimSpace(msg) == "" {
		return
	}
	if _, err := s.dg.ChannelMessageSend(channelID, msg); err != nil {
		log.Printf("[engine-discord] send error: %v", err)
		return
	}
	s.recordOutbound(channelID, msg, kind, sessionID)
}

// splitChannelThread turns a possibly-thread channel ID into (parent, thread).
// Returns (channelID, "") if the channel is not a thread.
func (s *Service) splitChannelThread(channelID string) (string, string) {
	if s.dg == nil || strings.TrimSpace(channelID) == "" {
		return channelID, ""
	}
	ch, err := s.dg.Channel(channelID)
	if err != nil || ch == nil {
		return channelID, ""
	}
	if isThread(ch) {
		return ch.ParentID, ch.ID
	}
	return ch.ID, ""
}

func (s *Service) resolveContext(channelID, threadID string) (projectPath, sessionID string) {
	if strings.TrimSpace(threadID) != "" {
		if mapping, _ := db.DiscordGetSessionByThread(threadID); mapping != nil {
			return mapping.ProjectPath, mapping.SessionID
		}
	}
	// Fall back to channel-based project lookup.
	if strings.TrimSpace(channelID) != "" {
		s.stateMu.RLock()
		defer s.stateMu.RUnlock()
		for _, p := range s.state.Projects {
			if p.ChannelID == channelID {
				return p.ProjectPath, ""
			}
		}
	}
	return "", ""
}

func isThread(ch *discordgo.Channel) bool {
	return ch.Type == discordgo.ChannelTypeGuildPublicThread ||
		ch.Type == discordgo.ChannelTypeGuildPrivateThread ||
		ch.Type == discordgo.ChannelTypeGuildNewsThread
}

func buildThreadName(prompt string) string {
	cleaned := strings.TrimSpace(prompt)
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	if len(cleaned) > 60 {
		cleaned = cleaned[:57] + "..."
	}
	if cleaned == "" {
		cleaned = "chat"
	}
	return fmt.Sprintf("chat-%s", cleaned)
}

func parsePositiveInt(raw string) (int, error) {
	v := 0
	for _, r := range strings.TrimSpace(raw) {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a positive integer")
		}
		v = v*10 + int(r-'0')
	}
	if v == 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return v, nil
}

func shortTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("01-02 15:04")
}

func displayName(name, direction string) string {
	if strings.TrimSpace(name) == "" {
		if direction == "out" {
			return "engine"
		}
		return "user"
	}
	return name
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max-1] + "\u2026"
}

func (s *Service) resolveProjectForMessage(channelID string, args []string) (ProjectBinding, bool) {
	if p, ok := s.resolveProjectByChannel(channelID); ok {
		return p, true
	}
	if len(args) == 0 {
		return ProjectBinding{}, false
	}
	_, p, ok := s.resolveProjectByRef(args[0])
	return p, ok
}

func (s *Service) resolveAskTarget(channelID string, args []string) (ProjectBinding, string, bool) {
	if p, ok := s.resolveProjectByChannel(channelID); ok {
		return p, strings.Join(args, " "), true
	}
	if len(args) < 2 {
		return ProjectBinding{}, "", false
	}
	_, p, ok := s.resolveProjectByRef(args[0])
	if !ok {
		return ProjectBinding{}, "", false
	}
	return p, strings.Join(args[1:], " "), true
}

func (s *Service) resolveProjectByChannel(channelID string) (ProjectBinding, bool) {
	if strings.TrimSpace(channelID) == "" {
		return ProjectBinding{}, false
	}

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for _, p := range s.state.Projects {
		if p.ChannelID == channelID {
			return p, true
		}
	}

	if s.dg == nil {
		return ProjectBinding{}, false
	}

	ch, err := s.dg.Channel(channelID)
	if err != nil || ch == nil || strings.TrimSpace(ch.ParentID) == "" {
		return ProjectBinding{}, false
	}
	for _, p := range s.state.Projects {
		if p.ChannelID == ch.ParentID {
			return p, true
		}
	}
	return ProjectBinding{}, false
}

func (s *Service) resolveProjectByRef(ref string) (string, ProjectBinding, bool) {
	needle := strings.TrimSpace(strings.ToLower(ref))
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for path, p := range s.state.Projects {
		if strings.EqualFold(path, ref) || strings.EqualFold(p.RepoName, ref) || strings.EqualFold(slug(p.RepoName), needle) {
			return path, p, true
		}
	}
	return "", ProjectBinding{}, false
}

func (s *Service) ensureControlChannel() (string, error) {
	s.stateMu.RLock()
	existingID := s.state.ControlChannelID
	s.stateMu.RUnlock()
	if existingID != "" {
		if _, err := s.dg.Channel(existingID); err == nil {
			return existingID, nil
		}
	}

	channels, err := s.dg.GuildChannels(s.cfg.GuildID)
	if err != nil {
		return "", err
	}
	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildText && ch.Name == s.cfg.ControlChannelName {
			s.stateMu.Lock()
			s.state.ControlChannelID = ch.ID
			s.stateMu.Unlock()
			if saveErr := s.saveState(); saveErr != nil {
				log.Printf("save discord state after finding control channel failed: %v", saveErr)
			}
			return ch.ID, nil
		}
	}

	ch, err := s.dg.GuildChannelCreate(s.cfg.GuildID, s.cfg.ControlChannelName, discordgo.ChannelTypeGuildText)
	if err != nil {
		return "", err
	}
	s.stateMu.Lock()
	s.state.ControlChannelID = ch.ID
	s.stateMu.Unlock()
	if saveErr := s.saveState(); saveErr != nil {
		log.Printf("save discord state after creating control channel failed: %v", saveErr)
	}
	s.send(ch.ID, "Engine Discord control plane ready. Use !help.")
	return ch.ID, nil
}

func (s *Service) ensureProjectChannel(name string) (*discordgo.Channel, error) {
	if s.dg == nil {
		return nil, fmt.Errorf("discord session not ready")
	}
	channels, err := s.dg.GuildChannels(s.cfg.GuildID)
	if err != nil {
		return nil, err
	}
	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildText && ch.Name == name {
			return ch, nil
		}
	}
	return s.dg.GuildChannelCreate(s.cfg.GuildID, name, discordgo.ChannelTypeGuildText)
}

func (s *Service) send(channelID, msg string) {
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(msg) == "" {
		return
	}
	if s.testSendHook != nil {
		s.testSendHook(channelID, msg)
		return
	}
	if s.dg == nil {
		return
	}
	if _, err := s.dg.ChannelMessageSend(channelID, msg); err != nil {
		log.Printf("[engine-discord] send error: %v", err)
		return
	}
	s.recordOutbound(channelID, msg, "message", "")
}

// handleIssuesCommand lists GitHub issues Engine is currently assigned to.
// Requires GITHUB_TOKEN or ENGINE_GITHUB_BOT_TOKEN to be set.
func (s *Service) handleIssuesCommand(m *discordgo.MessageCreate, args []string) {
	login := githubEngineLoginFn()
	if login == "" {
		s.send(m.ChannelID, "Engine GitHub identity not configured. Set ENGINE_GITHUB_BOT_TOKEN + ENGINE_GITHUB_LOGIN (or GITHUB_TOKEN).")
		return
	}

	s.stateMu.RLock()
	projects := make([]ProjectBinding, 0, len(s.state.Projects))
	for _, p := range s.state.Projects {
		projects = append(projects, p)
	}
	s.stateMu.RUnlock()

	if len(projects) == 0 {
		s.send(m.ChannelID, "No projects enrolled — nothing to show.")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Issues assigned to **@%s** across enrolled projects:\n", login)
	found := false
	for _, p := range projects {
		owner, repo, ok := parseGitHubOwnerRepo(extractGitHubURL(p.ProjectPath, p.RepoName))
		if !ok {
			owner, repo = "", p.RepoName
		}
		if owner == "" {
			continue
		}
		issues, err := githubListAssignedFn(owner, repo, login)
		if err != nil {
			fmt.Fprintf(&b, "- **%s/%s**: error fetching issues (%v)\n", owner, repo, err)
			continue
		}
		if len(issues) == 0 {
			continue
		}
		for _, iss := range issues {
			found = true
			labels := make([]string, 0, len(iss.Labels))
			for _, l := range iss.Labels {
				labels = append(labels, l.Name)
			}
			labelStr := ""
			if len(labels) > 0 {
				labelStr = " [" + strings.Join(labels, ", ") + "]"
			}
			fmt.Fprintf(&b, "- **%s/%s** #%d%s: %s\n  %s\n", owner, repo, iss.Number, labelStr, iss.Title, iss.HTMLURL)
		}
	}
	if !found {
		b.WriteString("_(none found)_\n")
	}
	s.send(m.ChannelID, truncate(b.String(), maxDiscordMessageChars))
}

// handleIdentityCommand shows Engine's current GitHub persona.
func (s *Service) handleIdentityCommand(m *discordgo.MessageCreate) {
	login := githubEngineLoginFn()
	token := githubEngineTokenFn()
	tokenStatus := "not set"
	if token != "" {
		tokenStatus = "set (" + strconv.Itoa(len(token)) + " chars)"
	}
	if login == "" {
		login = "(not resolved — set ENGINE_GITHUB_LOGIN or ENGINE_GITHUB_BOT_TOKEN)"
	}
	projectNum := githubProjectNumberFn()
	projectLine := ""
	if projectNum > 0 {
		projectLine = fmt.Sprintf("\n- **Project board:** #%d", projectNum)
	}
	s.send(m.ChannelID, fmt.Sprintf(
		"Engine GitHub identity:\n- **Login:** @%s\n- **Token:** %s%s",
		login, tokenStatus, projectLine,
	))
}

// injectable shims so tests can mock GitHub API calls.
var (
	githubEngineLoginFn   = func() string { return engineLoginFn() }
	githubEngineTokenFn   = func() string { return engineTokenFn() }
	githubProjectNumberFn = func() int { return projectNumberFn() }
	githubListAssignedFn  = listAssignedIssues
)

// engineLoginFn / engineTokenFn / projectNumberFn forward to the github package
// without creating a hard import cycle. They are set via the github package's
// exported helpers in service_bridge.go.
var (
	engineLoginFn   func() string
	engineTokenFn   func() string
	projectNumberFn func() int
)

// extractGitHubURL tries to derive a "owner/repo" GitHub URL from the local
// clone path or the repo name hint.
func extractGitHubURL(projectPath, repoName string) string {
	// Try to read remote.origin.url from the local clone.
	out, err := gitRemoteOriginFn(projectPath)
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	return repoName
}

// gitRemoteOriginFn reads git remote origin url; injectable for tests.
var gitRemoteOriginFn = func(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	return string(out), err
}

// listAssignedIssues returns open issues in owner/repo assigned to login.
func listAssignedIssues(owner, repo, login string) ([]githubIssue, error) {
	c, err := newGitHubClientFn(owner, repo)
	if err != nil {
		return nil, err
	}
	return c.listIssuesAssignedTo(login)
}

// githubIssue is a minimal mirror of github.Issue to avoid a package import.
type githubIssue struct {
	Number  int
	Title   string
	HTMLURL string
	Labels  []struct{ Name string }
}

// newGitHubClientFn is injectable; default builds a client via GITHUB_TOKEN.
var newGitHubClientFn = defaultGitHubClientFn

type issueListClient interface {
	listIssuesAssignedTo(login string) ([]githubIssue, error)
}

type defaultIssueClient struct{ owner, repo, token string }

func (c *defaultIssueClient) listIssuesAssignedTo(login string) ([]githubIssue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=open&assignee=%s&per_page=30",
		c.owner, c.repo, login)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list issues: %d", resp.StatusCode)
	}

	var raw []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		PullRequest *struct{} `json:"pull_request,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var out []githubIssue
	for _, r := range raw {
		if r.PullRequest != nil {
			continue // skip PRs
		}
		iss := githubIssue{Number: r.Number, Title: r.Title, HTMLURL: r.HTMLURL}
		for _, l := range r.Labels {
			iss.Labels = append(iss.Labels, struct{ Name string }{l.Name})
		}
		out = append(out, iss)
	}
	return out, nil
}

func defaultGitHubClientFn(owner, repo string) (issueListClient, error) {
	tok := os.Getenv("ENGINE_GITHUB_BOT_TOKEN")
	if tok == "" {
		tok = os.Getenv("GITHUB_TOKEN")
	}
	if tok == "" {
		return nil, fmt.Errorf("no GitHub token configured")
	}
	return &defaultIssueClient{owner: owner, repo: repo, token: tok}, nil
}

func (s *Service) sendHelp(channelID string) {
	help := strings.Join([]string{
		"Engine Discord commands:",
		"- !help",
		"- !project add <path>",
		"- !project list",
		"- !project remove <name>",
		"- !status [project]",
		"- !sessions [project]",
		"- !lastcommit [project]",
		"- !pause [project]   — pause active orchestrator at next checkpoint",
		"- !resume [project]  — resume a paused orchestrator",
		"- !stop [project]    — stop the active orchestrator entirely",
		"- !redirect [project] <instruction> — inject a new instruction the orchestrator picks up next step",
		"- !plan [project]    — show the current orchestration plan (.engine/plan.md)",
		"- !orchestrators     — list projects with active orchestrators",
		"- !issues            — list GitHub issues Engine is currently assigned to",
		"- !identity          — show Engine's GitHub bot persona",
		"- Chat normally in a project channel to run the agent",
		"- !ask <prompt> (explicit prompt command, optional)",
		"- !ask <project> <prompt> (from control channel)",
		"- !auto <prompt> — engage autonomous mode for that prompt (alias: !autonomous, !build)",
		"- !search <query> — full-text search across this project's history",
		"- !history [hours] — recent messages in this project channel (default 24h)",
	}, "\n")
	s.send(channelID, help)
}

func (s *Service) stateFile() string {
	// Refuse to resolve a relative state-file path. An empty StoragePath would
	// land the state file in the process cwd — that pollutes source trees
	// during tests (and any production cwd Engine happens to be launched from),
	// and the dirty mtime invalidates the Go test cache on every run.
	storage := strings.TrimSpace(s.cfg.StoragePath)
	if storage == "" {
		return ""
	}
	return filepath.Join(storage, defaultStateFileName)
}

func (s *Service) loadState() error {
	path := s.stateFile()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read discord state: %w", err)
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("parse discord state: %w", err)
	}
	if st.Projects == nil {
		st.Projects = make(map[string]ProjectBinding)
	}
	if migrated, changed := s.migrateProjectBindings(st); changed {
		st = migrated
		migratedData, _ := json.MarshalIndent(st, "", "  ")
		if writeErr := os.WriteFile(path, migratedData, 0600); writeErr != nil {
			log.Printf("discord: failed to persist migrated state %s: %v", path, writeErr)
		}
	}
	s.state = st
	return nil
}

func (s *Service) migrateProjectBindings(st persistedState) (persistedState, bool) {
	if len(st.Projects) == 0 {
		return st, false
	}
	clonesDir := resolveClonesDir(s.project)
	workspaceRoot := strings.TrimSpace(s.project)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	prefixes := []string{
		filepath.Join(workspaceRoot, "engine", "projects") + string(os.PathSeparator),
		filepath.Join(workspaceRoot, ".engine", "projects") + string(os.PathSeparator),
		filepath.Join(workspaceRoot, "packages", "server-go", ".engine", "projects") + string(os.PathSeparator),
	}

	next := make(map[string]ProjectBinding, len(st.Projects))
	changed := false
	for key, binding := range st.Projects {
		bindingPath := strings.TrimSpace(binding.ProjectPath)
		migratedPath, ok := migrateBindingPath(bindingPath, clonesDir, prefixes)
		if ok {
			changed = true
			binding.ProjectPath = migratedPath
			next[migratedPath] = binding
			if key != migratedPath {
				log.Printf("discord: migrated project binding path from %s to %s", key, migratedPath)
			}
			continue
		}
		next[key] = binding
	}

	if !changed {
		return st, false
	}
	st.Projects = next
	return st, true
}

func migrateBindingPath(pathValue, clonesDir string, workspacePrefixes []string) (string, bool) {
	if strings.TrimSpace(pathValue) == "" {
		return "", false
	}
	abs := filepath.Clean(pathValue)
	for _, prefix := range workspacePrefixes {
		if !strings.HasPrefix(abs, prefix) {
			continue
		}
		repo := filepath.Base(abs)
		if strings.TrimSpace(repo) == "" || repo == "." || repo == string(os.PathSeparator) {
			return "", false
		}
		candidate := filepath.Join(clonesDir, repo)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, true
		}
		log.Printf("discord: workspace project binding %s detected but runtime clone %s not found; leaving unchanged", abs, candidate)
		return "", false
	}
	return "", false
}

func (s *Service) saveState() error {
	path := s.stateFile()
	if path == "" {
		return nil
	}
	s.stateMu.RLock()
	data, _ := json.MarshalIndent(s.state, "", "  ")
	s.stateMu.RUnlock()
	return os.WriteFile(path, data, 0600)
}

func (s *Service) isAllowedUser(userID string) bool {
	_, ok := s.cfg.AllowedUsers[userID]
	return ok
}

func ensureSession(projectPath string) (string, error) {
	sessions, err := db.ListSessions(projectPath)
	if err == nil && len(sessions) > 0 {
		return sessions[0].ID, nil
	}
	id := fmt.Sprintf("discord-%d", time.Now().UnixNano())
	branch, _ := gogit.GetCurrentBranch(projectPath)
	if err := db.CreateSession(id, projectPath, branch); err != nil {
		return "", err
	}
	return id, nil
}

func parseCommand(content string, prefix string) (string, []string, bool) {
	line := strings.TrimSpace(content)
	if line == "" {
		return "", nil, false
	}
	if !strings.HasPrefix(line, prefix) {
		return "", nil, false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if line == "" {
		return "", nil, false
	}
	parts := strings.Fields(line)
	cmd := strings.ToLower(parts[0])
	args := []string{}
	if len(parts) > 1 {
		args = parts[1:]
	}
	return cmd, args, true
}

func slug(name string) string {
	value := strings.TrimSpace(strings.ToLower(name))
	if value == "" {
		return "project"
	}
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		isAlpha := r >= 'a' && r <= 'z'
		isNum := r >= '0' && r <= '9'
		if isAlpha || isNum {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(out.String(), "-")
	if result == "" {
		return "project"
	}
	return result
}

func splitForDiscord(text string, max int) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return []string{"(empty response)"}
	}
	if len(trimmed) <= max {
		return []string{trimmed}
	}

	parts := make([]string, 0)
	rest := trimmed
	for len(rest) > max {
		cut := strings.LastIndex(rest[:max], "\n")
		if cut < max/4 {
			cut = max
		}
		parts = append(parts, strings.TrimSpace(rest[:cut]))
		rest = strings.TrimSpace(rest[cut:])
	}
	if rest != "" {
		parts = append(parts, rest)
	}
	return parts
}

func parseCSVSet(raw string) map[string]bool {
	return parseSliceSet(strings.Split(raw, ","))
}

func parseSliceSet(values []string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range values {
		v := strings.TrimSpace(part)
		if v != "" {
			result[v] = true
		}
	}
	return result
}

func stateDir(projectPath string) string {
	if override := strings.TrimSpace(os.Getenv("ENGINE_STATE_DIR")); override != "" {
		return override
	}
	if strings.TrimSpace(projectPath) != "" {
		return filepath.Join(projectPath, ".engine")
	}
	configDir, _ := os.UserConfigDir()
	return filepath.Join(configDir, "Engine")
}

func shortID(id string) string {
	value := strings.TrimSpace(id)
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func ternary[T any](condition bool, a T, b T) T {
	if condition {
		return a
	}
	return b
}

// ── Public API for WS / UI ────────────────────────────────────────────────

// ValidationResult summarizes a Discord configuration preflight check.
type ValidationResult struct {
	OK        bool     `json:"ok"`
	Enabled   bool     `json:"enabled"`
	GuildName string   `json:"guildName,omitempty"`
	BotTag    string   `json:"botTag,omitempty"`
	InviteURL string   `json:"inviteUrl,omitempty"`
	Errors    []string `json:"errors"`
	Warnings  []string `json:"warnings"`
}

func buildInviteURL(clientID string) string {
	cleanID := strings.TrimSpace(clientID)
	if cleanID == "" {
		return ""
	}
	return fmt.Sprintf(
		"https://discord.com/api/oauth2/authorize?client_id=%s&permissions=%d&scope=bot%%20applications.commands",
		cleanID,
		requiredInvitePerms,
	)
}

// Validate runs a non-destructive preflight against the given configuration.
// It opens a short-lived session (if needed), verifies the token, guild
// access, and resolves allowed users. Nothing is persisted.
func Validate(cfg Config) ValidationResult {
	result := ValidationResult{
		Enabled:  cfg.Enabled,
		Errors:   []string{},
		Warnings: []string{},
	}
	if !cfg.Enabled {
		result.OK = true
		result.Warnings = append(result.Warnings, "Discord is disabled in config.")
		return result
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		result.Errors = append(result.Errors, "Bot token is empty.")
	}
	if strings.TrimSpace(cfg.GuildID) == "" {
		result.Errors = append(result.Errors, "Guild ID is empty.")
	}
	if len(cfg.AllowedUsers) == 0 {
		result.Errors = append(result.Errors, "At least one allowed user ID is required.")
	}
	if len(result.Errors) > 0 {
		return result
	}

	dg, _ := discordgo.New("Bot " + cfg.BotToken)
	// Use the same privileged intents as Start() so that a missing
	// "Message Content Intent" in the Developer Portal surfaces here
	// (as a 4014 error) rather than silently passing validate while
	// Start() later fails and leaves the bot offline.
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	if err := dg.Open(); err != nil {
		result.Errors = append(result.Errors, "Cannot open gateway: "+err.Error())
		return result
	}
	defer func() {
		if err := dg.Close(); err != nil {
			log.Printf("discord: validate close failed: %v", err)
		}
	}()

	if self, err := dg.User("@me"); err == nil {
		result.BotTag = self.Username
		result.InviteURL = buildInviteURL(self.ID)
	}

	guild, err := dg.Guild(cfg.GuildID)
	if err != nil {
		result.Errors = append(result.Errors, "Cannot access guild "+cfg.GuildID+": "+err.Error())
		return result
	}
	result.GuildName = guild.Name

	// Resolve each allowed user against the guild.
	for userID := range cfg.AllowedUsers {
		if _, err := dg.GuildMember(cfg.GuildID, userID); err != nil {
			result.Warnings = append(result.Warnings, "Allowed user not in guild: "+userID)
		}
	}

	result.OK = len(result.Errors) == 0
	return result
}

// Reload swaps in a new configuration and reopens the gateway if needed.
// Callers must hold no references to the prior session.
func (s *Service) Reload(cfg Config) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if sameConfig(s.cfg, cfg) {
		// No runtime/config drift, keep current gateway session untouched.
		s.cfg = cfg
		return nil
	}

	if s.dg != nil {
		if err := s.dg.Close(); err != nil {
			log.Printf("discord: close existing gateway session failed during reload: %v", err)
		}
		s.dg = nil
	}
	s.cfg = cfg
	if cfg.Enabled {
		if err := s.loadState(); err != nil {
			return err
		}

		// Inline Start() logic while holding lifecycle lock to prevent concurrent
		// reload/start races from creating repeated gateway sessions.
		if len(s.cfg.BotToken) < 50 {
			return fmt.Errorf("discord bot token is too short (expected ~70 chars, got %d)", len(s.cfg.BotToken))
		}

		dg, _ := discordgo.New("Bot " + s.cfg.BotToken)
		dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
		dg.ShouldReconnectOnError = false
		dg.AddHandler(s.onReady)
		dg.AddHandler(s.onMessage)

		s.dg = dg
		if err := dg.Open(); err != nil {
			s.dg = nil
			return fmt.Errorf("discord open: %w", err)
		}
		log.Printf("[engine-discord] connected (guild=%s, prefix=%s)", s.cfg.GuildID, s.cfg.CommandPrefix)
		return nil
	}
	return nil
}

func sameConfig(a, b Config) bool {
	if a.Enabled != b.Enabled {
		return false
	}
	if strings.TrimSpace(a.BotToken) != strings.TrimSpace(b.BotToken) {
		return false
	}
	if strings.TrimSpace(a.GuildID) != strings.TrimSpace(b.GuildID) {
		return false
	}
	if strings.TrimSpace(a.CommandPrefix) != strings.TrimSpace(b.CommandPrefix) {
		return false
	}
	if strings.TrimSpace(a.ControlChannelName) != strings.TrimSpace(b.ControlChannelName) {
		return false
	}
	if len(a.AllowedUsers) != len(b.AllowedUsers) {
		return false
	}
	for id := range a.AllowedUsers {
		if !b.AllowedUsers[id] {
			return false
		}
	}
	return true
}

// SearchHistory is the public entry for WS/agent callers. It enforces bounded
// output so the full archive is never dumped into an LLM context.
func (s *Service) SearchHistory(projectPath, query, since string, limit int) ([]db.DiscordSearchHit, error) {
	return db.DiscordSearchMessages(projectPath, query, since, limit)
}

// RecentHistory returns recent archived messages scoped to a project and/or
// thread. Bounded and paginated by caller.
func (s *Service) RecentHistory(projectPath, threadID, since string, limit int) ([]db.DiscordMessage, error) {
	return db.DiscordListRecentMessages(projectPath, threadID, since, limit)
}

// CurrentConfig returns a copy of the active configuration with secrets masked.
func (s *Service) CurrentConfig() Config {
	return s.cfg
}

// WriteConfig persists a configuration to the project-local config file.
// This is the single source of truth consumed by LoadConfig on restart.
func WriteConfig(projectPath string, cfg Config) error {
	path := configFilePath(projectPath)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	allowed := make([]string, 0, len(cfg.AllowedUsers))
	for id := range cfg.AllowedUsers {
		allowed = append(allowed, id)
	}
	sort.Strings(allowed)
	enabled := cfg.Enabled
	payload := fileConfig{
		Enabled:            &enabled,
		BotToken:           strings.TrimSpace(cfg.BotToken),
		GuildID:            strings.TrimSpace(cfg.GuildID),
		AllowedUserIDs:     allowed,
		CommandPrefix:      strings.TrimSpace(cfg.CommandPrefix),
		ControlChannelName: strings.TrimSpace(cfg.ControlChannelName),
		StoragePath:        strings.TrimSpace(cfg.StoragePath),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return os.WriteFile(path, data, 0600)
}
