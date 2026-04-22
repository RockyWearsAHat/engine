package discord

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	maxDiscordMessageChars = 1800
)

// Config controls Discord bot behavior.
type Config struct {
	Enabled            bool
	BotToken           string
	GuildID            string
	AllowedUsers       map[string]bool
	CommandPrefix      string
	ControlChannelName string
	StoragePath        string
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

// Service hosts the Discord control plane.
type Service struct {
	cfg      Config
	project  string
	dg       *discordgo.Session
	stateMu  sync.RWMutex
	state    persistedState
	activeMu sync.Mutex
	active   map[string]bool
}

// LoadConfig reads Discord configuration from env vars.
func LoadConfig(projectPath string) (Config, error) {
	cfg := Config{
		Enabled:            strings.TrimSpace(os.Getenv("ENGINE_DISCORD")) == "1",
		BotToken:           strings.TrimSpace(os.Getenv("ENGINE_DISCORD_BOT_TOKEN")),
		GuildID:            strings.TrimSpace(os.Getenv("ENGINE_DISCORD_GUILD_ID")),
		AllowedUsers:       parseCSVSet(os.Getenv("ENGINE_DISCORD_ALLOWED_USER_IDS")),
		CommandPrefix:      strings.TrimSpace(os.Getenv("ENGINE_DISCORD_PREFIX")),
		ControlChannelName: strings.TrimSpace(os.Getenv("ENGINE_DISCORD_CONTROL_CHANNEL")),
		StoragePath:        filepath.Join(stateDir(projectPath), "discord"),
	}

	if cfg.CommandPrefix == "" {
		cfg.CommandPrefix = defaultCommandPrefix
	}
	if cfg.ControlChannelName == "" {
		cfg.ControlChannelName = defaultControlChanName
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if cfg.BotToken == "" {
		return cfg, fmt.Errorf("ENGINE_DISCORD_BOT_TOKEN is required when ENGINE_DISCORD=1")
	}
	if cfg.GuildID == "" {
		return cfg, fmt.Errorf("ENGINE_DISCORD_GUILD_ID is required for private-server mode")
	}
	if len(cfg.AllowedUsers) == 0 {
		return cfg, fmt.Errorf("ENGINE_DISCORD_ALLOWED_USER_IDS must include at least one Discord user id")
	}
	if err := os.MkdirAll(cfg.StoragePath, 0700); err != nil {
		return cfg, fmt.Errorf("create discord storage: %w", err)
	}
	return cfg, nil
}

// NewService creates a Discord service.
func NewService(cfg Config, projectPath string) (*Service, error) {
	s := &Service{
		cfg:     cfg,
		project: projectPath,
		state: persistedState{
			Projects: make(map[string]ProjectBinding),
		},
		active: make(map[string]bool),
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
	if !s.cfg.Enabled {
		return nil
	}

	dg, err := discordgo.New("Bot " + s.cfg.BotToken)
	if err != nil {
		return fmt.Errorf("discord session: %w", err)
	}

	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	dg.AddHandler(s.onReady)
	dg.AddHandler(s.onMessage)

	if err := dg.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}
	s.dg = dg
	log.Printf("[engine-discord] connected (guild=%s, prefix=%s)", s.cfg.GuildID, s.cfg.CommandPrefix)
	return nil
}

// Close shuts down the Discord session.
func (s *Service) Close() error {
	if s.dg == nil {
		return nil
	}
	return s.dg.Close()
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

	cmd, args, ok := parseCommand(m.Content, s.cfg.CommandPrefix)
	if !ok {
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
	case "ask":
		s.handleAskCommand(m, args)
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
	abs, err := filepath.Abs(clean)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("path must be an existing directory")
	}

	repo := filepath.Base(abs)
	channelName := "proj-" + slug(repo)
	ch, err := s.ensureProjectChannel(channelName)
	if err != nil {
		return err
	}

	s.stateMu.Lock()
	s.state.Projects[abs] = ProjectBinding{
		ProjectPath: abs,
		RepoName:    repo,
		ChannelID:   ch.ID,
		Paused:      false,
	}
	s.stateMu.Unlock()
	if err := s.saveState(); err != nil {
		return err
	}

	branch, _ := gogit.GetCurrentBranch(abs)
	s.send(replyChannelID, fmt.Sprintf("Project enrolled: %s\\nChannel: <#%s>\\nBranch: %s", abs, ch.ID, branch))
	s.send(ch.ID, "Engine linked to this project channel. Commands: !status !sessions !lastcommit !pause !resume !ask <prompt>")
	return nil
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
		return err
	}

	s.send(replyChannelID, fmt.Sprintf("Removed project %s (channel kept: <#%s>).", binding.RepoName, binding.ChannelID))
	return nil
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
		b.WriteString(fmt.Sprintf("- %s (%s) in <#%s>\n", p.RepoName, state, p.ChannelID))
	}
	s.send(channelID, b.String())
}

func (s *Service) handleStatusCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use this command in a project channel or pass a project name.")
		return
	}

	status, err := gogit.GetStatus(binding.ProjectPath)
	if err != nil {
		s.send(m.ChannelID, "Status error: "+err.Error())
		return
	}
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
	s.send(m.ChannelID, msg)
}

func (s *Service) handleSessionsCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found.")
		return
	}

	sessions, err := db.ListSessions(binding.ProjectPath)
	if err != nil {
		s.send(m.ChannelID, "Session lookup failed: "+err.Error())
		return
	}
	if len(sessions) == 0 {
		s.send(m.ChannelID, "No sessions yet for this project.")
		return
	}

	limit := len(sessions)
	if limit > 5 {
		limit = 5
	}
	var b strings.Builder
	b.WriteString("Recent sessions:\n")
	for i := 0; i < limit; i++ {
		sess := sessions[i]
		summary := strings.TrimSpace(sess.Summary)
		if summary == "" {
			summary = "(no summary yet)"
		}
		if len(summary) > 120 {
			summary = summary[:117] + "..."
		}
		b.WriteString(fmt.Sprintf("- %s | msgs=%d | %s\\n", shortID(sess.ID), sess.MessageCount, summary))
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
		s.send(m.ChannelID, "Failed to update project state: "+err.Error())
		return
	}

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
	if binding.Paused {
		s.send(m.ChannelID, "Project is paused. Run !resume first.")
		return
	}

	s.activeMu.Lock()
	if s.active[binding.ProjectPath] {
		s.activeMu.Unlock()
		s.send(m.ChannelID, "A task is already running for this project. Wait for it to finish.")
		return
	}
	s.active[binding.ProjectPath] = true
	s.activeMu.Unlock()

	s.send(m.ChannelID, "Running agent request...")
	go func() {
		defer func() {
			s.activeMu.Lock()
			delete(s.active, binding.ProjectPath)
			s.activeMu.Unlock()
		}()

		sessionID, err := ensureSession(binding.ProjectPath)
		if err != nil {
			s.send(m.ChannelID, "Failed to open session: "+err.Error())
			return
		}

		var outMu sync.Mutex
		var output strings.Builder
		var lastErr string

		cancel := make(chan struct{})
		defer close(cancel)

		ai.Chat(&ai.ChatContext{
			ProjectPath: binding.ProjectPath,
			SessionID:   sessionID,
			Cancel:      cancel,
			OnChunk: func(content string, done bool) {
				if done || strings.TrimSpace(content) == "" {
					return
				}
				outMu.Lock()
				output.WriteString(content)
				outMu.Unlock()
			},
			OnToolCall: func(_ string, _ interface{}) {},
			OnToolResult: func(_ string, _ interface{}, _ bool) {},
			OnError: func(err string) {
				lastErr = strings.TrimSpace(err)
			},
			OnSessionUpdated: func(_ *db.Session) {},
			RequestApproval: func(kind, title, message, command string) (bool, error) {
				_ = kind
				_ = title
				_ = message
				_ = command
				return false, fmt.Errorf("approval required but unavailable in discord command mode")
			},
		}, prompt)

		if strings.TrimSpace(lastErr) != "" {
			s.send(m.ChannelID, "Agent error: "+lastErr)
			return
		}

		outMu.Lock()
		answer := strings.TrimSpace(output.String())
		outMu.Unlock()
		if answer == "" {
			answer = "(No response text returned.)"
		}

		for _, part := range splitForDiscord(answer, maxDiscordMessageChars) {
			s.send(m.ChannelID, part)
		}
	}()
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
			_ = s.saveState()
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
	if err := s.saveState(); err != nil {
		return "", err
	}
	s.send(ch.ID, "Engine Discord control plane ready. Use !help.")
	return ch.ID, nil
}

func (s *Service) ensureProjectChannel(name string) (*discordgo.Channel, error) {
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
	if s.dg == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	if strings.TrimSpace(msg) == "" {
		return
	}
	if _, err := s.dg.ChannelMessageSend(channelID, msg); err != nil {
		log.Printf("[engine-discord] send error: %v", err)
	}
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
		"- !pause [project]",
		"- !resume [project]",
		"- !ask <prompt> (inside project channel)",
		"- !ask <project> <prompt> (from control channel)",
	}, "\n")
	s.send(channelID, help)
}

func (s *Service) stateFile() string {
	return filepath.Join(s.cfg.StoragePath, "control-plane.json")
}

func (s *Service) loadState() error {
	path := s.stateFile()
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
	s.state = st
	return nil
}

func (s *Service) saveState() error {
	s.stateMu.RLock()
	data, err := json.MarshalIndent(s.state, "", "  ")
	s.stateMu.RUnlock()
	if err != nil {
		return fmt.Errorf("serialize discord state: %w", err)
	}
	return os.WriteFile(s.stateFile(), data, 0600)
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
	if summary := ai.BuildInitialSessionSummary(projectPath); summary != "" {
		db.UpdateSessionSummary(id, summary) //nolint:errcheck
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
	if len(parts) == 0 {
		return "", nil, false
	}
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
	result := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
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
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "Engine")
	}
	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".engine")
	}
	if strings.TrimSpace(projectPath) != "" {
		return filepath.Join(projectPath, ".engine")
	}
	return filepath.Join(".", ".engine")
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
