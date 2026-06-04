package discord

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/engine/server/ai"
)

// orchestratorControlFns are the live hooks into the running orchestrator.
// Bound at package init to the ai package functions; tests can swap them.
var (
	orchestratorHandleFn = ai.GetOrchestratorHandle
	orchestratorListFn   = ai.ListOrchestratorProjects
)

// handleStopCommand stops the orchestrator running for the bound project.
// Usage: !stop [project]
func (s *Service) handleStopCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use this command in a project channel or pass a project name.")
		return
	}
	h := orchestratorHandleFn(binding.ProjectPath)
	if h == nil {
		s.send(m.ChannelID, fmt.Sprintf("No active orchestrator for **%s**. Nothing to stop.", binding.RepoName))
		return
	}
	h.Stop()
	s.send(m.ChannelID, fmt.Sprintf("🛑 Stopping orchestrator for **%s** at next checkpoint.", binding.RepoName))
}

// handleRedirectCommand injects a new instruction into the orchestrator.
// The instruction is prepended to the next builder step.
// Usage: !redirect <instruction>
//
//	!redirect <project> <instruction>   (from control channel)
func (s *Service) handleRedirectCommand(m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 {
		s.send(m.ChannelID, "Usage: !redirect <instruction>   or   !redirect <project> <instruction> (from control channel)")
		return
	}
	binding, instruction, ok := s.resolveAskTarget(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use this command in a project channel or include project name.")
		return
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		s.send(m.ChannelID, "Instruction is required.")
		return
	}
	h := orchestratorHandleFn(binding.ProjectPath)
	if h == nil {
		s.send(m.ChannelID, fmt.Sprintf("No active orchestrator for **%s** — instruction not queued.", binding.RepoName))
		return
	}
	h.Redirect(instruction)
	s.send(m.ChannelID, fmt.Sprintf("📨 Queued redirect for **%s**: %s", binding.RepoName, truncate(instruction, 200)))
}

// handlePlanCommand prints the current orchestration plan for the project.
// Usage: !plan [project]
func (s *Service) handlePlanCommand(m *discordgo.MessageCreate, args []string) {
	binding, ok := s.resolveProjectForMessage(m.ChannelID, args)
	if !ok {
		s.send(m.ChannelID, "Project not found. Use this command in a project channel or pass a project name.")
		return
	}
	planPath := filepath.Join(binding.ProjectPath, ".engine", "plan.md")
	data, err := os.ReadFile(planPath)
	if err != nil || len(data) == 0 {
		s.send(m.ChannelID, fmt.Sprintf("No plan recorded yet for **%s**.", binding.RepoName))
		return
	}
	body := string(data)
	if len(body) > maxDiscordMessageChars-30 {
		body = body[:maxDiscordMessageChars-30] + "\n…(truncated)"
	}
	s.send(m.ChannelID, "```\n"+body+"\n```")
}

// handleOrchestratorsCommand lists projects with active orchestrators.
// Usage: !orchestrators   (control-plane visibility)
func (s *Service) handleOrchestratorsCommand(m *discordgo.MessageCreate, _ []string) {
	projects := orchestratorListFn()
	if len(projects) == 0 {
		s.send(m.ChannelID, "No active orchestrators.")
		return
	}
	var b strings.Builder
	b.WriteString("Active orchestrators:\n")
	for _, p := range projects {
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	s.send(m.ChannelID, b.String())
}

// applyPauseToOrchestrator wires the existing !pause / !resume commands into
// the live orchestrator handle. The original handlePauseResume only flips a
// state flag; this complements it by also telling a running orchestrator to
// pause at the next checkpoint.
func (s *Service) applyPauseToOrchestrator(projectPath string, pause bool) {
	h := orchestratorHandleFn(projectPath)
	if h == nil {
		return
	}
	if pause {
		h.Pause()
	} else {
		h.Resume()
	}
}
